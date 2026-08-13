// Помічник реінвесту: що купити на вільні гроші.
//
// Порівнює три інструменти між собою за РЕАЛЬНОЮ дохідністю після податку
// й знецінення — саме вона, а не ціна, вирішує порядок.

package api

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	money "github.com/Rhymond/go-money"
)

// brokerFit — скільки таких паперів тягне конкретний брокер. Баланси
// роздільні, тож загальна сума нічого не каже: гривня на inzhur не
// купить папір у mono.
type brokerFit struct {
	Broker string `json:"broker"`
	Qty    int64  `json:"qty"`
}

// suggestion — одна пропозиція реінвесту. Спільна для трьох
// інструментів: облігація, сертифікат фонду, поповнення вкладу.
//
// Що робить їх порівнянними — RealPct: реальна річна дохідність після
// податку в СЬОГОДНІШНІЙ купівельній спроможності. Саме вона, а не
// ціна, вирішує порядок. Інакше сертифікат за 10 ₴ вічно був би
// «найкращим», просто бо найдешевший, а це не порада, а тавтологія.
type suggestion struct {
	// Kind — bond | fund | deposit. Label — те, що показуємо людині
	// (ISIN, назва фонду, «вклад <банк>»).
	Kind     string    `json:"kind"`
	Label    string    `json:"label"`
	ISIN     string    `json:"isin,omitempty"`
	Currency string    `json:"currency"`
	RatePct  string    `json:"rate_pct,omitempty"`
	Maturity string    `json:"maturity,omitempty"`
	Nominal  moneyJSON `json:"nominal,omitempty"`
	// CostPerBond — скільки коштує ОДИН крок: папір за номіналом плюс
	// НКД, один сертифікат за останньою ціною, або поповнення вкладу
	// на суму відкриття.
	CostPerBond moneyJSON `json:"cost_per_bond"`
	// YTMPct — дохідність до погашення (лише облігації).
	// RealPct — після податку й знецінення; порівнянна між усіма.
	// YieldBasis — з ЧОГО ця дохідність узята: обіцянка (до погашення,
	// ставка вкладу) чи оцінка (дивіденди фонду). Природа різна, і
	// ховати це за одним числом було б нечесно.
	YTMPct float64 `json:"ytm_pct,omitempty"`
	// NominalPct — дохідність ДО знецінення, для всіх трьох видів.
	// YTMPct поруч є лише в облігацій, тож без цього поля фонд і
	// вклад показувались у списку самою реальною, а папір — двома
	// числами, і порівняти їх по-номінальному було ні з чим.
	NominalPct    float64     `json:"nominal_pct,omitempty"`
	RealPct       float64     `json:"real_pct"`
	YieldBasis    string      `json:"yield_basis"`
	Brokers       []brokerFit `json:"brokers,omitempty"`
	Affordable    int64       `json:"affordable"`
	CanBuy        bool        `json:"can_buy"`
	Reason        string      `json:"reason"`
	DurationNow   float64     `json:"duration_now,omitempty"`
	DurationAfter float64     `json:"duration_after,omitempty"`
	// LastAuction / LastAuctionPct — коли цей самий папір востаннє
	// розміщували на аукціоні Мінфіну й під скільки. Порожньо означає, що
	// за все відоме нам вікно його не розміщували жодного разу, тобто
	// взяти його можна хіба на вторинному ринку.
	LastAuction    string  `json:"last_auction,omitempty"`
	LastAuctionPct float64 `json:"last_auction_pct,omitempty"`
	// stale — папір без розміщення за відоме вікно. Це твердження про
	// НАШУ ВПЕВНЕНІСТЬ, а не про якість паперу: ціна в цьому рядку
	// рахується як «номінал + НКД», тобто за первинним розміщенням, і
	// для паперу, якого рік не розміщували, така база — вигадка. Тому
	// такі рядки опускаються нижче, але не ховаються: чому саме ліміт
	// нічого не ховає, сказано в overLimit вище.
	stale bool
	// def — на скільки в.п. капіталу бракує ВАЛЮТИ цього паперу до цілі;
	// kindDef — те саме для ВИДУ інструмента. Одиниця в них однакова
	// (відсоткові пункти капіталу), тож у ранжуванні вони складаються:
	// покупка, що зрушує до політики за обома вимірами одразу, важить
	// більше за ту, що закриває лише один. Складати їх можна саме тому,
	// що знаменник тепер один на всіх (див. state.Capital).
	def       float64
	kindDef   float64
	ladderNom float64
	// overLimit — цей папір (або рік його погашення) уже перевищує заданий
	// ліміт концентрації. Порада НЕ ховається: ліміт міг бути порушений із
	// причин, яких застосунок не знає, і заборона тут була б порадою
	// навпаки. Але серед доступних такі опускаються нижче — це те, чого
	// користувач сам від себе хотів, коли ставив ліміт.
	//
	// Брокера сюди не включаємо: він не властивість паперу, той самий
	// папір можна купити в іншого, і мітка на рядку означала б, що
	// проблема в папері.
	overLimit bool
}

