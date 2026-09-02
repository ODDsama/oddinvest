package domain

import (
	"fmt"
	"math/big"
	"sort"

	money "github.com/Rhymond/go-money"
)

// EstimateAccrued — оцінка НКД на один папір станом на дату on за
// конвенцією ACT/ACT: купон періоду × (днів від початку періоду до on) /
// (днів у періоді). Якщо on поза купонними періодами — 0.
// Використовується як підказка в UI при введенні продажу; фактичний
// НКД користувач може ввести власноруч (біржа/банк можуть рахувати
// за іншою конвенцією).
//
// Початок періоду — попередня виплата, коли вона є в графіку; коли її
// немає, період відновлюється із самої сітки купонів (couponStart).
// Доти на цьому місці стояв нуль, і коштував він дорого: у щойно
// дорозміщеного випуску попередньої виплати в даних НБУ немає, тож
// свіжокуплений папір оцінювався рівно в номінал — без купона, який на
// ньому вже наріс і був сплачений у ціні. Занижений НКД тягнув за собою
// занижену ціну кроку покупки (unit_cost.go), плитку «Накопичений купон»
// і термінальну вартість у XIRR: −36% річних там, де насправді −4%.
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
	zero := money.New(0, coupons[0].PerBond.Currency().Code)

	// prev — остання виплата не пізніше on, next — перша після неї,
	// after — наступна за next: із відстані між ними видно крок сітки.
	var prev, next, after *Payment
	for i := range coupons {
		switch {
		case !coupons[i].PayDate.After(on):
			prev = &coupons[i]
		case next == nil:
			next = &coupons[i]
		case after == nil:
			after = &coupons[i]
		}
	}
	if next == nil {
		return zero, nil // папір погашено: наростати більше нема чому
	}

	start := Date("")
	if prev != nil {
		start = prev.PayDate
	} else if s, ok := couponStart(next, after); ok {
		start = s
	} else {
		return zero, nil
	}

	periodDays := DaysBetween(start, next.PayDate)
	if periodDays <= 0 {
		return nil, fmt.Errorf("некоректний купонний період %s..%s", start, next.PayDate)
	}
	elapsed := DaysBetween(start, on)
	if elapsed <= 0 {
		return zero, nil // період ще не почався
	}
	r := new(big.Rat).SetInt64(next.PerBond.Amount())
	r.Mul(r, big.NewRat(int64(elapsed), int64(periodDays)))
	minor, err := RatToInt64HalfEven(r)
	if err != nil {
		return nil, err
	}
	return money.New(minor, next.PerBond.Currency().Code), nil
}

// couponStart — початок періоду, який закінчується виплатою next, коли
// попередньої виплати в графіку немає.
//
// Довідник НБУ подає лише МАЙБУТНІ виплати. Для дорозміщеного випуску це
// означає, що перший купон у даних — не перший у житті паперу: у
// UA4000239081 графік починається з 2026-08-26, хоча період почався
// 2026-02-25, і на 21.08.2026 на папері наросло 79.94 з 82.20 грн купона.
// Саме цю різницю модель втрачала.
//
// Крок сітки беремо з відстані між двома найближчими виплатами й
// відкладаємо назад від next. Якщо перший купон СКОРОЧЕНИЙ — папір
// справді новий, а не дорозміщений, — НБУ подає меншу суму, і відношення
// сум дає довжину реального періоду: половина купона означає половину
// періоду.
//
// razm_date з довідника тут навмисно не використовується, і поля під неї
// в Bond немає. Для дорозміщеного випуску це дата ОСТАННЬОГО розміщення,
// а не початок періоду: у того ж UA4000239081 вона 2026-07-28 — за нею
// вийшло б 11 грн замість 80. Помилка була б менша за нинішню, зате
// правдоподібна, а таку вже не видно.
func couponStart(next, after *Payment) (Date, bool) {
	if after == nil {
		return "", false // єдина виплата в графіку: крок сітки нізвідки взяти
	}
	grid := DaysBetween(next.PayDate, after.PayDate)
	full, first := after.PerBond.Amount(), next.PerBond.Amount()
	if grid <= 0 || full <= 0 || first <= 0 {
		return "", false
	}
	days := grid
	if first < full {
		d, err := RatToInt64HalfEven(big.NewRat(int64(grid)*first, full))
		if err != nil || d <= 0 {
			return "", false
		}
		days = int(d)
	}
	return next.PayDate.AddDays(-days), true
}

// AccruedItem — НКД, сплачений при купівлі, віднесений на дату того
// купона, який його повертає.
//
// НЕ CashflowItem, і це не про зручність типів. CashflowItem означає
// «гроші, що надійдуть», і його читають xirr.go, state_schedule.go,
// state_tasks.go, ready_on.go, state_builder.go, handlers_reports.go.
// Значення, яке грошима НЕ є, у тому типі — один необережний append від
// фантомної виплати в календарі.
type AccruedItem struct {
	Date   Date // дата ПЕРШОГО купона, який лот справді отримує
	ISIN   string
	Amount *money.Money // НКД на папір × кількість; ЗАВЖДИ ДОДАТНИЙ
}

