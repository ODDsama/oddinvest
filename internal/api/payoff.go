// План погашення: коли це скінчиться і скільки коштуватиме дорогою.
//
// # ЩО САМЕ СТОЇТЬ У ЧЕРЗІ, А ЩО НІ
//
// Лише СПРАВЖНІЙ борг — той, на який нараховують. Це розстрочки (кожна зі
// своєю комісією) і непільгова частина картки: готівка й перекази, у яких
// пільгового немає ніколи. Оборот у межах пільгового періоду сюди НЕ
// входить, і це головна межа всієї фази: зарплата, покладена на картку, і
// покупки з неї вже описані витратами й часткою потоку в портфель, тож
// друге їх віднімання відняло б побут двічі. Ціна помилки з пільговим —
// окреме число (grace_cost), а не рядок черги.
//
// # ЩО ДАЄ ДОСТРОКОВЕ ПОГАШЕННЯ РОЗСТРОЧКИ
//
// Не «зекономлені відсотки на залишок», як у кредиті, а СКАСОВАНІ КОМІСІЇ
// майбутніх місяців. Комісія береться від початкової суми й нараховується,
// доки розстрочка жива; закрив на три місяці раніше — трьох комісій не
// буде. Саме тому черга за ставкою (лавина) тут не косметика: різниця між
// 50% і 0% розстрочкою — це різниця між «гасити передусім» і «не чіпати».
//
// # ПРИПУЩЕННЯ, ЯКІ ТУТ Є, І ЧОМУ ВОНИ НАЗВАНІ
//
// Прохід уперед моделює місяці, а не дні: платежі падають у свій місяць
// цілком. Для питання «скільки місяців і скільки грошей» денна точність
// нічого не додає, а вимагала б знати дату кожного надходження — тобто
// повторити «Маршрут грошей», який відповідає на інше питання.
//
// Гривня одна: усе зводиться грн-еквівалентом за СЬОГОДНІШНІМ курсом, як
// це вже робить deriveGoals. Борг у валюті в застосунку можливий, але
// зростання курсу на горизонті погашення тут не моделюється — і це
// сказано, а не сховане.
package api

import (
	"math"
	"sort"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
)

// Стратегії. Рядками, бо приходять параметром запиту і їдуть у JSON.
const (
	// payoffAvalanche — спершу найдорожчий за ставкою. Дає найменшу
	// переплату завжди; це арифметика, а не думка.
	payoffAvalanche = "avalanche"
	// payoffSnowball — спершу найменший за залишком. Платить більше, але
	// закриває перший борг раніше, і рядків у списку меншає швидше.
	payoffSnowball = "snowball"
	// payoffMinimum — нічого понад обовʼязкове. Не порада, а лінійка: без
	// неї «швидше на 4 місяці» немає з чим порівняти.
	payoffMinimum = "minimum"
)

// payoffMaxMonths — стеля проходу. 50 років: усе, що довше, читається як
// «ніколи», і зайві рядки цього не уточнять.
const payoffMaxMonths = 600

// payoffDebt — борг у вигляді, придатному до проходу вперед. Усе в
// грн-еквіваленті й у копійках.
type payoffDebt struct {
	ID        int64
	Name      string
	Kind      string
	Rate      float64
	RateBasis string

	// Left — скільки ТІЛА лишилось гасити.
	Left int64

	// --- розстрочка ---
	// perMonth — тіло в одному плановому платежі; feeMonth — комісія в
	// ньому. Обидва беруться з domain.InstallmentSchedule, а не рахуються
	// вдруге: графік має одне означення на застосунок.
	perMonth   int64
	feeMonth   int64
	firstMonth int
	feeFreeTo  int // до якого місяця проходу комісії ще немає

	// --- картка ---
	monthlyRate float64
	minBp       int64
	minFloor    int64
}

// payoffStep — один місяць одного боргу.
type payoffStep struct {
	Month     int
	DebtID    int64
	Paid      int64
	Principal int64
	Cost      int64 // комісія або відсоток — те, що лишається банку
	Extra     int64 // скільки з Paid пішло понад обовʼязкове
}

// payoffRun — підсумок одного проходу.
type payoffRun struct {
	Months   int
	Paid     int64
	Cost     int64
	CloseAt  map[int64]int // борг → місяць закриття
	Steps    []payoffStep
	Unfunded bool // обовʼязкових платежів не вистачило навіть на мінімум
}

