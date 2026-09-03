package tunnel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ODDsama/oddinvest/internal/store"
)

// tunnelName — назва тунелю в акаунті Cloudflare. Стала: другий тунель
// того самого застосунку на тій самій машині не має сенсу, а стала назва
// дає ідемпотентність — повторне «Підключити» знаходить свій, а не плодить
// «oddinvest-2».
const tunnelName = "oddinvest"

const (
	// backoffMin/backoffMax — від скількох до скількох чекати між
	// перезапусками конектора. Хвилина стелі, а не година: тунель — це
	// доступ, і людина, яка щойно полагодила мережу, не має чекати довше
	// за чашку кави.
	backoffMin = 5 * time.Second
	backoffMax = time.Minute
	// aliveResets — скільки конектор має прожити, щоб затримка скинулась.
	// Без цього два падіння поспіль через різні причини сходились би в
	// одну довгу паузу.
	aliveResets = 60 * time.Second
	// statusTTL — як довго тримати відповідь Cloudflare про стан тунелю.
	// Сторінка опитує стан щоп'ять секунд, поки йде підключення, а
	// зовнішній API про це не просив.
	statusTTL = 30 * time.Second
)

// Manager — тунель як частина демона: створює його по API, тримає
// конектор живим і вміє все це прибрати.
//
// Стан живе в таблиці secrets, а не в полях: демон перезапускається на
// кожному деплої, і тунель мусить піднятись сам, без другого «Підключити».
type Manager struct {
	st     *store.Store
	log    *slog.Logger
	base   string // база API Cloudflare (тести підставляють свою)
	origin string // куди тунель веде: http://127.0.0.1:<порт>
	home   string // HOME для конектора (юніт його не задає)

	// run — як саме запустити конектор. Полем, щоб тест міг підставити
	// свою функцію: перевіряти нагляд, ганяючи справжній cloudflared,
	// означало б залежати від мережі й від того, що він узагалі є.
	run func(ctx context.Context, token string) error
	// wait0 — початкова затримка перед перезапуском. Полем із тієї самої
	// причини, що pause в jobs.Runner: інакше тест нагляду чекав би
	// справжні пʼять секунд на кожне падіння.
	wait0 time.Duration

	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	running  bool
	lastErr  string
	status   string
	statusAt time.Time
}

func NewManager(st *store.Store, log *slog.Logger, base, origin, home string) *Manager {
	m := &Manager{st: st, log: log, base: base, origin: origin, home: home, wait0: backoffMin}
	m.run = m.runCloudflared
	return m
}

// CloudflaredPath — де лежить конектор; порожньо, якщо його немає.
// Питання ставить сторінка: без бінарника тунель створиться, а зʼєднання
// не буде, і сказати про це треба до, а не після.
func CloudflaredPath() string {
	p, err := exec.LookPath("cloudflared")
	if err != nil {
		return ""
	}
	return p
}

// runCloudflared — конектор дочірнім процесом.
//
// Лише --token: конфіг тунелю живе в хмарі (config_src=cloudflare), тож
// ані файла конфігурації, ані credentials-файла тут не треба — і добре,
// бо під юнітом /root і /etc/cloudflared демонові недосяжні
// (ProtectHome=strict). HOME задається явно з тієї ж причини: юніт із
// User= його не виставляє, а cloudflared шукає в ньому свій кеш.
func (m *Manager) runCloudflared(ctx context.Context, token string) error {
	bin := CloudflaredPath()
	if bin == "" {
		return errors.New("cloudflared не встановлений")
	}
	cmd := exec.CommandContext(ctx, bin, "tunnel", "--no-autoupdate",
		"--loglevel", "warn", "run", "--token", token)
	cmd.Env = append(os.Environ(), "HOME="+m.home)
	// Вивід конектора — у журнал демона: інакше причина «тунель не
	// піднявся» лишилась би в /dev/null.
	cmd.Stdout = logWriter{m.log, slog.LevelInfo}
	cmd.Stderr = logWriter{m.log, slog.LevelWarn}
	return cmd.Run()
}

type logWriter struct {
	log   *slog.Logger
	level slog.Level
}

func (w logWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			w.log.Log(context.Background(), w.level, "cloudflared", "msg", line)
		}
	}
	return len(p), nil
}

// Start піднімає конектор, якщо тунель уже налаштований. Кличеться на
// старті демона: перезапуск сервісу не має вимагати повторного
// «Підключити».
func (m *Manager) Start(ctx context.Context) {
	tok, err := m.st.GetSecret(ctx, store.SecretCFTunnelToken)
	if err != nil || tok == "" {
		return
	}
	m.supervise(ctx, tok)
}

