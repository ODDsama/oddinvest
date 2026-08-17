package domain

import (
	"math"
	"sort"
	"strconv"
	"strings"

	money "github.com/Rhymond/go-money"
)

// Недержавний пенсійний фонд.
//
// Облік як у сертифіката: одиниці × ціна. Але ціна тут зветься ЧВОПА
// (чиста вартість одиниці пенсійних активів), і від ціни сертифіката вона
// відрізняється двома речами, через які НПФ не вліз у fund_ops:
//
//   - одиниці ДРОБОВІ. 1000 ₴ за ЧВОПА 3.472156 зараховують 287.897…
//     одиниці, а FundOp.Qty — цілі сертифікати. Округлення губить гроші на
//     кожному внеску;
//   - зайва точність не примха: ЧВОПА має шість знаків, і четвертий на
//     десятках тисяч одиниць розходиться на гривні. Тому ×10⁶, тоді як
//     FundPosition.LastPrice живе на ×10⁴.
//
// Економічно ж це взагалі інший звір. До пенсії з нього не приходить
// НІЧОГО — ні купона, ні дивіденда, ні погашення, — а доступ настає лише
// з AccessDate. Це найдовша й абсолютно неліквідна позиція портфеля.
//
// До появи NPFPayoutSchedule нижче НПФ не породжував CashflowItem узагалі,
// і в state_risk.go поруч з IsFundISIN не стояло IsNPFISIN — виключати було
// нема чого. Тепер потік є, і охорони поставлені в трьох місцях:
//
//   - ціновий ризик і ризик перевкладення (state_risk.go): пенсійні активи
//     не переоцінюються ставками ОВДП, а двадцятип'ятирічний потік зруйнував
//     би середній строк перевкладення;
//   - цикл потоків у newSleeveFactory (state_projection.go): там гроші вже
//     віддає Accum за власним графіком, і другий раз тим самим потоком вони
//     б подвоїлись.
//
// Плюс payLabel у state/derive.go, щоб у календарі стояло імʼя фонду, а не
// сирий npf:1.

// NPFISINPrefix — префікс синтетичного ключа НПФ.
const NPFISINPrefix = "npf:"

// IsNPFISIN — чи належить ключ пенсійному фонду.
func IsNPFISIN(isin string) bool { return strings.HasPrefix(isin, NPFISINPrefix) }

// NPFSyntheticISIN — ключ рахунку в спільних вимірах (концентрація,
// підписи). Ім'я, а не id: id — деталь сховища, а на екрані має стояти те,
// що людина вводила.
func NPFSyntheticISIN(name string) string { return NPFISINPrefix + name }

// NPFPlanDest — ключ рахунку в plan_flows.dest. ID, а не імʼя, і це
// НАВМИСНЕ розходження з NPFSyntheticISIN вище.
//
// Два ключі, бо два різні питання. Концентрація й підписи звертаються до
// людини, і «npf:Династія» там єдине, що читається. План — це зв'язок між
// двома рядками бази, і він мусить пережити перейменування рахунку: інакше
// виправлення описки в назві тихо відчепило б внески, і проєкція
// перестала б їх бачити, не сказавши ані слова.
//
// Спільний префікс лишається, тож IsNPFISIN ловить обидва. Це не
// недогляд: обидва означають «це про пенсійний фонд», і місця, де важливо
// саме котрий саме, розбирають ключ явно (ParseNPFPlanDest).
func NPFPlanDest(id int64) string {
	return NPFISINPrefix + strconv.FormatInt(id, 10)
}

