package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/store"
)

// fakeCF — Cloudflare рівно в тому обсязі, який чіпає застосунок.
//
// Своїм сервером, а не моком клієнта: перевіряти треба саме те, які
// запити йдуть назовні й що робиться з відповідями — мок на рівні методів
// клієнта переказував би наші ж припущення.
type fakeCF struct {
	srv *httptest.Server

	mu       sync.Mutex
	zones    map[string]string // назва зони -> id
	tunnels  map[string]string // назва -> id
	ingress  map[string]any
	records  map[string]string // id -> адреса
	status   string
	deleted  []string
	nextID   int
	failWith string // якщо не порожньо — будь-який запит дає цю помилку
	seen     []string
}

func newFakeCF(t *testing.T) *fakeCF {
	f := &fakeCF{
		zones:   map[string]string{"example.com": "zone-1"},
		tunnels: map[string]string{},
		records: map[string]string{},
		status:  "healthy",
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.route))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCF) ok(w http.ResponseWriter, result any) {
	raw, _ := json.Marshal(result)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck // тестовий сервер
		"success": true, "errors": []any{}, "result": json.RawMessage(raw),
	})
}

func (f *fakeCF) route(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	if f.failWith != "" {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck // тестовий сервер
			"success": false,
			"errors":  []map[string]any{{"code": 10000, "message": f.failWith}},
		})
		return
	}
	p := r.URL.Path
	switch {
	case p == "/zones":
		name := r.URL.Query().Get("name")
		id, ok := f.zones[name]
		if !ok {
			f.ok(w, []any{})
			return
		}
		f.ok(w, []map[string]any{{"id": id, "name": name,
			"account": map[string]string{"id": "acc-1"}}})

	case strings.HasSuffix(p, "/cfd_tunnel") && r.Method == http.MethodGet:
		name := r.URL.Query().Get("name")
		if id, ok := f.tunnels[name]; ok {
			f.ok(w, []map[string]any{{"id": id, "name": name, "status": f.status}})
			return
		}
		f.ok(w, []any{})

	case strings.HasSuffix(p, "/cfd_tunnel") && r.Method == http.MethodPost:
		var in map[string]string
		json.NewDecoder(r.Body).Decode(&in) //nolint:errcheck // тестовий сервер
		f.nextID++
		id := "tun-1"
		f.tunnels[in["name"]] = id
		f.ok(w, map[string]any{"id": id, "name": in["name"], "status": "inactive"})

	case strings.HasSuffix(p, "/configurations"):
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in) //nolint:errcheck // тестовий сервер
		f.ingress = in
		f.ok(w, map[string]any{"tunnel_id": "tun-1"})

	case strings.HasSuffix(p, "/token"):
		f.ok(w, "connector-token")

	case strings.HasSuffix(p, "/dns_records") && r.Method == http.MethodGet:
		name := r.URL.Query().Get("name")
		for id, n := range f.records {
			if n == name {
				f.ok(w, []map[string]any{{"id": id, "content": "old"}})
				return
			}
		}
		f.ok(w, []any{})

	case strings.HasSuffix(p, "/dns_records") && r.Method == http.MethodPost:
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in) //nolint:errcheck // тестовий сервер
		f.records["rec-1"] = in["name"].(string)
		f.ok(w, map[string]any{"id": "rec-1"})

	case strings.Contains(p, "/dns_records/") && r.Method == http.MethodPatch:
		f.ok(w, map[string]any{"id": strings.TrimPrefix(p[strings.LastIndex(p, "/"):], "/")})

	case strings.Contains(p, "/dns_records/") && r.Method == http.MethodDelete:
		delete(f.records, p[strings.LastIndex(p, "/")+1:])
		f.deleted = append(f.deleted, "dns")
		f.ok(w, map[string]any{"id": "rec-1"})

	case strings.HasSuffix(p, "/connections") && r.Method == http.MethodDelete:
		f.deleted = append(f.deleted, "connections")
		f.ok(w, map[string]any{})

	case r.Method == http.MethodDelete && strings.Contains(p, "/cfd_tunnel/"):
		f.deleted = append(f.deleted, "tunnel")
		f.tunnels = map[string]string{}
		f.ok(w, map[string]any{"id": "tun-1"})

	case r.Method == http.MethodGet && strings.Contains(p, "/cfd_tunnel/"):
		f.ok(w, map[string]any{"id": "tun-1", "name": tunnelName, "status": f.status})

	default:
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck // тестовий сервер
			"success": false,
			"errors":  []map[string]any{{"code": 404, "message": "no route " + p}},
		})
	}
}

