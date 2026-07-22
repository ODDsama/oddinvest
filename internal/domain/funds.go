package domain

import "math"

// Сертифікати фондів (Inzhur REIT і подібні).
//
// Від облігації такий папір відрізняється всім, на чому тримається решта
// моделі: немає дати погашення, немає номіналу, немає графіка купонів.
// Натомість є ринкова ціна, яка змінюється, нерегулярні дивіденди — і
// податок на них. Останнє важливо окремо: купон ОВДП звільнений від
// податку, а дивіденд фонду ні, тож порівнювати їхні «дохідності» до
// податку означало б підсовувати фонду фору, якої в нього немає.

// FundOpKind — тип операції в журналі фонду.
type FundOpKind string

const (
	FundBuy      FundOpKind = "buy"
	FundSell     FundOpKind = "sell"
	FundDividend FundOpKind = "dividend"
)

// FundOp — одна операція. Amount і Tax — у мінорних одиницях, завжди
// додатні; напрямок задає Kind.
type FundOp struct {
	ID       int64
	Date     Date
	Fund     string
	Kind     FundOpKind
	Qty      int64
	Amount   int64
	Tax      int64
	Currency string
	Broker   string
	Note     string
}

// FundPosition — стан одного фонду, зведений із журналу операцій.
type FundPosition struct {
	Fund     string
	Currency string
	Qty      int64
	// CostBasis — скільки вкладено в поточний залишок (мінорні). Продаж
	// зменшує його пропорційно проданій частці, тож це саме собівартість
	// того, що лишилось, а не сума всіх колись сплачених грошей.
	CostBasis int64
	// LastPrice — остання відома ціна за сертифікат у СОТИХ мінорної
	// одиниці, тобто 1/10000 гривні: 11.1120 ₴ = 111120. Зайва точність
	// тут не примха — ціна сертифіката справді має чотири знаки, і
	// округлення до копійки на 1738 сертифікатах розходиться на гривні.
	//
	// Береться з останньої операції купівлі/продажу: виписка приносить
	// ціну з собою, тож окреме джерело котирувань не потрібне.
	LastPrice     int64
	LastPriceDate Date
	// DividendsGross/DividendsTax — за весь час; Realized — результат
	// закритих продажів (виручка мінус собівартість проданого, мінус
	// податок).
	DividendsGross int64
	DividendsTax   int64
	Realized       int64
}

// MarketValue — вартість залишку за останньою відомою ціною, у мінорних
// одиницях (ділення на 100 знімає зайвий розряд точності LastPrice).
func (p FundPosition) MarketValue() int64 { return p.Qty * p.LastPrice / 100 }

// FundPositions зводить журнал у позиції. Операції мають бути
// відсортовані за датою: собівартість рахується послідовно, і продаж до
// купівлі дав би від'ємний залишок.
//
// Собівартість — середньозважена, а не FIFO: у безстрокового інструмента
// без податкової потреби розрізняти партії FIFO лише додав би складності.
func FundPositions(ops []FundOp) map[string]*FundPosition {
	out := map[string]*FundPosition{}
	for _, op := range ops {
		p := out[op.Fund]
		if p == nil {
			p = &FundPosition{Fund: op.Fund, Currency: op.Currency}
			out[op.Fund] = p
		}
		switch op.Kind {
		case FundBuy:
			p.Qty += op.Qty
			p.CostBasis += op.Amount
			if op.Qty > 0 {
				p.LastPrice = op.Amount * 100 / op.Qty
				p.LastPriceDate = op.Date
			}
		case FundSell:
			if op.Qty > 0 {
				p.LastPrice = op.Amount * 100 / op.Qty
				p.LastPriceDate = op.Date
			}
			sold := int64(0)
			if p.Qty > 0 {
				// Собівартість проданої частки — пропорційно до залишку.
				sold = p.CostBasis * op.Qty / p.Qty
			}
			p.CostBasis -= sold
			p.Qty -= op.Qty
			if p.Qty < 0 { // журнал неповний — не тягнемо мінус далі
				p.Qty = 0
			}
			if p.CostBasis < 0 {
				p.CostBasis = 0
			}
			p.Realized += op.Amount - sold - op.Tax
		case FundDividend:
			p.DividendsGross += op.Amount
			p.DividendsTax += op.Tax
		}
	}
	return out
}

// DividendYieldNet — чиста дивідендна дохідність позиції, % річних:
// дивіденди ПІСЛЯ податку за останні 365 днів ÷ ринкова вартість.
//
// Саме після податку й саме за фактом: у фонду немає обіцяної ставки, є
// лише те, що він реально платив. on — сьогоднішня дата.
func DividendYieldNet(ops []FundOp, p *FundPosition, on Date) (float64, bool) {
	if p == nil || p.MarketValue() <= 0 {
		return 0, false
	}
	var net int64
	for _, op := range ops {
		if op.Fund != p.Fund || op.Kind != FundDividend {
			continue
		}
		if d := DaysBetween(op.Date, on); d >= 0 && d <= 365 {
			net += op.Amount - op.Tax
		}
	}
	if net <= 0 {
		return 0, false
	}
	return math.Round(float64(net)/float64(p.MarketValue())*10000) / 100, true
}
