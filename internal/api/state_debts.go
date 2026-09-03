// Борг у документі стану: одне рішення, від якого залежать чужі числа,
// і стан кожної картки на сьогодні.
//
// ЩО В ДОКУМЕНТІ, А ЩО REST. Перелік боргів і план погашення — REST
// (/api/debts, /api/payoff): план погашення — проєкція й залежить від
// питання, той самий поділ, що для /api/progress і /api/decisions. Стан
// картки (DebtPlan.Cards) — у ДОКУМЕНТІ, і це перевертає рішення фази 21
// («стан картки живе годинами й старіє між звірками»). Довід тодішній
// був про те, щоб не робити документ ДЖЕРЕЛОМ балансу. Довід теперішній:
//
//   - розрахункова дата й днів до неї — умова договору (statement_day),
//     а не вимір, і вона однаково точна за будь-якої давнини звірки;
//   - «скільки принести до дати» вже стояло в документі ПРОЗОЮ — задача
//     card-due-* несе його в заголовку й у Why. Числа лише повторюють те,
//     що документ уже казав словами, і документ републікується на кожну
//     мутацію, тож свіжішими вони не стануть, але й старішими теж;
//   - читач тепер є: Home Assistant не має іншого джерела, крім MQTT, а
//     сповіщення «до розрахункової дати три дні, принести 15 400» — одне з
//     найцінніших у застосунку, і без цих полів воно неможливе.
//
// Вік звірки їде поруч (MarkAgeDays) — лікуємо ПОКАЗОМ, як price_stale:
// сенсор, що показує тиждень тому звірене число, каже й те, що йому
// тиждень.
package api

import (
	"math"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
)

// debtCapsReserve — чи живий зараз борг, який коштує РЕАЛЬНИХ грошей.
//
// Від нього залежить стеля подушки на час боргу (reserve_debt_months).
//
// # ЧОМУ ПОРІГ — ЗНЕЦІНЕННЯ, А НЕ ДОХІДНІСТЬ ПОРТФЕЛЯ
//
// План цієї фази обіцяв порівнювати зі зведеною реальною дохідністю. Від
// цього довелось відмовитись, і на те дві причини, обидві не про зручність.
//
// Технічна: стеля подушки потрібна в reserveMonthShare, а та рахується за
// шістсот рядків ДО того, як зведена дохідність існує. Порахувати її
// раніше означало б завести друге означення головного числа портфеля —
// рівно те, проти чого стоїть увесь цей пакет.
//
// Суттєва, і вона важливіша: гроші подушки в портфель не йдуть узагалі.
// Вони лежать готівкою або в резервному вкладі, тож питання «тримати
// подушку чи гасити борг» порівнює борг НЕ з портфелем. Правильний поріг —
// той, за яким борг перестає бути безкоштовним: знецінення. Реальна
// ставка вище нуля означає, що борг зʼїдає більше, ніж зʼїдає гривня.
//
// Наслідок приємний і не задуманий: безвідсоткова розстрочка «частинами»
// сюди не потрапляє сама собою — її реальна ставка відʼємна.
func debtCapsReserve(debts []domain.Debt, marks []domain.DebtMark,
	ops []domain.DebtOp, devalPct float64, today domain.Date) bool {

	for _, d := range debts {
		if d.Closed() {
			continue
		}
		// БОРГ, ЯКИЙ НЕ МОЖНА ПОГАСИТИ ДОСТРОКОВО, СТЕЛІ НЕ ВМИКАЄ.
		//
		// Уся стеля стоїть на думці «гроші зараз корисніші в борзі, ніж під
		// матрацом». Там, де банк бере комісії за весь строк однаково, ця
		// думка хибна: у борг їх подіти нікуди, і обрізана подушка означала
		// б менше грошей на руках при тому самому борзі — найгірше з обох
		// світів. Такий борг діє в протилежний бік, підлогою цілі
		// (ReserveTarget), і два правила не сперечаються саме тому, що
		// говорять про різні борги.
		if !domain.DebtPrepayCancels(d) {
			continue
		}
		// РЕЖИМ ВИХОДУ ВМИКАЄ СТЕЛЮ САМ, не питаючи про ставку.
		//
		// Інакше найбільший борг власника її не вмикав би взагалі: борг у
		// пільговому періоді коштує нуль, реальна ставка відʼємна, і за
		// порогом нижче він проходить як безкоштовний. Але людина, яка
		// назвала дату виходу, сказала цим, що гроші потрібні ЗАРАЗ, — а не
		// тоді, коли банк почне нараховувати.
		if d.IsCard() && d.ExitBy != "" && d.ExitBy.After(today) {
			return true
		}
		balance := int64(0)
		if d.IsCard() {
			st := domain.CardState(d, marks, ops, nil, today)
			balance = st.NonGrace
			if balance <= 0 {
				continue
			}
		}
		rate, basis := domain.DebtEffectiveRate(d, balance)
		if basis == domain.DebtRateNone {
			continue
		}
		// Ставка приходить відсотками, realYield чекає частку. Помилка
		// масштабу тут не косметична: без ділення умова була б істинною
		// майже завжди, і стеля подушки вмикалась би від безкоштовної
		// розстрочки.
		if realYield(rate/100, d.Currency, devalPct) > 0 {
			return true
		}
	}
	return false
}

