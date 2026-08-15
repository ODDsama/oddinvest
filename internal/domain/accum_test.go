package domain

import (
	"math"
	"testing"
)

// TestCompoundFromSimpleIsNotTheSameNumber — проста середньорічна й
// складна це РІЗНІ числа, і різниця не косметична.
//
// Фонд обіцяє «середньорічну просту 25% за три роки»: сумарно ×1.75.
// Застосунок компаундить щомісяця, і 25% складних дають ×1.95 — приріст,
// завищений на чверть, під тією самою назвою «дохідність, %».
func TestCompoundFromSimpleIsNotTheSameNumber(t *testing.T) {
	got := CompoundFromSimple(25, 3)
	if math.Abs(got-20.507) > 0.01 {
		t.Errorf("проста 25%% за 3 р. дала %.3f%% складних, очікували ≈20.51", got)
	}
	// Головна властивість: за той самий строк обидві дають той самий
	// підсумок. Якщо це не так, переведення не переведення, а окреме
	// припущення.
	r := 1 + got/100
	if total := r * r * r; math.Abs(total-1.75) > 1e-9 {
		t.Errorf("складна %.4f%% за 3 роки дала ×%.6f, а проста обіцяла ×1.75", got, total)
	}
	// Нуль років означає «обіцянка вже складна» — чіпати її не можна.
	if got := CompoundFromSimple(25, 0); got != 25 {
		t.Errorf("без строку обіцянка мала лишитись 25%%, маємо %v", got)
	}
	// Один рік: проста й складна збігаються за визначенням.
	if got := CompoundFromSimple(25, 1); math.Abs(got-25) > 1e-9 {
		t.Errorf("за один рік проста й складна мали збігтись, маємо %v", got)
	}
}

// TestAccumGrowsWhereLockedWouldLieStill — накопичувальна позиція росте,
// а замкнений капітал ні.
//
// Це і є вся вада, заради якої з'явився четвертий кошик: фонд, що обіцяє
// 25% річних, лежав у locked і за три роки не додавав анічого.
func TestAccumGrowsWhereLockedWouldLieStill(t *testing.T) {
	acc := Sleeve{
		Currency: "UAH", Rate0: 1,
		Accum: []Accum{{Value0: 5000, Cost0: 5000, RatePct: 25}},
	}
	locked := Sleeve{Currency: "UAH", Rate0: 1, Nominal0: 5000}

	a := ProjectSleeves([]Sleeve{acc}, 0, 36).TodayUAH
	l := ProjectSleeves([]Sleeve{locked}, 0, 36).TodayUAH
	if math.Abs(l-5000) > 0.01 {
		t.Errorf("замкнене мало лишитись 5000, маємо %.2f", l)
	}
	want := 5000 * 1.25 * 1.25 * 1.25
	if math.Abs(a-want) > 1 {
		t.Errorf("накопичувальне дало %.2f, очікували ≈%.2f", a, want)
	}
}

// TestAccumPaysNoIncomeUntilItCloses — накопичувальний фонд не дає
// потоку, і картка доходу не має його вигадувати.
//
// Доти його вартість лежала в locked, а дохід рахувався як
// (invested+locked)×ставка рукава — тобто фонд, який не платить ані
// копійки, кредитувався ОБЛІГАЦІЙНОЮ ставкою.
func TestAccumPaysNoIncomeUntilItCloses(t *testing.T) {
	s := Sleeve{
		Currency: "UAH", Rate0: 1, RatePct: 16, RateTerminalPct: 16,
		Accum: []Accum{{Value0: 5000, Cost0: 5000, RatePct: 25, CloseM: 30}},
	}
	if inc := ProjectSleeves([]Sleeve{s}, 0, 24).IncomeMonthlyTodayUAH; inc != 0 {
		t.Errorf("до закриття фонд дав дохід %.2f, а платить він нічого", inc)
	}
	// Після закриття гроші стають звичайним капіталом і починають
	// приносити — уже за ставкою рукава, бо фонду більше немає.
	if inc := ProjectSleeves([]Sleeve{s}, 0, 36).IncomeMonthlyTodayUAH; inc <= 0 {
		t.Errorf("після закриття дохід мав з'явитись, маємо %.2f", inc)
	}
}

