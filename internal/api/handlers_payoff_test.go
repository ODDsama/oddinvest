package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/payoff"
)

// Наскрізь через HTTP: три стратегії, чутливість і пільговий блок.
func TestPayoffEndpoint(t *testing.T) {
	srv, _ := testServer(t)
	card := addDebt(t, srv.URL, `{"name":"ПУМБ","kind":"card","currency":"UAH",
		"limit":"200000","statement_day":"30","apr_pct":"47.88","apr_overdue_pct":"62",
		"min_payment_pct":"3","late_fee":"100"}`)
	addDebt(t, srv.URL, `{"name":"Холодильник","kind":"installment","currency":"UAH",
		"card_id":"`+did(card)+`","principal":"30000","payments_total":"9",
		"first_payment_date":"2026-09-30","fee_month_pct":"1.99"}`)
	if resp, out := do(t, "POST", srv.URL+"/api/debt-marks",
		`{"debt_id":"`+did(card)+`","balance":"5000","statement_due":"12000","non_grace":"4000"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("звірка: %d %s", resp.StatusCode, out)
	}

	resp, out := do(t, "GET", srv.URL+"/api/payoff?extra=3000", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/payoff: %d %s", resp.StatusCode, out)
	}
	var got payoffResp
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Strategy != payoff.Avalanche {
		t.Errorf("замовчування %q, чекали лавину", got.Strategy)
	}
	if len(got.Debts) != 2 {
		t.Fatalf("боргів у черзі %d, чекали 2 (розстрочка + готівка картки): %s",
			len(got.Debts), out)
	}
	// Порядок рядків — це черга погашення: першою стоїть найдорожча.
	// Готівка з ліміту під 60% обходить розстрочку під ~50%.
	if got.Debts[0].Rate <= got.Debts[1].Rate {
		t.Errorf("черга не за ставкою: %.2f%% перед %.2f%%",
			got.Debts[0].Rate, got.Debts[1].Rate)
	}
	var inst payoffDebtJSON
	for _, d := range got.Debts {
		if d.Kind == domain.DebtInstallment {
			inst = d
		}
	}
	// Розстрочка з комісією 1,99% коштує ~50% річних, а не 23,88% — це
	// головна знахідка всієї фази.
	if inst.Rate < 45 || inst.Rate > 55 {
		t.Errorf("ставка розстрочки %.2f%%, чекали ~50%%: %+v", inst.Rate, got.Debts)
	}
	if inst.Basis != domain.DebtRateFromSchedule {
		t.Errorf("основа %q, чекали виведену з графіка", inst.Basis)
	}
	// Реальна ставка мусить бути НИЖЧОЮ за номінальну рівно на знецінення.
	if inst.RealPct >= inst.Rate {
		t.Errorf("реальна %.2f не менша за номінальну %.2f", inst.RealPct, inst.Rate)
	}
	if len(got.Compare) != 3 {
		t.Errorf("порівняння стратегій: %d рядків", len(got.Compare))
	}
	if got.Plan.FreeDate == "" || got.Plan.Months == 0 {
		t.Errorf("дати свободи немає: %+v", got.Plan)
	}
	if len(got.Sensitivity) == 0 {
		t.Error("чутливості немає — саме вона відповідає «а якщо ще тисяча»")
	}
	if len(got.Grace) != 1 {
		t.Fatalf("пільгового блоку немає: %s", out)
	}
	g := got.Grace[0]
	if !g.Known || g.DueDate == "" {
		t.Errorf("пільговий блок без дати або без звірки: %+v", g)
	}
	// «Вільно» = 5 000 − 12 000 − частина розстрочки: відʼємне, і саме це
	// і є та пастка, через яку безкоштовний оборот стає боргом.
	if g.Free.Amount[0] != '-' {
		t.Errorf("вільно %s — чекали відʼємне при боргу більшому за баланс", g.Free.Amount)
	}
	if g.MissMinCost.Amount == g.MissFullCost.Amount {
		t.Error("ціни двох помилок злилися")
	}
}