// debtCoverUAH — скільки боргу подушка мусить перекривати, грн-екв.
//
// # ЧОМУ САМЕ МАЙБУТНІ ПЛАТЕЖІ, А НЕ «ЗАЛИШОК ТІЛА»
//
// Питання, на яке відповідає це число, одне: чи є чим закрити кредити,
// якщо дохід зникне. Візьмуть із власника не тіло, а ПЛАТЕЖІ — тіло разом
// із комісіями, — і саме на розстрочці, комісії якої не скасовуються,
// різниця між цими двома числами і є вся ціна помилки.
//
// # ЩО СЮДИ НЕ ВХОДИТЬ
//
// Оборот у межах пільгового періоду. Межа 0045 лишається на місці: він
// уже описаний витратами, а подушка й так рахується в місяцях витрат, тож
// друге його врахування наклало б побут на побут.
func debtCoverUAH(debts []domain.Debt, marks []domain.DebtMark,
	ops []domain.DebtOp, rates fx.Rates, today domain.Date) float64 {

	minor := int64(0)
	for _, d := range debts {
		if d.Closed() {
			continue
		}
		add := func(v int64) {
			if v <= 0 {
				return
			}
			if u, err := fx.ToUAH(money.New(v, d.Currency), rates); err == nil {
				minor += u.Amount()
			}
		}
		if d.IsCard() {
			add(payoffCardDebt(d, marks, ops, today))
			continue
		}
		for _, p := range domain.InstallmentSchedule(d) {
			if p.Date.Before(today) {
				continue
			}
			add(p.Amount)
		}
	}
	return round2(float64(minor) / 100)
}

