package api

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/ODDsama/oddinvest/internal/finomo"
)

// НА ПОРОЖНІЙ БАЗІ ПЕРЕЛІК ПРОДАВЦІВ УЖЕ МУСИТЬ БУТИ.
//
// Це регресія на замкнене коло, у яке робота потрапила на бойовому: ціна
// не йде в квиток без зіставлення брокера з продавцем, а зіставляти нема з
// чим, доки не набрано цін, — і випадайка в довіднику брокерів була
// порожня рівно тоді, коли вона найпотрібніша.
func TestQuoteSourcesListedBeforeAnyPrices(t *testing.T) {
	srv, _ := testServer(t)
	resp, err := http.Get(srv.URL + "/api/quotes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Rows    []any `json:"rows"`
		Sources []struct {
			Key   string `json:"key"`
			Label string `json:"label"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Rows) != 0 {
		t.Fatalf("база мала бути порожня, а цін %d", len(doc.Rows))
	}
	got := map[string]string{}
	for _, s := range doc.Sources {
		got[s.Key] = s.Label
	}
	for key, label := range finomo.KnownSellers {
		if got[key] != label {
			t.Errorf("продавця %s немає в переліку (або без назви: %q) — "+
				"зіставляти брокерів буде нема з чим, доки не набрано цін", key, got[key])
		}
	}
}
