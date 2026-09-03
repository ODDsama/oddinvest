package tunnel

// Сертифікат Let's Encrypt на те саме імʼя, що й тунель, — щоб домен
// працював і ВДОМА, повз Cloudflare.
//
// НАВІЩО. Тунель відкриває адресу звідусіль, але кожен запит із сусідньої
// кімнати їде в чужий дата-центр і назад: повільніше, і без інтернету не
// працює взагалі. Якщо домашній DNS скерувати те саме імʼя просто на LXC
// (один рядок у AdGuard Home), запит лишиться в межах будинку — але
// браузер вимагатиме сертифікат уже від нас, бо TLS більше нема кому
// термінувати. Звідси цей файл.
//
// ОДНА АДРЕСА, А НЕ ДВІ, і це не про красу. http://192.168.88.73:8080 і
// https://oddinvest.<домен> — РІЗНІ origin: різні cookie сесії, різний
// localStorage (розкриті рядки, недавнє в палітрі), різна іконка PWA.
// Один домен на обидва шляхи робить із двох застосунків один.
//
// ЧОМУ DNS-01, А НЕ ПРОСТІШЕ. Перевірки http-01 і tls-alpn-01 вимагають,
// щоб Let's Encrypt достукався ззовні, тобто через тунель — а перед ним
// стоїть Cloudflare Access, який заблокує й перевірку теж; поновлення тоді
// залежало б ще й від живого тунелю. DNS-01 не потребує жодного відкритого
// порту, а токен Cloudflare із правом DNS:Edit у нас УЖЕ Є — той самий,
// яким створювався тунель. Тобто нових питань до власника нуль.
//
// ЧОМУ НЕ autocert. Він уміє лише http-01 і tls-alpn-01 — рівно ті дві
// перевірки, які тут не годяться. Тож нижче низькорівневий acme.Client:
// акаунтний ключ, order, TXT, Accept, CSR.
//
// ЧОМУ НЕ СВІЙ CA. Самопідписаний корінь довелось би окремо ставити й
// довіряти на кожному телефоні, і на iOS це ще й два різні екрани
// налаштувань. Ціна одноразова лише на вигляд: вона повертається з кожним
// новим пристроєм.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/acme"

	"github.com/ODDsama/oddinvest/internal/store"
)

const (
	// acmeDirectory — бойовий каталог Let's Encrypt. Полем менеджера, а не
	// константою у виклику: тест підставляє свій.
	acmeDirectory = "https://acme-v02.api.letsencrypt.org/directory"
	// certRenewBefore — за скільки до кінця поновлювати. Тридцять днів при
	// строку в девʼяносто: якщо поновлення ламається, лишається місяць,
	// щоб це помітити й полагодити, а не ніч.
	certRenewBefore = 30 * 24 * time.Hour
	// certCheckEvery — як часто дивитись на строк. Двічі на добу, бо
	// перевірка коштує читання рядка з бази, а пропущений день на межі
	// тридцяти діб коштував би сертифіката.
	certCheckEvery = 12 * time.Hour
	// dnsPropagateWait — скільки чекати, доки TXT-запис побачать усі.
	//
	// Чекаємо САМІ, а не сподіваємось: невдала перевірка коштує дорожче за
	// очікування. Let's Encrypt рахує провали (пʼять на імʼя за годину), і
	// поспіх обертається годинним простоєм замість двох хвилин терпіння.
	dnsPropagateWait = 2 * time.Minute
	dnsPollEvery     = 5 * time.Second
	// acmeResolver — у кого питати про поширення TXT. Прямо в Cloudflare,
	// повз системний резолвер: удома системний — це AdGuard, який для
	// нашого ж домену матиме власний перезапис і відповість інакше, ніж
	// відповість авторитетний сервер Let's Encrypt.
	acmeResolver = "1.1.1.1:53"
)

// certState — сертифікат у памʼяті. Окремим типом, а не полями менеджера,
// щоб читання під RWMutex було одним присвоєнням.
type certState struct {
	cert    *tls.Certificate
	expires time.Time
}

