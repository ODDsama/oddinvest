package domain

import (
	"math"
	"testing"

	money "github.com/Rhymond/go-money"
)

// schedule — річний папір із піврічним купоном 8% і погашенням номіналу.
func schedule(isin string, from Date) []Payment {
	return []Payment{
		{ISIN: isin, PayDate: from.AddDays(182), Type: PayCoupon,
			PerBond: money.New(8_000, money.UAH)},
		{ISIN: isin, PayDate: from.AddDays(365), Type: PayCoupon,
			PerBond: money.New(8_000, money.UAH)},
		{ISIN: isin, PayDate: from.AddDays(365), Type: PayRedemption,
			PerBond: money.New(100_000, money.UAH)},
	}
}

// ГОЛОВНЕ, ЧОГО ЦЕЙ ФАЙЛ СТЕРЕЖЕ: поріг і вердикт мусять сходитись у нулі.
// Продаж рівно за порогом має дати дохідність утримання, що дорівнює
// ставці альтернативи, — інакше два числа на одному екрані сперечались би.
func TestBreakEvenAgreesWithVerdict(t *testing.T) {
	today := Date("2026-08-24")
	in := SwitchInput{ISIN: "UA1", Payments: schedule("UA1", today), Today: today,
		AltRatePct: 18}
	be, err := BreakEvenClean(in)
	if err != nil {
		t.Fatal(err)
	}
	res, err := SwitchVerdict(in, be)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.HoldRatePct-18) > 0.01 {
		t.Errorf("за порогом дохідність утримання %.4f%%, очікували ставку альтернативи 18%%",
			res.HoldRatePct)
	}
	if a := res.GainPerBond.Amount(); a < -1 || a > 1 {
		t.Errorf("за порогом виграш мав бути нульовим, маємо %d мінорних", a)
	}
}

// Що вища ставка альтернативи, то нижчий поріг: перекладати варто вже за
// гіршу ціну. Зворотний порядок означав би переплутаний знак дисконту.
func TestBreakEvenFallsAsAlternativeRises(t *testing.T) {
	today := Date("2026-08-24")
	pays := schedule("UA1", today)
	var prev int64 = math.MaxInt64
	for _, rate := range []float64{5, 10, 18, 25} {
		be, err := BreakEvenClean(SwitchInput{ISIN: "UA1", Payments: pays,
			Today: today, AltRatePct: rate})
		if err != nil {
			t.Fatal(err)
		}
		if be.Amount() >= prev {
			t.Errorf("ставка %.0f%%: поріг %d не нижчий за попередній %d", rate, be.Amount(), prev)
		}
		prev = be.Amount()
	}
}

// Ціна вище порога — перекладати вигідно, нижче — збитково. Знак виграшу
// й знак «дохідність утримання проти ставки альтернативи» мусять
// збігатися: це два вимірювання однієї відповіді.
func TestVerdictChangesSignAroundThreshold(t *testing.T) {
	today := Date("2026-08-24")
	in := SwitchInput{ISIN: "UA1", Payments: schedule("UA1", today), Today: today,
		AltRatePct: 18}
	be, err := BreakEvenClean(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name  string
		clean *money.Money
		want  bool // виграш додатній
	}{
		{"вище порога", money.New(be.Amount()+2_000, money.UAH), true},
		{"нижче порога", money.New(be.Amount()-2_000, money.UAH), false},
	} {
		res, err := SwitchVerdict(in, c.clean)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := res.GainPerBond.Amount() > 0; got != c.want {
			t.Errorf("%s: виграш %d, очікували додатній=%v", c.name, res.GainPerBond.Amount(), c.want)
		}
		// Дорожче продаси — менше дохідності лишиш собі, тримаючи.
		if got := res.HoldRatePct < in.AltRatePct; got != c.want {
			t.Errorf("%s: дохідність утримання %.2f%% проти альтернативи %.2f%% — знаки розійшлись",
				c.name, res.HoldRatePct, in.AltRatePct)
		}
	}
}

// Папір без майбутніх виплат — помилка, а не нуль: нульовий поріг
// прочитався б як «віддавай задарма».
func TestSwitchRefusesBondWithoutFuturePayments(t *testing.T) {
	today := Date("2026-08-24")
	past := schedule("UA1", Date("2020-01-01"))
	if _, err := BreakEvenClean(SwitchInput{ISIN: "UA1", Payments: past,
		Today: today, AltRatePct: 18}); err == nil {
		t.Error("погашений папір мав дати помилку")
	}
}

func TestSwitchRefusesNonPositivePrice(t *testing.T) {
	today := Date("2026-08-24")
	in := SwitchInput{ISIN: "UA1", Payments: schedule("UA1", today), Today: today,
		AltRatePct: 18}
	if _, err := SwitchVerdict(in, money.New(0, money.UAH)); err == nil {
		t.Error("нульова ціна мала дати помилку")
	}
}

// Виплата в САМ день продажу належить продавцю — та сама межа, що в
// HolderQty і YTM. Тож у теперішню вартість решти виплат вона не входить:
// вона й так твоя, і дисконтувати її як «майбутню» означало б заплатити
// покупцеві за те, що лишається тобі.
//
// Перевіряємо саме switchPV, а не поріг: у порозі поруч стоїть НКД, і
// купон сьогодні законно зсуває ЙОГО (з'являється попередня виплата, від
// якої період рахується наново — див. EstimateAccrued). Це поведінка НКД,
// а не перекладання, і змішувати два твердження в одному тесті означало б
// не перевірити жодного.
func TestSwitchPVExcludesPaymentOnSaleDay(t *testing.T) {
	today := Date("2026-08-24")
	base := schedule("UA1", today)
	withToday := append(append([]Payment(nil), base...),
		Payment{ISIN: "UA1", PayDate: today, Type: PayCoupon,
			PerBond: money.New(8_000, money.UAH)})

	a, err := switchPV(SwitchInput{ISIN: "UA1", Payments: withToday, Today: today, AltRatePct: 18})
	if err != nil {
		t.Fatal(err)
	}
	b, err := switchPV(SwitchInput{ISIN: "UA1", Payments: base, Today: today, AltRatePct: 18})
	if err != nil {
		t.Fatal(err)
	}
	if a.Amount() != b.Amount() {
		t.Errorf("купон у день продажу потрапив у теперішню вартість: %d проти %d",
			a.Amount(), b.Amount())
	}
}

// Чужі ISIN у графіку не беруться: PaymentsFor віддає виплати пачкою на
// кілька паперів, і сплутати їх означало б порахувати чужі гроші.
func TestSwitchIgnoresOtherISINs(t *testing.T) {
	today := Date("2026-08-24")
	mixed := append(schedule("UA1", today), schedule("UA2", today)...)
	mine, err := BreakEvenClean(SwitchInput{ISIN: "UA1", Payments: mixed,
		Today: today, AltRatePct: 18})
	if err != nil {
		t.Fatal(err)
	}
	alone, err := BreakEvenClean(SwitchInput{ISIN: "UA1", Payments: schedule("UA1", today),
		Today: today, AltRatePct: 18})
	if err != nil {
		t.Fatal(err)
	}
	if mine.Amount() != alone.Amount() {
		t.Errorf("чужий папір зрушив поріг: %d проти %d", mine.Amount(), alone.Amount())
	}
}
