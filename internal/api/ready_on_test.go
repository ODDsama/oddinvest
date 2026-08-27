package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// docWith — мінімальний документ стану з одними лише балансами: readyFor
// більше нічого й не читає.
func docWith(brokers map[string]map[string]float64) *state.Doc {
	return &state.Doc{Brokers: brokers}
}

// testLogger — тихий журнал для сервера, зібраного повз testServer:
// частині тестів потрібен не HTTP, а самі методи.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// Гроші на рахунку A не наближають покупку в рахунку B.
//
// Це головний тест напряму: саме тому дата рахується по парах
// (брокер × валюта), а не по валюті. Зведена сума тут покрила б папір
// удвічі, а насправді не покриває його ніде.
func TestReadyForUsesOnlyThatBrokersIncome(t *testing.T) {
	inc := incomeAhead{
		{Broker: "inzhur", Currency: money.UAH}: {
			{Date: "2026-09-16", Amount: 300_00, Label: "UA4000227748"},
		},
	}
	doc := docWith(map[string]map[string]float64{
		"inzhur": {money.UAH: 200},
		"mono":   {money.UAH: 900},
	})

	if _, ok := inc.readyFor(doc, money.UAH, 1000_00); ok {
		t.Fatal("500 ₴ в inzhur і 900 ₴ в mono не покривають папір за 1 000 ₴ ніде")
	}
	// А з купоном, що покриває саме inzhur, дата зʼявляється — і саме там.
	inc[store.BrokerCur{Broker: "inzhur", Currency: money.UAH}] = []readyFlow{
		{Date: "2026-09-16", Amount: 900_00, Label: "UA4000227748"},
	}
	got, ok := inc.readyFor(doc, money.UAH, 1000_00)
	if !ok {
		t.Fatal("200 + 900 = 1 100 ₴ мали покрити папір за 1 000 ₴")
	}
	if got.Broker != "inzhur" || got.Date != "2026-09-16" {
		t.Errorf("набереться %s у %q, чекали 2026-09-16 в inzhur", got.Date, got.Broker)
	}
}

// Коли покривають двоє — відповідь та, що раніша.
func TestReadyForPicksTheEarliestDate(t *testing.T) {
	inc := incomeAhead{
		{Broker: "mono", Currency: money.UAH}: {
			{Date: "2026-12-01", Amount: 1000_00, Label: "вклад mono"},
		},
		{Broker: "inzhur", Currency: money.UAH}: {
			{Date: "2026-09-16", Amount: 1000_00, Label: "UA4000227748"},
		},
	}
	doc := docWith(map[string]map[string]float64{
		"inzhur": {money.UAH: 0},
		"mono":   {money.UAH: 0},
	})

	got, ok := inc.readyFor(doc, money.UAH, 1000_00)
	if !ok {
		t.Fatal("обидва рахунки покривають — дата мала бути")
	}
	if got.Broker != "inzhur" || got.Date != "2026-09-16" {
		t.Errorf("%s у %q, чекали найранішу 2026-09-16 в inzhur", got.Date, got.Broker)
	}
}

// Надходження є, але їх мало: дати немає, і це окрема відповідь, а не
// «набереться колись».
func TestReadyForSilentWhenIncomeNeverCovers(t *testing.T) {
	inc := incomeAhead{
		{Broker: "inzhur", Currency: money.UAH}: {
			{Date: "2026-09-16", Amount: 82_75, Label: "UA4000227748"},
			{Date: "2027-03-17", Amount: 82_75, Label: "UA4000227748"},
		},
	}
	doc := docWith(map[string]map[string]float64{"inzhur": {money.UAH: 10}})
	if _, ok := inc.readyFor(doc, money.UAH, 1000_00); ok {
		t.Error("два купони по 82.75 ₴ не покривають папір за 1 000 ₴")
	}
}