// TestAccumCloseTaxesOnlyTheGain — податок береться з доходу, а не з
// усієї суми, і рахується від СОБІВАРТОСТІ, а не від сьогоднішньої ціни.
//
// Позиція, яка вже подорожчала до сьогодні, несе цей неоподаткований
// приріст із собою. Рахувати податок від сьогоднішньої вартості означало
// б його подарувати.
func TestAccumCloseTaxesOnlyTheGain(t *testing.T) {
	// Купили за 4000, сьогодні коштує 5000, росте 25% рік, закриття
	// через 12 місяців, податок 14%.
	s := Sleeve{
		Currency: "UAH", Rate0: 1,
		Accum: []Accum{{Value0: 5000, Cost0: 4000, RatePct: 25, CloseM: 12, TaxPct: 14}},
	}
	got := ProjectSleeves([]Sleeve{s}, 0, 12).TodayUAH
	gross := 5000 * 1.25
	want := gross - (gross-4000)*0.14
	if math.Abs(got-want) > 1 {
		t.Errorf("після закриття лишилось %.2f, очікували %.2f "+
			"(податок з доходу %.2f, а не з усієї суми)", got, want, gross-4000)
	}
	// Той самий фонд без податку лишає всю суму — інакше перевірка вище
	// проходила б і з податком, узятим бозна з чого.
	free := s
	free.Accum = []Accum{{Value0: 5000, Cost0: 4000, RatePct: 25, CloseM: 12}}
	if got := ProjectSleeves([]Sleeve{free}, 0, 12).TodayUAH; math.Abs(got-gross) > 1 {
		t.Errorf("без податку мало лишитись %.2f, маємо %.2f", gross, got)
	}
}

// AccumCloseValue мусить давати РІВНО те саме, що симуляція випускає в
// місяць закриття.
//
// Ця функція існує лише тому, що закриття фонду потрібне за межами
// симуляції: профіль надходжень малює його подією на осі, а projState вміє
// тільки вмішати суму в загальний потік місяця й забути, звідки вона.
// Тобто арифметика описана двічі — і цей тест єдине, що не дає двом
// описам розійтись.
//
// Порівнюємо з рукавом БЕЗ ставки й без порога: тоді все, що вийшло з
// фонду, просто лягає в підсумок і не змішується з відсотками рукава.
func TestAccumCloseValueMatchesSimulation(t *testing.T) {
	for _, c := range []struct {
		name string
		a    Accum
	}{
		{"із прибутком", Accum{Value0: 5000, Cost0: 4000, RatePct: 25, CloseM: 12, TaxPct: 14}},
		{"збиткова — податку немає", Accum{Value0: 3000, Cost0: 4000, RatePct: 0, CloseM: 6, TaxPct: 14}},
		{"без податку", Accum{Value0: 5000, Cost0: 4000, RatePct: 10, CloseM: 24}},
		{"довгий строк", Accum{Value0: 1000, Cost0: 1000, RatePct: 18, CloseM: 36, TaxPct: 19.5}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := Sleeve{Currency: "UAH", Rate0: 1, Accum: []Accum{c.a}}
			// Горизонт = місяць закриття: гроші виходять і більше ніде не
			// працюють, тож підсумок рукава і є сума закриття.
			sim := ProjectSleeves([]Sleeve{s}, 0, c.a.CloseM).TodayUAH
			got := AccumCloseValue(c.a)
			if math.Abs(sim-got) > 0.01 {
				t.Errorf("AccumCloseValue дала %.2f, симуляція випустила %.2f", got, sim)
			}
			if got <= 0 {
				t.Errorf("тест нічого не перевірив: %.2f", got)
			}
		})
	}
	// Фонд, що не закривається, події не має.
	if got := AccumCloseValue(Accum{Value0: 5000, Cost0: 4000, RatePct: 25}); got != 0 {
		t.Errorf("без дати закриття мало бути 0, маємо %.2f", got)
	}
}

// TestAccumStopsGrowingAfterClose — після закриття гроші працюють за
// ставкою рукава, а не далі за фондовою.
//
// Інакше дата закриття була б декорацією: фонд, чиї облігації гасяться в
// 2029-му, малював би 25% і в 2035-му.
func TestAccumStopsGrowingAfterClose(t *testing.T) {
	// Рукав без порога докупівлі й без власної дохідності: усе, що
	// вийшло з фонду, лягає в invested і росте під 0%.
	s := Sleeve{
		Currency: "UAH", Rate0: 1, RatePct: 0, RateTerminalPct: 0,
		Accum: []Accum{{Value0: 1000, Cost0: 1000, RatePct: 100, CloseM: 12}},
	}
	at12 := ProjectSleeves([]Sleeve{s}, 0, 12).TodayUAH
	at36 := ProjectSleeves([]Sleeve{s}, 0, 36).TodayUAH
	if math.Abs(at12-2000) > 1 {
		t.Errorf("на закритті мало бути ≈2000, маємо %.2f", at12)
	}
	if math.Abs(at36-at12) > 1 {
		t.Errorf("після закриття капітал змінився з %.2f на %.2f — "+
			"фонд продовжив рости, хоч його вже немає", at12, at36)
	}
}

