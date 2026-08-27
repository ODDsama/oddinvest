// Розкладка надходження: прийшла сума — куди й скільки з неї.
//
// ЧОМУ ЦЕ ОКРЕМИЙ ЕНДПОЙНТ, А НЕ РЯДОК У ПОМІЧНИКУ. «Що купити» відповідає
// на «що вигідніше», і його перелік не знає ні про яку суму: він ранжує
// інструменти й каже, скільки штук тягне КОЖЕН брокер окремо. Питання тут
// інше й вужче — «ось 32 000 ₴, розклади саме їх», — і відповідь на нього
// не перелік, а НАБІР із цілими квитками, який у сумі дорівнює тому, що
// прийшло. Дві різні відповіді в одному списку означали б, що частина
// рядків стосується цих грошей, а частина ні, і розрізняти їх довелося б
// очима.
//
// ВЛАСНОЇ АРИФМЕТИКИ ТУТ НЕМАЄ ЖОДНОЇ, і це головна вимога до файла:
//
//	частка подушки — doc.Reserve.FillNowUAH, готове число картки резерву;
//	бюджет виду    — spreadMonth (state_rebalance.go), той самий поділ, що
//	                 малює картка «Куди йдуть гроші місяця», лише
//	                 прикладений до цієї суми замість плану місяця;
//	порядок і ціна — reinvestSuggestions, тобто рейтинг, упорядкований
//	                 налаштуванням reinvest_rank, яке вибрав користувач.
//
// Друга копія будь-чого з цього означала б, що «Що купити» радить одне, а
// розкладка того ж дня — інше; проти рівно цієї розбіжності в now-view.js
// уже стоїть окреме попередження.
//
// РЕЗЕРВ ЗАБИРАЄ СВОЄ ПЕРШИМ І ЖАДІБНО. Не пропорційно й не «скільки
// лишиться»: подушка має власну, абсолютну ціль, і доки розрив живий,
// перші гроші місяця закривають саме його. Часткове закриття виходить
// само — коли FillNowUAH більший за суму, вирізка дорівнює всій сумі,
// рядків покупок немає, і хвіст добере наступна відмітка.
//
// ЧОГО ТУТ НЕМАЄ — обмеження залишком на рахунках. Гроші щойно прийшли, на
// брокері їх ще може не бути, і зрізати розкладку сьогоднішньою готівкою
// означало б відповісти на питання, якого не ставили.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
)

type allocateReq struct {
	Amount   string `json:"amount"`             // десятковий, як усюди в цьому API
	Currency string `json:"currency,omitempty"` // порожньо = UAH
}

// allocLine — один крок розкладки.
//
// Addable каже, чи можна цей рядок покласти в план купівель одним рухом.
// Хибне воно рівно у вкладу, і це не недогляд: порада про вклад — це
// ПОПОВНЕННЯ наявного (або «новий вклад» без банку), а plan_buys описує
// НОВИЙ вклад і вимагає строку, якого в пораді немає. Рядок, що записав би
// пів-вкладу, гірший за рядок, який чесно веде у форму поповнення.
type allocLine struct {
	Kind     string `json:"kind"` // bond | fund | deposit | npf
	Ref      string `json:"ref"`  // ISIN, назва фонду, банк, id рахунку НПФ
	Label    string `json:"label"`
	Currency string `json:"currency"`
	// Qty / Unit — для паперу й сертифіката; Amount — для вкладу й внеску.
	// Кожен вид відповідає на своє питання «скільки», і та сама межа вже
	// закріплена у planBuyFromReq.
	//
	// Вказівниками, а не значеннями: omitempty на структурі не діє, і рядок
	// паперу віз би порожній `amount:{"amount":"","currency":""}`, який у
	// браузері читається як «сума є, вона нульова» — тобто «0 ₴» замість
	// прочерку.
	Qty      int64      `json:"qty,omitempty"`
	Unit     *moneyJSON `json:"unit,omitempty"`
	Amount   *moneyJSON `json:"amount,omitempty"`
	TotalUAH float64    `json:"total_uah"`
	RealPct  float64    `json:"real_pct"`
	Why      string     `json:"why"`
	Addable  bool       `json:"addable"`
	// Convert — валюта кроку не збігається з валютою надходження.
	// ConvertNative — скільки самого надходження на це піде.
	//
	// Рядок при цьому НЕ ховається: сховати єдину доступну пораду тому, що
	// вона в іншій валюті, означало б відповісти «нема куди» там, де
	// відповідь — «поміняй спершу гроші». Мовчки конвертувати теж не можна,
	// тому позначка й число.
	Convert       bool    `json:"convert,omitempty"`
	ConvertNative float64 `json:"convert_native,omitempty"`
}

