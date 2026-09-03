// Package tunnel — доступ ззовні через Cloudflare Tunnel: створення
// тунелю по API й нагляд за конектором cloudflared.
//
// НАВІЩО ЦЕ В ЗАСТОСУНКУ. Спершу тунель ставився скриптом у контейнері
// (deploy/lxc-cloudflared.sh) з інтерактивним `cloudflared tunnel login`,
// тобто SSH, браузер і ще один systemd-сервіс із власними реквізитами в
// /root. Для власника, у якого все інше налаштовується на екрані, це був
// єдиний крок, який вимагав чужого інструмента. Тепер він робиться
// формою: API-токен Cloudflare + бажана адреса.
//
// ЧОМУ ТУНЕЛЬ, А НЕ ПРОБРОС ПОРТУ. Домашня адреса за CGNAT — вхідних
// зʼєднань до неї не буває. Конектор ініціює зʼєднання зсередини, TLS
// термінує Cloudflare, і жоден порт назовні не відкривається.
//
// ЧОГО ТУТ СВІДОМО НЕМАЄ. Завантаження самого cloudflared: бінарник
// ставиться пакетом (deploy/), а не тягнеться демоном із інтернету —
// ланцюг постачання не те місце, де варто економити крок. І Cloudflare
// Access: другий замок живе в панелі Cloudflare, керується політиками
// акаунта, і вигляд «ми ним керуємо» був би неправдою в мить, коли
// політику змінили не звідси.
package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBase — база API Cloudflare. Параметром, а не константою в коді
// запитів: тести підставляють httptest (той самий шов, що ODDINVEST_NBU_BASE).
const DefaultBase = "https://api.cloudflare.com/client/v4"

const userAgent = "oddinvestd (+https://github.com/ODDsama/oddinvest)"

// Client — тонка обгортка над API Cloudflare.
type Client struct {
	base  string
	token string
	hc    *http.Client
}

func New(base, apiToken string) *Client {
	if base == "" {
		base = DefaultBase
	}
	return &Client{base: strings.TrimSuffix(base, "/"), token: apiToken,
		hc: &http.Client{Timeout: 30 * time.Second}}
}

// apiResp — спільна оболонка кожної відповіді Cloudflare.
type apiResp struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

// do — запит і розбір оболонки.
//
// Помилка Cloudflare доходить ДОСЛІВНО, з кодом: «Invalid API Token»,
// «Actor does not have permission…», «zone not found» — це рівно те, що
// людина йде виправляти в панелі, і переказ своїми словами лише сховав
// би, куди саме дивитись.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("запит у Cloudflare %s: %w", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // тіло дочитане нижче
	var env apiResp
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("відповідь Cloudflare %s (HTTP %d) не розібралась: %w",
			path, resp.StatusCode, err)
	}
	if !env.Success {
		if len(env.Errors) > 0 {
			e := env.Errors[0]
			return fmt.Errorf("відповідь Cloudflare: %s (код %d)", e.Message, e.Code)
		}
		return fmt.Errorf("відповідь Cloudflare %s: HTTP %d без пояснення", path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

// Zone — зона й акаунт, якому вона належить.
type Zone struct {
	ID        string
	AccountID string
	Name      string
}

// ZoneFor знаходить зону за ПОВНОЮ адресою: oddinvest.example.com →
// example.com.
//
// Перебором суфіксів, а не «взяти два останні сегменти»: у зони буває
// складна назва (example.co.uk), і вгадувати межу за кількістю крапок
// означало б помилятись саме там, де людина цього не чекає. Зон в акаунті
// одиниці, тож запитів теж одиниці.
func (c *Client) ZoneFor(ctx context.Context, hostname string) (Zone, error) {
	parts := strings.Split(strings.Trim(hostname, "."), ".")
	if len(parts) < 2 {
		return Zone{}, fmt.Errorf("%q не схоже на адресу виду oddinvest.example.com", hostname)
	}
	for i := 0; i+1 < len(parts); i++ {
		name := strings.Join(parts[i:], ".")
		var out []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Account struct {
				ID string `json:"id"`
			} `json:"account"`
		}
		if err := c.do(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(name), nil, &out); err != nil {
			return Zone{}, err
		}
		if len(out) > 0 {
			return Zone{ID: out[0].ID, AccountID: out[0].Account.ID, Name: out[0].Name}, nil
		}
	}
	return Zone{}, fmt.Errorf("серед зон Cloudflare немає жодної, що підходить до %q — "+
		"перевір домен і те, що токен має право на цю зону", hostname)
}

type tunnelRow struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// FindTunnel — тунель із такою назвою або порожній id.
//
// is_deleted=false обовʼязковий: видалені тунелі лишаються в списку, і без
// фільтра застосунок «знайшов» би мертвий тунель і мовчки чіпляв би до
// нього адресу.
func (c *Client) FindTunnel(ctx context.Context, accountID, name string) (string, error) {
	var out []tunnelRow
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel?name=%s&is_deleted=false",
		url.PathEscape(accountID), url.QueryEscape(name))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return "", err
	}
	for _, t := range out {
		if t.Name == name {
			return t.ID, nil
		}
	}
	return "", nil
}

