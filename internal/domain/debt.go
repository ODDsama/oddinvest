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
	"sort"
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

// Що договір робить із комісією розстрочки при достроковому погашенні.
// Порожнього значення серед констант немає навмисно: «не з'ясовано» — це
// відсутність відповіді, а не третій вид договору, і писати його в код
// довелося б лише щоб порівняти з порожнім рядком.
const (
	// DebtFeeCancel — комісії майбутніх місяців зникають разом із боргом.
	DebtFeeCancel = "cancel"
	// DebtFeeKeep — банк бере комісії за всі місяці, хоч гаси, хоч ні.
	DebtFeeKeep = "keep"
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
	// ExitBy — дата, до якої баланс має вийти в нуль. Порожньо = режиму
	// виходу немає (довід — у міграції 0047).
	ExitBy Date

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
	// FeeOnPrepay — що договір робить із комісією при достроковому
	// погашенні: DebtFeeCancel, DebtFeeKeep або порожньо («не з'ясовано»).
	// Довід, чому станів три й чому порожнє поводиться як keep, — у
	// міграції 0049.
	FeeOnPrepay string

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

// DebtPrepayCancels — чи скасовує дострокове погашення цього боргу хоч
// щось із МАЙБУТНЬОЇ його ціни.
//
// # ЧОМУ ЦЕ ОКРЕМЕ ПИТАННЯ ВІД СТАВКИ
//
// Ставка каже, скільки коштують уже взяті гроші, і 47,88% на розстрочці
// правда незалежно від того, гасиш ти її раніше чи ні. Дострокове
// погашення — питання про МАЙБУТНЄ: чи стане чогось менше, якщо віддати
// гроші сьогодні. Це різні числа, і черга погашення потребує другого.
//
// # ПРАВИЛО
//
// Картка — так завжди: відсоток нараховують на залишок, тож будь-який
// платіж зменшує наступне нарахування.
//
// Розстрочка — лише коли є що скасовувати (комісія більша за нуль) І
// договір її скасовує. Комісія нуль означає, що дострокове погашення не
// економить нічого й у найкращому разі не шкодить: ту саму суму віддано
// раніше. Безвідсоткова розстрочка «частинами» — саме цей випадок, і
// кидати в неї гроші поперед графіка нема жодної причини.
//
// Порожнє FeeOnPrepay поводиться як DebtFeeKeep — довід у міграції 0049.
func DebtPrepayCancels(d Debt) bool {
	if d.Closed() {
		return false
	}
	b := DebtPrepayBasis(d)
	return b == DebtPrepayCard || b == DebtPrepayCancel
}

// Чому дострокове погашення цього боргу має або не має сенсу. Рядком, як
// і основа ставки, і з того самого доводу: «ні» буває чотирьох різних
// ґатунків, і людині треба сказати, якого саме, — «банк бере за всі
// місяці» й «ми не питали в банку» вимагають різних дій.
const (
	// DebtPrepayCard — картка: відсоток нараховують на залишок, тож будь-який
	// платіж зменшує наступне нарахування.
	DebtPrepayCard = "card"
	// DebtPrepayCancel — комісії майбутніх місяців скасовуються.
	DebtPrepayCancel = "cancel"
	// DebtPrepayKeep — банк бере комісії за всі місяці однаково.
	DebtPrepayKeep = "keep"
	// DebtPrepayFree — комісії немає взагалі: скасовувати нічого.
	DebtPrepayFree = "free"
	// DebtPrepayUnknown — договір не звірений. Не «мабуть скасовується»:
	// довід, чому незнання поводиться як відмова, — у міграції 0049.
	DebtPrepayUnknown = "unknown"
)

// DebtPrepayBasis — та сама відповідь словом, для екрана.
func DebtPrepayBasis(d Debt) string {
	if d.IsCard() {
		return DebtPrepayCard
	}
	if d.FeeMonthBp <= 0 {
		return DebtPrepayFree
	}
	switch d.FeeOnPrepay {
	case DebtFeeCancel:
		return DebtPrepayCancel
	case DebtFeeKeep:
		return DebtPrepayKeep
	}
	return DebtPrepayUnknown
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
	// BringByDue — скільки треба ПРИНЕСТИ до DueDate: сума виписки за
	// вирахуванням своїх грошей, які вже лежать на картці.
	//
	// Окремо від StatementDue, бо це різні питання: перше — «скільки
	// винен банку за цей період», друге — «скільки я мушу знайти». На
	// картці з плюсом вони розходяться рівно на цей плюс.
	//
	// І окремо від Free, бо Free рахує ще й частини розстрочок, які
	// спишуться до тієї ж дати, — а вони йдуть у НАСТУПНУ виписку, з
	// іншим строком. Склавши їх в одне число під підписом найближчої
	// дати, застосунок вимагав би грошей на місяць раніше: спіймано
	// вживу, коли «вільно −13 818,70» стояло з підписом «стільки треба
	// принести до 30.09», хоча до 30.09 треба було 5 212.
	BringByDue int64
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
	// Free — скільки СВОЇХ грошей на картці лишається вільним після того,
	// як покриєш виписку й найближчі частини розстрочок:
	// max(0, баланс) − сума до сплати − частини розстрочок.
	//
	// Головне число картки. Відʼємне означає «стільки ще треба принести до
	// дати, інакше почнуть нараховувати» — і воно відʼємне як тоді, коли
	// картка вже в мінусі, так і тоді, коли вона ще в плюсі, але плюса не
	// вистачає. Друге і є та пастка, через яку безкоштовний оборот стає
	// боргом під ставку.
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

	// ВІДʼЄМНИЙ БАЛАНС У «ВІЛЬНО» НЕ ВХОДИТЬ, і це виправлення, знайдене на
	// бойових даних.
	//
	// Було `Balance - StatementDue`, і на живій картці (борг 182 317,
	// виписка 180 260) воно дало −362 577: сума до сплати ПОСІЛА місце
	// двічі, бо відʼємний баланс — це і є той самий борг, а не окрема
	// претензія понад нього. Банк так і рахує: платіж гасить виписку й
	// піднімає баланс однією дією, тож додатний баланс і жива виписка
	// разом не існують.
	//
	// Тому чисельником іде лише те, що на картці Є СВОГО: max(0, баланс).
	// Тоді відʼємне «вільно» читається однозначно — стільки ще треба
	// принести, щоб виписка закрилась і відсотки не почались.
	own := st.Balance
	if own < 0 {
		own = 0
	}
	st.BringByDue = st.StatementDue - own
	if st.BringByDue < 0 {
		st.BringByDue = 0
	}
	st.Free = own - st.StatementDue - st.InstallmentDue
	return st
}

// CardBurn — скільки СПРАВДІ витрачено з картки за проміжок між двома
// звірками.
//
// # ЧОМУ ЦЕ ВИМІРЮЄТЬСЯ, А НЕ БЕРЕТЬСЯ З НАЛАШТУВАНЬ
//
// Заявлені місячні витрати — намір, і на живих даних вони розійшлися з
// реальністю так, що ліміт стояв вибраним до дна при витратах, яких мало б
// вистачати з запасом. Питання «скільки я можу витрачати» безглузде, якщо
// відповідь звіряється з тим самим числом, яке її й породило.
//
// # ТОТОЖНІСТЬ, НА ЯКІЙ УСЕ СТОЇТЬ
//
//	баланс_тепер − баланс_тоді = внесено − витрачено
//
// звідки витрачено = внесено − приріст балансу. Записані покупки й зняття
// в цій формулі СКОРОЧУЮТЬСЯ: вони вже сидять у виміряному балансі, і
// додавати їх окремо означало б порахувати їх двічі. Тому вести журнал
// покупок не треба — досить записувати надходження.
type CardBurn struct {
	// Known — вимір відбувся. Хибне значення завжди супроводжується Why:
	// мовчазний нуль тут читався б як «ти нічого не витрачаєш».
	Known bool
	Why   string
	From  Date
	To    Date
	Days  int
	// SpentUAH — за весь проміжок; PerMonth — те саме, приведене до 30,44
	// дня, бо порівнюють його з місячними числами.
	Spent    int64
	PerMonth int64
}

// CardBurnFrom міряє спалення за двома останніми звірками картки.
func CardBurnFrom(card Debt, marks []DebtMark, ops []DebtOp, today Date) CardBurn {
	if !card.IsCard() {
		return CardBurn{}
	}
	var mine []DebtMark
	for _, m := range marks {
		if m.DebtID == card.ID && !m.Date.After(today) {
			mine = append(mine, m)
		}
	}
	if len(mine) < 2 {
		return CardBurn{Why: "потрібні дві звірки: витрати міряються тим, " +
			"як змінився баланс між ними"}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].Date < mine[j].Date })
	prev, now := mine[len(mine)-2], mine[len(mine)-1]
	days := DaysBetween(prev.Date, now.Date)
	if days <= 0 {
		return CardBurn{Why: "дві останні звірки в один день — міряти нема чого"}
	}

	var paid int64
	for _, op := range ops {
		if op.DebtID != card.ID || op.Kind != DebtOpPayment {
			continue
		}
		// Проміжок ПІВВІДКРИТИЙ: рух того самого дня, що й попередня
		// звірка, уже в ній (та сама межа, що в CardState).
		if !op.Date.After(prev.Date) || op.Date.After(now.Date) {
			continue
		}
		paid += op.Amount
	}

	spent := paid - (now.Balance - prev.Balance)
	if spent < 0 {
		// Баланс зріс дужче, ніж пояснюють записані надходження: гроші
		// прийшли й не записані. Відʼємні витрати — не «заробіток», а
		// прогалина в журналі, і назвати її треба прямо.
		return CardBurn{Why: "баланс зріс більше, ніж записано надходжень — " +
			"запиши зарплату рухом «унесено», інакше витрати не виміряти"}
	}
	return CardBurn{
		Known: true, From: prev.Date, To: now.Date, Days: days,
		Spent:    spent,
		PerMonth: int64(math.Round(float64(spent) / float64(days) * 30.44)),
	}
}

