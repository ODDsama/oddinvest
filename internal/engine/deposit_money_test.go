package engine

import (
	"context"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Закритий вклад не стирає відсотків, які вже надійшли до закриття.
//
// Вклад із помісячною виплатою жив пів року, щомісяця кладучи відсотки на
// рахунок банку, — і щойно людина записувала дострокове розірвання,
// гаманець, рух грошей і XIRR пропускали ВЕСЬ графік заднім числом: баланс
// банку падав на суму всіх виплат, а XIRR показував збиток. Звірка
// гаманців при цьому мовчала, бо обидва боки губили ті самі рядки.
func TestClosedDepositKeepsInterestPaidBeforeClosing(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	today := domain.NewDate(time.Now())
	open := today.AddMonths(-7)
	dep := domain.Deposit{
		Bank: "ПУМБ", Currency: money.UAH, Principal: 120_000_00,
		RateBP: 1500, OpenDate: open, MaturityDate: open.AddMonths(12),
		Payout: domain.PayoutMonthly, TaxBP: 2300,
	}
	id, err := st.AddTermDeposit(ctx, dep)
	if err != nil {
		t.Fatal(err)
	}
	e := New(st, testLogger())
	before, err := e.BuildState(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	bankBefore := before.Brokers["ПУМБ"][money.UAH].Minor()
	if bankBefore <= -120_000_00 {
		t.Fatalf("до закриття на рахунку %d — виплат відсотків не видно взагалі", bankBefore)
	}

	// Закриття сьогодні: тіло повернулось, відсотки до того вже лежать.
	dep.ID, dep.ClosedDate, dep.ClosedAmount = id, today, 120_000_00
	if err := st.UpdateTermDeposit(ctx, dep); err != nil {
		t.Fatal(err)
	}
	after, err := e.BuildState(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	bankAfter := after.Brokers["ПУМБ"][money.UAH].Minor()
	// До закриття: −тіло + виплати. Після: −тіло + виплати + тіло. Різниця —
	// рівно повернене тіло, і виплати нікуди не зникли.
	if got := bankAfter - bankBefore; got != 120_000_00 {
		t.Errorf("закриття змінило баланс на %d коп., чекали рівно тіло 12000000 — "+
			"виплачені раніше відсотки зникли заднім числом", got)
	}

	events, err := e.CashEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var interest int
	for _, ev := range events {
		if ev.Kind == FlowIncome {
			interest++
		}
	}
	if interest < 6 {
		t.Errorf("у русі грошей %d виплат відсотків, чекали щонайменше 6 до закриття", interest)
	}
}

// Те саме в домені: XIRR бачить виплати до закриття.
func TestClosedDepositFlowsKeepPaidInterest(t *testing.T) {
	open := domain.Date("2026-01-15")
	d := domain.Deposit{
		Bank: "ПУМБ", Currency: money.UAH, Principal: 120_000_00,
		RateBP: 1500, OpenDate: open, MaturityDate: open.AddMonths(12),
		Payout: domain.PayoutMonthly, TaxBP: 2300,
		ClosedDate: "2026-07-20", ClosedAmount: 120_000_00,
	}
	paid := d.PaidBeforeClose()
	if len(paid) != 6 {
		t.Fatalf("до 20 липня мало надійти 6 виплат, маємо %d", len(paid))
	}
	for _, cf := range paid {
		if !cf.Date.Before(d.ClosedDate) || cf.Type != domain.PayCoupon {
			t.Errorf("зайвий рядок %+v", cf)
		}
	}
}
