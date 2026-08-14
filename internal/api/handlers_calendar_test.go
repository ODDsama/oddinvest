package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/state"
)

// TestCalendarAgreesWithSummary — вкладка «Календар» і зведення дають той
// самий розклад.
//
// Це питання одне: що і коли впаде на рахунок. Відповідей же було дві —
// /api/calendar збирав потоки власним кодом (облігації плюс вклади), а
// зведення своїм (плюс оцінені дивіденди фондів). На живих даних REIT
// платив 10 числа щомісяця й давав чверть усього доходу, а у вкладці за
// рік стояло 26 рядків і жодного фондового.
//
// Порівнюємо МНОЖИНАМИ рядків, а не кількістю: збіг кількості міг би
// вийти випадково, збіг дат, паперів і сум — ні.
func TestCalendarAgreesWithSummary(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)
	seed(t, st)
	today := domain.NewDate(time.Now())

	// Облігація — щоб у розкладі був не лише фонд, інакше тест зеленів би
	// і на порожньому календарі.
	if resp, b := do(t, "POST", srv.URL+"/api/lots",
		fmt.Sprintf(`{"isin":"UA4000227748","qty":10,"price_per_bond":"1000.00","buy_date":%q,"channel":"inzhur"}`,
			string(today.AddDays(-30)))); resp.StatusCode != 201 {
		t.Fatalf("лот: %d %s", resp.StatusCode, b)
	}
	// Фонд із днем виплати: саме він і губився.
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: today.AddDays(-60), Fund: "Inzhur REIT", Kind: domain.FundBuy,
		Qty: 500, Amount: 500_000, Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	funds, err := st.ListFunds(ctx)
	if err != nil || len(funds) != 1 {
		t.Fatalf("очікували один фонд: %v %d", err, len(funds))
	}
	f := funds[0]
	f.ExpectedYieldBP, f.ExpectedYieldCur, f.PayoutDay = 950, money.UAH, 10
	if err := st.RenameFund(ctx, f.ID, f); err != nil {
		t.Fatal(err)
	}

	// Зведення.
	resp, body := do(t, "GET", srv.URL+"/api/summary", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("summary дав %d: %s", resp.StatusCode, body)
	}
	var doc state.Doc
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("розбір зведення: %v", err)
	}
	want := map[string]bool{}
	funded := 0
	for _, p := range doc.Calendar {
		want[fmt.Sprintf("%s|%s|%.2f", p.Date, p.ISIN, p.Amount)] = true
		if domain.IsFundISIN(p.ISIN) {
			funded++
		}
	}
	if funded == 0 {
		t.Fatal("зведення не має жодної виплати фонду — фікстура не перевіряє те, заради чого написана")
	}

	// Вкладка «Календар», з тієї самої дати.
	resp, body = do(t, "GET", srv.URL+"/api/calendar?from="+string(today), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("calendar дав %d: %s", resp.StatusCode, body)
	}
	var rows []struct {
		Date   string `json:"date"`
		ISIN   string `json:"isin"`
		Amount struct {
			Amount string `json:"amount"`
		} `json:"amount"`
	}
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("розбір календаря: %v (%s)", err, body)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[fmt.Sprintf("%s|%s|%s", r.Date, r.ISIN, r.Amount.Amount)] = true
	}

	var missing []string
	for k := range want {
		if !got[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("у вкладці «Календар» немає %d виплат, які показує зведення: %v",
			len(missing), missing)
	}
}

