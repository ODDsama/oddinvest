package domain

import (
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
			// Папера немає в кеші довідника — але це не привід валити
			// весь портфель. Так буває, коли папір щойно розміщений і
			// довідник ще не оновлено, або коли лот прийшов з виписки
			// раніше за оновлення НБУ. Позицію показуємо з тим, що
			// знаємо напевно: кількість і вкладені гроші. Номінал,
			// погашення й виплати лишаються порожні — саме їх ми й не
			// знаємо, і вигадувати їх було б гірше за прогалину.
			cur := a.invested.Currency().Code
			out = append(out, Position{
				ISIN:     isin,
				Currency: cur,
				Qty:      a.qty,
				Invested: a.invested,
				// Нулі, а не nil: далі ці суми складаються й конвертуються,
				// і nil там перетворився б на паніку. Нуль тут чесний —
				// номіналу ми не знаємо, тож і в підсумки він не додає.
				Nominal:    money.New(0, cur),
				NextPayAmt: money.New(0, cur),
				Unknown:    true,
			})
			continue
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