// buildDebtPlan зводить борги в те, що змінює чужі числа.
//
// СКІЛЬКИ БОРГУ — це не «скільки я винен», а «скільки з того під
// ставкою». Пільговий оборот картки не рахується (шапка файла й міграція
// 0045), тож на здоровому місяці TotalUAH дорівнює нулю навіть за живої
// картки. Щоб це не читалось як «боргів немає», поруч іде CardsWatched.
//
// СКІЛЬКИ ВІДДАНО ПОНАД ОБОВʼЯЗКОВЕ рахується ЛИШЕ по розстрочках, і це
// не спрощення, а межа знання. Платіж на картку — одна операція, у якій
// нерозрізнимо змішані повернення виписки (побут) і погашення непільгової
// частини (борг); розділити їх можна було б лише вигаданим правилом, а
// вигадане правило в головному числі гірше за чесно вужчу основу.
func buildDebtPlan(src *sources, debts []domain.Debt, marks []domain.DebtMark,
	ops []domain.DebtOp, set *state.SettingsDoc, mp *state.MonthPlan, rates fx.Rates,
	now time.Time, today domain.Date) *state.DebtPlan {

	if len(debts) == 0 {
		return nil
	}
	out := &state.DebtPlan{}
	inst := map[int64]bool{}
	for _, d := range debts {
		if d.Closed() {
			continue
		}
		balance := int64(0)
		if d.IsCard() {
			out.CardsWatched++
			st := domain.CardState(d, marks, ops, nil, today)
			balance = st.NonGrace
			if st.Debt > 0 && balance > st.Debt {
				balance = st.Debt
			}
			if balance <= 0 {
				continue
			}
		} else {
			inst[d.ID] = true
			for _, p := range domain.InstallmentSchedule(d) {
				if !p.Date.Before(today) {
					balance += p.Principal
				}
			}
			if balance <= 0 {
				continue
			}
		}
		if u, err := fx.ToUAH(money.New(balance, d.Currency), rates); err == nil {
			out.TotalUAH += float64(u.Amount()) / 100
		}
		if rate, basis := domain.DebtEffectiveRate(d, balance); basis != domain.DebtRateNone &&
			rate > out.TopRatePct {
			out.TopRatePct, out.TopName = round2(rate), d.Name
		}
	}
	out.TotalUAH = round2(out.TotalUAH)
	if mp != nil {
		out.DueThisMonthUAH = mp.DebtDueUAH
	}

	// Сплачене понад обовʼязкове — по розстрочках цього місяця, проти
	// їхнього ж обовʼязкового. Саме їхнього, а не всього DueThisMonthUAH:
	// туди входить іще й мінімалка картки, і віднімати її від платежів за
	// розстрочками означало б порахувати гроші не з того кошика.
	instDue := 0.0
	first := monthStart(today, 0)
	lastDay := monthStart(today, 1).AddDays(-1)
	for _, d := range debts {
		if d.Closed() || d.IsCard() {
			continue
		}
		for _, p := range domain.DebtSchedule(d, 0, first, lastDay) {
			if u, err := fx.ToUAH(money.New(p.Amount, d.Currency), rates); err == nil {
				instDue += float64(u.Amount()) / 100
			}
		}
	}
	for _, op := range ops {
		if op.Kind != domain.DebtOpPayment || !inst[op.DebtID] ||
			op.Date.Year() != now.Year() || int(op.Date.Month()) != int(now.Month()) {
			continue
		}
		if u, err := fx.ToUAH(money.New(op.Amount, debtCurrency(debts, op.DebtID)), rates); err == nil {
			out.PaidExtraUAH += float64(u.Amount()) / 100
		}
	}
	out.PaidExtraUAH = round2(math.Max(0, out.PaidExtraUAH-instDue))

	// Стеля дострокового — від ДОЗВОЛЕНОЇ частини плану, як у подушки й
	// цілей, і обрізана самим боргом: платити більше, ніж винен, ніде.
	if set != nil && set.DebtFillSharePct != nil && mp != nil && out.TotalUAH > 0 {
		if share := *set.DebtFillSharePct; share > 0 && mp.PlanDebtUAH > 0 {
			month := math.Min(mp.PlanDebtUAH*share/100, out.TotalUAH)
			out.FillMonthUAH = round2(month)
			out.FillNowUAH = round2(math.Max(0, month-out.PaidExtraUAH))
		}
	}
	out.Exit = buildDebtExit(debts, marks, ops, set, src, rates, today)
	out.Cards = buildDebtCards(debts, marks, ops, rates, today)
	if out.TotalUAH == 0 && out.CardsWatched == 0 && out.Exit == nil {
		return nil
	}
	return out
}

// buildDebtCards — стан кожної відкритої картки на сьогодні (довід — у
// шапці файла). Порядок — як у довіднику, щоб сенсор не стрибав.
//
// Гривня курсом СЬОГОДНІ через fx.ToUAH, як і TotalUAH поруч, а не через
// cardAmountUAH із задач: там нуль для валютної картки виправданий тим,
// що сума вже названа в заголовку, тут заголовка немає. Валютна картка в
// житті власника поки одна — гривнева, тож і різниці немає.
func buildDebtCards(debts []domain.Debt, marks []domain.DebtMark, ops []domain.DebtOp,
	rates fx.Rates, today domain.Date) []state.DebtCard {

	var out []state.DebtCard
	for _, d := range debts {
		if !d.IsCard() || d.Closed() {
			continue
		}
		// Розстрочки передаються всі — CardState сам відбере привʼязані
		// до цієї картки; без них FreeUAH не бачив би найближчих частин.
		st := domain.CardState(d, marks, ops, debts, today)
		out = append(out, state.DebtCard{
			Name:          d.Name,
			Known:         st.Known,
			MarkDate:      string(st.MarkDate),
			MarkAgeDays:   st.MarkAgeDays,
			DueDate:       string(st.DueDate),
			DaysToDue:     st.DaysToDue,
			BringByDueUAH: minorUAH(st.BringByDue, d.Currency, rates),
			MinDueUAH:     minorUAH(st.MinDue, d.Currency, rates),
			FreeUAH:       minorUAH(st.Free, d.Currency, rates),
			DebtUAH:       minorUAH(st.Debt, d.Currency, rates),
			UsedPct:       round2(st.UsedPct),
			ExitBy:        string(d.ExitBy),
		})
	}
	return out
}

