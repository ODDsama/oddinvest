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

	"github.com/ODDsama/oddinvest/internal/store"
)

func testHub(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	hub := NewHub(st, New(st, nil, log), log, nil)
	if err := hub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(hub.Handler())
	t.Cleanup(srv.Close)
	return srv, st
}

// doP — як do, але із заголовками (портфель, cookie).
func doP(t *testing.T, method, url, body string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
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

func inPortfolio(slug string) map[string]string { return map[string]string{portfolioHeader: slug} }

const testLot = `{"isin":"UA4000227748","qty":10,"price_per_bond":"1000.00","buy_date":"2026-07-01","channel":"inzhur"}`

func TestHubPortfoliosCRUD(t *testing.T) {
	srv, _ := testHub(t)

	if _, body := doP(t, "GET", srv.URL+"/api/portfolios", "", nil); !strings.Contains(body, `"slug":"main"`) || strings.Contains(body, `"wife"`) {
		t.Fatalf("свіжий перелік: %s", body)
	}
	resp, body := doP(t, "POST", srv.URL+"/api/portfolios", `{"slug":"wife","name":"Дружина"}`, nil)
	if resp.StatusCode != http.StatusCreated || !strings.Contains(body, `"name":"Дружина"`) {
		t.Fatalf("створення: %d %s", resp.StatusCode, body)
	}
	if resp, _ := doP(t, "POST", srv.URL+"/api/portfolios", `{"slug":"wife","name":"Ще"}`, nil); resp.StatusCode != http.StatusConflict {
		t.Errorf("повторний slug: %d, хочемо 409", resp.StatusCode)
	}
	if resp, _ := doP(t, "POST", srv.URL+"/api/portfolios", `{"slug":"Дружина","name":"x"}`, nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("кириличний slug: %d, хочемо 400", resp.StatusCode)
	}
	if resp, _ := doP(t, "PUT", srv.URL+"/api/portfolios/wife", `{"name":"Олена"}`, nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("перейменування: %d", resp.StatusCode)
	}
	if resp, _ := doP(t, "PUT", srv.URL+"/api/portfolios/nobody", `{"name":"x"}`, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("перейменування невідомого: %d", resp.StatusCode)
	}
	if _, body := doP(t, "GET", srv.URL+"/api/portfolios", "", nil); !strings.Contains(body, `"name":"Олена"`) {
		t.Errorf("назва не змінилась: %s", body)
	}
	if resp, _ := doP(t, "DELETE", srv.URL+"/api/portfolios/main", "", nil); resp.StatusCode != http.StatusConflict {
		t.Errorf("стирання головного: %d, хочемо 409", resp.StatusCode)
	}
	if resp, _ := doP(t, "DELETE", srv.URL+"/api/portfolios/wife", "", nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("стирання: %d", resp.StatusCode)
	}
	if resp, _ := doP(t, "GET", srv.URL+"/api/lots", "", inPortfolio("wife")); resp.StatusCode != http.StatusNotFound {
		t.Errorf("стертий портфель у заголовку: %d, хочемо 404", resp.StatusCode)
	}
}

// Заголовок X-Portfolio розводить запити по портфелях; без нього — головний.
func TestHubDispatchesByHeader(t *testing.T) {
	srv, st := testHub(t)
	seed(t, st)
	if resp, body := doP(t, "POST", srv.URL+"/api/portfolios", `{"slug":"wife","name":"Дружина"}`, nil); resp.StatusCode != 201 {
		t.Fatalf("створення: %d %s", resp.StatusCode, body)
	}

	if resp, body := doP(t, "POST", srv.URL+"/api/lots", testLot, nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот у головний: %d %s", resp.StatusCode, body)
	}
	if _, body := doP(t, "GET", srv.URL+"/api/lots", "", inPortfolio("wife")); strings.Contains(body, "UA4000227748") {
		t.Errorf("сателіт бачить лот головного: %s", body)
	}
	if resp, body := doP(t, "POST", srv.URL+"/api/lots", testLot, inPortfolio("wife")); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот у сателіт: %d %s", resp.StatusCode, body)
	}
	for _, c := range []struct {
		hdr  map[string]string
		want int
	}{{nil, 1}, {inPortfolio(store.MainSlug), 1}, {inPortfolio("wife"), 1}} {
		_, body := doP(t, "GET", srv.URL+"/api/lots", "", c.hdr)
		if n := strings.Count(body, `"isin"`); n != c.want {
			t.Errorf("%v: лотів %d, хочемо %d: %s", c.hdr, n, c.want, body)
		}
	}

	// Налаштування — теж свої: стратегія дружини не чіпає власника.
	if resp, body := doP(t, "PUT", srv.URL+"/api/settings", `{"usd_target_share_pct":"70"}`, inPortfolio("wife")); resp.StatusCode >= 300 {
		t.Fatalf("налаштування сателіта: %d %s", resp.StatusCode, body)
	}
	if _, body := doP(t, "GET", srv.URL+"/api/settings", "", nil); strings.Contains(body, `"usd_target_share_pct":"70"`) {
		t.Errorf("налаштування сателіта протекло в головний: %s", body)
	}
	if _, body := doP(t, "GET", srv.URL+"/api/settings", "", inPortfolio("wife")); !strings.Contains(body, `"usd_target_share_pct":"70"`) {
		t.Errorf("налаштування сателіта не збереглось: %s", body)
	}

	// Невідомий slug — 404, а не мовчазний головний.
	if resp, body := doP(t, "GET", srv.URL+"/api/lots", "", inPortfolio("nobody")); resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "nobody") {
		t.Errorf("невідомий портфель: %d %s", resp.StatusCode, body)
	}
	// Маршрути без портфеля заголовок ігнорують.
	if resp, _ := doP(t, "GET", srv.URL+"/api/auth", "", inPortfolio("nobody")); resp.StatusCode != http.StatusOK {
		t.Errorf("/api/auth із чужим slug-ом: %d, хочемо 200", resp.StatusCode)
	}
	if resp, _ := doP(t, "GET", srv.URL+"/", "", inPortfolio("nobody")); resp.StatusCode != http.StatusOK {
		t.Errorf("статика із чужим slug-ом: %d", resp.StatusCode)
	}
}

