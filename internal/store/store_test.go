package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLotsRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, err := s.AddLot(ctx, domain.Lot{
		ISIN: "UA4000227748", Qty: 5,
		PricePerBond: money.New(99500, money.UAH),
		BuyDate:      "2026-07-01", Channel: "Дія",
	})
	if err != nil {
		t.Fatal(err)
	}
	lots, err := s.ListLots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(lots) != 1 || lots[0].ID != id || lots[0].PricePerBond.Amount() != 99500 ||
		lots[0].PricePerBond.Currency().Code != "UAH" || lots[0].BuyDate != "2026-07-01" {
		t.Fatalf("round-trip: %+v", lots)
	}
}

func TestSaleValidation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, _ := s.AddLot(ctx, domain.Lot{ISIN: "UA1", Qty: 5,
		PricePerBond: money.New(99500, money.UAH), BuyDate: "2026-07-01"})

	// продаж більше залишку
	_, err := s.AddSale(ctx, domain.Sale{LotID: id, SaleDate: "2026-08-01", Qty: 6,
		CleanPerBond: money.New(100000, money.UAH)})
	if err == nil {
		t.Fatal("очікували помилку oversell")
	}
	// валютна невідповідність
	_, err = s.AddSale(ctx, domain.Sale{LotID: id, SaleDate: "2026-08-01", Qty: 1,
		CleanPerBond: money.New(100000, money.USD)})
	if err == nil {
		t.Fatal("очікували помилку валют")
	}
	// нормальний продаж, двічі по 3 — друга має впасти
	if _, err = s.AddSale(ctx, domain.Sale{LotID: id, SaleDate: "2026-08-01", Qty: 3,
		CleanPerBond: money.New(100000, money.UAH), Accrued: money.New(500, money.UAH)}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddSale(ctx, domain.Sale{LotID: id, SaleDate: "2026-09-01", Qty: 3,
		CleanPerBond: money.New(100000, money.UAH)}); err == nil {
		t.Fatal("сумарний oversell має падати")
	}
}

func TestDirectoryReplaceAndSearch(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	secs := []nbu.Security{
		{Bond: domain.Bond{ISIN: "UA1", Nominal: money.New(100000, money.UAH),
			RateBP: 1655, Maturity: "2027-03-17", Descr: "гривневі"},
			Payments: []domain.Payment{{ISIN: "UA1", PayDate: "2026-09-16",
				Type: domain.PayCoupon, PerBond: money.New(8275, money.UAH)}}},
		{Bond: domain.Bond{ISIN: "US1", Nominal: money.New(100000, money.USD),
			RateBP: 324, Maturity: "2027-09-17", Descr: "доларові"}},
	}
	if err := s.ReplaceDirectory(ctx, secs, time.Now()); err != nil {
		t.Fatal(err)
	}
	found, err := s.SearchBonds(ctx, "", "USD", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ISIN != "US1" {
		t.Fatalf("пошук по валюті: %+v", found)
	}
	pays, err := s.PaymentsFor(ctx, []string{"UA1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pays) != 1 || pays[0].PerBond.Amount() != 8275 {
		t.Fatalf("payments: %+v", pays)
	}
	// повторний replace не дублює
	if err := s.ReplaceDirectory(ctx, secs, time.Now()); err != nil {
		t.Fatal(err)
	}
	all, _ := s.SearchBonds(ctx, "", "", "", "", 10)
	if len(all) != 2 {
		t.Fatalf("після повторного replace: %d паперів", len(all))
	}
}

func TestSettingsRatesSnapshotsStatuses(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SetSetting(ctx, "monthly_target_uah", "500000"); err != nil {
		t.Fatal(err)
	}
	v, _ := s.GetSetting(ctx, "monthly_target_uah")
	if v != "500000" {
		t.Fatalf("setting: %s", v)
	}
	if err := s.SaveRate(ctx, "USD", 441234, "2026-07-15"); err != nil {
		t.Fatal(err)
	}
	r, _ := s.LatestRate(ctx, "USD")
	if r != 441234 {
		t.Fatalf("rate: %d", r)
	}
	if err := s.SaveSnapshot(ctx, "2026-07-15", 100, 200, 5000, 0, 500000); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSnapshot(ctx, "2026-07-15", 150, 250, 5100, 10, 600000); err != nil {
		t.Fatal(err) // upsert того ж дня
	}
	if err := s.SetPaymentStatus(ctx, "UA1", "2026-09-16", "received"); err != nil {
		t.Fatal(err)
	}
	st, _ := s.PaymentStatuses(ctx)
	if st["UA1|2026-09-16"] != "received" {
		t.Fatalf("status: %+v", st)
	}
	if err := s.SetPaymentStatus(ctx, "UA1", "2026-09-16", "spent"); err == nil {
		t.Fatal("невалідний статус має падати")
	}
}