// minorUAH — мінорні одиниці валюти в гривні сьогоднішнім курсом, зі
// знаком. Нуль, коли курсу немає: fx.ToUAH відмовляє, і ставити тут
// щось інше означало б вигадати число.
func minorUAH(minor int64, cur string, rates fx.Rates) float64 {
	if minor == 0 {
		return 0
	}
	sign := int64(1)
	if minor < 0 {
		sign, minor = -1, -minor
	}
	u, err := fx.ToUAH(money.New(minor, cur), rates)
	if err != nil {
		return 0
	}
	return round2(float64(sign*u.Amount()) / 100)
}

// buildDebtExit — вихід із кредитних лімітів: спільний план на ВСІ картки
// з названою датою.
//
// # ЧОМУ ОДИН ПЛАН, А НЕ ПО ОДНОМУ НА КАРТКУ
//
// Гроші одні. Два окремі плани кожен вважав би, що весь залишок доходу
// його, і обидва вийшли б надто оптимістичними — а разом вони ще й
// суперечили б один одному на тому самому екрані.
//
// Доти бралась ОДНА картка, з найближчою датою, і на бойових даних 30.10 у
// mono (борг 5 212) витіснило 31.10 у ПУМБ (борг 182 317): застосунок
// планував вихід із меншого боргу, а більший зник з екрана зовсім.
//
// ПОТРЕБА РАХУЄТЬСЯ ПО КАРТКАХ І СУМУЄТЬСЯ. У кожної своя дата, тож
// ближча тисне сильніше: той самий борг, поділений на менше місяців, дає
// більший доданок. Одне ділення спільного боргу на спільні місяці було б
// неправдою для обох.
//
// # СЕРЕДНІЙ ДОХІД, А НЕ ДОХІД ПОТОЧНОГО МІСЯЦЯ
//
// Спіймано на бойових даних: у серпні одна зарплата власника скінчилась, а
// три ще не почались, і «скільки приходить» вийшло 128 911 ₴ замість
// 222 800. Стеля витрат, порахована з такого місяця, була б удвічі
// суворішою за правду — і людина повірила б їй, бо число виглядає точним.
//
// Тому дохід усереднюється по місяцях ДО ЦІЛІ: саме той період, про який
// і питають. Місяці розгортає той самий buildMonthPlan, тож другого
// означення «скільки цей потік платить у листопаді» не зʼявляється.
func buildDebtExit(debts []domain.Debt, marks []domain.DebtMark, ops []domain.DebtOp,
	set *state.SettingsDoc, src *sources, rates fx.Rates, today domain.Date) *state.DebtExit {

	// ВІКНО ПОЧИНАЄТЬСЯ ЗІ ЗВІРКИ — за найсвіжішою серед цілей.
	//
	// Борг у звірці — це вже РЕЗУЛЬТАТ того, що сталось до неї: дохід із
	// платіжним днем на її дату або раніше прийшов, витрати сталися, залишок
	// осів у балансі. Рахувати ці гроші ще раз означало б обіцяти їх двічі
	// (спіймано власником 31 серпня).
	//
	// Але прожитим є не МІСЯЦЬ звірки, а те, що заплачено НА ЇЇ ДАТУ.
	// Спіймано власником 1 вересня: звірка того ж дня викидала весь вересень,
	// хоч аванс 7-го й дві зарплати 15-го та 21-го були ще попереду — вікно
	// звужувалось до одного жовтня, а «за твоїм темпом» поруч рахувало
	// вересень і казало 21 вересня. Коли після звірки в її місяці не платить
	// ніщо, він прожитий цілком, як і доти.
	//
	// # ВІДЛІК — ВІД ПОЧАТКУ МІСЯЦЯ ЗВІРКИ, А НЕ ВІД САМОЇ ЗВІРКИ
	//
	// Перша правка робила місяць звірки хвостом «з 2-го»: дохід лише після
	// звірки, витрати за 29 днів, місяців 1,97 — і «приходить у середньому»
	// виходило числом, якого не буває в жодному місяці. Власник: «воно пляше
	// від теперішнього мінуса, а не від мінуса на початок періоду; те, що я
	// звірив баланс, означає лише, що я в цьому місяці вже щось закинув».
	//
	// Тому місяць звірки входить ЦІЛИМ — повним доходом, повними витратами й
	// розстрочками, — а відлік іде від боргу НА ЙОГО ПОЧАТОК. Цей борг ніхто
	// не міряв, тож він ВІДНОВЛЮЄТЬСЯ зі звірки: до теперішнього мінуса
	// додається дохід, що прийшов на картку до звірки (платіжний день ≤ її
	// дати), і віднімаються списані до неї розстрочки й витрати за прожиті
	// дні — заявлені, як і всюди на цьому екрані. Арифметично це те саме,
	// що хвіст від звірки: Σ повних профіцитів − борг на початок = Σ
	// профіцитів після звірки − борг зараз. Змінюється не запас, а те, що
	// людина читає: місяці цілі, середні справжні, таблиця — про місяць.
	//
	// Глибше за ліміт бути не можна: коли ліміти задані всім карткам плану,
	// відновлений борг обрізається їхньою сумою.
	//
	// Одне вікно на всі картки, а не своє в кожної: дохід у них спільний, і
	// два різні початки дали б два різні середні на ті самі гроші.
	var targets []domain.Debt
	var names []string
	var debtTotal, limitLeft, limitTotal int64
	limitKnown, limitAll := false, true
	var markDate domain.Date
	endM := 0
	for _, d := range debts {
		if !d.IsCard() || d.Closed() || d.ExitBy == "" || !d.ExitBy.After(today) {
			continue
		}
		st := domain.CardState(d, marks, ops, debts, today)
		if st.Debt <= 0 {
			continue // з цієї виходити нема звідки — і це найкращий стан
		}
		targets = append(targets, d)
		names = append(names, d.Name)
		debtTotal += st.Debt
		// Скільки ще дозволяють самі ліміти: запас із доходу може бути
		// більшим, ніж банк узагалі дасть узяти. Картка, вибрана понад
		// ліміт, дає нуль, а не відʼємне — у сусідньої картки її мінус
		// нічого не забирає.
		if d.LimitAmount > 0 {
			limitKnown = true
			limitTotal += d.LimitAmount
			if room := d.LimitAmount - st.Debt; room > 0 {
				limitLeft += room
			}
		} else {
			limitAll = false
		}
		if m := domain.MonthsBetween(today, d.ExitBy); m > endM {
			endM = m
		}
		if last := lastMarkDate(d, marks, today); last.After(markDate) {
			markDate = last
		}
	}
	if len(targets) == 0 || debtTotal <= 0 {
		return nil
	}

	// Перший місяць вікна. Без звірки — поточний: у балансі й так нічого
	// не виміряно. Зі звіркою — її місяць, якщо в ньому ще щось платиться
	// після неї; інакше наступний.
	startM := 0
	var markMonth domain.Date // дата звірки, коли її місяць у вікні
	if markDate != "" {
		markM := domain.MonthsBetween(today, markDate)
		startM = markM + 1
		if mp := buildMonthPlan(src, rates, today, markM, 0, markDate); mp != nil && mp.GrossUAH > 0 {
			startM = markM
			markMonth = markDate
		}
	}
	if endM < startM {
		// До найпізнішої дати не лишилось нічого непрожитого. Це не
		// помилка, а відповідь: питати «скільки витрачати щомісяця» пізно.
		return nil
	}
	months := endM - startM + 1
	if months > 24 {
		months = 24
	}

	declared := 0.0
	if set != nil && set.MonthlyExpensesUAH != nil {
		declared = *set.MonthlyExpensesUAH
	}
	// Вимір витрат — по картці з НАЙБІЛЬШИМ боргом: саме через неї йде
	// основний оборот, а складати виміряне спалення однієї картки з
	// невиміряним другої означало б скласти факт із припущенням.
	main := targets[0]
	mainDebt := domain.CardState(main, marks, ops, debts, today).Debt
	for _, d := range targets[1:] {
		if v := domain.CardState(d, marks, ops, debts, today).Debt; v > mainDebt {
			main, mainDebt = d, v
		}
	}
	burn := domain.CardBurnFrom(main, marks, ops, today)
	spend, basis := declared, "заявлено"
	if burn.Known {
		spend, basis = float64(burn.PerMonth)/100, "виміряно"
	}

	var gross, invest, inst float64
	// perMonth — той самий обхід, але помісячно: із нього виходить і
	// середнє, і прохід балансу вперед. Другого циклу не заводимо, бо
	// «скільки прийде в жовтні» мусить лишитись одним означенням.
	perMonth := make([]debtMonthRow, 0, months)
	for m := startM; m < startM+months; m++ {
		row := debtMonthRow{}
		if mp := buildMonthPlan(src, rates, today, m, 0, ""); mp != nil {
			row.gross, row.invest = mp.GrossUAH, mp.IncomeUAH+mp.ExtraUAH
		}
		row.inst = cardInstallmentsInMonth(src, rates, today, m, "")
		perMonth = append(perMonth, row)
		gross += row.gross
		invest += row.invest
		inst += row.inst
	}
	gross /= float64(months)
	invest /= float64(months)
	inst /= float64(months)

	// Борг на ПОЧАТОК вікна. Коли вікно починається з місяця звірки — його
	// відновлюють: що прийшло до звірки, додати назад; що списалось і
	// витратилось за прожиті дні — теж (воно вже зменшило/збільшило мінус).
	debtNow := float64(debtTotal) / 100
	startDebt := debtNow
	var paidBefore, instBefore, spendBefore float64
	if markMonth != "" {
		full := perMonth[0]
		after := debtMonthRow{}
		if mp := buildMonthPlan(src, rates, today, startM, 0, markMonth); mp != nil {
			after.gross, after.invest = mp.GrossUAH, mp.IncomeUAH+mp.ExtraUAH
		}
		after.inst = cardInstallmentsInMonth(src, rates, today, startM, markMonth.AddDays(1))
		days := monthStart(today, startM+1).AddDays(-1).Day()
		paidBefore = round2((full.gross - full.invest) - (after.gross - after.invest))
		instBefore = round2(full.inst - after.inst)
		spendBefore = round2(spend * float64(markMonth.Day()) / float64(days))
		startDebt = round2(debtNow + paidBefore - instBefore - spendBefore)
		if limitAll && startDebt > float64(limitTotal)/100 {
			startDebt = float64(limitTotal) / 100
		}
	}

	// Потреба — по кожній картці за ЇЇ датою, і сумою. Різниця між боргом
	// на початок і боргом зараз лягає на головну картку: саме через неї йде
	// оборот, і саме її мінус аванс і зменшив.
	var needTotal int64
	for _, d := range targets {
		debt := domain.CardState(d, marks, ops, debts, today).Debt
		if d.ID == main.ID {
			if debt += int64(math.Round((startDebt - debtNow) * 100)); debt < 0 {
				debt = 0
			}
		}
		own := domain.MonthsBetween(today, d.ExitBy) - startM + 1
		if own < 1 {
			own = 1 // дата вже в цьому вікні: усе треба звільнити одразу
		}
		needTotal += int64(math.Ceil(float64(debt) / float64(own)))
	}

	exitBy := targets[0].ExitBy
	for _, d := range targets[1:] {
		if d.ExitBy.After(exitBy) {
			exitBy = d.ExitBy
		}
	}
	plan := domain.CardExit(domain.CardExitInput{
		DebtUAH:        int64(math.Round(startDebt * 100)),
		DebtNowUAH:     debtTotal,
		From:           monthStart(today, startM),
		GrossUAH:       int64(math.Round(gross * 100)),
		InvestUAH:      int64(math.Round(invest * 100)),
		InstallmentUAH: int64(math.Round(inst * 100)),
		SpendUAH:       int64(math.Round(spend * 100)),
		ExitBy:         exitBy, Today: today,
		// Місяців рівно стільки, скільки у вікні, а не скільки днів ділиться
		// на 30,44: інакше «треба звільняти щомісяця» рахувалось би з того,
		// що вже минуло.
		Months:          float64(months),
		NeedPerMonthUAH: needTotal,
	})
	if !plan.Known {
		return nil
	}
	out := &state.DebtExit{
		Cards: names, ExitBy: string(plan.ExitBy), Months: round2(plan.Months),
		SpendCapUAH:      round2(float64(plan.SpendCap) / 100),
		NeedPerMonthUAH:  round2(float64(plan.NeedPerMonth) / 100),
		Feasible:         plan.Feasible,
		ShortPerMonthUAH: round2(float64(plan.ShortPerMonth) / 100),
		ETADate:          string(plan.ETADate),
		GrossUAH:         round2(gross),
		InvestUAH:        round2(invest),
		InstallmentsUAH:  round2(inst),
		SpendUsedUAH:     round2(spend),
		SpendBasis:       basis,
		SpendDeclaredUAH: round2(declared),
		BurnWhy:          burn.Why,

		WithInvestSpendCapUAH: round2(float64(plan.WithInvestSpendCap) / 100),
		WithInvestETADate:     string(plan.WithInvestETADate),

		HeadroomUAH:           round2(float64(plan.Headroom) / 100),
		MaxDebtUAH:            round2(float64(plan.MaxDebt) / 100),
		WithInvestHeadroomUAH: round2(float64(plan.WithInvestHeadroom) / 100),

		StartDebtUAH: round2(startDebt), DebtNowUAH: round2(debtNow),
		StartMonth: monthKeyAt(today, startM),
	}
	if markMonth != "" {
		out.MarkDate = string(markMonth)
		out.PaidBeforeMarkUAH = paidBefore
		out.InstallmentsBeforeMarkUAH = instBefore
		out.SpendBeforeMarkUAH = spendBefore
	}
	if limitKnown {
		v := round2(float64(limitLeft) / 100)
		out.LimitLeftUAH = &v
	}
	if burn.Known {
		out.SpendMeasuredUAH = round2(float64(burn.PerMonth) / 100)
		out.BurnFrom, out.BurnTo = string(burn.From), string(burn.To)
	}
	out.Schedule = debtExitWalk(perMonth, startDebt, spend, today, startM)
	return out
}