// TestCalendarShowsThePastWhenAsked — from справді фільтрує, і минулі
// виплати з архіву не зникають.
//
// Друга половина правила про дві дати. Сусідній тест стереже, щоб оцінки
// не поповзли в минуле; цей — щоб разом із ними туди не перестали
// потрапляти ВІДОМІ виплати. Без нього «рахувати все від сьогодні»
// виглядало б цілком робочим: вкладка просто тихо втратила б історію.
func TestCalendarShowsThePastWhenAsked(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)
	today := domain.NewDate(time.Now())
	past, future := today.AddDays(-30), today.AddDays(60)
	secs := []nbu.Security{{
		Bond: domain.Bond{ISIN: "UA4000999999", Nominal: money.New(100000, money.UAH),
			RateBP: 1600, Maturity: future, Descr: "з уже сплаченим купоном"},
		Payments: []domain.Payment{
			{ISIN: "UA4000999999", PayDate: past, Type: domain.PayCoupon, PerBond: money.New(8000, money.UAH)},
			{ISIN: "UA4000999999", PayDate: future, Type: domain.PayRedemption, PerBond: money.New(100000, money.UAH)},
		},
	}}
	if err := st.ReplaceDirectory(ctx, secs, time.Now()); err != nil {
		t.Fatal(err)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/lots",
		fmt.Sprintf(`{"isin":"UA4000999999","qty":10,"price_per_bond":"1000.00","buy_date":%q,"channel":"inzhur"}`,
			string(today.AddDays(-60)))); resp.StatusCode != 201 {
		t.Fatalf("лот: %d %s", resp.StatusCode, b)
	}
	// Вклад із щомісячною виплатою, відкритий три місяці тому: його
	// відсотки теж мають архів, і from стосується їх так само. Без цього
	// рядка «вклади завжди від сьогодні» проходило б непоміченим.
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: money.UAH, Principal: 10_000_00, RateBP: 1600,
		OpenDate: today.AddDays(-90), MaturityDate: today.AddDays(275),
		Payout: domain.PayoutMonthly, TaxBP: 1950,
	}); err != nil {
		t.Fatal(err)
	}

	// pastRows — скільки рядків календаря датовані раніше за сьогодні.
	pastRows := func(from string) (bond, deposit int) {
		t.Helper()
		resp, body := do(t, "GET", srv.URL+"/api/calendar?from="+from, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("calendar дав %d: %s", resp.StatusCode, body)
		}
		var rows []struct {
			Date string `json:"date"`
			ISIN string `json:"isin"`
		}
		if err := json.Unmarshal([]byte(body), &rows); err != nil {
			t.Fatalf("розбір: %v", err)
		}
		for _, r := range rows {
			if r.Date >= string(today) {
				continue
			}
			if strings.HasPrefix(r.ISIN, "UA") {
				bond++
			} else {
				deposit++
			}
		}
		return
	}
	b, d := pastRows("1970-01-01")
	if b == 0 {
		t.Errorf("купон %s зник із архіву — from не доходить до купонів", past)
	}
	if d == 0 {
		t.Error("минулі відсотки вкладу зникли з архіву — from не доходить до вкладів")
	}
	if b, d := pastRows(string(today)); b != 0 || d != 0 {
		t.Errorf("запит від сьогодні дав %d минулих купонів і %d відсотків — from не фільтрує", b, d)
	}
}

