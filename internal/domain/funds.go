package domain

import (
	"math"
	"sort"
)

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
	// PairID зв'язує дві операції в одну КОНВЕРТАЦІЮ між фондами: продаж
	// в одному фонді й купівля в іншому — це не два незалежні рішення, а
	// один переказ. Без цього зв'язку пара рахувалась як справжній продаж
	// за гроші: гаманець рухався двічі, а на різниці цін нараховувався
	// прибуток, якого ти не отримував. 0 — пари немає.
	PairID int64
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
	// Short — скільки сертифікатів продано понад куплені. Журнал, у якому
	// продано більше, ніж придбано, неповний: у ньому бракує надходження.
	//
	// Раніше такий мінус мовчки занулювався «щоб не тягнути далі», і
	// позиція виглядала здоровою. На реальних даних це коштувало дорого:
	// у REIT продано 1854 сертифікати проти 421 купленого, застосунок
	// показував правдоподібні 305 — і півтори тисячі сертифікатів, що
	// взялися нізвідки, не помітив ніхто. Тепер розбіжність не зникає, а
	// називається вголос.
	Short int64
}

// Inconsistent — чи має позиція дірку в журналі.
func (p FundPosition) Inconsistent() bool { return p.Short > 0 }

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
			if p.Qty < 0 {
				// Мінус далі не тягнемо — інакше він поповз би в ринкову
				// вартість і зробив би капітал від'ємним, — але й не
				// ховаємо: скільки саме не сходиться, лишається в Short.
				p.Short += -p.Qty
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

// Мінімальний вік грошей і смуга правдоподібності для ануалізації —
// ті самі, що й у портфельного XIRR. Три дні тримання з приростом 0.4%
// дають 60% річних, і це не дохідність, а арифметика ділення на малий
// строк.
const (
	fundReturnMinDays = 30.0
	fundReturnMax     = 1.0
	fundReturnMin     = -0.95
)

// FundTotalReturn — ПОВНА дохідність позиції фонду, % річних: дивіденди
// після податку І зміна ціни разом, ануалізовані по фактичних датах
// операцій (XIRR з термінальною ринковою вартістю).
//
// Навіщо, коли поруч є DividendYieldNet. Та міряє лише дохідну частину,
// і поряд з облігацією це нечесно: YTM ловить УВЕСЬ дохід паперу — і
// купон, і дисконт до номіналу, — а фонд показував самі дивіденди, тож
// у спільній таблиці систематично виглядав гіршим, ніж є. REIT, який
// платить 5.8% і дорожчає, — це не 5.8%.
//
// Природа числа при цьому інша, і ховати це не можна: YTM облігації —
// обіцянка на майбутнє, а тут ФАКТ по вже прожитому. Сертифікат не
// гаситься, майбутніх виплат за графіком у нього немає, тож іншого
// чесного способу порахувати повну дохідність не існує.
func FundTotalReturn(ops []FundOp, fund string, asOf Date) (float64, bool) {
	flows := FundFlowsOne(ops, fund, asOf)
	if len(flows) < 2 {
		return 0, false
	}
	if MoneyWeightedDays(flows, asOf) < fundReturnMinDays {
		return 0, false
	}
	r, err := XIRR(flows)
	if err != nil || r > fundReturnMax || r < fundReturnMin {
		return 0, false
	}
	return math.Round(r*10000) / 100, true
}

// DividendYieldNet — чиста дивідендна дохідність позиції, % річних:
// ОСТАННЯ виплата після податку, приведена до року за ритмом виплат.
//
// Саме після податку: у фонду немає обіцяної ставки, є лише те, що він
// реально платить, а дивіденд оподатковується, на відміну від купона ОВДП.
//
// Чому не «сума за 365 днів ÷ вартість», як було раніше: та формула міряє
// не дохідність паперу, а те, скільки історії встигло накопичитись. Фонд,
// куплений три місяці тому, показував би близько чверті своєї справжньої
// дохідності — і не тому, що погано платить, а тому, що ще не встиг
// заплатити дванадцять разів. Позиція з підчищеним журналом виглядала так
// само. Останню виплату натомість фонд щойно зробив, і вона про сьогодні.
//
// Ритм береться з фактичних проміжків між виплатами (медіана — щоб один
// зсунутий місяць не перекосив усе), а не задається константою: щомісячний
// фонд і щоквартальний однаково не мають підганятись під чуже припущення.
// Одна-єдина виплата ритму не дає, тож для неї береться місяць — найчастіший
// для REIT-фондів випадок.
//
// on — сьогоднішня дата.
func DividendYieldNet(ops []FundOp, p *FundPosition, on Date) (float64, bool) {
	if p == nil || p.MarketValue() <= 0 {
		return 0, false
	}
	// Лише виплати останнього року: торішній ритм до сьогоднішньої
	// дохідності стосунку не має.
	var recent []FundOp
	for _, op := range ops {
		if op.Fund != p.Fund || op.Kind != FundDividend {
			continue
		}
		if d := DaysBetween(op.Date, on); d >= 0 && d <= 365 {
			recent = append(recent, op)
		}
	}
	if len(recent) == 0 {
		return 0, false
	}
	sort.Slice(recent, func(i, j int) bool { return recent[i].Date < recent[j].Date })
	last := recent[len(recent)-1]
	net := last.Amount - last.Tax
	if net <= 0 {
		return 0, false
	}

	// Медіана проміжків між сусідніми виплатами; менше двох виплат —
	// вважаємо місячний ритм.
	period := 30.0
	if len(recent) >= 2 {
		gaps := make([]int, 0, len(recent)-1)
		for i := 1; i < len(recent); i++ {
			if g := DaysBetween(recent[i-1].Date, recent[i].Date); g > 0 {
				gaps = append(gaps, g)
			}
		}
		if len(gaps) > 0 {
			sort.Ints(gaps)
			period = float64(gaps[len(gaps)/2])
		}
	}
	if period < 1 {
		period = 1
	}
	annual := float64(net) * 365 / period
	return math.Round(annual/float64(p.MarketValue())*10000) / 100, true
}
