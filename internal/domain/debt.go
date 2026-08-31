// Борг: пільговий цикл картки, графік розстрочки й чесна ставка.
//
// # ГОЛОВНЕ, ЩО РОБИТЬ ЦЕЙ ФАЙЛ
//
// Перетворює «0% і комісія 1,99% на місяць» на число, з яким можна щось
// зробити. Комісія рахується ВІД ПОЧАТКОВОЇ суми, а тіло щомісяця спадає,
// тож «1,99 × 12 = 23,9% річних» — не ставка, а сума знижок, яких не було.
// Справжня ціна тих самих грошей — близько 43% річних, і виводиться вона
// тим самим XIRR, яким застосунок міряє власну дохідність (xirr.go). Без
// цього числа чергу погашення скласти нема з чого: 0%-розстрочка й
// 43%-розстрочка виглядають в анкеті банку однаково.
//
// # ЧОМУ ПІЛЬГОВИЙ ЦИКЛ — ЧИСЛО МІСЯЦЯ, А НЕ СТРОК
//
// «До 62 днів без відсотків» — це НАСЛІДОК, а не правило. Правило: покупки
// звітного періоду треба закрити до наступної розрахункової дати (у ПУМБ
// на вибір 10, 20 або 30 число). Покупка наступного дня після виписки має
// ~62 дні, покупка напередодні наступної — ~31. Зберігати 62 означало б
// зберегти найкращий випадок як правило й обіцяти місяць, якого немає.
//
// # ДВА ПОРОГИ, А НЕ ОДИН
//
// Мінімальний платіж і повна сума виписки — різні твердження з різною
// ціною, і зводити їх в одне число не можна:
//
//	пропустив мінімалку   → штраф і підвищена ставка на весь борг;
//	вніс мінімалку, але не всю суму → відсотки на решту за звичайною ставкою.
//
// Друга помилка дешевша за першу, і одне число на обидві сховало б саме
// її — тобто підказувало б платити мінімалку там, де людина цілком могла
// закрити все.
//
// # ЧОГО ТУТ НЕМАЄ
//
// Ретроспективного нарахування «з дати кожної покупки». Банки рахують
// пільговий по-різному, договір у кожного свій, а вгадана модель дала б
// точне на вигляд число, яке не збігається з випискою. Замість неї —
// виміряні числа звірки (DebtMark) і чесний показ віку тієї звірки.
package domain

import (
	"math"
	"time"
)

// Види боргу. Рядками, бо їдуть у JSON і в CHECK міграції 0045.
const (
	DebtCard        = "card"
	DebtInstallment = "installment"
)

// Види руху. payment зменшує борг, draw і cash — збільшують; різниця між
// останніми двома в тому, що на cash пільговий не поширюється НІКОЛИ.
const (
	DebtOpPayment = "payment"
	DebtOpDraw    = "draw"
	DebtOpCash    = "cash"
)

// Звідки взялася ставка боргу. Показується поруч із нею: 43% «з графіка» і
// 43% «зі слів банку» — різні за надійністю твердження.
const (
	// DebtRateFromSchedule — виведено з платежів через XIRR. Єдине чесне
	// джерело для розстрочки з комісією.
	DebtRateFromSchedule = "graph"
	// DebtRateCompound — заявлена річна ставка, перерахована з місячною
	// капіталізацією: борг картки, який переїхав за пільговий, щомісяця
	// обростає відсотком, і той відсоток далі теж приносить відсоток.
	DebtRateCompound = "compound"
	// DebtRateNone — рахувати нема з чого (ставка не задана, графіка
	// немає). Нуль тут означав би «безкоштовно», тому його не буває.
	DebtRateNone = "none"
)

// Debt — кредитна картка або розстрочка.
//
// Одна структура на два види: половина полів у кожному випадку мертва, і
// це названо в міграції 0045 поіменно. Дві структури означали б два
// списки, дві черги погашення й два рейтинги — а питання одне: скільки я
// винен і під скільки.
type Debt struct {
	ID       int64
	Name     string
	Kind     string
	Currency string
	// CardID — розстрочка всередині картки: її щомісячна частина падає у
	// виписку цієї картки. 0 = самостійний борг (у базі NULL).
	CardID int64

	// --- картка ---
	LimitAmount int64
	// StatementDay — розрахункова дата, число місяця. Довід, чому саме
	// число, — у шапці файла.
	StatementDay    int64
	APRBp           int64
	APROverdueBp    int64
	MinPaymentBp    int64
	MinPaymentFloor int64
	LateFee         int64

	// --- розстрочка ---
	Principal     int64
	PaymentsTotal int64
	// FirstPaymentDate задає й дату, і ЧИСЛО МІСЯЦЯ всього графіка.
	FirstPaymentDate Date
	// FeeMonthBp — комісія за місяць ВІД ПОЧАТКОВОЇ суми, ×100.
	FeeMonthBp int64
	// FeeFreeMonths — скільки перших місяців комісії немає (товарна
	// розстрочка ПУМБ: 0% перші три, далі 3%).
	FeeFreeMonths int64

	OpenedDate Date
	ClosedDate Date
	Place      string
	Note       string
}

