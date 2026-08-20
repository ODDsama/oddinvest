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
	// Зняття при цьому чесне: гроші справді пішли з рахунку брокера, і
	// картка «знято цього місяця» має це показати.
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

	p := buildMonthPlan(src, fx.Rates{}, today, 0, 0)
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
	}, nil), fx.Rates{}, today, 0, 0)
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
	}, nil), fx.Rates{}, today, 0, 0)
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
func TestMonthPlanLeftAndReserve(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	exp, months, share := 10000.0, 6.0, 40.0
	set := &state.SettingsDoc{
		MonthlyExpensesUAH: &exp, ReserveTargetMonths: &months, ReserveFillSharePct: &share,
	}
	flows := []store.PlanFlow{
		{ID: 1, Name: "Зарплата", Kind: "income", Amount: 3_000_000, Currency: money.UAH,
			Cadence: "month", FromDate: domain.NewDate(now.AddDate(0, 0, -30)), InvestBP: 10000},
	}
	// Резерв 0 при цілі 60 000 — розрив величезний, тож стелю нічим не
	// обрізати: 40% від 30 000.
	p := buildMonthPlan(monthPlanSrc(flows, nil, set), fx.Rates{}, today, 0, 12000)
	if p.ReserveUAH != 12000 {
		t.Errorf("у резерв %v, очікували 12000 (40%% від 30 000)", p.ReserveUAH)
	}
	if p.LeftUAH != 18000 { // 30 000 − 12 000 уже внесених
		t.Errorf("лишилось %v, очікували 18000", p.LeftUAH)
	}
	// Той самий план, але резерв уже майже зібраний: стелю обрізає розрив.
	p = buildMonthPlan(monthPlanSrc(flows, nil, set), fx.Rates{}, today, 59_000, 0)
	if p.ReserveUAH != 1000 {
		t.Errorf("у резерв %v, очікували 1000 — більше за розрив не кладуть", p.ReserveUAH)
	}
	// Перевиконаний план: нуль, а не від'ємне число.
	p = buildMonthPlan(monthPlanSrc(flows, nil, set), fx.Rates{}, today, 0, 40000)
	if p.LeftUAH != 0 {
		t.Errorf("лишилось %v, очікували 0 — план місяця перевиконано", p.LeftUAH)
	}
}

// TestMonthPlanAbsentWithoutFlows — без плану доходу блока немає ВЗАГАЛІ.
//
// Саме nil, а не нулі: «план обіцяє нуль» і «плану немає» — різні речі, і
// UI гілкується на наявність блока.
func TestMonthPlanAbsentWithoutFlows(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	if p := buildMonthPlan(monthPlanSrc(nil, nil, nil), fx.Rates{},
		domain.NewDate(now), 0, 0); p != nil {
		t.Errorf("план місяця %+v, очікували nil — джерел доходу немає", p)
	}
}
