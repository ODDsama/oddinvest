package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/store"
)

// getRivals — відповідь ручки на одному рівні.
func getRivals(t *testing.T, base, level string) rivalsResp {
	t.Helper()
	var out rivalsResp
	_, body := do(t, "GET", base+"/api/rivals?level="+level, "")
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("rivals %s: %v: %s", level, err, body)
	}
	return out
}

// openWindow — перший добовий знімок, від якого починається порівняння.
//
// Без нього порівнювати нема з чим за побудовою: своєї кривої застосунок
// до першого знімка не має. Тому знімок стоїть у КОЖНОМУ тесті нижче — це
// не декорація набору, а умова існування відповіді.
func openWindow(t *testing.T, st *store.Store, on domain.Date, sn store.Snapshot) {
	t.Helper()
	sn.Date = on
	if err := st.SaveSnapshot(context.Background(), sn); err != nil {
		t.Fatal(err)
	}
}

// twoRatePoints — курс торік і сьогодні, щоб валютні суперники мали чим
// жити.
func twoRatePoints(t *testing.T, st *store.Store, code string, thenE4, nowE4 int64) {
	t.Helper()
	ctx := context.Background()
	if err := st.SaveRate(ctx, code, thenE4, "2025-01-01"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRate(ctx, code, nowE4, domain.NewDate(time.Now())); err != nil {
		t.Fatal(err)
	}
}

// ГОЛОВНИЙ СТОРОЖ ФАЗИ: долар у «Ціні рішень» — те саме число, що й у
// /api/benchmark, до копійки.
//
// Це не порівняння двох реалізацій, а доказ, що реалізація одна: benchmark
// відколи існують суперники — тонка обгортка над тим самим рушієм. Тест
// стоїть саме тому, що зворотне вилізло б не тут, а на віхі «Обіграв
// просто долари», яка каже те саме іншими словами й на іншому екрані.
func TestRivalsUSDMatchesBenchmark(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	twoRatePoints(t, st, "USD", 250000, 500000)
	openWindow(t, st, "2025-06-01", store.Snapshot{})
	for _, d := range []struct {
		on  domain.Date
		amt int64
	}{{"2025-06-15", 1_000_000}, {domain.NewDate(time.Now()), 1_000_000}} {
		if _, err := st.AddDeposit(ctx, store.Deposit{
			Date: d.on, Amount: d.amt, Currency: "UAH", Broker: "mono"}); err != nil {
			t.Fatal(err)
		}
	}

	var b benchResult
	_, body := do(t, "GET", srv.URL+"/api/benchmark", "")
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		t.Fatalf("benchmark: %v: %s", err, body)
	}
	rv := getRivals(t, srv.URL, levelPortfolio)
	usd := rv.row(domain.RivalUSDCash)

	if usd.Why != "" {
		t.Fatalf("курси є на всі дати, а суперник мовчить: %s", usd.Why)
	}
	if math.Abs(usd.TerminalUAH-b.BenchmarkUAH) > 0.005 {
		t.Errorf("долар: суперник %.2f, бенчмарк %.2f — це два різні рахунки одного числа",
			usd.TerminalUAH, b.BenchmarkUAH)
	}
	if math.Abs(rv.ActualUAH-b.PortfolioUAH) > 0.005 {
		t.Errorf("портфель: суперники %.2f, бенчмарк %.2f", rv.ActualUAH, b.PortfolioUAH)
	}
	if math.Abs(usd.DiffUAH-b.DiffUAH) > 0.005 {
		t.Errorf("різниця: суперники %.2f, бенчмарк %.2f", usd.DiffUAH, b.DiffUAH)
	}
}

// «Гривня під матрацом» — це сума внесків, і вона ж in_uah.
//
// Сторож зібраності потоку на рівні ручки: 10 000 + 10 000 внесених — це
// 20 000 незалежно від того, що з ними сталось далі.
func TestRivalsUAHCashEqualsContributions(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	twoRatePoints(t, st, "USD", 400000, 400000)
	openWindow(t, st, "2025-06-01", store.Snapshot{})
	for _, on := range []domain.Date{"2025-06-15", domain.NewDate(time.Now())} {
		if _, err := st.AddDeposit(ctx, store.Deposit{
			Date: on, Amount: 1_000_000, Currency: "UAH", Broker: "mono"}); err != nil {
			t.Fatal(err)
		}
	}
	rv := getRivals(t, srv.URL, levelPortfolio)
	cash := rv.row(domain.RivalUAHCash)
	if math.Abs(cash.TerminalUAH-20000) > 0.01 {
		t.Errorf("сума внесків = %.2f, очікували 20 000", cash.TerminalUAH)
	}
	if math.Abs(rv.InUAH-cash.TerminalUAH) > 0.005 {
		t.Errorf("in_uah (%.2f) мусить дорівнювати гривні під матрацом (%.2f)",
			rv.InUAH, cash.TerminalUAH)
	}
}