// TestAccumIsSellableLikeCash — сертифікати доступні декумуляції, і без
// податку тримають зняття рівно стільки ж, скільки та сама сума готівкою.
//
// Доти вони лежали в замкненому разом із номіналом ОВДП, і картка писала
// «портфель вичерпався на першому місяці», маючи сертифікатів на всю
// суму. Різниця саме в ціні: ціну сертифіката застосунок ЗНАЄ, і
// продається він будь-якого дня.
func TestAccumIsSellableLikeCash(t *testing.T) {
	fund := Sleeve{
		Currency: "UAH", Rate0: 1,
		Accum: []Accum{{Value0: 12000, Cost0: 12000, CloseM: 6}},
	}
	cash := Sleeve{Currency: "UAH", Rate0: 1, Cash0: 12000}
	a := DrawdownMonths([]Sleeve{fund}, 0, 1000, 60)
	b := DrawdownMonths([]Sleeve{cash}, 0, 1000, 60)
	if b != 13 {
		t.Fatalf("12000 готівкою під 1000/міс мали вичерпатись на 13-му, маємо %d", b)
	}
	if a != b {
		t.Errorf("фонд протримав %d місяців, готівка %d — без податку різниці бути не може", a, b)
	}
	// Зняття рівно на всю позицію: єдиний спосіб потрапити в гілку
	// ПОВНОГО продажу. Зняттями по тисячі туди не зайти ніколи — на
	// останньому кроці лишається копійчаний хвіст від ділення, і продаж
	// щоразу частковий. Мутація «продаж не обнуляє позицію» саме через це
	// лишалась зеленою.
	if m := DrawdownMonths([]Sleeve{fund}, 0, 12000, 60); m != 2 {
		t.Errorf("зняття на всю позицію мало вичерпати її за один місяць "+
			"(вперлось би на другому), маємо %d", m)
	}
}

// TestAccumExitTaxShortensTheRunway — податок при достроковому виході
// з'їдає частину, і портфеля вистачає на менше.
//
// Число в картці мусить бути тим, що лишається на руки, а не тим, що
// написано на екрані брокера.
func TestAccumExitTaxShortensTheRunway(t *testing.T) {
	free := Sleeve{Currency: "UAH", Rate0: 1,
		Accum: []Accum{{Value0: 20000, Cost0: 10000}}}
	taxed := Sleeve{Currency: "UAH", Rate0: 1,
		Accum: []Accum{{Value0: 20000, Cost0: 10000, ExitTaxPct: 23}}}
	a := DrawdownMonths([]Sleeve{free}, 0, 1000, 60)
	b := DrawdownMonths([]Sleeve{taxed}, 0, 1000, 60)
	if a != 21 {
		t.Fatalf("20000 без податку під 1000/міс мали вичерпатись на 21-му, маємо %d", a)
	}
	// Дохід 10000, податок 23% = 2300, лишається 17700 → 17 повних знять,
	// на вісімнадцятому вже нема з чого.
	if b != 18 {
		t.Errorf("з податком 23%% мало вистачити на 17 знять (місяць 18), маємо %d", b)
	}
}

// TestDrawdownSellsDistributingBeforeAccumulating — порядок продажу за
// ціною виходу.
//
// Не смак: у розподільного вихід коштує лише податок із доходу, у
// накопичувального — вищу ставку за дострокове припинення. Продавати
// спершу дешевше — єдине впорядкування, яке не є порадою.
//
// Перший підхід до цього тесту нічого не перевіряв, і це варто назвати.
// Я взяв дві позиції без дати закриття й дивився на місяць вичерпання —
// а він від порядку НЕ ЗАЛЕЖИТЬ: чиста вартість кожної позиції рахується
// окремо, тож сума однакова, у якому б порядку їх не продавали. Тест
// зеленів би й зі зворотним порядком.
//
// Розрізняє порядок лише дата закриття. Доживши до неї, фонд віддає
// гроші за ставкою ЗАКРИТТЯ (тут 0), а проданий раніше — за ставкою
// ВИХОДУ (тут 50%). Якщо розподільна йде першою, накопичувальна встигає
// дожити й дає всі 5000; якщо ні — половину.
func TestDrawdownSellsDistributingBeforeAccumulating(t *testing.T) {
	s := Sleeve{
		Currency: "UAH", Rate0: 1,
		Dist:  []Dist{{Value: 5000, Cost: 5000}},
		Accum: []Accum{{Value0: 5000, Cost0: 0, CloseM: 6, TaxPct: 0, ExitTaxPct: 50}},
	}
	// Розподільної рівно на п'ять знять; на шостому фонд закривається й
	// додає 5000 без податку. Разом десять повних знять.
	if m := DrawdownMonths([]Sleeve{s}, 0, 1000, 60); m != 11 {
		t.Errorf("вистачило до місяця %d, очікували 11. Вісім означало б, що "+
			"накопичувальну продали першою й заплатили 50%% там, де можна було дочекатись", m)
	}
}
