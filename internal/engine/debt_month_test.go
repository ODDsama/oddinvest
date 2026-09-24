package engine

import (
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	money "github.com/Rhymond/go-money"
)

// НАДГРОБОК: ДОСТРОКОВЕ ПОГАШЕННЯ НЕ ЗАБИРАЄ ПОРТФЕЛЬНИХ ГРОШЕЙ.
//
// Тут стояли два тести — TestAllocateCutsDebtBeforeGoals (борг ріжеться
// після подушки й перед цілями) і TestAllocateDebtRespectsOwnPolicy
// (політика «з яких грошей гасити»). Обидва описували вирізку, якої більше
// немає: вона брала частку від PlanDebtUAH, тобто від грошей, призначених
// у портфель, і зменшувала базу, від якої міряються цільові частки видів.
//
// Замість них — один тест на протилежне твердження, і саме він тут
// найпотрібніший: симетрія з подушкою й цілями здається очевидною («борг
// під пʼятдесят відсотків дорожчий за будь-який вид»), тож наступний автор
// потягнеться повернути вирізку саме сюди.
func TestAllocateLeavesDebtOutOfPortfolioMoney(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, &state.Reserve{
		GapUAH: state.Major(5000, money.UAH), FillMonthUAH: state.Major(1000, money.UAH), FillNowUAH: state.Major(1000, money.UAH), FillFromUAH: state.Major(5000, money.UAH),
	})
	// Борг живий, дорогий і зі стелею — тобто все, що колись вмикало вирізку.
	doc.Debt = &state.DebtPlan{
		TotalUAH: state.Major(30000, money.UAH), TopRatePct: 49.8, TopName: "Холодильник",
		FillMonthUAH: state.Major(2000, money.UAH), FillNowUAH: state.Major(2000, money.UAH),
	}

	got := AllocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, ToMoneyJSON(money.New(500000, money.UAH)), 5000,
		AllocAllow{ReserveUAH: 5000, GoalsUAH: 5000}, money.UAH, nil)

	// Подушка своє бере — вона з портфельних грошей і далі ріже.
	if got.Reserve == nil || got.Reserve.AmountUAH.Major() != 1000 {
		t.Fatalf("подушка: %+v", got.Reserve)
	}
	// А на папери йде ВСЯ решта: 5 000 − 1 000 = 4 000, чотири квитки по
	// 1 000. Була б вирізка боргу — лишилось би два.
	if got.AvailUAH.Major() != 4000 {
		t.Errorf("на папери %.2f, чекали 4000: борг більше не ріже портфельних грошей",
			got.AvailUAH.Major())
	}
	spent := 0.0
	for _, l := range got.Lines {
		spent += l.TotalUAH.Major()
	}
	if spent != 4000 {
		t.Errorf("куплено на %.2f, чекали 4000", spent)
	}
}

// Режим виходу вмикає стелю подушки САМ, не питаючи про ставку.
//
// Інакше найбільший борг власника її не вмикав би взагалі: у пільговому
// періоді він коштує нуль, реальна ставка відʼємна, і за загальним порогом
// він проходить як безкоштовний. Але названа дата виходу означає «гроші
// потрібні зараз».
func TestExitModeCapsReserveEvenWithoutRate(t *testing.T) {
	today := domain.Date("2026-09-10")
	card := domain.Debt{
		ID: 1, Kind: domain.DebtCard, Currency: money.UAH,
		StatementDay: 30, APRBp: 4788,
	}
	// Борг є, але весь пільговий: нараховувати ще нема на що.
	marks := []domain.DebtMark{{DebtID: 1, Date: "2026-09-01",
		Balance: -180_000_00, StatementDue: 180_000_00}}

	if debtCapsReserve([]domain.Debt{card}, marks, nil, 7, today) {
		t.Error("пільговий борг сам собою ввімкнув стелю подушки")
	}
	card.ExitBy = "2026-11-30"
	if !debtCapsReserve([]domain.Debt{card}, marks, nil, 7, today) {
		t.Error("названа дата виходу не ввімкнула стелю подушки")
	}
	// Дата в минулому режимом не є.
	card.ExitBy = "2026-01-01"
	if debtCapsReserve([]domain.Debt{card}, marks, nil, 7, today) {
		t.Error("минула дата виходу далі тримає стелю")
	}
}

