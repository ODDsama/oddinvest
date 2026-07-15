package domain

import (
	"fmt"
	"math/big"
	"sort"

	money "github.com/Rhymond/go-money"
)

// EstimateAccrued — оцінка НКД на один папір станом на дату on за
// конвенцією ACT/ACT: купон періоду × (днів від попередньої виплати
// до on) / (днів у періоді). Потрібні попередня і наступна купонні
// виплати в графіку; якщо on поза купонними періодами — 0.
// Використовується як підказка в UI при введенні продажу; фактичний
// НКД користувач може ввести власноруч (біржа/банк можуть рахувати
// за іншою конвенцією).
func EstimateAccrued(payments []Payment, isin string, on Date) (*money.Money, error) {
	var coupons []Payment
	for _, p := range payments {
		if p.ISIN == isin && p.Type == PayCoupon {
			coupons = append(coupons, p)
		}
	}
	if len(coupons) == 0 {
		return nil, fmt.Errorf("немає купонних виплат для %s", isin)
	}
	sort.Slice(coupons, func(i, j int) bool { return coupons[i].PayDate < coupons[j].PayDate })

	var prev, next *Payment
	for i := range coupons {
		c := coupons[i]
		if !c.PayDate.After(on) { // c <= on
			prev = &coupons[i]
		}
		if c.PayDate.After(on) && next == nil {
			next = &coupons[i]
		}
	}
	if prev == nil || next == nil {
		zero := money.New(0, coupons[0].PerBond.Currency().Code)
		return zero, nil
	}
	periodDays := DaysBetween(prev.PayDate, next.PayDate)
	if periodDays <= 0 {
		return nil, fmt.Errorf("некоректний купонний період %s..%s", prev.PayDate, next.PayDate)
	}
	elapsed := DaysBetween(prev.PayDate, on)
	r := new(big.Rat).SetInt64(next.PerBond.Amount())
	r.Mul(r, big.NewRat(int64(elapsed), int64(periodDays)))
	minor, err := RatToInt64HalfEven(r)
	if err != nil {
		return nil, err
	}
	return money.New(minor, next.PerBond.Currency().Code), nil
}
