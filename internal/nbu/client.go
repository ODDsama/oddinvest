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
	// Результати аукціонів Мінфіну: під яку дохідність розміщували кожен
	// папір кожного дня. Це ЄДИНЕ джерело в застосунку, що каже, скільки
	// платить ринок за строк, — довідник знає ставку паперу, але не знає
	// строку як виміру.
	auctionsURI = "/NBU_ovdp?json"
	userAgent   = "Mozilla/5.0 (compatible; oddinvestd/1.0)"
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
		// Довідник змішує домашні ОВДП (ISIN UA…, номінал 1000) і зовнішні
		// ОЗДП/єврооблігації (ISIN XS…), які НБУ нормалізує до номіналу 1
		// (купони — частка одиниці). Беремо лише домашні ОВДП внутрішнього
		// ринку, щоб не плутати шкалу номіналу.
		if !strings.HasPrefix(strings.ToUpper(r.CPCode), "UA") {
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

// --- аукціони Мінфіну ---

type rawAuction struct {
	AuctionDate string      `json:"AuctionDate"` // DD.MM.YYYY
	AuctionNum  string      `json:"AuctionNum"`
	ValCode     string      `json:"ValCode"`
	StockCode   string      `json:"StockCode"` // ISIN, як cpcode в довіднику
	RepayDate   string      `json:"RepayDate"`
	DaysToRepay json.Number `json:"DaysToRepay"`
	Bucket      string      `json:"Bucket"`      // "1y", "1,5y", "2y"…
	IncomeLevel json.Number `json:"IncomeLevel"` // середньозважена дохідність, %
	MinLevel    json.Number `json:"MinLevel"`
	MaxLevel    json.Number `json:"MaxLevel"`
	BTC         json.Number `json:"BTC"` // заявок до розміщеного
	VolumeSold  json.Number `json:"VolumeSold"`
}

// Auction — один рядок результатів аукціону: скільки й під яку дохідність
// розмістили конкретний папір конкретного дня.
type Auction struct {
	Date        domain.Date
	Num         string
	ISIN        string
	Currency    string
	Bucket      string // строк, як його назвав НБУ
	DaysToRepay int64
	RepayDate   domain.Date
	IncomeBP    int64 // дохідність розміщення, % × 100
	MinBP       int64 // найнижча і найвища прийняті заявки
	MaxBP       int64
	BTCx100     int64 // bid-to-cover × 100 — більше НБУ й не публікує
	SoldMinor   int64 // розміщено за номіналом, мінорні одиниці Currency
}

// Auctions — результати аукціонів ОВДП за ОДИН день. Порожня on означає
// «останній аукціонний день», і на цьому тримається вся стратегія
// опитування: один запит без параметрів каже, чи з'явилось узагалі щось
// нове, не перебираючи дати.
//
// ФОРМАТ ДАТИ ТУТ ІНШИЙ, ніж у сусідньому exchange, і це не помилка
// оформлення: цей ендпойнт хоче DD.MM.YYYY, а на YYYYMMDD відповідає
// HTTP 500 з ораклівським «literal does not match format string». Два
// ендпойнти одного API розходяться в цьому назавжди, тож звести їх до
// спільного хелпера дат не можна — саме тому формат тут заданий на місці.
//
// День без аукціону — порожній масив, і це НЕ помилка: аукціони бувають
// раз на тиждень, тож порожньо тут звичайна відповідь, а не збій.
func (c *Client) Auctions(ctx context.Context, on domain.Date) ([]Auction, error) {
	url := c.base + auctionsURI
	if on != "" {
		d, err := time.Parse("2006-01-02", string(on))
		if err != nil {
			return nil, fmt.Errorf("НБУ аукціони: дата %q: %w", on, err)
		}
		url += "&date=" + d.Format("02.01.2006")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("НБУ аукціони: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("НБУ аукціони: HTTP %d", resp.StatusCode)
	}
	var raw []rawAuction
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("НБУ аукціони: декодування: %w", err)
	}
	return parseAuctions(raw)
}

func parseAuctions(raw []rawAuction) ([]Auction, error) {
	out := make([]Auction, 0, len(raw))
	for _, r := range raw {
		if r.StockCode == "" || r.ValCode == "" {
			continue
		}
		// Лише домашні ОВДП — той самий відбір, що й у parseSecurities, і з
		// тієї ж причини: два джерела мусять описувати ОДНЕ коло паперів.
		// Інакше зовнішня ОЗДП, розміщена в доларі під зовсім іншу ставку,
		// потрапила б у криву внутрішнього ринку й зсунула б орієнтир, з
		// яким людина порівнює свій портфель.
		if !strings.HasPrefix(strings.ToUpper(r.StockCode), "UA") {
			continue
		}
		code := strings.ToUpper(r.ValCode)
		if money.GetCurrency(code) == nil {
			continue
		}
		d, err := parseNBUDate(r.AuctionDate)
		if err != nil {
			return nil, fmt.Errorf("%s: AuctionDate: %w", r.StockCode, err)
		}
		income, err := parseRateBPOrZero(r.IncomeLevel)
		if err != nil {
			return nil, fmt.Errorf("%s: IncomeLevel: %w", r.StockCode, err)
		}
		// Рядок без прийнятої дохідності пропускаємо. Аукціон, на якому
		// нічого не розмістили, буває, і для календаря подій він факт — але
		// тут нас цікавить саме РІВЕНЬ, а нуль у кривій дохідності це не
		// «нуль відсотків», це «невідомо», і зберігати їх однаково не можна.
		if income <= 0 {
			continue
		}
		minBP, err := parseRateBPOrZero(r.MinLevel)
		if err != nil {
			return nil, fmt.Errorf("%s: MinLevel: %w", r.StockCode, err)
		}
		maxBP, err := parseRateBPOrZero(r.MaxLevel)
		if err != nil {
			return nil, fmt.Errorf("%s: MaxLevel: %w", r.StockCode, err)
		}
		btc, err := parseRateBPOrZero(r.BTC) // теж ×100, і більше НБУ не дає
		if err != nil {
			return nil, fmt.Errorf("%s: BTC: %w", r.StockCode, err)
		}
		var sold int64
		if s := strings.TrimSpace(r.VolumeSold.String()); s != "" {
			if sold, err = domain.ParseDecimalToMinor(s, code); err != nil {
				return nil, fmt.Errorf("%s: VolumeSold: %w", r.StockCode, err)
			}
		}
		var days int64
		if s := strings.TrimSpace(r.DaysToRepay.String()); s != "" {
			if days, err = r.DaysToRepay.Int64(); err != nil {
				return nil, fmt.Errorf("%s: DaysToRepay: %w", r.StockCode, err)
			}
		}
		// Дата погашення тут довідкова: якщо НБУ її не дав, це не привід
		// втратити рівень дохідності, заради якого рядок і потрібен.
		repay, _ := parseNBUDate(r.RepayDate) //nolint:errcheck // необов'язкове поле: без нього рядок лишається придатним
		out = append(out, Auction{
			Date: d, Num: strings.TrimSpace(r.AuctionNum), ISIN: r.StockCode,
			Currency: code, Bucket: normalizeBucket(r.Bucket),
			DaysToRepay: days, RepayDate: repay,
			IncomeBP: income, MinBP: minBP, MaxBP: maxBP,
			BTCx100: btc, SoldMinor: sold,
		})
	}
	return out, nil
}

// normalizeBucket — «1,5y» -> «1.5y». Кома тут локальний артефакт
// джерела, а не значення: строк один і той самий, як його не запиши, а
// два написання одного строку розкололи б криву на дві.
func normalizeBucket(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
}

// parseRateBPOrZero — те саме, що parseRateBP, але порожнє поле це нуль,
// а не помилка: у відповіді аукціону частина рівнів буває відсутня.
func parseRateBPOrZero(n json.Number) (int64, error) {
	s := strings.TrimSpace(n.String())
	if s == "" {
		return 0, nil
	}
	return parseRateBP(s)
}

// rawExchange — відповідь exchange?valcode=XXX&json.
type rawExchange struct {
	Rate json.Number `json:"rate"`
	CC   string      `json:"cc"`
	// ExchangeDate — дата котирування у форматі DD.MM.YYYY. Довго не
	// парсилась, і дата в базі ставилась із локального годинника; у
	// вихідні НБУ віддає п'ятничний курс, тож ці дві дати розходились.
	ExchangeDate string `json:"exchangedate"`
}

// Rate повертає курс валюти до гривні, ×10⁴, на сьогодні.
func (c *Client) Rate(ctx context.Context, code string) (int64, error) {
	e4, _, err := c.RateOn(ctx, code, "")
	return e4, err
}

// RateOn — курс на конкретну дату; порожня on означає «сьогодні».
// Другим значенням повертає ДАТУ КОТИРУВАННЯ, як її назвав НБУ: на
// вихідний він віддає курс попереднього робочого дня, і записувати його
// під сьогоднішнім числом означало б вигадувати котирування, якого не
// було.
func (c *Client) RateOn(ctx context.Context, code string, on domain.Date) (int64, domain.Date, error) {
	url := c.base + fmt.Sprintf(exchangeURI, strings.ToUpper(code))
	if on != "" {
		// НБУ чекає YYYYMMDD без роздільників.
		url += "&date=" + strings.ReplaceAll(string(on), "-", "")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("НБУ exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("НБУ exchange: HTTP %d", resp.StatusCode)
	}
	var raw []rawExchange
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return 0, "", fmt.Errorf("НБУ exchange: декодування: %w", err)
	}
	if len(raw) == 0 {
		return 0, "", fmt.Errorf("НБУ exchange: порожня відповідь для %s", code)
	}
	e4, err := fx.ParseRateE4(raw[0].Rate.String())
	if err != nil {
		return 0, "", err
	}
	return e4, parseExchangeDate(raw[0].ExchangeDate), nil
}

// parseExchangeDate — DD.MM.YYYY -> domain.Date. Порожня на будь-якому
// несподіваному вигляді: краще лишити дату виклику, ніж записати сміття.
func parseExchangeDate(s string) domain.Date {
	t, err := time.Parse("02.01.2006", strings.TrimSpace(s))
	if err != nil {
		return ""
	}
	return domain.NewDate(t)
}
