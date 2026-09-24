package engine

import (
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
)

// Дванадцять місяців доходу — дванадцять РІЗНИХ місяців, хоч би якого
// числа сьогодні.
//
// Ключі будувались як today + i місяців (AddDate), а Go переносить
// 31 січня + 1 міс у березень. З 29–31 числа лютий, квітень, червень,
// вересень і листопад зникали, а п'ять місяців ішли двічі — і Income12m,
// Coupons12m, MonthlyNow та віхи доходу на них рахувались неправильно.
func TestIncome12mMonthsDistinctFromMonthEnd(t *testing.T) {
	today := domain.Date("2026-01-31")
	sch := schedule{Cashflow: []domain.CashflowItem{
		{Date: "2026-02-15", ISIN: "UA1", Type: domain.PayCoupon, Amount: money.New(100_00, money.UAH)},
	}}
	got := summarizeIncome(sch, fx.Rates{}, today)
	seen := map[string]bool{}
	for _, m := range got.Income12m {
		if seen[m.Month] {
			t.Errorf("місяць %s двічі: %+v", m.Month, got.Income12m)
		}
		seen[m.Month] = true
	}
	if !seen["2026-02"] || got.Income12m[1].Amount.Major() != 100 {
		t.Errorf("лютневий купон мав стояти другим місяцем: %+v", got.Income12m)
	}
}
