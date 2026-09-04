package store

import (
	"context"
	"errors"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Ізоляція портфелів на рівні сховища (0054).
//
// Сторож у scope_guard_test.go перевіряє ТЕКСТ запитів; цей тест — їхню
// ПОВЕДІНКУ на двох портфелях одночасно: кожен бачить лише своє, чужий id
// у правці читається як «не знайдено», а дитина (продаж, рух цілі) не
// чіпляється до чужого батька. Один портфель цього не доведе: з пропущеним
// WHERE він проходить так само.
func TestPortfolioIsolation(t *testing.T) {
	a := openTest(t)
	ctx := context.Background()
	wid, err := a.AddPortfolio(ctx, "wife", "Дружина")
	if err != nil {
		t.Fatal(err)
	}
	b := a.For(wid)

	lot := func(qty int64) domain.Lot {
		return domain.Lot{ISIN: "UA4000239016", Qty: qty,
			PricePerBond: money.New(100000, money.UAH), BuyDate: "2026-09-01", Channel: "mono"}
	}
	aLot, err := a.AddLot(ctx, lot(3))
	if err != nil {
		t.Fatal(err)
	}
	bLot, err := b.AddLot(ctx, lot(5))
	if err != nil {
		t.Fatal(err)
	}

	// --- списки: кожен своє ---
	if l, _ := a.ListLots(ctx); len(l) != 1 || l[0].ID != aLot {
		t.Errorf("a бачить не лише своє: %+v", l)
	}
	if l, _ := b.ListLots(ctx); len(l) != 1 || l[0].ID != bLot {
		t.Errorf("b бачить не лише своє: %+v", l)
	}

	// --- правка чужого id: не знайдено, і нічого не змінилось ---
	foreign := lot(9)
	foreign.ID = aLot
	if err := b.UpdateLot(ctx, foreign); err == nil {
		t.Error("b переписав лот a")
	}
	if err := b.DeleteLot(ctx, aLot); err == nil {
		// DeleteLot може й не звітувати про 0 рядків — головне, що лот a
		// на місці.
		t.Log("DeleteLot чужого id мовчить — перевіряємо наслідок")
	}
	if l, _ := a.ListLots(ctx); len(l) != 1 || l[0].Qty != 3 {
		t.Errorf("лот a постраждав від b: %+v", l)
	}

	// --- дитина до чужого батька: продаж лота з іншого портфеля ---
	if _, err := b.AddSale(ctx, domain.Sale{LotID: aLot, SaleDate: "2026-09-02", Qty: 1,
		CleanPerBond: money.New(100000, money.UAH)}); err == nil {
		t.Error("b записав продаж лота a")
	}
	if s, _ := a.ListSales(ctx); len(s) != 0 {
		t.Errorf("у a зʼявився чужий продаж: %+v", s)
	}

	// --- рух цілі до чужої цілі ---
	aGoal, err := a.AddGoal(ctx, Goal{Name: "авто", TargetAmount: 100000, Currency: "UAH"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddGoalOp(ctx, GoalOp{GoalID: aGoal, Date: "2026-09-01", Amount: 100, Currency: "UAH"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("рух до чужої цілі: %v, хочемо ErrNotFound", err)
	}

	// --- налаштування: та сама ставка різними числами ---
	if err := a.SetSetting(ctx, "usd_target_share_pct", "40"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetSetting(ctx, "usd_target_share_pct", "70"); err != nil {
		t.Fatal(err)
	}
	if v, _ := a.GetSetting(ctx, "usd_target_share_pct"); v != "40" {
		t.Errorf("налаштування a = %q", v)
	}
	if all, _ := b.AllSettings(ctx); all["usd_target_share_pct"] != "70" {
		t.Errorf("налаштування b = %q", all["usd_target_share_pct"])
	}

	// --- знімки за той самий день ---
	for _, st := range []*Store{a, b} {
		if err := st.SaveSnapshot(ctx, Snapshot{Date: "2026-09-01", InvestedUAH: st.Portfolio()}); err != nil {
			t.Fatal(err)
		}
	}
	if sn, _ := b.ListSnapshots(ctx, "", ""); len(sn) != 1 || sn[0].InvestedUAH != wid {
		t.Errorf("знімки b: %+v", sn)
	}

	// --- брокери: «mono» в обох, два різні рядки ---
	aMono, err := a.AddBroker(ctx, "mono")
	if err != nil {
		t.Fatal(err)
	}
	bMono, err := b.AddBroker(ctx, "mono")
	if err != nil {
		t.Fatal(err)
	}
	if aMono == bMono {
		t.Errorf("один брокер на два портфелі: %d", aMono)
	}
	if err := a.RenameBroker(ctx, bMono, "x"); err == nil {
		t.Error("a перейменував брокера b")
	}
	if l, _ := a.ListBrokers(ctx); len(l) != 1 || l[0].Name != "mono" {
		t.Errorf("брокери a: %+v", l)
	}

	// --- статус виплати й профіль імпорту: той самий ключ у кожного ---
	if err := b.SetPaymentStatus(ctx, "UA4000239016", "2026-09-10", "received"); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.PaymentStatuses(ctx); len(st) != 0 {
		t.Errorf("статуси a: %+v", st)
	}
	if err := a.SaveImportProfile(ctx, ImportProfile{Name: "inzhur"}); err != nil {
		t.Fatal(err)
	}
	if err := b.SaveImportProfile(ctx, ImportProfile{Name: "inzhur", Header: 3}); err != nil {
		t.Fatal(err)
	}
	if p, _ := a.GetImportProfile(ctx, "inzhur"); p == nil || p.Header == 3 {
		t.Errorf("профіль a перезаписаний профілем b: %+v", p)
	}
}
