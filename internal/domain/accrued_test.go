package domain

import "testing"

// Період 2026-06-01 .. 2026-12-01 (183 дні), купон 80.00.
// НКД на 2026-07-01 (30 днів): 8000 × 30/183 = 1311.47... -> 1311.
func TestEstimateAccrued(t *testing.T) {
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-06-01", Type: PayCoupon, PerBond: uah(8000)},
		{ISIN: "UA1", PayDate: "2026-12-01", Type: PayCoupon, PerBond: uah(8000)},
	}
	got, err := EstimateAccrued(pays, "UA1", "2026-07-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount() != 1311 {
		t.Errorf("НКД = %d, хочемо 1311", got.Amount())
	}
}

func TestEstimateAccruedOnCouponDateIsZero(t *testing.T) {
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-06-01", Type: PayCoupon, PerBond: uah(8000)},
		{ISIN: "UA1", PayDate: "2026-12-01", Type: PayCoupon, PerBond: uah(8000)},
	}
	got, err := EstimateAccrued(pays, "UA1", "2026-06-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount() != 0 {
		t.Errorf("НКД у день купона має бути 0, маємо %d", got.Amount())
	}
}

func TestEstimateAccruedOutsidePeriods(t *testing.T) {
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-06-01", Type: PayCoupon, PerBond: uah(8000)},
		{ISIN: "UA1", PayDate: "2026-12-01", Type: PayCoupon, PerBond: uah(8000)},
	}
	got, err := EstimateAccrued(pays, "UA1", "2027-01-15") // після останнього купона
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount() != 0 {
		t.Errorf("поза періодами НКД = 0, маємо %d", got.Amount())
	}
}
