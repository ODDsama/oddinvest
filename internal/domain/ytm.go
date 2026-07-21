package domain

import (
	"fmt"

	money "github.com/Rhymond/go-money"
)

// YTM — дохідність до погашення одного паперу: ставка, за якої теперішня
// вартість усіх майбутніх виплат дорівнює тому, що за папір заплатили.
//
// Досі дохідність портфеля рахувалась як «річний купон ÷ НОМІНАЛ». Це
// відповідає на питання «скільки папір платить», а не «скільки я
// заробляю»: купивши за 995 при номіналі 1000, ти маєш і вищий поточний
// дохід, і ще 5 ₴ на погашенні, — і навпаки з премією. Різниця тим
// більша, чим ближче погашення, бо дисконт відпрацьовує за менший строк.
//
// Повертає ЕФЕКТИВНУ річну ставку (як і XIRR), тож піврічний купон 8.275%
// дає 17.24%, а не 16.55% — саме в такому вигляді її й чекає MonthlyRate.
//
// costPerBond — усе, що фактично сплачено за один папір: «брудна» ціна
// плюс частка комісії. payments — графік виплат на ОДИН папір.
func YTM(costPerBond *money.Money, buyDate Date, payments []Payment, isin string) (float64, error) {
	if costPerBond == nil || costPerBond.Amount() <= 0 {
		return 0, fmt.Errorf("YTM: ціна має бути додатною")
	}
	flows := []Flow{{Date: buyDate, Amount: -costPerBond.Amount()}}
	for _, p := range payments {
		if p.ISIN != isin || p.PerBond == nil {
			continue
		}
		// Виплати рівно в день купівлі покупцеві вже не належать.
		if !p.PayDate.After(buyDate) {
			continue
		}
		flows = append(flows, Flow{Date: p.PayDate, Amount: p.PerBond.Amount()})
	}
	if len(flows) < 2 {
		return 0, fmt.Errorf("YTM: у %s немає майбутніх виплат після %s", isin, buyDate)
	}
	return XIRR(flows)
}

// WeightedYTM — середня дохідність набору покупок, зважена за вкладеними
// грішми. Вагою беремо саме СОБІВАРТІСТЬ, а не номінал: питання «яку
// дохідність я маю на свої гроші», тож більше грошей — більший вплив.
//
// Позиції, для яких YTM не рахується (немає майбутніх виплат, битий
// графік), просто не беруть участі — і у вазі теж.
type YTMLot struct {
	CostPerBond *money.Money
	Qty         int64
	BuyDate     Date
	ISIN        string
}

func WeightedYTM(lots []YTMLot, payments []Payment) (float64, bool) {
	var sumW, sumWY float64
	for _, l := range lots {
		if l.Qty <= 0 || l.CostPerBond == nil {
			continue
		}
		y, err := YTM(l.CostPerBond, l.BuyDate, payments, l.ISIN)
		if err != nil {
			continue
		}
		w := float64(l.CostPerBond.Amount()) * float64(l.Qty)
		sumW += w
		sumWY += w * y
	}
	if sumW == 0 {
		return 0, false
	}
	return sumWY / sumW * 100, true // частка -> %
}
