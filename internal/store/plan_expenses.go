// Планові витрати: вирішені разові гроші з датою й станом «сплачено».
//
// Окремим файлом, а не рядками в plan.go, бо це окрема сутність, а не
// різновид потоку — довід у шапці міграції 0056. Повторювати його тут
// означало б завести другу копію, яка розійдеться.
//
// ЖУРНАЛУ РЕВІЗІЙ ТУТ НЕМАЄ, і це не пропуск. plan_flow_revisions (0026)
// існує тому, що buildPlanHistory реконструює минулі місяці плану й мусить
// знати, яким потік був тоді. З plan_expenses минулі місяці не
// реконструюються ніде: витрата або сплачена, або ні, і обидва стани
// видно в самому рядку. Дзеркало 0026 було б механізмом без читача.
//
// Тип рядка живе в domain (domain.PlanExpense), а не тут: «чи тисне вона в
// жовтні» питають троє незалежних читачів, і відповідь мусить бути одна.

package store

import (
	"context"

	"github.com/ODDsama/oddinvest/internal/domain"
)

func (s *Store) AddPlanExpense(ctx context.Context, e domain.PlanExpense) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO plan_expenses
		(portfolio_id, name, amount, currency, due_date, paid_from, paid_date, place, note)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		s.pid, e.Name, e.Amount, e.Currency, string(e.DueDate), e.PaidFrom,
		string(e.PaidDate), e.Place, e.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdatePlanExpense переписує витрату, зберігаючи id.
//
// Повна заміна рядка, як і всюди в цьому застосунку: часткове оновлення
// вимагало б розрізняти «поле не надіслали» й «поле спорожнили», а на
// paid_date ця різниця означала б «не чіпай стан» проти «поверни в
// невиконані» — рівно те, що людина робить, знявши помилкову позначку.
func (s *Store) UpdatePlanExpense(ctx context.Context, e domain.PlanExpense) error {
	res, err := s.db.ExecContext(ctx, `UPDATE plan_expenses SET
		name=?, amount=?, currency=?, due_date=?, paid_from=?, paid_date=?, place=?, note=?
		WHERE id=? AND portfolio_id=?`,
		e.Name, e.Amount, e.Currency, string(e.DueDate), e.PaidFrom,
		string(e.PaidDate), e.Place, e.Note, e.ID, s.pid)
	if err != nil {
		return err
	}
	return affectedOne(res, "планова витрата")
}

// DeletePlanExpense прибирає витрату назовсім.
//
// Перевірки «чи є під нею щось» немає, бо під нею немає нічого: журналу в
// планової витрати не буває за побудовою (див. шапку файла). Саме тому
// скасована витрата видаляється, а не отримує другу дату стану — довід у
// шапці 0056.
func (s *Store) DeletePlanExpense(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM plan_expenses WHERE id=? AND portfolio_id=?`, id, s.pid)
	if err != nil {
		return err
	}
	return affectedOne(res, "планова витрата")
}

// ListPlanExpenses — усі планові витрати в порядку настання.
//
// Порядок задається ТУТ, а не сортуванням у кожного читача: список у UI,
// підсумок місяця й черга задач мусять називати ті самі витрати в тому
// самому порядку. Порожньої дати тут не буває (due_date обовʼязкова), тож
// трюк із порожньою датою, що стоїть у ListGoals, тут не потрібен.
//
// Без фільтра «лише несплачені»: сплачені показує список, і другий запит
// на кожен /api/summary був би платою за те, що зробить один if у читача.
func (s *Store) ListPlanExpenses(ctx context.Context) ([]domain.PlanExpense, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, amount, currency,
		due_date, paid_from, paid_date, place, note
		FROM plan_expenses WHERE portfolio_id=? ORDER BY due_date, id`, s.pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.PlanExpense
	for rows.Next() {
		var e domain.PlanExpense
		var due, paid string
		if err := rows.Scan(&e.ID, &e.Name, &e.Amount, &e.Currency,
			&due, &e.PaidFrom, &paid, &e.Place, &e.Note); err != nil {
			return nil, err
		}
		e.DueDate, e.PaidDate = domain.Date(due), domain.Date(paid)
		out = append(out, e)
	}
	return out, rows.Err()
}