// cardInstallmentsInMonth — платежі розстрочок, ПРИВʼЯЗАНИХ до карток, у
// місяці зі зсувом m; при непорожньому from — лише з цієї дати (день після
// звірки: те, що списалось до неї, уже в її балансі).
//
// Вони списуються з картки, тож воюють за ті самі гроші, що й витрати. У
// портфельний план місяця вони при цьому НЕ входять — довід у шапці
// debtDueForMonth.
func cardInstallmentsInMonth(src *sources, rates fx.Rates, today domain.Date,
	m int, from domain.Date) float64 {

	first := monthStart(today, m)
	if from.After(first) {
		first = from
	}
	last := monthStart(today, m+1).AddDays(-1)
	total := 0.0
	for _, d := range src.debts {
		if d.Closed() || d.IsCard() || d.CardID == 0 {
			continue
		}
		for _, p := range domain.DebtSchedule(d, 0, first, last) {
			if u, err := fx.ToUAH(money.New(p.Amount, d.Currency), rates); err == nil {
				total += float64(u.Amount()) / 100
			}
		}
	}
	return round2(total)
}

// debtAhead — борг по місяцях горизонту для «Маршруту грошей» (route.go,
// «Борг»). Ключ — місяць "YYYY-MM", m = 0..months.
//
// Чотири числа, і всі з тих самих функцій, що годують план місяця й картку
// боргу: обовʼязкове — debtDueForMonth (уже відняте від планових ніг),
// карткові розстрочки — cardInstallmentsInMonth (картковий контур), тіло за
// графіком — те, на що тане TotalUAH (усі відкриті розстрочки, як у
// buildDebtPlan; картка тане лише від платежів, тож тут її немає), рубіж
// покриття — debtCoverUAH станом на перше число місяця.
func debtAhead(src *sources, rates fx.Rates, today domain.Date, months int) map[string]routeDebtMonth {
	out := make(map[string]routeDebtMonth, months+1)
	if len(src.debts) == 0 {
		return out
	}
	for m := 0; m <= months; m++ {
		first := monthStart(today, m)
		last := monthStart(today, m+1).AddDays(-1)
		principal := 0.0
		for _, d := range src.debts {
			if d.Closed() || d.IsCard() {
				continue
			}
			for _, p := range domain.DebtSchedule(d, 0, first, last) {
				if u, err := fx.ToUAH(money.New(p.Principal, d.Currency), rates); err == nil {
					principal += float64(u.Amount()) / 100
				}
			}
		}
		out[monthKeyAt(today, m)] = routeDebtMonth{
			DueUAH:       debtDueForMonth(src, rates, today, m),
			CardInstUAH:  cardInstallmentsInMonth(src, rates, today, m, ""),
			PrincipalUAH: round2(principal),
			CoverUAH:     debtCoverUAH(src.debts, src.debtMarks, src.debtOps, rates, first),
		}
	}
	return out
}

