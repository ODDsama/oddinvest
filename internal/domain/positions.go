package domain

import (
	"fmt"
	"sort"

	money "github.com/Rhymond/go-money"
)

// Positions — агрегація лотів у позиції по ISIN станом на дату asOf.
func Positions(bonds map[string]Bond, payments []Payment, lots []Lot, sales []Sale, asOf Date) ([]Position, error) {
	type acc struct {
		qty      int64
		invested *money.Money
	}
	byISIN := map[string]*acc{}
	for _, l := range lots {
		rem := RemainingQtyNow(l, sales)
		if rem == 0 {
			continue
		}
		a, ok := byISIN[l.ISIN]
		if !ok {
			a = &acc{invested: money.New(0, l.PricePerBond.Currency().Code)}
			byISIN[l.ISIN] = a
		}
		a.qty += rem
		cost := MulQty(l.PricePerBond, rem)
		fee, err := Apportion(l.Fee, rem, l.Qty)
		if err != nil {
			return nil, err
		}
		if !fee.IsZero() {
			if cost, err = cost.Add(fee); err != nil {
				return nil, err
			}
		}
		sum, err := a.invested.Add(cost)
		if err != nil {
			return nil, err
		}
		a.invested = sum
	}

	out := make([]Position, 0, len(byISIN))
	for isin, a := range byISIN {
		b, ok := bonds[isin]
		if !ok {
			return nil, fmt.Errorf("папір %s відсутній у довіднику", isin)
		}
		pos := Position{
			ISIN:      isin,
			Currency:  b.Nominal.Currency().Code,
			Qty:       a.qty,
			Invested:  a.invested,
			Nominal:   MulQty(b.Nominal, a.qty),
			Maturity:  b.Maturity,
			DaysToMat: DaysBetween(asOf, b.Maturity),
		}
		for _, p := range payments {
			if p.ISIN != isin || p.PayDate.Before(asOf) {
				continue
			}
			if pos.NextPayDate == "" || p.PayDate.Before(pos.NextPayDate) {
				pos.NextPayDate = p.PayDate
				pos.NextPayAmt = MulQty(p.PerBond, a.qty)
			}
		}
		out = append(out, pos)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Maturity < out[j].Maturity })
	return out, nil
}
