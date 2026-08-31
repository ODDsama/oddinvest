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
func buildDebtPlan(debts []domain.Debt, marks []domain.DebtMark, ops []domain.DebtOp,
	set *state.SettingsDoc, mp *state.MonthPlan, rates fx.Rates,
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
	if out.TotalUAH == 0 && out.CardsWatched == 0 {
		return nil
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
