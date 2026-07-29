package domain

import "testing"

// TestDrawdownPlainCashLastsAsExpected — портфель без дохідності й без
// потоків тане рівно на суму зняття.
//
// Найпростіший випадок, і саме тому найкорисніший: якщо він не сходиться,
// решта чисел не має сенсу перевірятись. 120 000 ₴ по 10 000 ₴/міс без
// відсотків і без знецінення мусять скінчитись на 13-му місяці — на
// дванадцятому знімається останнє.
func TestDrawdownPlainCashLastsAsExpected(t *testing.T) {
	sl := []Sleeve{{Currency: "UAH", Cash0: 120_000, Rate0: 1}}
	if got := DrawdownMonths(sl, 0, 10_000, 720); got != 13 {
		t.Errorf("маємо %d, очікували 13", got)
	}
}

// TestDrawdownIncomeCoversWithdrawal — коли потік покриває зняття,
// портфель не вичерпується.
//
// −1 означає саме це, і плутати його з нулем не можна: нуль це «не
// вистачає навіть на перший місяць», тобто протилежність.
func TestDrawdownIncomeCoversWithdrawal(t *testing.T) {
	// Мільйон під 12% дає близько 10 000 ₴/міс; знімаємо 1 000.
	sl := []Sleeve{{Currency: "UAH", Cash0: 1_000_000, RatePct: 12,
		RateTerminalPct: 12, Rate0: 1}}
	if got := DrawdownMonths(sl, 0, 1_000, 240); got != -1 {
		t.Errorf("маємо %d, очікували -1: дохід покриває зняття", got)
	}
}

// TestDrawdownNotEnoughForFirstMonth — нуль означає «не вистачає навіть
// на місяць», і це теж відповідь.
func TestDrawdownNotEnoughForFirstMonth(t *testing.T) {
	sl := []Sleeve{{Currency: "UAH", Cash0: 5_000, Rate0: 1}}
	if got := DrawdownMonths(sl, 0, 50_000, 720); got != 1 {
		t.Errorf("маємо %d, очікували 1 (перший же місяць не покривається)", got)
	}
}

// TestDrawdownDoesNotSellLocked — замкнене достроково не продається.
//
// Це найконсервативніша з припущених угод, і найлегша для тихого злому:
// досить узяти зняття з total() замість cash+invested, і портфель почне
// «жити» з номіналу ОВДП, який насправді продати не можна.
//
// Тут уся вартість замкнена, потоків немає — отже зняти нема звідки вже
// першого місяця, попри мільйон «капіталу».
func TestDrawdownDoesNotSellLocked(t *testing.T) {
	sl := []Sleeve{{Currency: "UAH", Nominal0: 1_000_000, Rate0: 1}}
	if got := DrawdownMonths(sl, 0, 10_000, 720); got != 1 {
		t.Errorf("маємо %d, очікували 1: замкнене не продається, а готівки немає", got)
	}
}

// TestDrawdownRedemptionRefillsCash — погашення повертає гроші й продовжує
// життя портфеля.
//
// Дзеркало попереднього: замкнене не продається, але коли воно гаситься
// за графіком, гроші приходять і зняття знову є з чого робити.
func TestDrawdownRedemptionRefillsCash(t *testing.T) {
	bare := []Sleeve{{Currency: "UAH", Cash0: 30_000, Nominal0: 500_000, Rate0: 1}}
	withRedeem := []Sleeve{{Currency: "UAH", Cash0: 30_000, Nominal0: 500_000, Rate0: 1,
		Redeem: map[int]float64{4: 500_000}}}
	a := DrawdownMonths(bare, 0, 10_000, 720)
	b := DrawdownMonths(withRedeem, 0, 10_000, 720)
	if a != 4 {
		t.Errorf("без погашення маємо %d, очікували 4", a)
	}
	if b <= a {
		t.Errorf("погашення не подовжило життя портфеля: %d проти %d", b, a)
	}
}

// TestDrawdownSplitsByLiquidNotTotal — рукав, у якого майже все замкнене,
// не тягне на себе більше, ніж може дати.
//
// Саме на цьому модель спершу й помилилась. Зняття ділилось пропорційно
// ПОВНІЙ вартості рукава, а платити може лише ліквідна частина: доларовий
// рукав із трьома тисячами номіналу отримував свою «частку» й не міг її
// покрити, недобір нікуди не переносився, і портфель «вичерпувався»
// першого ж місяця — маючи пів мільйона готівки в гривневому рукаві.
//
// Тут гривня має 600 000 готівки, долар — саму замкнену тисячу. 10 000
// ₴/міс мусять братись із гривні й вистачити надовго.
func TestDrawdownSplitsByLiquidNotTotal(t *testing.T) {
	sl := []Sleeve{
		{Currency: "UAH", Cash0: 600_000, Rate0: 1},
		{Currency: "USD", Nominal0: 1_000, Rate0: 44},
	}
	got := DrawdownMonths(sl, 0, 10_000, 720)
	if got < 60 {
		t.Errorf("вистачило на %d міс; гривневої готівки на 60, а доларовий рукав "+
			"замкнений і в поділі участі брати не може", got)
	}
}

// TestDrawdownIndexesWithdrawalToDevaluation — зняття задане в
// СЬОГОДНІШНІХ грошах, тож зі знеціненням портфеля вистачає на менше.
//
// Без індексації картка обіцяла б роки життя за корзину, яка через
// десять років коштує вдвічі більше.
func TestDrawdownIndexesWithdrawalToDevaluation(t *testing.T) {
	sl := func() []Sleeve {
		return []Sleeve{{Currency: "UAH", Cash0: 1_200_000, Rate0: 1}}
	}
	flat := DrawdownMonths(sl(), 0, 10_000, 720)
	inflated := DrawdownMonths(sl(), 15, 10_000, 720)
	if inflated >= flat {
		t.Errorf("зі знеціненням 15%%/рік вистачило на %d міс, без нього на %d — "+
			"зняття мусить дорожчати", inflated, flat)
	}
}
