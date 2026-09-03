package api

// Авторизація REST: пароль людини, токен машин, обидва в сховищі.
//
// НАВІЩО. README тримав це рішення умовним: «щойно зʼявиться будь-який
// зовнішній доступ — авторизація стає обовʼязковою ДО того, як цей доступ
// увімкнуть». Умова настала — сервіс виводиться в інтернет тунелем
// (Cloudflare Tunnel; домашня адреса за CGNAT, проброс порту неможливий),
// щоб застосунок відкривався з телефона поза домом.
//
// ЧОМУ НЕ ENV. Спершу пароль і токен читались із двох змінних
// /etc/oddinvestd.env. Ціна: щоб поставити замок, треба SSH у контейнер, а
// щоб змінити пароль — знову SSH і рестарт демона. Для застосунку, у якого
// один власник і один екран, це двері, до яких ключ лежить в іншій
// кімнаті. Тепер джерело правди одне — таблиця `secrets` (міграція 0053), і
// перший вхід сам просить задати пароль.
//
// МОДЕЛЬ — ДВА СЕКРЕТИ, НЕ ОДИН. Пароль набирає людина в браузері й дістає
// cookie на 30 днів; токен несе Home Assistant у заголовку Bearer на
// кожен запит. Розвести їх варто не заради краси: пароль людина міняє й
// забуває, токен живе в конфігурації інтеграції роками — і зміна одного
// не має ламати другого. Пароля немає = замка немає, і сервіс поводиться
// як досі; ЦЕ СТАН ПЕРШОГО ЗАПУСКУ, а не режим роботи — застосунок у ньому
// показує форму «задай пароль» і не дає її закрити.
//
// ТОКЕН БЕЗ ПАРОЛЯ НЕ БУВАЄ. Enabled питає лише про пароль: токен видає
// сторінка «Доступ ззовні», яка сама за замком, тож без пароля взятись
// йому нізвідки. Одне питання замість двох — і не буває стану «токен є,
// пароля немає», у якому браузер не міг би увійти взагалі.
//
// ЩО ЗАКРИТО. Увесь /api/*, крім auth/login/logout/setup, і читання нарівні
// із записом: GET /api/backup віддає ВСЮ базу, а POST /api/whatif нічого не
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
// ПАРОЛЬ У СХОВИЩІ — ЛИШЕ ХЕШЕМ: PBKDF2-SHA256, 600 000 ітерацій, сіль на
// кожен пароль (crypto/pbkdf2 зі stdlib — жодної нової залежності).
// Порівняння constant-time. Таблиця `secrets` не входить у бекап і в
// добовий дамп, тож пароль не витікає файлом і не підміняється чужим
// відновленням.
//
// COOKIE. Значення — «строк:HMAC(строк)», ключ — окремий випадковий
// session_secret зі сховища. Він ОБЕРТАЄТЬСЯ при кожній зміні пароля й при
// -reset-auth: та сама властивість «зміна пароля розлогінює всіх», яку
// раніше давав ключ, виведений із самого пароля. Сесії переживають рестарт
// демона (деплой рестартує його на кожен push), бо секрет лежить у базі.
// SameSite=Lax, не Strict: дип-лінки зі сповіщень HA відкривають застосунок
// першим переходом, і Strict лишив би їх без cookie на кожному відкритті.
// Secure ставиться лише коли запит прийшов по https (заголовок від тунелю
// або власний TLS): інакше вхід по http://192.168.88.73:8080 з дому не
// зберігся б узагалі.
//
// ПЕРЕБІР. Пʼять невдач з однієї адреси — і наступні спроби на пів
// хвилини дістають 429. Адреса береться з Cf-Connecting-Ip, коли він є
// (за тунелем RemoteAddr — це сам тунель), інакше з RemoteAddr. Це
// пригальмовує підбір, а не робить його неможливим: справжній другий
// замок — Cloudflare Access перед тунелем, і README радить його ввімкнути.
//
// ЗАБУТИЙ ПАРОЛЬ — `oddinvestd -reset-auth` у контейнері (cmd/oddinvestd).
// Скидання вимагає доступу до машини, тобто того самого рівня довіри, що й
// правка env раніше.

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ODDsama/oddinvest/internal/store"
)

const (
	authCookie   = "oi_session"
	authLifetime = 30 * 24 * time.Hour
	// authMaxFails/authLockFor — після скількох невдач і на скільки адреса
	// дістає 429. Пів хвилини — це не захист, а гальмо: 5 спроб за 30 с
	// проти словника — нічого, а людині з одруківкою майже не заважає.
	authMaxFails = 5
	authLockFor  = 30 * time.Second
	// authMinPassword — коротший пароль не приймається. Вісім символів —
	// не про криптографію, а про те, щоб «1234» не став замком, який
	// виглядає замком.
	authMinPassword = 8
	// pbkdfIters/pbkdfSaltLen/pbkdfKeyLen — параметри хеша. Ітерації в
	// самому хеші (формат нижче), тож підняти їх колись можна без міграції:
	// старі хеші перевіряться своїм числом.
	pbkdfIters   = 600_000
	pbkdfSaltLen = 16
	pbkdfKeyLen  = 32
)

