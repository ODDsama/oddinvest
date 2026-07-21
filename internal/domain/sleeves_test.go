package domain

import (
	"math"
	"testing"
)

func uahSleeve(cash, nominal, rate, contrib float64) Sleeve {
	return Sleeve{Currency: "UAH", Cash0: cash, Nominal0: nominal, RatePct: rate,
		ContribUAH: contrib, Rate0: 1,
		Coupon: map[int]float64{}, Redeem: map[int]float64{}}
}

func usdSleeve(cash, nominal, rate, contribUAH, rate0 float64) Sleeve {
	return Sleeve{Currency: "USD", Cash0: cash, Nominal0: nominal, RatePct: rate,
		ContribUAH: contribUAH, Rate0: rate0,
		Coupon: map[int]float64{}, Redeem: map[int]float64{}}
}

func approx(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s: маємо %.4f, очікували %.4f (допуск %.4f)", name, got, want, tol)
	}
}

// Без девальвації нова багатовалютна симуляція має збігатися зі старою
// однорукавною — інакше ми тихо змінили результат усім, хто не вмикав
// девальвацію.
func TestSleevesMatchSingleCurrencyWhenNoDevaluation(t *testing.T) {
	coupon := map[int]float64{6: 500, 12: 500}
	redeem := map[int]float64{12: 10000}
	s := uahSleeve(1000, 10000, 15, 5000)
	s.Threshold = 1000
	s.Coupon, s.Redeem = coupon, redeem

	got := ProjectSleeves([]Sleeve{s}, 0, 24)
	want := ProjectCapital(1000, 10000, 5000, 1000, 15, coupon, redeem, 24)

	approx(t, "TodayUAH", got.TodayUAH, want, 0.01)
	approx(t, "NominalUAH", got.NominalUAH, want, 0.01)
}

// Девальвація не має чіпати номінальну гривню — вона змінює лише те,
// СКІЛЬКИ ця гривня купує.
func TestDevaluationHitsRealNotNominal(t *testing.T) {
	s := uahSleeve(0, 0, 0, 1000)
	noDev := ProjectSleeves([]Sleeve{s}, 0, 12)
	withDev := ProjectSleeves([]Sleeve{s}, 10, 12)

	approx(t, "номінал не змінився", withDev.NominalUAH, noDev.NominalUAH, 0.01)
	if withDev.TodayUAH >= noDev.TodayUAH {
		t.Errorf("реальна вартість мала впасти: %.2f vs %.2f", withDev.TodayUAH, noDev.TodayUAH)
	}
	// 12 000 ₴ внесків за рік при 10% знеціненні коштують 12000/1.1 ≈ 10 909
	approx(t, "реальна вартість", withDev.TodayUAH, 12000/1.1, 1.0)
}

// Долар свою купівельну спроможність тримає: у сьогоднішніх гривнях
// доларовий рукав від девальвації не страждає. Страждають лише гривневі
// поповнення, які щомісяця купують менше валюти.
func TestHardCurrencyKeepsRealValue(t *testing.T) {
	// без поповнень: чистий запас валюти
	s := usdSleeve(100, 0, 0, 0, 40)
	noDev := ProjectSleeves([]Sleeve{s}, 0, 12)
	withDev := ProjectSleeves([]Sleeve{s}, 10, 12)
	approx(t, "реальна вартість валюти", withDev.TodayUAH, noDev.TodayUAH, 0.01)
	approx(t, "у сьогоднішніх ₴", withDev.TodayUAH, 4000, 0.01)
	// а в номінальних гривнях валюта дорожчає рівно на девальвацію
	approx(t, "номінально", withDev.NominalUAH, 4000*1.1, 1.0)

	// з поповненнями: 4000 ₴/міс купують дедалі менше доларів
	sc := usdSleeve(0, 0, 0, 4000, 40)
	devContrib := ProjectSleeves([]Sleeve{sc}, 10, 12)
	flatContrib := ProjectSleeves([]Sleeve{sc}, 0, 12)
	if devContrib.ByCurrency["USD"] >= flatContrib.ByCurrency["USD"] {
		t.Errorf("за девальвації гривневий внесок мав купити менше $: %.2f vs %.2f",
			devContrib.ByCurrency["USD"], flatContrib.ByCurrency["USD"])
	}
}

// Головне, заради чого все затівалось: девальвація має перевертати
// перевагу валют. Гривневі 16% проти доларових 4% — реальна дохідність
// гривні це (1+0.16)/(1+d)−1, тож точка беззбитковості лежить на
// d = 1.16/1.04 − 1 ≈ 11.5%. Нижче неї гривня ще виграє, вище — програє;
// тест закріплює саме цю поведінку, а не «долар завжди кращий».
func TestDevaluationFlipsCurrencyPreference(t *testing.T) {
	const months = 120
	// однаковий старт: 100 000 ₴ = $2 500 за курсом 40
	uah := uahSleeve(100000, 0, 16, 0)
	usd := usdSleeve(2500, 0, 4, 0, 40)

	gap := func(devalPct float64) float64 {
		return ProjectSleeves([]Sleeve{uah}, devalPct, months).TodayUAH -
			ProjectSleeves([]Sleeve{usd}, devalPct, months).TodayUAH
	}
	if gap(0) <= 0 {
		t.Fatalf("без девальвації гривня має вигравати, перевага %.0f", gap(0))
	}
	if gap(10) <= 0 {
		t.Errorf("на 10%% (нижче беззбитковості ~11.5%%) гривня ще має вигравати, перевага %.0f", gap(10))
	}
	if gap(10) >= gap(0) {
		t.Errorf("девальвація має з'їдати перевагу гривні: %.0f -> %.0f", gap(0), gap(10))
	}
	if gap(15) >= 0 {
		t.Errorf("на 15%% (вище беззбитковості) долар мав обігнати гривню, перевага %.0f", gap(15))
	}
}

