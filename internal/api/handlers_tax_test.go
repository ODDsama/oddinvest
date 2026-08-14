package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/store"
)

type taxJSON struct {
	Year     int     `json:"year"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	GrossUAH float64 `json:"gross_uah"`
	TaxUAH   float64 `json:"tax_uah"`
	ByKind   []struct {
		Kind     string  `json:"kind"`
		GrossUAH float64 `json:"gross_uah"`
		TaxUAH   float64 `json:"tax_uah"`
	} `json:"by_kind"`
}

func getTax(t *testing.T, base, query string) taxJSON {
	t.Helper()
	resp, body := do(t, "GET", base+"/api/tax?"+query, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/tax?%s дав %d: %s", query, resp.StatusCode, body)
	}
	var out taxJSON
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("розбір: %v (%s)", err, body)
	}
	return out
}

// TestTaxDefaultsToCalendarYear — період за замовчуванням це РІК, а не
// ковзні дванадцять місяців.
//
// Доти /api/tax типово брав «сьогодні мінус рік», а /api/export/csv поруч
// — календарний рік. Декларація річна, тож ковзне вікно не відповідало на
// жодне питання, яке справді ставлять, і головне — два маршрути про одні
// й ті самі гроші міряли різні відрізки.
func TestTaxDefaultsToCalendarYear(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	got := getTax(t, srv.URL, "")
	year := time.Now().Year()
	if got.Year != year {
		t.Errorf("рік = %d, очікували %d", got.Year, year)
	}
	// Межі рахуються від поточного року, а не зашиті числом: інакше тест
	// зеленів би до 31 грудня й падав першого січня.
	wantFrom := fmt.Sprintf("%d-01-01", year)
	wantTo := fmt.Sprintf("%d-12-31", year)
	if got.From != wantFrom || got.To != wantTo {
		t.Errorf("період %s → %s, очікували %s → %s", got.From, got.To, wantFrom, wantTo)
	}
}

// TestTaxYearParamWins — ?year= головніший за пару from/to, а сміття в
// ньому дає помилку, а не мовчазний нуль.
func TestTaxYearParamWins(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)

	got := getTax(t, srv.URL, "year=2024&from=2020-01-01&to=2020-12-31")
	if got.Year != 2024 || got.From != "2024-01-01" || got.To != "2024-12-31" {
		t.Errorf("year мав перемогти пару: %+v", got)
	}

	// Пара без року: період її, а year порожній — підписувати довільний
	// відрізок роком не можна.
	got = getTax(t, srv.URL, "from=2024-03-01&to=2024-05-31")
	if got.Year != 0 || got.From != "2024-03-01" || got.To != "2024-05-31" {
		t.Errorf("пара from/to: %+v", got)
	}

	for _, bad := range []string{"year=позаминулий", "year=20226", "year=0"} {
		if resp, body := do(t, "GET", srv.URL+"/api/tax?"+bad, ""); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s дав %d, очікували 400: %s", bad, resp.StatusCode, body)
		}
	}
	// Перевернутий період — теж помилка, а не порожній результат.
	if resp, _ := do(t, "GET", srv.URL+"/api/tax?from=2024-12-31&to=2024-01-01", ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("перевернутий період дав %d, очікували 400", resp.StatusCode)
	}
}

// TestTaxYearHelperIsShared — обидва маршрути беруть період з одного
// місця. Перевіряється сам помічник: інакше довелось би порівнювати
// заголовок CSV із JSON, а це тест про формат, а не про період.
func TestTaxYearHelperIsShared(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	y, from, to, err := taxYear(url.Values{}, now)
	if err != nil || y != 2026 || from != "2026-01-01" || to != "2026-12-31" {
		t.Fatalf("типовий період: %d %s→%s (%v)", y, from, to, err)
	}
	// Порожнє `to` при заданому `from` означає «по сьогодні», а не «по
	// кінець часів»: майбутніх податків не буває.
	_, _, to2, err := taxYear(url.Values{"from": {"2026-01-01"}}, now)
	if err != nil || to2 != "2026-08-14" {
		t.Errorf("from без to: %s (%v)", to2, err)
	}
}

// TestTaxAndCSVAgreeOnPeriod — картка й вивантаження бачать ОДНІ Й ТІ САМІ
// події.
//
// Це головний тест треку: доти обидва маршрути читали той самий портфель
// через різні відрізки часу, тож зійтись могли лише випадково. Купон
// поза роком не має потрапити ні туди, ні туди.
func TestTaxAndCSVAgreeOnPeriod(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)
	const isin = "UA4000888888"
	// Два купони: один торік, другий цьогоріч. Погашення далеко попереду,
	// щоб папір лишався в портфелі.
	year := time.Now().Year()
	thisYear := domain.Date(time.Date(year, 3, 17, 0, 0, 0, 0, time.UTC).Format("2006-01-02"))
	lastYear := domain.Date(time.Date(year-1, 3, 17, 0, 0, 0, 0, time.UTC).Format("2006-01-02"))
	maturity := domain.Date(time.Date(year+2, 3, 17, 0, 0, 0, 0, time.UTC).Format("2006-01-02"))
	if err := st.ReplaceDirectory(ctx, []nbu.Security{{
		Bond: domain.Bond{ISIN: isin, Nominal: money.New(100000, money.UAH),
			RateBP: 1600, Maturity: maturity},
		Payments: []domain.Payment{
			{ISIN: isin, PayDate: lastYear, Type: domain.PayCoupon, PerBond: money.New(8000, money.UAH)},
			{ISIN: isin, PayDate: thisYear, Type: domain.PayCoupon, PerBond: money.New(8000, money.UAH)},
			{ISIN: isin, PayDate: maturity, Type: domain.PayRedemption, PerBond: money.New(100000, money.UAH)},
		},
	}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	buy := domain.Date(time.Date(year-2, 1, 10, 0, 0, 0, 0, time.UTC).Format("2006-01-02"))
	if _, err := st.AddLot(ctx, domain.Lot{
		ISIN: isin, Qty: 10, PricePerBond: money.New(100000, money.UAH),
		Fee: money.New(0, money.UAH), BuyDate: buy, Channel: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}

	// Купон цього року — у вікні обох маршрутів.
	got := getTax(t, srv.URL, "")
	if got.GrossUAH == 0 {
		t.Fatalf("податкова картка порожня, хоч купон цього року є: %+v", got)
	}
	resp, csvBody := do(t, "GET", srv.URL+"/api/export/csv", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("csv дав %d", resp.StatusCode)
	}
	if !strings.Contains(csvBody, string(thisYear)) {
		t.Errorf("купон %s не потрапив у CSV:\n%s", thisYear, csvBody)
	}
	if strings.Contains(csvBody, string(lastYear)) {
		t.Errorf("купон %s з ТОРІШНЬОГО року потрапив у CSV за цей рік:\n%s", lastYear, csvBody)
	}

	// Той самий рік явно — обидва маршрути мусять погодитись і на минулому.
	prev := getTax(t, srv.URL, "year="+string(lastYear)[:4])
	if prev.GrossUAH == 0 {
		t.Errorf("за минулий рік картка порожня, хоч купон там є: %+v", prev)
	}
	_, prevCSV := do(t, "GET", srv.URL+"/api/export/csv?year="+string(lastYear)[:4], "")
	if !strings.Contains(prevCSV, string(lastYear)) {
		t.Errorf("CSV за минулий рік не містить купона %s:\n%s", lastYear, prevCSV)
	}
	if strings.Contains(prevCSV, string(thisYear)) {
		t.Errorf("CSV за минулий рік містить цьогорічний купон %s", thisYear)
	}
}

// TestTaxUsesRateOnEventDate — валютний дохід переводиться курсом ТОГО
// ДНЯ, а не сьогоднішнім.
//
// Доти /api/tax брав один поточний курс на всі події. На портфелі, де
// долар купували по 27, а дивляться на нього по 44, це не похибка
// округлення: податок за минулий рік виходив у півтора раза більшим за
// реально сплачений. Історія курсів у базі лежала весь цей час.
func TestTaxUsesRateOnEventDate(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)
	year := time.Now().Year()
	div := domain.Date(fmt.Sprintf("%d-03-10", year))

	// Курс НА ДАТУ ПОДІЇ вдвічі нижчий за сьогоднішній: якщо обробник
	// візьме сьогоднішній, сума буде вдвічі більшою, і сплутати це з
	// округленням неможливо.
	if err := st.SaveRate(ctx, money.USD, 20_0000, div); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRate(ctx, money.USD, 40_0000, domain.NewDate(time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: div, Fund: "Inzhur REIT", Kind: domain.FundDividend,
		Amount: 100_00, Tax: 14_00, Currency: money.USD, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}

	got := getTax(t, srv.URL, "")
	// $100 × 20 = 2000 ₴, а не $100 × 40 = 4000 ₴.
	if got.GrossUAH != 2000 {
		t.Errorf("нараховано %v ₴, очікували 2000 (курс 20 на дату події, а не 40 сьогодні)", got.GrossUAH)
	}
	if got.TaxUAH != 280 {
		t.Errorf("податок %v ₴, очікували 280", got.TaxUAH)
	}
}

// TestTaxReportsFXBasisAndGaps — застосунок КАЖЕ, звідки взявся курс і
// чого йому забракло.
//
// Помісячний бекфіл означає, що подія може відставати від найближчої
// точки на тижні. Мовчати про це означало б видавати оцінку за факт, тож
// картка отримує і правило, і найгірше відставання, і лічильник
// пропущеного — у тій самій формі, що вже вживається в /api/benchmark.
func TestTaxReportsFXBasisAndGaps(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)
	year := time.Now().Year()

	// Курс є, але на місяць раніше за подію — саме той випадок, який дає
	// помісячна історія.
	if err := st.SaveRate(ctx, money.USD, 40_0000, domain.Date(fmt.Sprintf("%d-02-01", year))); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: domain.Date(fmt.Sprintf("%d-03-03", year)), Fund: "REIT",
		Kind: domain.FundDividend, Amount: 100_00, Tax: 0,
		Currency: money.USD, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	// А цей — узагалі до початку історії курсів: врахувати нема чим.
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: domain.Date(fmt.Sprintf("%d-01-05", year)), Fund: "REIT",
		Kind: domain.FundDividend, Amount: 500_00, Tax: 0,
		Currency: money.EUR, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}

	resp, body := do(t, "GET", srv.URL+"/api/tax", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	var out struct {
		GrossUAH float64 `json:"gross_uah"`
		FXBasis  string  `json:"fx_basis"`
		MaxLag   int     `json:"fx_max_lag_days"`
		Note     string  `json:"note"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.FXBasis == "" {
		t.Error("картка мовчить про те, за яким курсом рахувала")
	}
	// 30 днів між 01-02 і 03-03 (лютий 28 днів + 2).
	if out.MaxLag < 25 || out.MaxLag > 35 {
		t.Errorf("найгірше відставання %d днів — очікували близько 30", out.MaxLag)
	}
	if !strings.Contains(out.Note, "без курсу") {
		t.Errorf("про подію без курсу не сказано: %q", out.Note)
	}
	// Дохід із курсом усе одно порахований: одна подія без курсу не
	// привід не показати звіт.
	if out.GrossUAH != 4000 {
		t.Errorf("нараховано %v, очікували 4000 (лише подія з курсом)", out.GrossUAH)
	}
}

