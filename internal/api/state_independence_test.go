package api

import (
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

func indepInput(t *testing.T, expenses, target float64) projectionInput {
	t.Helper()
	set := sensSettings()
	if expenses > 0 {
		set.MonthlyExpensesUAH = &expenses
	}
	if target > 0 {
		set.IncomeTargetUAH = &target
	}
	in := forecastInput(t, set)
	in.ActualMonthly = 20_000
	in.IncomeMonthlyNow = 1_234.56
	return in
}

// TestDrawdownWithdrawPrefersSetting — «скільки знімати» окреме питання
// від «який дохід достатній»: жити можна й скромніше за ціль.
func TestDrawdownWithdrawPrefersSetting(t *testing.T) {
	in := indepInput(t, 50_000, 30_000)
	w := 12_000.0
	in.Settings.WithdrawMonthlyUAH = &w
	out := buildProjection(in).Drawdown
	if out == nil {
		t.Fatal("декумуляції немає")
	}
	if out.WithdrawUAH != 12_000 || out.WithdrawFrom != "setting" {
		t.Errorf("зняття %v (%s); очікували 12000 із налаштування",
			out.WithdrawUAH, out.WithdrawFrom)
	}
	fallback := buildProjection(indepInput(t, 50_000, 30_000)).Drawdown
	if fallback.WithdrawUAH != 50_000 || fallback.WithdrawFrom != "expenses" {
		t.Errorf("спад дав %v (%s); очікували 50000 із витрат",
			fallback.WithdrawUAH, fallback.WithdrawFrom)
	}
}

// TestDrawdownSeesFundCertificates — сертифікати доходять до картки «На
// скільки вистачить» і тримають зняття нарівні з готівкою.
//
// Сторож саме на ПРОВОДКУ, а не на модель. У домені це вже перевірено, а
// от чи дійшли кошики фондів із фази фондів у рукави декумуляції — ні:
// у багатій фікстурі сертифікатів на 10 000 при знятті 20 000/міс, тобто
// менш ніж пів місяця, і golden такої різниці не показує взагалі.
//
// Порівнюємо з ПОРОЖНІМ портфелем, а не з готівкою. Готівка тримає на
// місяць довше, і це правильно: вона реінвестується під ставку рукава, а
// сертифікат у моделі не дорожчає — приросту ціни ніхто не обіцяв. Я
// спершу написав тут рівність із готівкою й отримав червоне саме на цій
// різниці.
func TestDrawdownSeesFundCertificates(t *testing.T) {
	months := func(fill func(in *projectionInput)) int {
		in := indepInput(t, 50_000, 30_000)
		in.CashByCur = map[string]int64{}
		// Без знецінення, щоб число читалось прямо: зняття лишається
		// сталим і в номіналі, тож 12000 це рівно дванадцять знять.
		in.Deval = 0
		w := 1_000.0
		in.Settings.WithdrawMonthlyUAH = &w
		fill(&in)
		out := buildProjection(in).Drawdown
		if out == nil {
			t.Fatal("декумуляції немає")
		}
		return out.Months
	}
	dist := months(func(in *projectionInput) {
		in.DistByCur = map[string][]domain.Dist{
			money.UAH: {{Value: 12_000, Cost: 12_000}},
		}
	})
	accum := months(func(in *projectionInput) {
		in.AccumByCur = map[string][]domain.Accum{
			money.UAH: {{Value0: 12_000, Cost0: 12_000}},
		}
	})
	// 12000, які не ростуть і не платять, під 1000/міс — рівно дванадцять
	// повних знять, на тринадцятому вже нема з чого.
	if dist != 13 {
		t.Errorf("розподільні сертифікати протримали до місяця %d, очікували 13. "+
			"Одиниця означала б, що вони не дійшли до декумуляції", dist)
	}
	if accum != 13 {
		t.Errorf("накопичувальні сертифікати протримали до місяця %d, очікували 13", accum)
	}
}

// TestDrawdownBiggerWithdrawalLastsLess — більше зняття вичерпує портфель
// раніше.
//
// Нуль тут неможливий (перший непокритий місяць це вже 1), а −1 означає
// «не вичерпується», тобто НАЙДОВШЕ. Порівнювати його як число не можна.
func TestDrawdownBiggerWithdrawalLastsLess(t *testing.T) {
	small := 3_000.0
	in := indepInput(t, 50_000, 30_000)
	in.Settings.WithdrawMonthlyUAH = &small
	a := buildProjection(in).Drawdown.Months

	big := 30_000.0
	in2 := indepInput(t, 50_000, 30_000)
	in2.Settings.WithdrawMonthlyUAH = &big
	b := buildProjection(in2).Drawdown.Months

	rank := func(m int) int {
		if m == -1 {
			return 1 << 30 // не вичерпується — найдовше з можливого
		}
		return m
	}
	if rank(b) >= rank(a) {
		t.Errorf("зняття 30 000 вистачило на %d, а 3 000 — на %d", b, a)
	}
}

// TestDrawdownCoveredPctExplainsTheNumber — покриття рахується від того
// самого доходу, що в сусідніх картках.
func TestDrawdownCoveredPctExplainsTheNumber(t *testing.T) {
	in := indepInput(t, 50_000, 30_000) // IncomeMonthlyNow = 1234.56
	w := 12_345.6
	in.Settings.WithdrawMonthlyUAH = &w
	out := buildProjection(in).Drawdown
	if out.CoveredPct != 10 {
		t.Errorf("покриття %v%%, очікували 10 (1234.56 з 12345.60)", out.CoveredPct)
	}
}

// TestIndependenceTargetPrefersSetting — явна ціль доходу перемагає
// місячні витрати.
//
// Спад на витрати — найчастіша відповідь, але не єдина розумна:
// половина витрат теж ціль. Якщо спад мовчки затирає задане число,
// налаштування стає декоративним, і помітити це можна лише порівнявши
// дві картки очима.
func TestIndependenceTargetPrefersSetting(t *testing.T) {
	out := buildProjection(indepInput(t, 50_000, 30_000)).Independence
	if out == nil {
		t.Fatal("незалежності немає")
	}
	if out.TargetUAH != 30_000 || out.TargetFrom != "setting" {
		t.Errorf("ціль %v (%s); очікували 30000 із налаштування",
			out.TargetUAH, out.TargetFrom)
	}
	// Без явної цілі лишаються витрати — і документ каже це вголос.
	fallback := buildProjection(indepInput(t, 50_000, 0)).Independence
	if fallback.TargetUAH != 50_000 || fallback.TargetFrom != "expenses" {
		t.Errorf("спад дав %v (%s); очікували 50000 із витрат",
			fallback.TargetUAH, fallback.TargetFrom)
	}
}

// TestIndependenceNoTargetNoCard — без витрат і без цілі картки немає.
//
// Питання «коли дохід покриє життя» без жодного уявлення про те, скільки
// коштує життя, відповіді не має, і вигадувати її не будемо.
func TestIndependenceNoTargetNoCard(t *testing.T) {
	if out := buildProjection(indepInput(t, 0, 0)).Independence; out != nil {
		t.Errorf("картка зʼявилась без цілі й без витрат: %+v", out)
	}
}

// TestIndependenceSlowerPaceComesLater — менший внесок відсуває дату.
//
// Дві дати в картці існують саме заради цього порівняння, і якщо вони
// поміняються місцями, різниця почне читатись навпаки: «за фактом швидше,
// ніж за планом» виглядає як гарна новина.
func TestIndependenceSlowerPaceComesLater(t *testing.T) {
	in := indepInput(t, 50_000, 30_000)
	in.ActualMonthly = 1_000 // темп сильно нижчий за плановий
	out := buildProjection(in).Independence
	if out.PlanMonths <= 0 {
		t.Fatalf("за планом незалежність мала настати, маємо %d", out.PlanMonths)
	}
	// Нуль означає «не досягається за 60 років», тобто НАЙПІЗНІШЕ з
	// можливого, а не найраніше. Порівнювати його як число не можна:
	// 0 < 123 читалось би як «за фактом швидше», тобто протилежно.
	rank := func(m int) int {
		if m == 0 {
			return 1 << 30
		}
		return m
	}
	if rank(out.ActualMonths) <= rank(out.PlanMonths) {
		t.Errorf("за темпом 1 000 ₴/міс незалежність настає не пізніше (%d), "+
			"ніж за плановим внеском (%d)", out.ActualMonths, out.PlanMonths)
	}
}

// TestIndependenceIncomeNowMatchesDocument — «зараз» у картці той самий,
// що й у «Пасивному доході».
//
// Це два рядки на сусідніх картках, і рахувати їх двічі означало б
// завести другу відповідь на те саме питання — рівно та форма, з якої
// починались усі розходження в цьому застосунку.
func TestIndependenceIncomeNowMatchesDocument(t *testing.T) {
	in := indepInput(t, 50_000, 30_000)
	out := buildProjection(in).Independence
	if out.IncomeNowUAH != in.IncomeMonthlyNow {
		t.Errorf("дохід зараз %v, а у фазі доходу %v", out.IncomeNowUAH, in.IncomeMonthlyNow)
	}
}

// TestIndependenceCapitalOnlyWhenReached — капітал показується лише
// тоді, коли перетин узагалі настає.
//
// Інакше картка малювала б суму «на момент, якого не буде».
func TestIndependenceCapitalOnlyWhenReached(t *testing.T) {
	in := indepInput(t, 0, 1_000_000_000) // ціль, недосяжна за 60 років
	out := buildProjection(in).Independence
	if out == nil {
		t.Fatal("картка мала бути: ціль задана явно")
	}
	if out.PlanMonths != 0 {
		t.Fatalf("ціль мала лишитись недосяжною, маємо %d", out.PlanMonths)
	}
	if out.CapitalUAH != 0 {
		t.Errorf("капітал %v на момент, якого не буде", out.CapitalUAH)
	}
}
