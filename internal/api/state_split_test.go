package api

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
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
	hold := domain.NewHoldings(nil, nil, nil, ops, nil, nil, today)
	out := buildFunds(src, hold, fx.Rates{}, 0 /* без знецінення */, today)

	if out.Split == nil {
		t.Fatal("основи різні — розклад мав зʼявитись")
	}
	// Гроші розкладу мусять зійтися з вагою зведеної: інакше десь загубився
	// доданок, і число обіцяє більше, ніж покриває.
	if math.Abs(out.Split.MeasuredUAH+out.Split.PromisedUAH-out.YieldWeight) > 0.01 {
		t.Errorf("половини %v + %v не дають вагу %v",
			out.Split.MeasuredUAH, out.Split.PromisedUAH, out.YieldWeight)
	}
	// МілТех — 5 000 ₴ обіцянки, REIT — 1 000 ₴ факту.
	if math.Abs(out.Split.PromisedUAH-5000) > 0.01 {
		t.Errorf("в обіцяній половині мав бути МілТех на 5000, маємо %v", out.Split.PromisedUAH)
	}
	if math.Abs(out.Split.MeasuredUAH-1000) > 0.01 {
		t.Errorf("у заробленій половині мав бути REIT на 1000, маємо %v", out.Split.MeasuredUAH)
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
	hold := domain.NewHoldings(nil, nil, nil, ops, nil, nil, today)
	if out := buildFunds(src, hold, fx.Rates{}, 0, today); out.Split != nil {
		t.Errorf("основа одна — розкладу бути не мало: %+v", out.Split)
	}
}

// Портфельний розклад: ОВДП і вклад цілком в обіцяній половині, фонд — у
// заробленій, а сума половин дорівнює базі зведеної.
//
// Це та відповідь, заради якої розклад і заводився: на звичайному портфелі
// майже все число тримається на обіцянках, і побачити це можна лише так.
func TestBlendedSplitPutsBondsAndDepositsInPromised(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	today := domain.NewDate(time.Now())

	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: money.UAH, Principal: 10_000_000, RateBP: 1600,
		OpenDate: today.AddDays(-200), MaturityDate: today.AddDays(165),
		Payout: domain.PayoutMonthly, TaxBP: 1950,
	}); err != nil {
		t.Fatal(err)
	}
	// Фонд із дивідендами — виміряний.
	for _, op := range []domain.FundOp{
		{Date: today.AddDays(-200), Fund: "REIT", Kind: domain.FundBuy,
			Qty: 100, Amount: 100_000, Currency: money.UAH},
		{Date: today.AddDays(-20), Fund: "REIT", Kind: domain.FundDividend,
			Amount: 3_000, Tax: 420, Currency: money.UAH},
	} {
		if _, err := st.AddFundOp(ctx, op); err != nil {
			t.Fatal(err)
		}
	}

	var sum struct {
		Split *state.YieldSplit `json:"blended_yield_split"`
		Base  float64           `json:"blended_yield_base_uah"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	if sum.Split == nil {
		t.Fatalf("вклад обіцяний, фонд виміряний — розклад мав бути: %s", body)
	}
	if math.Abs(sum.Split.MeasuredUAH+sum.Split.PromisedUAH-sum.Base) > 0.01 {
		t.Errorf("половини %v + %v не дають базу %v",
			sum.Split.MeasuredUAH, sum.Split.PromisedUAH, sum.Base)
	}
	// Вклад на 100 000 ₴ — в обіцяній; фонд на 1 000 ₴ — у заробленій.
	if math.Abs(sum.Split.PromisedUAH-100000) > 0.01 {
		t.Errorf("вклад мав піти в обіцяну половину: %v", sum.Split.PromisedUAH)
	}
	if math.Abs(sum.Split.MeasuredUAH-1000) > 0.01 {
		t.Errorf("фонд мав піти в зароблену половину: %v", sum.Split.MeasuredUAH)
	}
}
