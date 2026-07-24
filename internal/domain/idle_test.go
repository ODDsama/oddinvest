package domain

import "testing"

func ev(d Date, amt int64) CashEvent { return CashEvent{Date: d, Amount: amt} }

// Головний випадок, заради якого все й переписано: купон замалий, щоб
// купити щось сам, тож покупка робиться зі своїми грішми — і купон у неї
// входить. Доти це вимагало ручного кліка, і без нього 82 ₴ висіли в
// «не перевкладено» вічно.
func TestIdleIncomeEatenByBiggerPurchase(t *testing.T) {
	income := []CashEvent{ev("2026-07-20", 8275)}          // купон 82.75 ₴
	buys := []CashEvent{ev("2026-07-21", 458_200)}         // папір за 4 582 ₴
	if got := IdleIncome(income, buys); got != 0 {
		t.Errorf("купон мав увійти в покупку, лишилось %d", got)
	}
}

// Покупка ДО надходження купона його не з'їдає: заплатити грошима, яких
// іще немає, не можна.
func TestIdleIncomeIgnoresEarlierPurchase(t *testing.T) {
	income := []CashEvent{ev("2026-07-20", 8275)}
	buys := []CashEvent{ev("2026-07-01", 458_200)}
	if got := IdleIncome(income, buys); got != 8275 {
		t.Errorf("купон надійшов після покупки, мав лишитись цілим, маємо %d", got)
	}
}

// Найстаріше — першим. Дві виплати, одна покупка рівно на першу: друга
// має лишитись недоторканою.
func TestIdleIncomeConsumesOldestFirst(t *testing.T) {
	income := []CashEvent{ev("2026-05-01", 10_000), ev("2026-06-01", 30_000)}
	buys := []CashEvent{ev("2026-07-01", 10_000)}
	if got := IdleIncome(income, buys); got != 30_000 {
		t.Errorf("мала з'їстись травнева виплата, лишилось %d замість 30000", got)
	}
}

// Виплата того самого дня, що й покупка, вважається доступною: гроші вже
// на рахунку, коли ти натискаєш «купити».
func TestIdleIncomeSameDayCounts(t *testing.T) {
	if got := IdleIncome([]CashEvent{ev("2026-07-20", 5_000)},
		[]CashEvent{ev("2026-07-20", 5_000)}); got != 0 {
		t.Errorf("виплата того самого дня мала бути доступна, лишилось %d", got)
	}
}

// Кілька покупок вичерпують чергу поступово, а надлишок покупки понад
// дохід просто оплачений своїми грішми — це не помилка.
func TestIdleIncomeMultiplePurchases(t *testing.T) {
	income := []CashEvent{ev("2026-01-01", 1_000), ev("2026-02-01", 2_000), ev("2026-03-01", 3_000)}
	buys := []CashEvent{ev("2026-02-15", 2_500), ev("2026-04-01", 500_000)}
	if got := IdleIncome(income, buys); got != 0 {
		t.Errorf("усе мало витратитись, лишилось %d", got)
	}
	// А без другої покупки лишається те, що надійшло після першої.
	if got := IdleIncome(income, buys[:1]); got != 3_500 {
		t.Errorf("мало лишитись 3500 (500 з лютого + 3000 з березня), маємо %d", got)
	}
}

// Без покупок нічого не витрачається; без доходу — нема чому лишатись.
func TestIdleIncomeEdges(t *testing.T) {
	if got := IdleIncome([]CashEvent{ev("2026-01-01", 700)}, nil); got != 700 {
		t.Errorf("без покупок дохід мав лишитись цілим, маємо %d", got)
	}
	if got := IdleIncome(nil, []CashEvent{ev("2026-01-01", 700)}); got != 0 {
		t.Errorf("без доходу мало бути 0, маємо %d", got)
	}
}
