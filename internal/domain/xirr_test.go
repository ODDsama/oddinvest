package domain

import (
	"math"
	"testing"

	money "github.com/Rhymond/go-money"
)

func TestXIRRSimpleYear(t *testing.T) {
	// −1000.00 і рівно через рік +1160.00 -> 16.00%
	flows := []Flow{
		{Date: "2025-01-01", Amount: -100000},
		{Date: "2026-01-01", Amount: 116000},
	}
	r, err := XIRR(flows)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(r-0.16) > 1e-4 {
		t.Errorf("XIRR = %.6f, хочемо 0.16", r)
	}
}

func TestXIRRWithCoupon(t *testing.T) {
	// −1000, купон +80 через півроку, +1080 через рік: r ≈ 16.32%
	flows := []Flow{
		{Date: "2025-01-01", Amount: -100000},
		{Date: "2025-07-02", Amount: 8000}, // ~0.4986 року
		{Date: "2026-01-01", Amount: 108000},
	}
	r, err := XIRR(flows)
	if err != nil {
		t.Fatal(err)
	}
	// перевірка: NPV у знайденій ставці ~0
	if r < 0.16 || r > 0.17 {
		t.Errorf("XIRR = %.6f, очікували ~0.163", r)
	}
}

func TestXIRRErrors(t *testing.T) {
	if _, err := XIRR([]Flow{{Date: "2025-01-01", Amount: -1}}); err == nil {
		t.Error("один потік має падати")
	}
	if _, err := XIRR([]Flow{
		{Date: "2025-01-01", Amount: -1}, {Date: "2026-01-01", Amount: -2},
	}); err == nil {
		t.Error("потоки одного знаку мають падати")
	}
}

func TestPortfolioFlowsPerCurrency(t *testing.T) {
	bonds := map[string]Bond{
		"UA1": {ISIN: "UA1", Nominal: uah(100000), Maturity: "2027-03-17"},
		"US1": {ISIN: "US1", Nominal: money.New(100000, money.USD), Maturity: "2027-09-17"},
	}
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-06-01", Type: PayCoupon, PerBond: uah(8000)},
	}
	lots := []Lot{
		{ID: 1, ISIN: "UA1", Qty: 5, PricePerBond: uah(98750), BuyDate: "2026-01-10"},
		{ID: 2, ISIN: "US1", Qty: 2, PricePerBond: money.New(99500, money.USD), BuyDate: "2026-02-01"},
	}
	sales := []Sale{{ID: 1, LotID: 1, SaleDate: "2026-07-01", Qty: 2,
		CleanPerBond: uah(99100), Accrued: uah(500)}}

	flows, err := PortfolioFlows(bonds, pays, lots, sales, "UAH", "2026-07-15")
	if err != nil {
		t.Fatal(err)
	}
	// покупка UA1, купон 5×80, продаж 2×991+5, термінал 3×1000 — і ЖОДНОГО USD
	want := map[Date]int64{
		"2026-01-10": -493750,
		"2026-06-01": 40000,
		"2026-07-01": 198700,
		"2026-07-15": 300000,
	}
	if len(flows) != len(want) {
		t.Fatalf("потоки: %+v", flows)
	}
	for _, f := range flows {
		if want[f.Date] != f.Amount {
			t.Errorf("%s: %d, хочемо %d", f.Date, f.Amount, want[f.Date])
		}
	}
	r, err := XIRR(flows)
	if err != nil {
		t.Fatal(err)
	}
	if r < 0.10 || r > 0.30 {
		t.Errorf("XIRR гривневого портфеля неправдоподібний: %.4f", r)
	}
}
