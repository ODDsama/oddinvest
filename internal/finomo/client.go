// Package finomo — ринкова ціна ОВДП у брокерів.
//
// ЧОМУ ЦЕЙ ПАКЕТ ІСНУЄ. Довідник НБУ знає про папір усе, крім єдиного, що
// потрібно перед покупкою: скільки він коштує. Доти застосунок міряв крок
// номіналом плюс НКД і чесно про це писав (unit_cost.go), але число було
// наближенням, а не виміром: на живих даних папір коштує 1 100 ₴ там, де
// наближення давало 1 011 ₴. Помилка йшла в дохідність поради й у розмір
// квитка розкладки, тобто в обидві відповіді помічника одразу.
//
// ЦЕ ПЕРШИЙ І ПОКИ ЄДИНИЙ HTML У ПРОЄКТІ, і залежності на кшталт goquery
// заради нього не заводимо. Причина проста: ми не розбираємо розмітку, а
// дістаємо з неї ОДИН тег із відомим id, усередині якого лежить готовий
// JSON, який сайт кладе туди для власного браузерного коду. Далі працює
// штатний json.Decoder із UseNumber(), як в усьому іншому коді.
//
// ЧОМУ НЕ JSON-LD, ЯКИЙ ТАМ ТЕЖ Є. На сторінці є розмітка для пошукових
// систем із тими самими цінами, але імена продавців у ній українські й
// HTML-екрановані, а дати немає зовсім. У bond-page-config ключі джерел
// латинські й сталі, і кожна ціна приходить зі своєю датою — тобто
// «оновлено сьогодні» ми не переказуємо зі слів сайту, а бачимо самі.
package finomo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

const (
	DefaultBase = "https://finomo.com.ua"
	bondURI     = "/bond/%s"
	// Той самий User-Agent, що в клієнта НБУ: чужі сайти за Cloudflare
	// звичайний Go-шний UA іноді ріжуть, і другого правила для другого
	// джерела заводити нема за чим.
	userAgent = "Mozilla/5.0 (compatible; oddinvestd/1.0)"
	// Стеля тіла відповіді. Сторінка паперу важить ~140 КБ; чотири
	// мегабайти — із запасом і водночас захист від того, щоб чужа помилка
	// не з'їла пам'ять демона.
	maxBody = 4 << 20
)

// ErrNoBond — сайт про такий папір не знає (HTTP 404). Це НЕ помилка
// проходу: у довіднику НБУ під дві сотні паперів, а в продажу буває три
// десятки, і решта просто не має сторінки. Прохід такий папір пропускає
// й іде далі.
var ErrNoBond = errors.New("паперу немає в джерела")

// ErrCutOff — джерело відмовило в доступі (403/429).
//
// Окремою помилкою, бо реакція на неї протилежна до всіх інших: «цей папір
// не вийшов» означає «іди далі», а «нас більше не пускають» — «зупинись».
// Довбати після 429 ще півсотні разів — найшвидший спосіб перетворити
// тимчасову відмову на постійну.
var ErrCutOff = errors.New("джерело цін відмовило в доступі")

// RunResult — чим скінчився обхід джерела.
//
// ЖИВЕ ТУТ, А НЕ В jobs, і причина не в природі типу, а в межі: інтерфейс
// api.Refresher навмисно вільний від типів пакета jobs (там і сьогодні
// саме лише error), а jobs так само навмисно не знає про api — заради
// цього він приймає побудову документа callback-ом. Спільний листовий
// пакет — єдине місце, де обидва можуть бачити одну структуру, не
// заводячи ребра між собою.
//
// Шість чисел, а не одне «ок»: у них різні причини й різні дії. «Без цін»
// — це нормально (папір ніхто не продає), «не вийшло» — привід глянути в
// журнал, «понад стелю» — привід натиснути ще раз.
type RunResult struct {
	Asked   int `json:"asked"`
	Stored  int `json:"stored"`  // паперів, для яких лягла хоч одна ціна
	NoPrice int `json:"noprice"` // сторінка є, продавців немає
	Missing int `json:"missing"` // сторінки немає (404)
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"` // не влізли в стелю проходу
}

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

