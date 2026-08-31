// GET /api/payoff — коли борги скінчаться і скільки коштуватиме дорогою.
//
// ЧОМУ ЦЕ REST, А НЕ ПОЛЕ ДОКУМЕНТА СТАНУ. План погашення — ПРОЄКЦІЯ, а не
// стан: він залежить від питання («а якщо кидати ще тисячу?») і від
// стратегії, яку обрала людина. Документ стану їде в MQTT і в знімок, тобто
// описує те, що Є. Той самий поділ, що вже проведено для /api/progress і
// /api/decisions.
//
// ТРИ СТРАТЕГІЇ РАХУЮТЬСЯ ЗАВЖДИ, а не лише обрана. Без «лише мінімалки»
// немає з чим порівняти, а «швидше на 7 місяців» — це і є та відповідь, по
// яку сюди приходять. Дешево: прохід уперед — це десятки місяців на
// десяток боргів.
package api

import (
	"net/http"
	"strings"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

type payoffDebtJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Rate — ефективна річна, %. Basis каже, звідки вона: виведена з
	// графіка платежів чи перерахована із заявленої банком.
	Rate  float64 `json:"rate_pct"`
	Basis string  `json:"rate_basis"`
	// RealPct — та сама ставка за вирахуванням знецінення, щоб її можна
	// було покласти поруч із реальною дохідністю портфеля.
	RealPct float64   `json:"real_pct"`
	Left    moneyJSON `json:"left"`
	// CloseDate — коли цей борг закриється за обраною стратегією.
	CloseDate string `json:"close_date,omitempty"`
}

type payoffPlanJSON struct {
	Strategy string `json:"strategy"`
	Months   int    `json:"months"`
	// FreeDate — місяць, у якому не лишиться жодного боргу.
	FreeDate string    `json:"free_date,omitempty"`
	Paid     moneyJSON `json:"paid"`
	// Cost — скільки з Paid лишається банку: комісії розстрочок і відсотки
	// картки. Саме воно, а не сума платежів, є ціною боргу.
	Cost moneyJSON `json:"cost"`
	// Unfunded — за цієї стратегії борг не гаситься взагалі: мінімалка не
	// покриває навіть відсотка. Не помилка розрахунку, а стан, який треба
	// побачити.
	Unfunded bool `json:"unfunded,omitempty"`
}

type payoffMonthJSON struct {
	Month string    `json:"month"`
	Paid  moneyJSON `json:"paid"`
	Cost  moneyJSON `json:"cost"`
	Left  moneyJSON `json:"left"`
}

type payoffSensitivityJSON struct {
	Extra moneyJSON `json:"extra"`
	// MonthsSaved / CostSaved — скільки місяців і грошей дає ця добавка
	// ПРОТИ обраної суми. Різницею, а не абсолютом: питання тут «що дасть
	// іще тисяча», і відповідь на нього — різниця.
	MonthsSaved int       `json:"months_saved"`
	CostSaved   moneyJSON `json:"cost_saved"`
}

type payoffGraceJSON struct {
	DebtID int64  `json:"debt_id"`
	Name   string `json:"name"`
	// DueDate — до якого числа треба внести; FullDue — скільки, щоб не
	// платити відсотків зовсім; MinDue — щоб не отримати штраф і підвищену
	// ставку. Два пороги, бо помилки дві.
	DueDate   string    `json:"due_date,omitempty"`
	DaysToDue int       `json:"days_to_due,omitempty"`
	FullDue   moneyJSON `json:"full_due"`
	MinDue    moneyJSON `json:"min_due"`
	// Free — скільки ще можна витратити, не потрапивши на відсотки.
	Free moneyJSON `json:"free"`
	// MissFullCost / MissMinCost — ціна кожної з двох помилок за місяць.
	MissFullCost moneyJSON `json:"miss_full_cost"`
	MissMinCost  moneyJSON `json:"miss_min_cost"`
	// MarkDate / MarkAgeDays — на чому це все стоїть. Вік звірки
	// показується завжди: місячної давнини баланс кредитки — це спогад.
	MarkDate    string `json:"mark_date,omitempty"`
	MarkAgeDays int    `json:"mark_age_days,omitempty"`
	Known       bool   `json:"known"`
}