// ParseNPFPlanDest — id рахунку з plan_flows.dest. ok=false для порожнього
// призначення (ліквідний портфель) і для всього, що не є "npf:<число>".
func ParseNPFPlanDest(dest string) (int64, bool) {
	if !IsNPFISIN(dest) {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(dest, NPFISINPrefix), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// npfScale — масштаб одиниць і ЧВОПА: обидва ×10⁶.
const npfScale = 1_000_000

// NPFOp — один внесок. UnitsE6 — зараховані одиниці ×10⁶, Amount —
// сплачено в мінорних одиницях (копійках). Обидва завжди додатні: іншого
// напрямку в цього журналу немає.
//
// Ціни за одиницю тут немає навмисно — вона завжди виводиться з пари
// (Amount, UnitsE6), і окреме поле лише дало б їм розійтись. Те саме
// правило, що в fund_ops.
type NPFOp struct {
	ID     int64
	NPFID  int64
	Date   Date
	Units  int64 // ×10⁶
	Amount int64 // мінорні
	Broker string
	Note   string
}

// NavE6 — ЧВОПА, за якою пройшов цей внесок, ×10⁶.
//
// Це і є причина, чому НПФ не потребує окремого джерела котирувань: кожен
// внесок приносить ціну на свою дату з собою, як виписка фонду приносить
// ціну сертифіката.
//
//	Amount(коп) = Units × Nav / 10^10  ⇒  Nav = Amount × 10^10 / Units
func (o NPFOp) NavE6() int64 {
	if o.Units <= 0 {
		return 0
	}
	return o.Amount * 10_000_000_000 / o.Units
}

// NPFNav — одна точка ЧВОПА, не привʼязана до моїх операцій.
type NPFNav struct {
	ID    int64
	NPFID int64
	Date  Date
	Nav   int64 // ×10⁶
}

// NPFAccount — властивості рахунку, які потрібні моделі. Дзеркалить рядок
// довідника, але живе в domain, щоб оцінка позиції не тягнула сховище.
type NPFAccount struct {
	ID            int64
	Name          string
	Administrator string
	Currency      string
	// Nav / NavDate — остання ВІДОМА ЧВОПА. Може бути свіжішою за останній
	// внесок: її оновлюють руками з кабінету.
	Nav     int64 // ×10⁶
	NavDate Date
	// ExpectedYieldBP — обіцяна дохідність × 100, 0 = не задана.
	// YieldSimpleYears — ненульове означає, що обіцянка задана ПРОСТОЮ
	// середньорічною за стільки років (див. CompoundFromSimple).
	ExpectedYieldBP  int64
	YieldSimpleYears int64
	// AccessDate — коли виплати стають законними (50 років). Порожньо =
	// невідомо, і тоді модель не малює ні закриття, ні розблокування.
	AccessDate Date
	// IncomeTaxBP — податок на виплату × 100. За п. 170.8.2 ПКУ 40% виплати
	// на визначений строк звільнено, тобто оподатковується 60%: ПДФО 18% +
	// ВЗ 5% на цю частину дають ефективні 13.8% = 1380.
	IncomeTaxBP int64
	// CreditRateBP — ставка ПДФО-знижки на внески × 100 (18% = 1800).
	CreditRateBP int64
	// ContribDay — число місяця, коли планую вносити (0 = не нагадувати).
	ContribDay int64
	// PayoutYears / PayoutFreq — як фонд віддає гроші після AccessDate:
	// на скільки років розтягнута виплата й з якою періодичністю
	// (month | quarter | year). 0 років = разово.
	//
	// За законом виплата на визначений строк іде мінімум десять років, а
	// одноразово можна забрати лише суму нижче межі. Разова модель
	// вивалювала весь капітал у готівку одного місяця й далі
	// реінвестувала його ринковою ставкою — тобто малювала гроші, яких
	// не буде.
	PayoutYears int64
	PayoutFreq  string
	Note        string
}

// NPFPosition — стан одного рахунку, зведений із журналу.
type NPFPosition struct {
	NPFID    int64
	Name     string
	Currency string
	// Units — залишок одиниць ×10⁶; Nav — ЧВОПА ×10⁶, за якою оцінено.
	Units   int64
	Nav     int64
	NavDate Date
	// Cost — сума всіх внесків у мінорних. Продажу немає, тож собівартість
	// лише росте: це просто «скільки я вніс».
	Cost int64
	// LastOp — дата останнього внеску. Порожньо = внесків ще немає.
	LastOp Date
}

// Value — вартість позиції в мінорних одиницях.
//
//	Amount(коп) = Units × Nav / 10^10
func (p NPFPosition) Value() int64 {
	if p.Units <= 0 || p.Nav <= 0 {
		return 0
	}
	return p.Units * p.Nav / 10_000_000_000
}

// Gain — приріст у мінорних: вартість мінус внесене.
func (p NPFPosition) Gain() int64 { return p.Value() - p.Cost }

// UnitsMajor — одиниці як звичайне число, для показу.
func (p NPFPosition) UnitsMajor() float64 { return float64(p.Units) / npfScale }

// NavMajor — ЧВОПА як звичайне число, для показу.
func (p NPFPosition) NavMajor() float64 { return float64(p.Nav) / npfScale }

// NPFPositions зводить журнал у позиції.
//
// ЧВОПА береться найсвіжіша з двох джерел — довідника й самих операцій.
// Не «завжди з довідника»: його оновлюють руками, і забутий на пів року
// довідник оцінював би позицію за ціною, давнішою за останній внесок,
// тобто показував би менше, ніж уже відомо. І не «завжди з операцій»: тоді
// оновлення з кабінету між внесками не мало б жодного ефекту.
func NPFPositions(accounts []NPFAccount, ops []NPFOp) map[int64]*NPFPosition {
	out := map[int64]*NPFPosition{}
	for _, a := range accounts {
		cur := a.Currency
		if cur == "" {
			cur = "UAH"
		}
		out[a.ID] = &NPFPosition{
			NPFID: a.ID, Name: a.Name, Currency: cur,
			Nav: a.Nav, NavDate: a.NavDate,
		}
	}
	sorted := make([]NPFOp, len(ops))
	copy(sorted, ops)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Date < sorted[j].Date })
	for _, op := range sorted {
		p := out[op.NPFID]
		if p == nil {
			// Внесок на рахунок, якого немає в довіднику. Мовчки викидати
			// його означало б занизити капітал і не сказати про це; але й
			// вигадати рахунок тут нема з чого — FK у схемі це й не пускає.
			continue
		}
		p.Units += op.Units
		p.Cost += op.Amount
		p.LastOp = op.Date
		if nav := op.NavE6(); nav > 0 && op.Date >= p.NavDate {
			p.Nav, p.NavDate = nav, op.Date
		}
	}
	return out
}

