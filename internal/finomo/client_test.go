package finomo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("фікстура %s: %v", name, err)
	}
	return b
}

func TestParseUAHPage(t *testing.T) {
	got, err := parsePage(fixture(t, "bond_uah.html"), "UA4000239107", "2029-02-07")
	if err != nil {
		t.Fatalf("parsePage: %v", err)
	}
	if got.NominalMinor != 100000 {
		t.Errorf("номінал: %d, чекали 100000", got.NominalMinor)
	}
	// Порядок значущий: найдешевша першою — на цьому тримається «у кого
	// дешевше».
	want := []Quote{
		{Source: "Inzhur", Label: "Inzhur", Date: "2026-09-06", PriceMinor: 101365, Currency: "UAH"},
		{Source: "BtcBroker", Label: "БТС Брокер", Date: "2026-09-06", PriceMinor: 101862, Currency: "UAH"},
		{Source: "Univer", Label: "УНІВЕР", Date: "2026-09-06", PriceMinor: 101862, Currency: "UAH"},
		{Source: "Privat24", Label: "Приват 24", Date: "2026-09-06", PriceMinor: 101953, Currency: "UAH"},
	}
	if len(got.Quotes) != len(want) {
		t.Fatalf("цін: %d, чекали %d: %+v", len(got.Quotes), len(want), got.Quotes)
	}
	for i := range want {
		if got.Quotes[i] != want[i] {
			t.Errorf("ціна %d: %+v, чекали %+v", i, got.Quotes[i], want[i])
		}
	}
}

// НБУ віддає СПРАВЕДЛИВУ ВАРТІСТЬ, а не пропозицію: купити за неї не можна
// ніде. У фікстурі вона 1034.18 при найдешевшій брокерській 1013.65, але
// на інших паперах вона буває найнижчою — і тоді «де дешевше» вказало б у
// порожнечу. Тест сторожить саме фільтр, а не конкретне число.
func TestNBUIsNotAnOffer(t *testing.T) {
	got, err := parsePage(fixture(t, "bond_uah.html"), "UA4000239107", "")
	if err != nil {
		t.Fatalf("parsePage: %v", err)
	}
	for _, q := range got.Quotes {
		if q.Source == "NBU" {
			t.Fatalf("НБУ потрапив у пропозиції: %+v", q)
		}
	}
}

// Валютний папір: ціна у ВАЛЮТІ ПАПЕРА, не в гривні. Плюс тут видно, чому
// «найкраща ціна», яку сайт друкує вгорі сторінки, не годиться нам за
// джерело: у цього паперу вона дорівнює справедливій вартості НБУ, бо
// єдина брокерська точка дворічної свіжості. Ми рахуємо найдешевшу самі й
// з датою, тож таку ціну відсіє перевірка свіжості, а не випадок.
func TestParseUSDPageKeepsBondCurrency(t *testing.T) {
	got, err := parsePage(fixture(t, "bond_usd.html"), "UA4000236806", "2027-03-18")
	if err != nil {
		t.Fatalf("parsePage: %v", err)
	}
	if len(got.Quotes) != 1 {
		t.Fatalf("цін: %d, чекали 1: %+v", len(got.Quotes), got.Quotes)
	}
	q := got.Quotes[0]
	if q.Currency != "USD" {
		t.Errorf("валюта: %q, чекали USD", q.Currency)
	}
	if q.Source != "Privat24" || q.PriceMinor != 102585 || q.Date != "2026-07-09" {
		t.Errorf("ціна: %+v", q)
	}
}

// Погашений папір сторінку має, а продавців — ні. Це відповідь, а не збій:
// прохід мусить порахувати такий папір як «без ціни» й піти далі.
func TestRedeemedBondHasNoOffers(t *testing.T) {
	got, err := parsePage(fixture(t, "bond_redeemed.html"), "UA4000190284", "2026-05-27")
	if err != nil {
		t.Fatalf("parsePage: %v", err)
	}
	if len(got.Quotes) != 0 {
		t.Fatalf("чекали жодної пропозиції, маємо %+v", got.Quotes)
	}
}

// Сторож формату: числа, які розібрались, але стосуються не того паперу, —
// найгірший вигляд зміни чужої розмітки.
func TestMaturityMismatchIsRefused(t *testing.T) {
	_, err := parsePage(fixture(t, "bond_uah.html"), "UA4000239107", "2030-01-01")
	if err == nil {
		t.Fatal("розбіжність погашення мусила стати помилкою")
	}
	if !strings.Contains(err.Error(), "довідник") {
		t.Errorf("помилка мусить називати причину: %v", err)
	}
}

func TestMissingTagFailsLoudly(t *testing.T) {
	_, err := parsePage([]byte("<html><body>нічого</body></html>"), "UA4000239107", "")
	if err == nil {
		t.Fatal("сторінка без тега мусила стати помилкою")
	}
	if !strings.Contains(err.Error(), "bond-page-config") {
		t.Errorf("помилка мусить називати тег: %v", err)
	}
}

func TestWrongISINIsRefused(t *testing.T) {
	if _, err := parsePage(fixture(t, "bond_uah.html"), "UA4000000000", ""); err == nil {
		t.Fatal("сторінка про інший папір мусила стати помилкою")
	}
}

func TestQuotes404IsErrNoBond(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	_, err := New(srv.URL).Quotes(context.Background(), "UA4000239107", "")
	if !errors.Is(err, ErrNoBond) {
		t.Fatalf("чекали ErrNoBond, маємо %v", err)
	}
}

func TestQuotesOverHTTP(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(fixture(t, "bond_uah.html"))
	}))
	defer srv.Close()
	// Регістр ISIN сайт не розрізняє, а наш довідник тримає верхній: клієнт
	// нормалізує сам, щоб виклик не мусив про це знати.
	got, err := New(srv.URL).Quotes(context.Background(), "ua4000239107", domain.Date("2029-02-07"))
	if err != nil {
		t.Fatalf("Quotes: %v", err)
	}
	if asked != "/bond/UA4000239107" {
		t.Errorf("шлях: %q", asked)
	}
	if got.ISIN != "UA4000239107" || len(got.Quotes) != 4 {
		t.Errorf("знімок: %+v", got)
	}
}
