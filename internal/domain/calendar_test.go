package domain

import (
	"testing"

	money "github.com/Rhymond/go-money"
)

func uah(minor int64) *money.Money { return money.New(minor, money.UAH) }

// Портфель: лот 10 паперів, продано 4 у середині періоду.
// Виплата ДО продажу і В ДЕНЬ продажу — на повні 10; ПІСЛЯ — на 6.
func TestFuturePaymentsRespectsSales(t *testing.T) {
	lot := Lot{ID: 1, ISIN: "UA1", Qty: 10, PricePerBond: uah(100000), BuyDate: "2026-01-10"}
	sale := Sale{ID: 1, LotID: 1, SaleDate: "2026-06-15", Qty: 4, CleanPerBond: uah(100500), Accrued: uah(0)}
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-06-01", Type: PayCoupon, PerBond: uah(8000)},
		{ISIN: "UA1", PayDate: "2026-06-15", Type: PayCoupon, PerBond: uah(8000)},
		{ISIN: "UA1", PayDate: "2026-12-01", Type: PayCoupon, PerBond: uah(8000)},
		{ISIN: "UA1", PayDate: "2027-06-01", Type: PayRedemption, PerBond: uah(100000)},
	}
	items, err := FuturePayments(pays, []Lot{lot}, []Sale{sale}, "2026-01-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("очікували 4 виплати, маємо %d", len(items))
	}
	want := []int64{80000, 80000, 48000, 600000} // 10×8000, 10×8000 (день продажу), 6×8000, 6×100000
	for i, w := range want {
		if items[i].Amount.Amount() != w {
			t.Errorf("виплата %d (%s): %d, хочемо %d", i, items[i].Date, items[i].Amount.Amount(), w)
		}
	}
}

func TestFuturePaymentsAggregatesLots(t *testing.T) {
	lots := []Lot{
		{ID: 1, ISIN: "UA1", Qty: 3, PricePerBond: uah(100000), BuyDate: "2026-01-10"},
		{ID: 2, ISIN: "UA1", Qty: 2, PricePerBond: uah(99000), BuyDate: "2026-02-10"},
	}
	pays := []Payment{{ISIN: "UA1", PayDate: "2026-12-01", Type: PayCoupon, PerBond: uah(8000)}}
	items, err := FuturePayments(pays, lots, nil, "2026-06-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Amount.Amount() != 40000 {
		t.Fatalf("очікували одну агреговану виплату 40000, маємо %+v", items)
	}
}

func TestFuturePaymentsSkipsPast(t *testing.T) {
	lot := Lot{ID: 1, ISIN: "UA1", Qty: 1, PricePerBond: uah(100000), BuyDate: "2025-01-10"}
	pays := []Payment{{ISIN: "UA1", PayDate: "2025-06-01", Type: PayCoupon, PerBond: uah(8000)}}
	items, err := FuturePayments(pays, []Lot{lot}, nil, "2026-01-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("минулі виплати мають відсікатись, маємо %+v", items)
	}
}

func TestLadder(t *testing.T) {
	bonds := map[string]Bond{
		"UA1": {ISIN: "UA1", Nominal: uah(100000), Maturity: "2027-03-01"},
		"UA2": {ISIN: "UA2", Nominal: uah(100000), Maturity: "2027-09-01"},
		"US1": {ISIN: "US1", Nominal: money.New(100000, money.USD), Maturity: "2028-01-15"},
		"OLD": {ISIN: "OLD", Nominal: uah(100000), Maturity: "2025-01-01"},
	}
	lots := []Lot{
		{ID: 1, ISIN: "UA1", Qty: 5, PricePerBond: uah(99000), BuyDate: "2026-01-01"},
		{ID: 2, ISIN: "UA2", Qty: 3, PricePerBond: uah(98000), BuyDate: "2026-01-01"},
		{ID: 3, ISIN: "US1", Qty: 2, PricePerBond: money.New(99500, money.USD), BuyDate: "2026-01-01"},
		{ID: 4, ISIN: "OLD", Qty: 9, PricePerBond: uah(99000), BuyDate: "2024-01-01"},
	}
	sales := []Sale{{ID: 1, LotID: 1, SaleDate: "2026-05-01", Qty: 2, CleanPerBond: uah(100000), Accrued: uah(0)}}
	got := Ladder(bonds, lots, sales, "2026-07-15")
	// 2027/UAH: (5-2)×1000 + 3×1000 = 600000 minor; 2028/USD: 2×1000$
	if len(got) != 2 {
		t.Fatalf("очікували 2 рядки драбини, маємо %+v", got)
	}
	if got[0].Year != 2027 || got[0].Currency != "UAH" || got[0].Nominal != 600000 {
		t.Errorf("2027/UAH: %+v", got[0])
	}
	if got[1].Year != 2028 || got[1].Currency != "USD" || got[1].Nominal != 200000 {
		t.Errorf("2028/USD: %+v", got[1])
	}
}
