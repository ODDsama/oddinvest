// Помічник реінвесту: що купити на вільні гроші.
//
// Порівнює три інструменти між собою за РЕАЛЬНОЮ дохідністю після податку
// й знецінення — саме вона, а не ціна, вирішує порядок.

package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
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
	Kind     string `json:"kind"`
	Label    string `json:"label"`
	ISIN     string `json:"isin,omitempty"`
	Currency string `json:"currency"`
	// RatePct — ДОГОВІРНА ставка вкладу, і лише вкладу. Це не дохідність:
	// порівнювати рядки між собою можна тільки за RealPct нижче.
	//
	// В облігації тут колись стояла купонна ставка з довідника. Вона не
	// показувалась ніде (UI малює це поле під kind == "deposit") і не могла
	// б: сира купонна ставка незіставна між валютами, а дохідність паперу
	// вже є поруч двома чесними числами — YTMPct і RealPct.
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
	NominalPct float64     `json:"nominal_pct,omitempty"`
	RealPct    float64     `json:"real_pct"`
	YieldBasis string      `json:"yield_basis"`
	Brokers    []brokerFit `json:"brokers,omitempty"`
	Affordable int64       `json:"affordable"`
	CanBuy     bool        `json:"can_buy"`
	Reason     string      `json:"reason"`
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
	// overTransit — у вкладах цієї валюти вже стільки, скільки треба на
	// наступний папір, тобто черга своє відстояла.
	//
	// Замінив собою kindDef для вкладів, і заміна не косметична. Доти
	// вклад діставав ОБИДВА дефіцити — валютний і видовий — за одну й ту
	// саму роботу: ціль за вкладами стояла поруч із валютною ціллю, хоч
	// вклад і був способом ту валютну ціль закрити. Відколи власної цілі
	// у вкладів немає (state_rebalance.go), kindDef["deposits"] нульовий
	// сам собою, а потребу в новому вкладі називає транзит.
	//
	// Опускає, але не ховає — те саме правило, що в overLimit: вклад понад
	// транзит не є помилкою (гроші можуть чекати на щось, чого застосунок
	// не знає), він просто вже не про наступний папір.
	overTransit bool
	// Locked / LockedUntil — гроші, покладені сюди, не можна забрати до
	// вказаної дати. Сьогодні це лише НПФ.
	//
	// Експортоване, на відміну від stale й overLimit, і причина в тому, що
	// це властивість самої ПРОПОЗИЦІЇ, а не нашої впевненості в ній: людина
	// мусить бачити замок у рядку, а не вгадувати його з порядку.
	//
	// Головне ж — чого тут НЕМА. Немає жодного коефіцієнта, який штрафував
	// би RealPct за неліквідність. Спокуса зробити «чесне порівняння»
	// множником сильна, але такий множник — вигадане число, і воно було б
	// хибним рівно там, де на нього дивляться. Замість цього замкнені рядки
	// просто стоять НИЖЧЕ за все ліквідне в УСІХ режимах ранжування — це
	// твердження про непорівнянність, а не про якість, і саме тому воно
	// механізм, а не арифметика. Той самий прийом, що вже застосований до
	// stale й overLimit.
	Locked      bool   `json:"locked,omitempty"`
	LockedUntil string `json:"locked_until,omitempty"`
	// ReadyOn / ReadyBroker / ReadyDays / ReadyVia / ReadyNote — коли на цей
	// рядок набереться, на чиєму рахунку і з чого саме. Заповнює
	// annotateReady (ready_on.go) і лише в /api/reinvest: черга задач тієї ж
	// збірки порад цих полів не показує, а другий прохід по джерелах
	// подорожчав би кожен /api/summary.
	//
	// Порожні там, де відповіді немає: рядок, на який стає вже сьогодні,
	// дати не має за визначенням.
	ReadyOn     string       `json:"ready_on,omitempty"`
	ReadyBroker string       `json:"ready_broker,omitempty"`
	ReadyDays   int          `json:"ready_days,omitempty"`
	ReadyVia    []readyEvent `json:"ready_via,omitempty"`
	ReadyNote   string       `json:"ready_note,omitempty"`
	// WaitCost / WaitAlt — скільки коштують ці дні очікування й чим міряно.
	// Вказівник, а не значення: нуль і «не було чим міряти» — різні
	// відповіді, і друга не має права виглядати як «безкоштовно».
	WaitCost *moneyJSON `json:"wait_cost,omitempty"`
	WaitAlt  string     `json:"wait_alt,omitempty"`
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

