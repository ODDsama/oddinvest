package domain

import (
	"sort"
	"strconv"
	"strings"

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
	// Replenishable — чи приймає вклад поповнення. Впливає лише на ПОРАДИ:
	// помічник реінвесту пропонує докласти тільки в такий вклад. Сам запис
	// поповнення прапорцем не обмежений — дані завжди сильніші за
	// налаштування, і виправляти факт через галочку було б дивно.
	Replenishable bool
	// Topups — ручні поповнення вкладу. Кожне докладання росте тіло від
	// своєї дати, і відсотки далі йдуть на більший баланс. Записи, а не
	// припущення: пропущений місяць не роздуває числа.
	Topups []DepositTopup
}

// DepositTopup — одне поповнення вкладу. Amount — мінорні, у валюті вкладу.
type DepositTopup struct {
	ID        int64
	DepositID int64
	Date      Date
	Amount    int64
}

// BalanceAt — тіло вкладу, накопичене до дати on (включно): початкове
// плюс усі поповнення з датою ≤ on. Без поповнень дорівнює Principal, тож
// уся решта логіки на вкладах без топапів поводиться як раніше.
func (d Deposit) BalanceAt(on Date) int64 {
	b := d.Principal
	for _, t := range d.Topups {
		if !t.Date.After(on) {
			b += t.Amount
		}
	}
	return b
}

