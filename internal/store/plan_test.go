package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPlanReceiptRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, err := s.AddPlanReceipt(ctx, PlanReceipt{
		FlowID: 7, Month: "2026-05", Name: "Зарплата", Amount: 3200000,
		Currency: "UAH", InvestBP: 10000, Note: "відпустка",
	})
	if err != nil {
		t.Fatal(err)
	}
	rs, err := s.ListPlanReceipts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].ID != id || rs[0].Month != "2026-05" || rs[0].Note != "відпустка" {
		t.Fatalf("round-trip: %+v", rs)
	}

	rs[0].Amount = 0
	rs[0].Note = "не прийшло"
	if err := s.UpdatePlanReceipt(ctx, rs[0]); err != nil {
		t.Fatal(err)
	}
	rs, _ = s.ListPlanReceipts(ctx)
	if rs[0].Amount != 0 || rs[0].Note != "не прийшло" {
		t.Fatalf("update не застосувався: %+v", rs[0])
	}

	if err := s.DeletePlanReceipt(ctx, id); err != nil {
		t.Fatal(err)
	}
	if rs, _ = s.ListPlanReceipts(ctx); len(rs) != 0 {
		t.Fatalf("очікували порожньо після видалення, маємо %+v", rs)
	}
	if err := s.DeletePlanReceipt(ctx, 42); !errors.Is(err, ErrNotFound) {
		t.Errorf("видалення неіснуючої відмітки мало дати ErrNotFound, маємо %v", err)
	}
}

// Одне джерело — одна відмітка на місяць, а «іншого» скільки завгодно.
//
// Часткова унікальність тут не косметика: без неї подвійний клік по «✓
// прийшло» записав би зарплату двічі, і місяць тихо показав би подвійне
// надходження. Друга половина правила так само потрібна: премія й
// подарунок у тому самому серпні — два різні факти.
func TestPlanReceiptOnePerFlowPerMonth(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	rec := PlanReceipt{FlowID: 7, Month: "2026-05", Amount: 100, Currency: "UAH", InvestBP: 10000}
	if _, err := s.AddPlanReceipt(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPlanReceipt(ctx, rec); !errors.Is(err, ErrConflict) {
		t.Errorf("друга відмітка того самого джерела мала дати ErrConflict, маємо %v", err)
	}
	// Інший місяць того самого джерела — не конфлікт.
	rec.Month = "2026-06"
	if _, err := s.AddPlanReceipt(ctx, rec); err != nil {
		t.Errorf("інший місяць мав пройти: %v", err)
	}
	// Позапланове (flow_id = 0) під частковий індекс не підпадає.
	other := PlanReceipt{FlowID: 0, Month: "2026-05", Name: "Премія", Amount: 500,
		Currency: "UAH", InvestBP: 10000}
	if _, err := s.AddPlanReceipt(ctx, other); err != nil {
		t.Fatal(err)
	}
	other.Name = "Подарунок"
	if _, err := s.AddPlanReceipt(ctx, other); err != nil {
		t.Errorf("друге позапланове в тому ж місяці мало пройти: %v", err)
	}
}

func TestPlanFlowRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, err := s.AddPlanFlow(ctx, PlanFlow{
		Name: "Зарплата", Kind: "income", Amount: 4000000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-09-01", GrowthBP: 500, InvestBP: 3000,
	})
	if err != nil {
		t.Fatal(err)
	}
	flows, err := s.ListPlanFlows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 1 || flows[0].ID != id || flows[0].Name != "Зарплата" ||
		flows[0].InvestBP != 3000 || flows[0].UntilDate != "" {
		t.Fatalf("round-trip: %+v", flows)
	}

	flows[0].Amount = 4500000
	if err := s.UpdatePlanFlow(ctx, flows[0]); err != nil {
		t.Fatal(err)
	}
	flows, _ = s.ListPlanFlows(ctx)
	if flows[0].Amount != 4500000 {
		t.Fatalf("update не застосувався: %+v", flows[0])
	}

	if err := s.DeletePlanFlow(ctx, id); err != nil {
		t.Fatal(err)
	}
	flows, _ = s.ListPlanFlows(ctx)
	if len(flows) != 0 {
		t.Fatalf("очікували порожньо після видалення, маємо %+v", flows)
	}
}

func TestPlanFlowUpdateMissingFails(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	err := s.UpdatePlanFlow(ctx, PlanFlow{ID: 999, Name: "х", Kind: "income",
		Amount: 100, Currency: "UAH", Cadence: "once", FromDate: "2026-01-01"})
	if err == nil {
		t.Fatal("очікували помилку на неіснуючому id")
	}
}