// CardExitInput — усе, що потрібно, щоб відповісти «скільки можна
// витрачати». Гроші — у ГРИВНІ, мінорними: переведення валют робить той,
// хто знає курси (той самий поділ, що в state.GoalInput).
type CardExitInput struct {
	// DebtUAH — скільки треба вивести в нуль.
	DebtUAH int64
	// GrossUAH — увесь дохід місяця; InvestUAH — та його частина, яку
	// зараз виводять в інструменти. На картці лишається різниця.
	GrossUAH  int64
	InvestUAH int64
	// SpendUAH — скільки витрачається за місяць насправді (або заявлено).
	SpendUAH int64
	// InstallmentUAH — щомісячні платежі розстрочок, ПРИВʼЯЗАНИХ до
	// карток. Вони списуються з тієї самої картки, тож воюють із боргом за
	// ті самі гроші, що й витрати, — але це не витрати, і зливати їх в
	// одне число означало б сховати найбільший регулярний відтік.
	InstallmentUAH int64
	// NeedPerMonthUAH — скільки треба звільняти щомісяця, коли це рахує
	// ВИКЛИКАЧ. Потрібне, коли карток кілька: у кожної своя дата, і одне
	// ділення боргу на спільні місяці було б неправдою для обох. Нуль =
	// рахувати самому як DebtUAH / Months.
	NeedPerMonthUAH int64
	ExitBy          Date
	// Today потрібен лише щоб назвати дату виходу за нинішнім темпом.
	Today Date
	// Months — скільки місяців ЩЕ ПОПЕРЕДУ, тобто таких, чиї гроші ще не
	// прийшли. Рахує їх викликач, і це не дрібниця: місяць, який уже
	// закінчився на дату звірки, у прохід не входить — його дохід уже
	// прийшов, уже витрачений, і його результат СИДИТЬ У БАЛАНСІ.
	//
	// Спіймано власником на екрані 31 серпня: прохід брав серпень першим
	// кроком, тобто рахував той самий місяць удвічі — і як уже прожитий (у
	// боргу), і як майбутній (у графіку).
	Months float64
}