// Повернення тіла їде поруч із сумою, окремим числом.
//
// «Коли вистачить» цієї різниці не бачить і не має бачити: на рахунку
// купон і погашення однакові гроші. Її бачить маршрут (route.go), де
// купон — новий капітал, а погашення лише міняє форму власного тіла. Тест
// стоїть тут, бо заповнюється поле саме тут, і мовчазна втрата Principal
// у майбутньому редагуванні futureIncome інакше вилізла б аж у проході.
//
// Фікстура зумисне та, де в день погашення платять ОБОЄ: після зведення
// одного дня рядок мусить нести повну суму й тіло всередині неї.
func TestFutureIncomeCarriesPrincipal(t *testing.T) {
	_, st := testServer(t)
	ctx := context.Background()
	today := domain.NewDate(time.Now())
	seedFuturePayer(t, st, today)

	if _, err := st.AddLot(ctx, domain.Lot{
		ISIN: futureISIN, Qty: 3, PricePerBond: money.New(995_00, money.UAH),
		BuyDate: today, Channel: "inzhur"}); err != nil {
		t.Fatal(err)
	}
	srv := New(st, nil, testLogger())
	src, err := srv.loadSources(ctx, today)
	if err != nil {
		t.Fatal(err)
	}
	inc, err := srv.futureIncome(src, today)
	if err != nil {
		t.Fatal(err)
	}

	flows := inc[store.BrokerCur{Broker: "inzhur", Currency: money.UAH}]
	if len(flows) != 2 {
		t.Fatalf("надходжень %d, чекали два (купон і день погашення): %+v", len(flows), flows)
	}
	coupon, maturity := flows[0], flows[1]
	if coupon.Principal != 0 {
		t.Errorf("купон несе тіло %d — купон це чистий дохід", coupon.Principal)
	}
	if coupon.Kind != "bonds" {
		t.Errorf("вид купона %q, чекали bonds", coupon.Kind)
	}
	// День погашення: 3 × (82,75 купона + 1 000,00 тіла).
	if maturity.Amount != 3*(82_75+1000_00) {
		t.Errorf("сума дня погашення %d, чекали %d", maturity.Amount, 3*(82_75+1000_00))
	}
	if maturity.Principal != 3*1000_00 {
		t.Errorf("тіло %d, чекали %d — купон того ж дня тілом не є",
			maturity.Principal, 3*1000_00)
	}
}

// Валюта не змішується: доларові надходження не наближають гривневий
// папір, скільки б їх не було.
func TestReadyForKeepsCurrenciesApart(t *testing.T) {
	inc := incomeAhead{
		{Broker: "inzhur", Currency: money.USD}: {
			{Date: "2026-09-16", Amount: 1000_00, Label: "UA4000227XXX"},
		},
	}
	doc := docWith(map[string]map[string]float64{"inzhur": {money.USD: 500}})
	if _, ok := inc.readyFor(doc, money.UAH, 1000_00); ok {
		t.Error("долари не купують гривневий папір")
	}
}

// Виплати, розкладені по брокерах, у сумі дорівнюють загальному розкладу.
//
// Саме на цьому тримається право партиціювати лоти й кликати
// domain.FuturePayments на кожну частку окремо: HolderQty лінійна за
// лотами, тож розбиття не створює й не губить грошей. Розійдись вони — і
// дата поїхала б у бік, у який ніхто не дивиться.
func TestFutureIncomeSplitByBrokerSumsToWholeSchedule(t *testing.T) {
	_, st := testServer(t)
	ctx := context.Background()
	today := domain.NewDate(time.Now())
	seedFuturePayer(t, st, today)

	for _, l := range []domain.Lot{
		{ISIN: futureISIN, Qty: 3, PricePerBond: money.New(995_00, money.UAH),
			BuyDate: today, Channel: "inzhur"},
		{ISIN: futureISIN, Qty: 2, PricePerBond: money.New(995_00, money.UAH),
			BuyDate: today, Channel: "mono"},
	} {
		if _, err := st.AddLot(ctx, l); err != nil {
			t.Fatal(err)
		}
	}

	srv := New(st, nil, testLogger())
	src, err := srv.loadSources(ctx, today)
	if err != nil {
		t.Fatal(err)
	}
	inc, err := srv.futureIncome(src, today)
	if err != nil {
		t.Fatal(err)
	}

	var split int64
	for _, flows := range inc {
		for _, f := range flows {
			split += f.Amount
		}
	}
	whole, err := domain.FuturePayments(src.pays, src.lots, src.sales, today)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, cf := range whole {
		total += cf.Amount.Amount()
	}
	if split != total {
		t.Errorf("по брокерах %d, разом %d — розбиття втратило гроші", split, total)
	}
	if len(inc) != 2 {
		t.Errorf("рахунків %d, чекали два (inzhur і mono)", len(inc))
	}

	// Купон і погашення того самого паперу того самого дня — один прихід
	// грошей, а не два рядки з однаковою назвою. Перевіряємо на дні
	// погашення, де вони й збігаються.
	for k, flows := range inc {
		seen := map[string]bool{}
		for _, f := range flows {
			key := string(f.Date) + "|" + f.Label
			if seen[key] {
				t.Errorf("%v: %s %s двічі — виплати одного дня мали злитись", k, f.Date, f.Label)
			}
			seen[key] = true
		}
	}
}