// ІНВАРІАНТ ДВОХ РІВНІВ: різниця баз — це рівно подушка, цілі й пенсійний,
// і різниця внесків — рівно ті самі три журнали.
//
// Саме він робить законним читання двох чисел поруч. Якби рівні збирались
// із різних доданків, «усі гроші мінус портфель» не означало б нічого, а
// виглядало б осмислено.
func TestRivalsLevelGapEqualsThreeJournals(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	twoRatePoints(t, st, "USD", 400000, 400000)
	openWindow(t, st, "2025-06-01", store.Snapshot{})

	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2025-06-15", Amount: 1_000_000, Currency: "UAH", Broker: "mono"}); err != nil {
		t.Fatal(err)
	}
	// Подушка й ціль — власними журналами, повз гаманець (саме так їх і
	// заводить застосунок).
	if _, err := st.AddReserveOp(ctx, store.ReserveOp{
		Date: "2025-07-01", Amount: 500_000, Currency: "UAH", Place: "готівка"}); err != nil {
		t.Fatal(err)
	}
	gid, err := st.AddGoal(ctx, store.Goal{Name: "Авто", TargetAmount: 10_000_000, Currency: "UAH"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddGoalOp(ctx, store.GoalOp{
		GoalID: gid, Date: "2025-07-02", Amount: 300_000, Currency: "UAH"}); err != nil {
		t.Fatal(err)
	}

	one := getRivals(t, srv.URL, levelPortfolio)
	all := getRivals(t, srv.URL, levelAll)

	// Внески: 10 000 проти 10 000 + 5 000 + 3 000.
	gapIn := all.InUAH - one.InUAH
	if math.Abs(gapIn-8000) > 0.01 {
		t.Errorf("різниця внесків = %.2f, а три журнали дають 8 000", gapIn)
	}
	// Бази: те саме, бо в цьому наборі подушка й ціль лежать грішми.
	gapBase := all.ActualUAH - one.ActualUAH
	if math.Abs(gapBase-8000) > 0.01 {
		t.Errorf("різниця баз = %.2f, а подушка+ціль дають 8 000", gapBase)
	}
	if all.LevelLabel != "Усі гроші" || one.LevelLabel != "Портфель" {
		t.Errorf("рівні мусять називатись: %q / %q", one.LevelLabel, all.LevelLabel)
	}
}

// Переказ між журналами НЕ створює нових грошей.
//
// Окремої сутності переказу в застосунку немає — він записується парою
// «зняття + поповнення», — і саме на цьому стоїть право підсумувати
// чотири журнали. Без цієї властивості рівень «усі гроші» роздувався б на
// кожному перекладанні з гаманця під матрац, лишаючись правдоподібним.
func TestRivalsTransferIsNotContribution(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	twoRatePoints(t, st, "USD", 400000, 400000)
	openWindow(t, st, "2025-06-01", store.Snapshot{})

	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2025-06-15", Amount: 1_000_000, Currency: "UAH", Broker: "mono"}); err != nil {
		t.Fatal(err)
	}
	before := getRivals(t, srv.URL, levelAll).InUAH

	// Переклали 4 000 ₴ із гаманця під матрац.
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2025-07-01", Amount: -400_000, Currency: "UAH", Broker: "mono"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddReserveOp(ctx, store.ReserveOp{
		Date: "2025-07-01", Amount: 400_000, Currency: "UAH", Place: "готівка"}); err != nil {
		t.Fatal(err)
	}
	after := getRivals(t, srv.URL, levelAll).InUAH

	if math.Abs(after-before) > 0.01 {
		t.Errorf("переказ додав %.2f нових грошей, а мав нуль (було %.2f, стало %.2f)",
			after-before, before, after)
	}
}