// Closed — борг погашено.
func (d Debt) Closed() bool { return d.ClosedDate != "" }

// IsCard — картка з пільговим циклом, а не розстрочка.
func (d Debt) IsCard() bool { return d.Kind == DebtCard }

// DebtOp — один рух під боргом. Amount завжди додатний, напрям задає Kind
// (правило 0025: одна колонка не відповідає на два питання).
type DebtOp struct {
	ID     int64
	DebtID int64
	Date   Date
	Kind   string
	Amount int64
	Note   string
}

// DebtMark — звірка з додатком банку: три числа одного моменту.
type DebtMark struct {
	ID     int64
	DebtID int64
	Date   Date
	// Balance ЗНАКОЗМІННИЙ: плюс — власні гроші на картці, мінус —
	// використаний ліміт.
	Balance int64
	// StatementDue — скільки лишилось внести за виставленою випискою, щоб
	// не платити відсотків зовсім.
	StatementDue int64
	// NonGrace — частина боргу, на яку пільговий не поширюється (готівка,
	// перекази). Вона під відсотком незалежно від дати.
	NonGrace int64
	Note     string
}

// DebtPayment — один обовʼязковий платіж наперед.
type DebtPayment struct {
	Date Date
	// No — номер платежу в графіку, 1..PaymentsTotal. Для картки 0:
	// мінімалка не має номера, вона повторюється, доки є борг.
	No int64
	// Amount = Principal + Fee. Розкладка потрібна, бо це РІЗНІ гроші:
	// тіло повертається до себе, комісія й відсоток ідуть банку, і саме
	// вони є ціною боргу.
	Amount    int64
	Principal int64
	Fee       int64
}

