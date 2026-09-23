package api

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
)

// Прогноз віднімає від внесків те, що піде в подушку й у цілі.
//
// Привід: стартовий капітал він обидва віднімав давно, а місячні внески
// брав із плану цілком — тобто крива щомісяця клала в папери й ту частку,
// яку застосунок сам же відрізав на іншому екрані.

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
