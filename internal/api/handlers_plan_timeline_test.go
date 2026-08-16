package api

import (
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/store"
)

// Минулий рік помісячно: план розгортається з таблиці потоків назад, факт
// береться з реальних поповнень, нестача — зі знімка того місяця.
//
// Набір навмисно відтворює сценарій кнопки «⇗»: стара зарплата закрита
// датою в травні, нова заведена з червня. Саме на ньому й видно, чи не
// переписує застосунок минуле — доти дата «до» стирала потік із минулого
// цілком, і квітень показав би нуль там, де зарплата справді була.
func TestBuildPlanHistory(t *testing.T) {
	today := domain.Date("2026-08-15")
	rates := fx.Rates{}

	flows := []store.PlanFlow{
		{Name: "стара зарплата", Kind: "income", Amount: 3_000_000, Currency: "UAH",
			Cadence: "month", FromDate: "2025-03-10", UntilDate: "2026-05-20", InvestBP: 10000},
		{Name: "нова зарплата", Kind: "income", Amount: 4_000_000, Currency: "UAH",
			Cadence: "month", FromDate: "2026-06-01", InvestBP: 10000},
	}
	deposits := []store.Deposit{
		{Date: "2026-04-10", Amount: 2_000_000, Currency: "UAH"},
		{Date: "2026-07-05", Amount: 5_000_000, Currency: "UAH"},
		// Зняття того ж місяця: факт міряється НЕТТО, інакше переказ між
		// брокерами (зняття + поповнення) роздував би його на свою суму.
		{Date: "2026-07-20", Amount: -1_000_000, Currency: "UAH"},
		// Поза вікном — не мусить потрапити нікуди.
		{Date: "2024-01-10", Amount: 9_900_000, Currency: "UAH"},
	}
	reserve := []store.ReserveOp{
		{Date: "2026-07-25", Amount: 500_000, Currency: "UAH"},
	}
	snaps := []store.Snapshot{
		{Date: "2026-07-10", MonthTargetUAH: 700_000},
		// Останній знімок місяця виграє: це цифра, найближча до підсумку.
		{Date: "2026-07-31", MonthTargetUAH: 900_000},
	}

	got := buildPlanHistory(flows, deposits, reserve, snaps, nil, nil, today, rates)
	if len(got) != planHistoryMonths {
		t.Fatalf("мало бути %d місяців, маємо %d", planHistoryMonths, len(got))
	}
	if got[0].Month != "2025-08" || got[len(got)-1].Month != "2026-07" {
		t.Fatalf("вікно не те: %s … %s", got[0].Month, got[len(got)-1].Month)
	}

	byMonth := map[string]planHistoryPoint{}
	for _, p := range got {
		byMonth[p.Month] = p
	}
	for _, c := range []struct {
		month             string
		plan, actual, gap float64
		why               string
	}{
		{"2026-04", 30000, 20000, 0, "стара зарплата ще діяла, було одне поповнення"},
		{"2026-05", 30000, 0, 0, "місяць закриття: закрита датою зарплата за нього ще платить"},
		{"2026-06", 40000, 0, 0, "перший місяць нової зарплати, старої вже немає"},
		{"2026-07", 40000, 45000, 9000, "нетто поповнень плюс резерв; нестача з останнього знімка"},
	} {
		p := byMonth[c.month]
		if p.PlanUAH != c.plan || p.ActualUAH != c.actual || p.GapUAH != c.gap {
			t.Errorf("%s (%s): маємо план=%v факт=%v бракує=%v, чекали %v/%v/%v",
				c.month, c.why, p.PlanUAH, p.ActualUAH, p.GapUAH, c.plan, c.actual, c.gap)
		}
	}
}