// Quote — ціна одного паперу в одного продавця на названу ним дату.
//
// Source — ЛАТИНСЬКИЙ ключ джерела (Privat24, Inzhur, Univer, BtcBroker,
// ICU, Kinto); Label — його ж українська назва, як її пише сайт. Ключ
// переживає переназву у вітрині, підпис — ні, тому в базі лежить ключ, а
// підпис їде лише на екран.
//
// Валюта — валюта САМОГО ПАПЕРУ, не гривня: перевірено на доларових і
// єврових випусках (UA4000236806 — 1027.73 при номіналі 1000 USD і
// дохідності 2.6 %). Гривневого перерахунку в ціні немає, попри те що
// сторінка возить поруч курс НБУ для власного калькулятора.
//
// PriceMinor — ЦІНА БРУДНА, тобто разом із накопиченим купонним доходом:
// це та сума, яку віддаєш за одну штуку. Перевірено XIRR'ом за фактичними
// датами виплат на п'яти паперах — розрахована з неї дохідність збігається
// з опублікованою до третього знака (16.600 % проти 16.60 % тощо), тоді як
// та сама ціна, прочитана як чиста, дала б 15.975 %. ДОДАВАТИ ДО НЕЇ НКД
// НЕ МОЖНА: помилка вийде тиха, однобічна і в −0,6 в.п.
type Quote struct {
	Source     string
	Label      string
	Date       domain.Date
	PriceMinor int64
	Currency   string
}

// Snapshot — усе, що взяли зі сторінки одного паперу.
//
// Maturity і NominalMinor їдуть поруч із цінами не для показу, а для
// звірки з нашим довідником: див. Quotes. Номінал перевіряє МАСШТАБ —
// єдиний спосіб перетворити 1013.65 на 101365 — помилка масштабу, і вона
// мовчки зробила б один папір схожим на сотню в рейтингу.
//
// Quotes може бути ПОРОЖНІМ, і це не помилка: погашений папір і папір, який
// зараз ніхто не продає, сторінку мають, а пропозицій не мають. «Ціни
// немає» — така сама відповідь, як ціна, і відрізняється від збою.
type Snapshot struct {
	ISIN         string
	Maturity     domain.Date
	NominalMinor int64
	Quotes       []Quote
}

// --- сирі структури сторінки ---

type rawConfig struct {
	Bond struct {
		ISIN     string `json:"isin"`
		Currency string `json:"currency"`
	} `json:"bond"`
	Calculator struct {
		MaturityDate string      `json:"maturityDate"`
		Nominal      json.Number `json:"nominal"`
	} `json:"calculator"`
	PriceHistory []rawPricePoint `json:"priceHistory"`
	// BrokerNames — перелік ПРОДАВЦІВ: латинський ключ -> українська назва.
	// Він же й фільтр, і це не оптимізація, а захист від хибної відповіді:
	// у priceHistory поруч із брокерами живе джерело "NBU" зі справедливою
	// вартістю, і вона регулярно дорівнює рівно номіналу. Без фільтра
	// «найдешевше» одного дня вказало б на НБУ за 1000 ₴ — місце, де купити
	// не можна взагалі. У brokerNames НБУ немає, тобто сторінка сама віддає
	// нам потрібний перелік.
	BrokerNames map[string]string `json:"brokerNames"`
}

type rawPricePoint struct {
	Date   string      `json:"date"`
	Price  json.Number `json:"price"`
	Source string      `json:"source"`
}