// CardExitPlan — відповідь.
type CardExitPlan struct {
	Known  bool
	ExitBy Date
	Months float64
	// NeedPerMonth — скільки треба звільняти щомісяця, щоб устигнути.
	NeedPerMonth int64
	// SpendCap — ГОЛОВНЕ ЧИСЛО: скільки можна витрачати на місяць.
	SpendCap int64
	// Feasible — стеля додатна. Хибне означає «не встигнути навіть при
	// нульових витратах», і це окреме твердження, а не «мало».
	Feasible bool
	// ShortPerMonth — наскільки нинішні витрати перевищують стелю. Нуль,
	// коли вкладаєшся.
	ShortPerMonth int64
	// ETADate — коли вийдеш за НИНІШНІМИ витратами. Порожньо, коли борг не
	// меншає: дати немає, і вигадувати шістсот місяців ні до чого.
	ETADate  Date
	ETAMonth float64
	// WithInvest* — те саме, якщо на картку піде й інвестиційна частка.
	// Другий рядок існує, щоб ціна вибору була числом, а не відчуттям.
	WithInvestSpendCap int64
	WithInvestETADate  Date
}

// CardExit рахує вихід із кредитного ліміту.
//
// # ЧОМУ ЦЕ НЕ ПРОХІД УПЕРЕД, ЯК У ЧЕРЗІ ПОГАШЕННЯ
//
// У ліміту немає графіка. Борг меншає рівно на різницю «прийшло мінус
// витрачено», однакову з місяця в місяць, тож відповідь — ділення, а не
// симуляція. Проходом це виглядало б солідніше й давало б ті самі числа
// довшим шляхом.
func CardExit(in CardExitInput) CardExitPlan {
	out := CardExitPlan{ExitBy: in.ExitBy}
	if in.ExitBy == "" || in.DebtUAH <= 0 || !in.Today.Valid() {
		return out
	}
	if in.Months <= 0 {
		// Місяців попереду не лишилось: або дата минула, або весь її
		// проміжок уже прожито. Питання «чи встигну» більше немає, і
		// вигадувати відповідь на нього нема з чого.
		return out
	}
	out.Known = true
	out.Months = in.Months

	// На картці лишається дохід за вирахуванням того, що явно виводять в
	// інструменти й що заберуть розстрочки: саме ці гроші й воюють із
	// витратами.
	onCard := in.GrossUAH - in.InvestUAH - in.InstallmentUAH

	out.NeedPerMonth = in.NeedPerMonthUAH
	if out.NeedPerMonth <= 0 {
		out.NeedPerMonth = int64(math.Ceil(float64(in.DebtUAH) / out.Months))
	}
	out.SpendCap = onCard - out.NeedPerMonth
	out.Feasible = out.SpendCap > 0
	if s := in.SpendUAH - out.SpendCap; s > 0 {
		out.ShortPerMonth = s
	}
	out.ETADate, out.ETAMonth = cardExitETA(in.DebtUAH, onCard-in.SpendUAH, in.Today)

	out.WithInvestSpendCap = in.GrossUAH - in.InstallmentUAH - out.NeedPerMonth
	out.WithInvestETADate, _ = cardExitETA(in.DebtUAH,
		in.GrossUAH-in.InstallmentUAH-in.SpendUAH, in.Today)
	return out
}

// cardExitETA — коли борг вийде в нуль за місячним профіцитом. Порожня
// дата на невідʼємному профіциті: борг не меншає, і «через шістсот
// місяців» було б числом про стелю розрахунку, а не про гроші.
func cardExitETA(debtUAH, surplus int64, today Date) (Date, float64) {
	if surplus <= 0 || debtUAH <= 0 {
		return "", 0
	}
	months := float64(debtUAH) / float64(surplus)
	return today.AddDays(int(math.Ceil(months * 30.44))), months
}
