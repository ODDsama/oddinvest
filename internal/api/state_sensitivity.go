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
	money "github.com/Rhymond/go-money"
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
		BaseContribUAH: state.Major(in.ContribBase, money.UAH),
		BaseFrom:       in.BaseFrom,
		GoalUAH:        state.Major(in.Goal, money.UAH),
		DeadlineMonths: in.Deadline,
	}
	baseMonths, baseAmount := run(in.ContribBase, 0, in.Deval, in.Goal, in.Deadline)
	out.BaseGoalMonths, out.BaseAmountUAH = baseMonths, state.Major(baseAmount, money.UAH)
	out.BaseGoalDate = goalDate(in.Today, out.BaseGoalMonths)
	out.BaseGoalPct = goalPct(out.BaseAmountUAH.Major(), in.Goal)

	add := func(r state.SensitivityRow, months int, amount, goal float64) {
		r.GoalMonths, r.GoalDate = months, goalDate(in.Today, months)
		r.AmountUAH, r.GoalPct = state.Major(amount, money.UAH), goalPct(amount, goal)
		out.Rows = append(out.Rows, r)
	}

	// --- внесок: єдиний важіль, який людина рухає сама ---
	for _, k := range []float64{0.5, 1.5, 2} {
		c := in.ContribBase * k
		m, a := run(c, 0, in.Deval, in.Goal, in.Deadline)
		add(state.SensitivityRow{Lever: "contrib", Factor: k, Value: round2(c),
			ValueUAH: state.Major(c, money.UAH)}, m, a, in.Goal)
	}

	// --- ті самі два важелі в ОДНАКОВИХ одиницях ---
	//
	// НАВІЩО, КОЛИ ОБИДВА ВЖЕ Є НИЖЧЕ. Тому що нижче вони зрушені
	// НЕСПІВМІРНО: внесок множниками (×0.5, ×1.5, ×2), ставка пунктами
	// (±3 в.п.). З такої пари неможливо прочитати, ЩО СИЛЬНІШЕ, — а це
	// рівно те питання, заради якого картку й заводили: чи варто шукати
	// папір на пів пункта дохідніший, чи дешевше відкласти на тисячу
	// більше. Порівняти можна лише однакові кроки.
	//
	// Крок внеску не константа: тисяча гривень на портфелі в 50 тисяч і на
	// портфелі в п'ять мільйонів — це два різні питання. Береться десята
	// від нинішнього темпу, округлена до круглого числа, щоб рядок читався
	// як дія, а не як результат ділення.
	//
	// Ставка рухається на 1 в.п. — найменший крок, яким людина реально
	// вибирає між паперами.
	//
	// Вироку тут немає, як і в решти рядків (шапка файла): обидва числа
	// стоять поруч, а що з ними робити, вирішує людина.
	if step := niceStep(in.ContribBase * 0.10); step > 0 {
		m, a := run(in.ContribBase+step, 0, in.Deval, in.Goal, in.Deadline)
		add(state.SensitivityRow{Lever: "step_contrib", DeltaUAH: state.Major(step, money.UAH),
			Value: round2(in.ContribBase + step), ValueUAH: state.Major(in.ContribBase+step, money.UAH)},
			m, a, in.Goal)
	}
	{
		m, a := run(in.ContribBase, 1, in.Deval, in.Goal, in.Deadline)
		add(state.SensitivityRow{Lever: "step_rate", DeltaPP: 1, Value: 1}, m, a, in.Goal)
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
		add(state.SensitivityRow{Lever: "goal", Factor: k, Value: round2(goal),
			ValueUAH: state.Major(goal, money.UAH)}, m, out.BaseAmountUAH.Major(), goal)
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

// niceStep — округлення кроку внеску до числа, яке читається як дія.
//
// 1 000 замість 987.43, 500 замість 512. Рядок «відкладай на 987.43 ₴
// більше» формально точніший, але людина такого рішення не ухвалює: вона
// вирішує «на тисячу більше». Точність тут не втрачається — крок і так
// узятий з голови (десята від темпу), і вдавати, що в ньому значущі
// копійки, було б гірше за округлення.
//
// Сходинки ростуть із розміром: 100 → 500 → 1 000 → 5 000 → 10 000…
// Нижче сотні крок не має сенсу — на такому темпі жоден важіль нічого не
// зрушить, і рядка не буде.
func niceStep(v float64) float64 {
	if v < 100 {
		return 0
	}
	mag := math.Pow(10, math.Floor(math.Log10(v)))
	switch n := v / mag; {
	case n < 2:
		return mag
	case n < 7.5:
		return 5 * mag
	default:
		return 10 * mag
	}
}