// debtMonthRow — валовий дохід місяця, та його частина, що йде в портфель,
// і платежі карткових розстрочок.
//
// Іменований тип, а не анонімна структура: він перетинає межу функції, і
// анонімний довелось би повторити в сигнатурі слово в слово.
type debtMonthRow struct{ gross, invest, inst float64 }

// debtExitWalk — баланс картки місяць за місяцем до нуля, від боргу на
// ПОЧАТОК першого місяця (debtUAH).
//
// Кожен місяць гасить борг на «валовий мінус портфель мінус витрати», і
// саме тому місяці НЕ однакові: у власника одна зарплата скінчилась у
// серпні, а дві починаються у вересні, тож перший крок помітно менший за
// решту. Середнє цього не показує, а таблиця показує.
//
// МОВЧИТЬ, КОЛИ БОРГ НЕ МЕНШАЄ В ЖОДНОМУ МІСЯЦІ. Двадцять чотири рядки з
// однаковим залишком — не таблиця, а спосіб не сказати «за цим темпом
// виходу не буде»; це вже сказано порожньою датою виходу поруч. Слабкий
// перший місяць при живому темпі далі — не привід мовчати.
func debtExitWalk(perMonth []debtMonthRow,
	debtUAH, spendUAH float64, today domain.Date, startM int) []state.DebtExitStep {

	if debtUAH <= 0 || len(perMonth) == 0 {
		return nil
	}
	pays := make([]float64, len(perMonth))
	total := 0.0
	for m, row := range perMonth {
		if pay := row.gross - row.invest - row.inst - spendUAH; pay > 0 {
			pays[m] = pay
			total += pay
		}
	}
	if total <= 0 {
		return nil // борг не меншає в жодному місяці
	}
	left := debtUAH
	out := make([]state.DebtExitStep, 0, len(perMonth))
	for m, row := range perMonth {
		left -= pays[m]
		if left < 0 {
			left = 0
		}
		out = append(out, state.DebtExitStep{
			Month:    monthKeyAt(today, startM+m),
			GrossUAH: round2(row.gross), InvestUAH: round2(row.invest),
			InstallmentsUAH: round2(row.inst),
			SpendUAH:        round2(spendUAH), LeftUAH: round2(left),
		})
		if left == 0 {
			break
		}
	}
	return out
}

