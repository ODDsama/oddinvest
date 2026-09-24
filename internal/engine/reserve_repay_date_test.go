package engine

import (
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// Поповнення гасить позику на СВОЮ дату, а не на сьогодні.
//
// Доти борг позики для розливу рахувався на today — з цілим роком
// відсотка, — а порівнювався з поповненням, зробленим наступного дня
// після узяття. 11 000 на другий день ішли всі в першу позику (10 000 під
// 20%, «винна» на сьогодні ≈12 000), хоча того дня вона була винна ≈10 005;
// ~995 надлишку зникали, друга позика лишалась відкритою цілком і тримала
// ціль подушки піднятою.
func TestReserveRepayMatchedAtRepayDate(t *testing.T) {
	loans := []store.ReserveLoan{
		{ID: 1, RateBP: 2000, TakenDate: "2026-01-01", TakenAmount: 10_000_00, TakenCurrency: "UAH"},
		{ID: 2, RateBP: 2000, TakenDate: "2026-01-01", TakenAmount: 5_000_00, TakenCurrency: "UAH"},
	}
	ops := []store.ReserveOp{{Date: "2026-01-02", Amount: 11_000_00, Currency: "UAH"}}
	got := ReserveRepays(loans, ops, "2027-01-01")

	sum := func(rs []domain.LoanRepay) (s int64) {
		for _, r := range rs {
			s += r.Amount
		}
		return
	}
	first, second := sum(got[1]), sum(got[2])
	if first+second != 11_000_00 {
		t.Errorf("розлито %d + %d, а поповнення 11 000 — гроші зникли", first, second)
	}
	if owed, _ := domain.ReserveLoanBalance(10_000_00, 2000, "2026-01-01", got[1], "2026-01-02"); owed != 0 {
		t.Errorf("перша позика на 2 січня мала закритись, лишилось %d", owed)
	}
	// Перша на 2 січня винна тіло плюс день відсотка (≈10 005,48), решта
	// (≈994,52) — у другу, а не в нікуди.
	if first > 10_010_00 || second < 990_00 {
		t.Errorf("перша взяла %d, друга %d — надлишок першої мав перейти далі", first, second)
	}
}
