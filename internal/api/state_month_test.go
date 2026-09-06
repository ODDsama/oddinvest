package api

import (
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// TestMonthReserveMoveNetsToZero — переміщення гаманець → матрац не є
// внеском, хоч і записане двома окремими рухами.
//
// Це той самий інваріант, який описує коментар у buildMonth, і його треба
// перевіряти прямо. Golden цього не робить: рухи резерву в багатій
// фікстурі стоять у травні-червні, а документ будується на 15 липня, тож
// гілка резерву в місячному циклі там не виконується взагалі — мутація
// «прибрати резерв із внесків» golden не завалила.
//
// Ціна помилки тут висока в обидва боки. Порахувати лише першу ногу —
// показати втрату капіталу, якої не було. Не рахувати другу — показати
// внесок там, де гроші просто переклали з кишені в кишеню.
func TestMonthReserveMoveNetsToZero(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	d := func(off int) domain.Date { return domain.NewDate(now.AddDate(0, 0, off)) }

	src := &sources{
		deposits: []store.Deposit{
			// Гроші пішли з рахунку брокера в матрац.
			{Date: d(-3), Amount: -100_000, Currency: money.UAH, Broker: "mono"},
		},
		reserveOps: []store.ReserveOp{
			// Та сама сума прийшла в матрац — друга нога переміщення.
			{Date: d(-3), Amount: 100_000, Currency: money.UAH, Place: "готівка"},
		},
	}
	out, err := buildMonth(src, domain.Holdings{}, fx.Rates{}, now, today, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.DepositedUAH.Amount(); got != 0 {
		t.Errorf("переміщення в резерв дало внесок %d, а мусить дати 0 — нових грошей не з'явилось", got)
	}
	// РОЗКЛАД — те, що тепер стоїть на плитці замість пари валових чисел.
	// Внесене нульове, але складається воно з двох рівних і протилежних
	// половин: сотня пішла з рахунків у матрац.
	if got := out.OutsideUAH.Amount(); got != 100_000 {
		t.Errorf("повз рахунки %d, очікували 100000", got)
	}
	if got := out.ContributedUAH.Amount(); got != -100_000 {
		t.Errorf("на рахунки %d, очікували -100000", got)
	}
	// А зняття — ЯКІР ДЕФЕКТУ, через який валові числа з екрана прибрані.
	// Воно чесне лише наполовину: гроші справді пішли з рахунку брокера,
	// але та сама сотня рахується ще й поповненням (нога резерву вище).
	// Нетто від цього правильне, обидва валові — завищені рівно на переказ,
	// і пара «поповнення X − зняття Y» показувала б переказ як рух назовні.
	// На живих даних це давало «зняття 4 941», де дві третини не були
	// зняттям. Поле лишається — його читає інтеграція HA.
	if got := out.WithdrawnUAH.Amount(); got != 100_000 {
		t.Errorf("знято %d, очікували 100000", got)
	}
}

// TestMonthExternalReserveIsContribution — гроші, відкладені в матрац
// ЗЗОВНІ (на рахунок брокера вони не заходили), це справжній внесок.
//
// Дзеркало попереднього тесту: разом вони фіксують, що резерв рахується
// в тому самому нетто, що й поповнення, а не окремим правилом.
func TestMonthExternalReserveIsContribution(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)

	src := &sources{
		reserveOps: []store.ReserveOp{
			{Date: domain.NewDate(now.AddDate(0, 0, -2)), Amount: 50_000,
				Currency: money.UAH, Place: "сейф"},
		},
	}
	out, err := buildMonth(src, domain.Holdings{}, fx.Rates{}, now, today, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.DepositedUAH.Amount(); got != 50_000 {
		t.Errorf("внесено %d, очікували 50000 — відкладене зовні теж внесок", got)
	}
	if got := out.WithdrawnUAH.Amount(); got != 0 {
		t.Errorf("знято %d, а знять не було", got)
	}
}

// --- план поточного місяця ---

// monthPlanSrc — потоки й відмітки під один тест. Курсів немає навмисно:
// усе в гривні, щоб перевірялась саме арифметика плану, а не конвертація.
func monthPlanSrc(flows []store.PlanFlow, rs []store.PlanReceipt,
	set *state.SettingsDoc) *sources {
	return &sources{planFlows: flows, planReceipts: rs, settings: set}
}

// TestMonthPlanNets — надходження, витрата й позапланове складаються в
// нетто, а не показуються трьома незалежними числами.
//
// Гілка витрат тут головна: у потоках вона від'ємна, у контракті додатна,
// і переплутати знак — це помилка, яку видно лише в сумі.
func TestMonthPlanNets(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	src := monthPlanSrc([]store.PlanFlow{
		{ID: 1, Name: "Зарплата", Kind: "income", Amount: 4_000_000, Currency: money.UAH,
			Cadence: "month", FromDate: domain.NewDate(now.AddDate(0, 0, -30)), InvestBP: 5000},
		{ID: 2, Name: "Оренда", Kind: "expense", Amount: 900_000, Currency: money.UAH,
			Cadence: "month", FromDate: domain.NewDate(now.AddDate(0, 0, -30)), InvestBP: 10000},
	}, []store.PlanReceipt{
		{FlowID: 0, Month: "2026-07", Name: "Премія", Amount: 600_000,
			Currency: money.UAH, InvestBP: 5000},
	}, nil)

	p := buildMonthPlan(src, fx.Rates{}, today, 0, 0, "")
	if p == nil {
		t.Fatal("план місяця не порахувався")
	}
	if p.IncomeUAH != 20000 { // 40 000 × 50%
		t.Errorf("надходження %v, очікували 20000", p.IncomeUAH)
	}
	if p.ExpenseUAH != 9000 {
		t.Errorf("витрати %v, очікували 9000 ДОДАТНІМ числом", p.ExpenseUAH)
	}
	if p.ExtraUAH != 3000 { // 6 000 × 50%
		t.Errorf("позапланове %v, очікували 3000", p.ExtraUAH)
	}
	if p.PlanUAH != 14000 { // 20 000 + 3 000 − 9 000
		t.Errorf("нетто %v, очікували 14000", p.PlanUAH)
	}
	if p.Sources != 1 {
		t.Errorf("джерел %d, очікували 1 — витрата джерелом доходу не є", p.Sources)
	}
}

// TestMonthPlanMarkNotArrived — відмітка «не прийшло» лишає джерело в
// переліку, хоч і дає нуль грошей.
//
// Це найтонша гілка в усій збірці. Якби «чи платить цього місяця»
// вирішувалось за сумою З ВІДМІТКАМИ, нуль викидав би рядок зі списку — і
// «зарплати не було» стало б невідрізнимим від «зарплати тут не
// планувалось». Різницю між цими двома станами весь застосунок береже
// окремо (див. чеклист надходжень), і тут вона мусить уціліти.
func TestMonthPlanMarkNotArrived(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	flows := []store.PlanFlow{
		{ID: 1, Name: "Зарплата", Kind: "income", Amount: 4_000_000, Currency: money.UAH,
			Cadence: "month", FromDate: domain.NewDate(now.AddDate(0, 0, -30)), InvestBP: 10000},
	}
	p := buildMonthPlan(monthPlanSrc(flows, []store.PlanReceipt{
		{FlowID: 1, Month: "2026-07", Amount: 0, Currency: money.UAH, InvestBP: 10000},
	}, nil), fx.Rates{}, today, 0, 0, "")
	if p.IncomeUAH != 0 {
		t.Errorf("надходження %v, очікували 0 — відмічено «не прийшло»", p.IncomeUAH)
	}
	if p.Sources != 1 || p.Marked != 1 {
		t.Errorf("джерел %d, відмічено %d — очікували 1 і 1: джерело нікуди не поділось",
			p.Sources, p.Marked)
	}
}

// TestMonthPlanMarkReplacesAmount — відмітка заміщає планову суму, і саме
// вона йде і в надходження, і в «підтверджено».
func TestMonthPlanMarkReplacesAmount(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	flows := []store.PlanFlow{
		{ID: 1, Name: "Зарплата", Kind: "income", Amount: 4_000_000, Currency: money.UAH,
			Cadence: "month", FromDate: domain.NewDate(now.AddDate(0, 0, -30)), InvestBP: 10000},
	}
	p := buildMonthPlan(monthPlanSrc(flows, []store.PlanReceipt{
		{FlowID: 1, Month: "2026-07", Amount: 4_500_000, Currency: money.UAH, InvestBP: 10000},
	}, nil), fx.Rates{}, today, 0, 0, "")
	if p.IncomeUAH != 45000 || p.ReceivedUAH != 45000 {
		t.Errorf("надходження %v, підтверджено %v — очікували 45000 в обох: факт заміщає план",
			p.IncomeUAH, p.ReceivedUAH)
	}
}

// TestMonthPlanLeftAndReserve — скільки ще закинути і скільки з цього піде
// в подушку.
//
// Стеля резерву тут прикладена до НОВИХ грошей місяця, а не до готівки на
// рахунках, і обрізається розривом до цілі: у резерв не кладуть більше,
// ніж до неї бракує.
func TestMonthPlanLeftAndCovered(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	flows := []store.PlanFlow{
		{ID: 1, Name: "Зарплата", Kind: "income", Amount: 3_000_000, Currency: money.UAH,
			Cadence: "month", FromDate: domain.NewDate(now.AddDate(0, 0, -30)), InvestBP: 10000},
	}
	src := monthPlanSrc(flows, nil, nil)

	p := buildMonthPlan(src, fx.Rates{}, today, 0, 12000, "")
	if p.LeftUAH != 18000 { // 30 000 плану − 12 000 уже внесених
		t.Errorf("лишилось %v, очікували 18000", p.LeftUAH)
	}
	if p.CoveredPct != 40 {
		t.Errorf("покрито %v%%, очікували 40", p.CoveredPct)
	}
	// Перевиконаний план: «лишилось» нуль, а покриття — БЕЗ стелі в сотню.
	// Обрізати 133% до 100% означало б сховати саме те, заради чого число й
	// показують.
	p = buildMonthPlan(src, fx.Rates{}, today, 0, 40000, "")
	if p.LeftUAH != 0 {
		t.Errorf("лишилось %v, очікували 0 — план місяця перевиконано", p.LeftUAH)
	}
	if p.CoveredPct != 133.33 {
		t.Errorf("покрито %v%%, очікували 133.33 без обрізання", p.CoveredPct)
	}
}

// TestReserveMonthShare — стеля подушки береться з НОВИХ грошей і зменшується
// на те, що вже покладено під матрац цього місяця.
//
// Обидві половини тут — виправлення того, що було. База: доти стеля
// рахувалась від готівки на брокерському рахунку, і на живих даних 40% від
// 6,19 ₴ давали пораду «спершу поповнити резерв — 2,48 ₴» при розриві в
// 359 500 ₴. Віднімання: доти порада висіла незмінною, скільки б ти не
// відкладав, бо розрив зменшується повільно, а стеля від плану стала.
func TestReserveMonthShare(t *testing.T) {
	exp, months, share := 10000.0, 6.0, 40.0
	set := &state.SettingsDoc{
		MonthlyExpensesUAH: &exp, ReserveTargetMonths: &months, ReserveFillSharePct: &share,
	}
	plan := &state.MonthPlan{PlanUAH: 30000, PlanReserveUAH: 30000, PlanGoalsUAH: 30000}

	// Резерв порожній при цілі 60 000 — розрив великий, обрізати нічим.
	month, fill, _ := reserveMonthShare(set, 0, plan, 0, false, 0, 0)
	if month != 12000 || fill != 12000 {
		t.Errorf("частка %v, лишилось %v — очікували 12000 і 12000 (40%% від 30 000)", month, fill)
	}
	// Половину місячної частки вже відкладено — порада зменшилась рівно на неї.
	month, fill, _ = reserveMonthShare(set, 5000, plan, 5000, false, 0, 0)
	if month != 12000 || fill != 7000 {
		t.Errorf("частка %v, лишилось %v — очікували 12000 і 7000", month, fill)
	}
	// Місячну частку добрано з запасом — порада мовчить, а не йде в мінус.
	if _, fill, _ = reserveMonthShare(set, 15000, plan, 15000, false, 0, 0); fill != 0 {
		t.Errorf("лишилось %v, очікували 0 — місячну частку вже перекрито", fill)
	}
	// Розрив менший за стелю — беремо розрив: класти більше за ціль означало
	// б завести другу ціль поруч із заданою в місяцях витрат.
	if month, _, _ = reserveMonthShare(set, 59_000, plan, 0, false, 0, 0); month != 1000 {
		t.Errorf("частка %v, очікували 1000 — більше за розрив не кладуть", month)
	}
	// Ціль зібрано — стеля мовчить незалежно від плану.
	if month, fill, _ = reserveMonthShare(set, 60_000, plan, 0, false, 0, 0); month != 0 || fill != 0 {
		t.Errorf("частка %v, лишилось %v — очікували нулі: ціль резерву зібрана", month, fill)
	}
	// Плану немає — рахувати нема від чого (готівка на рахунках сюди більше
	// не входить узагалі).
	if month, fill, _ = reserveMonthShare(set, 0, nil, 0, false, 0, 0); month != 0 || fill != 0 {
		t.Errorf("частка %v, лишилось %v — очікували нулі: плану доходу немає", month, fill)
	}
}

// TestMonthPlanAbsentWithoutFlows — без плану доходу блока немає ВЗАГАЛІ.
//
// Саме nil, а не нулі: «план обіцяє нуль» і «плану немає» — різні речі, і
// UI гілкується на наявність блока.
func TestMonthPlanAbsentWithoutFlows(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	if p := buildMonthPlan(monthPlanSrc(nil, nil, nil), fx.Rates{},
		domain.NewDate(now), 0, 0, ""); p != nil {
		t.Errorf("план місяця %+v, очікували nil — джерел доходу немає", p)
	}
}

// --- дозвіл потоку (0041) ---
//
// Три числа плану місяця: PlanUAH каже, скільки план дає всього,
// PlanReserveUAH і PlanGoalsUAH — скільки з того дозволено подушці й
// цілям. Від других двох міряються стелі наповнення, тож помилка тут не
// косметична: вона або обіцяє подушці гроші, на які та не має права, або
// мовчки вимикає її зовсім.

// ГОЛОВНИЙ РЯДОК УСІЄЇ ФІЧІ: без обмежень усі три числа рівні. Саме він
// тримає зворотну сумісність — план, набраний до появи дозволу, мусить
// поводитись рівно так, як поводився.
func TestMonthPlanBucketsEqualPlanWithoutLimits(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	src := monthPlanSrc([]store.PlanFlow{
		{ID: 1, Name: "Зарплата", Kind: "income", Amount: 4_000_000, Currency: money.UAH,
			Cadence: "month", FromDate: domain.NewDate(now.AddDate(0, 0, -30)), InvestBP: 10000},
		{ID: 2, Name: "Комуналка", Kind: "expense", Amount: 900_000, Currency: money.UAH,
			Cadence: "month", FromDate: domain.NewDate(now.AddDate(0, 0, -30)), InvestBP: 10000},
	}, []store.PlanReceipt{
		{FlowID: 0, Month: "2026-07", Name: "Премія", Amount: 600_000,
			Currency: money.UAH, InvestBP: 10000},
	}, nil)

	p := buildMonthPlan(src, fx.Rates{}, today, 0, 0, "")
	if p.PlanReserveUAH != p.PlanUAH || p.PlanGoalsUAH != p.PlanUAH {
		t.Errorf("без обмежень: план %v, подушці %v, цілям %v — мусять збігатися",
			p.PlanUAH, p.PlanReserveUAH, p.PlanGoalsUAH)
	}
}

// Дозволи НЕЗАЛЕЖНІ, і саме тому чисел два, а не одне: дохід буває таким,
// що на авто його класти можна, а в подушку — ні, і спільне число мусило б
// вибрати одну з двох неправд.
func TestMonthPlanBucketsAreIndependent(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	from := domain.NewDate(now.AddDate(0, 0, -30))
	src := monthPlanSrc([]store.PlanFlow{
		{ID: 1, Name: "Зарплата", Kind: "income", Amount: 3_000_000, Currency: money.UAH,
			Cadence: "month", FromDate: from, InvestBP: 10000},
		// Оренда — тільки на інвестиції: ні подушці, ні цілям.
		{ID: 2, Name: "Оренда", Kind: "income", Amount: 1_000_000, Currency: money.UAH,
			Cadence: "month", FromDate: from, InvestBP: 10000, Uses: "invest"},
		// Дивіденд — цілям можна, подушці ні.
		{ID: 3, Name: "Дивіденд", Kind: "income", Amount: 500_000, Currency: money.UAH,
			Cadence: "month", FromDate: from, InvestBP: 10000, Uses: "goals,invest"},
	}, nil, nil)

	p := buildMonthPlan(src, fx.Rates{}, today, 0, 0, "")
	if p.PlanUAH != 45000 {
		t.Fatalf("план %v, очікували 45000", p.PlanUAH)
	}
	if p.PlanReserveUAH != 30000 { // лише зарплата
		t.Errorf("подушці %v, очікували 30000 — оренда й дивіденд їй заборонені",
			p.PlanReserveUAH)
	}
	if p.PlanGoalsUAH != 35000 { // зарплата + дивіденд
		t.Errorf("цілям %v, очікували 35000 — заборонена лише оренда", p.PlanGoalsUAH)
	}
}

// ВИТРАТИ З'ЇДАЮТЬ СПЕРШУ ДОЗВОЛЕНЕ, і це закріплюється тестом, бо вибір
// свідомий: рознести комуналку між дозволеними й недозволеними доходами
// пропорційно можна лише вигаданим правилом. Повне віднімання
// консервативне в потрібний бік, і нижче нуля не опускається.
func TestMonthPlanExpensesEatAllowedFirst(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	from := domain.NewDate(now.AddDate(0, 0, -30))
	src := monthPlanSrc([]store.PlanFlow{
		{ID: 1, Name: "Зарплата", Kind: "income", Amount: 1_000_000, Currency: money.UAH,
			Cadence: "month", FromDate: from, InvestBP: 10000},
		{ID: 2, Name: "Оренда", Kind: "income", Amount: 4_000_000, Currency: money.UAH,
			Cadence: "month", FromDate: from, InvestBP: 10000, Uses: "invest"},
		{ID: 3, Name: "Комуналка", Kind: "expense", Amount: 1_500_000, Currency: money.UAH,
			Cadence: "month", FromDate: from, InvestBP: 10000},
	}, nil, nil)

	p := buildMonthPlan(src, fx.Rates{}, today, 0, 0, "")
	if p.PlanUAH != 35000 { // 10 000 + 40 000 − 15 000
		t.Fatalf("план %v, очікували 35000", p.PlanUAH)
	}
	// Дозволених 10 000, витрат 15 000 — не нижче нуля, а не «мінус 5 000».
	if p.PlanReserveUAH != 0 {
		t.Errorf("подушці %v, очікували 0: витрати більші за дозволений дохід",
			p.PlanReserveUAH)
	}
}

// Позапланове читає ВЛАСНИЙ дозвіл — потоку за ним немає, успадкувати нема
// від кого. Та сама межа, що вже проведена для частки в портфель.
func TestMonthPlanOtherReceiptUsesOwnPermission(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	src := monthPlanSrc([]store.PlanFlow{
		{ID: 1, Name: "Зарплата", Kind: "income", Amount: 1_000_000, Currency: money.UAH,
			Cadence: "month", FromDate: domain.NewDate(now.AddDate(0, 0, -30)), InvestBP: 10000},
	}, []store.PlanReceipt{
		// Премія відкладається лише на авто: у подушку не йде.
		{FlowID: 0, Month: "2026-07", Name: "Премія", Amount: 500_000,
			Currency: money.UAH, InvestBP: 10000, Uses: "goals"},
	}, nil)

	p := buildMonthPlan(src, fx.Rates{}, today, 0, 0, "")
	if p.PlanUAH != 15000 {
		t.Fatalf("план %v, очікували 15000", p.PlanUAH)
	}
	if p.PlanReserveUAH != 10000 {
		t.Errorf("подушці %v, очікували 10000 — премія їй заборонена", p.PlanReserveUAH)
	}
	if p.PlanGoalsUAH != 15000 {
		t.Errorf("цілям %v, очікували 15000 — премія саме для них", p.PlanGoalsUAH)
	}
}

// Стеля подушки міряється від ДОЗВОЛЕНОЇ бази. Без цього застосунок
// обіцяв би вирізку, якої розкладка не зробить: різниця осідала б у
// reserve_skip_why кожного надходження.
func TestReserveMonthShareUsesAllowedBase(t *testing.T) {
	exp, months, share := 10000.0, 6.0, 40.0
	set := &state.SettingsDoc{
		MonthlyExpensesUAH: &exp, ReserveTargetMonths: &months, ReserveFillSharePct: &share,
	}
	plan := &state.MonthPlan{PlanUAH: 30000, PlanReserveUAH: 12000, PlanGoalsUAH: 30000}
	month, fill, from := reserveMonthShare(set, 0, plan, 0, false, 0, 0)
	// Третє число — САМ ДОЗВІЛ, без темпу: саме на нього спирається другий
	// прохід розкладки, якому темп обходити можна, а дозвіл — ні.
	if from != 12000 {
		t.Errorf("дозвіл %v, очікували 12000 — це PlanReserveUAH, не помножений на темп", from)
	}
	if month != 4800 || fill != 4800 { // 40% від 12 000, а не від 30 000
		t.Errorf("частка %v, лишилось %v — очікували 4800 (40%% від дозволених 12 000)",
			month, fill)
	}
}

// --- планові витрати (0056) ---

// plannedSrc — план доходу з однією зарплатою плюс названі планові
// витрати. Зарплата потрібна не для краси: без потоків buildMonthPlan
// повертає nil, і планова витрата сама місяця не створює (довід — у
// ранній відмові функції).
func plannedSrc(now time.Time, exps ...domain.PlanExpense) *sources {
	src := monthPlanSrc([]store.PlanFlow{
		{ID: 1, Name: "Зарплата", Kind: "income", Amount: 4_000_000, Currency: money.UAH,
			Cadence: "month", FromDate: domain.NewDate(now.AddDate(0, 0, -30)), InvestBP: 10000},
	}, nil, nil)
	src.planExpenses = exps
	return src
}

func planExp(name, due, paid, from string, amount int64) domain.PlanExpense {
	return domain.PlanExpense{
		Name: name, Amount: amount, Currency: money.UAH,
		DueDate: domain.Date(due), PaidDate: domain.Date(paid), PaidFrom: from,
	}
}

// Планова витрата з ПОРТФЕЛЬНОГО контуру віднімається від грошей місяця
// нарівні з витратним потоком — і лише у свій місяць.
func TestMonthPlanSubtractsPlannedExpense(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	src := plannedSrc(now,
		planExp("Котел", "2026-07-20", "", domain.PaidFromPlan, 3_000_000),
		planExp("Гуми", "2026-09-01", "", domain.PaidFromPlan, 800_000),
	)

	p := buildMonthPlan(src, fx.Rates{}, today, 0, 0, "")
	if p.PlannedUAH != 30000 {
		t.Errorf("планові витрати місяця %v, очікували 30000 — вересневі гуми в липні не тиснуть", p.PlannedUAH)
	}
	if p.PlanUAH != 10000 { // 40 000 − 30 000
		t.Errorf("нетто %v, очікували 10000", p.PlanUAH)
	}
	// А у вересні — навпаки: котел уже позаду, гуми попереду.
	if q := buildMonthPlan(src, fx.Rates{}, today, 2, 0, ""); q.PlannedUAH != 8000 {
		t.Errorf("вересень: планові витрати %v, очікували 8000", q.PlannedUAH)
	}
}

// ГОЛОВНЕ. Прострочена й далі тисне — і саме на ПОТОЧНИЙ місяць, а не на
// свій. Разовий потік у цьому місці дає нуль, і це та різниця, заради якої
// заведено окрему таблицю 0056.
func TestMonthPlanOverduePlannedStillPresses(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	src := plannedSrc(now, planExp("Страховка", "2026-05-14", "", domain.PaidFromPlan, 1_200_000))

	p := buildMonthPlan(src, fx.Rates{}, today, 0, 0, "")
	if p.PlannedUAH != 12000 {
		t.Errorf("прострочена дала %v, а мусить тиснути на поточний місяць своїми 12000: "+
			"травень уже прожито, а зникнувши, вона перестала б вимагати грошей, "+
			"яких далі вимагає", p.PlannedUAH)
	}
	// І тисне РІВНО ОДИН РАЗ: у наступному місяці її вже немає.
	if q := buildMonthPlan(src, fx.Rates{}, today, 1, 0, ""); q.PlannedUAH != 0 {
		t.Errorf("серпень: прострочена тисне вдруге на %v", q.PlannedUAH)
	}
}

func TestMonthPlanPaidPlannedGone(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	src := plannedSrc(now, planExp("Котел", "2026-07-10", "2026-07-11", domain.PaidFromPlan, 3_000_000))

	p := buildMonthPlan(src, fx.Rates{}, today, 0, 0, "")
	if p.PlannedUAH != 0 {
		t.Errorf("сплачена тисне на %v — гроші вже пішли", p.PlannedUAH)
	}
	if p.PlanUAH != 40000 {
		t.Errorf("нетто %v, очікували 40000", p.PlanUAH)
	}
}

// СТОРОЖ ПРОТИ ПОДВІЙНОГО РАХУНКУ. Витрата з картки не має права чіпати
// план місяця: вона живе в другому контурі (buildDebtExit), і віднята в
// обох, забрала б удвічі більше, ніж коштує. Дзеркальний сторож стоїть у
// TestDebtExitPlanPlannedDoesNotTouchCap.
func TestMonthPlanCardPlannedDoesNotTouchPlan(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	src := plannedSrc(now, planExp("Котел", "2026-07-20", "", domain.PaidFromCard, 3_000_000))

	p := buildMonthPlan(src, fx.Rates{}, today, 0, 0, "")
	if p.PlannedUAH != 0 {
		t.Errorf("карткова витрата зайшла в план місяця на %v", p.PlannedUAH)
	}
	if p.PlanUAH != 40000 {
		t.Errorf("нетто %v, очікували 40000 — карткова витрата плану не стосується", p.PlanUAH)
	}
}

// Планова витрата ріже ВСІ ТРИ дозволені числа повністю, а не пропорційно
// — той самий довід, що для витратного потоку: рознести її між дозволеними
// й недозволеними доходами можна лише вигаданим правилом.
func TestMonthPlanPlannedCutsAllThreeAllowances(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	src := monthPlanSrc([]store.PlanFlow{
		{ID: 1, Name: "Зарплата", Kind: "income", Amount: 4_000_000, Currency: money.UAH,
			Cadence: "month", FromDate: domain.NewDate(now.AddDate(0, 0, -30)), InvestBP: 10000},
	}, nil, nil)
	src.planExpenses = []domain.PlanExpense{
		planExp("Котел", "2026-07-20", "", domain.PaidFromPlan, 3_000_000),
	}

	p := buildMonthPlan(src, fx.Rates{}, today, 0, 0, "")
	for _, c := range []struct {
		name string
		got  float64
	}{
		{"подушці", p.PlanReserveUAH},
		{"цілям", p.PlanGoalsUAH},
		{"боргу", p.PlanDebtUAH},
	} {
		if c.got != 10000 {
			t.Errorf("дозволено %s %v, очікували 10000 — витрата ріже кожен кошик повністю", c.name, c.got)
		}
	}
}

// Місяць звірки картки: витрата, чий день НЕ пізніший за звірку, у
// балансі вже сидить. Але прострочена — ні, хай яка стара її дата: її не
// сплатили, отже в балансі її немає за визначенням.
func TestMonthPlanPlannedRespectsAfterFilter(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	src := plannedSrc(now,
		planExp("Рано", "2026-07-10", "", domain.PaidFromPlan, 500_000),  // до звірки, і вже прострочена
		planExp("Пізно", "2026-07-25", "", domain.PaidFromPlan, 700_000), // після звірки
	)

	p := buildMonthPlan(src, fx.Rates{}, today, 0, 0, "2026-07-18")
	// 5 000 прострочені (фільтр їх не бере) + 7 000 попереду = 12 000.
	if p.PlannedUAH != 12000 {
		t.Errorf("у місяці звірки %v, очікували 12000: прострочену фільтр не відсікає ніколи, "+
			"бо в балансі звірки її немає — її ж не сплатили", p.PlannedUAH)
	}

	// А сплачена до звірки справді зникає: гроші пішли й уже в мінусі.
	src2 := plannedSrc(now, planExp("Рано", "2026-07-10", "2026-07-10", domain.PaidFromPlan, 500_000))
	if q := buildMonthPlan(src2, fx.Rates{}, today, 0, 0, "2026-07-18"); q.PlannedUAH != 0 {
		t.Errorf("сплачена до звірки тисне на %v — вона вже у виміряному балансі", q.PlannedUAH)
	}
}
