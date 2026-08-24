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
