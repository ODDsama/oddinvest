package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
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

// Вклад подушки чи цілі не живить прогноз портфеля.
//
// Його тіло збирач уже виводить зі старту проєкції (гроші подушки — не
// купівельна спроможність), а відсотки й повернення тіла доливались у
// рукави як звичайний дохід: 200 тис. подушки під 15% додавали до прогнозу
// двісті тисяч і відсотки, що самі реінвестувались, — гроші нізвідки.
// Тож вклад подушки мусить не зрушити прогноз ні на копійку.
func TestEarmarkedDepositStaysOutOfProjection(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	seed(t, st)
	today := domain.NewDate(time.Now())
	if err := st.SetSetting(ctx, "goal_amount_uah", "2000000"); err != nil {
		t.Fatal(err)
	}
	// Звичайний вклад, щоб прогноз мав що показувати: на порожньому
	// портфелі він нульовий за будь-яких потоків, і тест не перевіряв би
	// нічого.
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: today, Broker: "Приват", Amount: 100_000_00, Currency: money.UAH,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "Приват", Currency: money.UAH, Principal: 100_000_00, RateBP: 1400,
		OpenDate: today, MaturityDate: today.AddMonths(12),
		Payout: domain.PayoutEnd, TaxBP: domain.TaxBPByLaw,
	}); err != nil {
		t.Fatal(err)
	}
	e := New(st, testLogger())
	before, err := e.BuildState(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Projection) == 0 || before.Projection[0].WithReinvest.Minor() == 0 {
		t.Fatal("прогнозу немає — тест нічого не перевіряє")
	}
	// Гроші на вклад приходять поповненням рахунку банку — так, як це
	// записує людина. Без нього рахунок пішов би в мінус на тіло вкладу.
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: today, Broker: "ПУМБ", Amount: 200_000_00, Currency: money.UAH,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: money.UAH, Principal: 200_000_00, RateBP: 1500,
		OpenDate: today, MaturityDate: today.AddMonths(8),
		Payout: domain.PayoutEnd, TaxBP: domain.TaxBPByLaw, IsReserve: true,
	}); err != nil {
		t.Fatal(err)
	}
	after, err := e.BuildState(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for i := range before.Projection {
		b, a := before.Projection[i], after.Projection[i]
		if b.WithReinvest.Minor() != a.WithReinvest.Minor() {
			t.Errorf("горизонт %d р.: прогноз %.2f → %.2f від вкладу подушки",
				b.Years, b.WithReinvest.Major(), a.WithReinvest.Major())
		}
	}
}

