package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
)

type curveJSON struct {
	Currency string  `json:"currency"`
	Bucket   string  `json:"bucket"`
	Pct      float64 `json:"pct"`
	Date     string  `json:"date"`
	PrevPct  float64 `json:"prev_pct"`
	PrevDate string  `json:"prev_date"`
}

func TestAuctionsCurve(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)
	now := time.Now()
	old := domain.NewDate(now.AddDate(-1, 0, -30)) // трохи більше року тому
	if err := st.SaveAuctions(ctx, []nbu.Auction{
		// Гривня: два строки, обидва мають із чим порівнятись.
		{Date: domain.NewDate(now.AddDate(0, 0, -3)), ISIN: "UA1", Num: "1",
			Currency: money.UAH, Bucket: "1y", IncomeBP: 1519, DaysToRepay: 343},
		{Date: old, ISIN: "UA1", Num: "9",
			Currency: money.UAH, Bucket: "1y", IncomeBP: 1780, DaysToRepay: 350},
		{Date: domain.NewDate(now.AddDate(0, 0, -3)), ISIN: "UA2", Num: "2",
			Currency: money.UAH, Bucket: "2y", IncomeBP: 1610, DaysToRepay: 910},
		// Євро розміщували лише раз: порівнювати нема з чим.
		{Date: domain.NewDate(now.AddDate(0, -1, 0)), ISIN: "UA3", Num: "3",
			Currency: money.EUR, Bucket: "1.5y", IncomeBP: 320, DaysToRepay: 548},
	}); err != nil {
		t.Fatal(err)
	}

	resp, body := do(t, "GET", srv.URL+"/api/auctions/curve", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	var got []curveJSON
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if len(got) != 3 {
		t.Fatalf("пар (валюта, строк) хочемо 3, маємо %d: %s", len(got), body)
	}
	byKey := map[string]curveJSON{}
	for _, r := range got {
		byKey[r.Currency+"|"+r.Bucket] = r
	}
	// Свіже розміщення й те саме рік тому — поруч: сама крива каже, що
	// ринок платить, і мовчить про те, куди він рухається.
	if r := byKey["UAH|1y"]; r.Pct != 15.19 || r.PrevPct != 17.80 {
		t.Errorf("UAH 1y: %+v", r)
	}
	// Строк, який розміщували лише раз, не отримує «рік тому» з самого
	// себе: два однакові стовпчики читались би як «нічого не змінилось»,
	// хоч насправді нічого й не вимірювалось.
	if r := byKey["UAH|2y"]; r.Pct != 16.10 || r.PrevPct != 0 || r.PrevDate != "" {
		t.Errorf("UAH 2y мав лишитись без порівняння: %+v", r)
	}
	if r := byKey["EUR|1.5y"]; r.Pct != 3.20 || r.PrevPct != 0 {
		t.Errorf("EUR 1.5y: %+v", r)
	}
}

// Порожня історія — порожня крива, а не помилка: доти, доки бекфіл не
// відпрацював, показувати нема чого, і картка просто не малюється.
func TestAuctionsCurveEmpty(t *testing.T) {
	srv, _ := testServer(t)
	resp, body := do(t, "GET", srv.URL+"/api/auctions/curve", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	// Саме [], а не null: nil-зріз у JSON стає null, і UI на .map падає.
	if body != "[]\n" {
		t.Errorf("хочемо порожній масив, маємо %q", body)
	}
}
