package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

// testLogger — тихий журнал для сервера, зібраного повз testServer:
// частині тестів потрібен не HTTP, а самі методи.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
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