// withKindDef дописує до причини дефіцит за видом інструмента. Поріг
// 0.5 в.п. той самий, що й у валютного: менше — це шум округлення, а не
// привід щось радити.
func withKindDef(base string, def float64, what string) string {
	if def <= 0.5 {
		return base
	}
	return base + fmt.Sprintf("; добирає %s (%.0f в.п. до цілі)", what, def)
}

// staleAfterDays — за скільки днів без розміщення папір вважається таким,
// чиєї сьогоднішньої ціни ми не знаємо.
//
// Дорівнює вікну бекфілу аукціонів (52 тижні) НАВМИСНО: поріг, довший за
// дані, які ми взагалі тягнемо, був би заявою, яку нема чим підперти —
// ми б називали «несвіжим» те, про що просто не питали.
const staleAfterDays = 365

// handleReinvest — помічник реінвестиції: доступні папери з довідника,
// відранжовані під план (розрив до цільової частки — валютної й за видом
// інструмента разом → реальна дохідність → рік з «діркою» в драбині).
// Це інструмент, не порада.
//
// «Купонної ставки» в цьому переліку немає вже давно: сира ставка
// непорівнянна між валютами, і порядок задає реальна дохідність.
func (s *Server) handleReinvest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	today := domain.NewDate(now)
	doc, err := s.buildState(ctx, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	bonds, err := s.st.SearchBonds(ctx, "", "", today, "", 5000) // усі майбутні папери
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	target := map[string]float64{"USD": 0, "EUR": 0}
	if doc.Settings != nil {
		if doc.Settings.USDTargetSharePct != nil {
			target["USD"] = *doc.Settings.USDTargetSharePct
		}
		if doc.Settings.EURTargetSharePct != nil {
			target["EUR"] = *doc.Settings.EURTargetSharePct
		}
	}
	target["UAH"] = 100 - target["USD"] - target["EUR"]
	cur := map[string]float64{"USD": doc.USDSharePct, "EUR": doc.EURSharePct}
	cur["UAH"] = 100 - doc.USDSharePct - doc.EURSharePct

	// Виміри диверсифікації, зібрані в buildState, доводяться сюди — доти
	// вони жили самі по собі, і помічник про них не знав нічого: цілі за
	// видом інструмента стояли в налаштуваннях, а порядок порад від них
	// не змінювався.
	//
	// Беремо ГОТОВІ рядки зі стану, а не рахуємо заново: у цьому файлі вже
	// одного разу з'явилась власна копія арифметики часток, і саме через
	// неї плитка й картка показували різні числа.
	kindDef := map[string]float64{}  // вид інструмента → скільки в.п. бракує
	overISIN := map[string]float64{} // папір → на скільки в.п. перевищено
	overYear := map[int]float64{}    // рік погашень → те саме
	limitISIN, limitYear := 0.0, 0.0 // самі ліміти, для формулювання
	for _, r := range doc.Rebalance {
		if r.Dimension != "kind" || r.TargetPct <= 0 {
			continue
		}
		if d := r.TargetPct - r.CurrentPct; d > 0 {
			kindDef[r.Key] = d
		}
	}
	for _, c := range doc.Concentration {
		if c.OverUAH <= 0 {
			continue
		}
		switch c.Dimension {
		case "isin":
			overISIN[c.Key] = c.SharePct - c.LimitPct
			limitISIN = c.LimitPct
		case "year":
			if y, err := strconv.Atoi(c.Key); err == nil {
				overYear[y] = c.SharePct - c.LimitPct
				limitYear = c.LimitPct
			}
		}
	}

	ladderYear := map[int]map[string]float64{}
	for _, row := range doc.Ladder {
		ladderYear[row.Year] = map[string]float64{"UAH": row.UAH, "USD": row.USD, "EUR": row.EUR}
	}

	// Поточна дюрація/PV портфеля та цільова дюрація: для кожного паперу
	// рахуємо, куди зрушить дюрацію його купівля. Комбінована дюрація —
	// це середньозважена за приведеною вартістю, тож додавання паперу
	// рахується точно, без перебудови всього портфеля.
	var curMac, curPV float64
	if doc.RateRisk != nil {
		curMac, curPV = doc.RateRisk.DurationYears, doc.RateRisk.PVUAH
	}
	rank := "plan"
	if doc.Settings != nil && doc.Settings.ReinvestRank != "" {
		rank = doc.Settings.ReinvestRank
	}
	// Знецінення гривні: те саме припущення, що й у прогнозі, інакше
	// помічник радив би одне, а прогноз малював інше.
	devalPct := s.devaluation(ctx)
	rates, err := s.rates(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	isins := make([]string, 0, len(bonds))
	for _, b := range bonds {
		isins = append(isins, b.ISIN)
	}
	allPays, err := s.st.PaymentsFor(ctx, isins)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	paysByISIN := map[string][]domain.Payment{}
	for _, p := range allPays {
		paysByISIN[p.ISIN] = append(paysByISIN[p.ISIN], p)
	}
	// Останнє розміщення кожного паперу — одним запитом, не по одному на
	// ISIN: інакше відкриття «Огляду» коштувало б 187 звернень до бази.
	//
	// Помилка тут не фатальна: без аукціонів помічник працює рівно як
	// раніше. І поки їх немає взагалі (свіжа інсталяція, бекфіл ще в
	// фоні), про них не сказано НІ СЛОВА — написати «не розміщувався»
	// там, де ми просто не дивились, означало б видати незнання за факт.
	lastAuction, aerr := s.st.LastAuctionByISIN(ctx)
	if aerr != nil {
		lastAuction = nil
	}
	knowAuctions := len(lastAuction) > 0

	// fitsFor — скільки таких кроків тягне кожен брокер окремо. Баланси
	// роздільні: гривня на inzhur не купить папір у mono.
	fitsFor := func(c string, costMajor float64) ([]brokerFit, int64) {
		var fits []brokerFit
		var best int64
		if costMajor <= 0 {
			return nil, 0
		}
		for name, byCur := range doc.Brokers {
			if n := int64(byCur[c] / costMajor); n > 0 {
				fits = append(fits, brokerFit{Broker: name, Qty: n})
				if n > best {
					best = n
				}
			}
		}
		sort.Slice(fits, func(i, j int) bool {
			if fits[i].Qty != fits[j].Qty {
				return fits[i].Qty > fits[j].Qty
			}
			return fits[i].Broker < fits[j].Broker
		})
		return fits, best
	}
	out := []suggestion{}
	for _, b := range bonds {
		c := b.Nominal.Currency().Code
		nomMajor := float64(b.Nominal.Amount()) / 100
		if nomMajor <= 0 {
			continue
		}
		// Ціна входу сьогодні: номінал плюс НКД. Ціни в довіднику НБУ
		// немає, тож рахуємо «за номіналом» — це чесне наближення, і
		// принаймні НКД воно враховує, а він реально сплачується.
		cost := b.Nominal
		if acc, aerr := domain.EstimateAccrued(paysByISIN[b.ISIN], b.ISIN, today); aerr == nil && acc != nil {
			if c2, err2 := cost.Add(acc); err2 == nil {
				cost = c2
			}
		}
		costMajor := float64(cost.Amount()) / 100
		if costMajor <= 0 {
			continue
		}
		// Дохідність до погашення за цією ціною і вона ж у сьогоднішніх
		// гривнях. Для гривневих ділимо на знецінення — саме тут 4% у
		// доларі перестають програвати 16% у гривні автоматично.
		ytm, yerr := domain.YTM(cost, today, paysByISIN[b.ISIN], b.ISIN)
		if yerr != nil {
			continue // без майбутніх виплат порівнювати нема чого
		}
		real := realYield(ytm, c, devalPct)
		fits, best := fitsFor(c, costMajor)
		// Показуємо рекомендації ЗАВЖДИ, навіть коли грошей ще не вистачає:
		// інакше список порожніє одразу після покупки й помічник мовчить
		// саме тоді, коли ти плануєш наступний крок. Доступність — перший
		// критерій сортування, тож «можу купити» лишається зверху.
		canBuy := best > 0
		year := b.Maturity.Year()
		lnom := 0.0
		if m, ok := ladderYear[year]; ok {
			lnom = m[c]
		}
		def := target[c] - cur[c]
		var parts []string
		if def > 0.5 {
			parts = append(parts, fmt.Sprintf("добирає %s (%.0f%% → ціль %.0f%%)", c, cur[c], target[c]))
		}
		if kd := kindDef["bonds"]; kd > 0.5 {
			parts = append(parts, fmt.Sprintf("добирає ОВДП (%.0f в.п. до цілі)", kd))
		}
		if lnom == 0 {
			parts = append(parts, fmt.Sprintf("новий рік %d у драбині", year))
		} else {
			parts = append(parts, fmt.Sprintf("рік %d", year))
		}
		// Причину перевищення пишемо словами й лишаємо пораду на місці:
		// це попередження, а не фільтр.
		note := ""
		if over, ok := overISIN[b.ISIN]; ok {
			note = fmt.Sprintf("цього паперу вже %.0f в.п. понад ліміт %.0f%%", over, limitISIN)
		} else if over, ok := overYear[year]; ok {
			note = fmt.Sprintf("на %d рік уже припадає %.0f в.п. понад ліміт %.0f%%", year, over, limitYear)
		}
		// Чи розміщують цей папір узагалі. Довідник тримає 187 паперів і
		// показує їх однаково купованими, але на аукціонах за рік буває
		// три десятки: решту можна взяти лише на вторинному ринку й за
		// ціною, якої тут ніхто не знає.
		lastAucDate, lastAucPct, stale := "", 0.0, false
		if knowAuctions {
			if p, ok := lastAuction[b.ISIN]; ok {
				lastAucDate = string(p.Date)
				lastAucPct = float64(p.IncomeBP) / 100
				stale = domain.DaysBetween(p.Date, today) > staleAfterDays
			} else {
				stale = true
			}
			// Причина несе ЛИШЕ попередження — те, що міняє зміст рядка.
			// Саме «коли й під скільки» їде окремими полями: це нейтральний
			// факт, і UI має право показати його своїм форматом дати. Двічі
			// одне й те саме — і в прозі, і полем — читалось би як помилка.
			//
			// «Не розміщувався» там, де розміщували два роки тому, було б
			// неправдою: папір існує, застаріла лише ціна. Тому стани різні.
			switch {
			case !stale: // свіже розміщення — попереджати нема про що
			case lastAucDate != "":
				parts = append(parts, "відтоді лише вторинний ринок")
			default:
				parts = append(parts, "на аукціонах не розміщувався — лише вторинний ринок")
			}
		}
		if note != "" {
			parts = append(parts, "⚠ "+note)
		}
		// куди зрушить дюрацію купівля одного такого паперу
		y := doc.PortfolioYield[c] / 100
		if y <= 0 {
			y = doc.PortfolioYieldPct / 100
		}
		var pts []domain.CashPoint
		for _, p := range paysByISIN[b.ISIN] {
			if d := domain.DaysBetween(today, p.PayDate); d > 0 {
				pts = append(pts, domain.CashPoint{
					Years: float64(d) / 365, Amount: float64(p.PerBond.Amount()) / 100,
				})
			}
		}
		bMac, _, bPV := domain.Duration(pts, y)
		fxr := 1.0
		if c != money.UAH {
			fxr, _ = fx.RateMajor(c, rates)
		}
		bPVUAH := bPV * fxr
		newMac := curMac
		if curPV+bPVUAH > 0 {
			newMac = (curMac*curPV + bMac*bPVUAH) / (curPV + bPVUAH)
		}

		out = append(out, suggestion{
			Kind: "bond", Label: b.ISIN,
			ISIN: b.ISIN, Currency: c,
			RatePct:  fmt.Sprintf("%d.%02d", b.RateBP/100, b.RateBP%100),
			Maturity: string(b.Maturity), Nominal: toMoneyJSON(b.Nominal),
			CostPerBond: toMoneyJSON(cost),
			YTMPct:      round2(ytm * 100), NominalPct: round2(ytm * 100),
			RealPct:    round2(real * 100),
			YieldBasis: "до погашення",
			Brokers:    fits,
			Affordable: best, CanBuy: canBuy, Reason: strings.Join(parts, "; "),
			DurationNow: round2(curMac), DurationAfter: round2(newMac),
			LastAuction: lastAucDate, LastAuctionPct: round2(lastAucPct),
			def: def, kindDef: kindDef["bonds"], ladderNom: lnom,
			overLimit: note != "", stale: stale,
		})
	}

	// --- сертифікати фондів ---
	// Тут питання інше, ніж у портфелі, і число інше НАВМИСНО. У портфелі
	// «скільки я заробив» — факт, дивіденди разом зі зміною ціни. Тут
	// «скільки дасть, якщо докласти», і минуле подорожчання сертифіката в
	// цю відповідь не входить: його ніхто не обіцяв, а видавати його за
	// очікувану дохідність означало б радити купувати те, що вже виросло,
	// саме тому, що воно вже виросло.
	//
	// Обіцяна фондом дохідність — інша річ: вона саме про майбутнє, тож
	// заміняє дивідендну, коли задана. Без неї фонд, що обіцяє 9.5% у
	// доларі, порівнювався лише за дивідендами й опинявся в хвості з
	// −2.9% реальних, тобто в поради не потрапляв ніколи.
	for _, f := range doc.Funds {
		if f.LastPrice <= 0 {
			continue
		}
		c := f.Currency
		if c == "" {
			c = money.UAH
		}
		// Валюта обіцянки може відрізнятись від валюти сертифіката: 9.5% у
		// доларі вже реальні, і гривневий штраф до них не застосовується.
		nominal, yc, basis := f.YieldNetPct, c, "дивіденди після податку"
		if f.ExpectedPct > 0 {
			nominal, basis = f.ExpectedPct, "обіцяно фондом"
			if f.ExpectedCurrency != "" {
				yc = f.ExpectedCurrency
			}
		}
		if nominal <= 0 {
			continue // порівнювати нема з чим
		}
		// Фонд із закритим вікном залучення не пропонується взагалі.
		// Inzhur MilTech приймає гроші до 31.12.2026 і після цього не
		// приймає нікого; порада купити його в 2027-му — це порада
		// зробити неможливе.
		if f.BuyUntil != "" && f.BuyUntil < string(today) {
			continue
		}
		// Обіцянка йде в порівняння НЕТТО, як і ставка вкладу поруч.
		// Купон ОВДП від податку звільнений, дохід фонду ні, і брутто
		// поруч із ними робило фонд систематично кращим, ніж він є.
		//
		// Строк вирішує, коли податок береться: у фонду, що доживає до
		// закриття, дохід накопичується всередині й оподатковується один
		// раз у кінці, тож податок б'є по сумарному приросту, а не щороку.
		years := 0.0
		if f.CloseDate != "" {
			if d, derr := domain.ParseDate(f.CloseDate); derr == nil {
				if m := domain.MonthsBetween(today, d); m > 0 {
					years = float64(m) / 12
				}
			}
		}
		if f.IncomeTaxPct > 0 {
			nominal = round2(domain.NetOfTax(nominal, f.IncomeTaxPct, years))
			basis += ", після податку"
		}
		costMinor := int64(math.Round(f.LastPrice * 100))
		if costMinor <= 0 {
			continue
		}
		fits, best := fitsFor(c, f.LastPrice)
		// Рядок, який довго був неправдою для строкового фонду. У REIT
		// строку справді немає; у MilTech є і строк, і погашення активів,
		// і дата, після якої його не купити.
		term := "сертифікат: без строку й погашення, ціна ринкова"
		if f.CloseDate != "" {
			term = fmt.Sprintf("сертифікат зі строком: фонд закривається %s, ціна ринкова",
				f.CloseDate)
		}
		parts := []string{term}
		if f.BuyUntil != "" {
			parts = append(parts, "купити можна до "+f.BuyUntil)
		}
		if kd := kindDef["funds"]; kd > 0.5 {
			parts = append(parts, fmt.Sprintf("добирає фонди (%.0f в.п. до цілі)", kd))
		}
		note := ""
		if over, ok := overISIN[domain.FundISINPrefix+f.Fund]; ok {
			note = fmt.Sprintf("цього фонду вже %.0f в.п. понад ліміт %.0f%%", over, limitISIN)
			parts = append(parts, "⚠ "+note)
		}
		out = append(out, suggestion{
			Kind: "fund", Label: f.Fund, Currency: c,
			CostPerBond: toMoneyJSON(money.New(costMinor, c)),
			NominalPct:  round2(nominal),
			RealPct:     round2(realYield(nominal/100, yc, devalPct) * 100),
			YieldBasis:  basis,
			Brokers:     fits, Affordable: best, CanBuy: best > 0,
			Reason:    strings.Join(parts, "; "),
			def:       target[c] - cur[c],
			kindDef:   kindDef["funds"],
			overLimit: note != "",
		})
	}

	// --- поповнення вкладів ---
	// Ставка вкладу зафіксована, як YTM облігації, тож це така сама
	// обіцянка — лише оподаткована. «Крок» = поповнення на суму
	// відкриття: саме так працює поповнюваний вклад.
	deps, derr := s.st.ListTermDeposits(ctx)
	if derr != nil {
		writeErr(w, http.StatusInternalServerError, derr)
		return
	}
	for _, d := range deps {
		// Тільки поповнювані: радити докласти у вклад, який поповнень не
		// приймає, — порада, яку неможливо виконати.
		if !d.Replenishable || !d.Active(today) || d.Principal <= 0 {
			continue
		}
		c := d.Currency
		// Ставка після податку — те, що реально лишається.
		netRate := domain.NetRate(d.RateBP, d.TaxBP)
		real := realYield(netRate, c, devalPct)
		costMajor := float64(d.Principal) / 100
		fits, best := fitsFor(c, costMajor)
		bank := d.Bank
		if bank == "" {
			bank = "—"
		}
		out = append(out, suggestion{
			Kind: "deposit", Label: bank, Currency: c,
			RatePct:     fmt.Sprintf("%d.%02d", d.RateBP/100, d.RateBP%100),
			Maturity:    string(d.MaturityDate),
			CostPerBond: toMoneyJSON(money.New(d.Principal, c)),
			NominalPct:  round2(netRate * 100),
			RealPct:     round2(real * 100),
			YieldBasis:  "ставка вкладу після податку",
			Brokers:     fits, Affordable: best, CanBuy: best > 0,
			Reason:  withKindDef("поповнення на суму відкриття", kindDef["deposits"], "вклади"),
			def:     target[c] - cur[c],
			kindDef: kindDef["deposits"],
		})
	}

	// --- новий вклад ---
	// Вклад — теж інструмент реінвесту: якщо задано ставку у валюті,
	// пропонуємо відкрити НОВИЙ вклад на мінімальну суму ($100/€100). Це дає
	// простою USD/EUR куди йти, коли облігацій у цій валюті немає. Крок =
	// той самий мінімум, що й поріг «до реінвесту готовий». Без заданої
	// ставки поради немає — порівнювати дохідність не було б із чим.
	depRate := map[string]*float64{}
	if doc.Settings != nil {
		depRate[money.USD] = doc.Settings.DepositRateUSDPct
		depRate[money.EUR] = doc.Settings.DepositRateEURPct
		depRate[money.UAH] = doc.Settings.DepositRateUAHPct
	}
	depMin := s.depositMinMinorByCur(ctx)
	const depTaxBP = 1950 // дефолтна ставка податку на відсотки, як у deposit.go
	for _, c := range []string{money.USD, money.EUR, money.UAH} {
		rp := depRate[c]
		minMinor, hasMin := depMin[c]
		if rp == nil || *rp <= 0 || !hasMin || minMinor <= 0 {
			continue
		}
		rateBP := int64(math.Round(*rp * 100)) // % → ×100, як RateBP
		netRate := domain.NetRate(rateBP, depTaxBP)
		real := realYield(netRate, c, devalPct)
		costMajor := float64(minMinor) / 100
		fits, best := fitsFor(c, costMajor)
		out = append(out, suggestion{
			Kind: "deposit", Label: "Новий вклад", Currency: c,
			RatePct:     fmt.Sprintf("%d.%02d", rateBP/100, rateBP%100),
			CostPerBond: toMoneyJSON(money.New(minMinor, c)),
			NominalPct:  round2(netRate * 100),
			RealPct:     round2(real * 100),
			YieldBasis:  "ставка вкладу після податку",
			Brokers:     fits, Affordable: best, CanBuy: best > 0,
			Reason: withKindDef("новий вклад, мінімум "+money.New(minMinor, c).Display(),
				kindDef["deposits"], "вклади"),
			def:     target[c] - cur[c],
			kindDef: kindDef["deposits"],
		})
	}
	// Критерій ранжування обирає користувач: «під план» балансує валюту,
	// вид інструмента й драбину; решта — прості й передбачувані.
	//
	// Дюрації в цьому переліку НЕМАЄ, хоч раніше коментар її обіцяв.
	// Причина не в тому, що її забули: балансувати дюрацію нема до чого.
	// Цілі за нею користувач ніде не задає, а без цілі «більша» й «менша»
	// однаково не кращі — на відміну від валютної частки чи року драбини,
	// де ціль є і відхилення від неї вимірне. DurationNow/DurationAfter
	// лишаються в рядку як ДОВІДКА: видно, куди зрушить портфель ця
	// купівля, і рішення за людиною. Впорядковувати за числом, до якого
	// немає цілі, означало б вигадати цю ціль за неї.
	//
	// У сертифіката дати погашення немає — для впорядкування це «ніколи»,
	// а не «сьогодні»: інакше в режимі «короткі» фонди хибно вигравали б
	// у найкоротших паперів просто через порожнє поле.
	matKey := func(s suggestion) string {
		if s.Maturity == "" {
			return "9999-12-31"
		}
		return s.Maturity
	}
	// planScore — на скільки в.п. капіталу ця покупка зрушує портфель до
	// заявленої політики. Валютний і видовий дефіцити СКЛАДАЮТЬСЯ: одиниця
	// в них однакова (в.п. капіталу від одного знаменника), тож покупка,
	// що закриває обидва розриви, і має важити вдвічі. Доти видовий вимір
	// на порядок не впливав узагалі — цілі стояли в налаштуваннях, а
	// помічник про них не знав.
	planScore := func(s suggestion) float64 {
		return math.Max(0, s.def) + math.Max(0, s.kindDef)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.CanBuy != b.CanBuy {
			return a.CanBuy // те, що вже по кишені, — зверху
		}
		// Порушений ліміт опускає пораду, але не ховає її: заборона тут
		// була б порадою навпаки, а ліміт міг бути перевищений із причин,
		// яких застосунок не знає. Діє в УСІХ режимах ранжування — це не
		// критерій вигоди, а те, чого користувач сам від себе хотів.
		if a.overLimit != b.overLimit {
			return b.overLimit
		}
		// Папір, якого рік не розміщували, — нижче за той, що розміщують
		// зараз, і теж в УСІХ режимах. Підстава інша, ніж у ліміту, і
		// сказати її треба точно: це не судження про папір, а визнання,
		// що ЦІНА В ЦЬОМУ РЯДКУ рахується за номіналом, тобто за
		// первинним розміщенням. Для паперу, який розміщують сьогодні, це
		// чесне наближення; для того, якого рік не розміщували, — вигадка,
		// і головне число рядка варте менше. Сортувати за впевненістю в
		// власних числах застосунок має право; це не порада.
		if a.stale != b.stale {
			return b.stale
		}
		switch rank {
		case "rate":
			// «за ставкою» тепер означає за РЕАЛЬНОЮ дохідністю: сира
			// купонна ставка непорівнянна між валютами.
			if math.Abs(a.RealPct-b.RealPct) > 0.01 {
				return a.RealPct > b.RealPct
			}
		case "short":
			if matKey(a) != matKey(b) {
				return matKey(a) < matKey(b)
			}
		case "ladder":
			if a.ladderNom != b.ladderNom {
				return a.ladderNom < b.ladderNom
			}
			if math.Abs(a.RealPct-b.RealPct) > 0.01 {
				return a.RealPct > b.RealPct
			}
		default: // plan
			if sa, sb := planScore(a), planScore(b); math.Abs(sa-sb) > 0.01 {
				return sa > sb
			}
			if math.Abs(a.RealPct-b.RealPct) > 0.01 {
				return a.RealPct > b.RealPct
			}
			if a.ladderNom != b.ladderNom {
				return a.ladderNom < b.ladderNom
			}
		}
		// Останній критерій — реальна дохідність, а вже потім дата: у
		// фондів і вкладів драбина й дюрація не застосовні, тож без цього
		// вони впорядковувались би самою датою, а не вигодою.
		if math.Abs(a.RealPct-b.RealPct) > 0.01 {
			return a.RealPct > b.RealPct
		}
		return matKey(a) < matKey(b)
	})
	// Ліміту немає свідомо: у таблиці є фільтри, сортування й пагінація,
	// тож звужує користувач, а не бекенд мовчки.
	writeJSON(w, http.StatusOK, out)
}
