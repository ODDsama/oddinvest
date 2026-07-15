package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/store"
)

func testServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	srv := httptest.NewServer(New(st, nil, log).Handler())
	t.Cleanup(srv.Close)
	return srv, st
}

func seed(t *testing.T, st *store.Store) {
	t.Helper()
	secs := []nbu.Security{{
		Bond: domain.Bond{ISIN: "UA4000227748", Nominal: money.New(100000, money.UAH),
			RateBP: 1655, Maturity: "2027-03-17", Descr: "гривневі військові"},
		Payments: []domain.Payment{
			{ISIN: "UA4000227748", PayDate: "2026-09-16", Type: domain.PayCoupon, PerBond: money.New(8275, money.UAH)},
			{ISIN: "UA4000227748", PayDate: "2027-03-17", Type: domain.PayCoupon, PerBond: money.New(8275, money.UAH)},
			{ISIN: "UA4000227748", PayDate: "2027-03-17", Type: domain.PayRedemption, PerBond: money.New(100000, money.UAH)},
		},
	}}
	if err := st.ReplaceDirectory(context.Background(), secs, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRate(context.Background(), "USD", 441234, "2026-07-15"); err != nil {
		t.Fatal(err)
	}
}

func do(t *testing.T, method, url, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := new(strings.Builder)
	b := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(b)
		buf.Write(b[:n])
		if err != nil {
			break
		}
	}
	resp.Body.Close()
	return resp, buf.String()
}

func TestLotLifecycleAndSummary(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)

	resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"Дія"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання лота: %d %s", resp.StatusCode, body)
	}

	resp, body = do(t, "GET", srv.URL+"/api/summary", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("summary: %d %s", resp.StatusCode, body)
	}
	for _, want := range []string{`"schema":1`, `"invested_uah":4975`, `"next_payment"`, `"2026-09-16"`} {
		if !strings.Contains(body, want) {
			t.Errorf("summary не містить %s: %s", want, body)
		}
	}

	resp, body = do(t, "GET", srv.URL+"/api/positions", "")
	if !strings.Contains(body, `"qty":5`) || !strings.Contains(body, `"4975.00"`) {
		t.Errorf("positions: %s", body)
	}

	// продаж 2 паперів: перевіряємо валідацію і перерахунок
	resp, body = do(t, "POST", srv.URL+"/api/sales",
		`{"lot_id":1,"sale_date":"2026-08-01","qty":2,"clean_per_bond":"1001.00","accrued":"11.30","currency":"UAH"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("продаж: %d %s", resp.StatusCode, body)
	}
	resp, body = do(t, "GET", srv.URL+"/api/sales", "")
	// результат = (2×1001 + 11.30) − 2×995 = 23.30, купонів за володіння не було
	if !strings.Contains(body, `"23.30"`) {
		t.Errorf("realized result: %s", body)
	}

	// oversell після продажу
	resp, _ = do(t, "POST", srv.URL+"/api/sales",
		`{"lot_id":1,"sale_date":"2026-08-02","qty":4,"clean_per_bond":"1001.00","currency":"UAH"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("oversell мав повернути 400, маємо %d", resp.StatusCode)
	}

	// календар: виплати на залишок 3
	resp, body = do(t, "GET", srv.URL+"/api/calendar", "")
	if !strings.Contains(body, `"248.25"`) { // 3 × 82.75
		t.Errorf("календар після продажу: %s", body)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	srv, _ := testServer(t)
	resp, _ := do(t, "PUT", srv.URL+"/api/settings", `{"monthly_target_uah":"5000"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put settings: %d", resp.StatusCode)
	}
	_, body := do(t, "GET", srv.URL+"/api/settings", "")
	if !strings.Contains(body, `"monthly_target_uah":"5000"`) {
		t.Errorf("settings: %s", body)
	}
	resp, _ = do(t, "PUT", srv.URL+"/api/settings", `{"hacker_key":"1"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("невідомий ключ мав дати 400: %d", resp.StatusCode)
	}
}

func TestBondSearchAndAccrued(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	_, body := do(t, "GET", srv.URL+"/api/bonds/search?q=військ", "")
	if !strings.Contains(body, "UA4000227748") {
		t.Errorf("пошук: %s", body)
	}
	_, body = do(t, "GET", srv.URL+"/api/accrued/UA4000227748?on=2026-10-16", "")
	// період 2026-09-16..2027-03-17 (182 дні), 30 днів: 8275×30/182 = 1364.01 -> 13.64
	if !strings.Contains(body, `"13.64"`) {
		t.Errorf("НКД: %s", body)
	}
	_, body = do(t, "GET", srv.URL+"/api/bonds/UA0000000000", "")
	if !strings.Contains(body, "не знайдено") {
		t.Errorf("404: %s", body)
	}
}
