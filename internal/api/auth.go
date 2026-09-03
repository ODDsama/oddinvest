package api

// Авторизація REST: пароль для людини, токен для машин.
//
// НАВІЩО ЗАРАЗ. README тримав це рішення умовним: «щойно зʼявиться
// будь-який зовнішній доступ — авторизація стає обовʼязковою ДО того, як
// цей доступ увімкнуть». Умова настала — сервіс виводиться в інтернет
// тунелем (Cloudflare Tunnel; домашня адреса за CGNAT, проброс порту
// неможливий), щоб застосунок відкривався з телефона поза домом.
//
// МОДЕЛЬ — ДВА СЕКРЕТИ, НЕ ОДИН. Пароль набирає людина в браузері й дістає
// cookie на 30 днів; токен несе Home Assistant у заголовку Bearer на
// кожен запит. Розвести їх варто не заради краси: пароль людина міняє й
// забуває, токен живе в конфігурації інтеграції роками — і зміна одного
// не має ламати другого. Обидва порожні = авторизація вимкнена, і сервіс
// поводиться як досі. Це той самий прийом, що MQTTAddr == "" у main.go, і
// саме він тримає testServer у server_test.go без змін.
//
// ЩО ЗАКРИТО. Увесь /api/*, крім login/logout/auth, і читання нарівні із
// записом: GET /api/backup віддає ВСЮ базу, а POST /api/whatif нічого не
// пише — поділ «GET безпечний, POST ні» тут хибний в обидва боки.
//
// ЩО ВІДКРИТО — СТАТИКА. index.html і модулі віддаються без входу, і це
// рішення, а не недогляд. Даних у них немає (усе приходить із /api/*), а
// сам код лежить у публічному репозиторії. Гейт на «/» коштував би двох
// речей: окремої сторінки входу поза застосунком і health-перевірки
// деплою (deploy/lxc-deploy.sh чекає 200 на «/», інакше відкочує
// бінарник). Замість цього застосунок сам ловить 401 і показує діалог
// входу (web/js/app.js).
//
// COOKIE. Значення — «строк:HMAC(строк)», ключ виводиться з пароля:
// SHA-256("oddinvest-session" + пароль). Тому сесії переживають рестарт
// демона (деплой рестартує його на кожен push), а зміна пароля
// розлогінює всіх одразу — окремого секрету в env не потрібно, і його
// свідомо немає. SameSite=Lax, не Strict: дип-лінки зі сповіщень HA
// відкривають застосунок першим переходом, і Strict лишив би їх без
// cookie на кожному відкритті. Secure ставиться лише коли запит прийшов
// по https (заголовок від тунелю або власний TLS): інакше вхід по
// http://192.168.88.73:8080 з дому не зберігся б узагалі.
//
// ПЕРЕБІР. Пʼять невдач з однієї адреси — і наступні спроби на пів
// хвилини дістають 429. Адреса береться з Cf-Connecting-Ip, коли він є
// (за тунелем RemoteAddr — це сам тунель), інакше з RemoteAddr. Це
// пригальмовує підбір, а не робить його неможливим: справжній другий
// замок — Cloudflare Access перед тунелем, і README радить його ввімкнути.

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Auth — секрети, з якими сервер стартує. Нульове значення = вимкнено.
type Auth struct {
	Password string
	Token    string
}

// Enabled — чи взагалі є замок.
func (a Auth) Enabled() bool { return a.Password != "" || a.Token != "" }

const (
	authCookie   = "oi_session"
	authLifetime = 30 * 24 * time.Hour
	// authMaxFails/authLockFor — після скількох невдач і на скільки адреса
	// дістає 429. Пів хвилини — це не захист, а гальмо: 5 спроб за 30 с
	// проти словника — нічого, а людині з одруківкою майже не заважає.
	authMaxFails = 5
	authLockFor  = 30 * time.Second
)

var errNeedLogin = errors.New("потрібен вхід")

// authState — лічильники невдач у памʼяті. Губляться на рестарті, і це
// нормально: рестарт сам по собі коштує дорожче за 5 спроб.
type authState struct {
	mu    sync.Mutex
	fails map[string]authFail
	now   func() time.Time
}

type authFail struct {
	n     int
	until time.Time
}

func newAuthState() *authState {
	return &authState{fails: map[string]authFail{}, now: time.Now}
}