// withTransit дописує до причини вкладу, НАВІЩО він, а не яку частку
// добирає. Різниця не в словах: доти тут стояв withKindDef, тобто вклад
// пояснювався ціллю за вкладами — а ціль за вкладами й була тим, чого
// насправді не існує. Тепер рядок каже те єдине, що вклад справді робить:
// доносить валюту до наступного паперу.
//
// Три стани, і кожен вартий власного речення:
//
//	транзиту немає   — валютної цілі не названо, сказати нема чого;
//	черга триває     — бракує стільки-то до наступного паперу;
//	черга відстояла  — у вкладах уже стільки, скільки треба на папір.
//
// Останній випадок НЕ ховає рядок і не називає його помилкою: гроші можуть
// чекати на щось, чого застосунок не знає. Він лише перестає вдавати, що
// цей вклад про наступний папір, — і те саме твердження опускає рядок у
// порядку (overTransit).
func withTransit(base, cur string, transitNative, haveNative float64) string {
	if transitNative <= 0 {
		return base
	}
	if left := transitNative - haveNative; left > 0 {
		return base + "; " + money.New(int64(math.Round(left*100)), cur).Display() +
			" до наступного паперу"
	}
	return base + "; понад транзит — у вкладах уже стільки, скільки треба на папір"
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
// handleReinvest — GET /api/reinvest. Уся робота в reinvestSuggestions:
// те саме число потрібне ще й черзі задач, а дві збірки порад означали б,
// що «Що робити» і «Що купити» радять різне — рівно та розбіжність, проти
// якої в now-view.js уже стоїть окреме попередження.
func (s *Server) handleReinvest(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	doc, err := s.buildState(r.Context(), now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out, err := s.reinvestSuggestions(r.Context(), now, doc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Дата доступності — лише тут, і лише для екрана: чому не всередині
	// reinvestSuggestions, сказано в шапці ready_on.go.
	if err := s.annotateReady(r.Context(), domain.NewDate(now), doc, out); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Ліміту немає свідомо: у таблиці є фільтри, сортування й пагінація,
	// тож звужує користувач, а не бекенд мовчки.
	writeJSON(w, http.StatusOK, out)
}

// reinvestSuggestions — ранжовані поради за готовим документом стану.
//
// Документ приймається АРГУМЕНТОМ, а не будується всередині: тут його вже
// має той, хто кличе (обробник — свій, черга задач — свій), і другий
// buildState був би найдорожчим шляхом бекенда, пройденим двічі поспіль.
func (s *Server) reinvestSuggestions(ctx context.Context, now time.Time,
	doc *state.Doc) ([]suggestion, error) {
	today := domain.NewDate(now)
	bonds, err := s.st.SearchBonds(ctx, "", "", today, "", 5000) // усі майбутні папери
	if err != nil {
		return nil, err
	}

	// Валютний вимір існує ЛИШЕ тоді, коли валютну ціль назвали.
	//
	// Доти target["UAH"] рахувався як 100 − USD − EUR беззастережно, і при
	// жодній заданій цілі це давало 100: кожен гривневий рядок діставав
	// def = usdShare + eurShare і причину «добирає UAH (60% → ціль 100%)».
	// Тобто застосунок стверджував стовідсоткову гривневу ціль, якої ніхто
	// не ставив, і ще й ранжував за нею. Ребаланс так не робив ніколи —
	// buildRebalance просто не малює валютного рядка без цілі, — і помічник
	// лишався єдиним місцем, де ціль бралася нізвідки.
	//
	// Порожні мапи — не недогляд, а сам механізм: читання відсутнього ключа
	// дає нуль, тож def = 0 − 0 = 0 для КОЖНОЇ валюти, поріг 0.5 в.п. нижче
	// не спрацьовує жодного разу, а planScore лишається самим видовим
	// дефіцитом. Жодного прапорця нести далі не треба.
	//
	// Саме порожні, а не нільові: у нільову мапу не можна писати, і рядок
	// target["UAH"] усередині умови панікував би.
	target := map[string]float64{}
	cur := map[string]float64{}
	if doc.Settings != nil &&
		(doc.Settings.USDTargetSharePct != nil || doc.Settings.EURTargetSharePct != nil) {
		if doc.Settings.USDTargetSharePct != nil {
			target["USD"] = *doc.Settings.USDTargetSharePct
		}
		if doc.Settings.EURTargetSharePct != nil {
			target["EUR"] = *doc.Settings.EURTargetSharePct
		}
		// Названа лише одна з двох — це теж валютна політика, і вона мусить
		// працювати: гривня добирає залишок від НАЗВАНИХ часток (USD 40 →
		// UAH 60). Неназвана валюта лишається нулем, тобто рівно тим, чим
		// була й раніше: target["EUR"] = 0 і відсутній ключ на читанні
		// нерозрізненні.
		target["UAH"] = 100 - target["USD"] - target["EUR"]
		cur["USD"], cur["EUR"] = doc.USDSharePct, doc.EURSharePct
		cur["UAH"] = 100 - doc.USDSharePct - doc.EURSharePct
	}

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
	// transitNative — скільки грошей ЦІЄЇ валюти має чекати у вкладі, доки
	// на наступний папір не зібрано, у самій валюті. Береться готовим із
	// рядка ребалансу, як і все інше в цьому блоці: власна копія арифметики
	// часток у цьому файлі вже одного разу розвела плитку з карткою, і саме
	// тому тут не з'являється ні курс, ні друге ділення.
	transitNative := map[string]float64{}
	for _, r := range doc.Rebalance {
		if r.Dimension == "currency" && r.TransitNative > 0 {
			transitNative[r.Key] = r.TransitNative
		}
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

	rank := "plan"
	if doc.Settings != nil && doc.Settings.ReinvestRank != "" {
		rank = doc.Settings.ReinvestRank
	}
	// Знецінення гривні: те саме припущення, що й у прогнозі, інакше
	// помічник радив би одне, а прогноз малював інше.
	devalPct := s.devaluation(ctx)
	isins := make([]string, 0, len(bonds))
	for _, b := range bonds {
		isins = append(isins, b.ISIN)
	}
	allPays, err := s.st.PaymentsFor(ctx, isins)
	if err != nil {
		return nil, err
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
		// Ціна входу сьогодні — спільна з кошиком (unit_cost.go): два
		// обчислення давали б на екрані дві ціни того самого паперу.
		cost := bondUnitCost(b, paysByISIN[b.ISIN], today)
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
		out = append(out, suggestion{
			Kind: "bond", Label: b.ISIN,
			ISIN: b.ISIN, Currency: c,
			Maturity: string(b.Maturity), Nominal: toMoneyJSON(b.Nominal),
			CostPerBond: toMoneyJSON(cost),
			YTMPct:      round2(ytm * 100), NominalPct: round2(ytm * 100),
			RealPct:    round2(real * 100),
			YieldBasis: "до погашення",
			Brokers:    fits,
			Affordable: best, CanBuy: canBuy, Reason: strings.Join(parts, "; "),
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
		fundCost := fundUnitCost(f.LastPrice, c) // спільне з кошиком (unit_cost.go)
		if fundCost.Amount() <= 0 {
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
			CostPerBond: toMoneyJSON(fundCost),
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
		return nil, derr
	}
	// Скільки вже стоїть у вкладах КОЖНОЇ валюти, у самій валюті.
	// Порівнюється з транзитом цієї ж валюти: доки менше — вклад іще про
	// наступний папір, щойно більше — черга своє відстояла.
	//
	// Рахуємо тут, а не беремо з документа: там вклади опубліковані однією
	// сумою в гривні (deposits_uah), а питання валютне.
	//
	// Резервні вклади сюди НЕ входять: вони не стоять у черзі до паперу, а
	// лежать під аварію. Порахувати їх транзитом означало б вирішити, що
	// черга вже відстояна, і замовкнути про справжню — саме тим коштом, що
	// подушка й папір це різні гроші.
	depByCur := map[string]float64{}
	for _, d := range deps {
		if d.Active(today) && !d.IsReserve {
			depByCur[d.Currency] += float64(d.BalanceAt(today)) / 100
		}
	}
	// Без названої валютної цілі транзиту немає, і опускати нема за чим:
	// відсутність цілі це не «ціль нуль», а відмова від виміру.
	overTransitFor := func(cur string) bool {
		t, ok := transitNative[cur]
		return ok && depByCur[cur] >= t
	}
	for _, d := range deps {
		// Тільки поповнювані: радити докласти у вклад, який поповнень не
		// приймає, — порада, яку неможливо виконати.
		//
		// І тільки НЕ РЕЗЕРВНІ. Аргумент написаний у шапці
		// handlers_reserve.go: злиття подушки з купівельною спроможністю
		// змусило б помічника радити папір за аварійні гроші. Поповнюють
		// подушку власним механізмом — стелею reserve_fill_share_pct, — і
		// в якій саме формі, каже драбина доступу, а не цей перелік.
		if !d.Replenishable || d.IsReserve || !d.Active(today) || d.Principal <= 0 {
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
			Reason:      withTransit("поповнення на суму відкриття", c, transitNative[c], depByCur[c]),
			def:         target[c] - cur[c],
			kindDef:     kindDef["deposits"],
			overTransit: overTransitFor(c),
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
	rawSettings, err := s.st.AllSettings(ctx)
	if err != nil {
		return nil, err
	}
	depMin := depositMinMinorByCur(rawSettings)
	for _, c := range []string{money.USD, money.EUR, money.UAH} {
		rp := depRate[c]
		minMinor, hasMin := depMin[c]
		if rp == nil || *rp <= 0 || !hasMin || minMinor <= 0 {
			continue
		}
		rateBP := int64(math.Round(*rp * 100)) // % → ×100, як RateBP
		netRate := domain.NetRate(rateBP, defaultDepositTaxBP)
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
			Reason: withTransit("новий вклад, мінімум "+money.New(minMinor, c).Display(),
				c, transitNative[c], depByCur[c]),
			def:         target[c] - cur[c],
			kindDef:     kindDef["deposits"],
			overTransit: overTransitFor(c),
		})
	}
	// --- пенсійний фонд ---
	//
	// НПФ у цьому переліку — свідоме рішення, і воно стало можливим лише
	// після того, як податкова знижка почала враховуватись: 18% повернення
	// від внеску — це великий дохід ПЕРШОГО року, і без нього рядок був би
	// систематично заниженим саме там, де він і так найслабший.
	//
	// Порога входу немає: внести можна будь-яку суму, тож CostPerBond
	// нульовий, а CanBuy означає просто «на рахунках є хоч щось». Це не
	// недогляд полів — це властивість інструмента, і вигадувати йому
	// «мінімальний внесок» означало б додати обмеження, якого фонд не
	// ставить.
	//
	// Основа дохідності НАЗИВАЄ замок вголос. Це головне, що рядок мусить
	// сказати: число поруч із вкладом виглядає порівнянним, а воно не
	// порівнянне — і в застосунку, де yield_basis існує саме для таких
	// випадків, промовчати про це було б гірше, ніж не показувати рядок
	// узагалі.
	for _, n := range doc.NPF {
		if n.AccessDate == "" {
			continue // без дати доступу не сказати навіть, доки замкнено
		}
		real := n.RealPct
		if real == 0 {
			continue // ні виміряного зростання, ні обіцянки — порівнювати нічим
		}
		// ccy, а не cur: cur — це мапа поточних валютних часток, і затінити
		// її тут означало б порахувати def від назви валюти.
		ccy := n.Currency
		if ccy == "" {
			ccy = money.UAH
		}
		// «Будь-яка сума» рахується ОКРЕМО від fitsFor, а не нульовою ціною
		// через нього: там ціна — знаменник, і нуль чесно дає нуль.
		//
		// А нуль тут читався б як «ще збираєш на 0 ₴» — тобто рядок падав
		// би в блок недоступних із порогом, якого не існує. Питання до
		// пенсійного інше: не «чи вистачає на одну штуку», а «чи є взагалі
		// з чого внести». Штук у нього немає.
		var maxOne float64
		var fits []brokerFit
		for name, byCur := range doc.Brokers {
			if v := byCur[ccy]; v > 0 {
				fits = append(fits, brokerFit{Broker: name, Qty: 1})
				if v > maxOne {
					maxOne = v
				}
			}
		}
		sort.Slice(fits, func(i, j int) bool { return fits[i].Broker < fits[j].Broker })
		nominal := n.NavReturnPct
		if nominal == 0 {
			nominal = n.ExpectedPct
		}
		reason := "внесок у пенсійний; гроші замкнені до " + n.AccessDate
		if n.CreditEstUAH > 0 {
			reason += fmt.Sprintf("; знижка ПДФО за рік ≈%.0f ₴", n.CreditEstUAH)
		}
		out = append(out, suggestion{
			Kind: "npf", Label: n.Name, Currency: ccy,
			CostPerBond: toMoneyJSON(money.New(0, ccy)),
			NominalPct:  nominal,
			RealPct:     real,
			YieldBasis:  n.YieldBasis + "; замкнено до " + n.AccessDate,
			// Affordable = 1, бо «скільки штук» до пенсійного не стосується:
			// нуль читався б як «жодної», а справжня відповідь — «будь-яка
			// сума». CanBuy — чи є хоч десь гроші цієї валюти.
			Brokers: fits, Affordable: 1, CanBuy: maxOne > 0,
			Reason:      withKindDef(reason, kindDef["npf"], "НПФ"),
			Locked:      true,
			LockedUntil: n.AccessDate,
			def:         target[ccy] - cur[ccy],
			kindDef:     kindDef["npf"],
		})
	}

	// Критерій ранжування обирає користувач: «під план» балансує валюту,
	// вид інструмента й драбину; решта — прості й передбачувані.
	//
	// Дюрації в цьому переліку НЕМАЄ, хоч раніше коментар її обіцяв.
	// Причина не в тому, що її забули: балансувати дюрацію нема до чого.
	// Цілі за нею користувач ніде не задає, а без цілі «більша» й «менша»
	// однаково не кращі — на відміну від валютної частки чи року драбини,
	// де ціль є і відхилення від неї вимірне. Впорядковувати за числом, до
	// якого немає цілі, означало б вигадати цю ціль за неї.
	//
	// Полів duration_now/duration_after у рядку теж немає, хоч цей самий
	// коментар довго обіцяв їх «як довідку». Довідки не вийшло: жоден
	// екран їх не читав — ані веб, ані інтеграція, — а рахувались вони на
	// КОЖНОГО кандидата (Duration плюс перевід у гривню на папір із
	// п'яти тисяч довідника) при кожному відкритті «Огляду». Обіцянка,
	// яку ніхто не спожив, коштувала більше за все, що вона мала дати.
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
	// planScore — на скільки відсоткових пунктів ця покупка зрушує портфель
	// до заявленої політики. Валютний і видовий дефіцити СКЛАДАЮТЬСЯ:
	// покупка, що закриває обидва розриви, має важити вдвічі. Доти видовий
	// вимір на порядок не впливав узагалі — цілі стояли в налаштуваннях, а
	// помічник про них не знав.
	//
	// ЗНАМЕННИКИ В НИХ ТЕПЕР РІЗНІ, і промовчати про це не можна. Доти тут
	// стояло «одиниця в них однакова, тож складати їх можна», і це була
	// правда: обидва міряли частку від усього капіталу. Відколи цілі за
	// видом рахуються від капіталу БЕЗ резерву (state_rebalance.go), видовий
	// пункт трохи «дорожчий» за валютний.
	//
	// Чому це прийнятно: множник між базами (капітал / капітал без резерву)
	// один і той самий для ВСІХ видів у кожен момент, тож порядок усередині
	// видів не змінюється — зсувається лише вага «вид проти валюти», і то
	// рівно на розмір подушки. Це впорядкування, а не виміряна величина;
	// приводити бази одна до одної означало б показувати в причині рядка
	// число, якого немає в жодній картці.
	planScore := func(s suggestion) float64 {
		return math.Max(0, s.def) + math.Max(0, s.kindDef)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		// Замкнене — нижче за все ліквідне, і ПЕРШИМ серед пониження: це
		// найсильніше з трьох тверджень. Ліміт каже «ти сам цього не хотів»,
		// stale — «ми не впевнені в ціні», а замок — «це взагалі не те саме
		// питання»: між шестимісячним вкладом і двадцятип'ятирічним замком
		// немає числа, яке робило б їх порівнянними.
		//
		// Тому саме порядок, а не штраф до дохідності: коефіцієнт
		// неліквідності був би вигаданим числом, а вигадане число в головній
		// колонці списку гірше за чесний порядок.
		//
		// Стоїть ПЕРЕД CanBuy навмисно: замкнений рядок, на який грошей
		// вистачає, усе одно не має обганяти ліквідний, на який не вистачає
		// зовсім трохи. Питання «куди подіти прибулий купон» про перший не
		// стоїть узагалі.
		if a.Locked != b.Locked {
			return b.Locked
		}
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
		// Вклад, чия черга вже відстояна, — нижче, і теж в УСІХ режимах.
		// Підстава та сама, що в ліміту, і так само не є судженням про
		// інструмент: цей рядок просто більше не відповідає на питання, під
		// яке його сюди поставили. Доти те саме робив kindDef — і робив
		// навпаки, ПІДНІМАЮЧИ вклад за ціллю, якої не мало бути.
		if a.overTransit != b.overTransit {
			return b.overTransit
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
	return out, nil
}
