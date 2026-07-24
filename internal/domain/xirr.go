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

	// Термінальна вартість залишку: номінал ПЛЮС накопичений купон.
	//
	// Без НКД модель фіксувала збиток у мить купівлі. Папір купується
	// «брудним» — у ціну входить купон, що наріс попередньому власнику, —
	// а оцінювався він «чисто», за самим номіналом; різниця зникала. На
	// молодому портфелі це давало XIRR під −40% там, де насправді не
	// втрачено нічого: 8 417 сплачено, 8 000 «залишилось», 140 зароблених
	// НКД у розрахунку не існувало.
	var terminal int64
	for _, l := range lots {
		if l.PricePerBond.Currency().Code != currency {
			continue
		}
		b, ok := bonds[l.ISIN]
		if !ok || b.Maturity.Before(asOf) {
			continue
		}
		rem := RemainingQtyNow(l, sales)
		terminal += b.Nominal.Amount() * rem
		if acc, aerr := EstimateAccrued(payments, l.ISIN, asOf); aerr == nil && acc != nil && !acc.IsZero() {
			terminal += acc.Amount() * rem
		}
	}
	if terminal > 0 {
		flows = append(flows, Flow{Date: asOf, Amount: terminal})
	}
	sort.Slice(flows, func(i, j int) bool { return flows[i].Date < flows[j].Date })
	return flows, nil
}

// FundFlows — грошові потоки сертифікатів фондів для XIRR.
//
// У фонду немає ні номіналу, ні графіка, тож дохідність до погашення для
// нього не існує — але ФАКТИЧНО зароблене міряється так само, як у
// облігацій: купівля забирає гроші, продаж і дивіденд приносять, а те, що
// лишилось, оцінюється за останньою відомою ціною. Без цих потоків XIRR
// показував дохідність облігаційної частини, видаючи її за портфельну.
//
// Дивіденд береться ПІСЛЯ податку: до тебе дійшло саме стільки. Податок із
// продажу так само зменшує виручку.
func FundFlows(ops []FundOp, currency string, asOf Date) []Flow {
	var flows []Flow
	for _, op := range ops {
		if op.Currency != currency || op.Date.After(asOf) {
			continue
		}
		switch op.Kind {
		case FundBuy:
			flows = append(flows, Flow{Date: op.Date, Amount: -op.Amount})
		case FundSell:
			flows = append(flows, Flow{Date: op.Date, Amount: op.Amount - op.Tax})
		case FundDividend:
			flows = append(flows, Flow{Date: op.Date, Amount: op.Amount - op.Tax})
		}
	}
	if len(flows) == 0 {
		return nil
	}
	// Термінальна вартість — залишок за останньою ціною, тією самою, що
	// показана в позиції: інше джерело дало б XIRR, який не сходиться з
	// видимими числами.
	var terminal int64
	for _, p := range FundPositions(ops) {
		if p.Currency == currency {
			terminal += p.MarketValue()
		}
	}
	if terminal > 0 {
		flows = append(flows, Flow{Date: asOf, Amount: terminal})
	}
	sort.Slice(flows, func(i, j int) bool { return flows[i].Date < flows[j].Date })
	return flows
}

// FundFlowsOne — потоки ОДНОГО фонду. FundFlows поруч зводить усі фонди
// однієї валюти разом, бо відповідає на питання «скільки заробив
// портфель»; тут питання інше — «скільки заробив цей фонд», і змішувати
// його з сусідніми не можна.
func FundFlowsOne(ops []FundOp, fund string, asOf Date) []Flow {
	var flows []Flow
	for _, op := range ops {
		if op.Fund != fund || op.Date.After(asOf) {
			continue
		}
		switch op.Kind {
		case FundBuy:
			flows = append(flows, Flow{Date: op.Date, Amount: -op.Amount})
		case FundSell, FundDividend:
			flows = append(flows, Flow{Date: op.Date, Amount: op.Amount - op.Tax})
		}
	}
	if len(flows) == 0 {
		return nil
	}
	// Термінальна вартість — залишок за останньою ціною, тією самою, що
	// показана в позиції: інше джерело дало б дохідність, яка не
	// сходиться з видимими числами.
	if p := FundPositions(ops)[fund]; p != nil && p.MarketValue() > 0 {
		flows = append(flows, Flow{Date: asOf, Amount: p.MarketValue()})
	}
	sort.Slice(flows, func(i, j int) bool { return flows[i].Date < flows[j].Date })
	return flows
}

// MoneyWeightedDays — скільки в СЕРЕДНЬОМУ вже працюють вкладені гроші,
// зважено сумами. Вік першого потоку тут не годиться: портфель, де
// давня купівля дрібна, а вчорашня велика, за ним виглядав би зрілим,
// хоча майже всі гроші пролежали день, і їхня ануалізована дохідність —
// самий лише шум.
func MoneyWeightedDays(flows []Flow, asOf Date) float64 {
	var wDays, wMoney float64
	for _, f := range flows {
		if f.Amount >= 0 {
			continue // повернення, не вкладення
		}
		wDays += float64(DaysBetween(f.Date, asOf)) * float64(-f.Amount)
		wMoney += float64(-f.Amount)
	}
	if wMoney <= 0 {
		return 0
	}
	return wDays / wMoney
}

// DepositFlows — грошові потоки банківських вкладів для XIRR.
//
// Так само, як у фондів, показник міряє фактично зароблене на вкладених
// грошах. Розміщення забирає тіло, отримані відсотки (нетто, після
// податку) приносять, і те, що ще замкнене, оцінюється за тілом (вклад
// повертає рівно його). Закритий вклад — фактом: тіло геть при відкритті,
// ClosedAmount назад при розірванні.
func DepositFlows(deposits []Deposit, currency string, asOf Date) []Flow {
	var flows []Flow
	var terminal int64
	got := false
	for _, d := range deposits {
		if d.Currency != currency || d.OpenDate.After(asOf) {
			continue
		}
		got = true
		flows = append(flows, Flow{Date: d.OpenDate, Amount: -d.Principal})
		// Кожне поповнення — теж вкладені гроші: відтік на свою дату.
		for _, t := range d.Topups {
			if !t.Date.After(asOf) {
				flows = append(flows, Flow{Date: t.Date, Amount: -t.Amount})
			}
		}

		if d.ClosedDate != "" {
			if !d.ClosedDate.After(asOf) {
				flows = append(flows, Flow{Date: d.ClosedDate, Amount: d.ClosedAmount})
			} else {
				terminal += d.BalanceAt(asOf)
			}
			continue
		}
		// Реалізовані відсотки — ті, що вже надійшли (дата ≤ asOf).
		for _, cf := range d.interestFlows() {
			if !cf.Date.After(asOf) {
				flows = append(flows, Flow{Date: cf.Date, Amount: cf.Amount.Amount()})
			}
		}
		// Тіло: повернене (накопичене) на погашенні; інакше ще замкнене
		// й оцінюється балансом на asOf.
		if !d.MaturityDate.After(asOf) {
			flows = append(flows, Flow{Date: d.MaturityDate, Amount: d.BalanceAt(d.MaturityDate)})
		} else {
			terminal += d.BalanceAt(asOf)
		}
	}
	if !got {
		return nil
	}
	if terminal > 0 {
		flows = append(flows, Flow{Date: asOf, Amount: terminal})
	}
	sort.Slice(flows, func(i, j int) bool { return flows[i].Date < flows[j].Date })
	return flows
}