// Quotes — ціни всіх продавців для одного паперу.
//
// wantMaturity — погашення, яким цей папір знає НАШ довідник. Звірка
// обов'язкова й саме тут: сторінка чужа, її розмітка може змінитись
// будь-якого дня, і найгірший вигляд такої зміни — не помилка розбору, а
// числа, які розібрались, але стосуються не того паперу. Порожнє значення
// вимикає звірку (для тестів парсера й для паперів поза довідником).
func (c *Client) Quotes(ctx context.Context, isin string, wantMaturity domain.Date) (Snapshot, error) {
	isin = strings.ToUpper(strings.TrimSpace(isin))
	if isin == "" {
		return Snapshot{}, errors.New("finomo: порожній ISIN")
	}
	url := c.base + fmt.Sprintf(bondURI, isin)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Snapshot{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.hc.Do(req)
	if err != nil {
		return Snapshot{}, fmt.Errorf("finomo %s: %w", isin, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Snapshot{}, fmt.Errorf("finomo %s: %w", isin, ErrNoBond)
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return Snapshot{}, fmt.Errorf("finomo %s: HTTP %d: %w", isin, resp.StatusCode, ErrCutOff)
	}
	if resp.StatusCode != http.StatusOK {
		return Snapshot{}, fmt.Errorf("finomo %s: HTTP %d", isin, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Snapshot{}, fmt.Errorf("finomo %s: читання: %w", isin, err)
	}
	return parsePage(body, isin, wantMaturity)
}

// Межі тега, у якому сайт кладе дані для власного коду. Форма стала й
// однакова на різних паперах:
//
//	<script id="bond-page-config" type="application/json">
const (
	configAnchor = `id="bond-page-config"`
	scriptEnd    = "</script>"
)

func parsePage(body []byte, isin string, wantMaturity domain.Date) (Snapshot, error) {
	blob, err := extractConfig(body)
	if err != nil {
		return Snapshot{}, fmt.Errorf("finomo %s: %w", isin, err)
	}
	var raw rawConfig
	dec := json.NewDecoder(strings.NewReader(blob))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return Snapshot{}, fmt.Errorf("finomo %s: декодування: %w", isin, err)
	}
	got := strings.ToUpper(strings.TrimSpace(raw.Bond.ISIN))
	if got != isin {
		return Snapshot{}, fmt.Errorf("finomo %s: сторінка про %q", isin, got)
	}
	code := strings.ToUpper(strings.TrimSpace(raw.Bond.Currency))
	if money.GetCurrency(code) == nil {
		return Snapshot{}, fmt.Errorf("finomo %s: невідома валюта %q", isin, code)
	}
	mat, err := parseISODate(raw.Calculator.MaturityDate)
	if err != nil {
		return Snapshot{}, fmt.Errorf("finomo %s: погашення: %w", isin, err)
	}
	// Сторож формату, довід — у шапці Quotes.
	if wantMaturity != "" && mat != wantMaturity {
		return Snapshot{}, fmt.Errorf(
			"finomo %s: погашення %s не збігається з довідником (%s) — схоже, розмітка джерела змінилась",
			isin, mat, wantMaturity)
	}
	nomMinor, err := domain.ParseDecimalToMinor(raw.Calculator.Nominal.String(), code)
	if err != nil {
		return Snapshot{}, fmt.Errorf("finomo %s: номінал: %w", isin, err)
	}
	out := Snapshot{ISIN: isin, Maturity: mat, NominalMinor: nomMinor}
	// Остання точка по кожному джерелу. Ряд приходить упорядкованим за
	// датою, але покладатись на це не можна: порядок — властивість їхньої
	// вибірки, а не обіцянка, і мовчазна помилка тут дала б учорашню ціну
	// з виглядом сьогоднішньої.
	last := map[string]Quote{}
	for _, p := range raw.PriceHistory {
		src := strings.TrimSpace(p.Source)
		if src == "" {
			continue
		}
		// Лише продавці, довід — при rawConfig.BrokerNames.
		label, sells := raw.BrokerNames[src]
		if !sells {
			continue
		}
		d, derr := parseISODate(p.Date)
		if derr != nil {
			continue // одна крива точка ряду не варта відмови від усіх цін
		}
		minor, merr := domain.ParseDecimalToMinor(p.Price.String(), code)
		if merr != nil || minor <= 0 {
			continue
		}
		if prev, ok := last[src]; ok && prev.Date >= d {
			continue
		}
		last[src] = Quote{
			Source: src, Label: strings.TrimSpace(label),
			Date: d, PriceMinor: minor, Currency: code,
		}
	}
	for _, q := range last {
		out.Quotes = append(out.Quotes, q)
	}
	// Найдешевша першою, назва другим критерієм. Порядок значущий: на ньому
	// тримається «у кого дешевше», а дві однакові ціни інакше ставали б у
	// порядку обходу мапи, тобто по-різному від запуску до запуску.
	sort.Slice(out.Quotes, func(i, j int) bool {
		if out.Quotes[i].PriceMinor != out.Quotes[j].PriceMinor {
			return out.Quotes[i].PriceMinor < out.Quotes[j].PriceMinor
		}
		return out.Quotes[i].Source < out.Quotes[j].Source
	})
	return out, nil
}

// extractConfig — вміст тега з відомим id, зрізом по двох межах.
func extractConfig(body []byte) (string, error) {
	s := string(body)
	i := strings.Index(s, configAnchor)
	if i < 0 {
		return "", fmt.Errorf("тега %s на сторінці немає", configAnchor)
	}
	open := strings.IndexByte(s[i:], '>')
	if open < 0 {
		return "", fmt.Errorf("тег %s не закритий", configAnchor)
	}
	start := i + open + 1
	end := strings.Index(s[start:], scriptEnd)
	if end < 0 {
		return "", fmt.Errorf("тег %s без %s", configAnchor, scriptEnd)
	}
	return strings.TrimSpace(s[start : start+end]), nil
}

func parseISODate(s string) (domain.Date, error) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, 'T'); i > 0 {
		s = s[:i]
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return "", fmt.Errorf("нерозпізнана дата %q", s)
	}
	return domain.NewDate(t), nil
}