type payoffResp struct {
	Strategy string           `json:"strategy"`
	Extra    moneyJSON        `json:"extra"`
	Debts    []payoffDebtJSON `json:"debts"`
	Total    moneyJSON        `json:"total"`
	Plan     payoffPlanJSON   `json:"plan"`
	// Compare — усі три стратегії поруч, у місяцях і в гривнях.
	Compare     []payoffPlanJSON        `json:"compare"`
	Schedule    []payoffMonthJSON       `json:"schedule,omitempty"`
	Sensitivity []payoffSensitivityJSON `json:"sensitivity,omitempty"`
	// Grace — пільговий цикл карток. Не входить у чергу погашення (довід у
	// payoff.go), але саме тут його ціна стає видимою.
	Grace []payoffGraceJSON `json:"grace,omitempty"`
	// InvestInsteadPct — реальна дохідність портфеля, щоб покласти її
	// поруч зі ставками боргів. Без вироку: рядки порівнює людина, а
	// застосунок лише ставить числа в одну колонку.
	InvestInsteadPct float64 `json:"invest_instead_pct,omitempty"`
	// DevaluationPct — знецінення, яким переведені номінальні ставки в
	// реальні. Показується, бо без нього real_pct читається як магія.
	DevaluationPct float64 `json:"devaluation_pct"`
	// Note — чого в цих числах свідомо немає.
	Note string `json:"note,omitempty"`
}

