package domain

import (
	"testing"

	money "github.com/Rhymond/go-money"
)

// Купили 10 по 990.00, продали 4 по 1005.00 чистими + НКД 32.00 сумарно.
// За володіння отримали один купон 80.00/папір.
// Результат = (4×1005 + 32) − 4×990 + 4×80 = 4052 + 32... перевіримо в копійках.
func TestRealizedResult(t *testing.T) {
	lot := Lot{ID: 1, ISIN: "UA1", Qty: 10, PricePerBond: uah(99000), BuyDate: "2026-01-10"}
	sale := Sale{ID: 1, LotID: 1, SaleDate: "2026-07-01", Qty: 4, CleanPerBond: uah(100500), Accrued: uah(3200)}
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-06-01", Type: PayCoupon, PerBond: uah(8000)},
		{ISIN: "UA1", PayDate: "2026-12-01", Type: PayCoupon, PerBond: uah(8000)}, // після продажу — не рахується
		{ISIN: "UA1", PayDate: "2026-01-10", Type: PayCoupon, PerBond: uah(8000)}, // у день купівлі — попередньому власнику
	}
	res, err := RealizedResult(lot, sale, pays)
	if err != nil {
		t.Fatal(err)
	}
	// (4×100500 + 3200) − 4×99000 + 4×8000 = 402000+3200−396000+32000 = 41200
	if res.Amount() != 41200 {
		t.Errorf("результат = %d, хочемо 41200", res.Amount())
	}
}

func TestRealizedResultCouponOnSaleDayCountsToSeller(t *testing.T) {
	lot := Lot{ID: 1, ISIN: "UA1", Qty: 5, PricePerBond: uah(100000), BuyDate: "2026-01-10"}
	sale := Sale{ID: 1, LotID: 1, SaleDate: "2026-06-01", Qty: 5, CleanPerBond: uah(100000), Accrued: uah(0)}
	pays := []Payment{{ISIN: "UA1", PayDate: "2026-06-01", Type: PayCoupon, PerBond: uah(8000)}}
	res, err := RealizedResult(lot, sale, pays)
	if err != nil {
		t.Fatal(err)
	}
	if res.Amount() != 40000 {
		t.Errorf("купон у день продажу має належати продавцю: %d", res.Amount())
	}
}

func TestRealizedResultRejectsOversell(t *testing.T) {
	lot := Lot{ID: 1, ISIN: "UA1", Qty: 2, PricePerBond: uah(100000), BuyDate: "2026-01-10"}
	sale := Sale{ID: 1, LotID: 1, SaleDate: "2026-06-01", Qty: 3, CleanPerBond: uah(100000), Accrued: uah(0)}
	if _, err := RealizedResult(lot, sale, nil); err == nil {
		t.Error("очікували помилку на продажу більшого за лот")
	}
}

func TestRealizedResultCurrencyMismatchFails(t *testing.T) {
	lot := Lot{ID: 1, ISIN: "US1", Qty: 1, PricePerBond: money.New(99000, money.USD), BuyDate: "2026-01-10"}
	sale := Sale{ID: 1, LotID: 1, SaleDate: "2026-06-01", Qty: 1, CleanPerBond: uah(100000), Accrued: uah(0)}
	if _, err := RealizedResult(lot, sale, nil); err == nil {
		t.Error("змішування валют має падати помилкою, а не тихо рахуватись")
	}
}