// buildPayoffDebts перетворює борги, звірки й рухи на вхід проходу.
//
// Картка потрапляє сюди ЛИШЕ непільговою частиною (готівка, перекази) —
// довід у шапці файла. Картка без неї у черзі не зʼявляється взагалі, і це
// правильна відповідь, а не пропуск.
func buildPayoffDebts(debts []domain.Debt, marks []domain.DebtMark,
	ops []domain.DebtOp, rates fx.Rates, today domain.Date) []payoffDebt {

	toUAH := func(minor int64, cur string) int64 {
		if minor == 0 {
			return 0
		}
		u, err := fx.ToUAH(money.New(minor, cur), rates)
		if err != nil {
			// Невідомий код валюти: борг лишається у власних одиницях, а
			// не зникає. Утратити рядок боргу гірше, ніж показати його не
			// в тій валюті, — і на екрані валюта названа.
			return minor
		}
		return u.Amount()
	}

	out := make([]payoffDebt, 0, len(debts))
	for _, d := range debts {
		if d.Closed() {
			continue
		}
		rate, basis := domain.DebtEffectiveRate(d, payoffCardDebt(d, marks, ops, today))
		p := payoffDebt{ID: d.ID, Name: d.Name, Kind: d.Kind,
			Rate: rate, RateBasis: basis}

		if d.IsCard() {
			left := payoffCardDebt(d, marks, ops, today)
			if left <= 0 {
				continue
			}
			p.Left = toUAH(left, d.Currency)
			p.monthlyRate = float64(d.APRBp) / 10000 / 12
			p.minBp, p.minFloor = d.MinPaymentBp, toUAH(d.MinPaymentFloor, d.Currency)
			out = append(out, p)
			continue
		}

		sched := domain.InstallmentSchedule(d)
		first, seen := -1, false
		for _, s := range sched {
			if s.Date.Before(today) {
				continue
			}
			p.Left += toUAH(s.Principal, d.Currency)
			if !seen {
				first, seen = domain.MonthsBetween(today, s.Date), true
				p.perMonth = toUAH(s.Principal, d.Currency)
			}
			if s.Fee > 0 && p.feeMonth == 0 {
				p.feeMonth = toUAH(s.Fee, d.Currency)
			}
		}
		if !seen || p.Left <= 0 {
			continue
		}
		if first < 0 {
			first = 0
		}
		p.firstMonth = first
		// Скільки ще безкомісійних місяців лишилось попереду — рахуємо по
		// самому графіку, а не по FeeFreeMonths: половина пільгових
		// місяців могла вже минути.
		for _, s := range sched {
			if s.Date.Before(today) || s.Fee > 0 {
				continue
			}
			if m := domain.MonthsBetween(today, s.Date); m+1 > p.feeFreeTo {
				p.feeFreeTo = m + 1
			}
		}
		out = append(out, p)
	}
	return out
}

// payoffCardDebt — та частина боргу картки, на яку НАРАХОВУЮТЬ: готівка й
// перекази. Решта живе в пільговому циклі й у чергу погашення не входить.
func payoffCardDebt(d domain.Debt, marks []domain.DebtMark,
	ops []domain.DebtOp, today domain.Date) int64 {

	if !d.IsCard() {
		return 0
	}
	st := domain.CardState(d, marks, ops, nil, today)
	if st.NonGrace <= 0 {
		return 0
	}
	// Не більше за весь борг: непільгову частину міряє звірка, а борг міг
	// відтоді просісти платежами.
	if st.Debt > 0 && st.NonGrace > st.Debt {
		return st.Debt
	}
	return st.NonGrace
}

// runPayoff проходить місяці вперед, доки борги не закриються.
//
// extra — скільки гривень на місяць є ПОНАД обовʼязкові платежі. Воно йде
// цілком у голову черги: розмазування по всіх боргах відкладає закриття
// кожного, тобто продовжує платити комісію всім одночасно.
func runPayoff(debts []payoffDebt, strategy string, extra int64) payoffRun {
	run := payoffRun{CloseAt: map[int64]int{}}
	live := make([]payoffDebt, len(debts))
	copy(live, debts)

	for month := 0; month < payoffMaxMonths; month++ {
		left := 0
		for i := range live {
			if live[i].Left > 0 {
				left++
			}
		}
		if left == 0 {
			run.Months = month
			return run
		}

		pool := extra
		if strategy == payoffMinimum {
			pool = 0
		}
		// 1. Обовʼязкове — до останньої копійки й незалежно від стратегії.
		for i := range live {
			d := &live[i]
			if d.Left <= 0 {
				continue
			}
			pay, principal, cost := payoffMandatory(d, month)
			if pay == 0 {
				continue
			}
			run.record(month, d, pay, principal, cost, 0)
			if d.Left <= 0 {
				run.CloseAt[d.ID] = month
			}
		}
		// 2. Дострокове — у голову черги, доки є що віддати.
		if pool > 0 {
			for _, i := range payoffOrder(live, strategy) {
				d := &live[i]
				if d.Left <= 0 || pool <= 0 {
					continue
				}
				take := d.Left
				if take > pool {
					take = pool
				}
				d.Left -= take
				pool -= take
				run.record(month, d, take, take, 0, take)
				if d.Left <= 0 {
					run.CloseAt[d.ID] = month
				}
			}
		}
	}
	run.Months = payoffMaxMonths
	run.Unfunded = true
	return run
}

