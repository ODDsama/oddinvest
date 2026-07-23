package domain

import (
	"strconv"

	money "github.com/Rhymond/go-money"
)

// Банківський строковий вклад.
//
// Третій інструмент поряд з ОВДП і фондами. Від облігації відрізняється
// тим, що НЕ має вторинного ринку (тобто ні ціни, ні дюрації) і що
// відсотки ОПОДАТКОВУЮТЬСЯ — купон ОВДП звільнений, а відсотки вкладу ні.
// Від фонду — тим, що має відомий наперед графік і фіксовану ставку, а не
// нерегулярну оцінку з останньої виплати.
//
// Коротко: розклад, як в облігації, але оподаткований, як фонд, і без
// вторинного ринку. Тому власна таблиця й модель, а не втискання в lots
// чи fund_ops.

// DepositPayout — коли банк виплачує відсотки.
type DepositPayout string

const (
	PayoutEnd       DepositPayout = "end"       // усі відсотки в кінці строку
	PayoutMonthly   DepositPayout = "monthly"   // щомісяця на рахунок
	PayoutQuarterly DepositPayout = "quarterly" // щокварталу на рахунок
)

// Deposit — один вклад.
//
// Суми (Principal, ClosedAmount) — мінорні одиниці валюти вкладу.
// RateBP — річна ставка × 100 (16.5% = 1650), як RateBP у Bond.
// TaxBP — ставка податку на відсотки × 100 (19.5% = 1950). Зберігаємо
// саме СТАВКУ, а не суму: відсотки рахуються з контракту, тож і податок з
// них рахується, а не вводиться (на відміну від фондів, де дивіденд
// нерегулярний і податок беруть фактом із виписки).
type Deposit struct {
	ID           int64
	Bank         string // рахунок, на якому лежить вклад (як Channel у лота)
	Currency     string
	Principal    int64
	RateBP       int64
	OpenDate     Date
	MaturityDate Date
	Payout       DepositPayout
	// Capitalized — відсотки додаються до тіла й далі теж приносять
	// відсоток. Має сенс лише при Payout=end: якщо відсотки й так щомісяця
	// йдуть на рахунок, капіталізувати нема чого.
	Capitalized bool
	TaxBP       int64
	// ClosedDate/ClosedAmount — дострокове розірвання. Закритий вклад
	// живе за ФАКТОМ, а не за формулою: банк перерахував відсотки за
	// штрафною ставкою й видав суму, яку користувач і вводить (як фактичну
	// ціну при продажу лота). Штрафну ставку ми не рахуємо. "" = діє.
	ClosedDate   Date
	ClosedAmount int64
	Note         string
}

// Active — чи вклад ще діє (не розірваний і не погашений на дату asOf).
func (d Deposit) Active(asOf Date) bool {
	if d.ClosedDate != "" {
		return false
	}
	return !d.MaturityDate.Before(asOf)
}

// netInterest — відсоток за вирахуванням податку, у мінорних одиницях.
// Округлення донизу цілочисельним діленням: копійка похибки на вклад тут
// дешевша за плутанину з half-to-even у контексті, де сам податок — оцінка.
func (d Deposit) netInterest(gross int64) int64 {
	if gross <= 0 {
		return 0
	}
	tax := gross * d.TaxBP / 10000
	return gross - tax
}

// simpleInterest — прості відсотки на base за period днів, ACT/365, у
// мінорних одиницях (брутто, до податку).
func simpleInterest(base, rateBP int64, days int) int64 {
	if days <= 0 || base <= 0 {
		return 0
	}
	return base * rateBP * int64(days) / (10000 * 365)
}

// interestDates — дати виплат відсотків від OpenDate до MaturityDate за
// кроком payout (місяць / квартал). Остання дата — рівно MaturityDate,
// навіть якщо крок у неї не влучає: банк доплачує залишок за строком.
func (d Deposit) interestDates() []Date {
	step := 0
	switch d.Payout {
	case PayoutMonthly:
		step = 1
	case PayoutQuarterly:
		step = 3
	default:
		return []Date{d.MaturityDate} // end: одна виплата в кінці
	}
	var dates []Date
	cur := d.OpenDate.AddMonths(step)
	for cur.Before(d.MaturityDate) {
		dates = append(dates, cur)
		cur = cur.AddMonths(step)
	}
	dates = append(dates, d.MaturityDate)
	return dates
}

// DepositSchedule — МАЙБУТНІ потоки вкладу від asOf: відсотки (нетто,
// після податку) і повернення тіла на дату погашення.
//
// Закритий вклад майбутніх потоків не має: його розірвання — минула
// подія, і сума вже на рахунку. Так само нічого не повертаємо для вкладу,
// що вже погасився до asOf.
//
// Потоки — CashflowItem із синтетичним ISIN "deposit:<id>": так вони
// лягають у той самий календар і ту саму механіку статусів, що й купони,
// без окремої таблиці. Type: PayCoupon для відсотків, PayRedemption для
// тіла.
func DepositSchedule(d Deposit, asOf Date) []CashflowItem {
	if !d.Active(asOf) {
		return nil
	}
	isin := d.SyntheticISIN()
	cur := d.Currency
	var out []CashflowItem

	dates := d.interestDates()

	if d.Payout == PayoutEnd {
		// Одна виплата відсотків у кінці. Капіталізація — складний
		// відсоток помісячно до погашення; без неї — прості за весь строк.
		var gross int64
		if d.Capitalized {
			gross = d.compoundInterest()
		} else {
			gross = simpleInterest(d.Principal, d.RateBP, DaysBetween(d.OpenDate, d.MaturityDate))
		}
		if net := d.netInterest(gross); net > 0 && !d.MaturityDate.Before(asOf) {
			out = append(out, CashflowItem{Date: d.MaturityDate, ISIN: isin,
				Type: PayCoupon, Amount: money.New(net, cur)})
		}
	} else {
		// Періодичні виплати: прості відсотки на тіло за кожен проміжок.
		prev := d.OpenDate
		for _, pd := range dates {
			gross := simpleInterest(d.Principal, d.RateBP, DaysBetween(prev, pd))
			prev = pd
			if pd.Before(asOf) {
				continue // виплата вже минула — у майбутнє не входить
			}
			if net := d.netInterest(gross); net > 0 {
				out = append(out, CashflowItem{Date: pd, ISIN: isin,
					Type: PayCoupon, Amount: money.New(net, cur)})
			}
		}
	}

	// Повернення тіла на дату погашення.
	out = append(out, CashflowItem{Date: d.MaturityDate, ISIN: isin,
		Type: PayRedemption, Amount: money.New(d.Principal, cur)})
	return out
}

// compoundInterest — брутто-відсоток за весь строк при помісячній
// капіталізації: тіло щомісяця приростає на простий відсоток за той
// місяць і далі теж приносить відсоток. Повертає САМ відсоток (без тіла).
func (d Deposit) compoundInterest() int64 {
	base := d.Principal
	prev := d.OpenDate
	cur := d.OpenDate.AddMonths(1)
	for cur.Before(d.MaturityDate) {
		base += simpleInterest(base, d.RateBP, DaysBetween(prev, cur))
		prev = cur
		cur = cur.AddMonths(1)
	}
	base += simpleInterest(base, d.RateBP, DaysBetween(prev, d.MaturityDate))
	return base - d.Principal
}

// SyntheticISIN — ключ вкладу для календаря й статусів виплат. Вклад
// справжнього ISIN не має, тож синтезуємо стабільний із id.
func (d Deposit) SyntheticISIN() string {
	return "deposit:" + strconv.FormatInt(d.ID, 10)
}
