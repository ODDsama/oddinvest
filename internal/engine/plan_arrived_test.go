package engine

import (
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/store"
)

// Правка розподілу — правило для МАЙБУТНІХ надходжень.
//
// Сценарій власника, один на всі тести нижче: 25 листопада він змінює в
// потоці частку в портфель (або дозволені кошики). Питання щоразу те саме —
// чи рухається від цього місяць, у якому надходження вже прийшло.
//
// Миті ревізій стоять опівдні й за добу від платіжних днів: кінець
// платіжного дня рахується в зоні процесу, і тест не має залежати від того,
// у якій зоні його запустили.

var (
	arrivedToday = domain.Date("2026-11-25")
	arrivedSeed  = time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	arrivedEdit  = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
)

// arrivedFlow — місячний дохід 40 000 ₴ із платіжним днем day.
func arrivedFlow(id int64, day string, investBP int64, uses string) store.PlanFlow {
	return store.PlanFlow{
		ID: id, Name: "Зарплата", Kind: "income", Amount: 4_000_000, Currency: "UAH",
		Cadence: "month", FromDate: domain.Date("2026-01-" + day), InvestBP: investBP, Uses: uses,
	}
}

// arrivedJournal — «було was, 25 листопада стало now».
func arrivedJournal(was, now store.PlanFlow) []store.PlanFlowRevision {
	return []store.PlanFlowRevision{
		{FlowID: was.ID, ChangedAt: arrivedSeed, Op: "seed", Flow: was},
		{FlowID: now.ID, ChangedAt: arrivedEdit, Op: "update", Flow: now},
	}
}

func arrivedPlan(t *testing.T, src *sources, m int) float64 {
	t.Helper()
	p := buildMonthPlan(src, fx.Rates{}, arrivedToday, m, 0, "")
	if p == nil {
		t.Fatal("план місяця не порахувався")
	}
	return p.IncomeUAH.Major()
}

// Той самий баг, яким його спіймав власник: зарплата прийшла 5-го, частку
// змінено 25-го — ціль листопада мусить лишитись на старій частці.
func TestMonthPlanKeepsShareOfArrivedIncome(t *testing.T) {
	was := arrivedFlow(1, "05", 5000, "")
	now := arrivedFlow(1, "05", 2500, "")
	src := &sources{planFlows: []store.PlanFlow{now}, planRevs: arrivedJournal(was, now)}

	if got := arrivedPlan(t, src, 0); got != 20000 {
		t.Errorf("листопад %v, чекали 20000: зарплата вже прийшла за часткою 50%%", got)
	}
	// Грудень — уже за новим правилом: саме заради нього частку й міняли.
	if got := arrivedPlan(t, src, 1); got != 10000 {
		t.Errorf("грудень %v, чекали 10000: нова частка діє на майбутні надходження", got)
	}
}

// Надходження, яке ще попереду, бере нову частку одразу — інакше правка
// «з наступної зарплати» не діяла б до кінця місяця.
func TestMonthPlanAppliesNewShareToIncomeAhead(t *testing.T) {
	was := arrivedFlow(1, "28", 5000, "")
	now := arrivedFlow(1, "28", 2500, "")
	src := &sources{planFlows: []store.PlanFlow{now}, planRevs: arrivedJournal(was, now)}

	if got := arrivedPlan(t, src, 0); got != 10000 {
		t.Errorf("листопад %v, чекали 10000: зарплата 28-го ще не прийшла", got)
	}
}

// Половина місяця прийшла, половина ні: правка змінює рівно ту половину,
// що попереду. Це й є «по кожному надходженню», а не «місяць цілком».
func TestMonthPlanSplitsArrivedAndAhead(t *testing.T) {
	wasA, nowA := arrivedFlow(1, "05", 5000, ""), arrivedFlow(1, "05", 2500, "")
	wasB, nowB := arrivedFlow(2, "28", 5000, ""), arrivedFlow(2, "28", 2500, "")
	src := &sources{
		planFlows: []store.PlanFlow{nowA, nowB},
		planRevs: []store.PlanFlowRevision{
			{FlowID: 1, ChangedAt: arrivedSeed, Op: "seed", Flow: wasA},
			{FlowID: 2, ChangedAt: arrivedSeed, Op: "seed", Flow: wasB},
			{FlowID: 1, ChangedAt: arrivedEdit, Op: "update", Flow: nowA},
			{FlowID: 2, ChangedAt: arrivedEdit, Op: "update", Flow: nowB},
		},
	}
	if got := arrivedPlan(t, src, 0); got != 30000 {
		t.Errorf("листопад %v, чекали 30000 = 20000 отриманих за старою + 10000 за новою", got)
	}
}

