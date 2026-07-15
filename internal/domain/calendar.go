package domain

import (
	"sort"
)

// HolderQty — кількість паперів лота у власності на дату виплати.
// Конвенція: продаж з датою СТРОГО ДО дати виплати передає виплату
// покупцю; виплата в день продажу залишається продавцю (НКД за повний
// період еквівалентний купону).
func HolderQty(lot Lot, sales []Sale, on Date) int64 {
	q := lot.Qty
	for _, s := range sales {
		if s.LotID == lot.ID && s.SaleDate.Before(on) {
			q -= s.Qty
		}
	}
	if q < 0 {
		q = 0
	}
	return q
}

// RemainingQtyNow — залишок лота станом на сьогодні (усі продажі враховано).
func RemainingQtyNow(lot Lot, sales []Sale) int64 {
	q := lot.Qty
	for _, s := range sales {
		if s.LotID == lot.ID {
			q -= s.Qty
		}
	}
	if q < 0 {
		q = 0
	}
	return q
}

// FuturePayments будує агрегований календар майбутніх виплат портфеля:
// для кожної виплати з графіків НБУ рахується кількість паперів у
// власності на дату виплати з урахуванням продажів.
func FuturePayments(payments []Payment, lots []Lot, sales []Sale, from Date) ([]CashflowItem, error) {
	type key struct {
		date Date
		isin string
		typ  PayType
	}
	byISIN := map[string][]Lot{}
	for _, l := range lots {
		byISIN[l.ISIN] = append(byISIN[l.ISIN], l)
	}
	agg := map[key]*CashflowItem{}
	for _, p := range payments {
		if p.PayDate.Before(from) {
			continue
		}
		var qty int64
		for _, l := range byISIN[p.ISIN] {
			qty += HolderQty(l, sales, p.PayDate)
		}
		if qty == 0 {
			continue
		}
		amt := MulQty(p.PerBond, qty)
		k := key{p.PayDate, p.ISIN, p.Type}
		if ex, ok := agg[k]; ok {
			sum, err := ex.Amount.Add(amt)
			if err != nil {
				return nil, err
			}
			ex.Amount = sum
		} else {
			agg[k] = &CashflowItem{Date: p.PayDate, ISIN: p.ISIN, Type: p.Type, Amount: amt}
		}
	}
	out := make([]CashflowItem, 0, len(agg))
	for _, v := range agg {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		if out[i].ISIN != out[j].ISIN {
			return out[i].ISIN < out[j].ISIN
		}
		return out[i].Type < out[j].Type
	})
	return out, nil
}

// LadderEntry — сумарний номінал залишку з погашенням у певному році.
type LadderEntry struct {
	Year     int
	Currency string
	Nominal  int64 // мінорні одиниці валюти паперів
}

// Ladder — розподіл номіналу портфеля по роках погашення, окремо по валютах.
func Ladder(bonds map[string]Bond, lots []Lot, sales []Sale, asOf Date) []LadderEntry {
	type key struct {
		year int
		cur  string
	}
	agg := map[key]int64{}
	for _, l := range lots {
		b, ok := bonds[l.ISIN]
		if !ok || b.Maturity.Before(asOf) {
			continue
		}
		q := RemainingQtyNow(l, sales)
		if q == 0 {
			continue
		}
		k := key{b.Maturity.Year(), b.Nominal.Currency().Code}
		agg[k] += b.Nominal.Amount() * q
	}
	out := make([]LadderEntry, 0, len(agg))
	for k, v := range agg {
		out = append(out, LadderEntry{Year: k.year, Currency: k.cur, Nominal: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Year != out[j].Year {
			return out[i].Year < out[j].Year
		}
		return out[i].Currency < out[j].Currency
	})
	return out
}
