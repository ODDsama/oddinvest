package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

// TestDeleteGoalRefusesWhileDepositHangsOnIt — ціль не видаляється, доки на
// ній висить вклад.
//
// Без цієї перевірки видалення падало б сирою помилкою FK: те саме по суті,
// але незрозуміло, а головне — не сказало б, що робити (зняти ціль із
// вкладу, а не видаляти сам вклад).
func TestDeleteGoalRefusesWhileDepositHangsOnIt(t *testing.T) {
	st := testStore(t)
	seed(t, st)
	ctx := context.Background()
	gid := addGoalWithDeposit(t, st, ctx, "", 1600)
	err := st.DeleteGoal(ctx, gid)
	if err == nil {
		t.Fatal("ціль видалилась разом із вкладом, який на неї посилається")
	}
	if !strings.Contains(err.Error(), "вклад") {
		t.Errorf("помилка %q не називає вклади — людині нема з чого зрозуміти, що робити", err)
	}
}

func addGoalWithDeposit(t *testing.T, st *store.Store, ctx context.Context,
	due domain.Date, rateBP int64) int64 {

	t.Helper()
	gid, err := st.AddGoal(ctx, store.Goal{
		Name: "Авто", TargetAmount: 1_000_000_00, Currency: money.UAH, DueDate: due,
	})
	if err != nil {
		t.Fatal(err)
	}
	today := domain.NewDate(time.Now())
	// Поповнюваний — щоб «не пропонується» не пройшло випадково через те,
	// що вклад і так не приймає поповнень. Той самий прийом, що в тесті
	// подушки.
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: money.UAH, Principal: 200_000_00,
		RateBP: rateBP, OpenDate: today, MaturityDate: today.AddMonths(12),
		Payout: domain.PayoutEnd, TaxBP: 2300,
		Replenishable: true, GoalID: gid,
	}); err != nil {
		t.Fatal(err)
	}
	return gid
}
