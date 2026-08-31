package domain

import (
	"math"
	"testing"
)

// flat — ряд з однієї точки: значення діє від dawn і назавжди.
func flat(from Date, v float64) Quotes { return Quotes{{On: from, V: v}} }

func rivalByKey(rs []Rival, key string) Rival {
	for _, r := range rs {
		if r.Key == key {
			return r
		}
	}
	return Rival{}
}

func near(t *testing.T, got, want float64, what string) {
	t.Helper()
	if math.Abs(got-want) > 0.01 {
		t.Errorf("%s: маємо %.2f, треба %.2f", what, got, want)
	}
}

// AsOf бере ПОПЕРЕДНЮ точку, а не найближчу.
//
// Найближча була б курсом, якого в день внеску ще ніхто не знав, — тобто
// знанням із майбутнього, вкладеним у руки супернику. Різниця видно рівно
// тут: між двома точками правильна відповідь — ліва.
func TestQuotesAsOfTakesEarlierNeverLater(t *testing.T) {
	q := Quotes{{On: "2026-07-10", V: 41}, {On: "2026-07-20", V: 45}}
	if v, ok := q.AsOf("2026-07-19"); !ok || v != 41 {
		t.Errorf("19 липня діяв курс 41, а маємо %.2f (ok=%v)", v, ok)
	}
	if v, ok := q.AsOf("2026-07-20"); !ok || v != 45 {
		t.Errorf("у сам день зміни діє вже нова точка, а маємо %.2f", v)
	}
	if _, ok := q.AsOf("2026-07-09"); ok {
		t.Error("до першої точки значення немає — і мовчання тут єдина чесна відповідь")
	}
}

// «Гривня під матрацом» — це рівно сума внесків, і ніщо інше.
//
// Сторож зібраності потоку: будь-яке інше число тут означає, що внески
// порахували криво, і решта суперників, які ходять тим самим потоком,
// помиляються так само й тихо.
func TestRivalUAHCashEqualsContributions(t *testing.T) {
	flows := []Contribution{
		{On: "2026-07-15", UAH: 10_000},
		{On: "2026-08-01", UAH: 5_000},
		{On: "2026-08-10", UAH: -2_000},
	}
	rs := RunRivals(flows, DaysGrid("2026-07-15", "2026-08-31"), RivalInputs{})
	cash := rivalByKey(rs, RivalUAHCash)
	if cash.Why != "" {
		t.Fatalf("сумі внесків даних не треба, а вона мовчить: %s", cash.Why)
	}
	near(t, cash.TerminalUAH, 13_000, "сума внесків")
}

// Зняття зменшує лінію в день зняття, а не заднім числом.
func TestRivalUAHCashDropsOnWithdrawalDay(t *testing.T) {
	flows := []Contribution{{On: "2026-07-15", UAH: 10_000}, {On: "2026-08-10", UAH: -2_000}}
	days := DaysGrid("2026-07-15", "2026-08-31")
	cash := rivalByKey(RunRivals(flows, days, RivalInputs{}), RivalUAHCash)
	near(t, cash.Points[DaysBetween("2026-07-15", "2026-08-09")], 10_000, "напередодні зняття")
	near(t, cash.Points[DaysBetween("2026-07-15", "2026-08-10")], 8_000, "у день зняття")
}

// Валютний суперник купує за курсом ДНЯ ВНЕСКУ, а оцінюється сьогоднішнім.
//
// Дві точки курсу й один внесок між ними: 10 000 ₴ по 40 — це 250 $, і на
// курсі 50 вони варті 12 500 ₴. Число рахується руками саме тому, що
// перевіряється правило, а не реалізація.
func TestRivalFXBuysAtFlowDayRate(t *testing.T) {
	flows := []Contribution{{On: "2026-07-15", UAH: 10_000}}
	in := RivalInputs{USD: Quotes{{On: "2026-07-01", V: 40}, {On: "2026-08-20", V: 50}}}
	usd := rivalByKey(RunRivals(flows, DaysGrid("2026-07-15", "2026-08-31"), in), RivalUSDCash)
	if usd.Why != "" {
		t.Fatalf("курс є на всі дати, а суперник мовчить: %s", usd.Why)
	}
	near(t, usd.TerminalUAH, 12_500, "250 $ по 50")
}

// Валютний суперник не приносить відсотків: без руху курсу лінія стоїть.
//
// Купони й дивіденди — це те, що отримав ТИ натомість; нарахувати їх ще й
// супернику означало б порівняти портфель сам із собою.
func TestRivalFXEarnsNothingByItself(t *testing.T) {
	flows := []Contribution{{On: "2026-07-15", UAH: 10_000}}
	in := RivalInputs{USD: flat("2026-07-01", 40)}
	usd := rivalByKey(RunRivals(flows, DaysGrid("2026-07-15", "2026-08-31"), in), RivalUSDCash)
	near(t, usd.TerminalUAH, 10_000, "курс не рухався — і вартість теж")
}

