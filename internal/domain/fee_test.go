package domain

import (
	"testing"

	money "github.com/Rhymond/go-money"
)

func TestApportion(t *testing.T) {
	// 100.00 грн, 3 з 10 -> рівно 30.00
	got, err := Apportion(money.New(10000, money.UAH), 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount() != 3000 {
		t.Fatalf("3/10 від 100.00: хочемо 3000, маємо %d", got.Amount())
	}
	// half-even: 1 з 3 від 100.00 = 33.3333 -> 3333
	got, _ = Apportion(money.New(10000, money.UAH), 1, 3)
	if got.Amount() != 3333 {
		t.Fatalf("1/3 від 100.00: хочемо 3333, маємо %d", got.Amount())
	}
	// nil / нуль / некоректні аргументи -> нуль
	got, _ = Apportion(nil, 1, 3)
	if !got.IsZero() {
		t.Fatalf("nil має давати нуль")
	}
	got, _ = Apportion(money.New(10000, money.UAH), 5, 0)
	if !got.IsZero() {
		t.Fatalf("whole=0 має давати нуль")
	}
}

func TestRealizedResultWithFee(t *testing.T) {
	// куплено 10 по 100.00 з комісією 20.00; продано 5 по 105.00 без НКД.
	lot := Lot{ID: 1, ISIN: "UA1", Qty: 10, PricePerBond: money.New(10000, money.UAH),
		Fee: money.New(2000, money.UAH), BuyDate: "2026-01-10"}
	s := Sale{ID: 1, LotID: 1, SaleDate: "2026-02-10", Qty: 5,
		CleanPerBond: money.New(10500, money.UAH), Accrued: money.New(0, money.UAH)}
	res, err := RealizedResult(lot, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 525.00 виручка − (500.00 собівартість + 10.00 комісії на 5/10) = 15.00
	if res.Amount() != 1500 {
		t.Fatalf("результат із комісією: хочемо 1500, маємо %d", res.Amount())
	}
}

func TestPositionsInvestedIncludesFee(t *testing.T) {
	bonds := map[string]Bond{"UA1": {ISIN: "UA1", Nominal: money.New(100000, money.UAH),
		RateBP: 1600, Maturity: "2027-01-01"}}
	lots := []Lot{{ID: 1, ISIN: "UA1", Qty: 10, PricePerBond: money.New(99000, money.UAH),
		Fee: money.New(5000, money.UAH), BuyDate: "2026-01-10"}}
	pos, err := Positions(bonds, nil, lots, nil, "2026-02-01")
	if err != nil {
		t.Fatal(err)
	}
	// 10 × 990.00 + 50.00 комісії = 9950.00
	if pos[0].Invested.Amount() != 995000 {
		t.Fatalf("вкладено з комісією: хочемо 995000, маємо %d", pos[0].Invested.Amount())
	}
}
