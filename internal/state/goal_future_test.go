package state

import (
	"testing"

	money "github.com/Rhymond/go-money"
)

func goalRow(cur string, months float64) Goal {
	return Goal{
		Currency: cur, TargetNative: Major(600000, cur), CollectedNative: Major(100000, cur),
		MonthsLeft: months, DueDate: "2036-09-01",
	}
}

// TestGoalFutureGrowsTargetAndGap — головна перевірка: ціль, задана в
// сьогоднішніх грошах, у рік дедлайну коштує більше, і розрив рахується
// від НЕЇ, а не від сьогоднішньої ціни.
func TestGoalFutureGrowsTargetAndGap(t *testing.T) {
	g := goalRow(money.UAH, 120)
	deriveGoalFuture(&g, 10)

	// 600 000 × 1.1^10 ≈ 1 556 246.
	if g.TargetFutureNative.Major() < 1_500_000 || g.TargetFutureNative.Major() > 1_600_000 {
		t.Fatalf("майбутня ціна %v", g.TargetFutureNative.Major())
	}
	if g.GapFutureNative.Major() != round2(g.TargetFutureNative.Major()-100000) {
		t.Fatalf("розрив %v не дорівнює майбутній ціні мінус зібране", g.GapFutureNative.Major())
	}
	if g.RequiredFutureNative.Major() <= 0 {
		t.Fatal("потрібний темп до майбутньої ціни не порахувався")
	}
	if g.InflationPct != 10 {
		t.Fatalf("темп, яким рахували, не названий: %v", g.InflationPct)
	}
}

// TestGoalFutureLeavesTodayNumbersAlone — старі числа не чіпаються: на
// них стоять черга задач, стеля наповнення й прогноз.
func TestGoalFutureLeavesTodayNumbersAlone(t *testing.T) {
	g := goalRow(money.UAH, 120)
	g.GapNative, g.RequiredNative, g.DonePct = Major(500000, g.Currency), Major(4166.67, g.Currency), 16.7
	before := g
	deriveGoalFuture(&g, 10)

	if g.GapNative != before.GapNative || g.RequiredNative != before.RequiredNative ||
		g.DonePct != before.DonePct {
		t.Fatalf("сьогоднішні числа зрушили: %+v проти %+v", g, before)
	}
}

// TestGoalFutureOnlyForUAHGoals — валютній цілі потрібен індекс цін
// країни валюти, якого в застосунку немає й нізвідки взяти.
func TestGoalFutureOnlyForUAHGoals(t *testing.T) {
	for _, cur := range []string{money.USD, money.EUR} {
		g := goalRow(cur, 120)
		deriveGoalFuture(&g, 10)
		if g.TargetFutureNative.Major() != 0 || g.InflationPct != 0 {
			t.Fatalf("%s: майбутні числа зʼявились там, де їх нема з чого рахувати: %+v", cur, g)
		}
	}
}

// TestGoalFutureSilentWithoutCPIOrDeadline — мовчить, а не показує нулі:
// нуль тут читався б як «інфляції немає» або «ціль не подорожчає».
func TestGoalFutureSilentWithoutCPIOrDeadline(t *testing.T) {
	noCPI := goalRow(money.UAH, 120)
	deriveGoalFuture(&noCPI, 0)
	if noCPI.TargetFutureNative.Major() != 0 {
		t.Fatalf("без ряду ІСЦ зʼявилось число %v", noCPI.TargetFutureNative.Major())
	}

	noDue := goalRow(money.UAH, 0)
	deriveGoalFuture(&noDue, 10)
	if noDue.TargetFutureNative.Major() != 0 {
		t.Fatalf("без дедлайну зʼявилось число %v", noDue.TargetFutureNative.Major())
	}
}

// TestGoalFutureGapFloorsAtZero — зібраного більше за майбутню ціну:
// розриву немає, і темпу теж.
func TestGoalFutureGapFloorsAtZero(t *testing.T) {
	g := goalRow(money.UAH, 12)
	g.CollectedNative = Major(10_000_000, g.Currency)
	deriveGoalFuture(&g, 10)
	if g.GapFutureNative.Major() != 0 || g.RequiredFutureNative.Major() != 0 {
		t.Fatalf("перезібрана ціль дала розрив %v і темп %v",
			g.GapFutureNative.Major(), g.RequiredFutureNative.Major())
	}
	if g.TargetFutureNative.Major() == 0 {
		t.Fatal("майбутня ціна мусить лишатись — вона й пояснює, чому зібраного досить")
	}
}
