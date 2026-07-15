package domain

import (
	"fmt"

	money "github.com/Rhymond/go-money"
)

// SaleProceeds — виручка від продажу: чиста ціна × кількість + НКД.
func SaleProceeds(s Sale) (*money.Money, error) {
	p := MulQty(s.CleanPerBond, s.Qty)
	if s.Accrued != nil && !s.Accrued.IsZero() {
		return p.Add(s.Accrued)
	}
	return p, nil
}

// RealizedResult — реалізований результат угоди продажу у валюті паперу:
//
//	(чиста ціна × qty + НКД) − (ціна купівлі × qty) + Σ купонів,
//	отриманих на продану кількість за період володіння.
//
// Конвенція меж: купон з датою СТРОГО ПІСЛЯ купівлі і ДО АБО В ДЕНЬ
// продажу належить продавцю (узгоджено з HolderQty).
func RealizedResult(lot Lot, s Sale, payments []Payment) (*money.Money, error) {
	if s.LotID != lot.ID {
		return nil, fmt.Errorf("продаж %d не належить лоту %d", s.ID, lot.ID)
	}
	if s.Qty > lot.Qty {
		return nil, fmt.Errorf("продано %d > куплено %d", s.Qty, lot.Qty)
	}
	proceeds, err := SaleProceeds(s)
	if err != nil {
		return nil, err
	}
	cost := MulQty(lot.PricePerBond, s.Qty)
	fee, err := Apportion(lot.Fee, s.Qty, lot.Qty)
	if err != nil {
		return nil, err
	}
	if !fee.IsZero() {
		if cost, err = cost.Add(fee); err != nil {
			return nil, err
		}
	}
	res, err := proceeds.Subtract(cost)
	if err != nil {
		return nil, err
	}
	for _, p := range payments {
		if p.ISIN != lot.ISIN || p.Type != PayCoupon {
			continue
		}
		if p.PayDate.After(lot.BuyDate) && !p.PayDate.After(s.SaleDate) {
			res, err = res.Add(MulQty(p.PerBond, s.Qty))
			if err != nil {
				return nil, err
			}
		}
	}
	return res, nil
}
