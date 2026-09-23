package engine

import (
	"math"
	"testing"

	"github.com/ODDsama/oddinvest/internal/state"
)

// outsideEnv — план на 10 000 ₴/міс гривнею, без витрат і без валюти.
// Вектори довжиною в горизонт, щоб прохід уперед було де показати.
func outsideEnv(months int) (planTotal, planUAH, incRes, incGoals, expense []float64) {
	planTotal = make([]float64, months)
	planUAH = make([]float64, months)
	incRes = make([]float64, months)
	incGoals = make([]float64, months)
	expense = make([]float64, months)
	for m := 0; m < months; m++ {
		planTotal[m], planUAH[m] = 10_000, 10_000
		incRes[m], incGoals[m] = 10_000, 10_000
	}
	return
}

func outsideInput(resGap, goalGap, resPct, goalPct float64) projectionInput {
	set := &state.SettingsDoc{}
	if resPct > 0 {
		set.ReserveFillSharePct = &resPct
	}
	if goalPct > 0 {
		set.GoalsFillSharePct = &goalPct
	}
	return projectionInput{Settings: set, ReserveGapUAH: resGap, GoalsGapUAH: goalGap}
}

// Обидві стелі ріжуть, і ріжуть рівно свою частку.
func TestSpendOutsideCutsBothCeilings(t *testing.T) {
	total, uah, res, goals, exp := outsideEnv(3)
	// Стелі 30% і 20% від 10 000 = 3 000 і 2 000; розривів вистачає на всі
	// три місяці.
	spendOutside(outsideInput(100_000, 100_000, 30, 20), total, uah, nil, res, goals, exp)

	for m := 0; m < 3; m++ {
		if math.Abs(uah[m]-5_000) > 0.01 {
			t.Errorf("місяць %d: у папери йде %.2f, а мало 10 000 − 3 000 − 2 000 = 5 000",
				m+1, uah[m])
		}
		if math.Abs(total[m]-10_000) > 0.01 {
			t.Errorf("місяць %d: planTotal став %.2f — його чіпати не можна, "+
				"на ньому стоїть plan_provides_uah", m+1, total[m])
		}
	}
}

// Стеля ЗАМОВКАЄ, коли розрив закрився.
//
// Це головне в усьому проході: подушка на шість місяців витрат збереться й
// перестане брати. Стала вирізка різала б їй частку всі шістдесят років
// горизонту — тобто збрехала б сильніше, ніж те, що було доти.
func TestSpendOutsideStopsWhenGapIsClosed(t *testing.T) {
	total, uah, res, goals, exp := outsideEnv(4)
	// Розрив подушки 4 500 при стелі 3 000/міс: повний місяць, залишок
	// 1 500 у другому, далі тиша. Цілей немає взагалі.
	spendOutside(outsideInput(4_500, 0, 30, 0), total, uah, nil, res, goals, exp)

	want := []float64{7_000, 8_500, 10_000, 10_000}
	for m, w := range want {
		if math.Abs(uah[m]-w) > 0.01 {
			t.Errorf("місяць %d: у папери %.2f, чекали %.2f", m+1, uah[m], w)
		}
	}
}

// Без стель прогноз лишається таким, як був.
//
// Той, хто подушки й цілей не заводив, не мусить побачити жодної зміни, —
// і саме це відрізняє виправлення від нового правила.
func TestSpendOutsideSilentWithoutCeilings(t *testing.T) {
	total, uah, res, goals, exp := outsideEnv(3)
	spendOutside(outsideInput(100_000, 100_000, 0, 0), total, uah, nil, res, goals, exp)
	spendOutside(projectionInput{}, total, uah, nil, res, goals, exp)

	for m := 0; m < 3; m++ {
		if math.Abs(uah[m]-10_000) > 0.01 {
			t.Errorf("місяць %d: внесок поїхав до %.2f без жодної стелі", m+1, uah[m])
		}
	}
}

// Витрати віднімаються з бази стелі ПОВНІСТЮ, як у buildMonthPlan.
func TestSpendOutsideSubtractsExpensesFromBase(t *testing.T) {
	total, uah, res, goals, exp := outsideEnv(1)
	exp[0] = 6_000
	total[0], uah[0] = 4_000, 4_000
	// База стелі: 10 000 дозволених − 6 000 витрат = 4 000; 30% = 1 200.
	spendOutside(outsideInput(100_000, 0, 30, 0), total, uah, nil, res, goals, exp)

	if math.Abs(uah[0]-2_800) > 0.01 {
		t.Errorf("у папери %.2f, чекали 4 000 − 1 200 = 2 800", uah[0])
	}
}

// Вирізка ділиться між гривнею й валютою ПРОПОРЦІЙНО їхній частці місяця.
func TestSpendOutsideSplitsProportionallyAcrossCurrencies(t *testing.T) {
	total, uah, res, goals, exp := outsideEnv(1)
	// Місяць на 10 000 ₴: 6 000 гривнею і 4 000 еквівалента в доларі.
	uah[0] = 6_000
	native := map[string][]float64{"USD": {100}} // 100 $ ≈ 4 000 ₴
	spendOutside(outsideInput(100_000, 0, 50, 0), total, uah, native, res, goals, exp)

	// Стеля 50% від 10 000 = 5 000, тобто лишається половина кожного.
	if math.Abs(uah[0]-3_000) > 0.01 {
		t.Errorf("гривнева нога %.2f, чекали половину від 6 000", uah[0])
	}
	if math.Abs(native["USD"][0]-50) > 0.01 {
		t.Errorf("валютна нога %.2f, чекали половину від 100", native["USD"][0])
	}
}

// Борг у прогнозі — ЗА ГРАФІКОМ, а не сталою сумою сьогоднішнього місяця.
// Розстрочка на три місяці по 3 000 ₴ ріже рівно три місяці й звільняє
// гроші з четвертого; картка платить мінімалку, доки не вичерпано її
// залишок. Доти прогноз повторював обовʼязкове ЦЬОГО місяця, доки не
// вичерпувався сумарний залишок, — і розстрочка, що закінчується в
// березні, «платилась» місяцями довше, а картка — сумою чужого графіка.
func TestSpendOutsideDebtFollowsSchedule(t *testing.T) {
	total, uah, res, goals, exp := outsideEnv(6)
	in := projectionInput{
		InstallmentDueByMonth: []float64{3_000, 3_000, 3_000},
		CardDueUAH:            500, CardLeftUAH: 1_200,
	}
	spendOutside(in, total, uah, nil, res, goals, exp)
	// Місяці 0–1: 3 000 розстрочки + 500 картки; 2: 3 000 + 200 (залишок
	// картки); 3–5: нічого.
	want := []float64{6_500, 6_500, 6_800, 10_000, 10_000, 10_000}
	for m, w := range want {
		if math.Abs(uah[m]-w) > 0.01 {
			t.Errorf("місяць %d: до паперів %.2f, чекали %.2f", m, uah[m], w)
		}
	}
}
