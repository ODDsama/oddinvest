package api

import (
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// TestCapitalDeltaCountsNPFContributionOnce — внесок у пенсійний це переказ
// УСЕРЕДИНІ капіталу, а не зовнішні гроші.
//
// Доти buildCapitalDelta ходив по чотирьох журналах, і npf_ops був
// четвертим. Але внесок списується з рахунку (state_builder.go), а форма
// НПФ заводить парний рядок у deposits сама — тож ті самі гроші рахувались
// двічі. На бойовому це давало +1 601 ₴ до «зовнішніх грошей» місяця при
// капіталі, який від внесків не змінювався взагалі; та сама помилка
// приписувала суперникові в бенчмарку «Усі гроші» гроші, яких не було.
//
// Фікстура повторює бойову буквально: внесок і його «автопоповнення»
// однією датою, брокером і сумою.
func TestCapitalDeltaCountsNPFContributionOnce(t *testing.T) {
	now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	on := domain.NewDate(now.AddDate(0, 0, -5))
	ago := domain.NewDate(now.AddDate(0, 0, -30))

	src := &sources{
		capitalAgo: &store.Snapshot{Date: ago, AccountUAH: 100_000},
		deposits: []store.Deposit{
			{Date: on, Amount: 50_000, Currency: money.UAH, Broker: "пумб",
				Note: "автопоповнення: внесок у НПФ"},
		},
		npfOps: []domain.NPFOp{
			{NPFID: 1, Date: on, Amount: 50_000, Broker: "пумб"},
		},
	}
	out := buildCapitalDelta(src, 1500, fx.Rates{}, domain.NewDate(now))
	if out == nil {
		t.Fatal("дельти немає, хоч знімок є")
	}
	if out.ContribUAH.Major() != 500 {
		t.Errorf("зовнішні гроші %.2f, очікували 500 — внесок у НПФ уже порахований поповненням", out.ContribUAH.Major())
	}
}

// TestCapitalDeltaCountsThreeJournals — гаманець, подушка й цілі входять
// усі три, і переказ між кошиками дає нуль сам.
func TestCapitalDeltaCountsThreeJournals(t *testing.T) {
	now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	on := domain.NewDate(now.AddDate(0, 0, -5))
	ago := domain.NewDate(now.AddDate(0, 0, -30))

	src := &sources{
		capitalAgo: &store.Snapshot{Date: ago, AccountUAH: 100_000},
		deposits: []store.Deposit{
			{Date: on, Amount: 70_000, Currency: money.UAH, Broker: "mono"},
			// перша нога переказу гаманець → ціль
			{Date: on, Amount: -20_000, Currency: money.UAH, Broker: "mono"},
		},
		reserveOps: []store.ReserveOp{
			{Date: on, Amount: 30_000, Currency: money.UAH, Place: "сейф"},
		},
		goalOps: []store.GoalOp{
			// друга нога того самого переказу
			{GoalID: 1, Date: on, Amount: 20_000, Currency: money.UAH, Place: "готівка"},
		},
	}
	out := buildCapitalDelta(src, 2000, fx.Rates{}, domain.NewDate(now))
	if out == nil {
		t.Fatal("дельти немає, хоч знімок є")
	}
	if out.ContribUAH.Major() != 1000 {
		t.Errorf("зовнішні гроші %.2f, очікували 1000 (700 гаманець + 300 матрац; переказ у ціль дає нуль)", out.ContribUAH.Major())
	}
}

// Відсоток дельти — у валюті звітності: курс 40 → 44 за місяць зʼїдає
// весь гривневий ріст. Суми при цьому лишаються гривневими — їх перекладе
// презентер тими самими курсами.
func TestCapitalDeltaPctInReportCurrency(t *testing.T) {
	now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	ago := domain.NewDate(now.AddDate(0, 0, -30))
	src := &sources{
		capitalAgo: &store.Snapshot{Date: ago, AccountUAH: 100_000},
		report:     money.USD,
		fxHistory: map[string][]store.RatePoint{
			money.USD: {{Date: ago, RateE4: 400000}, {Date: today, RateE4: 440000}},
		},
	}
	out := buildCapitalDelta(src, 1100, fx.Rates{}, today)
	if out == nil {
		t.Fatal("дельти немає")
	}
	if out.DeltaPct != 0 {
		t.Errorf("у доларах ріст нульовий, дістали %v%%", out.DeltaPct)
	}
	if out.FromUAH != state.UAH(100_000) || out.DeltaUAH != state.UAH(10_000) {
		t.Errorf("суми мусять лишитись гривневими: %v / %v", out.FromUAH, out.DeltaUAH)
	}
	src.report = money.UAH
	if out := buildCapitalDelta(src, 1100, fx.Rates{}, today); out.DeltaPct != 10 {
		t.Errorf("у гривні +10%%, дістали %v%%", out.DeltaPct)
	}
}
