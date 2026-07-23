package store

import (
	"context"
	"path/filepath"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

func TestBackupRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	lotID, err := st.AddLot(ctx, domain.Lot{
		ISIN: "UA4000239016", Qty: 3, PricePerBond: money.New(107715, money.UAH),
		Fee: money.New(0, money.UAH), BuyDate: "2026-07-16", Channel: "mono", Note: "перший",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddSale(ctx, domain.Sale{
		LotID: lotID, SaleDate: "2026-08-01", Qty: 1,
		CleanPerBond: money.New(101000, money.UAH), Accrued: money.New(500, money.UAH), Note: "частину продав",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDeposit(ctx, Deposit{Date: "2026-07-01", Amount: 500000, Currency: "UAH", Broker: "mono"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddConversion(ctx, Conversion{
		Date: "2026-07-03", FromCurrency: "UAH", FromAmount: 200000, ToCurrency: "USD", ToAmount: 4500, Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "monthly_target_uah", "5000"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPaymentStatus(ctx, "UA4000239016", "2026-07-22", "received"); err != nil {
		t.Fatal(err)
	}

	dump, err := st.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// катастрофа: чиста база
	st2, err := Open(filepath.Join(t.TempDir(), "t2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if err := st2.ImportAll(ctx, dump); err != nil {
		t.Fatalf("імпорт: %v", err)
	}

	// перевіряємо, що все на місці й звʼязок лот↔продаж уцілів
	lots, _ := st2.ListLots(ctx)
	if len(lots) != 1 || lots[0].ID != lotID || lots[0].Channel != "mono" {
		t.Fatalf("лоти не відновились: %+v", lots)
	}
	sales, _ := st2.ListSales(ctx)
	if len(sales) != 1 || sales[0].LotID != lotID {
		t.Fatalf("продаж або звʼязок з лотом втрачено: %+v", sales)
	}
	deps, _ := st2.ListDeposits(ctx)
	if len(deps) != 1 || deps[0].Broker != "mono" || deps[0].Amount != 500000 {
		t.Fatalf("поповнення не відновились: %+v", deps)
	}
	convs, _ := st2.ListConversions(ctx)
	if len(convs) != 1 || convs[0].Broker != "mono" {
		t.Fatalf("конвертації не відновились: %+v", convs)
	}
	if v, _ := st2.GetSetting(ctx, "monthly_target_uah"); v != "5000" {
		t.Fatalf("налаштування не відновились: %q", v)
	}
	ps, _ := st2.PaymentStatuses(ctx)
	if ps["UA4000239016|2026-07-22"] != "received" {
		t.Fatalf("статуси виплат не відновились: %+v", ps)
	}
}

func TestImportWrongSchema(t *testing.T) {
	st, _ := Open(filepath.Join(t.TempDir(), "t.db"))
	defer st.Close()
	if err := st.ImportAll(context.Background(), &Backup{Schema: 999}); err == nil {
		t.Fatal("несумісний бекап мав відхилитись")
	}
}

// Операції фондів мають переживати експорт-імпорт. Без цього бекап був
// неповним, і виявилось би це рівно тоді, коли відновлюватись уже нема з
// чого — тож перевіряємо саме круговий обіг, а не наявність поля.
func TestBackupRoundTripKeepsFundOps(t *testing.T) {
	ctx := context.Background()
	src, err := Open(filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	ops := []domain.FundOp{
		{Date: "2025-09-04", Fund: "Inzhur REIT", Kind: domain.FundBuy, Qty: 1738,
			Amount: 1738000, Currency: "UAH", Broker: "inzhur", Note: "виписка"},
		{Date: "2026-07-10", Fund: "Inzhur REIT", Kind: domain.FundDividend,
			Amount: 1899, Tax: 266, Currency: "UAH", Broker: "inzhur"},
		{Date: "2026-07-20", Fund: "Inzhur REIT", Kind: domain.FundSell, Qty: 72,
			Amount: 79830, Tax: 178, Currency: "UAH", Broker: "inzhur"},
	}
	for _, op := range ops {
		if _, err := src.AddFundOp(ctx, op); err != nil {
			t.Fatal(err)
		}
	}
	b, err := src.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.FundOps) != len(ops) {
		t.Fatalf("експорт мав узяти %d операцій, маємо %d", len(ops), len(b.FundOps))
	}

	dst, err := Open(filepath.Join(t.TempDir(), "dst.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.ImportAll(ctx, b); err != nil {
		t.Fatal(err)
	}
	got, err := dst.ListFundOps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(ops) {
		t.Fatalf("після відновлення %d операцій замість %d", len(got), len(ops))
	}
	// Податок і кількість — найлегше загубити при переносі, тож звіряємо їх.
	for i, op := range ops {
		if got[i].Fund != op.Fund || got[i].Kind != op.Kind || got[i].Qty != op.Qty ||
			got[i].Amount != op.Amount || got[i].Tax != op.Tax || got[i].Broker != op.Broker {
			t.Errorf("операція %d поїхала: %+v vs %+v", i, got[i], op)
		}
	}
}

func TestBackupRoundTripKeepsTermDeposits(t *testing.T) {
	ctx := context.Background()
	src, err := Open(filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	want := domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 10000000, RateBP: 1650,
		OpenDate: "2026-01-15", MaturityDate: "2027-01-15",
		Payout: domain.PayoutMonthly, Capitalized: false, TaxBP: 1950, Note: "піврічний",
	}
	if _, err := src.AddTermDeposit(ctx, want); err != nil {
		t.Fatal(err)
	}
	b, err := src.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.TermDeposits) != 1 {
		t.Fatalf("експорт мав узяти 1 вклад, маємо %d", len(b.TermDeposits))
	}

	dst, err := Open(filepath.Join(t.TempDir(), "dst.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.ImportAll(ctx, b); err != nil {
		t.Fatal(err)
	}
	got, err := dst.ListTermDeposits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("після відновлення %d вкладів замість 1", len(got))
	}
	g := got[0]
	// Ставка, податок, строк, режим виплат — усе, що визначає гроші.
	if g.Bank != want.Bank || g.Principal != want.Principal || g.RateBP != want.RateBP ||
		g.TaxBP != want.TaxBP || g.Payout != want.Payout ||
		g.OpenDate != want.OpenDate || g.MaturityDate != want.MaturityDate {
		t.Errorf("вклад поїхав: %+v vs %+v", g, want)
	}
}

func TestBackupRoundTripKeepsDepositTopups(t *testing.T) {
	ctx := context.Background()
	src, err := Open(filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	depID, err := src.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 10000000, RateBP: 1600,
		OpenDate: "2026-01-15", MaturityDate: "2027-01-15", Payout: domain.PayoutEnd, TaxBP: 1950,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"2026-02-15", "2026-03-15"} {
		if _, err := src.AddDepositTopup(ctx, domain.DepositTopup{
			DepositID: depID, Date: domain.Date(d), Amount: 10000000,
		}); err != nil {
			t.Fatal(err)
		}
	}
	b, err := src.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.DepositTopups) != 2 {
		t.Fatalf("експорт мав узяти 2 поповнення, маємо %d", len(b.DepositTopups))
	}

	dst, err := Open(filepath.Join(t.TempDir(), "dst.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if err := dst.ImportAll(ctx, b); err != nil {
		t.Fatal(err)
	}
	deps, err := dst.ListTermDeposits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 || len(deps[0].Topups) != 2 {
		t.Fatalf("після відновлення очікували вклад із 2 поповненнями, маємо %+v", deps)
	}
	// Накопичене тіло = 100к + 100к + 100к = 300к.
	if bal := deps[0].BalanceAt("2026-12-01"); bal != 30000000 {
		t.Errorf("накопичене тіло після відновлення: маємо %d, хочемо 30000000", bal)
	}
}
