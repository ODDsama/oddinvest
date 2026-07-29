// Чутливість: який важіль наскільки зрушує ціль.
//
// Картка прогнозу чесно каже, що за фактичним темпом ціль покривається на
// 9% — і на цьому зупиняється. Питання, з яким після цього лишається
// користувач, звучить «а що саме змінити», і відповіді на нього в
// застосунку не було.
//
// МЕЖА, ЯКУ ТУТ НЕ ПЕРЕХОДИМО. У коді записано «Це інструмент, не порада»
// (handlers_reinvest.go, views/strategy.js). Тому жоден рядок звідси не
// каже «варто вносити більше» і рядки НЕ сортуються «найкращий зверху»:
// вони стоять сталими групами, а кожен показує лише наслідок одного
// припущення. Що з цим робити — вирішує людина, і половина важелів
// (ставка, знецінення) від неї взагалі не залежить.
//
// Важелі рухаються ПО ОДНОМУ. Змішані сценарії («і вношу більше, і ринок
// кращий») виглядають переконливіше, але відповідають на питання, якого
// ніхто не ставив: у них не видно, що саме дало ефект.
//
// Підписів тут немає — самі числа й ключ важеля, як у RebalanceRow.
// Складати «внесок ×2» на бекенді означало б тримати форматування в двох
// місцях: рядок у документі й той самий рядок у панелі.
package api

import (
	"math"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
)

// sensitivityInput — усе, що фазі потрібно понад те, що вже порахувала
// проєкція. Рукави збирає та сама фабрика, тож модель тут рівно одна.
type sensitivityInput struct {
	Factory  sleeveFactory
	Deval    float64
	Goal     float64
	Deadline int // місяців до дедлайну
	// ContribBase — від чого відштовхуються важелі внеску. Це ФАКТИЧНИЙ
	// темп, коли він відомий, а не план: людина стоїть там, де вона
	// стоїть, і «×2 від плану, якого ти не тягнеш» — марна відповідь.
	ContribBase float64
	BaseFrom    string // "actual" | "plan"
	// RateSpreadPP / DevalSpreadPP — ті самі, що задають ширину віяла.
	// Окремих чисел тут навмисно немає: інакше «песимістично» в одній
	// картці й «гірший ринок» у сусідній означали б різне.
	RateSpreadPP  float64
	DevalSpreadPP float64
	Today         domain.Date
}

// buildSensitivity проганяє модель по одному збуреному входу за раз.
func buildSensitivity(in sensitivityInput) *state.Sensitivity {
	if in.Goal <= 0 || in.Deadline <= 0 || in.ContribBase <= 0 {
		return nil
	}
	// Один прогін = одна відповідь. Обидві величини потрібні разом: «коли
	// дійду» і «скільки буде на дедлайн» — різні питання, і важіль може
	// зрушити одне, не торкнувшись другого.
	run := func(contrib, ratePP, deval, goal float64, months int) (int, float64) {
		sl := in.Factory.build(contrib, ratePP)
		hit := domain.MonthsToReachSleeves(sl, deval, goal, goalHorizonMonths)
		return hit, round2(domain.ProjectSleeves(sl, deval, months).TodayUAH)
	}

	out := &state.Sensitivity{
		BaseContribUAH: round2(in.ContribBase),
		BaseFrom:       in.BaseFrom,
		GoalUAH:        in.Goal,
		DeadlineMonths: in.Deadline,
	}
	out.BaseGoalMonths, out.BaseAmountUAH =
		run(in.ContribBase, 0, in.Deval, in.Goal, in.Deadline)
	out.BaseGoalDate = goalDate(in.Today, out.BaseGoalMonths)
	out.BaseGoalPct = goalPct(out.BaseAmountUAH, in.Goal)

	add := func(r state.SensitivityRow, months int, amount, goal float64) {
		r.GoalMonths, r.GoalDate = months, goalDate(in.Today, months)
		r.AmountUAH, r.GoalPct = amount, goalPct(amount, goal)
		out.Rows = append(out.Rows, r)
	}

	// --- внесок: єдиний важіль, який людина рухає сама ---
	for _, k := range []float64{0.5, 1.5, 2} {
		c := in.ContribBase * k
		m, a := run(c, 0, in.Deval, in.Goal, in.Deadline)
		add(state.SensitivityRow{Lever: "contrib", Factor: k, Value: round2(c)}, m, a, in.Goal)
	}

	// --- ринок: ставка й знецінення. Не важелі, а погода ---
	for _, d := range []float64{in.RateSpreadPP, -in.RateSpreadPP} {
		m, a := run(in.ContribBase, d, in.Deval, in.Goal, in.Deadline)
		add(state.SensitivityRow{Lever: "rate", DeltaPP: d, Value: d}, m, a, in.Goal)
	}
	for _, d := range []float64{-in.DevalSpreadPP, in.DevalSpreadPP} {
		deval := math.Max(0, in.Deval+d)
		m, a := run(in.ContribBase, 0, deval, in.Goal, in.Deadline)
		add(state.SensitivityRow{Lever: "deval", DeltaPP: d, Value: round2(deval)}, m, a, in.Goal)
	}

	// --- дедлайн: ціль не рухається, рухається час ---
	//
	// Тут потрібна лише сума: місяць досягнення від дедлайну не залежить
	// узагалі — він каже, КОЛИ ціль буде досягнута, а не коли її чекають.
	// Тому GoalMonths у цих рядках базовий, і це не помилка копіювання.
	sleevesBase := in.Factory.build(in.ContribBase, 0)
	for _, d := range []int{12, -12} {
		months := in.Deadline + d
		if months <= 0 {
			continue
		}
		a := round2(domain.ProjectSleeves(sleevesBase, in.Deval, months).TodayUAH)
		add(state.SensitivityRow{Lever: "deadline", DeltaMonths: d, Value: float64(months)},
			out.BaseGoalMonths, a, in.Goal)
	}

	// --- ціль: скільки з неї вже покривається ---
	//
	// Дзеркало попереднього: сума на дедлайн та сама, змінюється лише те,
	// з чим її порівнюють.
	for _, k := range []float64{0.75, 0.5} {
		goal := in.Goal * k
		m := domain.MonthsToReachSleeves(sleevesBase, in.Deval, goal, goalHorizonMonths)
		add(state.SensitivityRow{Lever: "goal", Factor: k, Value: round2(goal)},
			m, out.BaseAmountUAH, goal)
	}
	return out
}

// goalDate — дата, коли ціль буде досягнута. Порожньо, якщо вже або
// ніколи: у першому випадку дати в майбутньому немає, у другому її немає
// взагалі, і малювати «2086 рік» означало б удавати точність.
func goalDate(today domain.Date, months int) string {
	if months <= 0 {
		return ""
	}
	return string(domain.NewDate(today.Time().AddDate(0, months, 0)))
}

// goalPct — той самий крок округлення, що й у ForecastRow.GoalPct: одна
// десята відсотка. Два різні заокруглення для того самого показника
// читались би як розбіжність.
func goalPct(amount, goal float64) float64 {
	if goal <= 0 {
		return 0
	}
	return math.Round(amount/goal*1000) / 10
}