func (f *fakeCF) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

func testManager(t *testing.T, base string) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	m := NewManager(st, log, base, "http://127.0.0.1:8080", dir)
	m.wait0 = time.Millisecond // тест перевіряє перезапуск, а не годинник
	// Конектор підмінений: справжній cloudflared тягнув би за собою мережу
	// й наявність бінарника, а перевіряємо ми нагляд, а не його.
	m.run = func(ctx context.Context, token string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(m.Close)
	return m, st
}

// Зона знаходиться за СУФІКСОМ адреси: oddinvest.example.com → example.com.
func TestZoneForWalksSuffixes(t *testing.T) {
	f := newFakeCF(t)
	c := New(f.srv.URL, "tok")
	z, err := c.ZoneFor(context.Background(), "oddinvest.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if z.ID != "zone-1" || z.AccountID != "acc-1" {
		t.Fatalf("зона %+v", z)
	}
	if _, err := c.ZoneFor(context.Background(), "oddinvest.unknown.tld"); err == nil {
		t.Fatal("чужий домен мусить дати помилку, а не мовчання")
	}
	if _, err := c.ZoneFor(context.Background(), "example"); err == nil {
		t.Fatal("адреса без крапки — не адреса")
	}
}

// Помилка Cloudflare доходить ДОСЛІВНО, з кодом: саме за цим текстом
// людина знаходить, чого бракує токену.
func TestCloudflareErrorReachesCaller(t *testing.T) {
	f := newFakeCF(t)
	f.failWith = "Actor does not have permission to edit DNS"
	_, err := New(f.srv.URL, "tok").ZoneFor(context.Background(), "oddinvest.example.com")
	if err == nil || !strings.Contains(err.Error(), "permission to edit DNS") ||
		!strings.Contains(err.Error(), "10000") {
		t.Fatalf("помилка: %v", err)
	}
}

// Connect створює тунель, ingress і CNAME, пише реквізити й публічну
// адресу — і піднімає конектор.
func TestConnectWritesEverythingAndStarts(t *testing.T) {
	f := newFakeCF(t)
	m, st := testManager(t, f.srv.URL)
	ctx := context.Background()

	if err := m.Connect(ctx, "cf-token", "https://oddinvest.example.com/"); err != nil {
		t.Fatal(err)
	}
	sec, _ := st.AllSecrets(ctx)
	for k, want := range map[string]string{
		store.SecretCFAPIToken:    "cf-token",
		store.SecretCFAccountID:   "acc-1",
		store.SecretCFZoneID:      "zone-1",
		store.SecretCFTunnelID:    "tun-1",
		store.SecretCFTunnelToken: "connector-token",
		// Адреса нормалізована: схема й слеш зрізані.
		store.SecretCFHostname:    "oddinvest.example.com",
		store.SecretCFDNSRecordID: "rec-1",
	} {
		if sec[k] != want {
			t.Errorf("%s = %q, чекали %q", k, sec[k], want)
		}
	}
	if pu, _ := st.GetSetting(ctx, "public_url"); pu != "https://oddinvest.example.com" {
		t.Errorf("public_url = %q", pu)
	}
	// Ingress веде на наш порт і має запасне правило.
	cfg := f.ingress["config"].(map[string]any)["ingress"].([]any)
	if len(cfg) != 2 {
		t.Fatalf("правил ingress %d, чекали 2", len(cfg))
	}
	first := cfg[0].(map[string]any)
	if first["service"] != "http://127.0.0.1:8080" || first["hostname"] != "oddinvest.example.com" {
		t.Errorf("перше правило: %+v", first)
	}
	if cfg[1].(map[string]any)["service"] != "http_status:404" {
		t.Errorf("останнє правило мусить бути 404: %+v", cfg[1])
	}
	m.mu.Lock()
	running := m.running
	m.mu.Unlock()
	if !running {
		t.Error("після підключення конектор мусить бути запущений")
	}
}

// Повторний Connect знаходить свій тунель, а не плодить другий.
func TestConnectIsIdempotent(t *testing.T) {
	f := newFakeCF(t)
	m, _ := testManager(t, f.srv.URL)
	ctx := context.Background()
	if err := m.Connect(ctx, "cf-token", "oddinvest.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := m.Connect(ctx, "cf-token", "oddinvest.example.com"); err != nil {
		t.Fatal(err)
	}
	posts := 0
	for _, c := range f.calls() {
		if c == "POST /accounts/acc-1/cfd_tunnel" {
			posts++
		}
	}
	if posts != 1 {
		t.Fatalf("тунель створювався %d разів, чекали 1", posts)
	}
}

