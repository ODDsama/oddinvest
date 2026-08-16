package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

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
