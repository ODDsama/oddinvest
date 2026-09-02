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

// couponOn — купон, який FuturePayments платить на дату d: друга половина
// пари, з якою AccruedPaid мусить сходитись. Заводимо хелпер, бо перевірка
// «НКД проти СВОГО купона» повторюється, а без неї тест міряв би абсолютні
// числа замість співвідношення, заради якого функцію й писали.
func couponOn(t *testing.T, pays []Payment, lots []Lot, sales []Sale, d Date, isin string) int64 {
	t.Helper()
	cf, err := FuturePayments(pays, lots, sales, "1970-01-01")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cf {
		if c.Date == d && c.ISIN == isin && c.Type == PayCoupon {
			return c.Amount.Amount()
		}
	}
	t.Fatalf("купона %s %s немає в календарі", isin, d)
	return 0
}

// Бойова регресія, гілка couponStart: дорозміщений UA4000239081.
// Період 2026-02-25 .. 2026-08-26 (182 дні), купон 82.20.
// Лот 15.08 (171 день) -> 77.23 × 1; лот 18.08 (174 дні) -> 78.59 × 8.
// Разом 705.95 проти купона 739.80: зароблено 33.85, а картка показувала
// всі 739.80.
func TestAccruedPaidUA4000239081(t *testing.T) {
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-08-26", Type: PayCoupon, PerBond: uah(8220)},
		{ISIN: "UA1", PayDate: "2027-02-24", Type: PayCoupon, PerBond: uah(8220)},
	}
	lots := []Lot{
		{ID: 1, ISIN: "UA1", Qty: 1, PricePerBond: uah(108681), BuyDate: "2026-08-15"},
		{ID: 2, ISIN: "UA1", Qty: 8, PricePerBond: uah(108606), BuyDate: "2026-08-18"},
	}
	got, err := AccruedPaid(pays, lots, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Два лоти, один купон — ОДИН елемент: рядок НКД лягає 1:1 на рядок купона.
	if len(got) != 1 {
		t.Fatalf("очікували 1 елемент, маємо %d: %+v", len(got), got)
	}
	if got[0].Date != "2026-08-26" {
		t.Errorf("дата = %s, хочемо 2026-08-26", got[0].Date)
	}
	if got[0].Amount.Amount() != 70595 {
		t.Errorf("НКД = %d, хочемо 70595", got[0].Amount.Amount())
	}
	if c := couponOn(t, pays, lots, nil, "2026-08-26", "UA1"); c-got[0].Amount.Amount() != 3385 {
		t.Errorf("дохід = %d, хочемо 3385", c-got[0].Amount.Amount())
	}
}

// Бойова регресія, гілка prev: UA4000239016.
// Купон 21.01 у фікстурі НАВМИСНО — саме він робить період відомим напряму.
// Прибрати його означає піти в couponStart і отримати інше число: тест
// мусить казати, яку гілку він міряє.
func TestAccruedPaidUA4000239016(t *testing.T) {
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-01-21", Type: PayCoupon, PerBond: uah(7575)},
		{ISIN: "UA1", PayDate: "2026-07-22", Type: PayCoupon, PerBond: uah(7575)},
	}
	lots := []Lot{{ID: 1, ISIN: "UA1", Qty: 2, PricePerBond: uah(107715), BuyDate: "2026-07-16"}}
	got, err := AccruedPaid(pays, lots, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Date != "2026-07-22" {
		t.Fatalf("очікували один елемент на 2026-07-22, маємо %+v", got)
	}
	if got[0].Amount.Amount() != 14650 {
		t.Errorf("НКД = %d, хочемо 14650", got[0].Amount.Amount())
	}
	if c := couponOn(t, pays, lots, nil, "2026-07-22", "UA1"); c-got[0].Amount.Amount() != 500 {
		t.Errorf("дохід = %d, хочемо 500", c-got[0].Amount.Amount())
	}
}

// Грудневу купівлю гасить СІЧНЕВИЙ купон, тож і відрахування — наступного
// року, разом із доходом, який воно зменшує.
func TestAccruedPaidAttributesToCouponDate(t *testing.T) {
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-07-15", Type: PayCoupon, PerBond: uah(8000)},
		{ISIN: "UA1", PayDate: "2027-01-15", Type: PayCoupon, PerBond: uah(8000)},
	}
	lots := []Lot{{ID: 1, ISIN: "UA1", Qty: 1, PricePerBond: uah(108000), BuyDate: "2026-12-20"}}
	got, err := AccruedPaid(pays, lots, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Date != "2027-01-15" {
		t.Fatalf("НКД мав лягти на 2027-01-15, маємо %+v", got)
	}
}

// Інваріант, який замінює затискач: скільки б не лишалось до виплати, НКД
// строго менший за купон, що його повертає. Якщо couponStart колись
// зміниться так, що це перестане бути правдою, впаде саме цей тест.
func TestAccruedPaidNeverExceedsItsCoupon(t *testing.T) {
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-06-01", Type: PayCoupon, PerBond: uah(8000)},
		{ISIN: "UA1", PayDate: "2026-12-01", Type: PayCoupon, PerBond: uah(8000)},
	}
	for _, buy := range []Date{"2026-06-02", "2026-08-01", "2026-11-01", "2026-11-29", "2026-11-30"} {
		lots := []Lot{{ID: 1, ISIN: "UA1", Qty: 3, PricePerBond: uah(108000), BuyDate: buy}}
		got, err := AccruedPaid(pays, lots, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("купівля %s: очікували 1 елемент, маємо %d", buy, len(got))
		}
		coupon := couponOn(t, pays, lots, nil, "2026-12-01", "UA1")
		if got[0].Amount.Amount() >= coupon {
			t.Errorf("купівля %s: НКД %d >= купон %d", buy, got[0].Amount.Amount(), coupon)
		}
	}
}

