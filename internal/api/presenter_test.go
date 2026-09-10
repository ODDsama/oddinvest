package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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
	ProjectionRatePct   float64            `json:"projection_rate_pct"`
	ProjectionRateReal  float64            `json:"projection_rate_real_pct"`
	TotalReturn         *struct {
		GainUAH state.Money `json:"gain_uah"`
		XIRRPct *float64    `json:"xirr_pct"`
	} `json:"total_return"`
	Settings struct {
		ReportCurrency string `json:"report_currency"`
	} `json:"settings"`
	Tasks []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"tasks"`
}

// taskTitle — заголовок задачі за id; порожньо, коли її немає.
func (v summaryView) taskTitle(id string) string {
	for _, t := range v.Tasks {
		if t.ID == id {
			return t.Title
		}
	}
	return ""
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
	// І той самий курс на день купівлі: зведений результат у доларах
	// перекладає кожен потік курсом ЙОГО дати й мовчить, коли курсу
	// бракує хоч на один (усе або нічого, state_xirr.go).
	if err := st.SaveRate(context.Background(), "USD", 441234, "2026-07-01"); err != nil {
		t.Fatal(err)
	}
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
	// Лінійка одна: номінал бере реальну, реальна лишається рівною йому.
	if usd.BlendedYieldPct != uah.BlendedYieldRealPct || usd.BlendedYieldRealPct != uah.BlendedYieldRealPct {
		t.Errorf("лінійка: у доларі головною стає реальна (%v), а не номінал (%v); дістали %v / %v",
			uah.BlendedYieldRealPct, uah.BlendedYieldPct, usd.BlendedYieldPct, usd.BlendedYieldRealPct)
	}
	if usd.Settings.ReportCurrency != "USD" {
		t.Errorf("налаштування в документі: %+v", usd.Settings)
	}
	// Проза задач — теж у валюті звітності: рядок презентер не бачить, тож
	// текст пишеться одразу тим самим курсом (moneyText). «Купувати ще
	// рано — бракує 1 080,02 ₴» стає «…бракує 24,48 $».
	if got := usd.taskTitle("saving"); !strings.Contains(got, "$") || strings.Contains(got, "₴") {
		t.Errorf("проза задачі не в доларах: %q (у гривні було %q)", got, uah.taskTitle("saving"))
	}
	if got := uah.taskTitle("saving"); !strings.Contains(got, "₴") {
		t.Errorf("у гривні проза мусить лишитись гривневою: %q", got)
	}
	// Ставка проєкції — теж лінійка: у доларі стоїть реальна.
	if usd.ProjectionRatePct != uah.ProjectionRateReal || usd.ProjectionRateReal != uah.ProjectionRateReal || uah.ProjectionRateReal == 0 {
		t.Errorf("ставка проєкції: гривня %v/%v, долар %v/%v",
			uah.ProjectionRatePct, uah.ProjectionRateReal, usd.ProjectionRatePct, usd.ProjectionRateReal)
	}
	// Зведений результат рахується в доларах на дату кожного потоку, а не
	// ділиться на сьогоднішній курс. Тут курс один на всі дати, тож числа
	// сходяться з поділеним — з точністю до цента: кожен потік округлюється
	// окремо, і сума округлених не дорівнює округленій сумі. Річна ставка
	// та сама лише тому, що курс сталий.
	if uah.TotalReturn == nil || usd.TotalReturn == nil {
		t.Fatalf("total_return: %v / %v", uah.TotalReturn, usd.TotalReturn)
	}
	got, want := usd.TotalReturn.GainUAH.Minor(), uah.TotalReturn.GainUAH.In("USD", uah.Rates["USD"]).Minor()
	if got < want-1 || got > want+1 {
		t.Errorf("gain у доларах: %d, чекали %d ±1", got, want)
	}
	if d := *usd.TotalReturn.XIRRPct - *uah.TotalReturn.XIRRPct; d > 0.1 || d < -0.1 {
		t.Errorf("XIRR при сталому курсі мусить сходитись: %v / %v", *usd.TotalReturn.XIRRPct, *uah.TotalReturn.XIRRPct)
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
