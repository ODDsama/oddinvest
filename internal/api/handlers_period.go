// Підсумок місяця: чим цей період відрізнявся від попереднього.
//
// Застосунок відповідав на «що зараз» (Зараз / Портфель / Гроші) і на «що
// буде» (План). Минулого як розділу не було взагалі: щоденні знімки й
// крива «Як росте» є, журнал рішень є, звіт про рух є — а відповіді «чим
// липень відрізнявся від червня» не було, бо вона розсипана по чотирьох
// сторінках і жодна з них не бере період як ціле.
//
// ЩО ТУТ НЕ РАХУЄТЬСЯ ВДРУГЕ. Гроші періоду — це summarizeCash
// (cashflow.go), той самий виклик, яким живе «Гроші → Рухи». Дві
// реалізації тих самих п'яти сум розійшлись би мовчки, бо обидва числа
// лишились би правдоподібними; у цьому застосунку таке вже траплялось
// двічі (шапка handlers_whatif.go). Простій рахує domain.IdleIncome —
// та сама черга «покупка з'їдає найстаріший дохід», що й у зведенні.
// Рядок рішення збирає decisionBase (handlers_decisions.go).
//
// ЧОГО ТУТ СВІДОМО НЕМАЄ — звірки «мало прийти за графіком проти
// надійшло». Спокуса очевидна: календар знає, що папір платить, а звіт
// знає, що гроші прийшли. Але domain.Arrived зараховує МИНУЛУ дату
// отриманою без жодної позначки, тож для закритого місяця обидві сторони
// рівні за побудовою — «перевірка», яка не може не зійтися, гірша за її
// відсутність. Справжня звірка живе на сторінці «Звірка рахунку», де
// порівнюють із випискою брокера, а не із самим собою.
//
// І НЕМАЄ ОЦІНОК. Ні «добре», ні «відстаєш», ні кольору на дельті: числа
// зі знаком і порівняння з тим, що планувалось. Той самий принцип, за
// яким конфігуратор стратегій не має слова «рекомендований», а валютне
// вікно — підсвітки «дорого».
package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

// periodMoney — гроші періоду, у гривнях.
type periodMoney struct {
	OpeningUAH  state.Money `json:"opening_uah"`
	IncomeUAH   state.Money `json:"income_uah"`
	ContribUAH  state.Money `json:"contributed_uah"`
	PurchaseUAH state.Money `json:"purchased_uah"`
	ConvUAH     state.Money `json:"conversions_uah"`
	ClosingUAH  state.Money `json:"closing_uah"`
	// OutsideUAH — у подушку й цілі (поза залишком гаманця); OwnUAH —
	// «внесено своїх» разом, те саме означення, що в плитки «Цей місяць».
	// contributed_uah лишається ЛИШЕ гаманцем — це рядок виписки.
	OutsideUAH state.Money `json:"outside_uah"`
	OwnUAH     state.Money `json:"own_uah"`
}

// periodRow — один вимір «було → стало».
type periodRow struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// У валюті звітності «було» перекладається курсом дати першого знімка,
	// «стало» — другого (дати лежать у батьківській periodStructure), а
	// дельта — різниця перекладених (present: asof, diff): за місяць курс
	// рухається, і саме цей рух — частина відповіді.
	Before state.Money `json:"before" money:"asof=from_date"`
	After  state.Money `json:"after" money:"asof=to_date"`
	Delta  state.Money `json:"delta" money:"diff=after,before"`
}

// periodStructure — з чого складався портфель на початку періоду і з чого
// на кінці.
//
// FromDate/ToDate — СПРАВЖНІ дати знімків, а не межі періоду. Демон міг
// лежати, знімка рівно на 1 число могло не бути, і мовчазна підстановка
// сусіднього дня зробила б із дірки в даних результат місяця. Той самий
// прийом, що в fx_asof.go: дату, за якою рахували, називають уголос.
type periodStructure struct {
	FromDate     string      `json:"from_date"`
	ToDate       string      `json:"to_date"`
	Rows         []periodRow `json:"rows"`
	USDShareFrom float64     `json:"usd_share_from"`
	USDShareTo   float64     `json:"usd_share_to"`
	// Частка EUR — пара до доларової, але з сентинелом: до міграції 0061
	// колонки не було, і в старих знімках стоїть −1 бп, тобто −0.01 тут.
	// Затирати його нулем НЕ можна — 0 % це законна частка («євро
	// немає»), — тож відповідь везе знак як є, а рядок малює чи мовчить
	// уже фронтенд. Те саме правило, що над kept нижче: те, чого не було
	// ні на початку, ні в кінці, з екрана зникає.
	EURShareFrom float64 `json:"eur_share_from"`
	EURShareTo   float64 `json:"eur_share_to"`
}