var (
	errNeedLogin  = errors.New("потрібен вхід")
	errNoPassword = errors.New("пароль ще не заданий")
)

// authCache — секрети в памʼяті, щоб кожен запит не ходив у базу.
//
// Читається на кожному запиті (requireAuth), пишеться лише при зміні
// пароля чи токена — звідси RWMutex. Перезаряджається явно, з бази:
// зберігати «те, що щойно записали» окремо означало б два джерела правди
// в одному процесі.
type authCache struct {
	mu           sync.RWMutex
	passwordHash string
	sessionKey   []byte
	haTokenHash  string
}

func (a *authCache) snapshot() (hash string, key []byte, token string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.passwordHash, a.sessionKey, a.haTokenHash
}

func (a *authCache) set(hash, sessionSecret, token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.passwordHash, a.haTokenHash = hash, token
	a.sessionKey = nil
	if sessionSecret != "" {
		sum := sha256.Sum256([]byte("oddinvest-session\x00" + sessionSecret))
		a.sessionKey = sum[:]
	}
}

// reloadAuth перечитує секрети зі сховища в кеш. Кличеться на старті
// сервера й після кожного запису.
func (s *Server) reloadAuth(ctx context.Context) error {
	sec, err := s.st.AllSecrets(ctx)
	if err != nil {
		return err
	}
	s.auth.set(sec[store.SecretPasswordHash], sec[store.SecretSessionSecret], sec[store.SecretHATokenHash])
	return nil
}

// authEnabled — чи взагалі є замок. Питає лише про пароль (довід у шапці).
func (s *Server) authEnabled() bool {
	hash, _, _ := s.auth.snapshot()
	return hash != ""
}

// --- пароль ---

