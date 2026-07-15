package domain

import (
	"fmt"
	"math"
	"sort"
)

// Flow — грошовий потік для XIRR у мінорних одиницях ОДНІЄЇ валюти.
// Знак: інвестиції від'ємні, надходження додатні.
type Flow struct {
	Date   Date
	Amount int64
}

// XIRR — річна внутрішня ставка дохідності нерегулярних потоків
// (ACT/365). Рахується ОКРЕМО ПО КОЖНІЙ ВАЛЮТІ: змішування валют
// в одному XIRR дає безглузді числа, тому конвертації тут нема свідомо.
//
// Метод — бісекція по NPV: повільніше за Ньютона, зате не розбігається.
// Повертає ставку як частку (0.165 = 16.5%).
func XIRR(flows []Flow) (float64, error) {
	if len(flows) < 2 {
		return 0, fmt.Errorf("XIRR потребує щонайменше 2 потоків, маємо %d", len(flows))
	}
	sorted := make([]Flow, len(flows))
	copy(sorted, flows)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Date < sorted[j].Date })

	hasNeg, hasPos := false, false
	for _, f := range sorted {
		if f.Amount < 0 {
			hasNeg = true
		}
		if f.Amount > 0 {
			hasPos = true
		}
	}
	if !hasNeg || !hasPos {
		return 0, fmt.Errorf("XIRR потребує потоків обох знаків")
	}

	t0 := sorted[0].Date
	npv := func(rate float64) float64 {
		var sum float64
		for _, f := range sorted {
			years := float64(DaysBetween(t0, f.Date)) / 365.0
			sum += float64(f.Amount) / math.Pow(1+rate, years)
		}
		return sum
	}

	lo, hi := -0.9999, 10.0
	fLo, fHi := npv(lo), npv(hi)
	if fLo*fHi > 0 {
		return 0, fmt.Errorf("XIRR: немає кореня в діапазоні (-99.99%%, 1000%%)")
	}
	for i := 0; i < 200; i++ {
		mid := (lo + hi) / 2
		fMid := npv(mid)
		if math.Abs(fMid) < 1e-7 || hi-lo < 1e-10 {
			return mid, nil
		}
		if fLo*fMid < 0 {
			hi = mid
		} else {
			lo, fLo = mid, fMid
		}
	}
	return (lo + hi) / 2, nil
}

// PortfolioFlows будує потоки для XIRR однієї валюти станом на asOf:
//   - покупки лотів: −(ціна × кількість) у дату купівлі;
//   - продажі: +(чиста × кількість + НКД) у дату продажу;
//   - отримані виплати (купони/погашення на кількість у власності
//     на дату виплати) до asOf включно;
//   - термінальний потік: номінал залишку на asOf — консервативний
//     проксі ринкової вартості (ОВДП торгуються біля номіналу,
//     дюрації короткі). Це припущення, не оцінка ринку.
func PortfolioFlows(bonds map[string]Bond, payments []Payment, lots []Lot,
	sales []Sale, currency string, asOf Date) ([]Flow, error) {

	var flows []Flow
	lotByID := map[int64]Lot{}
	for _, l := range lots {
		if l.PricePerBond.Currency().Code != currency {
			continue
		}
		lotByID[l.ID] = l
		out := MulQty(l.PricePerBond, l.Qty)
		if l.Fee != nil && !l.Fee.IsZero() {
			var err error
			if out, err = out.Add(l.Fee); err != nil {
				return nil, err
			}
		}
		flows = append(flows, Flow{Date: l.BuyDate, Amount: -out.Amount()})
	}
	for _, s := range sales {
		lot, ok := lotByID[s.LotID]
		if !ok {
			continue // лот іншої валюти
		}
		_ = lot
		proceeds, err := SaleProceeds(s)
		if err != nil {
			return nil, err
		}
		flows = append(flows, Flow{Date: s.SaleDate, Amount: proceeds.Amount()})
	}

	// отримані виплати: минулий cashflow по власності
	past, err := FuturePayments(payments, lots, sales, "1970-01-01")
	if err != nil {
		return nil, err
	}
	for _, cf := range past {
		if cf.Date.After(asOf) || cf.Amount.Currency().Code != currency {
			continue
		}
		flows = append(flows, Flow{Date: cf.Date, Amount: cf.Amount.Amount()})
	}

	// термінальна вартість залишку за номіналом
	var terminal int64
	for _, l := range lots {
		if l.PricePerBond.Currency().Code != currency {
			continue
		}
		b, ok := bonds[l.ISIN]
		if !ok || b.Maturity.Before(asOf) {
			continue
		}
		terminal += b.Nominal.Amount() * RemainingQtyNow(l, sales)
	}
	if terminal > 0 {
		flows = append(flows, Flow{Date: asOf, Amount: terminal})
	}
	sort.Slice(flows, func(i, j int) bool { return flows[i].Date < flows[j].Date })
	return flows, nil
}