// topupDatesIn — дати поповнень СТРОГО в (from, to), відсортовані: точки,
// де баланс стрибає всередині періоду нарахування.
func (d Deposit) topupDatesIn(from, to Date) []Date {
	var out []Date
	for _, t := range d.Topups {
		if from.Before(t.Date) && t.Date.Before(to) {
			out = append(out, t.Date)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// topupsBetween — сума поповнень у (from, to] (from виключно, to включно).
func (d Deposit) topupsBetween(from, to Date) int64 {
	var sum int64
	for _, t := range d.Topups {
		if from.Before(t.Date) && !to.Before(t.Date) {
			sum += t.Amount
		}
	}
	return sum
}

// accruedInterest — брутто прості відсотки за (from, to] на балансі, що
// росте: проміжок ділиться датами поповнень, на кожному сегменті відсоток
// рахується від балансу на його початку.
func (d Deposit) accruedInterest(from, to Date) int64 {
	var gross int64
	segStart := from
	for _, t := range d.topupDatesIn(from, to) {
		gross += simpleInterest(d.BalanceAt(segStart), d.RateBP, DaysBetween(segStart, t))
		segStart = t
	}
	gross += simpleInterest(d.BalanceAt(segStart), d.RateBP, DaysBetween(segStart, to))
	return gross
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
	var out []CashflowItem
	// Відсотки на дату asOf і пізніше: минулі у майбутнє не входять.
	for _, cf := range d.interestFlows() {
		if !cf.Date.Before(asOf) {
			out = append(out, cf)
		}
	}
	// Повернення НАКОПИЧЕНОГО тіла (початкове + усі поповнення) на дату
	// погашення. Active гарантує, що вона ≥ asOf.
	out = append(out, CashflowItem{Date: d.MaturityDate, ISIN: d.SyntheticISIN(),
		Type: PayRedemption, Amount: money.New(d.BalanceAt(d.MaturityDate), d.Currency)})
	return out
}

// interestFlows — усі виплати відсотків вкладу (нетто, після податку) за
// ВЕСЬ строк, без огляду на asOf і статус розірвання. Це спільне ядро:
// майбутній графік (DepositSchedule) фільтрує його по даті, а XIRR
// (DepositFlows) бере з нього реалізовані. Тіло сюди не входить.
func (d Deposit) interestFlows() []CashflowItem {
	isin := d.SyntheticISIN()
	cur := d.Currency
	var out []CashflowItem
	if d.Payout == PayoutEnd {
		// Одна виплата в кінці. Капіталізація — складний відсоток помісячно
		// до погашення; без неї — прості за весь строк на балансі, що росте.
		gross := d.accruedInterest(d.OpenDate, d.MaturityDate)
		if d.Capitalized {
			gross = d.compoundInterest()
		}
		if net := d.netInterest(gross); net > 0 {
			out = append(out, CashflowItem{Date: d.MaturityDate, ISIN: isin,
				Type: PayCoupon, Amount: money.New(net, cur)})
		}
		return out
	}
	// Періодичні виплати: прості відсотки на баланс за кожен проміжок.
	prev := d.OpenDate
	for _, pd := range d.interestDates() {
		gross := d.accruedInterest(prev, pd)
		prev = pd
		if net := d.netInterest(gross); net > 0 {
			out = append(out, CashflowItem{Date: pd, ISIN: isin,
				Type: PayCoupon, Amount: money.New(net, cur)})
		}
	}
	return out
}

// DepositCashflows — майбутні потоки ВСІХ вкладів від asOf, відсортовані.
// Лягають у той самий календар, що й купони.
func DepositCashflows(deposits []Deposit, asOf Date) []CashflowItem {
	var out []CashflowItem
	for _, d := range deposits {
		out = append(out, DepositSchedule(d, asOf)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// DepositLadder — повернення тіла вкладів по роках погашення, окремо по
// валютах: у драбину поряд із номіналом ОВДП. Розірвані й погашені вклади
// тіла в майбутньому не повертають, тож не входять.
func DepositLadder(deposits []Deposit, asOf Date) []LadderEntry {
	type key struct {
		year int
		cur  string
	}
	agg := map[key]int64{}
	for _, d := range deposits {
		if !d.Active(asOf) {
			continue
		}
		agg[key{d.MaturityDate.Year(), d.Currency}] += d.BalanceAt(d.MaturityDate)
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

// compoundInterest — брутто-відсоток за весь строк при помісячній
// капіталізації: тіло щомісяця приростає на простий відсоток за той
// місяць і далі теж приносить відсоток. Повертає САМ відсоток — різницю
// між кінцевим балансом і всім внесеним тілом (початкове + поповнення).
//
// Поповнення місяця додаються до бази ПІСЛЯ нарахування відсотка того
// місяця, тобто починають працювати з наступного місяця. Це трохи
// консервативно (докладене мід-місяця не заробляє частину місяця), зате
// просто й ніколи не завищує.
func (d Deposit) compoundInterest() int64 {
	base := d.Principal
	prev := d.OpenDate
	cur := d.OpenDate.AddMonths(1)
	for cur.Before(d.MaturityDate) {
		base += simpleInterest(base, d.RateBP, DaysBetween(prev, cur))
		base += d.topupsBetween(prev, cur)
		prev = cur
		cur = cur.AddMonths(1)
	}
	base += simpleInterest(base, d.RateBP, DaysBetween(prev, d.MaturityDate))
	base += d.topupsBetween(prev, d.MaturityDate)
	return base - d.BalanceAt(d.MaturityDate)
}

// DepositISINPrefix — префікс синтетичного ключа вкладу.
const DepositISINPrefix = "deposit:"

// SyntheticISIN — ключ вкладу для календаря й статусів виплат. Вклад
// справжнього ISIN не має, тож синтезуємо стабільний із id.
func (d Deposit) SyntheticISIN() string {
	return DepositISINPrefix + strconv.FormatInt(d.ID, 10)
}

// IsDepositISIN — чи належить потік вкладу, а не облігації. Знадобилось,
// коли процентний ризик довелось рахувати роздільно: облігація має
// вторинний ринок і переоцінюється, вклад — ні, і рахувати йому просадку
// від зміни ставок означає вигадувати збиток, якого не буде.
func IsDepositISIN(isin string) bool {
	return strings.HasPrefix(isin, DepositISINPrefix)
}