// hashPassword — «pbkdf2-sha256$<ітерації>$<сіль b64>$<хеш b64>».
// Самоописовий рядок: параметри лежать поруч зі значенням, тож зміна
// вартості не потребує ані міграції, ані другого поля.
func hashPassword(pw string) (string, error) {
	salt := make([]byte, pbkdfSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, pbkdfIters, pbkdfKeyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdfIters,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// verifyPassword — чи цей пароль дає той самий хеш. Хибний формат читається
// як «не збігається»: сміття в базі не має валити вхід панікою.
func verifyPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iters, err := strconv.Atoi(parts[1])
	if err != nil || iters <= 0 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iters, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// setPassword — записати пароль і ОБЕРНУТИ ключ сесій (довід у шапці).
func (s *Server) setPassword(ctx context.Context, pw string) error {
	hash, err := hashPassword(pw)
	if err != nil {
		return err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	if err := s.st.SetSecret(ctx, store.SecretPasswordHash, hash); err != nil {
		return err
	}
	if err := s.st.SetSecret(ctx, store.SecretSessionSecret, hex.EncodeToString(secret)); err != nil {
		return err
	}
	return s.reloadAuth(ctx)
}

// checkNewPassword — спільні вимоги до пароля для setup і зміни.
func checkNewPassword(pw, confirm string) error {
	if len([]rune(pw)) < authMinPassword {
		return fmt.Errorf("пароль коротший за %d символів", authMinPassword)
	}
	if pw != confirm {
		return errors.New("паролі не збігаються")
	}
	return nil
}

// --- cookie ---

func sign(key []byte, exp string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(exp))
	return hex.EncodeToString(m.Sum(nil))
}

// issue — значення cookie зі строком на authLifetime від now.
func issue(key []byte, now time.Time) string {
	exp := strconv.FormatInt(now.Add(authLifetime).Unix(), 10)
	return exp + ":" + sign(key, exp)
}

// validCookie — чи cookie наша й не прострочена.
func validCookie(key []byte, v string, now time.Time) bool {
	if len(key) == 0 {
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
	return subtle.ConstantTimeCompare([]byte(sig), []byte(sign(key, exp))) == 1
}

// --- токен машин ---

func tokenHash(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// tokenOK — Bearer із заголовка збігається з хешем токена машин.
func tokenOK(want string, r *http.Request) bool {
	if want == "" {
		return false
	}
	t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(tokenHash(strings.TrimSpace(t))), []byte(want)) == 1
}

// --- гальмо перебору ---

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

// --- middleware ---

// authed — чи запит має право на /api/*.
func (s *Server) authed(r *http.Request) bool {
	hash, key, token := s.auth.snapshot()
	if hash == "" {
		return true // замка немає: перший запуск
	}
	if tokenOK(token, r) {
		return true
	}
	if c, err := r.Cookie(authCookie); err == nil {
		return validCookie(key, c.Value, time.Now())
	}
	return false
}

// authExempt — маршрути, які мусять працювати ДО входу.
func authExempt(path string) bool {
	switch path {
	case "/api/login", "/api/logout", "/api/auth", "/api/auth/setup":
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

// startSession — видати cookie на щойно доведене право входу.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request) {
	_, key, _ := s.auth.snapshot()
	s.setSessionCookie(w, r, issue(key, time.Now()), int(authLifetime/time.Second))
}

// --- маршрути ---

// GET /api/auth — чи є замок, чи цей запит за ним, чи треба ще задати
// пароль і чи виданий токен машин. Єдиний маршрут, яким UI дізнається, що
// показувати, — і він відкритий навмисно, інакше питання «чи треба
// входити» саме вимагало б входу.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	hash, _, token := s.auth.snapshot()
	writeJSON(w, http.StatusOK, map[string]bool{
		"enabled":   hash != "",
		"ok":        s.authed(r),
		"setup":     hash == "",
		"has_token": token != "",
	})
}

// POST /api/auth/setup {password, confirm} — перший пароль.
//
// Відкритий рівно доти, доки пароля немає: інакше це був би скидач пароля
// без жодної перевірки. Далі та сама ручка відповідає 409, а міняють
// пароль уже під замком (PUT /api/auth/password).
func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	if s.authEnabled() {
		writeErr(w, http.StatusConflict, errors.New("пароль уже заданий — міняти його можна лише зсередини"))
		return
	}
	var in struct {
		Password string `json:"password"`
		Confirm  string `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := checkNewPassword(in.Password, in.Confirm); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.setPassword(r.Context(), in.Password); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.log.Info("пароль задано — сервіс закрито")
	s.startSession(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// PUT /api/auth/password {current, password, confirm} — зміна пароля.
//
// Під замком, тож «поточний» тут не про право доступу (його вже довела
// cookie), а про крадену сесію: телефон, що лишився розблокованим, не має
// давати можливості перепризначити пароль.
func (s *Server) handleAuthPassword(w http.ResponseWriter, r *http.Request) {
	hash, _, _ := s.auth.snapshot()
	if hash == "" {
		writeErr(w, http.StatusConflict, errNoPassword)
		return
	}
	ip := clientIP(r)
	if s.authFails.locked(ip) {
		writeErr(w, http.StatusTooManyRequests, errors.New("забагато спроб — спробуй за пів хвилини"))
		return
	}
	var in struct {
		Current  string `json:"current"`
		Password string `json:"password"`
		Confirm  string `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !verifyPassword(hash, in.Current) {
		s.authFails.fail(ip)
		s.log.Warn("невдала зміна пароля", "ip", ip)
		writeErr(w, http.StatusUnauthorized, errors.New("поточний пароль не підходить"))
		return
	}
	if err := checkNewPassword(in.Password, in.Confirm); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.authFails.success(ip)
	if err := s.setPassword(r.Context(), in.Password); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Ключ сесій обернувся — стара cookie вже мертва, зокрема й у цього
	// браузера. Видаємо нову одразу: інакше зміна пароля виглядала б як
	// вихід із застосунку.
	s.startSession(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/auth/token — видати новий токен машин.
//
// Повертається РІВНО ТУТ і більше ніде: у базі лишається лише його
// SHA-256. Забув — видай новий; старий після цього мертвий, і саме тому
// сторінка перепитує.
func (s *Server) handleAuthToken(w http.ResponseWriter, r *http.Request) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	tok := hex.EncodeToString(buf)
	if err := s.st.SetSecret(r.Context(), store.SecretHATokenHash, tokenHash(tok)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.reloadAuth(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": tok})
}

// DELETE /api/auth/token — відкликати токен машин.
func (s *Server) handleAuthTokenRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteSecret(r.Context(), store.SecretHATokenHash); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.reloadAuth(r.Context()); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/login {password} → cookie.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	hash, _, _ := s.auth.snapshot()
	if hash == "" {
		// Замка немає — входити нема куди; 404, а не 401: маршруту в цьому
		// стані ніби й не існує, а UI натомість показує форму «задай пароль».
		writeErr(w, http.StatusNotFound, errNoPassword)
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
	if !verifyPassword(hash, in.Password) {
		s.authFails.fail(ip)
		s.log.Warn("невдалий вхід", "ip", ip)
		writeErr(w, http.StatusUnauthorized, errors.New("пароль не підходить"))
		return
	}
	s.authFails.success(ip)
	s.startSession(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/logout — стерти cookie. Відкритий: вийти можна й із
// простроченою сесією.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.setSessionCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}
