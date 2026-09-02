package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// periodOf — GET /api/period і розбір відповіді.
func periodOf(t *testing.T, srv, month string) periodResp {
	t.Helper()
	url := srv + "/api/period"
	if month != "" {
		url += "?month=" + month
	}
	resp, body := do(t, "GET", url, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: %d %s", url, resp.StatusCode, body)
	}
	var got periodResp
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return got
}

// ГОЛОВНИЙ ТЕСТ НАПРЯМУ: підсумок місяця й звіт про рух за той самий
// проміжок мусять давати ті самі гроші.
//
// Дві сторінки питають про той самий липень, і саме тут вони могли б
// розійтися мовчки — обидва числа лишились би правдоподібними. Тест
// стереже те, заради чого summarizeCash і винесена в спільну функцію.
func TestPeriodMoneyAgreesWithCashflow(t *testing.T) {
	srv, st := testServer(t)
	seedPeriodMonth(t, st)

	got := periodOf(t, srv.URL, "2026-07")

	resp, body := do(t, "GET", srv.URL+"/api/cashflow?from=2026-07-01&to=2026-07-31", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/cashflow: %d %s", resp.StatusCode, body)
	}
	var cf struct {
		OpeningUAH  float64 `json:"opening_uah"`
		IncomeUAH   float64 `json:"income_uah"`
		ContribUAH  float64 `json:"contributed_uah"`
		PurchaseUAH float64 `json:"purchased_uah"`
		ConvUAH     float64 `json:"conversions_uah"`
		ClosingUAH  float64 `json:"closing_uah"`
	}
	if err := json.Unmarshal([]byte(body), &cf); err != nil {
		t.Fatal(err)
	}
	m := got.Money
	if m.OpeningUAH != cf.OpeningUAH || m.IncomeUAH != cf.IncomeUAH ||
		m.ContribUAH != cf.ContribUAH || m.PurchaseUAH != cf.PurchaseUAH ||
		m.ConvUAH != cf.ConvUAH || m.ClosingUAH != cf.ClosingUAH {
		t.Errorf("підсумок %+v розійшовся з рухом %+v", m, cf)
	}
	if m.ContribUAH <= 0 || m.PurchaseUAH <= 0 {
		t.Errorf("місяць мав нести і внески, і покупку: %+v", m)
	}
}

// Місяць без жодної події не падає й не вигадує чисел.
func TestPeriodEmptyMonthIsAllZeros(t *testing.T) {
	srv, st := testServer(t)
	seedPeriodMonth(t, st)

	got := periodOf(t, srv.URL, "2026-05")
	if got.Money.IncomeUAH != 0 || got.Money.ContribUAH != 0 || got.Money.PurchaseUAH != 0 {
		t.Errorf("порожній місяць не мав нести грошей: %+v", got.Money)
	}
	if got.Decisions.Count != 0 || got.Decisions.Note == "" {
		t.Errorf("порожній місяць мав сказати, що нічого не куплено: %+v", got.Decisions)
	}
}

// Знімка рівно на межу немає — беремо найближчий раніший і НАЗИВАЄМО його
// справжню дату. Мовчазна підстановка сусіднього дня зробила б із дірки в
// даних результат місяця.
func TestPeriodStructureNamesTheRealSnapshotDates(t *testing.T) {
	srv, st := testServer(t)
	seedPeriodMonth(t, st)
	// 28 червня замість 30-го (демон лежав) і 29 липня замість 31-го.
	saveSnap(t, st, "2026-06-28", 100_000_00, 1000)
	saveSnap(t, st, "2026-07-29", 130_000_00, 1500)

	got := periodOf(t, srv.URL, "2026-07")
	if got.Structure == nil {
		t.Fatalf("розділ структури мав бути: %s", got.StructureNote)
	}
	if got.Structure.FromDate != "2026-06-28" || got.Structure.ToDate != "2026-07-29" {
		t.Errorf("дати знімків %s → %s, чекали 2026-06-28 → 2026-07-29",
			got.Structure.FromDate, got.Structure.ToDate)
	}
	var capital *periodRow
	for i := range got.Structure.Rows {
		if got.Structure.Rows[i].Key == "capital" {
			capital = &got.Structure.Rows[i]
		}
	}
	if capital == nil {
		t.Fatalf("рядка капіталу немає: %+v", got.Structure.Rows)
	}
	if capital.Before != 100000 || capital.After != 130000 || capital.Delta != 30000 {
		t.Errorf("капітал %+v, чекали 100 000 → 130 000 (+30 000)", capital)
	}
	if got.Structure.USDShareFrom != 10 || got.Structure.USDShareTo != 15 {
		t.Errorf("частка USD %.1f → %.1f, чекали 10 → 15",
			got.Structure.USDShareFrom, got.Structure.USDShareTo)
	}
}