// Стеля подушки на час боргу вмикається лише від боргу, що коштує РЕАЛЬНИХ
// грошей. Безвідсоткова розстрочка «частинами» її не вмикає — і це
// виходить само собою з порогу «реальна ставка вище нуля».
func TestDebtCapsReserveIgnoresFreeInstallment(t *testing.T) {
	today := domain.Date("2026-09-10")
	free := domain.Debt{
		ID: 1, Kind: domain.DebtInstallment, Currency: money.UAH,
		Principal: 30_000_00, PaymentsTotal: 9, FirstPaymentDate: "2026-09-30",
	}
	if debtCapsReserve([]domain.Debt{free}, nil, nil, 7, today) {
		t.Error("безкоштовна розстрочка ввімкнула стелю подушки")
	}

	// Комісія Є і договір її при достроковому СКАСОВУЄ — лише такий борг
	// вмикає стелю: у ньому дострокові гроші справді щось скасовують.
	paid := free
	paid.FeeMonthBp = 199
	paid.FeeOnPrepay = domain.DebtFeeCancel
	if !debtCapsReserve([]domain.Debt{paid}, nil, nil, 7, today) {
		t.Error("розстрочка під ~50%% не ввімкнула стелю подушки")
	}

	// Закритий борг не вмикає нічого: стеля самогасна за побудовою.
	closed := paid
	closed.ClosedDate = "2026-09-01"
	if debtCapsReserve([]domain.Debt{closed}, nil, nil, 7, today) {
		t.Error("погашений борг далі тримає стелю подушки")
	}
}

// Прохід балансу картки доходить до нуля, і сума погашеного дорівнює
// боргу.
func TestDebtExitScheduleReachesZero(t *testing.T) {
	rows := []debtMonthRow{
		{gross: 100_000, invest: 20_000},
		{gross: 100_000, invest: 20_000},
		{gross: 100_000, invest: 20_000},
	}
	got := debtExitWalk(rows, 120_000, 40_000, "2026-09-01", 0)
	if len(got) != 3 {
		t.Fatalf("кроків %d, чекали 3: 120 000 ÷ (100 000 − 20 000 − 40 000)", len(got))
	}
	if got[len(got)-1].LeftUAH.Major() != 0 {
		t.Errorf("останній крок лишає %.2f боргу", got[len(got)-1].LeftUAH.Major())
	}
	// Місяці НЕ однакові за побудовою — кожен несе свої числа, а не
	// середнє: інакше стрибок темпу не пояснити.
	if got[0].GrossUAH.Major() != 100_000 || got[0].InvestUAH.Major() != 20_000 || got[0].SpendUAH.Major() != 40_000 {
		t.Errorf("рядок не називає своїх чисел: %+v", got[0])
	}
	// Крок за кроком борг меншає рівно на профіцит.
	if got[0].LeftUAH.Major() != 80_000 || got[1].LeftUAH.Major() != 40_000 {
		t.Errorf("хід проходу: %.2f → %.2f", got[0].LeftUAH.Major(), got[1].LeftUAH.Major())
	}
}

// Коли витрати зʼїдають усе, що приходить, таблиці НЕМАЄ: двадцять чотири
// однакові рядки — не відповідь, а спосіб не сказати «виходу не буде».
func TestDebtExitScheduleStopsWhenDebtGrows(t *testing.T) {
	rows := []debtMonthRow{
		{gross: 100_000, invest: 20_000},
		{gross: 100_000, invest: 20_000},
	}
	if got := debtExitWalk(rows, 120_000, 80_000, "2026-09-01", 0); got != nil {
		t.Errorf("прохід намалював %d рядків при нульовому профіциті", len(got))
	}
	// Перший місяць може бути слабким (одна зарплата скінчилась, друга ще
	// не почалась) — але якщо далі темп є, таблиця будується, і слабкий
	// місяць у ній стоїть із нульовим кроком.
	rows[0] = debtMonthRow{gross: 30_000, invest: 20_000}
	got := debtExitWalk(rows, 120_000, 79_000, "2026-09-01", 0)
	if len(got) != 2 {
		t.Fatalf("живий профіцит у другому місяці дав %d кроків, чекали 2", len(got))
	}
	if got[0].LeftUAH.Major() != 120_000 || got[1].LeftUAH.Major() != 119_000 {
		t.Errorf("хід проходу: %.2f → %.2f, чекали 120 000 → 119 000", got[0].LeftUAH.Major(), got[1].LeftUAH.Major())
	}
}

// Карткові розстрочки віднімаються від того, що лишається на картці:
// стеля витрат менша рівно на їхні щомісячні платежі.
//
// Вони не тіло до погашення (за рішенням власника «вийти з ліміту» — це
// звести в нуль КАРТКИ), але з картки списуються, тож на витрати їх
// витратити вже не можна.
func TestDebtExitSubtractsCardInstallments(t *testing.T) {
	in := domain.CardExitInput{
		DebtUAH: 180_000_00, GrossUAH: 200_000_00, InvestUAH: 0,
		SpendUAH: 40_000_00, ExitBy: "2026-12-31",
		Today: domain.Date("2026-09-30"), Months: 3,
	}
	base := domain.CardExit(in)
	in.InstallmentUAH = 8_606_70
	with := domain.CardExit(in)

	if !base.Known || !with.Known {
		t.Fatalf("розрахунку немає: %+v / %+v", base, with)
	}
	if diff := base.SpendCap - with.SpendCap; diff != 8_606_70 {
		t.Errorf("стеля впала на %d, чекали рівно платіж розстрочок 860670", diff)
	}
	// І рядок «якщо й портфельні гроші підуть на картку» рахує з того
	// самого залишку — інакше два числа поруч суперечили б одне одному.
	if diff := base.WithInvestSpendCap - with.WithInvestSpendCap; diff != 8_606_70 {
		t.Errorf("другий рядок впав на %d, чекали 860670", diff)
	}
	// Дата виходу за нинішніми витратами теж відсувається: грошей на
	// погашення лишається менше.
	if with.ETADate <= base.ETADate {
		t.Errorf("дата виходу %s не пізніша за %s", with.ETADate, base.ETADate)
	}
}

