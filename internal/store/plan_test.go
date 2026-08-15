package store

import (
	"context"
	"errors"
	"testing"
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