// Знімків немає зовсім — розділ мовчить із названою причиною, а решта
// підсумку рахується.
func TestPeriodWithoutSnapshotsStillCountsMoney(t *testing.T) {
	srv, st := testServer(t)
	seedPeriodMonth(t, st)

	got := periodOf(t, srv.URL, "2026-07")
	if got.Structure != nil {
		t.Errorf("без знімків структури бути не мало: %+v", got.Structure)
	}
	if got.StructureNote == "" {
		t.Error("причина мовчання не названа")
	}
	if got.Money.ContribUAH <= 0 {
		t.Errorf("гроші мали порахуватись і без знімків: %+v", got.Money)
	}
}

// Місячна ціль береться зі знімка ТОГО місяця, а не з нинішніх
// налаштувань: ціль міняють, і міряти липень серпневою ціллю означало б
// переписувати минуле.
func TestPeriodPlanUsesTheTargetOfThatMonth(t *testing.T) {
	srv, st := testServer(t)
	seedPeriodMonth(t, st)
	sn := store.Snapshot{Date: "2026-07-20", MonthTargetUAH: 20_000_00}
	if err := st.SaveSnapshot(context.Background(), sn); err != nil {
		t.Fatal(err)
	}
	// Пізніша ціль — уже серпнева, і в липневий підсумок потрапити не має.
	sn = store.Snapshot{Date: "2026-08-20", MonthTargetUAH: 99_000_00}
	if err := st.SaveSnapshot(context.Background(), sn); err != nil {
		t.Fatal(err)
	}

	got := periodOf(t, srv.URL, "2026-07")
	if got.Plan == nil {
		t.Fatalf("розділ плану мав бути: %s", got.PlanNote)
	}
	if got.Plan.TargetUAH != 20000 || got.Plan.TargetOn != "2026-07-20" {
		t.Errorf("ціль %+v, чекали 20 000 ₴ зі знімка 2026-07-20", got.Plan)
	}
	if got.Plan.ContribUAH != got.Money.ContribUAH {
		t.Errorf("внесене в плані %.2f, у грошах %.2f — мусить бути те саме",
			got.Plan.ContribUAH, got.Money.ContribUAH)
	}
	if got.Plan.DonePct != 50 {
		t.Errorf("виконано %.1f%%, чекали 50 (10 000 з 20 000)", got.Plan.DonePct)
	}
}