// lastDayOfMonth — скільки днів у місяці. Потрібне рівно для одного:
// розрахункова дата 30 у лютому означає останній день лютого.
func lastDayOfMonth(year int, m time.Month) int {
	return time.Date(year, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// dateOnDay — дата з номером дня, ОБРІЗАНИМ до довжини місяця.
//
// Саме тут Date.AddMonths не годиться: у нього звичайна Go-семантика
// переповнення (31 січня + 1 міс = 3 березня), і для графіка вкладу це
// байдуже, а для розрахункової дати — ні. «30 лютого» мусить стати
// останнім днем лютого, а не зʼїхати на початок березня, інакше пільговий
// поріг раз на рік переносився б на інший місяць.
func dateOnDay(year int, m time.Month, day int) Date {
	if last := lastDayOfMonth(year, m); day > last {
		day = last
	}
	if day < 1 {
		day = 1
	}
	return NewDate(time.Date(year, m, day, 0, 0, 0, 0, time.UTC))
}

// addMonthsOnDay зсуває дату на n місяців, тримаючи число місяця day.
func addMonthsOnDay(d Date, n int, day int) Date {
	t := d.Time().AddDate(0, 0, 0)
	y, m := t.Year(), t.Month()
	total := int(m) - 1 + n
	y += total / 12
	total %= 12
	if total < 0 {
		total += 12
		y--
	}
	return dateOnDay(y, time.Month(total+1), day)
}

// StatementCycle — межі пільгового циклу картки на дату from.
//
// due — найближча розрахункова дата НА АБО ПІСЛЯ from: саме до неї треба
// внести суму виписки, щоб не платити відсотків. closed — попередня, тобто
// день, коли ця виписка сформувалась.
//
// Рівність «сьогодні = розрахункова дата» читається як «платити сьогодні»,
// а не «маємо ще місяць». Це свідомо консервативно: ціна помилки в цей бік
// — зайвий день напруження, у другий — штраф і підвищена ставка на весь
// борг.
//
// day поза 1..31 (тобто «не задано») дає порожні дати: вигадувати цикл
// картці, у якої не спитали розрахункову дату, означало б показати поріг,
// якого банк не виставляв.
func StatementCycle(day int64, from Date) (closed, due Date) {
	if day < 1 || day > 31 || !from.Valid() {
		return "", ""
	}
	d := int(day)
	due = dateOnDay(from.Year(), from.Month(), d)
	if due.Before(from) {
		due = addMonthsOnDay(from, 1, d)
	}
	closed = addMonthsOnDay(due, -1, d)
	return closed, due
}

// InstallmentSchedule — увесь графік розстрочки, від першого платежу до
// останнього.
//
// Тіло ділиться порівну, а решта від ділення йде В ОСТАННІЙ платіж — так
// це роблять банки, і так сума графіка дорівнює тілу до копійки. Рівномірне
// розмазування остачі дало б платежі, що не збігаються з квитанцією, а
// сходяться лише в підсумку.
//
// Комісія рахується від ПОЧАТКОВОЇ суми (а не від залишку) і не береться
// перші FeeFreeMonths місяців. Обидва правила — з договорів, не з моделі:
// саме через перше «1,99% на місяць» коштує близько 43% річних.
func InstallmentSchedule(d Debt) []DebtPayment {
	if d.IsCard() || d.PaymentsTotal <= 0 || d.Principal <= 0 ||
		!d.FirstPaymentDate.Valid() {
		return nil
	}
	n := d.PaymentsTotal
	base := d.Principal / n
	rest := d.Principal - base*n
	fee := int64(0)
	if d.FeeMonthBp > 0 {
		fee = roundDiv(d.Principal*d.FeeMonthBp, 10000)
	}
	day := d.FirstPaymentDate.Day()
	out := make([]DebtPayment, 0, n)
	for k := int64(1); k <= n; k++ {
		p := DebtPayment{
			Date:      addMonthsOnDay(d.FirstPaymentDate, int(k-1), day),
			No:        k,
			Principal: base,
		}
		if k == n {
			p.Principal += rest
		}
		if k > d.FeeFreeMonths {
			p.Fee = fee
		}
		p.Amount = p.Principal + p.Fee
		out = append(out, p)
	}
	return out
}

// roundDiv — ділення з округленням до найближчого (половина вгору).
// Дрібниця, але писана окремо: «комісія 1,99% від 30 000» це 597,00, і
// зрізання вниз щомісяця віддавало б банку копійку задарма в кожному
// рядку графіка.
func roundDiv(a, b int64) int64 {
	if b == 0 {
		return 0
	}
	if a >= 0 {
		return (a + b/2) / b
	}
	return -((-a + b/2) / b)
}

// DebtSchedule — обовʼязкові платежі боргу в проміжку [from, to].
//
// Для розстрочки це вирізка з готового графіка. Для картки — мінімальні
// платежі на непогашений залишок: борг, що переїхав за пільговий, щомісяця
// обростає відсотком, і мінімалка гризе його повільніше, ніж він росте,
// доки залишок великий. Саме тому картка тут моделюється прямо, а не
// «сумою до сплати»: остання відповідає на питання про ЦЕЙ місяць, а
// графік — про те, коли це скінчиться.
//
// balance — борг картки додатним числом (не баланс!). Нуль або менше
// означає, що гасити нема чого, і графіка немає.
func DebtSchedule(d Debt, balance int64, from, to Date) []DebtPayment {
	if d.Closed() || !from.Valid() || !to.Valid() || to.Before(from) {
		return nil
	}
	if !d.IsCard() {
		var out []DebtPayment
		for _, p := range InstallmentSchedule(d) {
			if p.Date.Before(from) || to.Before(p.Date) {
				continue
			}
			out = append(out, p)
		}
		return out
	}
	if balance <= 0 || d.StatementDay < 1 {
		return nil
	}
	// Стеля кроків — щоб мінімалка, менша за місячний відсоток, не
	// крутила цикл вічно. 600 місяців це 50 років: усе, що довше, і так
	// читається як «ніколи», і додаткові рядки цього не уточнять.
	const maxSteps = 600
	monthly := float64(d.APRBp) / 10000 / 12
	_, due := StatementCycle(d.StatementDay, from)
	day := int(d.StatementDay)
	left := balance
	var out []DebtPayment
	for step := 0; step < maxSteps && left > 0; step++ {
		date := addMonthsOnDay(due, step, day)
		if to.Before(date) {
			break
		}
		interest := int64(math.Round(float64(left) * monthly))
		pay := roundDiv(left*d.MinPaymentBp, 10000)
		if pay < d.MinPaymentFloor {
			pay = d.MinPaymentFloor
		}
		if pay > left+interest {
			pay = left + interest
		}
		principal := pay - interest
		if principal < 0 {
			// Мінімалка не покриває навіть відсотка: борг росте. Рядок
			// однаково пишемо — саме він і є те, що треба побачити.
			principal = 0
		}
		out = append(out, DebtPayment{
			Date: date, Amount: pay, Principal: principal, Fee: interest,
		})
		left = left + interest - pay
		if left <= 0 {
			break
		}
	}
	return out
}

// DebtEffectiveRate — річна ставка боргу у відсотках і те, звідки вона.
//
// Розстрочка: XIRR потоків «+тіло на старті, −кожен платіж». Це єдиний
// спосіб порівняти «0% і комісія» з «17% річних» — і єдиний, який не
// бреше на 20 в.п.
//
// Картка: заявлена річна з МІСЯЧНОЮ капіталізацією. Банк називає 47,88%
// річних, маючи на увазі 3,99% на місяць; борг, який лишився на рік,
// обростає до 60,0%, бо відсоток нараховується й на нарахований відсоток.
// Показувати заявлене число там, де гроші поводяться інакше, означало б
// занизити найдорожчий рядок портфеля.
//
// balance потрібен лише картці й лише для того, щоб мовчати на нульовому
// боргу: ставка боргу, якого немає, — не нуль, а відсутність питання.
func DebtEffectiveRate(d Debt, balance int64) (float64, string) {
	if d.Closed() {
		return 0, DebtRateNone
	}
	if d.IsCard() {
		if d.APRBp <= 0 || balance <= 0 {
			return 0, DebtRateNone
		}
		m := float64(d.APRBp) / 10000 / 12
		return (math.Pow(1+m, 12) - 1) * 100, DebtRateCompound
	}
	sched := InstallmentSchedule(d)
	if len(sched) == 0 {
		return 0, DebtRateNone
	}
	// Тіло приходить у день покупки. Дати покупки в розстрочки може не
	// бути записано — тоді це місяць до першого платежу, бо саме так
	// вибудуваний будь-який графік «частинами».
	start := d.OpenedDate
	if !start.Valid() {
		start = addMonthsOnDay(d.FirstPaymentDate, -1, d.FirstPaymentDate.Day())
	}
	flows := make([]Flow, 0, len(sched)+1)
	flows = append(flows, Flow{Date: start, Amount: d.Principal})
	for _, p := range sched {
		flows = append(flows, Flow{Date: p.Date, Amount: -p.Amount})
	}
	rate, err := XIRR(flows)
	if err != nil {
		return 0, DebtRateNone
	}
	return rate * 100, DebtRateFromSchedule
}

// CardStatus — усе, що треба знати про картку СЬОГОДНІ.
//
// Пʼять чисел, а не одне, бо питань до картки пʼять, і жодне з них не
// виводиться з решти.
type CardStatus struct {
	// Known — є хоч одна звірка. Без неї відомі лише умови договору:
	// дата є, порогів немає. Показувати нулі означало б сказати «нічого
	// не винен» там, де насправді «не знаю».
	Known bool
	// MarkDate/MarkAgeDays — коли звіряли востаннє. Вік показується
	// завжди: баланс кредитки живе днями, і місячна звірка — це не число,
	// а спогад (лікуємо ПОКАЗОМ, як price_stale).
	MarkDate    Date
	MarkAgeDays int
	// Balance знакозмінний: остання звірка плюс великі рухи після неї.
	Balance int64
	// Debt — борг додатним числом; нуль, коли баланс невідʼємний.
	Debt int64
	// StatementDue — скільки ще внести до DueDate, щоб не платити
	// відсотків ЗОВСІМ. MinDue — скільки внести, щоб не отримати штраф і
	// підвищену ставку. Два пороги, довід — у шапці файла.
	StatementDue int64
	MinDue       int64
	// NonGrace — готівка й перекази: під відсотком незалежно від дати.
	NonGrace int64
	// DueDate — найближча розрахункова дата. Є завжди, коли задано число
	// місяця: це умова договору, а не вимір.
	DueDate   Date
	DaysToDue int
	// InstallmentDue — частини карткових розстрочок, які спишуться до
	// DueDate. Вони не входять у суму цієї виписки (це покупки наступного
	// періоду), але гроші з картки заберуть — тож у «вільно» входять.
	InstallmentDue int64
	// Free — скільки можна витратити, НЕ потрапивши на відсотки:
	// баланс − сума до сплати − найближчі частини розстрочок.
	//
	// Головне число картки. Відʼємне означає «на картці плюс, але його
	// вже не вистачає»: рівно та пастка, через яку безкоштовний оборот
	// стає боргом під ставку.
	Free int64
	// UsedPct — скільки ліміту використано. Нуль, коли ліміт не заданий:
	// ділити на нуль ніде.
	UsedPct float64
}

// CardState зводить умови картки, останню звірку, рухи після неї й графіки
// привʼязаних розстрочок в один стан на дату today.
//
// РУХИ ПІСЛЯ ЗВІРКИ ЗМІНЮЮТЬ БАЛАНС, А СУМУ ВИПИСКИ — ЛИШЕ ПЛАТЕЖІ.
// Покупка сьогодні потрапить у НАСТУПНУ виписку, а не в ту, що вже
// виставлена, і додавати її до сьогоднішнього порога означало б вимагати
// грошей на місяць раніше. Платіж же гасить саме виставлену суму, тож її
// зменшує.
func CardState(card Debt, marks []DebtMark, ops []DebtOp,
	installments []Debt, today Date) CardStatus {

	var st CardStatus
	if !card.IsCard() {
		return st
	}
	_, st.DueDate = StatementCycle(card.StatementDay, today)
	if st.DueDate != "" {
		st.DaysToDue = DaysBetween(today, st.DueDate)
	}

	// Остання звірка НА АБО ДО сьогодні. Пізніші ігноруються: звірка,
	// датована завтрашнім числом, — одруківка, і рахувати з неї означало б
	// показати майбутнє як теперішнє.
	var last *DebtMark
	for i := range marks {
		m := marks[i]
		if m.DebtID != card.ID || m.Date.After(today) {
			continue
		}
		if last == nil || !m.Date.Before(last.Date) {
			mm := m
			last = &mm
		}
	}
	if last != nil {
		st.Known = true
		st.MarkDate = last.Date
		st.MarkAgeDays = DaysBetween(last.Date, today)
		st.Balance = last.Balance
		st.StatementDue = last.StatementDue
		st.NonGrace = last.NonGrace
	}

	for _, op := range ops {
		if op.DebtID != card.ID || op.Date.After(today) {
			continue
		}
		if last != nil && !op.Date.After(last.Date) {
			// Рух того самого дня, що й звірка, вже В НІЙ: звірка — це
			// виміряний залишок, а не початок відліку. Інакше зарплата,
			// занесена в один день зі звіркою, порахувалась би двічі.
			continue
		}
		switch op.Kind {
		case DebtOpPayment:
			st.Balance += op.Amount
			if st.StatementDue > 0 {
				st.StatementDue -= op.Amount
				if st.StatementDue < 0 {
					st.StatementDue = 0
				}
			}
		case DebtOpDraw, DebtOpCash:
			st.Balance -= op.Amount
			if op.Kind == DebtOpCash {
				// Готівка стає боргом під відсоток одразу, без пільгового.
				st.NonGrace += op.Amount
			}
		}
	}

	if st.Balance < 0 {
		st.Debt = -st.Balance
	}
	if card.LimitAmount > 0 && st.Debt > 0 {
		st.UsedPct = float64(st.Debt) / float64(card.LimitAmount) * 100
	}

	if st.StatementDue > 0 {
		st.MinDue = roundDiv(st.StatementDue*card.MinPaymentBp, 10000)
		if st.MinDue < card.MinPaymentFloor {
			st.MinDue = card.MinPaymentFloor
		}
		if st.MinDue > st.StatementDue {
			// Мінімалка не буває більшою за весь борг: «внеси 100 ₴ при
			// боргу 40 ₴» читалось би як вимога переплатити.
			st.MinDue = st.StatementDue
		}
	}

	for _, in := range installments {
		if in.CardID != card.ID || in.Closed() {
			continue
		}
		for _, p := range DebtSchedule(in, 0, today, st.DueDate) {
			st.InstallmentDue += p.Amount
		}
	}

	st.Free = st.Balance - st.StatementDue - st.InstallmentDue
	return st
}
