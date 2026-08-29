package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoalsCRUD(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	car, err := s.AddGoal(ctx, Goal{
		Name: "Авто", TargetAmount: 20_000_00, Currency: "USD",
		DueDate: "2028-03-01", Priority: 0, Place: "готівка",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddGoal(ctx, Goal{
		Name: "Будинок", TargetAmount: 3_000_000_00, Currency: "UAH", Priority: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// Порядок наповнення задає СХОВИЩЕ, а не читач: без цього «перша в
	// черзі» на одному екрані розійшлася б із першим рядком на іншому.
	got, err := s.ListGoals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Авто" || got[1].Name != "Будинок" {
		t.Fatalf("порядок цілей: %+v", got)
	}
	if got[0].TargetAmount != 20_000_00 || got[0].Currency != "USD" ||
		got[0].DueDate != "2028-03-01" {
		t.Errorf("ціль поїхала: %+v", got[0])
	}
	if got[1].DueDate != "" || got[1].Done() {
		t.Errorf("ціль без дедлайну не мусить мати ні дати, ні закриття: %+v", got[1])
	}

	// Правка зберігає id: «видалити й набрати заново» тут не спосіб
	// виправити одруківку — разом із ціллю пішов би журнал.
	upd := got[0]
	upd.TargetAmount, upd.Name = 25_000_00, "Авто (нове)"
	if err := s.UpdateGoal(ctx, upd); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ListGoals(ctx) //nolint:errcheck // помилку читання вже перевірено вище
	if got[0].ID != car || got[0].TargetAmount != 25_000_00 {
		t.Errorf("правка не зберегла id або суму: %+v", got[0])
	}
}

// Порожня дата йде В КІНЕЦЬ свого пріоритету.
//
// Сортування рядків клало б ” перед будь-якою датою, тобто ціль без
// дедлайну ставала б попереду терміновішої — і черга наповнення віддавала
// б гроші тій, що нікуди не поспішає.
func TestGoalsWithoutDueDateSortLast(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, g := range []Goal{
		{Name: "без дати", TargetAmount: 100, Currency: "UAH"},
		{Name: "березень", TargetAmount: 100, Currency: "UAH", DueDate: "2027-03-01"},
	} {
		if _, err := s.AddGoal(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListGoals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Name != "березень" {
		t.Errorf("першою мала стати ціль із дедлайном, маємо %q", got[0].Name)
	}
}

// Видалення цілі з рухами ВІДМОВЛЯЄ, а не каскадить.
//
// Без цієї перевірки видалення падало б сирою помилкою FK — говорило б те
// саме, але незрозуміло; а з каскадом ціль, прибрана помилково, забирала б
// із собою журнал, якого немає більше ніде.
func TestDeleteGoalRefusesWhileOpsExist(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, err := s.AddGoal(ctx, Goal{Name: "Авто", TargetAmount: 100, Currency: "UAH"})
	if err != nil {
		t.Fatal(err)
	}
	opID, err := s.AddGoalOp(ctx, GoalOp{GoalID: id, Date: "2026-08-01", Amount: 5000, Currency: "UAH"})
	if err != nil {
		t.Fatal(err)
	}
	err = s.DeleteGoal(ctx, id)
	if err == nil {
		t.Fatal("ціль із рухом видалилась — журнал пропав би мовчки")
	}
	if !strings.Contains(err.Error(), "рухів") {
		t.Errorf("відмова не називає причини: %v", err)
	}
	if err := s.DeleteGoalOp(ctx, opID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGoal(ctx, id); err != nil {
		t.Fatalf("порожня ціль мала видалитись: %v", err)
	}
}

// Рух можна перевести в ІНШУ ціль правкою.
//
// Записаний не туди рух — звичайна одруківка, і виправляти її видаленням
// означало б втратити дату, місце й нотатку.
func TestUpdateGoalOpMovesBetweenGoals(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	car, _ := s.AddGoal(ctx, Goal{Name: "Авто", TargetAmount: 100, Currency: "UAH"})      //nolint:errcheck // порожня БД: помилка тут неможлива, а перевірка сховала б суть тесту
	house, _ := s.AddGoal(ctx, Goal{Name: "Будинок", TargetAmount: 100, Currency: "UAH"}) //nolint:errcheck // те саме
	id, err := s.AddGoalOp(ctx, GoalOp{
		GoalID: car, Date: "2026-08-01", Amount: 5000, Currency: "UAH", Place: "сейф",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateGoalOp(ctx, GoalOp{
		ID: id, GoalID: house, Date: "2026-08-01", Amount: 5000, Currency: "UAH", Place: "сейф",
	}); err != nil {
		t.Fatal(err)
	}
	ops, err := s.ListGoalOps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].GoalID != house {
		t.Errorf("рух не переїхав: %+v", ops)
	}
}

// Бекап тримає ЦІЛІ Й ЖУРНАЛ, і відновлює їх у правильному порядку.
//
// Порядок тут не стиль: goal_ops має FK на goals, тож вставка рухів перед
// цілями впала б. А пропуск будь-якої з таблиць у переліку очищення дав би
// не «дублікати після відновлення», а відмову на UNIQUE(id) — рівно те, що
// вже сталося колись із вкладами.
func TestBackupRoundTripKeepsGoals(t *testing.T) {
	ctx := context.Background()
	src, err := Open(filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	id, err := src.AddGoal(ctx, Goal{
		Name: "Авто", TargetAmount: 20_000_00, Currency: "USD",
		DueDate: "2028-03-01", Priority: 2, Place: "готівка", Note: "б/у",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.AddGoalOp(ctx, GoalOp{
		GoalID: id, Date: "2026-08-01", Amount: 50_000_00, Currency: "UAH", Place: "сейф",
	}); err != nil {
		t.Fatal(err)
	}

	b, err := src.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Goals) != 1 || len(b.GoalOps) != 1 {
		t.Fatalf("експорт узяв %d цілей і %d рухів", len(b.Goals), len(b.GoalOps))
	}

	dst, err := Open(filepath.Join(t.TempDir(), "dst.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	// Двічі: відновлення ЗАМІЩУЄ, і другий прохід ловить забуту таблицю в
	// переліку очищення — саме той збій, що дає UNIQUE(id), а не дублікати.
	for i := 0; i < 2; i++ {
		if err := dst.ImportAll(ctx, b); err != nil {
			t.Fatalf("відновлення #%d: %v", i+1, err)
		}
	}
	goals, err := dst.ListGoals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(goals) != 1 {
		t.Fatalf("після відновлення %d цілей замість 1", len(goals))
	}
	g := goals[0]
	if g.Name != "Авто" || g.TargetAmount != 20_000_00 || g.Currency != "USD" ||
		g.DueDate != "2028-03-01" || g.Priority != 2 || g.Place != "готівка" {
		t.Errorf("ціль поїхала: %+v", g)
	}
	ops, err := dst.ListGoalOps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].GoalID != g.ID || ops[0].Amount != 50_000_00 {
		t.Errorf("журнал цілі поїхав: %+v", ops)
	}
}