func TestPlanActionUpdateMissingFails(t *testing.T) {
	s := openTest(t)
	err := s.UpdatePlanAction(context.Background(),
		PlanAction{ID: 42, Date: "2027-01-01", Type: "set_shares", USDBP: 1000, EURBP: -1})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("правка неіснуючої дії мала дати ErrNotFound, маємо %v", err)
	}
}

// Видалення доти мовчало: DELETE неіснуючого id віддавав успіх, тобто
// «видалено» звучало однаково і тоді, коли видаляти було нічого.
func TestPlanDeleteMissingFails(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.DeletePlanFlow(ctx, 42); !errors.Is(err, ErrNotFound) {
		t.Errorf("видалення неіснуючого потоку мало дати ErrNotFound, маємо %v", err)
	}
	if err := s.DeletePlanAction(ctx, 42); !errors.Is(err, ErrNotFound) {
		t.Errorf("видалення неіснуючої дії мало дати ErrNotFound, маємо %v", err)
	}
}

func TestPlanActionRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	// set_shares: -1 у нового поля означає «не задано», а не нуль.
	id, err := s.AddPlanAction(ctx, PlanAction{
		Date: "2027-01-01", Type: "set_shares", USDBP: 4000, EURBP: -1, Name: "менше долара",
	})
	if err != nil {
		t.Fatal(err)
	}
	actions, err := s.ListPlanActions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].ID != id || actions[0].USDBP != 4000 || actions[0].EURBP != -1 {
		t.Fatalf("round-trip set_shares: %+v", actions)
	}

	// lock: MilTech-подібна дія із замком суми на строк.
	_, err = s.AddPlanAction(ctx, PlanAction{
		Date: "2026-12-01", Type: "lock", USDBP: -1, EURBP: -1,
		Amount: 5000000, Currency: "UAH", RateBP: 2500, Months: 36, Name: "MilTech",
	})
	if err != nil {
		t.Fatal(err)
	}
	actions, _ = s.ListPlanActions(ctx)
	if len(actions) != 2 {
		t.Fatalf("очікували дві дії, маємо %d", len(actions))
	}
	// сортування за датою: MilTech (грудень 2026) раніше за set_shares (січень 2027).
	if actions[0].Name != "MilTech" || actions[1].Name != "менше долара" {
		t.Fatalf("сортування за датою поламане: %+v", actions)
	}

	if err := s.DeletePlanAction(ctx, id); err != nil {
		t.Fatal(err)
	}
	actions, _ = s.ListPlanActions(ctx)
	if len(actions) != 1 {
		t.Fatalf("очікували одну дію після видалення, маємо %d", len(actions))
	}
}

