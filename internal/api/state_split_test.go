package api

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
	money "github.com/Rhymond/go-money"
)

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
	if math.Abs(sum.Split.MeasuredUAH.Major()+sum.Split.PromisedUAH.Major()-sum.Base) > 0.01 {
		t.Errorf("половини %v + %v не дають базу %v",
			sum.Split.MeasuredUAH.Major(), sum.Split.PromisedUAH.Major(), sum.Base)
	}
	// Вклад на 100 000 ₴ — в обіцяній; фонд на 1 000 ₴ — у заробленій.
	if math.Abs(sum.Split.PromisedUAH.Major()-100000) > 0.01 {
		t.Errorf("вклад мав піти в обіцяну половину: %v", sum.Split.PromisedUAH.Major())
	}
	if math.Abs(sum.Split.MeasuredUAH.Major()-1000) > 0.01 {
		t.Errorf("фонд мав піти в зароблену половину: %v", sum.Split.MeasuredUAH.Major())
	}
}