// Disconnect прибирає й у Cloudflare, і в базі; порядок видалення —
// спершу зʼєднання, потім тунель (вимога API).
func TestDisconnectCleansUp(t *testing.T) {
	f := newFakeCF(t)
	m, st := testManager(t, f.srv.URL)
	ctx := context.Background()
	if err := m.Connect(ctx, "cf-token", "oddinvest.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := m.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	order := strings.Join(f.deleted, ",")
	f.mu.Unlock()
	if order != "dns,connections,tunnel" {
		t.Fatalf("порядок видалення %q", order)
	}
	sec, _ := st.AllSecrets(ctx)
	for _, k := range []string{store.SecretCFAPIToken, store.SecretCFTunnelToken, store.SecretCFHostname} {
		if sec[k] != "" {
			t.Errorf("%s лишився: %q", k, sec[k])
		}
	}
	if pu, _ := st.GetSetting(ctx, "public_url"); pu != "" {
		t.Errorf("public_url лишився: %q", pu)
	}
	m.mu.Lock()
	running := m.running
	m.mu.Unlock()
	if running {
		t.Error("після відключення конектор мусить бути зупинений")
	}
}

// Нагляд перезапускає конектор, який упав, і зупиняється разом із ctx.
func TestSuperviseRestartsAndStops(t *testing.T) {
	f := newFakeCF(t)
	m, _ := testManager(t, f.srv.URL)
	var runs atomic.Int32
	m.run = func(ctx context.Context, token string) error {
		if runs.Add(1) < 3 {
			return errors.New("упав")
		}
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.supervise(ctx, "connector-token")

	deadline := time.Now().Add(30 * time.Second)
	for runs.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if runs.Load() < 3 {
		t.Fatalf("перезапусків %d — нагляд не підняв конектор", runs.Load())
	}
	m.mu.Lock()
	lastErr := m.lastErr
	m.mu.Unlock()
	if !strings.Contains(lastErr, "упав") {
		t.Errorf("причина падіння мусить бути названа: %q", lastErr)
	}
	m.stop()
	before := runs.Load()
	time.Sleep(50 * time.Millisecond)
	if runs.Load() != before {
		t.Error("після зупинки конектор не мусить перезапускатись")
	}
}

// Статус без тунелю — «не налаштовано», а не помилка: сторінку відкривають
// саме тоді, коли тунелю ще немає.
func TestStatusWithoutTunnel(t *testing.T) {
	f := newFakeCF(t)
	m, _ := testManager(t, f.srv.URL)
	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Configured || st.Hostname != "" {
		t.Fatalf("стан %+v", st)
	}
}

func TestStatusAfterConnect(t *testing.T) {
	f := newFakeCF(t)
	m, _ := testManager(t, f.srv.URL)
	ctx := context.Background()
	if err := m.Connect(ctx, "cf-token", "oddinvest.example.com"); err != nil {
		t.Fatal(err)
	}
	st, err := m.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Configured || st.Hostname != "oddinvest.example.com" ||
		st.PublicURL != "https://oddinvest.example.com" || st.TunnelStatus != "healthy" {
		t.Fatalf("стан %+v", st)
	}
	if !st.Running {
		t.Error("конектор мусить бути запущений")
	}
}

// Start піднімає конектор із того, що вже лежить у базі: рестарт демона не
// має вимагати повторного «Підключити».
func TestStartResumesFromStore(t *testing.T) {
	f := newFakeCF(t)
	m, st := testManager(t, f.srv.URL)
	ctx := context.Background()
	if err := st.SetSecret(ctx, store.SecretCFTunnelToken, "connector-token"); err != nil {
		t.Fatal(err)
	}
	m.Start(ctx)
	m.mu.Lock()
	running := m.running
	m.mu.Unlock()
	if !running {
		t.Fatal("Start мусить підняти конектор за збереженим токеном")
	}
}

func TestOriginFromAddr(t *testing.T) {
	for addr, want := range map[string]string{
		":8080":          "http://127.0.0.1:8080",
		"127.0.0.1:8099": "http://127.0.0.1:8099",
		"0.0.0.0:9000":   "http://127.0.0.1:9000",
		"нісенітниця":    "http://127.0.0.1:8080",
	} {
		if got := OriginFromAddr(addr); got != want {
			t.Errorf("%q → %q, чекали %q", addr, got, want)
		}
	}
}