// Журнал (0026): кожна правка лишає рядок, а видалення пам'ятає, ЩО зникло.
//
// Це та властивість, на якій стоїть уся картка «План проти факту»: без
// запису про стан ДО правки місяць до неї довелось би виводити з
// теперішньої таблиці, тобто переписувати минуле.
func TestPlanFlowRevisionsJournal(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	id, err := s.AddPlanFlow(ctx, PlanFlow{
		Name: "Зарплата", Kind: "income", Amount: 4_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-01-17", InvestBP: 10000,
	})
	if err != nil {
		t.Fatal(err)
	}
	flows, _ := s.ListPlanFlows(ctx)
	flows[0].Amount = 4_500_000
	if err := s.UpdatePlanFlow(ctx, flows[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePlanFlow(ctx, id); err != nil {
		t.Fatal(err)
	}

	revs, err := s.ListPlanFlowRevisions(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 3 {
		t.Fatalf("мало бути три ревізії (create/update/delete), маємо %d: %+v", len(revs), revs)
	}
	want := []struct {
		op     string
		amount int64
	}{{"create", 4_000_000}, {"update", 4_500_000}, {"delete", 4_500_000}}
	for i, w := range want {
		if revs[i].Op != w.op || revs[i].Flow.Amount != w.amount {
			t.Errorf("ревізія %d: маємо %s/%d, чекали %s/%d",
				i, revs[i].Op, revs[i].Flow.Amount, w.op, w.amount)
		}
		if revs[i].FlowID != id || revs[i].Flow.Name != "Зарплата" ||
			revs[i].Flow.FromDate != "2026-01-17" {
			t.Errorf("ревізія %d втратила поля потоку: %+v", i, revs[i])
		}
		if revs[i].ChangedAt.IsZero() {
			t.Errorf("ревізія %d без часу", i)
		}
	}

	// Хронологія не спадає — на ній стоїть реконструкція «яким план був».
	for i := 1; i < len(revs); i++ {
		if revs[i].ChangedAt.Before(revs[i-1].ChangedAt) {
			t.Errorf("ревізії вийшли не в хронологічному порядку: %v перед %v",
				revs[i-1].ChangedAt, revs[i].ChangedAt)
		}
	}

	// since відрізає: журнал читається вікном, а не цілком.
	if got, _ := s.ListPlanFlowRevisions(ctx, time.Now().Add(time.Hour)); len(got) != 0 {
		t.Errorf("since у майбутньому мав дати порожньо, маємо %d", len(got))
	}
}

// Невдала мутація не лишає ревізії: рядок журналу й сама правка їдуть
// однією транзакцією, тож «історія показує зміну, якої не сталося»
// неможлива за побудовою.
func TestPlanFlowRevisionsAtomicWithMutation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	if err := s.UpdatePlanFlow(ctx, PlanFlow{ID: 404, Name: "нема", Kind: "income",
		Amount: 100, Currency: "UAH", Cadence: "once", FromDate: "2026-01-01"}); err == nil {
		t.Fatal("правка неіснуючого потоку мала впасти")
	}
	if err := s.DeletePlanFlow(ctx, 404); err == nil {
		t.Fatal("видалення неіснуючого потоку мало впасти")
	}
	revs, _ := s.ListPlanFlowRevisions(ctx, time.Time{})
	if len(revs) != 0 {
		t.Fatalf("невдалі мутації не мали лишити ревізій, маємо %+v", revs)
	}
}

// Дозвіл переживає round-trip і в потоці, і у відмітці, і — головне — у
// ЖУРНАЛІ РЕВІЗІЙ.
//
// Остання третина тут найважливіша. Журнал реконструює план на будь-яку
// дату в минулому, і колонка, якої в ньому немає, відновлюється з
// поточної таблиці — тобто заборона, поставлена сьогодні, заднім числом
// стала б такою, ніби діяла завжди. Той самий довід, що при 0030 для
// призначення, лише тут він гостріший: від дозволу залежить база стелі
// подушки, а не косметична пігулка.
func TestPlanFlowUsesRoundTripAndJournal(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	id, err := s.AddPlanFlow(ctx, PlanFlow{
		Name: "Оренда", Kind: "income", Amount: 1000000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-01-01", InvestBP: 10000, Uses: "invest",
	})
	if err != nil {
		t.Fatal(err)
	}
	flows, err := s.ListPlanFlows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 1 || flows[0].Uses != "invest" {
		t.Fatalf("дозвіл не повернувся: %+v", flows)
	}

	flows[0].Uses = "reserve,invest"
	if err := s.UpdatePlanFlow(ctx, flows[0]); err != nil {
		t.Fatal(err)
	}
	if flows, _ = s.ListPlanFlows(ctx); flows[0].Uses != "reserve,invest" {
		t.Fatalf("правка дозволу не застосувалась: %+v", flows[0])
	}

	revs, err := s.ListPlanFlowRevisions(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 2 {
		t.Fatalf("ревізій %d, чекали 2 (create + update): %+v", len(revs), revs)
	}
	// Саме РІЗНІ значення в двох ревізіях: однакові означали б, що журнал
	// бере дозвіл із сьогоднішньої таблиці, і минуле знову переписуване.
	if revs[0].Flow.Uses != "invest" {
		t.Errorf("ревізія create несе дозвіл %q, чекали invest", revs[0].Flow.Uses)
	}
	if revs[1].Flow.Uses != "reserve,invest" {
		t.Errorf("ревізія update несе дозвіл %q, чекали reserve,invest", revs[1].Flow.Uses)
	}

	// Видалення теж мусить лишити знімок: інакше «що саме заборонялось»
	// зникло б разом із рядком.
	if err := s.DeletePlanFlow(ctx, id); err != nil {
		t.Fatal(err)
	}
	revs, _ = s.ListPlanFlowRevisions(ctx, time.Time{})
	if last := revs[len(revs)-1]; last.Op != "delete" || last.Flow.Uses != "reserve,invest" {
		t.Errorf("ревізія delete: op=%q дозвіл=%q", last.Op, last.Flow.Uses)
	}
}

// У «іншого» дозвіл власний, і сховище має його берегти: потоку за ним
// немає, тож успадкувати нема від кого.
func TestPlanReceiptUsesRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.AddPlanReceipt(ctx, PlanReceipt{
		FlowID: 0, Month: "2026-05", Name: "Премія", Amount: 500000,
		Currency: "UAH", InvestBP: 10000, Uses: "goals",
	}); err != nil {
		t.Fatal(err)
	}
	rs, err := s.ListPlanReceipts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Uses != "goals" {
		t.Fatalf("дозвіл відмітки не повернувся: %+v", rs)
	}
}
