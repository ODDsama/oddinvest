package store

import (
	"context"
	"errors"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
)

func TestPlanExpenseCRUD(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	boiler, err := s.AddPlanExpense(ctx, domain.PlanExpense{
		Name: "Котел", Amount: 30_000_00, Currency: "UAH",
		DueDate: "2026-11-15", PaidFrom: domain.PaidFromCard, Place: "ПУМБ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPlanExpense(ctx, domain.PlanExpense{
		Name: "Страховка", Amount: 12_000_00, Currency: "UAH",
		DueDate: "2026-10-14", PaidFrom: domain.PaidFromPlan,
	}); err != nil {
		t.Fatal(err)
	}

	// Порядок задає СХОВИЩЕ: список у UI, підсумок місяця й черга задач
	// мусять називати ті самі витрати в тому самому порядку.
	got, err := s.ListPlanExpenses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Страховка" || got[1].Name != "Котел" {
		t.Fatalf("порядок настання поїхав: %+v", got)
	}
	if got[1].Amount != 30_000_00 || got[1].PaidFrom != domain.PaidFromCard ||
		got[1].Place != "ПУМБ" || got[1].Paid() {
		t.Errorf("витрата поїхала: %+v", got[1])
	}

	// «Сплачено» — це ПРАВКА рядка, а не окрема операція: PUT тут повна
	// заміна, і решта полів мусить пережити її незміненою.
	paid := got[1]
	paid.PaidDate = "2026-11-14"
	if err := s.UpdatePlanExpense(ctx, paid); err != nil {
		t.Fatal(err)
	}
	got, err = s.ListPlanExpenses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got[1].Paid() || got[1].PaidDate != "2026-11-14" {
		t.Errorf("позначка «сплачено» не збереглась: %+v", got[1])
	}
	if got[1].Place != "ПУМБ" || got[1].Amount != 30_000_00 {
		t.Errorf("правка стану витерла решту полів: %+v", got[1])
	}

	if err := s.DeletePlanExpense(ctx, boiler); err != nil {
		t.Fatal(err)
	}
	if got, err = s.ListPlanExpenses(ctx); err != nil || len(got) != 1 {
		t.Fatalf("після видалення лишилось %d витрат (%v)", len(got), err)
	}
	// Повторне видалення — ErrNotFound, а не тиша: інакше кнопка ✕ на
	// вже прибраному рядку виглядала б як успіх.
	if err := s.DeletePlanExpense(ctx, boiler); !errors.Is(err, ErrNotFound) {
		t.Errorf("видалення неіснуючої витрати: %v", err)
	}
}

// Чужий портфель не видно й не правиться — та сама межа, що в цілей.
func TestPlanExpenseScopedToPortfolio(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	pid, err := s.AddPortfolio(ctx, "wife", "Дружина")
	if err != nil {
		t.Fatal(err)
	}
	mine, err := s.AddPlanExpense(ctx, domain.PlanExpense{
		Name: "Котел", Amount: 30_000_00, Currency: "UAH",
		DueDate: "2026-11-15", PaidFrom: domain.PaidFromCard,
	})
	if err != nil {
		t.Fatal(err)
	}

	w := s.For(pid)
	got, err := w.ListPlanExpenses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("чужий портфель бачить %d моїх витрат", len(got))
	}
	// id приходить від клієнта, тож без звуження в UPDATE/DELETE колонка
	// portfolio_id була б лише косметикою.
	if err := w.UpdatePlanExpense(ctx, domain.PlanExpense{
		ID: mine, Name: "Підміна", Amount: 1_00, Currency: "UAH",
		DueDate: "2026-11-15", PaidFrom: domain.PaidFromCard,
	}); !errors.Is(err, ErrNotFound) {
		t.Errorf("чужа витрата виправлена з іншого портфеля: %v", err)
	}
	if err := w.DeletePlanExpense(ctx, mine); !errors.Is(err, ErrNotFound) {
		t.Errorf("чужа витрата видалена з іншого портфеля: %v", err)
	}
}