// debtCurrency — валюта боргу за id; гривня, коли борг не знайдено (рух
// під видаленим боргом FK не пускає, але бекап старшої схеми міг би).
func debtCurrency(debts []domain.Debt, id int64) string {
	for _, d := range debts {
		if d.ID == id {
			return d.Currency
		}
	}
	return money.UAH
}

// debtLeftUAH — скільки боргу під ставкою, грн-екв.
//
// Окремою функцією від buildDebtPlan, бо читач другий і працює РАНІШЕ:
// прогноз збирається до того, як зʼявиться план місяця, а той блок без
// плану місяця порахувати стелю не може.
func debtLeftUAH(src *sources, rates fx.Rates, today domain.Date) float64 {
	total := 0.0
	for _, d := range src.debts {
		if d.Closed() {
			continue
		}
		balance := int64(0)
		if d.IsCard() {
			st := domain.CardState(d, src.debtMarks, src.debtOps, nil, today)
			balance = st.NonGrace
			if st.Debt > 0 && balance > st.Debt {
				balance = st.Debt
			}
		} else {
			for _, p := range domain.InstallmentSchedule(d) {
				if !p.Date.Before(today) {
					balance += p.Principal
				}
			}
		}
		if balance <= 0 {
			continue
		}
		if u, err := fx.ToUAH(money.New(balance, d.Currency), rates); err == nil {
			total += float64(u.Amount()) / 100
		}
	}
	return round2(total)
}