// Виплата, яку гаманець уже порахував балансом, у майбутні надходження не
// потрапляє. Інакше позначений «отримано» купон сьогоднішнього дня
// лічився б двічі — і дата виходила б ближчою, ніж є.
func TestFutureIncomeSkipsWhatTheWalletAlreadyCounted(t *testing.T) {
	_, st := testServer(t)
	ctx := context.Background()
	today := domain.NewDate(time.Now())
	seedTodayPayer(t, st, today)

	if _, err := st.AddLot(ctx, domain.Lot{ISIN: todayISIN, Qty: 1,
		PricePerBond: money.New(995_00, money.UAH), BuyDate: today.AddDays(-10),
		Channel: "inzhur"}); err != nil {
		t.Fatal(err)
	}

	srv := New(st, nil, testLogger())
	src, err := srv.loadSources(ctx, today)
	if err != nil {
		t.Fatal(err)
	}
	inc, err := srv.futureIncome(src, today)
	if err != nil {
		t.Fatal(err)
	}
	if len(inc) == 0 {
		t.Fatal("непозначена сьогоднішня виплата мала лишитись надходженням")
	}

	if err := st.SetPaymentStatus(ctx, todayISIN, today, "received"); err != nil {
		t.Fatal(err)
	}
	src, err = srv.loadSources(ctx, today)
	if err != nil {
		t.Fatal(err)
	}
	inc, err = srv.futureIncome(src, today)
	if err != nil {
		t.Fatal(err)
	}
	for k, flows := range inc {
		for _, f := range flows {
			if f.Date == today {
				t.Errorf("%v: позначена отриманою виплата %s лишилась у майбутніх", k, f.Date)
			}
		}
	}
}

// Рядок, на який сьогодні не стає, у /api/reinvest несе дату, брокера й
// склад надходжень. Кінець-у-кінець: те, що побачить екран.
func TestReinvestRowCarriesReadyDate(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	today := domain.NewDate(time.Now())
	seedFuturePayer(t, st, today)

	// Лот у inzhur — саме він приведе купон на цей рахунок.
	if _, err := st.AddLot(ctx, domain.Lot{ISIN: futureISIN, Qty: 20,
		PricePerBond: money.New(995_00, money.UAH), BuyDate: today, Channel: "inzhur"}); err != nil {
		t.Fatal(err)
	}
	// Грошей на рахунку менше, ніж коштує папір, але з купоном стане.
	if _, err := st.AddDeposit(ctx, store.Deposit{Date: today, Amount: 500_00,
		Currency: money.UAH, Broker: "inzhur"}); err != nil {
		t.Fatal(err)
	}

	resp, body := do(t, "GET", srv.URL+"/api/reinvest", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/reinvest: %d %s", resp.StatusCode, body)
	}
	var rows []struct {
		ISIN     string `json:"isin"`
		CanBuy   bool   `json:"can_buy"`
		ReadyOn  string `json:"ready_on"`
		Broker   string `json:"ready_broker"`
		Days     int    `json:"ready_days"`
		ReadyVia []struct {
			Date  string `json:"date"`
			Label string `json:"label"`
		} `json:"ready_via"`
	}
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range rows {
		if r.ISIN != futureISIN {
			continue
		}
		found = true
		if r.CanBuy {
			t.Fatalf("на 500 ₴ папір за ~1 000 ₴ купитись не мав: %+v", r)
		}
		if r.ReadyOn == "" {
			t.Fatal("дата доступності мала бути: купон покриває нестачу")
		}
		if r.Broker != "inzhur" {
			t.Errorf("рахунок %q, чекали inzhur", r.Broker)
		}
		if r.Days <= 0 {
			t.Errorf("днів очікування %d, чекали додатне", r.Days)
		}
		if len(r.ReadyVia) == 0 || r.ReadyVia[0].Label != futureISIN {
			t.Errorf("склад надходжень %+v, чекали купон %s", r.ReadyVia, futureISIN)
		}
	}
	if !found {
		t.Fatalf("паперу %s немає в переліку: %s", futureISIN, body)
	}
}

