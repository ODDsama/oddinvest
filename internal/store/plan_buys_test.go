package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func openTmp(t *testing.T, name string) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPlanBuysCRUD(t *testing.T) {
	ctx := context.Background()
	s := openTmp(t, "buys.db")

	id, err := s.AddPlanBuy(ctx, PlanBuy{
		Kind: BuyBond, Ref: "UA4000227656", Qty: 3, Broker: "mono", Note: "під драбину",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Порожня дата сортується першою — «зараз» справді раніше за будь-яку
	// заплановану дату, і на цьому стоїть порядок у таблиці.
	later, err := s.AddPlanBuy(ctx, PlanBuy{
		Kind: BuyDeposit, Ref: "privat", Amount: 5000000, Currency: "UAH",
		BuyDate: "2027-03-01", RateBP: 1450, Months: 12, IsReserve: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ListPlanBuys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("маємо %d рядків замість 2", len(got))
	}
	if got[0].ID != id || got[1].ID != later {
		t.Errorf("порядок не за датою: %d, %d", got[0].ID, got[1].ID)
	}
	if !got[1].IsReserve || got[1].RateBP != 1450 || got[1].Months != 12 {
		t.Errorf("вклад поїхав: %+v", got[1])
	}

	// Правка міняє й ВИД: передумати й замість паперу взяти фонд — це та
	// сама планована купівля, а не нова.
	upd := got[0]
	upd.Kind, upd.Ref, upd.Qty, upd.UnitPrice, upd.Currency = BuyFund, "Inzhur MilTech", 2, 105000, "UAH"
	if err := s.UpdatePlanBuy(ctx, upd); err != nil {
		t.Fatal(err)
	}
	got, err = s.ListPlanBuys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Kind != BuyFund || got[0].UnitPrice != 105000 || got[0].ID != id {
		t.Errorf("правка не зберегла id або не змінила вид: %+v", got[0])
	}

	if err := s.DeletePlanBuy(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePlanBuy(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("повторне видалення мало дати ErrNotFound, маємо %v", err)
	}
	if err := s.UpdatePlanBuy(ctx, upd); !errors.Is(err, ErrNotFound) {
		t.Errorf("правка видаленого мала дати ErrNotFound, маємо %v", err)
	}
}

// Таблиця, забута в бекапі, губиться тихо — і саме тому цей тест ганяє
// повне коло разом із JSON: omitempty на нулі вже двічі з'їдав значення
// саме на цьому кроці, а не в SQL.
func TestBackupRoundTripKeepsPlanBuys(t *testing.T) {
	ctx := context.Background()
	src := openTmp(t, "src.db")
	// Порядок — той, у якому їх поверне ListPlanBuys (за датою), а не той,
	// у якому вони заведені: порівняння по індексу інакше звірялo б різні
	// рядки й падало б на правильних даних.
	want := []PlanBuy{
		{Kind: BuyBond, Ref: "UA4000227656", Qty: 3},
		{Kind: BuyNPF, Ref: "1", Amount: 400000, BuyDate: "2026-12-01"},
		{Kind: BuyDeposit, Ref: "privat", Amount: 5000000, Currency: "UAH",
			BuyDate: "2027-03-01", RateBP: 1450, Months: 12, IsReserve: true},
	}
	for _, b := range want {
		if _, err := src.AddPlanBuy(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	dump, err := src.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(dump.PlanBuys) != len(want) {
		t.Fatalf("експорт узяв %d рядків замість %d", len(dump.PlanBuys), len(want))
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	var back Backup
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	dst := openTmp(t, "dst.db")
	if err := dst.ImportAll(ctx, &back); err != nil {
		t.Fatal(err)
	}
	got, err := dst.ListPlanBuys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("після відновлення %d рядків замість %d", len(got), len(want))
	}
	for i := range want {
		w, g := want[i], got[i]
		if g.Kind != w.Kind || g.Ref != w.Ref || g.Qty != w.Qty || g.Amount != w.Amount ||
			g.Currency != w.Currency || g.BuyDate != w.BuyDate || g.RateBP != w.RateBP ||
			g.Months != w.Months || g.IsReserve != w.IsReserve {
			t.Errorf("рядок %d поїхав:\n маємо  %+v\n хотіли %+v", i, g, w)
		}
	}
	// Друге відновлення в ту саму базу — саме те, на чому вже падали
	// вклади й резерв: бекап тримає id, тож таблиця, забута в переліку
	// очистки, дає не дублікати, а відмову на UNIQUE(id).
	if err := dst.ImportAll(ctx, &back); err != nil {
		t.Fatalf("повторне відновлення впало (plan_buys забули очистити?): %v", err)
	}
}