// Дозвіл тримається того самого правила, що й частка: гроші, які прийшли,
// коли подушці було можна, лишаються дозволеними подушці.
func TestMonthPlanKeepsUsesOfArrivedIncome(t *testing.T) {
	was := arrivedFlow(1, "05", 10000, "")
	now := arrivedFlow(1, "05", 10000, domain.UsePlanInvest)
	src := &sources{planFlows: []store.PlanFlow{now}, planRevs: arrivedJournal(was, now)}

	p := buildMonthPlan(src, fx.Rates{}, arrivedToday, 0, 0, "")
	if p.PlanReserveUAH.Major() != 40000 || p.PlanGoalsUAH.Major() != 40000 {
		t.Errorf("дозвіл подушці %v і цілям %v, чекали 40000 і 40000: заборону поставили ПІСЛЯ приходу",
			p.PlanReserveUAH.Major(), p.PlanGoalsUAH.Major())
	}
	if next := buildMonthPlan(src, fx.Rates{}, arrivedToday, 1, 0, ""); next.PlanReserveUAH.Major() != 0 {
		t.Errorf("грудень дозволяє подушці %v, чекали 0: заборона діє на майбутнє", next.PlanReserveUAH.Major())
	}
}

// Зарплата прийшла раніше свого дня й одразу відмічена: отримана вона
// тоді, коли її відмітили, і правка наступного дня її не чіпає. Заради
// цього випадку й існує plan_receipts.marked_at (0071).
func TestMonthPlanKeepsShareOfIncomeMarkedEarly(t *testing.T) {
	was := arrivedFlow(1, "28", 5000, "")
	now := arrivedFlow(1, "28", 2500, "")
	mark := store.PlanReceipt{FlowID: 1, Month: "2026-11", Amount: 4_000_000, Currency: "UAH",
		MarkedAt: time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)}
	src := &sources{planFlows: []store.PlanFlow{now}, planRevs: arrivedJournal(was, now),
		planReceipts: []store.PlanReceipt{mark}}

	if got := arrivedPlan(t, src, 0); got != 20000 {
		t.Errorf("листопад %v, чекали 20000: відмічено 24-го, частку змінено 25-го", got)
	}

	// Відмітка, старша за колонку, миті не має — і падає на кінець
	// платіжного дня, який ще попереду. Тобто поводиться як до 0071.
	mark.MarkedAt = time.Time{}
	src.planReceipts = []store.PlanReceipt{mark}
	if got := arrivedPlan(t, src, 0); got != 10000 {
		t.Errorf("листопад %v, чекали 10000: мить відмітки невідома, платіжний день попереду", got)
	}
}

// Без журналу не міняється нічого — на цьому стоять усі тести плану
// місяця, що збирають джерела без ревізій.
func TestMonthPlanWithoutJournalReadsTable(t *testing.T) {
	src := &sources{planFlows: []store.PlanFlow{arrivedFlow(1, "05", 2500, "")}}
	if got := arrivedPlan(t, src, 0); got != 10000 {
		t.Errorf("листопад %v, чекали 10000 з теперішньої таблиці", got)
	}
}

// Маршрут ділить нетто місяця вагами потоків, і вага ноги, яку він уже не
// веде, мусить бути тим самим доданком, із якого складено план. Нова
// частка тут дала б поділ, що не сходиться з власним підсумком.
func TestPlanPartsWeighArrivedIncomeByOldShare(t *testing.T) {
	was := arrivedFlow(1, "05", 5000, "")
	now := arrivedFlow(1, "05", 2500, domain.UsePlanInvest)
	src := &sources{planFlows: []store.PlanFlow{now}, planRevs: arrivedJournal(was, now)}

	parts := planParts(src, nil, arrivedToday, 0, "2026-11")
	if len(parts) != 1 {
		t.Fatalf("ніг %d, чекали одну: %+v", len(parts), parts)
	}
	if p := parts[0]; !p.skip || p.weight != 20000 || p.uses != "" {
		t.Errorf("нога %+v, чекали пропущену з вагою 20000 і старим дозволом", p)
	}
}

// Історія читає потік на мить ЙОГО надходження, а не на кінець місяця.
//
// Доти правка 25-го переписувала в «Плані проти факту» зарплату, що
// прийшла 5-го: листопадовий стовпчик у грудні показував би нову частку й
// розходився б із плиткою, яку показував сам листопад.
func TestBuildPlanHistoryReadsFlowAsItArrived(t *testing.T) {
	today := domain.Date("2026-12-15")
	was := arrivedFlow(1, "05", 5000, "")
	now := arrivedFlow(1, "05", 2500, "")
	// Жовтень відмічено заднім числом, уже ПІСЛЯ правки: отриманий він
	// однаково в жовтні, і листопадова ревізія йому не указ.
	receipts := []store.PlanReceipt{{FlowID: 1, Month: "2026-10", Amount: 4_000_000,
		Currency: "UAH", MarkedAt: time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)}}

	got := buildPlanHistory([]store.PlanFlow{now}, nil, nil, arrivedJournal(was, now),
		receipts, today, fx.Rates{})
	byMonth := map[string]PlanHistoryPoint{}
	for _, p := range got {
		byMonth[p.Month] = p
	}
	if p := byMonth["2026-11"]; p.PlanUAH.Major() != 20000 {
		t.Errorf("листопад %v, чекали 20000: зарплата прийшла 5-го, до правки 25-го", p.PlanUAH.Major())
	}
	if p := byMonth["2026-10"]; p.PlanUAH.Major() != 20000 || p.ReceivedUAH.Major() != 20000 {
		t.Errorf("жовтень план=%v отримано=%v, чекали 20000 і 20000: відмітка заднім числом не тягне пізнішу ревізію",
			p.PlanUAH.Major(), p.ReceivedUAH.Major())
	}
}