// Ряд «надійшло» — четверте число історії, і воно НЕ заміщає план.
//
// Це головна асиметрія фази: у майбутньому відмітка стає планом (там вона
// найкраще відоме про місяць), а в минулому — стоїть поруч із ним. Злити
// їх означало б зробити обидва ряди рівними за побудовою, тобто прибрати
// саме те порівняння, заради якого картка існує.
func TestBuildPlanHistoryReceived(t *testing.T) {
	today := domain.Date("2026-08-15")
	flows := []store.PlanFlow{{
		ID: 1, Name: "зарплата", Kind: "income", Amount: 4_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2025-01-17", InvestBP: 5000,
	}}
	receipts := []store.PlanReceipt{
		// Прийшла третина: вийшов з відпустки, відпрацював кілька днів.
		{FlowID: 1, Month: "2026-06", Amount: 1_200_000, Currency: "UAH", InvestBP: 10000},
		// Не прийшло нічого — записаний нуль.
		{FlowID: 1, Month: "2026-07", Amount: 0, Currency: "UAH", InvestBP: 10000},
		// Позапланова премія, зі своєю часткою в портфель.
		{FlowID: 0, Month: "2026-07", Name: "Премія", Amount: 1_000_000,
			Currency: "UAH", InvestBP: 2000},
	}

	got := buildPlanHistory(flows, nil, nil, nil, nil, receipts, today, fx.Rates{})
	byMonth := map[string]planHistoryPoint{}
	for _, p := range got {
		byMonth[p.Month] = p
	}

	// План скрізь той самий: 40 000 × 50% = 20 000. Відмітка його не чіпає.
	for _, m := range []string{"2026-05", "2026-06", "2026-07"} {
		if p := byMonth[m]; p.PlanUAH != 20000 {
			t.Errorf("%s: план мав лишитись 20000, маємо %v", m, p.PlanUAH)
		}
	}
	// Червень: 12 000 × 50% (частка ПОТОКУ) = 6 000.
	if p := byMonth["2026-06"]; !p.Marked || p.ReceivedUAH != 6000 {
		t.Errorf("червень: чекали відмічені 6000, маємо marked=%v %v", p.Marked, p.ReceivedUAH)
	}
	// Липень: зарплата 0 плюс премія 10 000 × 20% (частка ВІДМІТКИ) = 2 000.
	if p := byMonth["2026-07"]; !p.Marked || p.ReceivedUAH != 2000 {
		t.Errorf("липень: чекали відмічені 2000, маємо marked=%v %v", p.Marked, p.ReceivedUAH)
	}
	// Травень не відмічали: нуль тут означає «не відмічено», і прапорець
	// мусить це сказати — інакше графік намалював би провалений місяць.
	if p := byMonth["2026-05"]; p.Marked || p.ReceivedUAH != 0 {
		t.Errorf("травень мав лишитись невідміченим, маємо marked=%v %v", p.Marked, p.ReceivedUAH)
	}
}

// Порожній початок відрізається: місяці до появи і плану, і поповнень — це
// «застосунком тоді ще не користувались», а не провалений план. Менше двох
// таких місяців — картки немає взагалі.
func TestBuildPlanHistoryTrimsEmptyHead(t *testing.T) {
	today := domain.Date("2026-08-15")
	flows := []store.PlanFlow{{
		Name: "зарплата", Kind: "income", Amount: 1_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-05-01", InvestBP: 10000,
	}}

	got := buildPlanHistory(flows, nil, nil, nil, nil, nil, today, fx.Rates{})
	if len(got) != 3 {
		t.Fatalf("мали лишитись травень-липень, маємо %d місяців: %+v", len(got), got)
	}
	if got[0].Month != "2026-05" {
		t.Errorf("перший місяць мав бути 2026-05, маємо %s", got[0].Month)
	}

	// Один місяць історії порівнювати нема з чим.
	short := []store.PlanFlow{{
		Name: "зарплата", Kind: "income", Amount: 1_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-07-01", InvestBP: 10000,
	}}
	if got := buildPlanHistory(short, nil, nil, nil, nil, nil, today, fx.Rates{}); got != nil {
		t.Errorf("на одному місяці історії картки не мало бути, маємо %+v", got)
	}
	if got := buildPlanHistory(nil, nil, nil, nil, nil, nil, today, fx.Rates{}); got != nil {
		t.Errorf("без плану й поповнень мав бути nil, маємо %+v", got)
	}
}

// Ключ місяця не має права з'їхати на переповненні дат: Date.AddMonths дає
// 31 березня − 1 міс = 3 березня, і побудований на ньому ключ показав би
// березень двічі, а лютий не показав би взагалі.
func TestMonthKeyAtSurvivesMonthEnds(t *testing.T) {
	for _, c := range []struct {
		today string
		m     int
		want  string
	}{
		{"2026-03-31", -1, "2026-02"},
		{"2026-03-31", -2, "2026-01"},
		{"2026-01-15", -1, "2025-12"},
		{"2026-01-15", -13, "2024-12"},
		{"2026-12-31", -12, "2025-12"},
	} {
		if got := monthKeyAt(domain.Date(c.today), c.m); got != c.want {
			t.Errorf("monthKeyAt(%s, %d) = %s, чекали %s", c.today, c.m, got, c.want)
		}
	}
}