// TestCalendarRespectsTo — верхня межа періоду є і працює, а її
// відсутність нічого не змінює.
//
// Друге твердження тут важливіше за перше: /api/tax і /api/cashflow давно
// беруть пару from+to, календар лишався єдиним періодним маршрутом лише з
// from — і саме тому вкладка просила «весь архів» і малювала його цілком.
// Параметр адитивний: запит без `to` мусить дати рівно те саме, що й
// раніше, інакше це вже не нова межа, а зміна поведінки.
func TestCalendarRespectsTo(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)
	today := domain.NewDate(time.Now())
	past, future := today.AddDays(-30), today.AddDays(60)
	secs := []nbu.Security{{
		Bond: domain.Bond{ISIN: "UA4000999999", Nominal: money.New(100000, money.UAH),
			RateBP: 1600, Maturity: future, Descr: "купон позаду, погашення попереду"},
		Payments: []domain.Payment{
			{ISIN: "UA4000999999", PayDate: past, Type: domain.PayCoupon, PerBond: money.New(8000, money.UAH)},
			{ISIN: "UA4000999999", PayDate: future, Type: domain.PayRedemption, PerBond: money.New(100000, money.UAH)},
		},
	}}
	if err := st.ReplaceDirectory(ctx, secs, time.Now()); err != nil {
		t.Fatal(err)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/lots",
		fmt.Sprintf(`{"isin":"UA4000999999","qty":10,"price_per_bond":"1000.00","buy_date":%q,"channel":"inzhur"}`,
			string(today.AddDays(-60)))); resp.StatusCode != 201 {
		t.Fatalf("лот: %d %s", resp.StatusCode, b)
	}

	dates := func(query string) []string {
		t.Helper()
		resp, body := do(t, "GET", srv.URL+"/api/calendar?"+query, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("calendar?%s дав %d: %s", query, resp.StatusCode, body)
		}
		var rows []struct {
			Date string `json:"date"`
		}
		if err := json.Unmarshal([]byte(body), &rows); err != nil {
			t.Fatalf("розбір: %v", err)
		}
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Date)
		}
		return out
	}
	has := func(ds []string, want domain.Date) bool {
		return slices.Contains(ds, string(want))
	}

	all := dates("from=1970-01-01")
	if !has(all, past) || !has(all, future) {
		t.Fatalf("без межі мали бути обидві виплати, маємо %v", all)
	}
	// Межа сьогоднішнім днем відрізає майбутнє погашення й лишає минулий купон.
	bounded := dates("from=1970-01-01&to=" + string(today))
	if !has(bounded, past) {
		t.Errorf("to=сьогодні зрізало й минулий купон %s: %v", past, bounded)
	}
	if has(bounded, future) {
		t.Errorf("to=сьогодні не відрізало погашення %s: %v", future, bounded)
	}
	// Межа за обрієм не зрізає нічого — це доводить, що фільтр саме
	// по даті, а не «перші N рядків».
	if far := dates("from=1970-01-01&to=2099-12-31"); len(far) != len(all) {
		t.Errorf("межа за обрієм змінила перелік: %d проти %d", len(far), len(all))
	}
	// Порожнє to читається як «без межі», а не як «до нуля»: інакше
	// старіший клієнт, який передає порожній параметр, дістав би порожньо.
	if empty := dates("from=1970-01-01&to="); len(empty) != len(all) {
		t.Errorf("порожнє to зрізало перелік: %d проти %d", len(empty), len(all))
	}
}

// TestCalendarKeepsThePast — гортання архіву не зламалось.
//
// Вкладка тягне календар від 1970-го, щоб показати й минулі виплати.
// Оцінені дивіденди при цьому рахуються від СЬОГОДНІ, а не від дати
// запиту: оцінки в минулому не буває, там є фактичні операції фонду.
// Порахувати їх від 1970-го означало б намалювати дванадцять місяців
// дивідендів у сімдесятих.
func TestCalendarKeepsThePast(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)
	seed(t, st)
	today := domain.NewDate(time.Now())
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: today.AddDays(-60), Fund: "Inzhur REIT", Kind: domain.FundBuy,
		Qty: 500, Amount: 500_000, Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	funds, _ := st.ListFunds(ctx) //nolint:errcheck // фонд щойно заведено операцією
	f := funds[0]
	f.ExpectedYieldBP, f.ExpectedYieldCur, f.PayoutDay = 950, money.UAH, 10
	if err := st.RenameFund(ctx, f.ID, f); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFundOp(ctx, domain.FundOp{ // щоб довідник точно застосувався
		Date: today, Fund: "Inzhur REIT", Kind: domain.FundDividend,
		Amount: 4_000, Tax: 560, Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}

	resp, body := do(t, "GET", srv.URL+"/api/calendar?from=1970-01-01", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("calendar дав %d: %s", resp.StatusCode, body)
	}
	var rows []struct {
		Date string `json:"date"`
		ISIN string `json:"isin"`
	}
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("розбір: %v", err)
	}
	fundRows := 0
	for _, r := range rows {
		if !domain.IsFundISIN(r.ISIN) {
			continue
		}
		fundRows++
		if r.Date < string(today) {
			t.Errorf("оцінена виплата фонду в МИНУЛОМУ (%s) — оцінки від дати "+
				"запиту, а мали бути від сьогодні", r.Date)
		}
	}
	if fundRows == 0 {
		t.Error("фонд зник із календаря на запиті з архівом")
	}
}
