package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/store"
)

// authServer — testServer із замком. Окремою функцією, щоб решта пакета
// й далі жила без пароля: нульове Auth = «як досі», і саме це тут
// перевіряється першим тестом.
func authServer(t *testing.T, a Auth) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	s := New(st, nil, log)
	s.SetAuth(a)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// doH — do() із заголовками; клієнт без редиректів і без cookie-jar, щоб
// кожен тест сам казав, що саме він несе.
func doH(t *testing.T, method, url, body string, h map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(strings.Builder)
	b := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(b)
		buf.Write(b[:n])
		if rerr != nil {
			break
		}
	}
	return resp, buf.String()
}

func TestAuthDisabledIsOpen(t *testing.T) {
	srv := authServer(t, Auth{})
	resp, body := do(t, "GET", srv.URL+"/api/brokers", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("без пароля /api/brokers мусить бути відкритий: %d %s", resp.StatusCode, body)
	}
	resp, body = do(t, "GET", srv.URL+"/api/auth", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"enabled":false`) || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("/api/auth без замка: %d %s", resp.StatusCode, body)
	}
	// Логін нема куди — маршрут поводиться як відсутній.
	resp, _ = do(t, "POST", srv.URL+"/api/login", `{"password":"x"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("login при вимкненому замку: %d, чекали 404", resp.StatusCode)
	}
}

func TestAuthGatesAPIButNotStatic(t *testing.T) {
	srv := authServer(t, Auth{Password: "secret"})
	resp, body := do(t, "GET", srv.URL+"/api/brokers", "")
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "потрібен вхід") {
		t.Fatalf("без cookie: %d %s, чекали 401", resp.StatusCode, body)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("401 мусить іти з no-store, а не осідати в кеші: %q", cc)
	}
	// Читання гейтиться нарівні із записом: бекап — це вся база.
	resp, _ = do(t, "GET", srv.URL+"/api/backup", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/api/backup без входу: %d", resp.StatusCode)
	}
	// Статика й health деплою (200 на «/») — відкриті.
	resp, _ = do(t, "GET", srv.URL+"/", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статика мусить бути відкрита: %d", resp.StatusCode)
	}
	resp, body = do(t, "GET", srv.URL+"/api/auth", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"enabled":true`) || !strings.Contains(body, `"ok":false`) {
		t.Fatalf("/api/auth до входу: %d %s", resp.StatusCode, body)
	}
}

func TestAuthLoginCookie(t *testing.T) {
	srv := authServer(t, Auth{Password: "secret"})
	resp, body := do(t, "POST", srv.URL+"/api/login", `{"password":"wrong"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("хибний пароль: %d %s", resp.StatusCode, body)
	}
	if len(resp.Cookies()) != 0 {
		t.Fatalf("хибний пароль не має видавати cookie: %v", resp.Cookies())
	}

	resp, body = do(t, "POST", srv.URL+"/api/login", `{"password":"secret"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("вхід: %d %s", resp.StatusCode, body)
	}
	var c *http.Cookie
	for _, ck := range resp.Cookies() {
		if ck.Name == authCookie {
			c = ck
		}
	}
	if c == nil {
		t.Fatal("після входу немає cookie сесії")
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Fatalf("cookie: HttpOnly=%v SameSite=%v Path=%q", c.HttpOnly, c.SameSite, c.Path)
	}
	if c.Secure {
		t.Fatal("по http cookie не має бути Secure — інакше вхід у LAN не збережеться")
	}
	if c.MaxAge < int(authLifetime/time.Second)-5 {
		t.Fatalf("строк cookie %d с, чекали ≈30 днів", c.MaxAge)
	}

	h := map[string]string{"Cookie": authCookie + "=" + c.Value}
	resp, body = doH(t, "GET", srv.URL+"/api/brokers", "", h)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("з cookie: %d %s", resp.StatusCode, body)
	}
	_, body = doH(t, "GET", srv.URL+"/api/auth", "", h)
	if !strings.Contains(body, `"ok":true`) {
		t.Fatalf("/api/auth з cookie: %s", body)
	}

	// Підроблена cookie — той самий формат, інший підпис.
	exp, _, _ := strings.Cut(c.Value, ":")
	fake := map[string]string{"Cookie": authCookie + "=" + exp + ":" + strings.Repeat("0", 64)}
	resp, _ = doH(t, "GET", srv.URL+"/api/brokers", "", fake)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("підроблена cookie пройшла: %d", resp.StatusCode)
	}

	// Вихід стирає cookie.
	resp, _ = doH(t, "POST", srv.URL+"/api/logout", "", h)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	cleared := false
	for _, ck := range resp.Cookies() {
		if ck.Name == authCookie && ck.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("logout не стер cookie: %v", resp.Cookies())
	}
}

func TestAuthSecureCookieBehindTLSProxy(t *testing.T) {
	srv := authServer(t, Auth{Password: "secret"})
	resp, _ := doH(t, "POST", srv.URL+"/api/login", `{"password":"secret"}`,
		map[string]string{"X-Forwarded-Proto": "https"})
	for _, ck := range resp.Cookies() {
		if ck.Name == authCookie && !ck.Secure {
			t.Fatal("за https-тунелем cookie мусить бути Secure")
		}
	}
}

func TestAuthSessionExpires(t *testing.T) {
	a := Auth{Password: "secret"}
	now := time.Now()
	v := a.issue(now)
	if !a.valid(v, now.Add(authLifetime-time.Minute)) {
		t.Fatal("сесія за хвилину до строку мусить бути чинною")
	}
	if a.valid(v, now.Add(authLifetime+time.Minute)) {
		t.Fatal("сесія після строку мусить згаснути")
	}
	// Зміна пароля розлогінює: ключ підпису виведений із нього.
	if (Auth{Password: "other"}).valid(v, now) {
		t.Fatal("cookie старого пароля пройшла з новим")
	}
}

func TestAuthBearerToken(t *testing.T) {
	srv := authServer(t, Auth{Password: "secret", Token: "machine-token"})
	resp, _ := doH(t, "GET", srv.URL+"/api/brokers", "", map[string]string{"Authorization": "Bearer machine-token"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("з токеном: %d", resp.StatusCode)
	}
	resp, _ = doH(t, "GET", srv.URL+"/api/brokers", "", map[string]string{"Authorization": "Bearer nope"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("з хибним токеном: %d", resp.StatusCode)
	}
	// Лише токен, без пароля: замок є, входу за паролем немає.
	only := authServer(t, Auth{Token: "machine-token"})
	resp, _ = do(t, "GET", only.URL+"/api/brokers", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("лише токен, без нього: %d", resp.StatusCode)
	}
	resp, _ = do(t, "POST", only.URL+"/api/login", `{"password":"x"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("login без пароля в конфігурації: %d, чекали 404", resp.StatusCode)
	}
}

func TestAuthLockoutAfterFailures(t *testing.T) {
	srv := authServer(t, Auth{Password: "secret"})
	for i := 0; i < authMaxFails; i++ {
		resp, _ := do(t, "POST", srv.URL+"/api/login", `{"password":"wrong"}`)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("спроба %d: %d", i+1, resp.StatusCode)
		}
	}
	// Шоста — уже 429, навіть із правильним паролем: гальмо не питає.
	resp, body := do(t, "POST", srv.URL+"/api/login", `{"password":"secret"}`)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("після %d невдач: %d %s, чекали 429", authMaxFails, resp.StatusCode, body)
	}
	// Інша адреса (за тунелем — інший Cf-Connecting-Ip) не гальмується.
	resp, _ = doH(t, "POST", srv.URL+"/api/login", `{"password":"secret"}`,
		map[string]string{"Cf-Connecting-Ip": "203.0.113.7"})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("інша адреса під чужим гальмом: %d", resp.StatusCode)
	}
}

func TestAuthLockoutExpires(t *testing.T) {
	st := newAuthState()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	st.now = func() time.Time { return now }
	for i := 0; i < authMaxFails; i++ {
		st.fail("1.2.3.4")
	}
	if !st.locked("1.2.3.4") {
		t.Fatal("після п'яти невдач адреса мусить бути під гальмом")
	}
	now = now.Add(authLockFor + time.Second)
	if st.locked("1.2.3.4") {
		t.Fatal("гальмо мусить відпустити після вікна")
	}
	// і рахунок починається з нуля
	st.fail("1.2.3.4")
	if st.locked("1.2.3.4") {
		t.Fatal("одна невдача після вікна — ще не гальмо")
	}
}
