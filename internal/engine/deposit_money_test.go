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

// Податок із відсотків вкладу у звіті — подіями: кожна виплата за курсом
// свого дня й лише та, що вже надійшла.
//
// Доти відсотки вікна зводились одним числом за курсом кінця вікна (для
// поточного року — навіть майбутнього дня), і вже у вересні звіт за рік
// показував відсотки жовтня–грудня. Доларовий вклад при курсі, що ріс із
// 41 до 44, давав кілька відсотків вигаданого гривневого доходу.
func TestTaxReportDepositEventsAtTheirDates(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	open := domain.Date("2026-01-10")
	dep := domain.Deposit{
		Bank: "ПУМБ", Currency: money.USD, Principal: 10_000_00,
		RateBP: 600, OpenDate: open, MaturityDate: open.AddMonths(12),
		Payout: domain.PayoutMonthly, TaxBP: domain.TaxBPByLaw,
	}
	if _, err := st.AddTermDeposit(ctx, dep); err != nil {
		t.Fatal(err)
	}
	// Курс росте щомісяця: 41.00, 41.50, … — кожна виплата має свій.
	rateOn := map[domain.Date]int64{}
	for m := 0; m < 12; m++ {
		d := open.AddMonths(m)
		r := int64(410000 + m*5000)
		rateOn[d] = r
		if err := st.SaveRate(ctx, money.USD, r, d); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	e := New(st, testLogger())
	rep, err := e.TaxReport(ctx, 2026, "2026-01-01", "2026-12-31", now)
	if err != nil {
		t.Fatal(err)
	}
	var want, wantTax int64
	for _, ev := range domain.DepositInterestEvents(dep, "2026-01-01", "2026-12-31") {
		if ev.Date.After(domain.NewDate(now)) {
			continue // ще не надійшло
		}
		r := rateOn[domain.Date(string(ev.Date)[:8]+"10")]
		want += (ev.Gross*r + 5000) / 10000
		wantTax += (ev.Tax*r + 5000) / 10000
	}
	var got, gotTax int64
	for _, l := range rep.ByKind {
		if l.Kind == "deposit" {
			got, gotTax = l.GrossUAH.Minor(), l.TaxUAH.Minor()
		}
	}
	if diff := got - want; diff < -5 || diff > 5 {
		t.Errorf("брутто відсотків %d коп., чекали %d (курс кожного дня, лише до 20 червня)", got, want)
	}
	if diff := gotTax - wantTax; diff < -5 || diff > 5 {
		t.Errorf("податок %d коп., чекали %d", gotTax, wantTax)
	}
}
