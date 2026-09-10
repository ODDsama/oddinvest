package state

import (
	"math"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Вирок «встигаю» проти МАЙБУТНЬОЇ ціни.
//
// Доти обидві сторони порівняння стояли в сьогоднішніх грошах, а дедлайн
// був у майбутньому: внутрішньо все сходилось, не сходились ДАТИ. На
// десятирічній цілі це давало «темп тримається» там, де насправді
// бракуватиме більш як половини.

// paceGoal — одна ціль із дедлайном через N місяців, зібраним і ставкою.
// Дні через 30.44, бо саме так deriveGoalPace рахує місяці назад.
func paceGoal(months int, collected, actual, ratePct float64, cur string) DeriveInput {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	due := today.AddDays(int(math.Round(float64(months) * 30.44)))
	return DeriveInput{
		Now: now,
		Goals: []GoalInput{{
			ID: 1, Name: "Школа", Currency: cur,
			TargetNative: Major(600_000, cur), TargetUAH: Major(600_000, cur),
			CollectedNative: Major(collected, cur), CollectedUAH: Major(collected, cur),
			DueDate:   string(due),
			ActualUAH: Major(actual, cur), ActualNative: Major(actual, cur),
			RatePct: ratePct,
		}},
		InflationPct: 10.7,
	}
}

func oneGoal(t *testing.T, in DeriveInput) Goal {
	t.Helper()
	doc := &Doc{}
	deriveGoals(doc, in)
	if len(doc.Goals) != 1 {
		t.Fatalf("цілей %d, чекали одну", len(doc.Goals))
	}
	return doc.Goals[0]
}

// TestGoalPaceMeasuredAgainstFuturePrice — потрібний темп проти майбутньої
// ціни СТРОГО більший за темп проти сьогоднішньої, і саме він виносить
// вирок.
//
// Найдорожча гілка всієї фази: посередині між двома числами лежить темп,
// на якому старий застосунок казав «тримається», а грошей на дедлайн не
// вистачало б.
func TestGoalPaceMeasuredAgainstFuturePrice(t *testing.T) {
	// 120 місяців, нічого не зібрано, гроші лежать під нуль.
	g := oneGoal(t, paceGoal(120, 0, 0, 0, money.UAH))
	if g.RequiredUAH.Major() <= 0 || g.RequiredFutureUAH.Major() <= 0 {
		t.Fatalf("темпів немає: сьогоднішній %.2f, майбутній %.2f", g.RequiredUAH.Major(), g.RequiredFutureUAH.Major())
	}
	if g.RequiredFutureUAH.Cmp(g.RequiredUAH) <= 0 {
		t.Errorf("майбутній темп %.2f не більший за сьогоднішній %.2f — інфляція загубилась",
			g.RequiredFutureUAH.Major(), g.RequiredUAH.Major())
	}
	if g.RequiredPaceUAH() != g.RequiredFutureUAH.Major() {
		t.Errorf("вирок стоїть на %.2f, а мав би на майбутньому темпі %.2f",
			g.RequiredPaceUAH(), g.RequiredFutureUAH.Major())
	}

	// Темп РІВНО МІЖ двома числами: старий вирок сказав би «встигаю».
	mid := (g.RequiredUAH.Major() + g.RequiredFutureUAH.Major()) / 2
	g2 := oneGoal(t, paceGoal(120, 0, mid, 0, money.UAH))
	if !g2.Behind {
		t.Errorf("темп %.2f перевищує сьогоднішній %.2f, але до майбутньої ціни "+
			"%.2f не дотягує — а застосунок каже «встигаю»",
			mid, g2.RequiredUAH.Major(), g2.RequiredFutureUAH.Major())
	}
	// І навпаки: темп понад майбутній — не відставання.
	g3 := oneGoal(t, paceGoal(120, 0, g.RequiredFutureUAH.Major()*1.01, 0, money.UAH))
	if g3.Behind {
		t.Error("темп понад майбутній вважається відставанням — вирок став невиконанним")
	}
}

// TestGoalOnDepositNeedsLess — ціль, чиї гроші працюють, вимагає меншого
// темпу, ніж та сама ціль у шухляді.
//
// Це і є арифметичне виправдання самого вкладу під ціль (0062): без
// ставки в рівнянні застосунок вимагав би однакового внеску від обох, і
// вклад лишався б числом, якого ніхто не помічає.
func TestGoalOnDepositNeedsLess(t *testing.T) {
	cash := oneGoal(t, paceGoal(120, 50_000, 0, 0, money.UAH))
	dep := oneGoal(t, paceGoal(120, 50_000, 0, 13.09, money.UAH))
	if dep.RequiredFutureUAH.Cmp(cash.RequiredFutureUAH) >= 0 {
		t.Errorf("ціль на вкладі вимагає %.2f, готівкою %.2f — ставка не дійшла до рівняння",
			dep.RequiredFutureUAH.Major(), cash.RequiredFutureUAH.Major())
	}
	// Розрив теж менший: зібране встигає вирости.
	if dep.GapFutureUAH.Cmp(cash.GapFutureUAH) >= 0 {
		t.Errorf("розрив на вкладі %.2f не менший за готівковий %.2f — зібране не росте",
			dep.GapFutureUAH.Major(), cash.GapFutureUAH.Major())
	}
}

// TestZeroRateGoalKeepsTheOldArithmetic — при нульовій ставці нова формула
// збігається зі старою до копійки.
//
// Сторож проти тихого зсуву всіх наявних цілей: вони лежать готівкою, і
// жодне їхнє число не мало права зрушити від самої появи ануїтету. Стара
// формула — розрив, поділений на місяці.
func TestZeroRateGoalKeepsTheOldArithmetic(t *testing.T) {
	g := oneGoal(t, paceGoal(60, 100_000, 0, 0, money.UAH))
	// Місяці — ЦІЛІ, ті самі, якими рахується майбутня ціна. Доти тут
	// стояли дробові MonthsLeft, тобто ціну проєктували на 60 місяців, а
	// ділили на 60.01 — розбіжність у копійки, але в різних одиницях.
	// Ануїтет прибрав її заразом, і саме тому порівняння тут із round().
	want := g.GapFutureUAH.Major() / math.Round(g.MonthsLeft)
	if math.Abs(g.RequiredFutureUAH.Major()-want) > 0.02 {
		t.Errorf("при нульовій ставці темп %.2f, а проста формула дає %.2f — "+
			"ануїтет розійшовся з тим, що було", g.RequiredFutureUAH.Major(), want)
	}
	// І зібране справді не виросло: розрив = майбутня ціна мінус те, що є.
	if math.Abs(g.GapFutureUAH.Major()-(g.TargetFutureNative.Major()-100_000)) > 0.02 {
		t.Errorf("розрив %.2f при ціні %.2f — гроші під нуль десь підросли",
			g.GapFutureUAH.Major(), g.TargetFutureNative.Major())
	}
}

// TestForeignGoalKeepsTodayPace — валютна ціль лишається на сьогоднішньому
// темпі й не отримує жодного майбутнього числа.
//
// Це не недогляд, а межа: індексу цін країни валюти в застосунку немає, а
// підставити туди українську інфляцію означало б сказати, що долар
// дорожчає, як гривня.
func TestForeignGoalKeepsTodayPace(t *testing.T) {
	g := oneGoal(t, paceGoal(120, 0, 0, 0, money.USD))
	if g.RequiredFutureUAH.Major() != 0 || g.GapFutureUAH.Major() != 0 || g.TargetFutureNative.Major() != 0 {
		t.Errorf("валютна ціль дістала майбутні числа: %+v", g)
	}
	if g.RequiredPaceUAH() != g.RequiredUAH.Major() {
		t.Errorf("вирок валютної цілі стоїть на %.2f замість сьогоднішнього %.2f",
			g.RequiredPaceUAH(), g.RequiredUAH.Major())
	}
}

// TestGoalETAAgreesWithTheVerdict — «збереться такого-то» й «відстаю» не
// можуть суперечити одне одному.
//
// Спіймано живцем на екрані, а не тестом: поруч стояли «за нинішнім
// темпом збереться 2035-10» і «⚠ до 2036-09 не збереться». Обидва рядки
// поодинці були правильні — перший ділив СЬОГОДНІШНІЙ розрив на темп,
// другий міряв майбутню ціну, — і саме тому суперечність читалась як
// поломка розрахунку, а не як два різні питання.
func TestGoalETAAgreesWithTheVerdict(t *testing.T) {
	// Темп між двома потрібними: старої лінійки вистачає, нової — ні.
	base := oneGoal(t, paceGoal(120, 55_000, 0, 13.16, money.UAH))
	mid := (base.RequiredUAH.Major() + base.RequiredFutureUAH.Major()) / 2
	g := oneGoal(t, paceGoal(120, 55_000, mid, 13.16, money.UAH))
	if !g.Behind {
		t.Fatalf("темп %.2f мав би не дотягувати до %.2f — тест нічого не перевіряє",
			mid, g.RequiredFutureUAH.Major())
	}
	if g.ETADate == "" {
		t.Fatal("дати немає зовсім — при живому темпі ціль колись та збереться")
	}
	if g.ETADate <= g.DueDate {
		t.Errorf("картка каже «відстаю», а поруч обіцяє %s — не пізніше за дедлайн %s",
			g.ETADate, g.DueDate)
	}

	// І дзеркало: темпу вистачає — дата не може бути пізнішою за дедлайн.
	ok := oneGoal(t, paceGoal(120, 55_000, base.RequiredFutureUAH.Major()*1.05, 13.16, money.UAH))
	if ok.Behind {
		t.Fatalf("темп понад потрібний вважається відставанням")
	}
	if ok.ETADate == "" || ok.ETADate > ok.DueDate {
		t.Errorf("темпу вистачає, а дата %s пізніша за дедлайн %s", ok.ETADate, ok.DueDate)
	}
}