// Долари й сьогоднішні гривні — це одні й ті самі числа в різних
// одиницях, тож перемикач в UI не має нічого перераховувати.
func TestUSDViewIsSameNumberDifferentUnit(t *testing.T) {
	r := ProjectSleeves([]Sleeve{uahSleeve(0, 0, 0, 4200)}, 6, 12)
	approx(t, "перемикання в $", r.InTodayUSD(42), r.TodayUAH/42, 1e-9)
	if r.InTodayUSD(0) != 0 {
		t.Error("без курсу маємо повертати 0, а не ділити на нуль")
	}
}

func TestMonthsToReachSleeves(t *testing.T) {
	s := uahSleeve(0, 0, 0, 1000)
	// без відсотків і девальвації 10 000 ₴ назбираються рівно за 10 міс
	if got := MonthsToReachSleeves([]Sleeve{s}, 0, 10000, 60); got != 10 {
		t.Errorf("без девальвації: маємо %d, очікували 10", got)
	}
	// з девальвацією реальна вартість росте повільніше — треба довше
	withDev := MonthsToReachSleeves([]Sleeve{s}, 10, 10000, 60)
	if withDev <= 10 {
		t.Errorf("за девальвації мало знадобитись більше 10 міс, маємо %d", withDev)
	}
	if got := MonthsToReachSleeves([]Sleeve{uahSleeve(50000, 0, 0, 0)}, 0, 10000, 60); got != -1 {
		t.Errorf("вже досягнуту ціль мали позначити -1, маємо %d", got)
	}
	if got := MonthsToReachSleeves([]Sleeve{uahSleeve(0, 0, 0, 1)}, 0, 1e9, 60); got != 0 {
		t.Errorf("недосяжну ціль мали позначити 0, маємо %d", got)
	}
}

// Підібраний внесок має справді виводити на ціль — перевіряємо симуляцією,
// а не довіряємо бісекції на слово.
func TestRequiredMonthlySleevesHitsGoal(t *testing.T) {
	sleeves := []Sleeve{
		uahSleeve(1000, 5000, 15, 3000),
		usdSleeve(0, 100, 4, 2000, 42),
	}
	const goal, months = 500000.0, 60
	need := RequiredMonthlySleeves(sleeves, 6, goal, months)
	if need <= 0 {
		t.Fatalf("бісекція повернула %.2f", need)
	}
	// пропорції рукавів зберігаються: 3000:2000 = 60:40
	scaled := make([]Sleeve, len(sleeves))
	copy(scaled, sleeves)
	scaled[0].ContribUAH = need * 0.6
	scaled[1].ContribUAH = need * 0.4
	got := ProjectSleeves(scaled, 6, months).TodayUAH
	approx(t, "підібраний внесок виводить на ціль", got, goal, goal*0.001)
}

// Колонки «внесено» і «за планом» мають жити в одній одиниці — інакше
// таблиця віднімає номінальні гроші від реальних і на коротких
// горизонтах малює від'ємний приріст при цілком здоровому портфелі.
func TestRealContributedSharesUnitWithProjection(t *testing.T) {
	const start, contrib, months = 8525.24, 5000.0, 12

	// без знецінення реальне = номінальне
	approx(t, "без знецінення", RealContributed(start, contrib, 0, months),
		start+contrib*months, 0.01)

	// зі знеціненням відкладені гроші коштують менше
	real6 := RealContributed(start, contrib, 6, months)
	if real6 >= start+contrib*months {
		t.Errorf("під матрацом гроші мали втратити вартість: %.2f", real6)
	}

	// і головне: портфель, що обганяє знецінення, дає ДОДАТНИЙ приріст
	// на всіх горизонтах, а не лише на далеких
	s := uahSleeve(start, 0, 16.7, contrib)
	for _, m := range []int{12, 36, 60, 120} {
		planned := ProjectSleeves([]Sleeve{s}, 6, m).TodayUAH
		mattress := RealContributed(start, contrib, 6, m)
		if planned <= mattress {
			t.Errorf("%d міс: 16.7%% проти 6%% знецінення мали дати приріст, маємо %.2f vs %.2f",
				m, planned, mattress)
		}
	}
}

// Ставка не вічна: сьогоднішні воєнні 16-17% мають сповзати до
// довгострокового рівня, інакше модель малює капітал, якого не буде.
func TestRateGlidesToTerminal(t *testing.T) {
	s := uahSleeve(0, 0, 16.7, 0)
	s.RateTerminalPct, s.GlideYears = 11, 5

	approx(t, "старт", s.rateAt(0), 16.7, 0.01)
	approx(t, "середина спуску", s.rateAt(30), (16.7+11)/2, 0.01)
	approx(t, "кінець спуску", s.rateAt(60), 11, 0.01)
	approx(t, "після спуску тримається", s.rateAt(240), 11, 0.01)

	// GlideYears = 0 лишає стару поведінку — вічну ставку
	flat := uahSleeve(0, 0, 16.7, 0)
	approx(t, "без спуску", flat.rateAt(240), 16.7, 0.01)

	// і на капіталі це має бути видно: спуск дає менше, ніж вічні 16.7%
	withGlide := ProjectSleeves([]Sleeve{s}, 0, 120).TodayUAH
	s2 := s
	s2.Cash0, flat.Cash0 = 100000, 100000
	withGlide = ProjectSleeves([]Sleeve{s2}, 0, 120).TodayUAH
	forever := ProjectSleeves([]Sleeve{flat}, 0, 120).TodayUAH
	if withGlide >= forever {
		t.Errorf("спуск ставки мав дати менший капітал: %.0f vs %.0f", withGlide, forever)
	}
}
