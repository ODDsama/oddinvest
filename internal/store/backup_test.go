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
