package api

import (
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// mine — зіставлення, як його бачить відбір: ключ продавця -> назва мого
// брокера.
var mineBrokers = map[string]string{"Inzhur": "inzhur", "Privat24": "privat24"}

// pq — котировка в порядку, який віддає LatestQuotes (дешевші першими).
func pq(source string, date domain.Date, minor int64) store.Quote {
	return store.Quote{ISIN: "UA4000236228", Source: source, Date: date,
		PriceMinor: minor, Currency: money.UAH, Origin: store.QuoteOriginFinomo}
}

// РЕГРЕСІЯ НА ЖИВИЙ ВИПАДОК.
//
// UA4000236228 на бойовому мав учорашню ціну Privat24 (1 097,09) і ціну
// Inzhur місячної давнини (1 087,18). Відбір ішов самою лише ціною, тож
// вигравав Inzhur — і провалював перевірку віку вже в bondUnitCost,
// забираючи з собою свіжу дорожчу. Рядок показував 1 084,56 за номіналом і
// дохідність 17,9 % замість справжніх 15,0 %: тобто ЗАВИЩУВАВ дохідність
// майже на три відсоткові пункти саме там, де ціну ми насправді знали.
func TestPickSkipsCheaperStaleQuote(t *testing.T) {
	on := domain.Date("2026-09-07")
	all := []store.Quote{
		pq("Inzhur", "2026-08-19", 108718),   // дешевша, але 19 днів
		pq("Privat24", "2026-09-06", 109709), // дорожча, зате вчорашня
	}
	got := pickQuotes(all, mineBrokers, on)["UA4000236228"]
	if got.Best == nil {
		t.Fatal("свіжа ціна була — рядок не мав лишитись без ціни")
	}
	if got.Best.Source != "Privat24" || got.Best.PriceMinor != 109709 {
		t.Errorf("обрано %+v, чекали свіжу Privat24", *got.Best)
	}
	// Другої СВІЖОЇ ціни немає, тож і порівнювати нема з чим: показати
	// поруч місячну котировку означало б звірити сьогоднішнє з позаминулим.
	if got.Alt != nil {
		t.Errorf("протухла ціна не мала стати другим числом: %+v", *got.Alt)
	}
}

// Коли свіжі обидві — виграє дешевша, і друга стає числом для порівняння.
func TestPickTakesCheapestAmongFresh(t *testing.T) {
	on := domain.Date("2026-09-07")
	all := []store.Quote{
		pq("Inzhur", "2026-09-06", 101365),
		pq("Privat24", "2026-09-05", 101953),
	}
	got := pickQuotes(all, mineBrokers, on)["UA4000236228"]
	if got.Best == nil || got.Best.Source != "Inzhur" {
		t.Fatalf("найдешевша свіжа мала виграти: %+v", got.Best)
	}
	if got.Label != "inzhur" {
		t.Errorf("назва мого брокера %q", got.Label)
	}
	if got.Alt == nil || got.Alt.Source != "Privat24" {
		t.Errorf("друге число мало бути Privat24: %+v", got.Alt)
	}
}

// Продавець, у якого рахунку немає, у відборі не бере участі — навіть
// найдешевший і найсвіжіший.
func TestPickIgnoresUnmappedSeller(t *testing.T) {
	on := domain.Date("2026-09-07")
	all := []store.Quote{
		pq("Kinto", "2026-09-06", 100000),
		pq("Privat24", "2026-09-06", 109709),
	}
	got := pickQuotes(all, mineBrokers, on)["UA4000236228"]
	if got.Best == nil || got.Best.Source != "Privat24" {
		t.Errorf("обрано %+v — Kinto не зіставлений, купити там нічого", got.Best)
	}
}

// Усі протухли — ціни немає зовсім, і це чесна відповідь: далі
// bondUnitCost віддасть номінал плюс НКД і скаже про це словами.
func TestPickEmptyWhenAllStale(t *testing.T) {
	on := domain.Date("2026-09-07")
	all := []store.Quote{
		pq("Inzhur", "2026-08-19", 108718),
		pq("Privat24", "2026-07-01", 109709),
	}
	if got := pickQuotes(all, mineBrokers, on)["UA4000236228"]; got.Best != nil {
		t.Errorf("протухле не мало стати ціною: %+v", *got.Best)
	}
}