// NPFFlows — потоки одного рахунку для XIRR: внески відʼємні, поточна
// вартість термінальна.
//
// Це «скільки заробили МОЇ гроші» — грошово-зважена дохідність, що
// враховує дати внесків. Поруч живе NPFNavReturn, і це ІНШЕ число: воно
// про те, як спрацював фонд, незалежно від того, коли я вносив. При
// нерівномірних внесках вони розходяться, і сильно; зводити їх в одне
// означало б відповідати на друге питання числом від першого.
func NPFFlows(p NPFPosition, ops []NPFOp, asOf Date) []Flow {
	var flows []Flow
	for _, op := range ops {
		if op.NPFID != p.NPFID || op.Date.After(asOf) {
			continue
		}
		flows = append(flows, Flow{Date: op.Date, Amount: -op.Amount})
	}
	if len(flows) == 0 {
		return nil
	}
	if v := p.Value(); v > 0 {
		flows = append(flows, Flow{Date: asOf, Amount: v})
	}
	sort.Slice(flows, func(i, j int) bool { return flows[i].Date < flows[j].Date })
	return flows
}

// npfNavReturnMinDays — коротший відрізок ануалізувати не варто: два дні
// різниці ЧВОПА, розтягнуті на рік, дають тризначні відсотки. Та сама
// причина, що в fundReturnMinDays.
const npfNavReturnMinDays = 180

// NPFNavReturn — зростання ЧВОПА, % річних складних: дохідність САМОГО
// ФОНДУ за наявними точками.
//
// Time-weighted, тобто дати й розміри моїх внесків на неї не впливають —
// це те саме число, яке публікує фонд. Друге значення — чи є що показувати:
// менше двох точок або надто короткий відрізок означає «ще нема», і тоді
// показувати треба обіцянку, сказавши про це вголос (yield_basis). Те саме
// правило витіснення, що в state_funds.go для FundTotalReturn.
func NPFNavReturn(points []NPFNav, asOf Date) (float64, bool) {
	var pts []NPFNav
	for _, p := range points {
		if p.Nav > 0 && !p.Date.After(asOf) {
			pts = append(pts, p)
		}
	}
	if len(pts) < 2 {
		return 0, false
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].Date < pts[j].Date })
	first, last := pts[0], pts[len(pts)-1]
	days := DaysBetween(first.Date, last.Date)
	if days < npfNavReturnMinDays {
		return 0, false
	}
	growth := float64(last.Nav) / float64(first.Nav)
	if growth <= 0 {
		return 0, false
	}
	r := (math.Pow(growth, 365.0/float64(days)) - 1) * 100
	return math.Round(r*100) / 100, true
}

