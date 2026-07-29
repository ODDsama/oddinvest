package domain

import "testing"

// oneSleeve — гривневий рукав без порога докупівлі: перевіряємо саму
// логіку перетину, а не поведінку готівки нижче ціни паперу.
func oneSleeve(cash, nominal, ratePct, contrib float64) []Sleeve {
	return []Sleeve{{
		Currency: "UAH", Cash0: cash, Nominal0: nominal,
		RatePct: ratePct, RateTerminalPct: ratePct, ContribUAH: contrib, Rate0: 1,
	}}
}

// TestMonthsToIncomeAlreadyCovered — коли дохід уже перевищує ціль,
// відповідь −1, а не нуль.
//
// Різниця протилежна за змістом: −1 означає «вже», нуль — «ніколи». Одне
// на місці іншого перетворило б досягнуту незалежність на недосяжну.
func TestMonthsToIncomeAlreadyCovered(t *testing.T) {
	// Мільйон під 12% дає ~10 000 ₴/міс — ціль у 1 000 покрита одразу.
	got := MonthsToIncomeSleeves(oneSleeve(0, 1_000_000, 12, 0), 0, 1_000, 720)
	if got != -1 {
		t.Errorf("маємо %d, очікували -1 (дохід уже покриває ціль)", got)
	}
}

// TestMonthsToIncomeUnreachable — недосяжна ціль дає нуль, а не
// останній місяць горизонту.
func TestMonthsToIncomeUnreachable(t *testing.T) {
	got := MonthsToIncomeSleeves(oneSleeve(0, 1_000, 5, 0), 0, 1_000_000, 240)
	if got != 0 {
		t.Errorf("маємо %d, очікували 0 (ціль недосяжна за горизонт)", got)
	}
}

// TestMonthsToIncomeMoreContribComesSooner — більший внесок наближає
// перетин.
//
// Той самий інваріант, що й для капіталу, і зламати його так само легко:
// досить зібрати рукави від чужого внеску.
func TestMonthsToIncomeMoreContribComesSooner(t *testing.T) {
	slow := MonthsToIncomeSleeves(oneSleeve(0, 100_000, 12, 5_000), 0, 20_000, 720)
	fast := MonthsToIncomeSleeves(oneSleeve(0, 100_000, 12, 20_000), 0, 20_000, 720)
	if slow == 0 || fast == 0 {
		t.Fatalf("обидва мали досягатись: повільний=%d швидкий=%d", slow, fast)
	}
	if fast >= slow {
		t.Errorf("більший внесок дав перетин не раніше: %d проти %d", fast, slow)
	}
}

// TestMonthsToIncomeCountsOnlyWorkingMoney — готівка, що не дотягла до
// найдешевшого паперу, доходу не дає.
//
// Це та сама угода, що й у ProjectSleeves, і порушити її означало б
// обіцяти потік із грошей, які лежать. Мільйон готівки з порогом у два
// мільйони не приносить нічого, скільки не чекай.
func TestMonthsToIncomeCountsOnlyWorkingMoney(t *testing.T) {
	sl := []Sleeve{{
		Currency: "UAH", Cash0: 1_000_000, RatePct: 12, RateTerminalPct: 12,
		Threshold: 2_000_000, Rate0: 1,
	}}
	if got := MonthsToIncomeSleeves(sl, 0, 1_000, 240); got != 0 {
		t.Errorf("маємо %d; готівка нижче порога доходу не приносить, отже 0", got)
	}
}

// TestMonthsToIncomeAgreesWithProjectSleeves — на знайденому місяці
// ProjectSleeves мусить показати дохід не менший за ціль.
//
// Дві функції рахують той самий потік різними шляхами: одна помісячно в
// лок-степі, друга — одним прогоном до кінця. Розійтись їм не можна,
// інакше картка називала б місяць, у якому потоку ще немає.
func TestMonthsToIncomeAgreesWithProjectSleeves(t *testing.T) {
	sl := oneSleeve(0, 100_000, 12, 10_000)
	const target = 20_000.0
	m := MonthsToIncomeSleeves(sl, 7, target, 720)
	if m <= 0 {
		t.Fatalf("ціль мала досягатись, маємо %d", m)
	}
	at := ProjectSleeves(sl, 7, m).IncomeMonthlyTodayUAH
	if at < target {
		t.Errorf("на місяці %d дохід %v, а ціль %v — функції розійшлись", m, at, target)
	}
	// І на місяць раніше — ще НЕ досягнуто, інакше знайдений місяць пізній.
	if prev := ProjectSleeves(sl, 7, m-1).IncomeMonthlyTodayUAH; prev >= target {
		t.Errorf("на місяці %d дохід %v уже покривав ціль — знайдено пізніший місяць",
			m-1, prev)
	}
}