// supervise — цикл нагляду за конектором.
//
// Той самий прийом, що в jobs.RunDaily: select на ctx, ніяких таймерів
// поза ним. Конектор падає рідко, але падає — мережа зникла, Cloudflare
// перезавантажив край, — і застосунок, який після цього мовчки лишається
// без дверей, гірший за застосунок, який їх узагалі не мав.
func (m *Manager) supervise(parent context.Context, token string) {
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return // уже наглядаємо
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	m.cancel, m.done, m.running = cancel, done, true
	m.mu.Unlock()

	go func() {
		defer close(done)
		wait := m.wait0
		for {
			started := time.Now()
			err := m.run(ctx, token)
			if ctx.Err() != nil {
				m.setRunning(false)
				return // зупинили ми самі
			}
			if err != nil {
				m.note(fmt.Sprintf("конектор зупинився: %v", err))
				m.log.Warn("cloudflared завершився", "err", err)
			}
			if time.Since(started) > aliveResets {
				wait = m.wait0
			}
			select {
			case <-ctx.Done():
				m.setRunning(false)
				return
			case <-time.After(wait):
			}
			if wait *= 2; wait > backoffMax {
				wait = backoffMax
			}
		}
	}()
}

func (m *Manager) setRunning(v bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.running = v
}

func (m *Manager) note(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastErr = msg
}

// stop зупиняє нагляд і чекає на конектор.
func (m *Manager) stop() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done = nil, nil
	m.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
	m.setRunning(false)
}

// Close — зупинка при вимкненні демона. Чекає на дочірній процес: інакше
// systemd убив би його разом із демоном, а Cloudflare ще хвилину вважав
// би тунель живим.
func (m *Manager) Close() { m.stop() }

// Connect — створити (або знайти) тунель, привʼязати адресу й запустити
// конектор. Ідемпотентна: повторний виклик із тими самими даними нічого
// не ламає.
func (m *Manager) Connect(ctx context.Context, apiToken, hostname string) error {
	hostname = strings.TrimSpace(strings.ToLower(hostname))
	hostname = strings.TrimPrefix(strings.TrimPrefix(hostname, "https://"), "http://")
	hostname = strings.Trim(hostname, "/")
	if apiToken == "" || hostname == "" {
		return errors.New("потрібні API-токен і адреса")
	}
	c := New(m.base, apiToken)
	zone, err := c.ZoneFor(ctx, hostname)
	if err != nil {
		return err
	}
	tunID, err := c.FindTunnel(ctx, zone.AccountID, tunnelName)
	if err != nil {
		return err
	}
	if tunID == "" {
		if tunID, err = c.CreateTunnel(ctx, zone.AccountID, tunnelName); err != nil {
			return err
		}
	}
	if err := c.PutIngress(ctx, zone.AccountID, tunID, hostname, m.origin); err != nil {
		return err
	}
	recID, err := c.UpsertCNAME(ctx, zone.ID, hostname, tunID+".cfargotunnel.com")
	if err != nil {
		return err
	}
	tok, err := c.TunnelToken(ctx, zone.AccountID, tunID)
	if err != nil {
		return err
	}

	// Записуємо ПІСЛЯ того, як усе вдалось: половина реквізитів у базі
	// після невдалої спроби виглядала б як налаштований тунель.
	for _, kv := range [][2]string{
		{store.SecretCFAPIToken, apiToken},
		{store.SecretCFAccountID, zone.AccountID},
		{store.SecretCFZoneID, zone.ID},
		{store.SecretCFTunnelID, tunID},
		{store.SecretCFTunnelToken, tok},
		{store.SecretCFHostname, hostname},
		{store.SecretCFDNSRecordID, recID},
		{store.SecretCFLastError, ""},
	} {
		if err := m.st.SetSecret(ctx, kv[0], kv[1]); err != nil {
			return err
		}
	}
	// Публічна адреса — звичайне налаштування: її читає HA з документа
	// стану, і саме тому вона не секрет.
	if err := m.st.SetSetting(ctx, "public_url", "https://"+hostname); err != nil {
		return err
	}
	m.note("")
	m.stop()
	m.supervise(context.WithoutCancel(ctx), tok)
	return nil
}