// Certificate — для tls.Config.GetCertificate.
//
// Сертифіката ще немає → помилка, і рукостискання просто не відбувається.
// Слухач при цьому лишається живим, тож щойно сертифікат зʼявиться, 443
// починає працювати БЕЗ перезапуску демона.
func (m *Manager) Certificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cert.cert == nil {
		return nil, errors.New("сертифіката ще немає — його видає сторінка «Доступ ззовні»")
	}
	return m.cert.cert, nil
}

// loadCert піднімає сертифікат зі сховища в памʼять. Кличеться на старті й
// після кожної видачі.
func (m *Manager) loadCert(ctx context.Context) error {
	sec, err := m.st.AllSecrets(ctx)
	if err != nil {
		return err
	}
	certPEM, keyPEM := sec[store.SecretCertPEM], sec[store.SecretCertKeyPEM]
	if certPEM == "" || keyPEM == "" {
		return nil
	}
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return fmt.Errorf("збережений сертифікат не читається: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("збережений сертифікат не розбирається: %w", err)
	}
	pair.Leaf = leaf
	m.mu.Lock()
	m.cert = certState{cert: &pair, expires: leaf.NotAfter}
	m.mu.Unlock()
	return nil
}

// needsRenewal — чи час поновлювати. Нульовий строк означає «сертифіката
// немає», тобто теж час.
func needsRenewal(expires time.Time, now time.Time) bool {
	return expires.IsZero() || now.Add(certRenewBefore).After(expires)
}