// CreateTunnel — тунель, керований із хмари (config_src: cloudflare).
//
// Саме «з хмари», а не локальним конфігом: інакше ingress довелось би
// тримати файлом поруч із демоном, тобто завести другий опис того самого
// й слідкувати, щоб вони не розійшлись.
func (c *Client) CreateTunnel(ctx context.Context, accountID, name string) (string, error) {
	var out tunnelRow
	body := map[string]string{"name": name, "config_src": "cloudflare"}
	err := c.do(ctx, http.MethodPost, "/accounts/"+url.PathEscape(accountID)+"/cfd_tunnel", body, &out)
	return out.ID, err
}

// TunnelToken — токен, яким запускається конектор.
func (c *Client) TunnelToken(ctx context.Context, accountID, tunnelID string) (string, error) {
	var out string
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/token",
		url.PathEscape(accountID), url.PathEscape(tunnelID))
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// PutIngress — куди тунель веде: одна адреса на наш порт і 404 на решту.
//
// Правило «все інше — 404» обовʼязкове за будовою cloudflared: без
// останнього правила без hostname конфіг не приймається.
func (c *Client) PutIngress(ctx context.Context, accountID, tunnelID, hostname, service string) error {
	body := map[string]any{"config": map[string]any{
		"ingress": []map[string]any{
			{"hostname": hostname, "service": service,
				// Origin отримує той самий Host, що прийшов ззовні. Не
				// косметика: застосунок вирішує по X-Forwarded-Proto, чи
				// ставити Secure на cookie, а Host лишається в журналі.
				"originRequest": map[string]any{"httpHostHeader": hostname}},
			{"service": "http_status:404"},
		},
	}}
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/configurations",
		url.PathEscape(accountID), url.PathEscape(tunnelID))
	return c.do(ctx, http.MethodPut, path, body, nil)
}

type dnsRow struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

// UpsertCNAME — запис, який веде адресу в тунель.
//
// Саме upsert: адресу могли вже колись прив'язати (той самий скрипт,
// попередня спроба), і падати на «record already exists» означало б
// вимагати ручного прибирання перед другою спробою.
func (c *Client) UpsertCNAME(ctx context.Context, zoneID, name, target string) (string, error) {
	var found []dnsRow
	list := fmt.Sprintf("/zones/%s/dns_records?type=CNAME&name=%s",
		url.PathEscape(zoneID), url.QueryEscape(name))
	if err := c.do(ctx, http.MethodGet, list, nil, &found); err != nil {
		return "", err
	}
	body := map[string]any{"type": "CNAME", "name": name, "content": target, "proxied": true}
	var out dnsRow
	if len(found) > 0 {
		path := fmt.Sprintf("/zones/%s/dns_records/%s", url.PathEscape(zoneID), url.PathEscape(found[0].ID))
		err := c.do(ctx, http.MethodPatch, path, body, &out)
		return found[0].ID, err
	}
	err := c.do(ctx, http.MethodPost, "/zones/"+url.PathEscape(zoneID)+"/dns_records", body, &out)
	return out.ID, err
}

func (c *Client) DeleteRecord(ctx context.Context, zoneID, recordID string) error {
	path := fmt.Sprintf("/zones/%s/dns_records/%s", url.PathEscape(zoneID), url.PathEscape(recordID))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// TunnelStatus — healthy | degraded | down | inactive.
func (c *Client) TunnelStatus(ctx context.Context, accountID, tunnelID string) (string, error) {
	var out tunnelRow
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s",
		url.PathEscape(accountID), url.PathEscape(tunnelID))
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out.Status, err
}

// DeleteTunnel — спершу розірвати зʼєднання, потім видалити.
//
// Порядок вимагає сам API: тунель із живими зʼєднаннями не видаляється.
// Конектор до цього моменту вже зупинений, але Cloudflare бачить його ще
// кількадесят секунд.
func (c *Client) DeleteTunnel(ctx context.Context, accountID, tunnelID string) error {
	base := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s",
		url.PathEscape(accountID), url.PathEscape(tunnelID))
	if err := c.do(ctx, http.MethodDelete, base+"/connections", nil, nil); err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, base, nil, nil)
}