// Рядок, на який стає вже сьогодні, дати не отримує: він і так зверху, а
// «набереться сьогодні» читалось би як очікування, якого немає.
func TestReinvestAffordableRowHasNoReadyDate(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	today := domain.NewDate(time.Now())
	seedFuturePayer(t, st, today)

	if _, err := st.AddDeposit(ctx, store.Deposit{Date: today, Amount: 50000_00,
		Currency: money.UAH, Broker: "inzhur"}); err != nil {
		t.Fatal(err)
	}

	resp, body := do(t, "GET", srv.URL+"/api/reinvest", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/reinvest: %d %s", resp.StatusCode, body)
	}
	var rows []struct {
		ISIN    string `json:"isin"`
		CanBuy  bool   `json:"can_buy"`
		ReadyOn string `json:"ready_on"`
		Note    string `json:"ready_note"`
	}
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ISIN == futureISIN {
			if !r.CanBuy {
				t.Fatalf("на 50 000 ₴ папір мав бути по кишені: %+v", r)
			}
			if r.ReadyOn != "" || r.Note != "" {
				t.Errorf("доступний рядок дістав дату %q / причину %q", r.ReadyOn, r.Note)
			}
		}
	}
}

const (
	futureISIN = "UA4000999901"
	todayISIN  = "UA4000999902"
)

