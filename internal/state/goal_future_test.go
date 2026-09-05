package state

import (
	"testing"

	money "github.com/Rhymond/go-money"
)

func goalRow(cur string, months float64) Goal {
	return Goal{
		Currency: cur, TargetNative: 600000, CollectedNative: 100000,
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
	if g.TargetFutureNative < 1_500_000 || g.TargetFutureNative > 1_600_000 {
		t.Fatalf("майбутня ціна %v", g.TargetFutureNative)
	}
	if g.GapFutureNative != round2(g.TargetFutureNative-100000) {
		t.Fatalf("розрив %v не дорівнює майбутній ціні мінус зібране", g.GapFutureNative)
	}
	if g.RequiredFutureNative <= 0 {
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
	g.GapNative, g.RequiredNative, g.DonePct = 500000, 4166.67, 16.7
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
		if g.TargetFutureNative != 0 || g.InflationPct != 0 {
			t.Fatalf("%s: майбутні числа зʼявились там, де їх нема з чого рахувати: %+v", cur, g)
		}
	}
}

// TestGoalFutureSilentWithoutCPIOrDeadline — мовчить, а не показує нулі:
// нуль тут читався б як «інфляції немає» або «ціль не подорожчає».
func TestGoalFutureSilentWithoutCPIOrDeadline(t *testing.T) {
	noCPI := goalRow(money.UAH, 120)
	deriveGoalFuture(&noCPI, 0)
	if noCPI.TargetFutureNative != 0 {
		t.Fatalf("без ряду ІСЦ зʼявилось число %v", noCPI.TargetFutureNative)
	}

	noDue := goalRow(money.UAH, 0)
	deriveGoalFuture(&noDue, 10)
	if noDue.TargetFutureNative != 0 {
		t.Fatalf("без дедлайну зʼявилось число %v", noDue.TargetFutureNative)
	}
}

// TestGoalFutureGapFloorsAtZero — зібраного більше за майбутню ціну:
// розриву немає, і темпу теж.
func TestGoalFutureGapFloorsAtZero(t *testing.T) {
	g := goalRow(money.UAH, 12)
	g.CollectedNative = 10_000_000
	deriveGoalFuture(&g, 10)
	if g.GapFutureNative != 0 || g.RequiredFutureNative != 0 {
		t.Fatalf("перезібрана ціль дала розрив %v і темп %v",
			g.GapFutureNative, g.RequiredFutureNative)
	}
	if g.TargetFutureNative == 0 {
		t.Fatal("майбутня ціна мусить лишатись — вона й пояснює, чому зібраного досить")
	}
}