// Ринковий суперник бере рівень розміщення Мінфіну на дату внеску.
func TestRivalsOVDPUsesAuctionLevel(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	twoRatePoints(t, st, "USD", 400000, 400000)
	if err := st.SaveAuctions(ctx, []nbu.Auction{{
		Date: "2025-01-10", Num: "1", ISIN: "UA4000000001", Currency: "UAH",
		Bucket: rivalOVDPBucket, DaysToRepay: 365, IncomeBP: 1500,
	}}); err != nil {
		t.Fatal(err)
	}
	// Рік тому рівно: 100 000 ₴ під 15% мали стати ≈115 000 ₴.
	yearAgo := domain.NewDate(time.Now().AddDate(-1, 0, 0))
	openWindow(t, st, yearAgo.AddDays(-1), store.Snapshot{})
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: yearAgo, Amount: 10_000_000, Currency: "UAH", Broker: "mono"}); err != nil {
		t.Fatal(err)
	}
	rv := getRivals(t, srv.URL, levelPortfolio)
	ovdp := rv.row(domain.RivalOVDPMarket)
	if ovdp.Why != "" {
		t.Fatalf("рівень розміщення є, а суперник мовчить: %s", ovdp.Why)
	}
	if ovdp.TerminalUAH < 114_500 || ovdp.TerminalUAH > 115_500 {
		t.Errorf("рік під 15%% мав дати ≈115 000, а маємо %.2f", ovdp.TerminalUAH)
	}
	if rv.OVDPBucket != rivalOVDPBucket {
		t.Errorf("строк суперника мусить бути названий у відповіді, а маємо %q", rv.OVDPBucket)
	}
}

// Без історії аукціонів ринковий суперник мовчить із причиною, а не
// показує нуль: нуль на графіку читався б як «ринок нічого не платив».
func TestRivalsOVDPSilentWithoutAuctions(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	twoRatePoints(t, st, "USD", 400000, 400000)
	openWindow(t, st, "2025-06-01", store.Snapshot{})
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2025-06-15", Amount: 1_000_000, Currency: "UAH", Broker: "mono"}); err != nil {
		t.Fatal(err)
	}
	ovdp := getRivals(t, srv.URL, levelPortfolio).row(domain.RivalOVDPMarket)
	if ovdp.Why == "" {
		t.Fatal("без жодного аукціону суперник мусив назвати причину мовчання")
	}
	if len(ovdp.PointsDiff) != 0 || ovdp.TerminalUAH != 0 {
		t.Errorf("мовчазний суперник не має віддавати чисел: точок %d, термінал %.2f",
			len(ovdp.PointsDiff), ovdp.TerminalUAH)
	}
}

// Крива факту й криві суперників стоять на ОДНІЙ сітці.
//
// Різна довжина намалювала б зсув у часі як розбіжність у грошах — те
// саме, від чого DaysGrid і живе в domain поруч із рушієм.
func TestRivalsCurvesShareOneGrid(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	twoRatePoints(t, st, "USD", 400000, 400000)
	openWindow(t, st, "2025-06-01", store.Snapshot{})
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2025-06-15", Amount: 1_000_000, Currency: "UAH", Broker: "mono"}); err != nil {
		t.Fatal(err)
	}
	rv := getRivals(t, srv.URL, levelPortfolio)
	if rv.DayCount != len(rv.Days) || len(rv.Actual) != len(rv.Days) {
		t.Fatalf("сітка %d, дат %d, факту %d", rv.DayCount, len(rv.Days), len(rv.Actual))
	}
	for _, row := range rv.Rivals {
		if row.Why != "" {
			continue
		}
		if len(row.PointsDiff) != len(rv.Days) {
			t.Errorf("%s: точок %d при сітці %d", row.Key, len(row.PointsDiff), len(rv.Days))
		}
	}
	if rv.Days[len(rv.Days)-1] != string(domain.NewDate(time.Now())) {
		t.Errorf("сітка мусить доходити до сьогодні, а закінчується %s", rv.Days[len(rv.Days)-1])
	}
	if rv.Young {
		t.Error("рік історії вже не молодий: прапорець вмикає прозу про момент входу, і тут вона зайва")
	}
}