// TestCSVTaxMatchesTaxEndpoint — сума податку у файлі дорівнює числу на
// картці.
//
// Це головний тест треку. Картка й вивантаження читають ті самі події,
// тим самим вікном і тим самим курсом — і саме тому їхнє розходження має
// падати тестом, а не виявлятись у податковій. Доти CSV податку не
// містив узагалі: у нього не потрапляли ні дивіденди фондів, ні відсотки
// вкладів, тобто ЄДИНЕ, з чого податок реально утримують.
func TestCSVTaxMatchesTaxEndpoint(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)
	year := time.Now().Year()
	seed(t, st)

	// Курси на дві різні дати: якщо котрийсь із маршрутів візьме
	// сьогоднішній замість історичного, суми розійдуться.
	if err := st.SaveRate(ctx, money.USD, 30_0000, domain.Date(fmt.Sprintf("%d-02-01", year))); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRate(ctx, money.USD, 45_0000, domain.NewDate(time.Now())); err != nil {
		t.Fatal(err)
	}
	// Валютний дивіденд із утриманим податком.
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: domain.Date(fmt.Sprintf("%d-02-14", year)), Fund: "Inzhur REIT",
		Kind: domain.FundDividend, Amount: 200_00, Tax: 28_00,
		Currency: money.USD, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	// Гривневий дивіденд — щоб у сумі були обидві валюти.
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: domain.Date(fmt.Sprintf("%d-05-20", year)), Fund: "Inzhur Земля",
		Kind: domain.FundDividend, Amount: 5_000_00, Tax: 700_00,
		Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	// Вклад: відсотки оподатковуються, і вони теж мусять бути в обох.
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: money.UAH, Principal: 100_000_00, RateBP: 1600,
		OpenDate:     domain.Date(fmt.Sprintf("%d-01-10", year)),
		MaturityDate: domain.Date(fmt.Sprintf("%d-01-10", year+2)),
		Payout:       domain.PayoutMonthly, TaxBP: 1950,
	}); err != nil {
		t.Fatal(err)
	}

	card := getTax(t, srv.URL, "")
	resp, csvBody := do(t, "GET", srv.URL+"/api/export/csv", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("csv дав %d", resp.StatusCode)
	}

	// Сума колонки «податок_грн».
	var total float64
	// Знімаємо BOM, який обробник пише заради українського Excel. Саме
	// екранованою послідовністю: сам символ у тілі go-файлу компілятор
	// відкидає як illegal byte order mark.
	lines := strings.Split(strings.TrimPrefix(csvBody, "\ufeff"), "\n")
	head := strings.Split(strings.TrimSpace(lines[0]), ";")
	col := -1
	for i, h := range head {
		if h == "податок_грн" {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("у шапці CSV немає колонки податку: %q", lines[0])
	}
	for _, ln := range lines[1:] {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		f := strings.Split(ln, ";")
		if len(f) <= col {
			continue
		}
		var v float64
		if _, err := fmt.Sscanf(f[col], "%f", &v); err == nil {
			total += v
		}
	}

	if card.TaxUAH == 0 {
		t.Fatalf("картка не показала податку — тест нічого не перевіряє: %+v", card)
	}
	// Копійка допуску: обидва боки округлюють до сотих незалежно.
	if diff := card.TaxUAH - total; diff > 0.01 || diff < -0.01 {
		t.Errorf("податок розійшовся: картка %.2f, CSV %.2f (різниця %.2f)\n%s",
			card.TaxUAH, total, diff, csvBody)
	}
}

// Купони ОВДП звільнені від податку — це закон, а не налаштування, тож
// нуль у рядку «Купони ОВДП» мусить лишатись нулем.
func TestTaxBondCouponsAreExempt(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)
	seed(t, st)
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: domain.NewDate(time.Now()), Amount: 100_000_00,
		Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	got := getTax(t, srv.URL, "")
	for _, l := range got.ByKind {
		if l.Kind == "bond" && l.TaxUAH != 0 {
			t.Errorf("з купонів ОВДП утримано %v — вони звільнені", l.TaxUAH)
		}
	}
}