// Disconnect — прибрати за собою: зупинити конектор, видалити запис DNS і
// тунель, стерти реквізити.
//
// Помилки видалення в Cloudflare НЕ зупиняють прибирання локального
// стану: якщо тунель уже видалили з панелі, застосунок не має лишатись із
// мертвими реквізитами назавжди.
func (m *Manager) Disconnect(ctx context.Context) error {
	sec, err := m.st.AllSecrets(ctx)
	if err != nil {
		return err
	}
	m.stop()
	if tok := sec[store.SecretCFAPIToken]; tok != "" {
		c := New(m.base, tok)
		if z, r := sec[store.SecretCFZoneID], sec[store.SecretCFDNSRecordID]; z != "" && r != "" {
			if err := c.DeleteRecord(ctx, z, r); err != nil {
				m.log.Warn("DNS-запис не видалився", "err", err)
			}
		}
		if a, t := sec[store.SecretCFAccountID], sec[store.SecretCFTunnelID]; a != "" && t != "" {
			if err := c.DeleteTunnel(ctx, a, t); err != nil {
				m.log.Warn("тунель не видалився", "err", err)
			}
		}
	}
	for _, k := range []string{
		store.SecretCFAPIToken, store.SecretCFAccountID, store.SecretCFZoneID,
		store.SecretCFTunnelID, store.SecretCFTunnelToken, store.SecretCFHostname,
		store.SecretCFDNSRecordID, store.SecretCFLastError,
	} {
		if err := m.st.DeleteSecret(ctx, k); err != nil {
			return err
		}
	}
	m.note("")
	return m.st.SetSetting(ctx, "public_url", "")
}

// Status — стан для сторінки «Доступ ззовні».
type Status struct {
	Configured       bool   `json:"configured"`
	Hostname         string `json:"hostname,omitempty"`
	PublicURL        string `json:"public_url,omitempty"`
	CloudflaredFound bool   `json:"cloudflared_found"`
	Running          bool   `json:"running"`
	TunnelStatus     string `json:"tunnel_status,omitempty"`
	PublicOK         bool   `json:"public_ok,omitempty"`
	LastError        string `json:"last_error,omitempty"`
}

// Status збирає стан. Помилка запиту до Cloudflare — не помилка сторінки:
// вона стає рядком last_error, бо сторінка мусить показатись і тоді, коли
// зовнішній API мовчить.
func (m *Manager) Status(ctx context.Context) (Status, error) {
	sec, err := m.st.AllSecrets(ctx)
	if err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	out := Status{
		CloudflaredFound: CloudflaredPath() != "",
		Running:          m.running,
		LastError:        m.lastErr,
		TunnelStatus:     m.status,
	}
	fresh := time.Since(m.statusAt) < statusTTL
	m.mu.Unlock()

	host := sec[store.SecretCFHostname]
	if host == "" {
		return out, nil
	}
	out.Configured = true
	out.Hostname = host
	out.PublicURL = "https://" + host

	if !fresh {
		if a, t := sec[store.SecretCFAccountID], sec[store.SecretCFTunnelID]; a != "" && t != "" {
			st, err := New(m.base, sec[store.SecretCFAPIToken]).TunnelStatus(ctx, a, t)
			if err != nil {
				out.LastError = err.Error()
			} else {
				out.TunnelStatus = st
			}
			m.mu.Lock()
			m.status, m.statusAt = st, time.Now()
			m.mu.Unlock()
		}
	}
	out.PublicOK = m.probe(ctx, out.PublicURL)
	return out, nil
}

// probe — чи відповідає адреса ЗЗОВНІ. Питаємо /api/auth: він відкритий і
// нічого не змінює, тобто найдешевша чесна перевірка «двері працюють».
func (m *Manager) probe(ctx context.Context, base string) bool {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodGet, base+"/api/auth", nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close() //nolint:errcheck // тіло не читаємо: питання лише «відповіло чи ні»
	return resp.StatusCode == http.StatusOK
}

// OriginFromAddr — куди тунель веде, з адреси прослуховування («:8080» →
// http://127.0.0.1:8080). Саме на петлю: тунель піднімає сам демон, тож
// ходити «в себе» зовнішньою адресою нема потреби.
func OriginFromAddr(addr string) string {
	_, port, ok := strings.Cut(addr, ":")
	if !ok || port == "" {
		port = "8080"
	}
	return "http://127.0.0.1:" + port
}

// HomeFor — HOME для конектора: каталог поруч із базою, єдине місце, куди
// демон має право писати (ReadWritePaths у юніті).
func HomeFor(dbPath string) string { return filepath.Dir(dbPath) }