// planAsOf — план таким, яким він БУВ, а не яким став.
func TestPlanAsOf(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	rev := func(ts string, id int64, op string, amount int64) store.PlanFlowRevision {
		return store.PlanFlowRevision{
			FlowID: id, ChangedAt: at(ts), Op: op,
			Flow: store.PlanFlow{ID: id, Name: "потік", Kind: "income", Amount: amount,
				Currency: "UAH", Cadence: "month", FromDate: "2026-01-10", InvestBP: 10000},
		}
	}
	revs := []store.PlanFlowRevision{
		rev("2026-01-10T09:00:00Z", 1, "create", 3_000_000),
		rev("2026-02-05T09:00:00Z", 2, "create", 1_000_000),
		rev("2026-04-20T09:00:00Z", 1, "update", 4_500_000),
		rev("2026-06-01T09:00:00Z", 2, "delete", 1_000_000),
	}

	sum := func(t time.Time) int64 {
		var s int64
		for _, f := range planAsOf(revs, t) {
			s += f.Amount
		}
		return s
	}
	for _, c := range []struct {
		when string
		want int64
		why  string
	}{
		{"2026-01-01T00:00:00Z", 0, "до першої ревізії плану ще немає"},
		{"2026-01-31T23:59:59Z", 3_000_000, "лише перший потік"},
		{"2026-03-31T23:59:59Z", 4_000_000, "обидва потоки, стара сума"},
		{"2026-04-30T23:59:59Z", 5_500_000, "після правки — нова сума"},
		{"2026-06-30T23:59:59Z", 4_500_000, "видалений потік не воскресає"},
	} {
		if got := sum(at(c.when)); got != c.want {
			t.Errorf("%s (%s): маємо %d, чекали %d", c.when, c.why, got, c.want)
		}
	}
}

// Головна властивість журналу: правка суми НЕ переписує минуле.
//
// Доти той самий набір дав би 6 000 ₴ у кожному місяці — і березень
// виглядав би так, ніби зарплата завжди була такою.
func TestBuildPlanHistoryReadsJournalNotTodaysTable(t *testing.T) {
	today := domain.Date("2026-08-15")
	// Теперішній стан таблиці: 6 000 ₴. Так її бачить UI сьогодні.
	flows := []store.PlanFlow{{
		ID: 1, Name: "Зарплата", Kind: "income", Amount: 600_000, Currency: "UAH",
		Cadence: "month", FromDate: "2025-10-05", InvestBP: 10000,
	}}
	old := flows[0]
	old.Amount = 450_000 // а до 20 червня було 4 500 ₴
	// Журнал з'явився пізніше за сам потік — як воно й буде після міграції.
	revs := []store.PlanFlowRevision{
		{FlowID: 1, ChangedAt: time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC),
			Op: "seed", Flow: old},
		{FlowID: 1, ChangedAt: time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC),
			Op: "update", Flow: flows[0]},
	}

	got := buildPlanHistory(flows, nil, nil, nil, revs, nil, today, fx.Rates{})
	byMonth := map[string]planHistoryPoint{}
	for _, p := range got {
		byMonth[p.Month] = p
	}
	for _, c := range []struct {
		month   string
		plan    float64
		derived bool
		why     string
	}{
		{"2025-12", 6000, true, "до журналу — виводиться з теперішньої таблиці, і позначено"},
		{"2026-01", 4500, false, "перший місяць журналу — уже тодішня сума"},
		{"2026-03", 4500, false, "читається з журналу: тодішня сума"},
		{"2026-05", 4500, false, "останній місяць старої суми"},
		{"2026-06", 6000, false, "місяць правки — уже нова"},
		{"2026-07", 6000, false, "після правки"},
	} {
		p := byMonth[c.month]
		if p.PlanUAH != c.plan || p.PlanDerived != c.derived {
			t.Errorf("%s (%s): маємо план=%v derived=%v, чекали %v/%v",
				c.month, c.why, p.PlanUAH, p.PlanDerived, c.plan, c.derived)
		}
	}
}
