package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ODDsama/oddinvest/internal/state"
)

type summaryView struct {
	Currency            string             `json:"currency"`
	CurrencyNote        string             `json:"currency_note"`
	InvestedUAH         state.Money        `json:"invested_uah"`
	NominalUAHEq        state.Money        `json:"nominal_uah_eq"`
	Rates               map[string]float64 `json:"rates"`
	BlendedYieldPct     float64            `json:"blended_yield_pct"`
	BlendedYieldRealPct float64            `json:"blended_yield_real_pct"`
	PortfolioYieldPct   float64            `json:"portfolio_yield_pct"`
	Settings            struct {
		ReportCurrency string `json:"report_currency"`
	} `json:"settings"`
}

func summaryOf(t *testing.T, url string) summaryView {
	t.Helper()
	resp, body := do(t, "GET", url+"/api/summary", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("summary: %d %s", resp.StatusCode, body)
	}
	var v summaryView
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// /api/summary у валюті звітності: суми поділені на курс, курс сам не
// чіпається, лінійка дохідності — реальна, гривневий номінал не
// публікується.
func TestSummaryInReportCurrency(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st) // курс USD 44.1234 на 2026-07-15
	resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"Дія"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, body)
	}
	uah := summaryOf(t, srv.URL)
	if uah.Currency != "UAH" || uah.CurrencyNote != "" {
		t.Fatalf("за замовчуванням гривня без примітки: %+v", uah)
	}

	resp, body = do(t, "PUT", srv.URL+"/api/settings", `{"report_currency":"USD"}`)
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		t.Fatalf("settings: %d %s", resp.StatusCode, body)
	}
	usd := summaryOf(t, srv.URL)
	if usd.Currency != "USD" || usd.CurrencyNote != "" {
		t.Fatalf("валюта звітності: %+v", usd)
	}
	// 4 975 ₴ / 44.1234 = 112.75 $ — ділення до копійки, не «приблизно».
	// Порівнюємо копійки: число з дроту валюти не несе, її каже currency.
	if got := usd.InvestedUAH.Minor(); got != 11275 {
		t.Errorf("invested у доларах: %v", usd.InvestedUAH)
	}
	if usd.NominalUAHEq.Minor() != 11332 {
		t.Errorf("nominal у доларах: %v", usd.NominalUAHEq)
	}
	if usd.Rates["USD"] != uah.Rates["USD"] {
		t.Errorf("курс — не сума, він не перекладається: %v → %v", uah.Rates, usd.Rates)
	}
	if usd.BlendedYieldPct != uah.BlendedYieldRealPct || usd.BlendedYieldRealPct != 0 {
		t.Errorf("лінійка: у доларі головною стає реальна (%v), а не номінал (%v); дістали %v / %v",
			uah.BlendedYieldRealPct, uah.BlendedYieldPct, usd.BlendedYieldPct, usd.BlendedYieldRealPct)
	}
	if usd.Settings.ReportCurrency != "USD" {
		t.Errorf("налаштування в документі: %+v", usd.Settings)
	}
}

// Попросили долар, а курсу ще немає: гривня і примітка, а не 500 і не нулі.
func TestSummaryFallsBackWithoutRate(t *testing.T) {
	srv, st := testServer(t)
	if err := st.SetSetting(context.Background(), reportCurrencyKey, "USD"); err != nil {
		t.Fatal(err)
	}
	v := summaryOf(t, srv.URL)
	if v.Currency != "UAH" {
		t.Errorf("без курсу мусить лишитись гривня, дістали %q", v.Currency)
	}
	if v.CurrencyNote == "" {
		t.Error("примітка про відсутній курс порожня")
	}
}

// Документ, який іде в добовий знімок, презентер не бачить: BuildStateDoc
// віддає сиру гривню незалежно від валюти звітності.
func TestBuildStateDocStaysInBookCurrency(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if err := st.SetSetting(context.Background(), reportCurrencyKey, "USD"); err != nil {
		t.Fatal(err)
	}
	do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"Дія"}`)
	s := New(st, nil, testLogger())
	doc, err := s.BuildStateDoc(context.Background(), goldenNow)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Currency != "UAH" || doc.InvestedUAH != state.UAH(497_500) {
		t.Errorf("сирий документ: %s %v", doc.Currency, doc.InvestedUAH)
	}
}
