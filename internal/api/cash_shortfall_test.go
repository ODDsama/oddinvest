package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// checkOf — POST на /check і розбір відповіді. Помилковий статус валить
// тест одразу: далі порівнювати нема чого.
func checkOf(t *testing.T, url, body string) cashCheckResp {
	t.Helper()
	resp, raw := do(t, "POST", url, body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: %d %s", url, resp.StatusCode, raw)
	}
	var got cashCheckResp
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

const lotBody = `{"isin":"UA4000227748","qty":5,"price_per_bond":"995.00",` +
	`"fee":"25.00","buy_date":"2026-07-01","channel":"inzhur"}`

// Вартість лота в /check — ціна×кількість ПЛЮС комісія, а нестача —
// різниця з тим, що вже лежить у брокера.
func TestLotCheckReportsExactShortfall(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if _, err := st.AddDeposit(context.Background(), store.Deposit{
		Date: "2026-07-01", Amount: 1000_00, Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}

	got := checkOf(t, srv.URL+"/api/lots/check", lotBody)
	if got.Enough {
		t.Fatalf("на 1 000 ₴ лот за 5 000 ₴ купитись не мав: %+v", got)
	}
	if got.Broker != "inzhur" {
		t.Errorf("брокер %q, чекали inzhur", got.Broker)
	}
	if got.Cost.Amount != "5000.00" {
		t.Errorf("вартість %s, чекали 5000.00 (5×995 + 25 комісії)", got.Cost.Amount)
	}
	if got.Have.Amount != "1000.00" {
		t.Errorf("на рахунку %s, чекали 1000.00", got.Have.Amount)
	}
	if got.Short.Amount != "4000.00" {
		t.Errorf("нестача %s, чекали 4000.00", got.Short.Amount)
	}
}

func TestLotCheckEnoughWhenFunded(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if _, err := st.AddDeposit(context.Background(), store.Deposit{
		Date: "2026-07-01", Amount: 5000_00, Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}

	got := checkOf(t, srv.URL+"/api/lots/check", lotBody)
	if !got.Enough || got.Short.Amount != "0.00" {
		t.Fatalf("рівно на лот мало вистачити: %+v", got)
	}
}

// /check відповідає на питання і НЕ пише: інакше перевірка «чи можу
// купити» сама створювала б покупку.
func TestLotCheckWritesNothing(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)

	checkOf(t, srv.URL+"/api/lots/check", lotBody)

	for _, path := range []string{"/api/lots", "/api/deposits"} {
		resp, body := do(t, "GET", srv.URL+path, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d %s", path, resp.StatusCode, body)
		}
		var rows []json.RawMessage
		if err := json.Unmarshal([]byte(body), &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 0 {
			t.Errorf("%s: після /check зʼявилось %d рядк(ів)", path, len(rows))
		}
	}
}

// Нестача від ВІД'ЄМНОГО балансу мусить включати сам борг до копійки.
//
// Це пін на math.Round у brokerBalanceMinor: попередній вираз
// int64(v*100+0.5) обрізав до нуля й на −1 000.00 давав −999.99, тобто
// нестачу на копійку меншу. Після «поповнити рівно на нестачу» рахунок
// ставав би −0.01 замість 0 — рівно те, що ця фіча має усувати.
func TestLotCheckFromNegativeBalance(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	// Лот без жодного поповнення: рахунок inzhur іде в мінус на 1 000 ₴.
	if _, err := st.AddLot(context.Background(), domain.Lot{
		ISIN: "UA4000227748", Qty: 1, PricePerBond: money.New(1000_00, money.UAH),
		BuyDate: "2026-07-01", Channel: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}

	got := checkOf(t, srv.URL+"/api/lots/check",
		`{"isin":"UA4000227748","qty":1,"price_per_bond":"1000.00",`+
			`"buy_date":"2026-07-01","channel":"inzhur"}`)
	if got.Have.Amount != "-1000.00" {
		t.Errorf("баланс %s, чекали -1000.00", got.Have.Amount)
	}
	if got.Short.Amount != "2000.00" {
		t.Errorf("нестача %s, чекали 2000.00 (борг 1 000 + вартість 1 000)", got.Short.Amount)
	}
}

// Наскрізний сценарій самої вимоги: не вистачає → поповнюємо рівно на
// нестачу → купуємо → на рахунку РІВНО нуль.
func TestTopUpThenBuyLandsAtZero(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	// Стартуємо з боргу, щоб перевірити найгірший випадок разом.
	if _, err := st.AddLot(context.Background(), domain.Lot{
		ISIN: "UA4000227748", Qty: 1, PricePerBond: money.New(1000_00, money.UAH),
		BuyDate: "2026-07-01", Channel: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}

	chk := checkOf(t, srv.URL+"/api/lots/check", lotBody)
	// Фронтенд саме так і робить: рядок суми йде в тіло поповнення
	// ДОСЛІВНО, без жодної арифметики на своєму боці.
	dep, body := do(t, "POST", srv.URL+"/api/deposits",
		`{"date":"2026-07-01","amount":"`+chk.Short.Amount+`","currency":"`+
			chk.Short.Currency+`","broker":"`+chk.Broker+`","note":"автопоповнення: купівля ОВДП"}`)
	if dep.StatusCode != http.StatusCreated {
		t.Fatalf("поповнення: %d %s", dep.StatusCode, body)
	}
	lot, body := do(t, "POST", srv.URL+"/api/lots", lotBody)
	if lot.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", lot.StatusCode, body)
	}

	resp, body := do(t, "GET", srv.URL+"/api/summary", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("summary: %d %s", resp.StatusCode, body)
	}
	var sum struct {
		Brokers map[string]map[string]float64 `json:"brokers"`
	}
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatal(err)
	}
	// Строго нуль, без допуску: гаманець рахується цілими мінорними
	// одиницями аж до byBroker(), тож «майже нуль» тут означав би баг.
	if got := sum.Brokers["inzhur"][money.UAH]; got != 0 {
		t.Errorf("після автопоповнення рахунок inzhur = %v, мав бути рівно 0", got)
	}
}

// Невідомий папір без явної валюти — 400 на тій самій гілці lotFromReq,
// що й у справжнього запису: /check і POST мусять відкидати те саме.
func TestLotCheckRejectsUnknownBond(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	bad := `{"isin":"UA0000000000","qty":1,"price_per_bond":"100.00","buy_date":"2026-07-01"}`
	for _, path := range []string{"/api/lots/check", "/api/lots"} {
		resp, body := do(t, "POST", srv.URL+path, bad)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d %s, чекали 400", path, resp.StatusCode, body)
		}
	}
}

func TestTermDepositCheck(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if _, err := st.AddDeposit(context.Background(), store.Deposit{
		Date: "2026-07-01", Amount: 30_000_00, Currency: money.UAH, Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}

	got := checkOf(t, srv.URL+"/api/term-deposits/check",
		`{"bank":"mono","currency":"UAH","principal":"100000.00","rate_pct":"16.5",`+
			`"open_date":"2026-07-01","maturity_date":"2027-07-01","payout":"end"}`)
	if got.Enough {
		t.Fatalf("на 30 000 ₴ вклад на 100 000 ₴ не відкривається: %+v", got)
	}
	if got.Broker != "mono" || got.Short.Amount != "70000.00" {
		t.Errorf("чекали mono / 70000.00, маємо %q / %s", got.Broker, got.Short.Amount)
	}
}

// Поповнення вкладу: банк і валюту /check бере з САМОГО вкладу — так
// само, як їх бере запис.
func TestDepositTopupCheckUsesDepositBankAndCurrency(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	ctx := context.Background()
	id, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "mono", Currency: money.USD, Principal: 1000_00, RateBP: 500,
		OpenDate: "2026-07-01", MaturityDate: "2027-07-01",
		Payout: domain.PayoutEnd, Replenishable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2026-07-01", Amount: 200_00, Currency: money.USD, Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}

	path := srv.URL + "/api/term-deposits/" + strconv.FormatInt(id, 10) + "/topups/check"
	got := checkOf(t, path, `{"date":"2026-07-02","amount":"500.00"}`)
	if got.Cost.Currency != money.USD {
		t.Errorf("валюта %s, чекали USD (з вкладу, не з запиту)", got.Cost.Currency)
	}
	// Тіло вкладу вже списало 1 000 $, поповнень було на 200 $ → −800 $.
	if got.Have.Amount != "-800.00" || got.Short.Amount != "1300.00" {
		t.Errorf("маємо %s / нестача %s, чекали -800.00 / 1300.00",
			got.Have.Amount, got.Short.Amount)
	}
}

func TestDepositTopupCheckUnknownDeposit(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	resp, body := do(t, "POST", srv.URL+"/api/term-deposits/999/topups/check",
		`{"date":"2026-07-02","amount":"500.00"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("%d %s, чекали 404", resp.StatusCode, body)
	}
}

// /check і запис ділять topupFromReq, тож відкидати нульову суму мусять
// однаково. Інакше форма пройшла б перевірку й упала на записі.
func TestTopupCheckRejectsSameAsWrite(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	id, err := st.AddTermDeposit(context.Background(), domain.Deposit{
		Bank: "mono", Currency: money.UAH, Principal: 1000_00, RateBP: 500,
		OpenDate: "2026-07-01", MaturityDate: "2027-07-01", Payout: domain.PayoutEnd,
	})
	if err != nil {
		t.Fatal(err)
	}
	base := srv.URL + "/api/term-deposits/" + strconv.FormatInt(id, 10) + "/topups"
	for _, path := range []string{base + "/check", base} {
		resp, body := do(t, "POST", path, `{"date":"2026-07-02","amount":"0"}`)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d %s, чекали 400", path, resp.StatusCode, body)
		}
	}
}