// CertStatus — стан сертифіката для сторінки.
type CertStatus struct {
	Have    bool   `json:"have"`
	Expires string `json:"expires,omitempty"`
	Issuing bool   `json:"issuing,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (m *Manager) certStatus(sec map[string]string) CertStatus {
	m.mu.Lock()
	out := CertStatus{Have: m.cert.cert != nil, Issuing: m.issuing}
	if !m.cert.expires.IsZero() {
		out.Expires = m.cert.expires.Format("2006-01-02")
	}
	m.mu.Unlock()
	out.Error = sec[store.SecretCertError]
	return out
}

// EnsureCert видає сертифікат, якщо його немає або строк добігає.
//
// Один одночасно: прапорець issuing. Дві видачі паралельно — це дві
// авторизації на те саме імʼя й два TXT-записи, які затирають один одного,
// тобто найтихіший спосіб зіпсувати саме те, що робиш.
func (m *Manager) EnsureCert(ctx context.Context, force bool) error {
	m.mu.Lock()
	if m.issuing {
		m.mu.Unlock()
		return errors.New("сертифікат уже видається — зачекай")
	}
	need := force || needsRenewal(m.cert.expires, time.Now())
	if !need {
		m.mu.Unlock()
		return nil
	}
	m.issuing = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.issuing = false
		m.mu.Unlock()
	}()

	err := m.issueCert(ctx)
	// Причина невдачі лишається в базі, а не тільки в журналі: сторінка
	// мусить сказати, ЩО саме відповів Let's Encrypt, — це те, що людина
	// піде виправляти (права токена, зона, ліміт).
	msg := ""
	if err != nil {
		msg = err.Error()
		m.log.Error("сертифікат не видався", "err", err)
	}
	if serr := m.st.SetSecret(ctx, store.SecretCertError, msg); serr != nil {
		m.log.Warn("причина невдачі не записалась", "err", serr)
	}
	return err
}

// issueCert — сама видача. Порядок кроків тут і є протокол ACME.
func (m *Manager) issueCert(ctx context.Context) error {
	sec, err := m.st.AllSecrets(ctx)
	if err != nil {
		return err
	}
	host, zone, token := sec[store.SecretCFHostname], sec[store.SecretCFZoneID], sec[store.SecretCFAPIToken]
	if host == "" || zone == "" || token == "" {
		return errors.New("спершу підключи тунель: сертифікат видається на те саме імʼя і тим самим токеном")
	}

	key, err := m.acmeAccountKey(ctx, sec[store.SecretACMEAccountKey])
	if err != nil {
		return err
	}
	client := &acme.Client{Key: key, DirectoryURL: m.acmeURL()}
	// Наявний акаунт — не помилка: ключ той самий, отже й акаунт той самий.
	if _, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil &&
		!errors.Is(err, acme.ErrAccountAlreadyExists) {
		return fmt.Errorf("реєстрація в Let's Encrypt: %w", err)
	}

	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(host))
	if err != nil {
		return fmt.Errorf("замовлення сертифіката: %w", err)
	}
	for _, authzURL := range order.AuthzURLs {
		if err := m.solveDNS01(ctx, client, authzURL, zone, host, token); err != nil {
			return err
		}
	}
	if order, err = client.WaitOrder(ctx, order.URI); err != nil {
		return fmt.Errorf("очікування замовлення: %w", err)
	}

	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
	}, certKey)
	if err != nil {
		return err
	}
	// bundle=true: разом із ланцюгом проміжних. Без нього браузер на
	// телефоні лаявся б на «невідомий видавець» саме там, де перевірити
	// нема чим.
	der, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return fmt.Errorf("видача сертифіката: %w", err)
	}
	return m.saveCert(ctx, der, certKey)
}

// solveDNS01 проходить одну авторизацію: ставить TXT, чекає поширення,
// каже «перевіряй», чекає вироку.
func (m *Manager) solveDNS01(ctx context.Context, client *acme.Client,
	authzURL, zone, host, token string) error {

	authz, err := client.GetAuthorization(ctx, authzURL)
	if err != nil {
		return fmt.Errorf("авторизація імені: %w", err)
	}
	if authz.Status == acme.StatusValid {
		return nil // цю адресу вже підтверджено раніше
	}
	var chal *acme.Challenge
	for _, c := range authz.Challenges {
		if c.Type == "dns-01" {
			chal = c
			break
		}
	}
	if chal == nil {
		return errors.New("серед перевірок Let's Encrypt немає dns-01")
	}
	value, err := client.DNS01ChallengeRecord(chal.Token)
	if err != nil {
		return err
	}

	name := "_acme-challenge." + host
	cf := New(m.base, token)
	recID, err := cf.UpsertTXT(ctx, zone, name, value)
	if err != nil {
		return fmt.Errorf("запис перевірки в DNS: %w", err)
	}
	// Прибираємо ЗАВЖДИ — і після успіху, і після провалу: покинутий
	// _acme-challenge у зоні нічого не ламає, але наступного разу
	// спантеличить того, хто дивитиметься на записи.
	defer func() {
		if err := cf.DeleteRecord(context.WithoutCancel(ctx), zone, recID); err != nil {
			m.log.Warn("запис перевірки не прибрався", "err", err)
		}
	}()

	if err := waitTXT(ctx, name, value); err != nil {
		return err
	}
	if _, err := client.Accept(ctx, chal); err != nil {
		return fmt.Errorf("початок перевірки: %w", err)
	}
	if _, err := client.WaitAuthorization(ctx, authzURL); err != nil {
		return fmt.Errorf("перевірка не пройшла: %w", err)
	}
	return nil
}

// waitTXT — доки авторитетний DNS не почне віддавати наше значення.
//
// Питаємо Cloudflare напряму (acmeResolver), повз системний резолвер:
// удома системний — це AdGuard із власними перезаписами, і його відповідь
// не каже нічого про те, що побачить Let's Encrypt.
func waitTXT(ctx context.Context, name, want string) error {
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, acmeResolver)
		},
	}
	deadline := time.Now().Add(dnsPropagateWait)
	var last error
	for {
		vals, err := r.LookupTXT(ctx, name)
		last = err
		for _, v := range vals {
			if strings.TrimSpace(v) == want {
				return nil
			}
		}
		if time.Now().After(deadline) {
			if last != nil {
				return fmt.Errorf("запис перевірки не поширився за %v: %w", dnsPropagateWait, last)
			}
			return fmt.Errorf("запис перевірки не поширився за %v", dnsPropagateWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(dnsPollEvery):
		}
	}
}

// acmeAccountKey — наш ключ у Let's Encrypt: зі сховища або новий.
func (m *Manager) acmeAccountKey(ctx context.Context, stored string) (*ecdsa.PrivateKey, error) {
	if stored != "" {
		block, _ := pem.Decode([]byte(stored))
		if block == nil {
			return nil, errors.New("акаунтний ключ ACME не читається")
		}
		return x509.ParseECPrivateKey(block.Bytes)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	raw, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	enc := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: raw})
	return key, m.st.SetSecret(ctx, store.SecretACMEAccountKey, string(enc))
}

// saveCert кладе ланцюг і ключ у сховище й одразу в памʼять.
func (m *Manager) saveCert(ctx context.Context, der [][]byte, key *ecdsa.PrivateKey) error {
	var chain strings.Builder
	for _, b := range der {
		if err := pem.Encode(&chain, &pem.Block{Type: "CERTIFICATE", Bytes: b}); err != nil {
			return err
		}
	}
	raw, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: raw})
	leaf, err := x509.ParseCertificate(der[0])
	if err != nil {
		return err
	}
	for _, kv := range [][2]string{
		{store.SecretCertPEM, chain.String()},
		{store.SecretCertKeyPEM, string(keyPEM)},
		{store.SecretCertExpires, leaf.NotAfter.Format(time.RFC3339)},
	} {
		if err := m.st.SetSecret(ctx, kv[0], kv[1]); err != nil {
			return err
		}
	}
	m.log.Info("сертифікат видано", "host", leaf.Subject.CommonName, "до", leaf.NotAfter.Format("2006-01-02"))
	return m.loadCert(ctx)
}

// renewLoop стежить за строком.
//
// ВЛАСНА горутина, а не добова джоба: та живе під пʼятихвилинним
// контекстом, а видача з очікуванням поширення DNS буває довшою — і
// обрізана посеред авторизації вона лишила б у зоні TXT і спалила б
// спробу з ліміту.
func (m *Manager) renewLoop(ctx context.Context) {
	for {
		// Порожнє імʼя означає «тунелю ще немає», і це не привід шуміти:
		// сертифікат видається на його адресу, тож до підключення робити
		// нічого. Помилка читання — інша річ, її видно в журналі.
		host, err := m.st.GetSecret(ctx, store.SecretCFHostname)
		switch {
		case err != nil:
			m.log.Warn("перевірка строку сертифіката", "err", err)
		case host != "":
			m.mu.Lock()
			exp := m.cert.expires
			m.mu.Unlock()
			if needsRenewal(exp, time.Now()) {
				if err := m.EnsureCert(ctx, false); err != nil {
					m.log.Warn("поновлення сертифіката", "err", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.certEvery):
		}
	}
}

// lanIPs — приватні адреси цієї машини в локальній мережі, ПОТРІБНОЮ
// першою.
//
// Потрібні рівно для однієї фрази на сторінці: «пропиши в домашньому DNS
// таке імʼя на таку адресу». Без них людина мусила б іти шукати адресу
// контейнера в іншому місці — а застосунок її знає.
//
// ПОРЯДОК ТУТ І Є ВІДПОВІДЬ. Машина легко має пʼять приватних адрес:
// мости docker, підмережі віртуалізації, службові інтерфейси. Картка
// друкує ПЕРШУ, і якщо нею виявиться міст docker, людина скопіює рядок,
// який мовчки не працюватиме. Тому першою йде адреса, з якої машина ходить
// у світ, — саме її бачать сусіди по домашній мережі.
//
// Лише IPv4 і лише приватні: публічна адреса тут була б неправдою (у нас
// CGNAT), а IPv6 у домашніх перезаписах DNS майже ніхто не веде.
func lanIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	primary := primaryIP()
	var out []string
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := n.IP.To4()
		if ip == nil || ip.IsLoopback() || !ip.IsPrivate() || ip.String() == primary {
			continue
		}
		out = append(out, ip.String())
	}
	if primary != "" {
		out = append([]string{primary}, out...)
	}
	return out
}

// primaryIP — адреса, з якої машина виходить у мережу.
//
// UDP-«зʼєднання» нічого не надсилає й нікуди не йде: ядро лише обирає
// маршрут і призначає локальну адресу, яку ми й читаємо. Тобто відповідь
// не залежить ані від інтернету, ані від того, чи 1.1.1.1 узагалі живий.
func primaryIP() string {
	c, err := net.Dial("udp", "1.1.1.1:80")
	if err != nil {
		return ""
	}
	defer c.Close() //nolint:errcheck // нічого не надсилали, закриття без наслідків
	addr, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil {
		return ""
	}
	ip := addr.IP.To4()
	if ip == nil || !ip.IsPrivate() {
		return ""
	}
	return ip.String()
}
