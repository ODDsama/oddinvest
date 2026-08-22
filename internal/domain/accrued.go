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
