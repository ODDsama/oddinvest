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

// Дорозміщений випуск: у довіднику НБУ лише МАЙБУТНІ виплати, тож
// попередньої в графіку немає — а купон на папері вже майже повний.
// Сітка 2026-08-26 .. 2027-02-24 (182 дні), обидва купони по 82.20:
// період почався 2026-02-25, і на 2026-08-21 наросло 8220 × 177/182.
// Це живий UA4000239081, на якому баг і побачили.
func TestEstimateAccruedFirstPeriodReopened(t *testing.T) {
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-08-26", Type: PayCoupon, PerBond: uah(8220)},
		{ISIN: "UA1", PayDate: "2027-02-24", Type: PayCoupon, PerBond: uah(8220)},
	}
	got, err := EstimateAccrued(pays, "UA1", "2026-08-21")
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount() != 7994 {
		t.Errorf("НКД = %d, хочемо 7994", got.Amount())
	}
}

// Скорочений перший купон означає скорочений період: половина суми —
// половина днів. Інакше папір, справді щойно випущений, отримав би НКД
// за півроку, якого не було.
func TestEstimateAccruedFirstPeriodShortCoupon(t *testing.T) {
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-08-26", Type: PayCoupon, PerBond: uah(4110)},
		{ISIN: "UA1", PayDate: "2027-02-24", Type: PayCoupon, PerBond: uah(8220)},
	}
	// 182 × 4110/8220 = 91 день, тобто період 2026-05-27 .. 2026-08-26.
	got, err := EstimateAccrued(pays, "UA1", "2026-06-26")
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount() != 1355 {
		t.Errorf("НКД = %d, хочемо 1355", got.Amount())
	}
}

// До початку відновленого періоду наростати ще нема чому.
func TestEstimateAccruedBeforeFirstPeriod(t *testing.T) {
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-08-26", Type: PayCoupon, PerBond: uah(8220)},
		{ISIN: "UA1", PayDate: "2027-02-24", Type: PayCoupon, PerBond: uah(8220)},
	}
	got, err := EstimateAccrued(pays, "UA1", "2026-02-20")
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount() != 0 {
		t.Errorf("до періоду НКД = 0, маємо %d", got.Amount())
	}
}

// Єдина виплата в графіку без попередньої: крок сітки нізвідки взяти,
// і вигадане число тут було б гірше за нуль. У живому реєстрі НБУ такого
// паперу немає жодного — це запобіжник, а не робочий шлях.
func TestEstimateAccruedSingleCouponNoGrid(t *testing.T) {
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-12-01", Type: PayCoupon, PerBond: uah(8000)},
	}
	got, err := EstimateAccrued(pays, "UA1", "2026-09-01")
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount() != 0 {
		t.Errorf("без сітки НКД = 0, маємо %d", got.Amount())
	}
}
