package engine

import (
	"math"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

// Той самий випадок, що на живих даних: один фонд заробив, другий поки лише
// обіцяє. Плитка показувала одне змішане число й підпис «різні основи», і
// питання «чому так мало» не мало відповіді на екрані.
func TestFundsSplitNamesMeasuredAndPromised(t *testing.T) {
	today := domain.Date("2026-07-15")
	ops := []domain.FundOp{
		// REIT: куплений давно, платить дивіденди — ФАКТ.
		{Date: "2026-01-10", Fund: "REIT", Kind: domain.FundBuy,
			Qty: 100, Amount: 100_000, Currency: money.UAH},
		{Date: "2026-06-10", Fund: "REIT", Kind: domain.FundDividend,
			Amount: 3_000, Tax: 420, Currency: money.UAH},
		// МілТех: куплений щойно — міряти нема по чому, лишається ОБІЦЯНКА.
		{Date: "2026-07-10", Fund: "MilTech", Kind: domain.FundBuy,
			Qty: 5, Amount: 500_000, Currency: money.UAH},
	}
	src := &sources{fundOps: ops, fundRefs: map[string]store.Fund{
		"REIT": {Name: "REIT", Currency: money.UAH, PayoutDay: 10},
		"MilTech": {Name: "MilTech", Currency: money.UAH, Kind: store.FundAccumulating,
			ExpectedYieldBP: 2500, ExpectedYieldCur: money.UAH, YieldSimpleYears: 3},
	}}
	hold := domain.NewHoldings(nil, nil, nil, ops, nil, nil, today, nil)
	out := buildFunds(src, hold, fx.Rates{}, 0 /* без знецінення */, today)

	if out.Split == nil {
		t.Fatal("основи різні — розклад мав зʼявитись")
	}
	// Гроші розкладу мусять зійтися з вагою зведеної: інакше десь загубився
	// доданок, і число обіцяє більше, ніж покриває.
	if math.Abs(out.Split.MeasuredUAH.Major()+out.Split.PromisedUAH.Major()-out.YieldWeight) > 0.01 {
		t.Errorf("половини %v + %v не дають вагу %v",
			out.Split.MeasuredUAH.Major(), out.Split.PromisedUAH.Major(), out.YieldWeight)
	}
	// МілТех — 5 000 ₴ обіцянки, REIT — 1 000 ₴ факту.
	if math.Abs(out.Split.PromisedUAH.Major()-5000) > 0.01 {
		t.Errorf("в обіцяній половині мав бути МілТех на 5000, маємо %v", out.Split.PromisedUAH.Major())
	}
	if math.Abs(out.Split.MeasuredUAH.Major()-1000) > 0.01 {
		t.Errorf("у заробленій половині мав бути REIT на 1000, маємо %v", out.Split.MeasuredUAH.Major())
	}
	// Обіцянка МілТеху — 25% простих за три роки, тобто 20.51 складних.
	if math.Abs(out.Split.PromisedRealPct-20.51) > 0.01 {
		t.Errorf("обіцяна половина мала показати 20.51, маємо %v", out.Split.PromisedRealPct)
	}
	// І головне число НЕ змінилось: розклад лише додається.
	if out.YieldRealPct == 0 {
		t.Error("зведена по фондах мала лишитись на місці")
	}
}

// Усі фонди міряні однаково — розкладу немає. Він повторив би головне
// число, а порожня половина читалась би як нуль.
func TestFundsNoSplitWhenSingleBasis(t *testing.T) {
	today := domain.Date("2026-07-15")
	ops := []domain.FundOp{
		{Date: "2026-01-10", Fund: "REIT", Kind: domain.FundBuy,
			Qty: 100, Amount: 100_000, Currency: money.UAH},
		{Date: "2026-06-10", Fund: "REIT", Kind: domain.FundDividend,
			Amount: 3_000, Tax: 420, Currency: money.UAH},
	}
	src := &sources{fundOps: ops, fundRefs: map[string]store.Fund{
		"REIT": {Name: "REIT", Currency: money.UAH, PayoutDay: 10},
	}}
	hold := domain.NewHoldings(nil, nil, nil, ops, nil, nil, today, nil)
	if out := buildFunds(src, hold, fx.Rates{}, 0, today); out.Split != nil {
		t.Errorf("основа одна — розкладу бути не мало: %+v", out.Split)
	}
}