func (s *Server) handlePayoff(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	today := domain.NewDate(now)

	strategy := strings.TrimSpace(r.URL.Query().Get("strategy"))
	switch strategy {
	case payoffAvalanche, payoffSnowball, payoffMinimum:
	default:
		// Замовчування — лавина: вона дає найменшу переплату, і це
		// арифметика, а не думка. Сніжок обирають свідомо, заради того,
		// щоб рядків меншало швидше.
		strategy = payoffAvalanche
	}

	rates, err := s.rates(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	debts, err := s.st.ListDebts(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	marks, err := s.st.ListDebtMarks(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	ops, err := s.st.ListDebtOps(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	extra := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("extra")); raw != "" {
		if extra, err = domain.ParseDecimalToMinor(raw, money.UAH); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if extra < 0 {
			extra = 0
		}
	}

	deval := s.devaluation(ctx)
	list := buildPayoffDebts(debts, marks, ops, rates, today)

	out := payoffResp{
		Strategy:       strategy,
		Extra:          toMoneyJSON(money.New(extra, money.UAH)),
		DevaluationPct: deval,
		Debts:          make([]payoffDebtJSON, 0, len(list)),
		Note: "У черзі лише те, на що нараховують: розстрочки й непільгова частина картки. " +
			"Оборот у межах пільгового періоду сюди не входить — його ціна показана окремо.",
	}
	if doc, derr := s.buildState(ctx, now); derr == nil && doc != nil {
		out.InvestInsteadPct = doc.BlendedYieldRealPct
	}

	run := runPayoff(list, strategy, extra)
	var total int64
	// Порядок рядків — це ЧЕРГА ПОГАШЕННЯ обраної стратегії, а не порядок
	// зі сховища. Список, у якому перший рядок не той, що гаситься першим,
	// довелося б читати очима проти власного заголовка.
	for _, i := range payoffOrder(list, strategy) {
		d := list[i]
		total += d.Left
		row := payoffDebtJSON{
			ID: d.ID, Name: d.Name, Kind: d.Kind,
			Rate:  round2(d.Rate),
			Basis: d.RateBasis,
			Left:  toMoneyJSON(money.New(d.Left, money.UAH)),
		}
		if d.RateBasis != domain.DebtRateNone {
			row.RealPct = round2(realYield(d.Rate, money.UAH, deval))
		}
		if m, ok := run.CloseAt[d.ID]; ok {
			row.CloseDate = monthKeyAt(today, m)
		}
		out.Debts = append(out.Debts, row)
	}
	out.Total = toMoneyJSON(money.New(total, money.UAH))
	out.Plan = payoffPlanToJSON(strategy, run, today)

	for _, alt := range []string{payoffAvalanche, payoffSnowball, payoffMinimum} {
		r := run
		if alt != strategy {
			r = runPayoff(list, alt, extra)
		}
		out.Compare = append(out.Compare, payoffPlanToJSON(alt, r, today))
	}

	out.Schedule = payoffSchedule(run, total, today)

	// Чутливість — рівно про те, що людина може зробити завтра: додати
	// тисячу чи пʼять. Абсолютних чисел тут немає навмисно, лише різниця
	// проти нинішнього темпу.
	if len(list) > 0 && strategy != payoffMinimum {
		for _, step := range []int64{1_000_00, 5_000_00} {
			alt := runPayoff(list, strategy, extra+step)
			out.Sensitivity = append(out.Sensitivity, payoffSensitivityJSON{
				Extra:       toMoneyJSON(money.New(step, money.UAH)),
				MonthsSaved: run.Months - alt.Months,
				CostSaved:   toMoneyJSON(money.New(run.Cost-alt.Cost, money.UAH)),
			})
		}
	}

	for _, d := range debts {
		if !d.IsCard() || d.Closed() {
			continue
		}
		st := domain.CardState(d, marks, ops, debts, today)
		missFull, missMin := payoffGraceCost(d, st)
		out.Grace = append(out.Grace, payoffGraceJSON{
			DebtID: d.ID, Name: d.Name,
			DueDate: string(st.DueDate), DaysToDue: st.DaysToDue,
			FullDue:      toMoneyJSON(money.New(st.StatementDue, d.Currency)),
			MinDue:       toMoneyJSON(money.New(st.MinDue, d.Currency)),
			Free:         toMoneyJSON(money.New(st.Free, d.Currency)),
			MissFullCost: toMoneyJSON(money.New(missFull, d.Currency)),
			MissMinCost:  toMoneyJSON(money.New(missMin, d.Currency)),
			MarkDate:     string(st.MarkDate), MarkAgeDays: st.MarkAgeDays,
			Known: st.Known,
		})
	}

	writeJSON(w, http.StatusOK, out)
}

func payoffPlanToJSON(strategy string, run payoffRun, today domain.Date) payoffPlanJSON {
	out := payoffPlanJSON{
		Strategy: strategy,
		Months:   run.Months,
		Paid:     toMoneyJSON(money.New(run.Paid, money.UAH)),
		Cost:     toMoneyJSON(money.New(run.Cost, money.UAH)),
		Unfunded: run.Unfunded,
	}
	if !run.Unfunded && run.Months > 0 {
		out.FreeDate = monthKeyAt(today, run.Months-1)
	}
	return out
}

// payoffSchedule зводить кроки в помісячні рядки: скільки віддано, скільки
// з того лишилось банку й скільки боргу ще попереду.
func payoffSchedule(run payoffRun, total int64, today domain.Date) []payoffMonthJSON {
	if len(run.Steps) == 0 {
		return nil
	}
	type acc struct{ paid, cost, principal int64 }
	per := map[int]*acc{}
	last := 0
	for _, st := range run.Steps {
		a := per[st.Month]
		if a == nil {
			a = &acc{}
			per[st.Month] = a
		}
		a.paid += st.Paid
		a.cost += st.Cost
		a.principal += st.Principal
		if st.Month > last {
			last = st.Month
		}
	}
	left := total
	out := make([]payoffMonthJSON, 0, last+1)
	for m := 0; m <= last; m++ {
		a := per[m]
		if a == nil {
			a = &acc{}
		}
		left -= a.principal
		if left < 0 {
			left = 0
		}
		out = append(out, payoffMonthJSON{
			Month: monthKeyAt(today, m),
			Paid:  toMoneyJSON(money.New(a.paid, money.UAH)),
			Cost:  toMoneyJSON(money.New(a.cost, money.UAH)),
			Left:  toMoneyJSON(money.New(left, money.UAH)),
		})
	}
	return out
}