// seedFuturePayer — папір, що платить купон через 30 днів і гаситься через
// рік. Дати рахуються від сьогодні навмисно: фіксовані числа перетворили б
// тест на такий, що псується від настання власної дати.
func seedFuturePayer(t *testing.T, st *store.Store, today domain.Date) {
	t.Helper()
	coupon := today.AddDays(30)
	maturity := today.AddDays(365)
	secs := []nbu.Security{{
		Bond: domain.Bond{ISIN: futureISIN, Nominal: money.New(1000_00, money.UAH),
			RateBP: 1655, Maturity: maturity, Descr: "тестовий"},
		Payments: []domain.Payment{
			{ISIN: futureISIN, PayDate: coupon, Type: domain.PayCoupon, PerBond: money.New(82_75, money.UAH)},
			{ISIN: futureISIN, PayDate: maturity, Type: domain.PayCoupon, PerBond: money.New(82_75, money.UAH)},
			{ISIN: futureISIN, PayDate: maturity, Type: domain.PayRedemption, PerBond: money.New(1000_00, money.UAH)},
		},
	}}
	if err := st.ReplaceDirectory(context.Background(), secs, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// seedTodayPayer — папір із виплатою рівно сьогодні: єдиний день, коли
// «отримано» й «попереду» можуть накластися.
func seedTodayPayer(t *testing.T, st *store.Store, today domain.Date) {
	t.Helper()
	maturity := today.AddDays(365)
	secs := []nbu.Security{{
		Bond: domain.Bond{ISIN: todayISIN, Nominal: money.New(1000_00, money.UAH),
			RateBP: 1655, Maturity: maturity, Descr: "тестовий"},
		Payments: []domain.Payment{
			{ISIN: todayISIN, PayDate: today, Type: domain.PayCoupon, PerBond: money.New(82_75, money.UAH)},
			{ISIN: todayISIN, PayDate: maturity, Type: domain.PayRedemption, PerBond: money.New(1000_00, money.UAH)},
		},
	}}
	if err := st.ReplaceDirectory(context.Background(), secs, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// routeIncome бачить те саме, що futureIncome, ПЛЮС оцінені дивіденди
// фонду — і кожне зі своєю основою.
//
// Головний бік цього тесту — саме РІВНІСТЬ зобовʼязань. Відмова в README
// («планових надходжень і оцінок у даті „коли вистачить" немає») тримається
// на тому, що futureIncome лишається такою, як була; варто routeIncome
// почати правити її зріз — і дата поїде разом із нею.
func TestRouteIncomeAddsEstimatesAndLeavesObligationsAlone(t *testing.T) {
	_, st := testServer(t)
	ctx := context.Background()
	today := domain.NewDate(time.Now())
	seedFuturePayer(t, st, today)

	if _, err := st.AddLot(ctx, domain.Lot{
		ISIN: futureISIN, Qty: 3, PricePerBond: money.New(995_00, money.UAH),
		BuyDate: today, Channel: "mono"}); err != nil {
		t.Fatal(err)
	}
	// Фонд, який платить: куплений в inzhur, з відомим днем виплати й
	// обіцяною ставкою — без них оцінка не рахується взагалі.
	for _, op := range []domain.FundOp{
		{Date: today.AddDays(-140), Fund: "Inzhur REIT", Kind: domain.FundBuy,
			Qty: 500, Amount: 500_000, Currency: money.UAH, Broker: "inzhur"},
	} {
		if _, err := st.AddFundOp(ctx, op); err != nil {
			t.Fatal(err)
		}
	}
	funds, err := st.ListFunds(ctx)
	if err != nil || len(funds) != 1 {
		t.Fatalf("довідник фондів: %v %+v", err, funds)
	}
	f := funds[0]
	f.ExpectedYieldBP, f.PayoutDay = 1200, 10
	if err := st.RenameFund(ctx, f.ID, f); err != nil {
		t.Fatal(err)
	}

	srv := New(st, nil, testLogger())
	src, err := srv.loadSources(ctx, today)
	if err != nil {
		t.Fatal(err)
	}
	base, err := srv.futureIncome(src, today)
	if err != nil {
		t.Fatal(err)
	}
	full, err := srv.routeIncome(src, today, 12)
	if err != nil {
		t.Fatal(err)
	}

	// Зобовʼязання лишились байт у байт тими самими.
	mono := store.BrokerCur{Broker: "mono", Currency: money.UAH}
	if len(full[mono]) != len(base[mono]) {
		t.Fatalf("зріз зобовʼязань змінився: було %d, стало %d",
			len(base[mono]), len(full[mono]))
	}
	for i := range base[mono] {
		if full[mono][i] != base[mono][i] {
			t.Errorf("надходження %d змінилось: %+v проти %+v",
				i, full[mono][i], base[mono][i])
		}
	}

	// А оцінки зʼявились — на рахунку, де фонд куплено, і підписані.
	inzhur := store.BrokerCur{Broker: "inzhur", Currency: money.UAH}
	if len(base[inzhur]) != 0 {
		t.Fatalf("futureIncome не мала бачити фонду взагалі: %+v", base[inzhur])
	}
	divs := full[inzhur]
	if len(divs) == 0 {
		t.Fatal("оцінені дивіденди не дійшли до маршруту")
	}
	for _, d := range divs {
		if d.Basis != basisEstimate {
			t.Errorf("дивіденд %s без підпису оцінки: basis=%q", d.Date, d.Basis)
		}
		if d.Kind != "funds" {
			t.Errorf("дивіденд %s: вид %q, чекали funds", d.Date, d.Kind)
		}
		if d.Principal != 0 {
			t.Errorf("дивіденд %s несе тіло %d — виплата фонду це чистий дохід",
				d.Date, d.Principal)
		}
	}
}