// Рішення місяця: скільки їх і скільки з них були верхнім рядком.
func TestPeriodCountsThisMonthsDecisions(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seedPeriodMonth(t, st)
	for _, d := range []store.Decision{
		{MadeOn: "2026-07-05", Kind: store.BuyBond, Ref: "UA4000227748",
			Currency: money.UAH, Amount: 5000_00, RealPct: 9.4, RankPos: 1, RankMode: "plan"},
		{MadeOn: "2026-07-20", Kind: store.BuyBond, Ref: "UA4000227748",
			Currency: money.UAH, Amount: 5000_00, RealPct: 8.0, RankPos: 3,
			TopLabel: "UA4000999999", TopRealPct: 9.0, RankMode: "plan"},
		// Червневе рішення в липневий підсумок не входить.
		{MadeOn: "2026-06-30", Kind: store.BuyBond, Ref: "UA4000227748",
			Currency: money.UAH, Amount: 1000_00, RealPct: 7.0, RankPos: 1, RankMode: "plan"},
	} {
		if _, err := st.AddDecision(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	got := periodOf(t, srv.URL, "2026-07")
	if got.Decisions.Count != 2 || got.Decisions.Followed != 1 {
		t.Errorf("рішень %d, за рейтингом %d; чекали 2 і 1",
			got.Decisions.Count, got.Decisions.Followed)
	}
	// −1.0 п.п.: обране 8.0 проти верхнього 9.0, і знак значущий.
	if got.Decisions.VsTopPPAvg != -1 {
		t.Errorf("середнє розходження %.2f, чекали −1.00", got.Decisions.VsTopPPAvg)
	}
}

// Місяць у неправильному вигляді — 400, а не мовчазний підсумок за
// вигаданий проміжок.
func TestPeriodRejectsBadMonth(t *testing.T) {
	srv, st := testServer(t)
	seedPeriodMonth(t, st)
	for _, bad := range []string{"2026-13", "липень", "2026-07-01"} {
		resp, _ := do(t, "GET", srv.URL+"/api/period?month="+bad, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("month=%q дав %d, чекали 400", bad, resp.StatusCode)
		}
	}
}

// seedPeriodMonth — липень 2026: два внески, одна покупка.
func seedPeriodMonth(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	seed(t, st)
	for _, d := range []store.Deposit{
		{Date: "2026-07-02", Amount: 6000_00, Currency: money.UAH, Broker: "inzhur"},
		{Date: "2026-07-18", Amount: 4000_00, Currency: money.UAH, Broker: "inzhur"},
	} {
		if _, err := st.AddDeposit(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.AddLot(ctx, domain.Lot{ISIN: "UA4000227748", Qty: 5,
		PricePerBond: money.New(995_00, money.UAH), BuyDate: "2026-07-10",
		Channel: "inzhur"}); err != nil {
		t.Fatal(err)
	}
}

// saveSnap — знімок із заданим капіталом (усе в ОВДП) і часткою USD у
// базисних пунктах.
func saveSnap(t *testing.T, st *store.Store, date string, nominalMinor, usdBP int64) {
	t.Helper()
	if err := st.SaveSnapshot(context.Background(), store.Snapshot{
		Date: domain.Date(date), NominalUAHEq: nominalMinor, USDShareBP: usdBP,
	}); err != nil {
		t.Fatal(err)
	}
}

// ГОЛОВНИЙ ТЕСТ ВИПРАВЛЕННЯ: «внесено своїх» у підсумку місяця дорівнює
// плитці «Цей місяць» на «Огляді» (month_deposited_uah) — гаманець разом
// із подушкою. Доти підсумок брав лише гаманець, і місяць, у якому гроші
// пішли в матрац повз рахунок брокера, стояв «повз» у серії.
func TestPeriodOwnMatchesMonthTile(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seed(t, st)
	today := domain.NewDate(time.Now())
	first := domain.Date(string(today)[:8] + "01")
	if _, err := st.AddDeposit(ctx, store.Deposit{Date: first, Amount: 10_000_00,
		Currency: money.UAH, Broker: "mono"}); err != nil {
		t.Fatal(err)
	}
	// У подушку повз гаманець — і назад частину.
	for _, a := range []int64{5_000_00, -1_500_00} {
		if _, err := st.AddReserveOp(ctx, store.ReserveOp{Date: first, Amount: a,
			Currency: money.UAH, Place: "готівка"}); err != nil {
			t.Fatal(err)
		}
	}
	got := periodOf(t, srv.URL, string(today)[:7])
	if got.Money.ContribUAH != 10_000 || got.Money.OutsideUAH != 3_500 || got.Money.OwnUAH != 13_500 {
		t.Errorf("гаманець %v / подушка %v / разом %v, чекали 10 000 / 3 500 / 13 500",
			got.Money.ContribUAH, got.Money.OutsideUAH, got.Money.OwnUAH)
	}
	// Залишок гаманця подушки не бачить.
	if got.Money.ClosingUAH != 10_000 {
		t.Errorf("залишок гаманця %v, чекали 10 000", got.Money.ClosingUAH)
	}
	resp, body := do(t, "GET", srv.URL+"/api/summary", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/summary: %d %s", resp.StatusCode, body)
	}
	var sum struct {
		Deposited float64 `json:"month_deposited_uah"`
	}
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Deposited != got.Money.OwnUAH {
		t.Errorf("плитка «Цей місяць» %v ≠ підсумок %v", sum.Deposited, got.Money.OwnUAH)
	}
}