// periodPlan — місячна ціль проти внесеного.
type periodPlan struct {
	TargetUAH  state.Money `json:"target_uah" money:"asof=target_on"`
	ContribUAH state.Money `json:"contributed_uah"`
	DonePct    float64     `json:"done_pct"`
	// TargetOn — дата знімка, з якого взята ціль. Ціль міняють, і
	// сьогоднішня не є тією, що діяла в тому місяці; знімок тримає ту,
	// що діяла.
	TargetOn string `json:"target_on"`
}

// periodDecisions — що куплено в цьому місяці й за чиєю порадою.
type periodDecisions struct {
	// Count і Followed — про ПОКУПКИ. Рухи в подушку сюди не входять і
	// мають свою пару: аргумент той самий, що в decisionsSummary, і
	// зводити їх в одне число не можна там і тут однаково.
	Count      int     `json:"count"`
	Followed   int     `json:"followed"`
	VsTopPPAvg float64 `json:"vs_top_pp_avg,omitempty"`
	// ReserveCount / ReserveForgonePctAvg — рухи в матрац за той самий
	// місяць і дохідність доступного в ті хвилини. Не «втрачене».
	ReserveCount         int     `json:"reserve_count,omitempty"`
	ReserveForgonePctAvg float64 `json:"reserve_forgone_pct_avg,omitempty"`
	// GoalCount / GoalForgonePctAvg — те саме для цілей накопичення, і теж
	// окремою парою: подушку тримають, щоб НЕ витратити, ціль — щоб
	// витратити, і місяць мусить уміти сказати це нарізно.
	GoalCount         int     `json:"goal_count,omitempty"`
	GoalForgonePctAvg float64 `json:"goal_forgone_pct_avg,omitempty"`
	// Rows — усі рядки місяця, подушку й цілі ВКЛЮЧНО: у таблиці вид
	// підписаний, і сховати з неї половину рішень заради чистого знаменника
	// означало б відповісти на «що я вирішив у серпні» неповно.
	Rows []decisionRow `json:"rows,omitempty"`
	Note string        `json:"note,omitempty"`
}

type periodResp struct {
	From string `json:"from"`
	// To — дата області для решти сум (present: asof): гроші періоду й
	// простій перекладаються курсом КІНЦЯ періоду. Один курс на всю
	// виписку, інакше тотожність «відкриття + дохід − покупки = закриття»
	// не сходилась би; що всередині місяця курс ходив — свідоме наближення.
	To    string      `json:"to" money:"asof"`
	Money periodMoney `json:"money"`
	// IdleUAH — скільки з доходу, що надійшов У ЦЬОМУ місяці, до кінця
	// місяця не пішло в діло. Саме місячна відповідь, а не всесвітня:
	// гроші могли піти в діло наступного числа, і рядок каже про місяць,
	// а не про долю цих грошей узагалі.
	IdleUAH       state.Money      `json:"idle_uah"`
	Structure     *periodStructure `json:"structure,omitempty"`
	StructureNote string           `json:"structure_note,omitempty"`
	Plan          *periodPlan      `json:"plan,omitempty"`
	PlanNote      string           `json:"plan_note,omitempty"`
	Decisions     periodDecisions  `json:"decisions"`
}