// Рубіж покриття рахує МАЙБУТНІ ПЛАТЕЖІ, а не залишок тіла: візьмуть із
// власника тіло разом із комісіями, і на розстрочці, комісії якої не
// скасовуються, різниця між цими двома числами і є вся ціна помилки.
func TestDebtCoverCountsFuturePaymentsWithFees(t *testing.T) {
	today := domain.Date("2026-09-10")
	// 30 000 на 9 платежів, комісія 1,99% від початкової суми = 597 ₴/міс.
	// Перший платіж 30.09 — попереду всі девʼять.
	inst := domain.Debt{
		ID: 1, Kind: domain.DebtInstallment, Currency: money.UAH,
		Principal: 30_000_00, PaymentsTotal: 9, FirstPaymentDate: "2026-09-30",
		FeeMonthBp: 199, FeeOnPrepay: domain.DebtFeeKeep,
	}
	got := debtCoverUAH([]domain.Debt{inst}, nil, nil, fx.Rates{}, today, false)
	const want = 30_000 + 9*597 // тіло плюс девʼять комісій
	if got != want {
		t.Errorf("покриття %.2f, чекали %d — тіло разом із комісіями", got, want)
	}

	// Закритий борг не покривають: закривати нема чого.
	closed := inst
	closed.ClosedDate = "2026-09-01"
	if v := debtCoverUAH([]domain.Debt{closed}, nil, nil, fx.Rates{}, today, false); v != 0 {
		t.Errorf("погашений борг просить покриття %.2f", v)
	}
}

// Борг, який не можна погасити достроково, стелі подушки НЕ вмикає.
//
// Стеля стоїть на думці «гроші зараз корисніші в борзі»; там, де їх у борг
// подіти нікуди, обрізана подушка дала б менше грошей на руках при тому
// самому борзі.
func TestDebtCapsReserveIgnoresStickyFee(t *testing.T) {
	today := domain.Date("2026-09-10")
	sticky := domain.Debt{
		ID: 1, Kind: domain.DebtInstallment, Currency: money.UAH,
		Principal: 30_000_00, PaymentsTotal: 9, FirstPaymentDate: "2026-09-30",
		FeeMonthBp: 199, FeeOnPrepay: domain.DebtFeeKeep,
	}
	if debtCapsReserve([]domain.Debt{sticky}, nil, nil, 7, today) {
		t.Error("борг, який не можна погасити раніше, обрізав подушку")
	}
	// Незвірений договір поводиться так само: припускати скасування комісій
	// означало б обрізати подушку на підставі здогадки.
	unknown := sticky
	unknown.FeeOnPrepay = ""
	if debtCapsReserve([]domain.Debt{unknown}, nil, nil, 7, today) {
		t.Error("незвірений договір обрізав подушку")
	}
}

// Планова витрата з КАРТКИ віднімається від того, що лишається на картці:
// стеля витрат менша рівно на неї. Той самий механізм, що з розстрочками,
// і саме тому вона окремим доданком, а не всередині SpendUAH: витрата —
// подія, а SpendUAH — ритм.
func TestDebtExitSpendCapDropsOnCardPlanned(t *testing.T) {
	in := domain.CardExitInput{
		DebtUAH: 180_000_00, GrossUAH: 200_000_00, InvestUAH: 0,
		SpendUAH: 40_000_00, ExitBy: "2026-12-31",
		Today: domain.Date("2026-09-30"), Months: 3,
	}
	base := domain.CardExit(in)
	in.PlannedUAH = 30_000_00 // котел, розмазаний по трьох місяцях вікна
	with := domain.CardExit(in)

	if !base.Known || !with.Known {
		t.Fatalf("розрахунку немає: %+v / %+v", base, with)
	}
	if diff := base.SpendCap - with.SpendCap; diff != 30_000_00 {
		t.Errorf("стеля впала на %d, чекали рівно планову витрату 3000000", diff)
	}
	// Другий рядок міряється з того самого залишку — інакше два числа
	// поруч суперечили б одне одному.
	if diff := base.WithInvestSpendCap - with.WithInvestSpendCap; diff != 30_000_00 {
		t.Errorf("другий рядок впав на %d, чекали 3000000", diff)
	}
	if with.ETADate <= base.ETADate {
		t.Errorf("дата виходу %s не пізніша за %s", with.ETADate, base.ETADate)
	}
}

