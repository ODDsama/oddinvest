package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/store"
)

type taxJSON struct {
	Year     int     `json:"year"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	GrossUAH float64 `json:"gross_uah"`
	TaxUAH   float64 `json:"tax_uah"`
	ByKind   []struct {
		Kind     string  `json:"kind"`
		GrossUAH float64 `json:"gross_uah"`
		TaxUAH   float64 `json:"tax_uah"`
	} `json:"by_kind"`
}

func getTax(t *testing.T, base, query string) taxJSON {
	t.Helper()
	resp, body := do(t, "GET", base+"/api/tax?"+query, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/tax?%s дав %d: %s", query, resp.StatusCode, body)
	}
	var out taxJSON
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("розбір: %v (%s)", err, body)
	}
	return out
}

// TestTaxDefaultsToCalendarYear — період за замовчуванням це РІК, а не
// ковзні дванадцять місяців.
//
// Доти /api/tax типово брав «сьогодні мінус рік», а /api/export/csv поруч
// — календарний рік. Декларація річна, тож ковзне вікно не відповідало на
// жодне питання, яке справді ставлять, і головне — два маршрути про одні
// й ті самі гроші міряли різні відрізки.
func TestTaxDefaultsToCalendarYear(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	got := getTax(t, srv.URL, "")
	year := time.Now().Year()
	if got.Year != year {
		t.Errorf("рік = %d, очікували %d", got.Year, year)
	}
	// Межі рахуються від поточного року, а не зашиті числом: інакше тест
	// зеленів би до 31 грудня й падав першого січня.
	wantFrom := fmt.Sprintf("%d-01-01", year)
	wantTo := fmt.Sprintf("%d-12-31", year)
	if got.From != wantFrom || got.To != wantTo {
		t.Errorf("період %s → %s, очікували %s → %s", got.From, got.To, wantFrom, wantTo)
	}
}

// TestTaxYearParamWins — ?year= головніший за пару from/to, а сміття в
// ньому дає помилку, а не мовчазний нуль.
func TestTaxYearParamWins(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)

	got := getTax(t, srv.URL, "year=2024&from=2020-01-01&to=2020-12-31")
	if got.Year != 2024 || got.From != "2024-01-01" || got.To != "2024-12-31" {
		t.Errorf("year мав перемогти пару: %+v", got)
	}

	// Пара без року: період її, а year порожній — підписувати довільний
	// відрізок роком не можна.
	got = getTax(t, srv.URL, "from=2024-03-01&to=2024-05-31")
	if got.Year != 0 || got.From != "2024-03-01" || got.To != "2024-05-31" {
		t.Errorf("пара from/to: %+v", got)
	}

	for _, bad := range []string{"year=позаминулий", "year=20226", "year=0"} {
		if resp, body := do(t, "GET", srv.URL+"/api/tax?"+bad, ""); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s дав %d, очікували 400: %s", bad, resp.StatusCode, body)
		}
	}
	// Перевернутий період — теж помилка, а не порожній результат.
	if resp, _ := do(t, "GET", srv.URL+"/api/tax?from=2024-12-31&to=2024-01-01", ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("перевернутий період дав %d, очікували 400", resp.StatusCode)
	}
}

// TestTaxYearHelperIsShared — обидва маршрути беруть період з одного
// місця. Перевіряється сам помічник: інакше довелось би порівнювати
// заголовок CSV із JSON, а це тест про формат, а не про період.
func TestTaxYearHelperIsShared(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	y, from, to, err := taxYear(url.Values{}, now)
	if err != nil || y != 2026 || from != "2026-01-01" || to != "2026-12-31" {
		t.Fatalf("типовий період: %d %s→%s (%v)", y, from, to, err)
	}
	// Порожнє `to` при заданому `from` означає «по сьогодні», а не «по
	// кінець часів»: майбутніх податків не буває.
	_, _, to2, err := taxYear(url.Values{"from": {"2026-01-01"}}, now)
	if err != nil || to2 != "2026-08-14" {
		t.Errorf("from без to: %s (%v)", to2, err)
	}
}

// TestTaxAndCSVAgreeOnPeriod — картка й вивантаження бачать ОДНІ Й ТІ САМІ
// події.
//
// Це головний тест треку: доти обидва маршрути читали той самий портфель
// через різні відрізки часу, тож зійтись могли лише випадково. Купон
// поза роком не має потрапити ні туди, ні туди.
func TestTaxAndCSVAgreeOnPeriod(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)
	const isin = "UA4000888888"
	// Два купони: один торік, другий цьогоріч. Погашення далеко попереду,
	// щоб папір лишався в портфелі.
	year := time.Now().Year()
	thisYear := domain.Date(time.Date(year, 3, 17, 0, 0, 0, 0, time.UTC).Format("2006-01-02"))
	lastYear := domain.Date(time.Date(year-1, 3, 17, 0, 0, 0, 0, time.UTC).Format("2006-01-02"))
	maturity := domain.Date(time.Date(year+2, 3, 17, 0, 0, 0, 0, time.UTC).Format("2006-01-02"))
	if err := st.ReplaceDirectory(ctx, []nbu.Security{{
		Bond: domain.Bond{ISIN: isin, Nominal: money.New(100000, money.UAH),
			RateBP: 1600, Maturity: maturity},
		Payments: []domain.Payment{
			{ISIN: isin, PayDate: lastYear, Type: domain.PayCoupon, PerBond: money.New(8000, money.UAH)},
			{ISIN: isin, PayDate: thisYear, Type: domain.PayCoupon, PerBond: money.New(8000, money.UAH)},
			{ISIN: isin, PayDate: maturity, Type: domain.PayRedemption, PerBond: money.New(100000, money.UAH)},
		},
	}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	buy := domain.Date(time.Date(year-2, 1, 10, 0, 0, 0, 0, time.UTC).Format("2006-01-02"))
	if _, err := st.AddLot(ctx, domain.Lot{
		ISIN: isin, Qty: 10, PricePerBond: money.New(100000, money.UAH),
		Fee: money.New(0, money.UAH), BuyDate: buy, Channel: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}

	// Купон цього року — у вікні обох маршрутів.
	got := getTax(t, srv.URL, "")
	if got.GrossUAH == 0 {
		t.Fatalf("податкова картка порожня, хоч купон цього року є: %+v", got)
	}
	resp, csvBody := do(t, "GET", srv.URL+"/api/export/csv", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("csv дав %d", resp.StatusCode)
	}
	if !strings.Contains(csvBody, string(thisYear)) {
		t.Errorf("купон %s не потрапив у CSV:\n%s", thisYear, csvBody)
	}
	if strings.Contains(csvBody, string(lastYear)) {
		t.Errorf("купон %s з ТОРІШНЬОГО року потрапив у CSV за цей рік:\n%s", lastYear, csvBody)
	}

	// Той самий рік явно — обидва маршрути мусять погодитись і на минулому.
	prev := getTax(t, srv.URL, "year="+string(lastYear)[:4])
	if prev.GrossUAH == 0 {
		t.Errorf("за минулий рік картка порожня, хоч купон там є: %+v", prev)
	}
	_, prevCSV := do(t, "GET", srv.URL+"/api/export/csv?year="+string(lastYear)[:4], "")
	if !strings.Contains(prevCSV, string(lastYear)) {
		t.Errorf("CSV за минулий рік не містить купона %s:\n%s", lastYear, prevCSV)
	}
	if strings.Contains(prevCSV, string(thisYear)) {
		t.Errorf("CSV за минулий рік містить цьогорічний купон %s", thisYear)
	}
}

// Купони ОВДП звільнені від податку — це закон, а не налаштування, тож
// нуль у рядку «Купони ОВДП» мусить лишатись нулем.
func TestTaxBondCouponsAreExempt(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)
	seed(t, st)
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: domain.NewDate(time.Now()), Amount: 100_000_00,
		Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	got := getTax(t, srv.URL, "")
	for _, l := range got.ByKind {
		if l.Kind == "bond" && l.TaxUAH != 0 {
			t.Errorf("з купонів ОВДП утримано %v — вони звільнені", l.TaxUAH)
		}
	}
}
