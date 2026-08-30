package api

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"

	"github.com/ODDsama/oddinvest/internal/state"
)

// Прогноз віднімає від внесків те, що піде в подушку й у цілі.
//
// Привід: стартовий капітал він обидва віднімав давно, а місячні внески
// брав із плану цілком — тобто крива щомісяця клала в папери й ту частку,
// яку застосунок сам же відрізав на іншому екрані.

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

// «Скільки план дає» лишається БРУТТО.
//
// На plan_provides_uah стоїть тотожність із ProvidesUAH кожного рядка «Що
// заходить». «Скільки план дає» — питання про план, а не про те, скільки з
// нього дійде до паперів; звести їх до одного числа означало б утратити обидва.
func TestPlanProvidesStaysGrossUnderCeilings(t *testing.T) {
	srv, _ := testServer(t)

	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_expenses":"10000","monthly_expenses_currency":"UAH","reserve_target_months":"6",
		  "reserve_fill_share_pct":"30","goals_fill_share_pct":"30","target_bonds_pct":"100"}`); resp.StatusCode >= 300 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"50000.00","cadence":"month",
		  "from_date":"2026-01-01","invest_pct":"100"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("потік: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/goals",
		`{"name":"Авто","amount":"500000","currency":"UAH"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("ціль: %d %s", resp.StatusCode, b)
	}

	var doc struct {
		PlanProvidesUAH float64 `json:"plan_provides_uah"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	if math.Abs(doc.PlanProvidesUAH-50_000) > 0.01 {
		t.Errorf("plan_provides_uah = %.2f, а план дає 50 000 — стелі з'їли брутто-число",
			doc.PlanProvidesUAH)
	}
}