// locked — чи адреса зараз під гальмом.
func (s *authState) locked(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.fails[ip]
	if !ok {
		return false
	}
	if !f.until.IsZero() && s.now().Before(f.until) {
		return true
	}
	if !f.until.IsZero() {
		// вікно минуло — рахунок з нуля
		delete(s.fails, ip)
	}
	return false
}

func (s *authState) fail(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.fails[ip]
	f.n++
	if f.n >= authMaxFails {
		f.until = s.now().Add(authLockFor)
	}
	s.fails[ip] = f
}

func (s *authState) success(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.fails, ip)
}

// sessionKey — ключ підпису cookie, виведений з пароля (довід у шапці).
func (a Auth) sessionKey() []byte {
	sum := sha256.Sum256([]byte("oddinvest-session\x00" + a.Password))
	return sum[:]
}

func (a Auth) sign(exp string) string {
	m := hmac.New(sha256.New, a.sessionKey())
	m.Write([]byte(exp))
	return hex.EncodeToString(m.Sum(nil))
}

// issue — значення cookie зі строком на authLifetime від now.
func (a Auth) issue(now time.Time) string {
	exp := strconv.FormatInt(now.Add(authLifetime).Unix(), 10)
	return exp + ":" + a.sign(exp)
}

// valid — чи cookie наша й не прострочена.
func (a Auth) valid(v string, now time.Time) bool {
	if a.Password == "" {
		return false
	}
	exp, sig, ok := strings.Cut(v, ":")
	if !ok {
		return false
	}
	n, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || now.Unix() >= n {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(sig), []byte(a.sign(exp))) == 1
}

// tokenOK — Bearer з заголовка збігається з токеном машин.
func (a Auth) tokenOK(r *http.Request) bool {
	if a.Token == "" {
		return false
	}
	h := r.Header.Get("Authorization")
	t, ok := strings.CutPrefix(h, "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimSpace(t)), []byte(a.Token)) == 1
}

// authed — чи запит має право на /api/*.
func (s *Server) authed(r *http.Request) bool {
	if !s.auth.Enabled() {
		return true
	}
	if s.auth.tokenOK(r) {
		return true
	}
	if c, err := r.Cookie(authCookie); err == nil {
		return s.auth.valid(c.Value, time.Now())
	}
	return false
}

// authExempt — маршрути, які мусять працювати ДО входу.
func authExempt(path string) bool {
	switch path {
	case "/api/login", "/api/logout", "/api/auth":
		return true
	}
	return false
}

// requireAuth — замок на /api/*. Статика проходить повз (довід у шапці).
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && !authExempt(r.URL.Path) && !s.authed(r) {
			writeErr(w, http.StatusUnauthorized, errNeedLogin)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// secureRequest — чи запит дійшов по https. За тунелем TLS термінується
// не в нас, і про нього каже лише заголовок.
func secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// clientIP — ключ лічильника невдач. Cf-Connecting-Ip — лише він, а не
// X-Forwarded-For цілком: перший підробляється не легше за RemoteAddr,
// коли запит іде через тунель, а другий пишеться ким завгодно.
func clientIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("Cf-Connecting-Ip")); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// GET /api/auth — чи є замок і чи цей запит уже за ним. Єдиний маршрут,
// яким UI дізнається, показувати діалог входу чи ні, — і він відкритий
// навмисно, інакше питання «чи треба входити» саме вимагало б входу.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"enabled": s.auth.Enabled(),
		"ok":      s.authed(r),
	})
}

// POST /api/login {password} → cookie.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.auth.Password == "" {
		// Замка на пароль немає (вимкнено або лише токен) — входити нема
		// куди; 404, а не 401: маршруту в цій конфігурації ніби й не існує.
		writeErr(w, http.StatusNotFound, errors.New("вхід за паролем вимкнено"))
		return
	}
	ip := clientIP(r)
	if s.authFails.locked(ip) {
		writeErr(w, http.StatusTooManyRequests, errors.New("забагато спроб — спробуй за пів хвилини"))
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if subtle.ConstantTimeCompare([]byte(in.Password), []byte(s.auth.Password)) != 1 {
		s.authFails.fail(ip)
		s.log.Warn("невдалий вхід", "ip", ip)
		writeErr(w, http.StatusUnauthorized, errors.New("пароль не підходить"))
		return
	}
	s.authFails.success(ip)
	s.setSessionCookie(w, r, s.auth.issue(time.Now()), int(authLifetime/time.Second))
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/logout — стерти cookie. Відкритий: вийти можна й із
// простроченою сесією.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.setSessionCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}
