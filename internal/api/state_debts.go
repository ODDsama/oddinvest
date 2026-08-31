// Борг у документі стану: одне рішення, від якого залежать чужі числа.
//
// ЧОМУ САМИХ БОРГІВ У ДОКУМЕНТІ НЕМАЄ. Перелік боргів, стан картки й план
// погашення — REST (/api/debts, /api/payoff), і це не економія на
// контракті. Документ стану їде в MQTT і в щоденний знімок, тобто описує
// те, ЩО Є; стан картки живе годинами (баланс рухається щодня, звірка
// старіє), а план погашення взагалі проєкція й залежить від питання. Той
// самий поділ, що вже проведено для /api/progress і /api/decisions.
//
// У документ входить рівно те, що змінює ЧУЖІ числа: обовʼязкові платежі
// місяця (MonthPlan.DebtDueUAH) і ця ознака.
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
	if out.TotalUAH == 0 && out.CardsWatched == 0 && out.Exit == nil {
		return nil
	}
	return out
}

// buildDebtExit — вихід із ліміту за найближчою названою датою.
//
// # СЕРЕДНІЙ ДОХІД, А НЕ ДОХІД ПОТОЧНОГО МІСЯЦЯ
//
// Спіймано на бойових даних: у серпні одна зарплата власника скінчилась, а
// три ще не почались, і «скільки приходить» вийшло 128 911 ₴ замість
// 222 800. Стеля витрат, порахована з такого місяця, була б удвічі
// суворішою за правду — і людина повірила б їй, бо число виглядає
// точним.
//
// Тому дохід усереднюється по місяцях ДО ЦІЛІ: саме той період, про який
// і питають. Місяці розгортає той самий buildMonthPlan, тож другого
// означення «скільки цей потік платить у листопаді» не зʼявляється.
func buildDebtExit(debts []domain.Debt, marks []domain.DebtMark, ops []domain.DebtOp,
	set *state.SettingsDoc, src *sources, rates fx.Rates, today domain.Date) *state.DebtExit {

	var card domain.Debt
	for _, d := range debts {
		if !d.IsCard() || d.Closed() || d.ExitBy == "" || !d.ExitBy.After(today) {
			continue
		}
		if card.ID == 0 || d.ExitBy.Before(card.ExitBy) {
			card = d
		}
	}
	if card.ID == 0 {
		return nil
	}
	st := domain.CardState(card, marks, ops, debts, today)
	if st.Debt <= 0 {
		return nil // виходити нема звідки — і це найкращий зі станів
	}

	// ЯКИЙ МІСЯЦЬ ПЕРШИЙ — і це не дрібниця, а вада, яку власник побачив
	// на екрані 31 серпня.
	//
	// Борг у звірці — це вже РЕЗУЛЬТАТ прожитих місяців: дохід прийшов,
	// витрати сталися, залишок осів у балансі. Прохід, який починається з
	// того самого місяця, рахує його вдруге — і обіцяє погашення, яке вже
	// або відбулось, або не відбулось.
	//
	// Тому перший крок — місяць ПІСЛЯ того, у якому зроблено звірку.
	// Правило свідомо консервативне: при звірці всередині місяця решта
	// його доходу в прохід не потрапить, тобто число вийде обережнішим, а
	// не оптимістичнішим. Ліки — нова звірка наприкінці місяця.
	//
	// Для СТАРОЇ звірки (місяць уже минув) прохід починається з поточного:
	// ті гроші справді ще не виміряні, і пропустити їх означало б
	// викинути реальні місяці. Що звірка застаріла, сказано окремо.
	startM := 0
	if last := lastMarkDate(card, marks, today); last != "" {
		if n := domain.MonthsBetween(today, last) + 1; n > startM {
			startM = n
		}
	}
	endM := domain.MonthsBetween(today, card.ExitBy)
	months := endM - startM + 1
	if months < 1 {
		// До дати не лишилось жодного НЕПРОЖИТОГО місяця. Це не помилка, а
		// відповідь: питати «скільки витрачати щомісяця» вже пізно.
		return nil
	}
	if months > 24 {
		months = 24
	}
	var gross, invest float64
	// perMonth — той самий обхід, але помісячно: із нього виходить і
	// середнє, і прохід балансу вперед. Другого циклу не заводимо, бо
	// «скільки прийде в жовтні» мусить лишитись одним означенням.
	perMonth := make([]debtMonthRow, 0, months)
	for m := startM; m < startM+months; m++ {
		mp := buildMonthPlan(src, rates, today, m, 0)
		if mp == nil {
			perMonth = append(perMonth, debtMonthRow{})
			continue
		}
		perMonth = append(perMonth, debtMonthRow{mp.GrossUAH, mp.IncomeUAH + mp.ExtraUAH})
		gross += mp.GrossUAH
		invest += mp.IncomeUAH + mp.ExtraUAH
	}
	gross /= float64(months)
	invest /= float64(months)

	declared := 0.0
	if set != nil && set.MonthlyExpensesUAH != nil {
		declared = *set.MonthlyExpensesUAH
	}
	burn := domain.CardBurnFrom(card, marks, ops, today)
	spend, basis := declared, "заявлено"
	if burn.Known {
		spend, basis = float64(burn.PerMonth)/100, "виміряно"
	}

	plan := domain.CardExit(domain.CardExitInput{
		DebtUAH:   st.Debt,
		GrossUAH:  int64(math.Round(gross * 100)),
		InvestUAH: int64(math.Round(invest * 100)),
		SpendUAH:  int64(math.Round(spend * 100)),
		ExitBy:    card.ExitBy, Today: today,
		// Місяців рівно стільки, скільки НЕПРОЖИТИХ лишилось до дати, а не
		// стільки, скільки днів ділиться на 30,44: інакше «треба звільняти
		// щомісяця» рахувалось би з місяців, які вже минули.
		Months: float64(months),
	})
	if !plan.Known {
		return nil
	}
	out := &state.DebtExit{
		Card: card.Name, ExitBy: string(plan.ExitBy), Months: round2(plan.Months),
		SpendCapUAH:      round2(float64(plan.SpendCap) / 100),
		NeedPerMonthUAH:  round2(float64(plan.NeedPerMonth) / 100),
		Feasible:         plan.Feasible,
		ShortPerMonthUAH: round2(float64(plan.ShortPerMonth) / 100),
		ETADate:          string(plan.ETADate),
		GrossUAH:         round2(gross),
		InvestUAH:        round2(invest),
		SpendUsedUAH:     round2(spend),
		SpendBasis:       basis,
		SpendDeclaredUAH: round2(declared),
		BurnWhy:          burn.Why,

		WithInvestSpendCapUAH: round2(float64(plan.WithInvestSpendCap) / 100),
		WithInvestETADate:     string(plan.WithInvestETADate),
	}
	if burn.Known {
		out.SpendMeasuredUAH = round2(float64(burn.PerMonth) / 100)
		out.BurnFrom, out.BurnTo = string(burn.From), string(burn.To)
	}
	out.Schedule = debtExitWalk(perMonth, float64(st.Debt)/100, spend, today, startM)
	return out
}