// СТОРОЖ ПРОТИ ПОДВІЙНОГО РАХУНКУ, дзеркальний до
// TestMonthPlanCardPlannedDoesNotTouchPlan. Витрата з ПОРТФЕЛЬНИХ грошей
// уже відбилась у плані місяця; зайшовши ще й у стелю витрат, вона
// забрала б удвічі більше, ніж коштує — рівно те, що вже коштувало
// 8 606,70 ₴/міс карткових розстрочок.
func TestDebtExitPlanPlannedDoesNotTouchCap(t *testing.T) {
	today := domain.Date("2026-09-10")
	src := &sources{
		planExpenses: []domain.PlanExpense{
			{Name: "Ремонт", Amount: 5_000_00, Currency: money.UAH,
				DueDate: "2026-10-15", PaidFrom: domain.PaidFromPlan},
			{Name: "Котел", Amount: 3_000_00, Currency: money.UAH,
				DueDate: "2026-10-20", PaidFrom: domain.PaidFromCard},
		},
	}
	if got := plannedInMonth(src, fx.Rates{}, today, 1, "", domain.PaidFromCard); got != 3000 {
		t.Errorf("картковий контур узяв %v, чекали самі 3000 — портфельна витрата "+
			"вже відбилась у плані місяця", got)
	}
	if got := plannedInMonth(src, fx.Rates{}, today, 1, "", domain.PaidFromPlan); got != 5000 {
		t.Errorf("портфельний контур узяв %v, чекали 5000", got)
	}
}

// У розкладі витрата стоїть у СВОЄМУ місяці, а не розмазана по вікну —
// заради цього розклад і потрібен поруч із середнім числом.
func TestDebtExitWalkShowsPlannedInItsMonth(t *testing.T) {
	rows := []debtMonthRow{
		{gross: 100_000, invest: 0},
		{gross: 100_000, invest: 0, planned: 30_000},
		{gross: 100_000, invest: 0},
	}
	got := debtExitWalk(rows, 200_000, 40_000, domain.Date("2026-09-10"), 0)
	if len(got) != 3 {
		t.Fatalf("у розкладі %d місяців, чекали 3: %+v", len(got), got)
	}
	if got[0].PlannedUAH.Major() != 0 || got[2].PlannedUAH.Major() != 0 {
		t.Errorf("витрата розмазалась на сусідні місяці: %+v", got)
	}
	if got[1].PlannedUAH.Major() != 30_000 {
		t.Errorf("у своєму місяці витрата %v, чекали 30000", got[1].PlannedUAH.Major())
	}
	// І борг у тому місяці меншає повільніше рівно на неї: 60 000 проти
	// 30 000 гасіння.
	if drop := got[0].LeftUAH.Major() - got[1].LeftUAH.Major(); drop != 30_000 {
		t.Errorf("у місяці витрати борг упав на %v, чекали 30000 замість звичних 60000", drop)
	}
}

// Підлога цілі подушки — лише борг, якого НЕ можна вигідно погасити
// достроково; рубіж на картці — усі борги.
//
// Доки підлогою йшли всі, розстрочка зі скасовними комісіями піднімала
// ціль назад на свою суму — ту саму, яку стеля «місяців подушки в
// боргах» щойно обрізала заради неї ж, і стеля не робила нічого.
func TestDebtFloorOnlyNonPrepayable(t *testing.T) {
	today := domain.Date("2026-09-10")
	keep := domain.Debt{ID: 1, Kind: domain.DebtInstallment, Currency: money.UAH,
		Principal: 9_000_00, PaymentsTotal: 9, FirstPaymentDate: "2026-09-30",
		FeeMonthBp: 199, FeeOnPrepay: domain.DebtFeeKeep}
	cancel := keep
	cancel.ID, cancel.FeeOnPrepay = 2, domain.DebtFeeCancel
	all := debtCoverUAH([]domain.Debt{keep, cancel}, nil, nil, fx.Rates{}, today, false)
	floor := debtCoverUAH([]domain.Debt{keep, cancel}, nil, nil, fx.Rates{}, today, true)
	one := debtCoverUAH([]domain.Debt{keep}, nil, nil, fx.Rates{}, today, false)
	if all != 2*one {
		t.Errorf("рубіж на картці %.2f, чекали обидва борги %.2f", all, 2*one)
	}
	if floor != one {
		t.Errorf("підлога %.2f, чекали лише розстрочку без скасування комісій %.2f", floor, one)
	}
}