// Проданий до першого купона лот НКД купоном не повертає — він повернувся
// в ціні продажу. Купона теж немає, тож віднімати нема від чого.
func TestAccruedPaidSoldBeforeFirstCoupon(t *testing.T) {
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-06-01", Type: PayCoupon, PerBond: uah(8000)},
		{ISIN: "UA1", PayDate: "2026-12-01", Type: PayCoupon, PerBond: uah(8000)},
	}
	lots := []Lot{{ID: 1, ISIN: "UA1", Qty: 5, PricePerBond: uah(108000), BuyDate: "2026-06-10"}}
	sales := []Sale{{ID: 1, LotID: 1, SaleDate: "2026-09-01", Qty: 5, CleanPerBond: uah(100000), Accrued: uah(4000)}}
	got, err := AccruedPaid(pays, lots, sales)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("очікували порожньо, маємо %+v", got)
	}
}

// Частковий продаж: НКД віднімається рівно на ту кількість, на яку
// FuturePayments платить купон. Одне число з однієї функції з обох боків.
func TestAccruedPaidPartialSaleNetsRemainderOnly(t *testing.T) {
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-06-01", Type: PayCoupon, PerBond: uah(8000)},
		{ISIN: "UA1", PayDate: "2026-12-01", Type: PayCoupon, PerBond: uah(8000)},
	}
	lots := []Lot{{ID: 1, ISIN: "UA1", Qty: 10, PricePerBond: uah(108000), BuyDate: "2026-06-10"}}
	sales := []Sale{{ID: 1, LotID: 1, SaleDate: "2026-09-01", Qty: 4, CleanPerBond: uah(100000), Accrued: uah(4000)}}
	got, err := AccruedPaid(pays, lots, sales)
	if err != nil {
		t.Fatal(err)
	}
	one, err := EstimateAccrued(pays, "UA1", "2026-06-10")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Amount.Amount() != one.Amount()*6 {
		t.Fatalf("НКД мав бути на 6 паперів (%d), маємо %+v", one.Amount()*6, got)
	}
}

// Купівля рівно в день купона: той купон належав продавцю (HolderQty), а
// НКД того дня рівно нульовий — період щойно почався. Тобто ні доходу, ні
// відрахування, і саме ця пара нулів мусить триматись разом: якби якорем
// став червневий купон, відрахування зʼявилось би без свого доходу.
func TestAccruedPaidCouponOnBuyDateNetsNothing(t *testing.T) {
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-06-01", Type: PayCoupon, PerBond: uah(8000)},
		{ISIN: "UA1", PayDate: "2026-12-01", Type: PayCoupon, PerBond: uah(8000)},
	}
	lots := []Lot{{ID: 1, ISIN: "UA1", Qty: 1, PricePerBond: uah(100000), BuyDate: "2026-06-01"}}
	got, err := AccruedPaid(pays, lots, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("НКД у день купона нульовий, очікували порожньо, маємо %+v", got)
	}
	cf, err := FuturePayments(pays, lots, nil, "1970-01-01")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cf {
		if c.Date == "2026-06-01" {
			t.Errorf("купон у день купівлі не мав стати доходом: %+v", c)
		}
	}
}

// Папір лише з погашенням: жодного купона — жодного елемента і, головне,
// НУЛЬОВА помилка. Це і є доказ, що «немає купонних виплат» з
// EstimateAccrued сюди не долітає.
func TestAccruedPaidNoCouponsForISIN(t *testing.T) {
	pays := []Payment{{ISIN: "UA1", PayDate: "2026-12-01", Type: PayRedemption, PerBond: uah(100000)}}
	lots := []Lot{{ID: 1, ISIN: "UA1", Qty: 1, PricePerBond: uah(95000), BuyDate: "2026-06-10"}}
	got, err := AccruedPaid(pays, lots, nil)
	if err != nil {
		t.Fatalf("помилки бути не мало: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("очікували порожньо, маємо %+v", got)
	}
}

// Щілина, названа в коментарі AccruedPaid, закріплена як НИНІШНЯ поведінка:
// єдиний купон у графіку — couponStart не має з чого відновити період, і
// відрахування немає. Тест стоїть тут, щоб закриття щілини було свідомим
// кроком із падаючим тестом, а не тихим дрейфом.
func TestAccruedPaidSingleCouponScheduleNetsNothing(t *testing.T) {
	pays := []Payment{{ISIN: "UA1", PayDate: "2026-12-01", Type: PayCoupon, PerBond: uah(8000)}}
	lots := []Lot{{ID: 1, ISIN: "UA1", Qty: 1, PricePerBond: uah(108000), BuyDate: "2026-06-10"}}
	got, err := AccruedPaid(pays, lots, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("поки що очікуємо порожньо, маємо %+v", got)
	}
}