// Порожня база — не привід малювати нулі: без жодного внеску порівнювати
// нема з чим, і відповідь мусить це сказати, а не показати чотири нулі.
func TestRivalsEmptyBaseSaysNothingToCompare(t *testing.T) {
	srv, _ := testServer(t)
	rv := getRivals(t, srv.URL, levelPortfolio)
	if rv.Flows != 0 || rv.DayCount != 0 || len(rv.Rivals) != 0 {
		t.Errorf("на порожній базі: рухів %d, днів %d, суперників %d — усе мало бути нулем",
			rv.Flows, rv.DayCount, len(rv.Rivals))
	}
	if rv.Why == "" {
		t.Error("порожнеча мусить бути названою: чотири нулі читались би як «усе втрачено»")
	}
}

// Невідомий рівень — помилка запиту, а не тихий портфель: інакше
// одруківка в адресі показувала б не ті гроші, і мовчки.
func TestRivalsRejectsUnknownLevel(t *testing.T) {
	srv, _ := testServer(t)
	res, _ := do(t, "GET", srv.URL+"/api/rivals?level=everything", "")
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("невідомий рівень мав дати 400, а дав %d", res.StatusCode)
	}
}

// Рух У САМ ДЕНЬ відкриття вікна рахується, а не зникає.
//
// Добовий знімок кладеться о 06:10 Києва, тобто описує ранок дня, а не
// його кінець; операції люди вносять удень. Перше формулювання відсікало
// потоки «<= відкриття» — і мовчки з'їдало все, зроблене першого дня
// після сніданку. Гроші, що зникають без сліду, роблять портфель кращим,
// ніж він є, і саме тому це окремий тест, а не рядок в іншому.
func TestRivalsOpeningDayFlowCounts(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	twoRatePoints(t, st, "USD", 400000, 400000)
	openWindow(t, st, "2025-06-01", store.Snapshot{})
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2025-06-01", Amount: 700_000, Currency: "UAH", Broker: "mono"}); err != nil {
		t.Fatal(err)
	}
	rv := getRivals(t, srv.URL, levelPortfolio)
	if math.Abs(rv.InUAH-7000) > 0.01 {
		t.Errorf("у грі %.2f ₴, а внесок дня відкриття — 7 000 ₴", rv.InUAH)
	}
	if rv.Flows != 1 {
		t.Errorf("рухів %d, а мав бути один", rv.Flows)
	}
}

// Вікно без жодних грошей мовчить, а не показує чотири нулі.
//
// Так виглядає перший день життя бази: демон кладе знімок одразу, а
// грошей ще немає. Нулі в таблиці читаються як «усе втрачено» — тобто
// найгірша з можливих відповідей на «нічого не сталось».
func TestRivalsEmptyWindowSaysNothingToCompare(t *testing.T) {
	srv, st := testServer(t)
	twoRatePoints(t, st, "USD", 400000, 400000)
	openWindow(t, st, domain.NewDate(time.Now()), store.Snapshot{})
	rv := getRivals(t, srv.URL, levelPortfolio)
	if rv.Why == "" {
		t.Fatal("порожнє вікно мусить бути назване, а не показане нулями")
	}
	if len(rv.Rivals) != 0 {
		t.Errorf("суперників %d, а мало бути нуль", len(rv.Rivals))
	}
}

// Остання точка кривої різниці дорівнює числу в таблиці.
//
// Крива й таблиця відповідають на одне питання, і розійтись їм не можна:
// на екрані вони стоять поруч, тож розбіжність читалась би не як помилка,
// а як два різні факти.
func TestRivalsDiffCurveEndsAtDiffNumber(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	twoRatePoints(t, st, "USD", 250000, 500000)
	openWindow(t, st, "2025-06-01", store.Snapshot{})
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2025-06-15", Amount: 1_000_000, Currency: "UAH", Broker: "mono"}); err != nil {
		t.Fatal(err)
	}
	rv := getRivals(t, srv.URL, levelPortfolio)
	for _, r := range rv.Rivals {
		if r.Why != "" {
			continue
		}
		if len(r.PointsDiff) != rv.DayCount {
			t.Errorf("%s: точок різниці %d при сітці %d", r.Key, len(r.PointsDiff), rv.DayCount)
			continue
		}
		last := r.PointsDiff[len(r.PointsDiff)-1]
		if math.Abs(last-r.DiffUAH) > 0.01 {
			t.Errorf("%s: кінець кривої %.2f, а в таблиці %.2f", r.Key, last, r.DiffUAH)
		}
	}
}