// record додає крок і зводить підсумки. Окремим методом, бо місць, звідки
// пишуться кроки, два, і другий екземпляр цих трьох рядків розійшовся б із
// першим на першій же правці.
func (r *payoffRun) record(month int, d *payoffDebt, paid, principal, cost, extra int64) {
	r.Steps = append(r.Steps, payoffStep{
		Month: month, DebtID: d.ID, Paid: paid,
		Principal: principal, Cost: cost, Extra: extra,
	})
	r.Paid += paid
	r.Cost += cost
}

// payoffMandatory — обовʼязковий платіж цього місяця. Повертає скільки
// сплачено, скільки з того пішло в тіло й скільки лишилось банку.
func payoffMandatory(d *payoffDebt, month int) (paid, principal, cost int64) {
	if d.Kind == domain.DebtCard {
		interest := int64(math.Round(float64(d.Left) * d.monthlyRate))
		pay := d.Left * d.minBp / 10000
		if pay < d.minFloor {
			pay = d.minFloor
		}
		if pay > d.Left+interest {
			pay = d.Left + interest
		}
		p := pay - interest
		if p < 0 {
			// Мінімалка не покриває навіть відсотка — борг росте. Саме це
			// й треба побачити, а не згладити.
			p = 0
		}
		d.Left = d.Left + interest - pay
		if d.Left < 0 {
			d.Left = 0
		}
		return pay, p, interest
	}
	if month < d.firstMonth {
		return 0, 0, 0
	}
	p := d.perMonth
	if p > d.Left {
		p = d.Left
	}
	fee := d.feeMonth
	if month < d.feeFreeTo {
		fee = 0
	}
	d.Left -= p
	return p + fee, p, fee
}

// payoffOrder — у якому порядку віддавати дострокові гроші.
//
// Лавина за ставкою, сніжок за залишком; рівних розводить id, щоб порядок
// не залежав від того, як база віддала рядки.
func payoffOrder(debts []payoffDebt, strategy string) []int {
	idx := make([]int, 0, len(debts))
	for i := range debts {
		idx = append(idx, i)
	}
	sort.SliceStable(idx, func(a, b int) bool {
		x, y := debts[idx[a]], debts[idx[b]]
		if strategy == payoffSnowball {
			if x.Left != y.Left {
				return x.Left < y.Left
			}
			return x.ID < y.ID
		}
		if x.Rate != y.Rate {
			return x.Rate > y.Rate
		}
		return x.ID < y.ID
	})
	return idx
}

// payoffGraceCost — ціна ДВОХ помилок із карткою за один місяць.
//
// Два числа, бо помилки дві й вони різні: не закрив пільгову суму —
// відсоток на решту за звичайною ставкою; пропустив мінімалку — штраф і
// підвищена ставка на весь борг. Друга дорожча, але перша трапляється
// частіше, і одне число на обидві сховало б саме ту, яку легше зробити.
func payoffGraceCost(d domain.Debt, st domain.CardStatus) (missFull, missMin int64) {
	if !d.IsCard() || st.StatementDue <= 0 {
		return 0, 0
	}
	missFull = int64(math.Round(float64(st.StatementDue) * float64(d.APRBp) / 10000 / 12))

	// ЦІНА ДРУГОЇ ПОМИЛКИ МОВЧИТЬ, КОЛИ ЇЇ НЕМА З ЧОГО РАХУВАТИ.
	//
	// Доти при незаданій підвищеній ставці підставлялась звичайна, а штраф
	// брався нулем — і два РІЗНІ за ціною ризики виходили одним числом,
	// показаним двічі. Спіймано вживу: «не закрити виписку» і «пропустити
	// мінімалку» стояли поруч із однаковими 207,96 ₴, тобто екран
	// стверджував, що прострочити мінімалку не дорожче, ніж просто не
	// закрити виписку. Це неправда для будь-якого договору.
	//
	// Нуль тут означає «не рахували», і той, хто показує, мусить сказати
	// це словами замість числа.
	if d.APROverdueBp <= 0 && d.LateFee <= 0 {
		return missFull, 0
	}
	overdue := d.APROverdueBp
	if overdue <= 0 {
		overdue = d.APRBp
	}
	base := st.Debt
	if base < st.StatementDue {
		base = st.StatementDue
	}
	missMin = d.LateFee + int64(math.Round(float64(base)*float64(overdue)/10000/12))
	return missFull, missMin
}
