package engine

import (
	"context"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

// Календар показує ВСЮ нараховану ренту, а маршрут — саму лише решту.
//
// Це не дві відповіді на одне питання, а одна відповідь на два різні:
// тут «скільки портфель ЗАРОБЛЯЄ», там «що впаде на рахунок і що з цим
// робити». Арифметика спільна — SplitFundDividend, — і розходяться лише
// два поля одного розколу.
//
// Ціна помилки саме тут: із календаря живиться IncomeMonthlyNow, а з нього
// віха «Дохід покриває чверть життя», яка міряє заробіток проти витрат.
// Якби реінвестована рента з неї випадала, прогрес просідав би від самого
// перемикача в довіднику — хоча фонд заробляє те саме.
func TestCalendarKeepsGrossWhileRouteTakesLeftover(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	today := domain.NewDate(time.Now())

	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: today.AddDays(-60), Fund: "Inzhur REIT", Kind: domain.FundBuy,
		Qty: 779, Amount: 866_575, Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	funds, err := st.ListFunds(ctx)
	if err != nil || len(funds) != 1 {
		t.Fatalf("довідник фондів: %v %+v", err, funds)
	}
	f := funds[0]
	f.ExpectedYieldBP, f.ExpectedYieldCur, f.PayoutDay = 950, money.UAH, 10
	f.Kind = store.FundReinvesting
	if err := st.RenameFund(ctx, f.ID, f); err != nil {
		t.Fatal(err)
	}

	srv := New(st, testLogger())
	src, err := srv.loadSources(ctx, today)
	if err != nil {
		t.Fatal(err)
	}
	hold := domain.NewHoldings(src.lots, src.sales, src.bonds,
		src.fundOps, src.fundPrices, src.payoutDays(), today, nil)
	sch, err := buildSchedule(src, hold, today, today, 12)
	if err != nil {
		t.Fatal(err)
	}
	var calendar int64
	for _, cf := range sch.Cashflow {
		if domain.IsFundISIN(cf.ISIN) {
			calendar = cf.Amount.Amount()
			break
		}
	}
	if calendar == 0 {
		t.Fatal("фонд мав лишитись у календарі: дохід нікуди не дівається")
	}

	inc, err := routeIncome(src, today, 12)
	if err != nil {
		t.Fatal(err)
	}
	route := inc[store.BrokerCur{Broker: "inzhur", Currency: money.UAH}]
	if len(route) == 0 {
		t.Fatal("решта мала дійти до маршруту")
	}
	if route[0].Amount >= calendar {
		t.Fatalf("маршрут мав узяти лише решту: календар %d, маршрут %d",
			calendar, route[0].Amount)
	}
}
