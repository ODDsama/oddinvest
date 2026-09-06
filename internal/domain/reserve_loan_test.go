package domain

import "testing"

// Головна властивість, на якій стоїть уся фіча: залишок ЗАВЖДИ дорівнює
// тіло + нарахований відсоток − повернене. Саме через неї розрив подушки
// сам собою дорівнює боргу, коли ціль піднята на accrued.
func checkIdentity(t *testing.T, principal, owed, accrued int64, repays []LoanRepay) {
	t.Helper()
	var back int64
	for _, r := range repays {
		back += r.Amount
	}
	if want := principal + accrued - back; owed != want {
		t.Fatalf("тотожність не тримається: owed=%d, а тіло+відсоток−повернене=%d", owed, want)
	}
}

func TestReserveLoanNoTimeNoInterest(t *testing.T) {
	owed, accrued := ReserveLoanBalance(1_000_000, 1200, "2026-01-01", nil, "2026-01-01")
	if owed != 1_000_000 || accrued != 0 {
		t.Fatalf("узяв і того ж дня подивився: owed=%d accrued=%d, хочемо 1000000/0", owed, accrued)
	}
}

// Рік під 12% на 10 000 ₴ — 1 200 ₴. Число, яке власник може перевірити
// в голові, і саме тому воно тут зашите.
func TestReserveLoanYearAtTwelvePercent(t *testing.T) {
	owed, accrued := ReserveLoanBalance(1_000_000, 1200, "2026-01-01", nil, "2027-01-01")
	if accrued != 120_000 {
		t.Fatalf("відсоток за рік = %d, хочемо 120000 (1 200 ₴)", accrued)
	}
	if owed != 1_120_000 {
		t.Fatalf("залишок = %d, хочемо 1120000", owed)
	}
}

// Повернення посеред строку зменшує БАЗУ нарахування, а не лише залишок:
// друга половина року рахується вже на менші гроші.
func TestReserveLoanRepayShrinksAccrualBase(t *testing.T) {
	repays := []LoanRepay{{Date: "2026-07-02", Amount: 500_000}}
	owed, accrued := ReserveLoanBalance(1_000_000, 1200, "2026-01-01", repays, "2027-01-01")
	if accrued >= 120_000 {
		t.Fatalf("відсоток %d не менший за рік на повне тіло (120000) — база не зменшилась", accrued)
	}
	checkIdentity(t, 1_000_000, owed, accrued, repays)
	if owed <= 0 {
		t.Fatalf("половина повернення не мала закрити позику, owed=%d", owed)
	}
}

// Повернення НЕ по порядку в списку не має міняти число: сортування
// всередині і є одиницею нарахування.
func TestReserveLoanRepayOrderDoesNotMatter(t *testing.T) {
	asc := []LoanRepay{{Date: "2026-04-01", Amount: 200_000}, {Date: "2026-09-01", Amount: 300_000}}
	desc := []LoanRepay{{Date: "2026-09-01", Amount: 300_000}, {Date: "2026-04-01", Amount: 200_000}}
	o1, a1 := ReserveLoanBalance(1_000_000, 1200, "2026-01-01", asc, "2027-01-01")
	o2, a2 := ReserveLoanBalance(1_000_000, 1200, "2026-01-01", desc, "2027-01-01")
	if o1 != o2 || a1 != a2 {
		t.Fatalf("порядок рядків змінив число: %d/%d проти %d/%d", o1, a1, o2, a2)
	}
}

// Повернув тіло з відсотком — позика закрита, і надбавки до цілі більше
// немає. Це і є «ціль вертається до базової» — окремого механізму під неї
// не існує навмисно.
func TestReserveLoanFullRepayClosesIt(t *testing.T) {
	repays := []LoanRepay{{Date: "2027-01-01", Amount: 1_120_000}}
	owed, accrued := ReserveLoanBalance(1_000_000, 1200, "2026-01-01", repays, "2027-01-01")
	if owed != 0 || accrued != 0 {
		t.Fatalf("повернене тіло з відсотком мало закрити позику, маємо owed=%d accrued=%d", owed, accrued)
	}
}

// Переплата — не відʼємний борг: надлишок це поповнення понад ціль, а не
// заборгованість подушки перед людиною.
func TestReserveLoanOverpayDoesNotGoNegative(t *testing.T) {
	repays := []LoanRepay{{Date: "2026-02-01", Amount: 5_000_000}}
	owed, _ := ReserveLoanBalance(1_000_000, 1200, "2026-01-01", repays, "2027-01-01")
	if owed != 0 {
		t.Fatalf("переплата дала owed=%d, хочемо 0", owed)
	}
}

// Після закриття відсоток більше не набігає, скільки б часу не минуло.
func TestReserveLoanStaysClosed(t *testing.T) {
	repays := []LoanRepay{{Date: "2026-02-01", Amount: 1_100_000}}
	a, _ := ReserveLoanBalance(1_000_000, 1200, "2026-01-01", repays, "2026-03-01")
	b, _ := ReserveLoanBalance(1_000_000, 1200, "2026-01-01", repays, "2030-03-01")
	if a != 0 || b != 0 {
		t.Fatalf("закрита позика ожила: %d / %d", a, b)
	}
}

// Нульова ставка законна: «поверну ту саму суму» — теж обіцянка. Борг є,
// надбавки до цілі немає.
func TestReserveLoanZeroRateStillOwes(t *testing.T) {
	owed, accrued := ReserveLoanBalance(1_000_000, 0, "2026-01-01", nil, "2027-01-01")
	if owed != 1_000_000 || accrued != 0 {
		t.Fatalf("під 0%%: owed=%d accrued=%d, хочемо 1000000/0", owed, accrued)
	}
}

// Порахувати не можна — не показуємо. Порожня дата, нульове тіло й дата
// «до» узяття дають нуль, а не вигадане число.
func TestReserveLoanUncountableIsSilent(t *testing.T) {
	cases := []struct {
		name            string
		principal, rate int64
		taken, asOf     Date
	}{
		{"без тіла", 0, 1200, "2026-01-01", "2027-01-01"},
		{"без дати узяття", 1_000_000, 1200, "", "2027-01-01"},
		{"без сьогодні", 1_000_000, 1200, "2026-01-01", ""},
		{"сьогодні раніше за узяття", 1_000_000, 1200, "2026-01-01", "2025-01-01"},
	}
	for _, c := range cases {
		if owed, accrued := ReserveLoanBalance(c.principal, c.rate, c.taken, nil, c.asOf); owed != 0 || accrued != 0 {
			t.Fatalf("%s: owed=%d accrued=%d, хочемо нулі", c.name, owed, accrued)
		}
	}
}

// Повернення поза строком позики ігноруються: рядок, датований до узяття,
// нічого не гасить, а датований після сьогодні ще не стався.
func TestReserveLoanIgnoresRepaysOutsideWindow(t *testing.T) {
	repays := []LoanRepay{
		{Date: "2025-06-01", Amount: 900_000},
		{Date: "2028-06-01", Amount: 900_000},
	}
	owed, accrued := ReserveLoanBalance(1_000_000, 1200, "2026-01-01", repays, "2027-01-01")
	if owed != 1_120_000 || accrued != 120_000 {
		t.Fatalf("чужі повернення вплинули: owed=%d accrued=%d", owed, accrued)
	}
}
