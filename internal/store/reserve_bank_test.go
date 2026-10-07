package store

import (
	"context"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Установа руху резерву й цілі (0069): посилання, а не текст. Перевіряється
// весь шлях — запис, читання, правка в «без установи», бекап і відмова
// видалити брокера, на якого посилається лише рух подушки.
func TestReserveAndGoalOpBank(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	rid, err := s.AddReserveOp(ctx, ReserveOp{Date: "2026-07-01", Amount: 10000, Currency: "EUR", Bank: "mono"})
	if err != nil {
		t.Fatal(err)
	}
	gid, err := s.AddGoal(ctx, Goal{Name: "авто", TargetAmount: 100000, Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddGoalOp(ctx, GoalOp{GoalID: gid, Date: "2026-07-02", Amount: 5000,
		Currency: "UAH", Bank: "mono", Place: "картка"}); err != nil {
		t.Fatal(err)
	}

	ops, err := s.ListReserveOps(ctx)
	if err != nil || len(ops) != 1 || ops[0].Bank != "mono" {
		t.Fatalf("рух резерву: %+v %v", ops, err)
	}
	gops, err := s.ListGoalOps(ctx)
	if err != nil || len(gops) != 1 || gops[0].Bank != "mono" || gops[0].Place != "картка" {
		t.Fatalf("рух цілі: %+v %v", gops, err)
	}

	brokers, err := s.ListBrokers(ctx)
	if err != nil || len(brokers) != 1 {
		t.Fatalf("брокер mono мав завестись сам: %+v %v", brokers, err)
	}
	if err := s.DeleteBroker(ctx, brokers[0].ID); err == nil {
		t.Fatal("брокера з рухами резерву й цілі видалено — посилання зависли б")
	}

	b, err := s.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fresh := openTest(t)
	if err := fresh.ImportAll(ctx, b); err != nil {
		t.Fatal(err)
	}
	if ops, _ := fresh.ListReserveOps(ctx); len(ops) != 1 || ops[0].Bank != "mono" {
		t.Errorf("бекап загубив установу резерву: %+v", ops)
	}
	if gops, _ := fresh.ListGoalOps(ctx); len(gops) != 1 || gops[0].Bank != "mono" {
		t.Errorf("бекап загубив установу цілі: %+v", gops)
	}

	// Правка в «без установи» — готівка: посилання знімається.
	op := ops[0]
	op.ID, op.Bank, op.Place = rid, "", "сейф"
	if err := s.UpdateReserveOp(ctx, op); err != nil {
		t.Fatal(err)
	}
	if ops, _ := s.ListReserveOps(ctx); ops[0].Bank != "" || ops[0].Place != "сейф" || ops[0].Date != domain.Date("2026-07-01") {
		t.Errorf("правка в готівку: %+v", ops[0])
	}
}
