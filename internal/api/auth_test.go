package api

import (
	"context"
	"encoding/json"
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

// authServer — сервер із паролем або без нього.
//
// Пароль ставиться ТИМ САМИМ шляхом, що й у застосунку (POST
// /api/auth/setup), а не записом хеша повз обробник: інакше тести
// перевіряли б стан, у який застосунок може й не вміти прийти.
func authServer(t *testing.T, password string) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	srv := httptest.NewServer(New(st, nil, log).Handler())
	t.Cleanup(srv.Close)
	if password != "" {
		resp, body := do(t, "POST", srv.URL+"/api/auth/setup",
			`{"password":"`+password+`","confirm":"`+password+`"}`)
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("setup: %d %s", resp.StatusCode, body)
		}
	}
	return srv, st
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

// sessionOf — cookie сесії з відповіді; порожньо, якщо її немає.
func sessionOf(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == authCookie {
			return c
		}
	}
	return nil
}

func login(t *testing.T, srv *httptest.Server, password string) map[string]string {
	t.Helper()
	resp, body := do(t, "POST", srv.URL+"/api/login", `{"password":"`+password+`"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("вхід: %d %s", resp.StatusCode, body)
	}
	c := sessionOf(t, resp)
	if c == nil {
		t.Fatal("після входу немає cookie сесії")
	}
	return map[string]string{"Cookie": authCookie + "=" + c.Value}
}

func TestAuthNoPasswordIsOpenAndAsksForSetup(t *testing.T) {
	srv, _ := authServer(t, "")
	resp, body := do(t, "GET", srv.URL+"/api/brokers", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("без пароля /api/brokers мусить бути відкритий: %d %s", resp.StatusCode, body)
	}
	resp, body = do(t, "GET", srv.URL+"/api/auth", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"enabled":false`) ||
		!strings.Contains(body, `"setup":true`) || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("/api/auth без пароля: %d %s", resp.StatusCode, body)
	}
	// Входити нема куди — маршрут поводиться як відсутній, а UI натомість
	// показує форму «задай пароль».
	resp, _ = do(t, "POST", srv.URL+"/api/login", `{"password":"x"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("login без пароля: %d, чекали 404", resp.StatusCode)
	}
}

func TestAuthSetupOnceThenConflict(t *testing.T) {
	srv, _ := authServer(t, "")
	// Короткий пароль і розбіжне підтвердження не приймаються.
	resp, _ := do(t, "POST", srv.URL+"/api/auth/setup", `{"password":"short7","confirm":"short7"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("короткий пароль: %d, чекали 400", resp.StatusCode)
	}
	resp, _ = do(t, "POST", srv.URL+"/api/auth/setup", `{"password":"correct-horse","confirm":"other"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("розбіжне підтвердження: %d, чекали 400", resp.StatusCode)
	}

	resp, body := do(t, "POST", srv.URL+"/api/auth/setup", `{"password":"correct-horse","confirm":"correct-horse"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("setup: %d %s", resp.StatusCode, body)
	}
	if sessionOf(t, resp) == nil {
		t.Fatal("setup мусить одразу видати cookie — інакше людина задає пароль і бачить форму входу")
	}
	// Друга спроба — 409: інакше це був би скидач пароля без перевірки.
	resp, _ = do(t, "POST", srv.URL+"/api/auth/setup", `{"password":"another-one","confirm":"another-one"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("повторний setup: %d, чекали 409", resp.StatusCode)
	}
	resp, _ = do(t, "GET", srv.URL+"/api/brokers", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("після setup API мусить бути закритий: %d", resp.StatusCode)
	}
}

func TestAuthGatesAPIButNotStatic(t *testing.T) {
	srv, _ := authServer(t, "correct-horse")
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
	// Зміна пароля й токен машин — теж під замком.
	resp, _ = do(t, "PUT", srv.URL+"/api/auth/password", `{"current":"correct-horse","password":"n","confirm":"n"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("зміна пароля без входу: %d", resp.StatusCode)
	}
	resp, _ = do(t, "POST", srv.URL+"/api/auth/token", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("видача токена без входу: %d", resp.StatusCode)
	}
	// Статика й health деплою (200 на «/») — відкриті.
	resp, _ = do(t, "GET", srv.URL+"/", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статика мусить бути відкрита: %d", resp.StatusCode)
	}
	resp, body = do(t, "GET", srv.URL+"/api/auth", "")
	if !strings.Contains(body, `"enabled":true`) || !strings.Contains(body, `"ok":false`) ||
		!strings.Contains(body, `"setup":false`) {
		t.Fatalf("/api/auth до входу: %d %s", resp.StatusCode, body)
	}
}

func TestAuthLoginCookie(t *testing.T) {
	srv, _ := authServer(t, "correct-horse")
	resp, body := do(t, "POST", srv.URL+"/api/login", `{"password":"wrong-one"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("хибний пароль: %d %s", resp.StatusCode, body)
	}
	if len(resp.Cookies()) != 0 {
		t.Fatalf("хибний пароль не має видавати cookie: %v", resp.Cookies())
	}

	resp, body = do(t, "POST", srv.URL+"/api/login", `{"password":"correct-horse"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("вхід: %d %s", resp.StatusCode, body)
	}
	c := sessionOf(t, resp)
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
	if cl := sessionOf(t, resp); cl == nil || cl.MaxAge >= 0 {
		t.Fatalf("logout не стер cookie: %v", resp.Cookies())
	}
}

// Зміна пароля вимагає поточного й гасить старі сесії — на інших
// пристроях. Той, хто змінив, лишається в застосунку: інакше зміна пароля
// виглядала б як вихід.
func TestAuthChangePassword(t *testing.T) {
	srv, _ := authServer(t, "correct-horse")
	h := login(t, srv, "correct-horse")

	resp, _ := doH(t, "PUT", srv.URL+"/api/auth/password",
		`{"current":"nope-nope","password":"brand-new-one","confirm":"brand-new-one"}`, h)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("хибний поточний: %d, чекали 401", resp.StatusCode)
	}
	resp, _ = doH(t, "PUT", srv.URL+"/api/auth/password",
		`{"current":"correct-horse","password":"short7","confirm":"short7"}`, h)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("короткий новий: %d, чекали 400", resp.StatusCode)
	}

	resp, body := doH(t, "PUT", srv.URL+"/api/auth/password",
		`{"current":"correct-horse","password":"brand-new-one","confirm":"brand-new-one"}`, h)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("зміна пароля: %d %s", resp.StatusCode, body)
	}
	fresh := sessionOf(t, resp)
	if fresh == nil {
		t.Fatal("зміна пароля мусить видати нову cookie тому, хто її зробив")
	}
	// Стара cookie мертва — ключ підпису обернувся.
	resp, _ = doH(t, "GET", srv.URL+"/api/brokers", "", h)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("стара cookie пережила зміну пароля: %d", resp.StatusCode)
	}
	resp, _ = doH(t, "GET", srv.URL+"/api/brokers", "",
		map[string]string{"Cookie": authCookie + "=" + fresh.Value})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("нова cookie не працює: %d", resp.StatusCode)
	}
	// Старий пароль більше не пускає, новий пускає.
	resp, _ = do(t, "POST", srv.URL+"/api/login", `{"password":"correct-horse"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("старий пароль пройшов: %d", resp.StatusCode)
	}
	login(t, srv, "brand-new-one")
}

func TestAuthSecureCookieBehindTLSProxy(t *testing.T) {
	srv, _ := authServer(t, "correct-horse")
	resp, _ := doH(t, "POST", srv.URL+"/api/login", `{"password":"correct-horse"}`,
		map[string]string{"X-Forwarded-Proto": "https"})
	if c := sessionOf(t, resp); c == nil || !c.Secure {
		t.Fatal("за https-тунелем cookie мусить бути Secure")
	}
}

func TestAuthSessionExpires(t *testing.T) {
	key := []byte("test-key-32-bytes-long-enough!!!")
	now := time.Now()
	v := issue(key, now)
	if !validCookie(key, v, now.Add(authLifetime-time.Minute)) {
		t.Fatal("сесія за хвилину до строку мусить бути чинною")
	}
	if validCookie(key, v, now.Add(authLifetime+time.Minute)) {
		t.Fatal("сесія після строку мусить згаснути")
	}
	if validCookie([]byte("another-key-32-bytes-long-ok!!!!"), v, now) {
		t.Fatal("cookie, підписана іншим ключем, пройшла")
	}
	if validCookie(nil, v, now) {
		t.Fatal("без ключа сесій cookie не може бути чинною")
	}
}

// Хеш пароля: той самий пароль дає інший рядок (сіль), але перевіряється;
// чужий — ні. Сміття в базі читається як «не збігається», а не панікою.
func TestPasswordHashing(t *testing.T) {
	h1, err := hashPassword("correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	h2, _ := hashPassword("correct-horse")
	if h1 == h2 {
		t.Fatal("два хеші одного пароля збіглись — сіль не працює")
	}
	if !strings.HasPrefix(h1, "pbkdf2-sha256$") {
		t.Fatalf("формат хеша: %s", h1)
	}
	if !verifyPassword(h1, "correct-horse") || !verifyPassword(h2, "correct-horse") {
		t.Fatal("правильний пароль не пройшов перевірку")
	}
	if verifyPassword(h1, "correct-horsE") {
		t.Fatal("чужий пароль пройшов перевірку")
	}
	for _, junk := range []string{"", "hello", "pbkdf2-sha256$x$y$z", "pbkdf2-sha256$1$!!$!!"} {
		if verifyPassword(junk, "correct-horse") {
			t.Fatalf("сміття %q пройшло як хеш", junk)
		}
	}
}

func TestAuthMachineToken(t *testing.T) {
	srv, _ := authServer(t, "correct-horse")
	h := login(t, srv, "correct-horse")

	_, body := doH(t, "GET", srv.URL+"/api/auth", "", h)
	if !strings.Contains(body, `"has_token":false`) {
		t.Fatalf("до видачі токена немає: %s", body)
	}
	resp, body := doH(t, "POST", srv.URL+"/api/auth/token", "", h)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("видача токена: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || len(out.Token) != 64 {
		t.Fatalf("токен: %q (%v)", out.Token, err)
	}
	bearer := map[string]string{"Authorization": "Bearer " + out.Token}
	resp, _ = doH(t, "GET", srv.URL+"/api/brokers", "", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("з токеном: %d", resp.StatusCode)
	}
	resp, _ = doH(t, "GET", srv.URL+"/api/brokers", "", map[string]string{"Authorization": "Bearer nope"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("з хибним токеном: %d", resp.StatusCode)
	}
	_, body = doH(t, "GET", srv.URL+"/api/auth", "", h)
	if !strings.Contains(body, `"has_token":true`) {
		t.Fatalf("після видачі токен мусить бути названий: %s", body)
	}

	// Новий токен вбиває старий: у базі лише один хеш.
	_, body = doH(t, "POST", srv.URL+"/api/auth/token", "", h)
	var second struct {
		Token string `json:"token"`
	}
	json.Unmarshal([]byte(body), &second) //nolint:errcheck // формат уже перевірено вище
	if second.Token == out.Token {
		t.Fatal("другий токен збігся з першим")
	}
	resp, _ = doH(t, "GET", srv.URL+"/api/brokers", "", bearer)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("старий токен пережив видачу нового: %d", resp.StatusCode)
	}

	// Відкликання.
	resp, _ = doH(t, "DELETE", srv.URL+"/api/auth/token", "", h)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("відкликання: %d", resp.StatusCode)
	}
	resp, _ = doH(t, "GET", srv.URL+"/api/brokers", "",
		map[string]string{"Authorization": "Bearer " + second.Token})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("відкликаний токен пройшов: %d", resp.StatusCode)
	}
}

func TestAuthLockoutAfterFailures(t *testing.T) {
	srv, _ := authServer(t, "correct-horse")
	for i := 0; i < authMaxFails; i++ {
		resp, _ := do(t, "POST", srv.URL+"/api/login", `{"password":"wrong-one"}`)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("спроба %d: %d", i+1, resp.StatusCode)
		}
	}
	// Шоста — уже 429, навіть із правильним паролем: гальмо не питає.
	resp, body := do(t, "POST", srv.URL+"/api/login", `{"password":"correct-horse"}`)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("після %d невдач: %d %s, чекали 429", authMaxFails, resp.StatusCode, body)
	}
	// Інша адреса (за тунелем — інший Cf-Connecting-Ip) не гальмується.
	resp, _ = doH(t, "POST", srv.URL+"/api/login", `{"password":"correct-horse"}`,
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

// ResetAuth повертає сервіс у стан першого запуску — це і є вихід із
// «забув пароль» (команда oddinvestd -reset-auth).
func TestAuthResetReturnsToSetup(t *testing.T) {
	srv, st := authServer(t, "correct-horse")
	h := login(t, srv, "correct-horse")
	if _, body := doH(t, "POST", srv.URL+"/api/auth/token", "", h); !strings.Contains(body, "token") {
		t.Fatalf("токен не видався: %s", body)
	}
	if err := st.ResetAuth(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Сервер тримає секрети в кеші, тож після скидання його перезапускають —
	// саме це робить systemctl після команди. Новий сервер на тій самій базі:
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	fresh := httptest.NewServer(New(st, nil, log).Handler())
	t.Cleanup(fresh.Close)
	_, body := do(t, "GET", fresh.URL+"/api/auth", "")
	if !strings.Contains(body, `"setup":true`) || !strings.Contains(body, `"has_token":false`) {
		t.Fatalf("після скидання: %s", body)
	}
	resp, _ := do(t, "GET", fresh.URL+"/api/brokers", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("після скидання API мусить бути відкритий: %d", resp.StatusCode)
	}
}

// Секрети не виходять із бекапу — і відновлення чужої копії не чіпає
// пароль. Це та властивість, заради якої заведено окрему таблицю.
func TestSecretsSurviveRestoreAndStayOutOfBackup(t *testing.T) {
	srv, st := authServer(t, "correct-horse")
	ctx := context.Background()
	dump, err := st.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"pbkdf2", "secret", "password_hash"} {
		if strings.Contains(string(raw), needle) {
			t.Fatalf("бекап несе %q — секрети мусять лишатись у базі", needle)
		}
	}
	h := login(t, srv, "correct-horse")
	resp, body := doH(t, "POST", srv.URL+"/api/restore", string(raw), h)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("відновлення: %d %s", resp.StatusCode, body)
	}
	// Пароль на місці: та сама cookie й далі працює.
	resp, _ = doH(t, "GET", srv.URL+"/api/brokers", "", h)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("після відновлення сесія померла: %d", resp.StatusCode)
	}
}