// AccruedPaid — НКД, який портфель сплатив у брудній ціні, розкладений по
// датах купонів, що його повертають.
//
// Купуючи папір усередині купонного періоду, покупець платить продавцю
// накопичений купон — він сидить у ціні. Купон, що приходить за кілька днів
// після такої купівлі, майже цілком є поверненням цих же грошей, а не
// доходом. Приклад із бойових даних: 9 паперів UA4000239081, куплених
// 15 і 18 серпня 2026, дали купон 739,80 грн 26 серпня — а НКД у їхній ціні
// був 705,95. Зароблено 33,85, показувалось 739,80.
//
// Суми ДОДАТНІ. Мінус — рішення подання («це віднімання»), і воно
// ухвалюється один раз, у того, хто малює рядок. Дзеркалить Sale.Accrued,
// який теж зберігається додатним.
//
// ЧОМУ НА ДАТУ КУПОНА, А НЕ КУПІВЛІ. Купівля 20 грудня з першим купоном
// 15 січня має зменшити НАСТУПНИЙ рік — той, у якому стоїть дохід, що вона
// гасить. Побічна вигода не менш важлива: ReplaceDirectory чистить графік
// на кожному оновленні довідника, тож коли купон звідти зникає,
// FuturePayments перестає його платити, а ця функція перестає знаходити для
// лота first. Обидві половини зникають ПАРОЮ, і відрахування ніколи не
// лишається без доходу, який воно гасить.
//
// ІНВАРІАНТ: сума НКД на дату ніколи не перевищує купон на ту саму дату.
// EstimateAccrued на on = BuyDate бере next = перший купон СТРОГО після
// купівлі; ця функція бере first = першу дату з HolderQty > 0, а HolderQty
// теж вимагає on > BuyDate, — отже це завжди одна й та сама виплата, і
// всередині EstimateAccrued elapsed < periodDays строго, тобто НКД строго
// менший за купон на папір навіть при купівлі за день до виплати. Лоти, що
// дають НКД на дату, — підмножина тих, що дають купон, із тією ж кількістю.
// Тому рядок «купони мінус НКД» у мінус піти не може, і затискача тут
// немає: він був би гілкою, у яку не потрапити. Хто змінює couponStart —
// відповідає за цей інваріант; його стереже TestAccruedPaidNeverExceedsItsCoupon.
//
// ЩО СЮДИ НЕ ПОТРАПЛЯЄ. EstimateAccrued повертає нуль, невідрізненний від
// справжнього, коли couponStart не може відновити період: у графіку рівно
// один купон по паперу (after == nil). Такий лот тихо лишається без
// відрахування — мініатюра того самого бага, який ця функція виправляє.
// Закрити це означає дати EstimateAccrued третій результат («оцінити не
// вдалось») і зачепити пʼять місць виклику. Свідомо не цього разу; лічильник
// у примітці заводити не варто — він не відрізнить «нуль» від «невідомо», а
// фальшива тривога на кожному лоті гірша за мовчання.
func AccruedPaid(payments []Payment, lots []Lot, sales []Sale) ([]AccruedItem, error) {
	couponDates := map[string][]Date{}
	for _, p := range payments {
		if p.Type == PayCoupon {
			couponDates[p.ISIN] = append(couponDates[p.ISIN], p.PayDate)
		}
	}
	for isin := range couponDates {
		d := couponDates[isin]
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	}

	type key struct {
		date Date
		isin string
	}
	agg := map[key]*money.Money{}
	for _, l := range lots {
		// Власність НЕ виводимо заново: HolderQty уже несе обидві межові
		// конвенції (купон, датований на/до дати купівлі, належав продавцю;
		// продаж строго до купона його передає покупцю). Другий примірник
		// того правила тут розійшовся б із FuturePayments при першій же
		// правці — а це рівно та пара, яка мусить рахувати однаково.
		var first Date
		var qty int64
		for _, d := range couponDates[l.ISIN] {
			if q := HolderQty(l, sales, d); q > 0 {
				first, qty = d, q
				break
			}
		}
		// Порожній first — лот не побачить жодного купона: або папір без
		// купонів у графіку, або лот продано раніше за перший із них. У
		// першому випадку ця перевірка ще й робить недосяжною єдину помилку
		// EstimateAccrued («немає купонних виплат»), тому вона стоїть ВИЩЕ
		// за виклик, а не нижче.
		if first == "" {
			continue
		}
		acc, err := EstimateAccrued(payments, l.ISIN, l.BuyDate)
		if err != nil {
			return nil, err
		}
		if acc.IsZero() {
			continue
		}
		k := key{first, l.ISIN}
		amt := MulQty(acc, qty)
		if ex, ok := agg[k]; ok {
			sum, err := ex.Add(amt)
			if err != nil {
				return nil, err
			}
			agg[k] = sum
		} else {
			agg[k] = amt
		}
	}

	out := make([]AccruedItem, 0, len(agg))
	for k, v := range agg {
		out = append(out, AccruedItem{Date: k.date, ISIN: k.isin, Amount: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Date != out[j].Date {
			return out[i].Date < out[j].Date
		}
		return out[i].ISIN < out[j].ISIN
	})
	return out, nil
}
