package engine

import (
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// TestCapitalDeltaCountsNPFContributionOnce — внесок у пенсійний рахується
// зовнішніми грошима рівно РАЗ.
//
// Доти внесок жив двома рядками: самим npf_ops і парним «автопоповненням»
// рахунку, яке форма НПФ заводила в deposits, — і buildCapitalDelta, що
// ходив по обох журналах, рахував ті самі гроші двічі. На бойовому це
// давало +1 601 ₴ до «зовнішніх грошей» місяця; та сама помилка
// приписувала суперникові в бенчмарку «Усі гроші» гроші, яких не було.
// Рахунків тепер немає, і внесок — одна покупка на межі інструмента
// (state_flows.go); сторож тримає, щоб другий шлях до тих самих грошей не
// повернувся.
func TestCapitalDeltaCountsNPFContributionOnce(t *testing.T) {
	now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	on := domain.NewDate(now.AddDate(0, 0, -5))
	ago := domain.NewDate(now.AddDate(0, 0, -30))

	src := &sources{
		capitalAgo: &store.Snapshot{Date: ago, NominalUAHEq: 100_000},
		npfOps: []domain.NPFOp{
			{NPFID: 1, Date: on, Amount: 50_000, Broker: "пумб"},
		},
	}
	out := buildCapitalDelta(src, 1500, 0, fx.Rates{}, domain.NewDate(now))
	if out == nil {
		t.Fatal("дельти немає, хоч знімок є")
	}
	if out.ContribUAH.Major() != 500 {
		t.Errorf("зовнішні гроші %.2f, очікували 500 — внесок у НПФ рахується один раз", out.ContribUAH.Major())
	}
}

// TestCapitalDeltaCountsThreeJournals — інструменти, подушка й цілі входять
// усі три, і переказ між кошиками дає нуль сам.
func TestCapitalDeltaCountsThreeJournals(t *testing.T) {
	now := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	on := domain.NewDate(now.AddDate(0, 0, -5))
	ago := domain.NewDate(now.AddDate(0, 0, -30))

	src := &sources{
		capitalAgo: &store.Snapshot{Date: ago, NominalUAHEq: 100_000},
		fundOps: []domain.FundOp{
			{ID: 1, Date: on, Fund: "REIT", Kind: domain.FundBuy, Qty: 70, Amount: 70_000, Currency: money.UAH},
			// перша нога переказу інструмент → ціль: дивіденд вийшов до власника
			{ID: 2, Date: on, Fund: "REIT", Kind: domain.FundDividend, Amount: 20_000, Currency: money.UAH},
		},
		reserveOps: []store.ReserveOp{
			{Date: on, Amount: 30_000, Currency: money.UAH, Place: "сейф"},
		},
		goalOps: []store.GoalOp{
			// друга нога того самого переказу
			{GoalID: 1, Date: on, Amount: 20_000, Currency: money.UAH, Place: "готівка"},
		},
	}
	out := buildCapitalDelta(src, 2000, 0, fx.Rates{}, domain.NewDate(now))
	if out == nil {
		t.Fatal("дельти немає, хоч знімок є")
	}
	if out.ContribUAH.Major() != 1000 {
		t.Errorf("зовнішні гроші %.2f, очікували 1000 (700 фонд + 300 матрац; переказ у ціль дає нуль)", out.ContribUAH.Major())
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
		capitalAgo: &store.Snapshot{Date: ago, NominalUAHEq: 100_000},
		report:     money.USD,
		fxHistory: map[string][]store.RatePoint{
			money.USD: {{Date: ago, RateE4: 400000}, {Date: today, RateE4: 440000}},
		},
	}
	out := buildCapitalDelta(src, 1100, 0, fx.Rates{}, today)
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
	if out := buildCapitalDelta(src, 1100, 0, fx.Rates{}, today); out.DeltaPct != 10 {
		t.Errorf("у гривні +10%%, дістали %v%%", out.DeltaPct)
	}
}

// Облігації в капіталі — «номінал + накопичений купон» (0063). Знімок,
// старший за колонку купона (−1), мусить порівнюватись із сьогоднішнім
// капіталом БЕЗ купона: інакше перші тридцять днів «за 30 днів» показувало
// б увесь накопичений купон як приріст.
func TestCapitalDeltaLikeForLikeAcrossAccruedColumn(t *testing.T) {
	now := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	ago := domain.NewDate(now.AddDate(0, 0, -30))
	// Капітал у знімку 1000 ₴, купона той день не рахували; сьогодні
	// капітал 1050, з них 50 — накопичений купон. Справжнього приросту немає.
	src := &sources{capitalAgo: &store.Snapshot{Date: ago, NominalUAHEq: 100_000, AccruedUAH: -1}}
	if out := buildCapitalDelta(src, 1050, 50, fx.Rates{}, domain.NewDate(now)); out.DeltaUAH.Major() != 0 {
		t.Errorf("дельта проти знімка без купона %.2f, чекали 0 — купон порахувався приростом",
			out.DeltaUAH.Major())
	}
	// Знімок, що купон знає, порівнюється як є: +20 купона — справжній приріст.
	src.capitalAgo.AccruedUAH = 3_000
	if out := buildCapitalDelta(src, 1050, 50, fx.Rates{}, domain.NewDate(now)); out.DeltaUAH.Major() != 20 {
		t.Errorf("дельта %.2f, чекали 20 (1050 − (1000 + 30))", out.DeltaUAH.Major())
	}

	// «Підсумок місяця»: якщо бодай один кінець купона не знає — купон
	// знімається з обох.
	a := store.Snapshot{NominalUAHEq: 100_000, AccruedUAH: -1}
	b := store.Snapshot{NominalUAHEq: 100_000, AccruedUAH: 5_000}
	if ca, cb := SnapshotCapitalPair(a, b); ca != cb {
		t.Errorf("пара %d → %d: місяць міграції показав купон приростом", ca, cb)
	}
	a.AccruedUAH = 2_000
	if ca, cb := SnapshotCapitalPair(a, b); cb-ca != 3_000 {
		t.Errorf("пара %d → %d, чекали приріст купона 30,00", ca, cb)
	}
}