// handlePeriod — GET /api/period?month=YYYY-MM.
//
// Типово — МИНУЛИЙ календарний місяць, а не поточний: підсумок має сенс
// для закритого періоду, а незакритий щодня показував би інше число й
// читався б як «місяць провалюється». Поточний доступний явним ?month —
// заборони немає, є замовчування.
func (s *Server) handlePeriod(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	today := domain.NewDate(time.Now())
	month := r.URL.Query().Get("month")
	if month == "" {
		month = string(today.AddMonths(-1))[:7]
	}
	from, to, err := monthBounds(month)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	events, err := s.cashEvents(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	sum := summarizeCash(events, from, to)
	out := periodResp{From: string(from), To: string(to), Money: periodMoney{
		OpeningUAH: state.Major(sum.major(sum.OpeningUAH), money.UAH),
		IncomeUAH:  state.Major(sum.major(sum.IncomeUAH), money.UAH),
		ContribUAH: state.Major(sum.major(sum.ContribUAH), money.UAH),
		// Знак перевертається тут із тієї ж причини, що й у звіті про рух:
		// у підсумку покупки віднімаються, і мінус на мінусі читався б як
		// помилка.
		PurchaseUAH: state.Major(sum.major(-sum.PurchaseUAH), money.UAH),
		ConvUAH:     state.Major(sum.major(sum.ConvUAH), money.UAH),
		ClosingUAH:  state.Major(sum.major(sum.ClosingUAH()), money.UAH),
		OutsideUAH:  state.Major(sum.major(sum.OutsideUAH), money.UAH),
		OwnUAH:      state.Major(sum.major(sum.OwnUAH()), money.UAH),
	}}

	var income, buys []domain.CashEvent
	for _, e := range sum.Rows {
		switch e.Kind {
		case flowIncome:
			income = append(income, domain.CashEvent{Date: e.Date, Amount: e.UAH})
		case flowPurchase:
			buys = append(buys, domain.CashEvent{Date: e.Date, Amount: -e.UAH})
		}
	}
	out.IdleUAH = state.Major(sum.major(domain.IdleIncome(income, buys)), money.UAH)

	snaps, err := s.st.ListSnapshots(ctx, "", to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out.Structure, out.StructureNote = periodStructureOf(snaps, from, "місяць", "місяця")
	// Проти цілі — свої гроші РАЗОМ із подушкою й цілями: саме так рахує
	// плитка «Цей місяць», і ціль місяця сама включає стелю подушки.
	out.Plan, out.PlanNote = periodPlanOf(snaps, from, to, sum.OwnUAH())

	list, err := s.st.ListDecisions(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out.Decisions = periodDecisionsOf(list, from, to)
	if err := s.present(ctx, &out); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// monthBounds — межі календарного місяця «YYYY-MM».
func monthBounds(month string) (domain.Date, domain.Date, error) {
	if len(month) != 7 || month[4] != '-' {
		return "", "", fmt.Errorf("місяць має вигляд YYYY-MM, а не %q", month)
	}
	from, err := domain.ParseDate(month + "-01")
	if err != nil {
		return "", "", fmt.Errorf("місяць має вигляд YYYY-MM, а не %q", month)
	}
	return from, from.AddMonths(1).AddDays(-1), nil
}

// periodStructureOf — знімки на межах періоду й різниця між ними.
//
// Відкритий знімок — останній ДО початку періоду: саме він описує стан, з
// якого місяць почався. Закритий — останній у межах періоду. Коли ні
// того, ні того немає, розділ мовчить із названою причиною: порожня
// таблиця «було → стало» з нулями стверджувала б, що капітал був нульовим.
func periodStructureOf(snaps []store.Snapshot, from domain.Date, acc, gen string) (*periodStructure, string) {
	var before, after *store.Snapshot
	for i := range snaps {
		sn := snaps[i]
		if sn.Date.Before(from) {
			before = &snaps[i]
			continue
		}
		after = &snaps[i]
	}
	if after == nil {
		return nil, "знімків за цей " + acc + " немає — застосунок тоді ще не працював"
	}
	if before == nil {
		return nil, "немає знімка до початку " + gen + ", тож порівнювати немає з чим"
	}
	row := func(key, label string, b, a int64) periodRow {
		return periodRow{Key: key, Label: label,
			Before: state.Minor(b, money.UAH), After: state.Minor(a, money.UAH),
			Delta: state.Minor(a-b, money.UAH)}
	}
	out := &periodStructure{
		FromDate:     string(before.Date),
		ToDate:       string(after.Date),
		USDShareFrom: round2(float64(before.USDShareBP) / 100),
		USDShareTo:   round2(float64(after.USDShareBP) / 100),
		EURShareFrom: round2(float64(before.EURShareBP) / 100),
		EURShareTo:   round2(float64(after.EURShareBP) / 100),
		Rows: []periodRow{
			row("capital", "Капітал", snapshotCapitalUAH(*before), snapshotCapitalUAH(*after)),
			row("bonds", "ОВДП (номінал)", before.NominalUAHEq, after.NominalUAHEq),
			row("funds", "Фонди", before.FundsUAH, after.FundsUAH),
			row("deposits", "Вклади", before.DepositsUAH, after.DepositsUAH),
			row("npf", "НПФ", before.NPFUAH, after.NPFUAH),
			row("reserve", "Резерв", before.ReserveUAH, after.ReserveUAH),
			row("goals", "Цілі накопичення", before.GoalsUAH, after.GoalsUAH),
			row("account", "На рахунках", before.AccountUAH, after.AccountUAH),
		},
	}
	// Вид, якого не було ні на початку, ні на кінці, з таблиці зникає:
	// рядок «Фонди 0 → 0» не є фактом про місяць, це просто інструмент,
	// якого в тебе немає. Капітал лишається завжди — він і є підсумком.
	kept := out.Rows[:1]
	for _, r := range out.Rows[1:] {
		if r.Before.Major() != 0 || r.After.Major() != 0 {
			kept = append(kept, r)
		}
	}
	out.Rows = kept
	return out, ""
}

// periodPlanOf — місячна ціль того місяця проти фактично внесеного.
//
// Ціль береться зі знімка ПЕРІОДУ, а не з нинішніх налаштувань: ціль
// міняють, і міряти липень серпневою ціллю означало б переписувати
// минуле. Знімок тримає month_target_uah саме тому.
func periodPlanOf(snaps []store.Snapshot, from, to domain.Date, contribMinor int64) (*periodPlan, string) {
	var target int64
	var on domain.Date
	for _, sn := range snaps {
		if sn.Date.Before(from) || sn.Date.After(to) {
			continue
		}
		if sn.MonthTargetUAH > 0 {
			target, on = sn.MonthTargetUAH, sn.Date
		}
	}
	if target <= 0 {
		return nil, "місячної цілі тоді не було задано, тож порівнювати внесене немає з чим"
	}
	return &periodPlan{
		TargetUAH:  state.Minor(target, money.UAH),
		ContribUAH: state.Minor(contribMinor, money.UAH),
		DonePct:    round2(float64(contribMinor) / float64(target) * 100),
		TargetOn:   string(on),
	}, ""
}

// periodDecisionsOf — рішення, ухвалені в цьому місяці.
//
// Зведення тут БЕЗ порога decisionsMinRows, і це не суперечність із
// сусіднім розділом. Там зведення відповідає на «який режим рейтингу мені
// підходить» — висновок, який на трьох рядках був би шумом. Тут же
// питання інше й дрібніше: що я купив цього місяця і чи це були верхні
// рядки. На нього два рішення відповідають чесно, бо це переказ, а не
// висновок.
func periodDecisionsOf(list []store.Decision, from, to domain.Date) periodDecisions {
	out := periodDecisions{}
	var sum, forgone, goalForgone float64
	var withTop int
	for _, d := range list {
		if d.MadeOn.Before(from) || d.MadeOn.After(to) {
			continue
		}
		row := decisionBase(d)
		out.Rows = append(out.Rows, row)
		if d.Kind == decisionKindReserve {
			out.ReserveCount++
			forgone += row.ForgonePct
			continue
		}
		if d.Kind == decisionKindGoal {
			out.GoalCount++
			goalForgone += row.ForgonePct
			continue
		}
		out.Count++
		if row.RankPos == 1 {
			out.Followed++
		}
		if row.TopLabel != "" {
			sum += row.VsTopPP
			withTop++
		}
	}
	if withTop > 0 {
		out.VsTopPPAvg = round2(sum / float64(withTop))
	}
	if out.ReserveCount > 0 {
		out.ReserveForgonePctAvg = round2(forgone / float64(out.ReserveCount))
	}
	if out.GoalCount > 0 {
		out.GoalForgonePctAvg = round2(goalForgone / float64(out.GoalCount))
	}
	if out.Count == 0 {
		out.Note = "цього місяця нічого не куплено"
	}
	return out
}