// debtFillSharePct — стеля дострокового відсотком; нуль, коли не задано.
func debtFillSharePct(set *state.SettingsDoc) float64 {
	if set == nil || set.DebtFillSharePct == nil {
		return 0
	}
	return *set.DebtFillSharePct
}

// debtOwedUAH — скільки винен УСЬОГО, грн-екв.: і те, на що нараховують, і
// пільговий борг картки.
//
// Ширше за DebtPlan.TotalUAH навмисно, і це не розбіжність. Черга
// погашення питає «що коштує грошей», тож пільговий оборот у неї не йде;
// чистий капітал питає «скільки в мене насправді», і для нього байдуже,
// під яку ставку ти винен.
func debtOwedUAH(src *sources, rates fx.Rates, today domain.Date) float64 {
	total := 0.0
	for _, d := range src.debts {
		if d.Closed() {
			continue
		}
		owed := int64(0)
		if d.IsCard() {
			owed = domain.CardState(d, src.debtMarks, src.debtOps, nil, today).Debt
		} else {
			for _, p := range domain.InstallmentSchedule(d) {
				if !p.Date.Before(today) {
					owed += p.Principal
				}
			}
		}
		if owed <= 0 {
			continue
		}
		if u, err := fx.ToUAH(money.New(owed, d.Currency), rates); err == nil {
			total += float64(u.Amount()) / 100
		}
	}
	return round2(total)
}

// lastMarkDate — дата останньої звірки картки на або до сьогодні.
// Порожньо, коли звірок немає: тоді прохід починається з поточного місяця,
// бо в балансі й так нічого не виміряно.
func lastMarkDate(card domain.Debt, marks []domain.DebtMark, today domain.Date) domain.Date {
	var out domain.Date
	for _, m := range marks {
		if m.DebtID != card.ID || m.Date.After(today) {
			continue
		}
		if out == "" || m.Date.After(out) {
			out = m.Date
		}
	}
	return out
}
