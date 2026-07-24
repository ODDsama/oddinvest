package nbu

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

func TestParseSecuritiesFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/securities_sample.json")
	if err != nil {
		t.Fatal(err)
	}
	var rs []rawSecurity
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&rs); err != nil {
		t.Fatal(err)
	}
	secs, err := parseSecurities(rs)
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != 2 {
		t.Fatalf("очікували 2 папери, маємо %d", len(secs))
	}

	ua := secs[0]
	if ua.Bond.ISIN != "UA4000227748" || ua.Bond.Nominal.Amount() != 100000 ||
		ua.Bond.Nominal.Currency().Code != money.UAH {
		t.Errorf("UAH bond: %+v", ua.Bond)
	}
	if ua.Bond.RateBP != 1655 {
		t.Errorf("ставка 16.55%% -> 1655 б.п., маємо %d", ua.Bond.RateBP)
	}
	if ua.Bond.Maturity != domain.Date("2027-03-17") {
		t.Errorf("погашення ISO: %s", ua.Bond.Maturity)
	}
	// 82.75 грн -> 8275 коп, без жодного float64 по дорозі
	if ua.Payments[0].PerBond.Amount() != 8275 || ua.Payments[0].Type != domain.PayCoupon {
		t.Errorf("купон: %+v", ua.Payments[0])
	}
	if ua.Payments[2].Type != domain.PayRedemption || ua.Payments[2].PerBond.Amount() != 100000 {
		t.Errorf("погашення: %+v", ua.Payments[2])
	}

	usd := secs[1]
	if usd.Bond.Nominal.Currency().Code != money.USD || usd.Bond.RateBP != 324 {
		t.Errorf("USD bond: %+v", usd.Bond)
	}
	// дата у форматі 17.09.2027 нормалізується в ISO
	if usd.Bond.Maturity != domain.Date("2027-09-17") {
		t.Errorf("нормалізація дати: %s", usd.Bond.Maturity)
	}
	if usd.Payments[0].PerBond.Amount() != 1620 { // $16.20
		t.Errorf("USD купон: %d", usd.Payments[0].PerBond.Amount())
	}
}

func TestRateEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"r030":840,"txt":"Долар США","rate":44.1234,"cc":"USD","exchangedate":"15.07.2026"}]`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	got, err := c.Rate(context.Background(), "usd")
	if err != nil {
		t.Fatal(err)
	}
	if got != 441234 {
		t.Errorf("курс ×10⁴ = %d, хочемо 441234", got)
	}
}

// Історичний курс: НБУ приймає &date=YYYYMMDD, і без цього виміряти
// знецінення довгим вікном не було б з чого — добова джоба наповнює
// таблицю лише вперед від дня встановлення.
func TestRateOnSendsDateAndReturnsQuoteDate(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`[{"r030":840,"txt":"Долар США","rate":24.000667,"cc":"USD","exchangedate":"04.01.2016"}]`))
	}))
	defer srv.Close()
	c := New(srv.URL)

	rate, quoted, err := c.RateOn(context.Background(), "usd", "2016-01-04")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotQuery, "date=20160104") {
		t.Errorf("дата мала піти в запит без роздільників, маємо %q", gotQuery)
	}
	if rate != 240007 {
		t.Errorf("курс ×10⁴ = %d, хочемо 240007", rate)
	}
	// Дату котирування беремо з відповіді, а не з годинника: на вихідний
	// НБУ віддає курс попереднього робочого дня, і записувати його під
	// запитаним числом означало б вигадати котирування.
	if quoted != domain.Date("2016-01-04") {
		t.Errorf("дата котирування = %q", quoted)
	}
}

// Без дати — та сама поведінка, що й була: запит на сьогодні.
func TestRateOnWithoutDateAsksToday(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`[{"rate":44.811,"cc":"USD","exchangedate":"24.07.2026"}]`))
	}))
	defer srv.Close()
	if _, _, err := New(srv.URL).RateOn(context.Background(), "usd", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotQuery, "date=") {
		t.Errorf("без дати параметра бути не мало, маємо %q", gotQuery)
	}
}

func TestParseNBUDateFormats(t *testing.T) {
	for in, want := range map[string]domain.Date{
		"2027-03-17":          "2027-03-17",
		"17.03.2027":          "2027-03-17",
		"20270317":            "2027-03-17",
		"2027-03-17T00:00:00": "2027-03-17",
	} {
		got, err := parseNBUDate(in)
		if err != nil || got != want {
			t.Errorf("parseNBUDate(%s) = %s, %v", in, got, err)
		}
	}
	if _, err := parseNBUDate("березень 2027"); err == nil {
		t.Error("очікували помилку на нерозпізнаному форматі")
	}
}