// allocReserve — вирізка подушки. Окремим полем, а не рядком у Lines: у
// резерву немає ні ціни кроку, ні дохідності, ні рядка в кошику, і
// поставити його в один список із покупками означало б або вигадати йому
// ці числа, або завести для нього особливий випадок у кожному читачі.
type allocReserve struct {
	AmountUAH float64 `json:"amount_uah"`
	Why       string  `json:"why"`
}

type allocPlan struct {
	Amount    moneyJSON     `json:"amount"`
	AmountUAH float64       `json:"amount_uah"`
	Reserve   *allocReserve `json:"reserve,omitempty"`
	AvailUAH  float64       `json:"avail_uah"`
	Lines     []allocLine   `json:"lines"`
	// RestUAH — те, що не склалося в цілі квитки. RestWhy називає причину
	// словами: сума без пояснення читається як загублена.
	RestUAH float64 `json:"rest_uah,omitempty"`
	RestWhy string  `json:"rest_why,omitempty"`
	// Note — чому рядків немає взагалі. Порожня відповідь без причини
	// читається як поломка, а причин рівно три: усе забрала подушка, цілей
	// за видом не задано, або на жоден цілий крок не вистачило.
	Note string `json:"note,omitempty"`
}

func (s *Server) handleAllocate(w http.ResponseWriter, r *http.Request) {
	var req allocateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cur := orUAH(strings.TrimSpace(req.Currency))
	minor, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("сума: %w", err))
		return
	}
	if minor <= 0 {
		writeErr(w, http.StatusBadRequest, badRequestf("сума розкладки має бути > 0"))
		return
	}
	now := time.Now()
	doc, err := s.buildState(r.Context(), now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rates, err := s.rates(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	uahM, err := fx.ToUAH(money.New(minor, cur), rates)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sug, err := s.reinvestSuggestions(r.Context(), now, doc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, allocatePlan(doc, sug, rates,
		toMoneyJSON(money.New(minor, cur)), float64(uahM.Amount())/100, cur,
		s.npfIDByName(r.Context())))
}

// npfIDByName — id рахунків НПФ за назвою.
//
// Id береться зі СХОВИЩА, а не з поради: doc.NPF несе назву (саме її й
// видно на екрані), а план тримається за id, щоб виправлення описки в
// назві не відчепило внески — розходження між двома ключами пояснене при
// domain.NPFPlanDest.
//
// Окремою функцією, бо читачів двоє: розкладка суми, що вже прийшла, і
// маршрут грошей, які ще прийдуть (route.go). Друга копія цього циклу
// означала б, що дві відповіді на «у котрий пенсійний вносити» можуть
// розійтись — а розійтись вони можуть рівно в тому випадку, заради якого
// нуль нижче й стоїть.
func (s *Server) npfIDByName(ctx context.Context) map[string]int64 {
	out := map[string]int64{}
	accs, err := s.st.ListNPFAccounts(ctx)
	if err != nil {
		return out
	}
	for _, a := range accs {
		if _, dup := out[a.Name]; dup {
			// Дві однакові назви — і сказати, у котрий саме рахунок вносити,
			// нема з чого. Нуль вимикає рядок, замість того щоб вгадати
			// навмання.
			out[a.Name] = 0
			continue
		}
		out[a.Name] = a.ID
	}
	return out
}

// allocKind — вид інструмента поради в термінах ребалансу. Мапа, а не
// switch у трьох місцях: ті самі чотири пари вже стоять у kindRows
// (buy-plan.js) і в kindTargets (state_rebalance.go), і п'ята копія
// розійшлася б із ними тихо.
var allocKind = map[string]string{
	"bond": "bonds", "fund": "funds", "deposit": "deposits", "npf": "npf",
}

// allocatePlan — уся розкладка. Чиста функція над готовим документом: саме
// тому її можна перевірити тестом, не піднімаючи сервера.
func allocatePlan(doc *state.Doc, sug []suggestion, rates fx.Rates,
	amount moneyJSON, amountUAH float64, cur string, npfID map[string]int64) allocPlan {

	out := allocPlan{Amount: amount, AmountUAH: round2(amountUAH), Lines: []allocLine{}}

	// --- подушка першою ---
	avail := amountUAH
	if doc.Reserve != nil && doc.Reserve.FillNowUAH > 0 {
		cut := math.Min(amountUAH, doc.Reserve.FillNowUAH)
		why := fmt.Sprintf("місячна частка подушки — %s з %s",
			uah(cut), uah(doc.Reserve.FillMonthUAH))
		switch {
		case cut < doc.Reserve.FillNowUAH:
			why += "; більше з цієї суми не вийде — решту добере наступне надходження"
		case doc.Reserve.GapUAH > 0:
			why += fmt.Sprintf("; до цілі ще %s", uah(doc.Reserve.GapUAH))
		}
		out.Reserve = &allocReserve{AmountUAH: round2(cut), Why: why}
		avail = amountUAH - cut
	}
	out.AvailUAH = round2(avail)
	if avail <= 0 {
		out.Note = "усе пішло в подушку: доки розрив не закритий, вона забирає своє першою"
		return out
	}

	// --- бюджети видів ---
	//
	// spreadMonth рахує на КОПІЇ рядків документа, і копія обнуляється перед
	// викликом. Без цього вид, для якого поділ нічого не дасть, лишився б із
	// числом ВІД ПЛАНУ МІСЯЦЯ — тобто розкладка 500 ₴ могла б порадити
	// купити на тридцять тисяч (spreadMonth виходить одразу, коли ділити
	// нічого, і чужих чисел за собою не прибирає).
	rows := make([]state.RebalanceRow, len(doc.Rebalance))
	copy(rows, doc.Rebalance)
	for i := range rows {
		rows[i].MonthShareUAH, rows[i].MonthBalanceUAH = 0, 0
	}
	spreadMonth(rows, avail, doc.CapitalUAH-doc.ReserveUAH)

	type kindBudget struct {
		key string
		uah float64
	}
	var budgets []kindBudget
	for _, r := range rows {
		if r.Dimension != "kind" || r.TargetPct <= 0 || r.MonthBalanceUAH <= 0 {
			continue
		}
		budgets = append(budgets, kindBudget{key: r.Key, uah: r.MonthBalanceUAH})
	}
	if len(budgets) == 0 {
		out.RestUAH = round2(avail)
		out.Note = "цілей за видом інструмента не задано, або всі вони вже перебрані — " +
			"розкладати нема за яким правилом"
		return out
	}
	// Найбільший розрив першим, ключ другим критерієм: два однакові бюджети
	// інакше стали б у порядку, різному від запуску до запуску.
	sort.Slice(budgets, func(i, j int) bool {
		if math.Abs(budgets[i].uah-budgets[j].uah) > 0.005 {
			return budgets[i].uah > budgets[j].uah
		}
		return budgets[i].key < budgets[j].key
	})

	// --- бюджет → цілі квитки ---
	rest, cheapest, cheapestWhat := 0.0, 0.0, ""
	for _, b := range budgets {
		left := b.uah
		for i := range sug {
			sg := sug[i]
			if allocKind[sg.Kind] != b.key {
				continue
			}
			line, spent, ok := allocOne(sg, left, rates, cur, npfID)
			if !ok {
				// Крок дорожчий за те, що лишилось. Запам'ятовуємо найдешевший
				// НЕДОСЯЖНИЙ крок: саме він і пояснює залишок.
				if c := allocStepUAH(sg, rates); c > 0 && (cheapest == 0 || c < cheapest) {
					cheapest, cheapestWhat = c, sg.Label
				}
				continue
			}
			out.Lines = append(out.Lines, line)
			left -= spent
			// НПФ бере бюджет виду цілком: порога входу він не має, тож
			// «наступного кроку» в ньому не буває.
			if sg.Kind == "npf" {
				break
			}
		}
		rest += left
	}
	out.RestUAH = round2(rest)
	// Причина залишку — три різні речі, і зводити їх до однієї фрази не можна:
	// «бракує 730 ₴» і «інструментів немає взагалі» вимагають різних дій.
	if rest > 0.005 {
		switch {
		case cheapest > rest:
			out.RestWhy = fmt.Sprintf("на наступний крок (%s, %s) бракує %s",
				cheapestWhat, uah(cheapest), uah(cheapest-rest))
		case cheapest > 0:
			// Найдешевший крок дешевший за залишок — але залишок зібраний із
			// РІЗНИХ видів, і в жодному з них своєї частки на цілий крок не
			// набралось. Гроші не перекидаються між видами: це зламало б саме
			// те вирівнювання, заради якого поділ і робиться.
			out.RestWhy = "залишок зібраний із різних видів, і в жодному з них " +
				"своєї частки на цілий крок не набралось"
		default:
			out.RestWhy = "інструментів із відомою ціною в цих видах немає — " +
				"довідник порожній або без цін"
		}
	}
	if len(out.Lines) == 0 && out.Note == "" {
		out.Note = "на цілий крок жодного виду не вистачило — гроші чекають на наступне надходження"
	}
	return out
}

// allocStepUAH — ціна одного кроку поради в гривні-еквіваленті. Нуль
// означає «кроку немає» (пенсійний) або «ціни немає».
func allocStepUAH(sg suggestion, rates fx.Rates) float64 {
	step := moneyAmount(sg.CostPerBond)
	if step <= 0 {
		return 0
	}
	rate, ok := fx.RateMajor(sg.Currency, rates)
	if !ok {
		return 0
	}
	return step * rate
}

// allocOne — скільки цієї поради вміщується в залишок бюджету. ok=false
// означає «жодного цілого кроку»: половини паперу не буває, і саме тут
// живе «квиток ОВДП ≈1000 ₴» — одним діленням над однією ціною, тією
// самою, яку показує «Що купити» (bondUnitCost).
func allocOne(sg suggestion, left float64, rates fx.Rates,
	cur string, npfID map[string]int64) (allocLine, float64, bool) {

	line := allocLine{
		Kind: sg.Kind, Label: sg.Label, Currency: sg.Currency,
		RealPct: sg.RealPct, Why: sg.Reason,
	}

	if sg.Kind == "npf" {
		id := npfID[sg.Label]
		if id <= 0 || left <= 0 {
			return line, 0, false
		}
		// Валюта тут гривнева свідомо: бюджети видів міряні в грн-екв., а
		// внесок приймає будь-яку суму — конвертувати нема чого, і позначка
		// «конвертація» на ньому була б неправдою.
		line.Ref = strconv.FormatInt(id, 10)
		line.Currency = money.UAH
		amt := toMoneyJSON(money.New(int64(math.Round(left*100)), money.UAH))
		line.Amount = &amt
		line.TotalUAH = round2(left)
		line.Addable = true
		return line, left, true
	}

	step := allocStepUAH(sg, rates)
	if step <= 0 {
		return line, 0, false
	}
	n := int64(left / step)
	if n < 1 {
		return line, 0, false
	}
	spent := float64(n) * step
	line.Qty = n
	unit := sg.CostPerBond
	line.Unit = &unit
	line.TotalUAH = round2(spent)
	switch sg.Kind {
	case "bond":
		line.Ref, line.Addable = sg.ISIN, sg.ISIN != ""
	case "fund":
		line.Ref, line.Addable = sg.Label, sg.Label != ""
	default: // deposit
		// Ref — банк, і лише для наявного вкладу: у рядка «Новий вклад» банку
		// немає взагалі. Addable хибне в обох випадках (див. allocLine).
		line.Ref = sg.Label
		amt := toMoneyJSON(money.New(
			int64(math.Round(spent/allocRate(sg.Currency, rates)*100)), sg.Currency))
		line.Amount = &amt
		line.Qty, line.Unit = 0, nil
	}
	if sg.Currency != cur {
		line.Convert = true
		if rate, ok := fx.RateMajor(cur, rates); ok && rate > 0 {
			line.ConvertNative = round2(spent / rate)
		}
	}
	return line, spent, true
}

// allocRate — курс валюти в гривні; одиниця для гривні й для невідомого.
// Одиниця замість нуля тут навмисно: нуль пішов би в знаменник.
func allocRate(cur string, rates fx.Rates) float64 {
	if rate, ok := fx.RateMajor(cur, rates); ok && rate > 0 {
		return rate
	}
	return 1
}