// NPFNavPoints — усі відомі точки ЧВОПА рахунку: заведені руками разом із
// виведеними з внесків, по одній на дату.
//
// Обʼєднання, а не вибір одного джерела: внески дають точки задарма, але
// їх щонайбільше дванадцять на рік і жодної до першого внеску, а руками
// заводиться опублікована фондом історія — саме вона й робить видимим
// track record, на якому тримається обіцянка. Заведене руками важить
// більше за виведене: воно точне, а виведене несе округлення суми.
func NPFNavPoints(manual []NPFNav, ops []NPFOp, npfID int64) []NPFNav {
	byDate := map[Date]NPFNav{}
	for _, op := range ops {
		if op.NPFID != npfID {
			continue
		}
		if nav := op.NavE6(); nav > 0 {
			byDate[op.Date] = NPFNav{NPFID: npfID, Date: op.Date, Nav: nav}
		}
	}
	for _, m := range manual {
		if m.NPFID == npfID && m.Nav > 0 {
			byDate[m.Date] = m
		}
	}
	out := make([]NPFNav, 0, len(byDate))
	for _, v := range byDate {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// NPFOwnRatePct — ВЛАСНА складна річна ставка рахунку: виміряне зростання
// ЧВОПА, якщо є чим міряти, інакше обіцянка. Другим значенням — основа,
// щоб екран назвав, що саме показано.
//
// Порядок саме такий, і він той самий, що у фондів: факт витісняє
// обіцянку. Обіцянка лишається лише доти, доки міряти нема по чому.
func NPFOwnRatePct(a NPFAccount, navPoints []NPFNav, asOf Date) (float64, string) {
	if r, ok := NPFNavReturn(navPoints, asOf); ok {
		return r, "зростання ЧВОПА"
	}
	if a.ExpectedYieldBP > 0 {
		return CompoundFromSimple(float64(a.ExpectedYieldBP)/100, int(a.YieldSimpleYears)),
			"обіцяно фондом"
	}
	return 0, ""
}

// NPFAccessMonths — скільки місяців до дати доступу. 0 = дата не задана або
// вже настала, і тоді проєкція не малює закриття.
func NPFAccessMonths(a NPFAccount, today Date) int {
	if a.AccessDate == "" {
		return 0
	}
	m := MonthsBetween(today, a.AccessDate)
	if m <= 0 {
		return 0
	}
	return m
}

// NPFContribDue — чи прострочений внесок цього місяця.
//
// Самогасне за побудовою: щойно в журналі зʼявиться внесок за поточний
// календарний місяць, умова стає хибною. Тому нагадування не смикає двічі
// й не потребує окремого «я вже бачив» — стану, який довелось би десь
// тримати й якось скидати першого числа.
//
// ContribDay == 0 означає «не нагадувати»: рахунок може бути й таким, куди
// вносять нерегулярно, і вигадувати йому ритм застосунок не має права.
func NPFContribDue(a NPFAccount, ops []NPFOp, today Date) bool {
	if a.ContribDay <= 0 {
		return false
	}
	if int64(today.Day()) < a.ContribDay {
		return false
	}
	y, m := today.Year(), today.Month()
	for _, op := range ops {
		if op.NPFID != a.ID {
			continue
		}
		if op.Date.Year() == y && op.Date.Month() == m {
			return false
		}
	}
	return true
}

// NPFCreditEstimate — ОЦІНКА податкової знижки на внески за рік, у
// мінорних одиницях.
//
// Саме оцінка, і саме тому вона нікуди не входить — ні в календар, ні в
// проєкцію, ні в by_kind податкового звіту. Три передумови застосунок
// знати не може: чи подано декларацію, чи вистачило утриманого з зарплати
// ПДФО, і який ліміт цього року (він щороку інший — прожитковий мінімум
// працездатних на 1 січня × 1.4, округлено до 10 ₴). Вивести з цього
// приплив автоматично означало б те саме, що малювати ціну паперу з рівня
// аукціону: точно на вигляд, з джерелом, і хибно там, де на це дивляться.
//
// Тому число показується поруч із власною арифметикою й підписом «не
// входить у жоден прогноз», а перемикач стоїть вимкненим за замовчуванням.
//
// capMonth — ліміт внеску за кожен місяць (мінорні, 0 = без ліміту).
// pdfoYear — фактично утриманий за рік ПДФО (мінорні, 0 = стелю не
// застосовувати). Знижка не може перевищити його: держава повертає
// сплачене, а не дарує.
func NPFCreditEstimate(a NPFAccount, ops []NPFOp, year int, capMonth, pdfoYear int64) int64 {
	if a.CreditRateBP <= 0 {
		return 0
	}
	// Ліміт місячний, тож і внески групуються по місяцях: 60 000 ₴ одним
	// платежем у січні дають знижку лише з ліміту одного місяця, а не з
	// усієї суми. Рахувати від річної суми означало б завищити її для
	// нерівномірних внесків — а вони тут звичайна річ.
	byMonth := map[int]int64{}
	for _, op := range ops {
		if op.NPFID != a.ID {
			continue
		}
		if op.Date.Year() != year {
			continue
		}
		byMonth[int(op.Date.Month())] += op.Amount
	}
	var base int64
	for _, sum := range byMonth {
		if capMonth > 0 && sum > capMonth {
			sum = capMonth
		}
		base += sum
	}
	credit := base * a.CreditRateBP / 10000
	if pdfoYear > 0 && credit > pdfoYear {
		credit = pdfoYear
	}
	return credit
}

// NPFPayoutMonths — на скільки місяців розтягнута виплата й з яким кроком.
// months = 0 означає «разово» (строк не заданий).
func NPFPayoutMonths(a NPFAccount) (months, step int) {
	if a.PayoutYears <= 0 {
		return 0, 0
	}
	switch a.PayoutFreq {
	case "quarter":
		step = 3
	case "year":
		step = 12
	default:
		step = 1
	}
	return int(a.PayoutYears) * 12, step
}

// NPFPayoutSchedule — графік виплат на пенсії, КАЛЕНДАРНИМИ подіями.
//
// totalNet — уся сума, яку рахунок віддасть, у мінорних одиницях і вже
// ПІСЛЯ податку. Передається готовою (AccumCloseValue), а не рахується тут,
// і це головне рішення функції: інакше календар і проєкція мали б по
// власному означенню «скільки буде на пенсії», а такі пари в цьому
// застосунку розходяться завжди.
//
// Це ОЦІНКА, не зобовʼязання — та сама категорія, що оцінені дивіденди
// фондів, і з тим самим наслідком: ключ npf:<id> лишається впізнаваним, і
// там, де оцінці не місце, вона відсіюється. Місць три, і кожне має власну
// причину:
//
//   - ціновий ризик: пенсійні активи не переоцінюються ставками ОВДП;
//   - ризик перевкладення: двадцятип'ятирічний потік зруйнував би середній
//     строк, а перевкладати виплату на пенсії ніхто не збирається;
//   - рукав проєкції: там гроші вже віддає Accum за власним графіком, і
//     другий раз тим самим потоком вони б подвоїлись.
//
// until обрізає горизонт: викликач вирішує, наскільки далеко зазирати —
// календарю досить року, профілю плану треба до дедлайну.
func NPFPayoutSchedule(a NPFAccount, totalNet int64, until Date) []CashflowItem {
	months, step := NPFPayoutMonths(a)
	if a.AccessDate == "" || totalNet <= 0 {
		return nil
	}
	cur := a.Currency
	if cur == "" {
		cur = "UAH"
	}
	key := NPFPlanDest(a.ID)
	if months == 0 {
		// Разова виплата — одна подія на дату доступу. Тип «погашення», бо
		// це і є повернення тіла: дохід усередині вже оподаткований.
		if a.AccessDate > until {
			return nil
		}
		return []CashflowItem{{
			Date: a.AccessDate, ISIN: key, Type: PayRedemption,
			Amount: money.New(totalNet, cur),
		}}
	}
	n := months / step
	if n <= 0 {
		return nil
	}
	// Ділення цілочисельне, а залишок від нього дописується в ОСТАННЮ
	// виплату: інакше Σ подій календаря розійшлась би з totalNet на копійки,
	// і звірка «подія на осі проти суми графіка» падала б без причини.
	per := totalNet / int64(n)
	var out []CashflowItem
	for i := 0; i < n; i++ {
		d := a.AccessDate.AddMonths(i * step)
		if d > until {
			break
		}
		amt := per
		if i == n-1 {
			amt = totalNet - per*int64(n-1)
		}
		out = append(out, CashflowItem{
			Date: d, ISIN: key, Type: PayCoupon,
			Amount: money.New(amt, cur),
		})
	}
	return out
}
