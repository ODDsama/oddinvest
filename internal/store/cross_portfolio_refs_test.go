package store

import (
	"context"
	"errors"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Посилання на ЧУЖИЙ запис відхиляється, а не приймається мовчки.
//
// Вклад портфеля B міг назвати ціль портфеля A (goal_id), рух подушки B —
// позику A (loan_id). Зовнішній ключ без дії на видалення після цього
// валив DeleteGoal / DeleteReserveLoan / DeletePortfolio у портфелі A:
// свій рахунок A бачив нуль посилань, а база — одне. Та сама перевірка
// ownsRow, що вже стоїть на операціях цілі й боргу.
func TestCrossPortfolioRefsRejected(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	pidB, err := st.AddPortfolio(ctx, "wife", "Дружина")
	if err != nil {
		t.Fatal(err)
	}
	b := st.For(pidB)

	goalA, err := st.AddGoal(ctx, Goal{Name: "Авто", TargetAmount: 500_000_00, Currency: "UAH"})
	if err != nil {
		t.Fatal(err)
	}
	dep := domain.Deposit{Bank: "Приват", Currency: "UAH", Principal: 10_000_00, RateBP: 1500,
		OpenDate: "2026-01-10", MaturityDate: "2027-01-10", Payout: domain.PayoutEnd, GoalID: goalA}
	if _, err := b.AddTermDeposit(ctx, dep); !errors.Is(err, ErrNotFound) {
		t.Errorf("вклад B на ціль A: чекали ErrNotFound, маємо %v", err)
	}
	dep.GoalID = 0
	id, err := b.AddTermDeposit(ctx, dep)
	if err != nil {
		t.Fatal(err)
	}
	dep.ID, dep.GoalID = id, goalA
	if err := b.UpdateTermDeposit(ctx, dep); !errors.Is(err, ErrNotFound) {
		t.Errorf("правка вкладу B на ціль A: чекали ErrNotFound, маємо %v", err)
	}

	take, err := st.AddReserveOp(ctx, ReserveOp{Date: "2026-02-01", Amount: -5_000_00, Currency: "UAH"})
	if err != nil {
		t.Fatal(err)
	}
	loanA, err := st.AddReserveLoan(ctx, ReserveLoan{OpID: take, RateBP: 1200})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddReserveOp(ctx, ReserveOp{Date: "2026-03-01", Amount: 1_000_00, Currency: "UAH",
		LoanID: loanA}); !errors.Is(err, ErrNotFound) {
		t.Errorf("повернення B у позику A: чекали ErrNotFound, маємо %v", err)
	}
	// Головне: A видаляє свою позику й ціль без чужих хвостів.
	if err := st.DeleteReserveLoan(ctx, loanA); err != nil {
		t.Errorf("A не змогла видалити свою позику: %v", err)
	}
	if err := st.DeleteGoal(ctx, goalA); err != nil {
		t.Errorf("A не змогла видалити свою ціль: %v", err)
	}
}