// debtMonthRow — валовий дохід місяця й та його частина, що йде в
// портфель. Іменований тип, а не анонімна структура: він перетинає межу
// функції, і анонімний довелось би повторити в сигнатурі слово в слово.
type debtMonthRow struct{ gross, invest float64 }

// debtExitWalk — баланс картки місяць за місяцем до нуля.
//
// Кожен місяць гасить борг на «валовий мінус портфель мінус витрати», і
// саме тому місяці НЕ однакові: у власника одна зарплата скінчилась у
// серпні, а дві починаються у вересні, тож перший крок помітно менший за
// решту. Середнє цього не показує, а таблиця показує.
//
// МОВЧИТЬ, КОЛИ БОРГ НЕ МЕНШАЄ. Двадцять чотири рядки з однаковим
// залишком — не таблиця, а спосіб не сказати «за цим темпом виходу не
// буде»; це вже сказано порожньою датою виходу поруч.
func debtExitWalk(perMonth []debtMonthRow,
	debtUAH, spendUAH float64, today domain.Date, startM int) []state.DebtExitStep {

	if debtUAH <= 0 || len(perMonth) == 0 {
		return nil
	}
	left := debtUAH
	out := make([]state.DebtExitStep, 0, len(perMonth))
	for m, row := range perMonth {
		pay := row.gross - row.invest - spendUAH
		if pay <= 0 && m == 0 {
			return nil // борг не меншає з першого ж місяця
		}
		if pay < 0 {
			pay = 0
		}
		left -= pay
		if left < 0 {
			left = 0
		}
		out = append(out, state.DebtExitStep{
			Month:    monthKeyAt(today, startM+m),
			GrossUAH: round2(row.gross), InvestUAH: round2(row.invest),
			SpendUAH: round2(spendUAH), LeftUAH: round2(left),
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
