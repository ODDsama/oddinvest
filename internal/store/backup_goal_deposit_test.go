package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// TestBackupRemapsGoalOnTermDeposit — вклад під ціль (0062) переживає
// відновлення разом зі СВОЄЮ ціллю, а не з чиєюсь.
//
// Перевіряється не «поле доїхало», а перемапування id. У файлі бекапу
// лежать СТАРІ номери, а відновлення роздає нові — і якщо goal_id поїде
// сирим, вклад мовчки причепиться до іншої цілі або впреться у FK. Щоб
// це справді перевірялось, цілей тут ДВІ й вклад висить на другій:
// з однією ціллю id збіглися б випадково, і тест проходив би завжди.
//
// Друга половина перевірки — ПОРЯДОК вставки. Цілі мусять лягти раніше за
// вклади, інакше ids.of("goals") ще порожня й відновлення падає на FK — і
// падає лише в тих, у кого такий вклад є.
func TestBackupRemapsGoalOnTermDeposit(t *testing.T) {
	ctx := context.Background()
	src, err := Open(filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	if _, err := src.AddGoal(ctx, Goal{
		Name: "Ремонт", TargetAmount: 5000000, Currency: "UAH",
	}); err != nil {
		t.Fatal(err)
	}
	car, err := src.AddGoal(ctx, Goal{
		Name: "Авто", TargetAmount: 20000000, Currency: "USD", DueDate: "2028-06-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 10000000, RateBP: 1650,
		OpenDate: "2026-01-15", MaturityDate: "2027-01-15",
		Payout: domain.PayoutEnd, TaxBP: 2300, GoalID: car,
	}); err != nil {
		t.Fatal(err)
	}
	// Звичайний вклад поруч: nil у goal_id мусить лишитись nil, а не стати
	// нулем, який FK не знайде.
	if _, err := src.AddTermDeposit(ctx, domain.Deposit{
		Bank: "mono", Currency: "UAH", Principal: 3000000, RateBP: 1500,
		OpenDate: "2026-02-01", MaturityDate: "2026-08-01",
		Payout: domain.PayoutEnd, TaxBP: 2300,
	}); err != nil {
		t.Fatal(err)
	}

	b, err := src.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}

	dst, err := Open(filepath.Join(t.TempDir(), "dst.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	// Ціль-пустушка в приймачі зсуває нумерацію: без неї нові id збіглися б
	// зі старими, і перемапування знову лишилось би неперевіреним.
	if _, err := dst.AddGoal(ctx, Goal{
		Name: "Чуже", TargetAmount: 100, Currency: "UAH",
	}); err != nil {
		t.Fatal(err)
	}
	if err := dst.ImportAll(ctx, b); err != nil {
		t.Fatalf("відновлення впало — найпевніше цілі вставляються ПІСЛЯ вкладів: %v", err)
	}

	goals, err := dst.ListGoals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]int64{}
	for _, g := range goals {
		byName[g.Name] = g.ID
	}
	deps, err := dst.ListTermDeposits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var withGoal, without int
	for _, d := range deps {
		switch d.Bank {
		case "ПУМБ":
			withGoal++
			if d.GoalID != byName["Авто"] {
				t.Errorf("вклад причепився до цілі %d, а «Авто» тепер %d", d.GoalID, byName["Авто"])
			}
		case "mono":
			without++
			if d.GoalID != 0 {
				t.Errorf("вклад без цілі приїхав із ціллю %d", d.GoalID)
			}
		}
	}
	if withGoal != 1 || without != 1 {
		t.Fatalf("після відновлення %d цільових і %d звичайних вкладів замість 1 і 1", withGoal, without)
	}
}
