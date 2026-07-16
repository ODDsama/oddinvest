// Package nbu — клієнт відкритого API НБУ: довідник ЦП з графіками
// виплат і курси валют. Усі числа парсяться через json.Number ->
// big.Rat -> мінорні одиниці; float64 не з'являється ніде в ланцюжку.
package nbu

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
)

const (
	DefaultBase = "https://bank.gov.ua"
	// Реєстр ОВДП із графіком купонів/погашень (cpcode/nominal/auk_proc/
	// pgs_date/val_code/payments[]). Саме його чекає parseSecurities.
	securitiesURI = "/depo_securities?json"
	exchangeURI   = "/NBUStatService/v1/statdirectory/exchange?valcode=%s&json"
	userAgent     = "Mozilla/5.0 (compatible; oddinvestd/1.0)"
)

type Client struct {
	base string
	hc   *http.Client
}

func New(base string) *Client {
	if base == "" {
		base = DefaultBase
	}
	return &Client{base: base, hc: &http.Client{Timeout: 30 * time.Second}}
}

// --- сирі структури відповіді ---

type rawSecurity struct {
	CPCode   string       `json:"cpcode"`   // ISIN
	Nominal  json.Number  `json:"nominal"`  // номінал
	AukProc  json.Number  `json:"auk_proc"` // ставка, %
	PgsDate  string       `json:"pgs_date"` // погашення
	ValCode  string       `json:"val_code"` // "UAH"/"USD"/...
	CPDescr  string       `json:"cpdescr"`
	Payments []rawPayment `json:"payments"`
}

type rawPayment struct {
	PayDate string      `json:"pay_date"`
	PayType json.Number `json:"pay_type"` // 1 купон / 2 погашення / 3 дострокове
	PayVal  json.Number `json:"pay_val"`  // сума на один папір
}

// Security — розпарсений папір з графіком виплат.
type Security struct {
	Bond     domain.Bond
	Payments []domain.Payment
}

// Securities тягне повний довідник паперів в обігу.
func (c *Client) Securities(ctx context.Context) ([]Security, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+securitiesURI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("НБУ securities: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("НБУ securities: HTTP %d", resp.StatusCode)
	}
	var raw []rawSecurity
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("НБУ securities: декодування: %w", err)
	}
	return parseSecurities(raw)
}

func parseSecurities(raw []rawSecurity) ([]Security, error) {
	out := make([]Security, 0, len(raw))
	for _, r := range raw {
		if r.CPCode == "" || r.ValCode == "" {
			continue
		}
		code := strings.ToUpper(r.ValCode)
		if money.GetCurrency(code) == nil {
			continue
		}
		nomMinor, err := domain.ParseDecimalToMinor(r.Nominal.String(), code)
		if err != nil {
			return nil, fmt.Errorf("%s: номінал: %w", r.CPCode, err)
		}
		mat, err := parseNBUDate(r.PgsDate)
		if err != nil {
			return nil, fmt.Errorf("%s: pgs_date: %w", r.CPCode, err)
		}
		rateBP, err := parseRateBP(r.AukProc.String())
		if err != nil {
			return nil, fmt.Errorf("%s: auk_proc: %w", r.CPCode, err)
		}
		sec := Security{Bond: domain.Bond{
			ISIN:     r.CPCode,
			Nominal:  money.New(nomMinor, code),
			RateBP:   rateBP,
			Maturity: mat,
			Descr:    r.CPDescr,
		}}
		for _, p := range r.Payments {
			d, err := parseNBUDate(p.PayDate)
			if err != nil {
				return nil, fmt.Errorf("%s: pay_date: %w", r.CPCode, err)
			}
			t, err := p.PayType.Int64()
			if err != nil {
				return nil, fmt.Errorf("%s: pay_type: %w", r.CPCode, err)
			}
			valMinor, err := domain.ParseDecimalToMinor(p.PayVal.String(), code)
			if err != nil {
				return nil, fmt.Errorf("%s: pay_val: %w", r.CPCode, err)
			}
			sec.Payments = append(sec.Payments, domain.Payment{
				ISIN:    r.CPCode,
				PayDate: d,
				Type:    domain.PayType(t),
				PerBond: money.New(valMinor, code),
			})
		}
		out = append(out, sec)
	}
	return out, nil
}

// parseNBUDate — НБУ в різних ендпоінтах віддає дати по-різному;
// приймаємо основні варіанти і нормалізуємо в ISO.
func parseNBUDate(s string) (domain.Date, error) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, 'T'); i > 0 {
		s = s[:i]
	}
	for _, layout := range []string{"2006-01-02", "02.01.2006", "20060102"} {
		if t, err := time.Parse(layout, s); err == nil {
			return domain.NewDate(t), nil
		}
	}
	return "", fmt.Errorf("нерозпізнаний формат дати %q", s)
}

// parseRateBP: "16.55" -> 1655 базисних пунктів (відсоток × 100).
func parseRateBP(s string) (int64, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, fmt.Errorf("невалідна ставка %q", s)
	}
	r.Mul(r, new(big.Rat).SetInt64(100))
	return domain.RatToInt64HalfEven(r)
}

// rawExchange — відповідь exchange?valcode=XXX&json.
type rawExchange struct {
	Rate json.Number `json:"rate"`
	CC   string      `json:"cc"`
}

// Rate повертає курс валюти до гривні, ×10⁴.
func (c *Client) Rate(ctx context.Context, code string) (int64, error) {
	url := c.base + fmt.Sprintf(exchangeURI, strings.ToUpper(code))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("НБУ exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("НБУ exchange: HTTP %d", resp.StatusCode)
	}
	var raw []rawExchange
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return 0, fmt.Errorf("НБУ exchange: декодування: %w", err)
	}
	if len(raw) == 0 {
		return 0, fmt.Errorf("НБУ exchange: порожня відповідь для %s", code)
	}
	return fx.ParseRateE4(raw[0].Rate.String())
}