// Погашений вклад подушки лишається подушкою, а пролонгація бере гроші з
// неї, а не з рахунку банку.
//
// Доти тіло й відсотки такого вкладу, щойно надійшли, лягали на рахунок
// банку звичайною готівкою: подушка меншала на розмір вкладу, а розкладка
// пропонувала вкласти ці гроші в папери.
func TestMaturedReserveDepositStaysInReserve(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	today := domain.NewDate(time.Now())
	open := today.AddMonths(-12).AddDays(-10) // погашено 10 днів тому
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: open, Broker: "ПУМБ", Amount: 100_000_00, Currency: money.UAH,
	}); err != nil {
		t.Fatal(err)
	}
	dep := domain.Deposit{
		Bank: "ПУМБ", Currency: money.UAH, Principal: 100_000_00, RateBP: 1200,
		OpenDate: open, MaturityDate: open.AddMonths(12),
		Payout: domain.PayoutEnd, TaxBP: domain.TaxBPByLaw, IsReserve: true,
	}
	if _, err := st.AddTermDeposit(ctx, dep); err != nil {
		t.Fatal(err)
	}
	e := New(st, testLogger())
	doc, err := e.BuildState(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var maturity int64
	for _, cf := range domain.DepositSchedule(dep, "1970-01-01") {
		maturity += cf.Amount.Amount()
	}
	if got := doc.Brokers["ПУМБ"][money.UAH].Minor(); got != 0 {
		t.Errorf("на рахунку ПУМБ %d — гроші погашеного вкладу подушки стали вільною готівкою", got)
	}
	if got := doc.ReserveUAH.Minor(); got != maturity {
		t.Errorf("подушка %d, чекали %d (тіло + відсотки погашеного вкладу)", got, maturity)
	}
	reconcile(t, e, doc)
	src, err := e.loadSources(ctx, today)
	if err != nil {
		t.Fatal(err)
	}
	var task bool
	for _, x := range buildTasks(doc, nil, src, today) {
		if strings.HasPrefix(x.ID, "earmark-matured:reserve:ПУМБ") {
			task = true
		}
	}
	if !task {
		t.Error("погашений вклад подушки мав дати задачу «перевклади або лиши»")
	}

	// Пролонгація: новий вклад подушки в тому самому банку в день погашення
	// бере гроші з пулу — з рахунку не списується нічого.
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: money.UAH, Principal: maturity, RateBP: 1300,
		OpenDate: dep.MaturityDate, MaturityDate: dep.MaturityDate.AddMonths(12),
		Payout: domain.PayoutEnd, TaxBP: domain.TaxBPByLaw, IsReserve: true,
	}); err != nil {
		t.Fatal(err)
	}
	doc, err = e.BuildState(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Brokers["ПУМБ"][money.UAH].Minor(); got != 0 {
		t.Errorf("після пролонгації на рахунку %d — новий вклад мав узяти гроші з подушки", got)
	}
	if got := doc.ReserveUAH.Minor(); got != maturity {
		t.Errorf("подушка після пролонгації %d, чекали %d", got, maturity)
	}
	reconcile(t, e, doc)
}

// reconcile — подієвий рух грошей сходиться з гаманцем збирача.
func reconcile(t *testing.T, e *Engine, doc *state.Doc) {
	t.Helper()
	ev, err := e.CashEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sum := SummarizeCash(ev, "1970-01-01", domain.NewDate(time.Now()))
	if got, want := sum.ClosingUAH(), doc.AccountUAH.Minor(); got != want {
		t.Errorf("рух грошей %d ≠ рахунок %d — гаманці розійшлись", got, want)
	}
}

// Обіцяні дивіденди фонду в календарі й маршруті — після податку, який
// фонд утримує з кожної виплати.
//
// Виміряна дохідність (DividendYieldNet) і так нетто, а обіцяна бралась
// брутто: REIT на 100 тис. з обіцянкою 10% показував у календарі близько
// 833 ₴ на місяць там, де при 14% податку приходить близько 717. «Що
// купити» податок віднімав — і сусідні екрани розходились.
func TestPromisedFundDividendsAreNetOfTax(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	today := domain.NewDate(time.Now())
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: today.AddDays(-40), Fund: "Inzhur REIT", Kind: domain.FundBuy,
		Qty: 100, Amount: 100_000_00, Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	calendarFund := func(taxBP int64) int64 {
		t.Helper()
		funds, err := st.ListFunds(ctx)
		if err != nil || len(funds) != 1 {
			t.Fatalf("довідник фондів: %v %+v", err, funds)
		}
		f := funds[0]
		f.ExpectedYieldBP, f.ExpectedYieldCur, f.PayoutDay, f.IncomeTaxBP = 1000, money.UAH, 10, taxBP
		if err := st.RenameFund(ctx, f.ID, f); err != nil {
			t.Fatal(err)
		}
		doc, err := New(st, testLogger()).BuildState(ctx, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range doc.Calendar {
			if domain.IsFundISIN(p.ISIN) {
				return p.Amount.Minor()
			}
		}
		t.Fatal("фонду немає в календарі")
		return 0
	}
	gross, net := calendarFund(0), calendarFund(1400)
	if ratio := float64(net) / float64(gross); ratio < 0.855 || ratio > 0.865 {
		t.Errorf("виплата з податком 14%% — %d, без податку — %d: частка %.3f, чекали 0.86", net, gross, ratio)
	}
}
