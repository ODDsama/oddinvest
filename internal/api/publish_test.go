package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/finomo"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// countingRefresher рахує публікації стану; решта методів — порожні.
type countingRefresher struct{ n atomic.Int32 }

func (c *countingRefresher) RefreshAll(context.Context) error { return nil }
func (c *countingRefresher) PublishState(context.Context) error {
	c.n.Add(1)
	return nil
}
func (c *countingRefresher) RefreshQuotes(context.Context, []string) (finomo.RunResult, error) {
	return finomo.RunResult{}, nil
}

// Публікація в MQTT — наслідок УСПІШНОГО запису, і вирішує це одна
// обгортка (publishAfterWrite), а не кожен обробник окремо. Тест тримає
// всі чотири гілки: вдалий запис публікує; невдалий, читання й запис із
// переліку винятків — ні.
func TestPublishAfterWrite(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := New(st, testLogger())
	ref := &countingRefresher{}
	s.SetRefresher(ref)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	do := func(method, path, body string) int {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	// Публікація йде у фоновій горутині — чекаємо, доки лічильник стане
	// таким, як треба, або доки не мине час.
	settled := func(want int32) int32 {
		deadline := time.Now().Add(2 * time.Second)
		for ref.n.Load() != want && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		// Запас на публікацію, якої бути не повинно.
		time.Sleep(50 * time.Millisecond)
		return ref.n.Load()
	}

	if code := do("GET", "/api/summary", ""); code != http.StatusOK {
		t.Fatalf("GET /api/summary: %d", code)
	}
	if got := settled(0); got != 0 {
		t.Errorf("читання опублікувало стан: %d", got)
	}
	if code := do("PUT", "/api/settings", "{не json"); code < 400 {
		t.Fatalf("кривий запис пройшов: %d", code)
	}
	if got := settled(0); got != 0 {
		t.Errorf("невдалий запис опублікував стан: %d", got)
	}
	if code := do("POST", "/api/logout", ""); code >= 400 {
		t.Fatalf("POST /api/logout: %d", code)
	}
	if got := settled(0); got != 0 {
		t.Errorf("запис із переліку винятків опублікував стан: %d", got)
	}
	if code := do("PUT", "/api/settings", `{"monthly_expenses":"0","monthly_expenses_currency":"UAH"}`); code >= 400 {
		t.Fatalf("PUT /api/settings: %d", code)
	}
	if got := settled(1); got != 1 {
		t.Errorf("вдалий запис: публікацій %d, чекали 1", got)
	}
}

// Версія збірки потрапляє в документ на шляху публікації — звідти її
// читає Home Assistant (sw_version пристрою).
func TestPresentDocStampsVersion(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	doc := &state.Doc{Currency: "UAH"}
	if err := New(st, testLogger()).PresentDoc(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	if doc.AppVersion != Version {
		t.Errorf("app_version = %q, чекали %q", doc.AppVersion, Version)
	}
}