// Замок один на всіх: пароль, заданий на головному, закриває й сателіт.
func TestHubAuthGuardsSatellites(t *testing.T) {
	srv, _ := testHub(t)
	if resp, body := doP(t, "POST", srv.URL+"/api/portfolios", `{"slug":"wife","name":"Дружина"}`, nil); resp.StatusCode != 201 {
		t.Fatalf("створення: %d %s", resp.StatusCode, body)
	}
	resp, body := doP(t, "POST", srv.URL+"/api/auth/setup", `{"password":"correct horse","confirm":"correct horse"}`, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("пароль: %d %s", resp.StatusCode, body)
	}
	var cookie string
	for _, c := range resp.Cookies() {
		if c.Name == authCookie {
			cookie = c.Name + "=" + c.Value
		}
	}
	if cookie == "" {
		t.Fatal("setup не видав cookie")
	}
	if resp, _ := doP(t, "GET", srv.URL+"/api/lots", "", inPortfolio("wife")); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("сателіт без входу: %d, хочемо 401", resp.StatusCode)
	}
	// Невідомий slug без входу — теж 401, а не 404: імена портфелів не
	// перебираються анонімно.
	if resp, _ := doP(t, "GET", srv.URL+"/api/lots", "", inPortfolio("nobody")); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("невідомий slug без входу: %d, хочемо 401", resp.StatusCode)
	}
	if resp, _ := doP(t, "GET", srv.URL+"/api/lots", "", map[string]string{portfolioHeader: "wife", "Cookie": cookie}); resp.StatusCode != http.StatusOK {
		t.Errorf("сателіт із cookie головного: %d, хочемо 200", resp.StatusCode)
	}
}
