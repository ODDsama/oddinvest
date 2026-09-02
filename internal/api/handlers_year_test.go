package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func yearOf(t *testing.T, srv string, year string) yearResp {
	t.Helper()
	url := srv + "/api/year"
	if year != "" {
		url += "?year=" + year
	}
	resp, body := do(t, "GET", url, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: %d %s", url, resp.StatusCode, body)
	}
	var got yearResp
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return got
}

// Рік і звіт про рух за той самий рік — ті самі гроші, і дні хітмапу в
// сумі дають ті самі статті: хітмап — це виписка, розкладена по днях, а
// не другий рахунок.
func TestYearMoneyAgreesWithCashflowAndDays(t *testing.T) {
	srv, st := testServer(t)
	seedPeriodMonth(t, st)

	got := yearOf(t, srv.URL, "2026")

	resp, body := do(t, "GET", srv.URL+"/api/cashflow?from=2026-01-01&to=2026-12-31", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/cashflow: %d %s", resp.StatusCode, body)
	}
	var cf struct {
		IncomeUAH   float64 `json:"income_uah"`
		ContribUAH  float64 `json:"contributed_uah"`
		PurchaseUAH float64 `json:"purchased_uah"`
		ClosingUAH  float64 `json:"closing_uah"`
	}
	if err := json.Unmarshal([]byte(body), &cf); err != nil {
		t.Fatal(err)
	}
	m := got.Money
	if m.IncomeUAH != cf.IncomeUAH || m.ContribUAH != cf.ContribUAH ||
		m.PurchaseUAH != cf.PurchaseUAH || m.ClosingUAH != cf.ClosingUAH {
		t.Errorf("рік %+v розійшовся з рухом %+v", m, cf)
	}
	var contrib, income, purchase float64
	for _, d := range got.Days {
		contrib += d.ContribUAH
		income += d.IncomeUAH
		purchase -= d.PurchaseUAH
		if d.Lvl < 1 || d.Lvl > 4 {
			t.Errorf("%s: рівень %d поза 1..4", d.Date, d.Lvl)
		}
	}
	// Дні несуть свої гроші РАЗОМ із подушкою (own_uah), а не лише
	// гаманець (contributed_uah).
	if round2(contrib) != m.OwnUAH || round2(income) != m.IncomeUAH ||
		round2(purchase) != m.PurchaseUAH {
		t.Errorf("дні (%v/%v/%v) не сходяться зі статтями %+v", contrib, income, purchase, m)
	}
	if got.EarnedUAH+got.PrincipalUAH != m.IncomeUAH {
		t.Errorf("зароблене %v + тіло %v ≠ дохід %v", got.EarnedUAH, got.PrincipalUAH, m.IncomeUAH)
	}
	if len(got.Years) == 0 || got.Years[0] < 2026 {
		t.Errorf("роки %v мають починатись із поточного", got.Years)
	}
}

// Рік без жодної події не падає й не вигадує чисел; неправильний рік —
// 400, а не порожній рік.
func TestYearEmptyAndBadInput(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	got := yearOf(t, srv.URL, "2019")
	if got.Money.ClosingUAH != 0 || len(got.Days) != 0 || got.BestMonth != nil {
		t.Errorf("порожній рік мав бути порожнім: %+v", got)
	}
	if got.Partial {
		t.Error("2019 давно закрився — не partial")
	}
	if resp, _ := do(t, "GET", srv.URL+"/api/year?year=abc", ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("рік «abc» мав дати 400, дав %d", resp.StatusCode)
	}
}

// Квартилі, а не частка від максимуму: один великий внесок не робить
// решту року блідою.
func TestYearHeatLevelsByQuartile(t *testing.T) {
	byDay := map[string]*yearDay{}
	for i, v := range []float64{10, 20, 30, 40, 1_000_000} {
		d := &yearDay{Date: "2026-01-0" + string(rune('1'+i)), ContribUAH: v}
		byDay[d.Date] = d
	}
	days := heatDays(byDay)
	if len(days) != 5 {
		t.Fatalf("днів %d, чекали 5", len(days))
	}
	if days[0].Lvl != 1 || days[4].Lvl != 4 || days[2].Lvl == 1 {
		t.Errorf("рівні %v: перший 1, останній 4, середній не 1", days)
	}
}
