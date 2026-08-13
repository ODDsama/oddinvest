package domain

import (
	"math"
	"testing"
)

// TestDistPaysItsOwnRateNotTheSleeveRate — розподільний фонд приносить
// СВОЮ дивідендну, а не ставку рукава.
//
// Доти його вартість лежала в замкненому капіталі, а дохід рахувався як
// (invested+locked)×ставка рукава — тобто фонд приносив стільки ж,
// скільки облігація. Для ОВДП це правда за побудовою: саме її купон ту
// ставку й задає. Для фонду ні, і на портфелі, де вони розходяться на
// кілька пунктів, картка «коли дохід покриє життя» відповідала не про той
// портфель.
func TestDistPaysItsOwnRateNotTheSleeveRate(t *testing.T) {
	// Ставка рукава НУЛЬОВА: під старою моделлю фонд у замкненому дав би
	// рівно нуль доходу, тож будь-яке ненульове число тут — його власне.
	s := Sleeve{
		Currency: "UAH", Rate0: 1, RatePct: 0, RateTerminalPct: 0,
		Dist: []Dist{{Value: 10_000, RatePct: 12}},
	}
	want := 10_000 * MonthlyRate(12)
	got := ProjectSleeves([]Sleeve{s}, 0, 12).IncomeMonthlyTodayUAH
	if math.Abs(got-want) > 0.01 {
		t.Errorf("дохід %.2f, очікували %.2f (12%% на 10000, власна ставка фонду)", got, want)
	}
	// І він НЕ дорівнює тому, що дала б ставка рукава, — інакше перевірка
	// вище проходила б випадково.
	if sleeveRate := 10_000 * MonthlyRate(30); math.Abs(got-sleeveRate) < 0.01 {
		t.Errorf("дохід збігся зі ставкою рукава %.2f", sleeveRate)
	}
}

// TestDistKeepsPayingPastTheFirstYear — потік фонду живе весь горизонт, а
// не рік.
//
// Найдорожча з двох вад. Календар оцінює дивіденди на 12 місяців уперед,
// і ці ж потоки лягали в купони рукава — на тринадцятому місяці фонд у
// моделі замовкав назавжди. На десятирічному горизонті це дев'ять років,
// у які позиція не давала нічого й при цьому не зникала.
func TestDistKeepsPayingPastTheFirstYear(t *testing.T) {
	s := Sleeve{
		Currency: "UAH", Rate0: 1, RatePct: 0, RateTerminalPct: 0,
		Dist: []Dist{{Value: 10_000, RatePct: 12}},
	}
	at12 := ProjectSleeves([]Sleeve{s}, 0, 12).TodayUAH
	at24 := ProjectSleeves([]Sleeve{s}, 0, 24).TodayUAH
	first := at12 - 10_000
	second := at24 - at12
	if first <= 0 {
		t.Fatalf("за перший рік фонд не заплатив нічого: %.2f", first)
	}
	// Ставка рукава нульова, тож виплачене просто лежить: другий рік
	// мусить додати рівно стільки ж, скільки перший.
	if math.Abs(second-first) > 0.01 {
		t.Errorf("перший рік дав %.2f, другий %.2f — потік урвався", first, second)
	}
}

// TestDistValueDoesNotGrow — сертифікат не дорожчає в моделі.
//
// Свідома угода, і вона мусить лишитись помітною: подорожчання ціни
// ніхто не обіцяв, тож домальовувати його — гірше, ніж занизити. Увесь
// дохід розподільного фонду приходить виплатами.
func TestDistValueDoesNotGrow(t *testing.T) {
	// Поріг вищий за будь-яку виплату — і все, що фонд платить, лишається
	// готівкою. Тоді підсумок дорівнює вартості плюс виплати, а не
	// вартості, яка сама росте.
	s := Sleeve{
		Currency: "UAH", Rate0: 1, Threshold: 1e9,
		Dist: []Dist{{Value: 10_000, RatePct: 12}},
	}
	got := ProjectSleeves([]Sleeve{s}, 0, 12).TodayUAH
	want := 10_000 + 12*10_000*MonthlyRate(12)
	if math.Abs(got-want) > 0.01 {
		t.Errorf("підсумок %.2f, очікували %.2f (вартість + дванадцять виплат)", got, want)
	}
}

// TestIncomeCountsEveryBucketOnce — дохід зводить усі кошики капіталу й
// жодного не рахує двічі.
//
// Формула жила двома копіями в сусідніх функціях, і кожна нова сутність
// мусила бути дописана в обидві. Тепер вона одна, і цей тест — про те, що
// в ній усі складники: працюючий пул і замкнене за ставкою рукава,
// розподільні за власною, накопичувальні нулем.
func TestIncomeCountsEveryBucketOnce(t *testing.T) {
	s := Sleeve{
		Currency: "UAH", Rate0: 1, RatePct: 10, RateTerminalPct: 10, Threshold: 1e9,
		Nominal0: 50_000,
		Dist:     []Dist{{Value: 10_000, RatePct: 12}},
		Accum:    []Accum{{Value0: 20_000, Cost0: 20_000, RatePct: 25}},
	}
	got := ProjectSleeves([]Sleeve{s}, 0, 12).IncomeMonthlyTodayUAH
	// Готівка від виплат фонду лежить (поріг захмарний), тож invested = 0.
	want := 50_000*MonthlyRate(10) + 10_000*MonthlyRate(12)
	if math.Abs(got-want) > 0.01 {
		t.Errorf("дохід %.2f, очікували %.2f: номінал за ставкою рукава, фонд за своєю, "+
			"накопичувальний нулем", got, want)
	}
}