// Пропущений курс глушить СУПЕРНИКА ЦІЛКОМ, а не окремий внесок.
//
// Це та сама вада, від якої правило «все або нічого» з'явилось у зведеному
// XIRR: тихо пропущена КУПІВЛЯ лишає термінал на місці й задирає результат
// у стелю. Число з діркою гірше за мовчання, бо виглядає однаково.
func TestRivalFXSilentOnMissingRate(t *testing.T) {
	flows := []Contribution{
		{On: "2026-07-15", UAH: 10_000}, // курс є
		{On: "2026-06-01", UAH: 10_000}, // курсу ще немає
	}
	in := RivalInputs{USD: flat("2026-07-01", 40)}
	usd := rivalByKey(RunRivals(flows, DaysGrid("2026-06-01", "2026-08-31"), in), RivalUSDCash)
	if usd.Why == "" {
		t.Fatal("внесок без курсу мусив замовкнути суперника")
	}
	if len(usd.Points) != 0 || usd.TerminalUAH != 0 {
		t.Errorf("мовчазний суперник не має віддавати чисел: точок %d, термінал %.2f",
			len(usd.Points), usd.TerminalUAH)
	}
}

// Ринкова ОВДП росте за рівнем розміщення на дату внеску, ACT/365.
func TestRivalOVDPGrowsAtAuctionLevel(t *testing.T) {
	flows := []Contribution{{On: "2026-07-15", UAH: 100_000}}
	in := RivalInputs{OVDP: flat("2026-01-01", 0.15)}
	days := DaysGrid("2026-07-15", "2027-07-15")
	ovdp := rivalByKey(RunRivals(flows, days, in), RivalOVDPMarket)
	if ovdp.Why != "" {
		t.Fatalf("рівень є, а суперник мовчить: %s", ovdp.Why)
	}
	near(t, ovdp.Points[0], 100_000, "у день покупки — сама сума")
	near(t, ovdp.TerminalUAH, 115_000, "рівно рік під 15%")
}

// На погашенні тіло з відсотками перекладається в рівень ТОГО дня, а не
// того, під який купувалось.
//
// Без цього суперник назавжди застигав би на ставці першого дня — тобто
// був би не «ринком», а «ринком одного разу».
func TestRivalOVDPRollsAtMaturity(t *testing.T) {
	flows := []Contribution{{On: "2026-07-15", UAH: 100_000}}
	in := RivalInputs{OVDP: Quotes{
		{On: "2026-01-01", V: 0.15},
		{On: "2027-07-15", V: 0.05}, // рівень упав рівно на погашенні
	}}
	days := DaysGrid("2026-07-15", "2028-07-14")
	ovdp := rivalByKey(RunRivals(flows, days, in), RivalOVDPMarket)
	if ovdp.Why != "" {
		t.Fatalf("рівень є на обидві дати, а суперник мовчить: %s", ovdp.Why)
	}
	// 100 000 × 1.15 = 115 000 на погашенні, далі ще рік під 5%.
	near(t, ovdp.TerminalUAH, 115_000*1.05, "другий рік уже за новим рівнем")
}

// Зняття ріже всі відкриті папери пропорційно — вторинної ціни застосунок
// не моделює, і вигадувати її заради суперника не можна.
func TestRivalOVDPWithdrawalScalesProportionally(t *testing.T) {
	flows := []Contribution{
		{On: "2026-07-15", UAH: 100_000},
		{On: "2026-07-16", UAH: -50_000},
	}
	in := RivalInputs{OVDP: flat("2026-01-01", 0.15)}
	days := DaysGrid("2026-07-15", "2027-07-16")
	ovdp := rivalByKey(RunRivals(flows, days, in), RivalOVDPMarket)
	// За день наросло мізер; після зняття лишилось ≈ половина, і за
	// решту року вона виросла на 15%.
	got := ovdp.TerminalUAH
	if got < 56_000 || got > 59_000 {
		t.Errorf("після зняття половини за рік мало вийти ≈57–58 тис., а маємо %.2f", got)
	}
}

// Рівень розміщення без історії глушить ринкового суперника так само, як
// пропущений курс — валютного.
func TestRivalOVDPSilentWithoutLevel(t *testing.T) {
	flows := []Contribution{{On: "2026-07-15", UAH: 100_000}}
	ovdp := rivalByKey(RunRivals(flows, DaysGrid("2026-07-15", "2026-08-31"), RivalInputs{}), RivalOVDPMarket)
	if ovdp.Why == "" {
		t.Fatal("без жодного рівня розміщення суперник мусив замовкнути")
	}
}

// Сітка днів суцільна й містить обидва кінці: на ній стоять і крива
// факту, і криві суперників, тож зсув на день читався б як розбіжність.
func TestDaysGridIsContinuousAndInclusive(t *testing.T) {
	g := DaysGrid("2026-07-15", "2026-07-18")
	if len(g) != 4 || g[0] != "2026-07-15" || g[3] != "2026-07-18" {
		t.Fatalf("сітка має бути [15..18], а маємо %v", g)
	}
	if DaysGrid("2026-07-18", "2026-07-15") != nil {
		t.Error("перевернутий проміжок — це не сітка з одного дня, а порожнеча")
	}
}
