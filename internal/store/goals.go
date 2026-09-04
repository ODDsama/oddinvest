// Цілі накопичення та їхні рухи.
//
// Окремим файлом, а не рядками в store.go, бо сутностей тут ДВІ й вони
// звʼязані: ціль і журнал під нею. У store.go рухи резерву живуть поруч із
// поповненнями рівно тому, що в резерву самої «цілі» як запису немає — вона
// виводиться з налаштувань. Тут запис є, і разом із журналом це вже свій
// шматок домену.
//
// Довід, чому це не reserve_ops і не deposits, лежить у міграції 0039 —
// повторювати його тут означало б завести другу копію, яка розійдеться.

package store

import (
	"context"
	"fmt"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Goal — ціль накопичення: названа сума в названій валюті, необовʼязково
// з датою.
//
// TargetAmount мінорні й У ВАЛЮТІ ЦІЛІ: гривневого числа тут бути не може,
// бо ціна авто в доларах — це і є ціль, а її гривневий еквівалент
// змінюється щодня (аргумент у шапці міграції).
type Goal struct {
	ID           int64
	Name         string
	TargetAmount int64
	Currency     string
	// DueDate порожня = ціль без дедлайну. Стан законний: «збираю, коли
	// збереться — куплю», і питання «чи встигаю» такій цілі не ставлять.
	DueDate domain.Date
	// Priority — порядок наповнення, коли цілей кілька. Менше = раніше.
	Priority int64
	Place    string
	Note     string
	// DoneDate — річ куплена. Не видалення: журнал і є історія того, як на
	// неї збирали.
	DoneDate domain.Date
}

// Done — чи ціль закрита. Метод, а не порівняння в кожного читача: їх
// троє (наповнення, список, підсумок), і три однакові рядки з порожнім
// літералом розійшлись би на першій же правці.
func (g Goal) Done() bool { return g.DoneDate != "" }

// GoalOp — один рух під ціллю: + відклав, − узяв.
//
// Валюта СВОЯ, а не ціль-ова: на доларову ціль можна відкладати гривнею, і
// саме тоді курс стає видимим.
type GoalOp struct {
	ID       int64
	GoalID   int64
	Date     domain.Date
	Amount   int64 // мінорні; + відклав / − узяв
	Currency string
	Place    string
	Note     string
}

func (s *Store) AddGoal(ctx context.Context, g Goal) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO goals
		(portfolio_id, name, target_amount, currency, due_date, priority, place, note, done_date)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		s.pid, g.Name, g.TargetAmount, g.Currency, string(g.DueDate), g.Priority,
		g.Place, g.Note, string(g.DoneDate))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateGoal переписує ціль, зберігаючи id.
func (s *Store) UpdateGoal(ctx context.Context, g Goal) error {
	res, err := s.db.ExecContext(ctx, `UPDATE goals SET
		name=?, target_amount=?, currency=?, due_date=?, priority=?,
		place=?, note=?, done_date=? WHERE id=? AND portfolio_id=?`,
		g.Name, g.TargetAmount, g.Currency, string(g.DueDate), g.Priority,
		g.Place, g.Note, string(g.DoneDate), g.ID, s.pid)
	if err != nil {
		return err
	}
	return affectedOne(res, "ціль")
}

// DeleteGoal відмовляє, доки під ціллю є рухи.
//
// Каскаду немає навмисно — той самий прецедент, що з фондом і позначками
// цін (refs.go): ціль можна прибрати й помилково, а разом із нею пішов би
// журнал, якого немає більше ніде. Без цієї перевірки видалення падало б
// сирою помилкою FK, тобто говорило б те саме, але незрозуміло.
//
// Досягнута ціль видаляється НЕ ЦИМ шляхом: у неї є done_date, і закрити
// річ, на яку зібрав, — не те саме, що стерти, ніби її не було.
func (s *Store) DeleteGoal(ctx context.Context, id int64) error {
	var used int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM goal_ops WHERE goal_id=? AND portfolio_id=?`, id, s.pid).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return fmt.Errorf("під ціллю %d рухів — спершу видали їх або познач ціль досягнутою", used)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM goals WHERE id=? AND portfolio_id=?`, id, s.pid)
	if err != nil {
		return err
	}
	return affectedOne(res, "ціль")
}

// ListGoals — усі цілі в порядку НАПОВНЕННЯ: пріоритет, далі найближчий
// дедлайн, далі id.
//
// Порядок один на всіх читачів і задається тут, а не сортуванням у кожного:
// черга наповнення, список у майстрі й підсумок мусять називати ті самі
// цілі в тому самому порядку, інакше «перша в черзі» на одному екрані не
// збігатиметься з першим рядком на іншому.
//
// Порожня дата йде В КІНЕЦЬ свого пріоритету, а не на початок: ціль без
// дедлайну нікуди не поспішає за визначенням, а сортування рядків клало б
// ” перед будь-якою датою й ставило її попереду терміновіших.
func (s *Store) ListGoals(ctx context.Context) ([]Goal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, target_amount, currency,
		due_date, priority, place, note, done_date
		FROM goals WHERE portfolio_id=? ORDER BY priority, due_date = '', due_date, id`, s.pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Goal
	for rows.Next() {
		var g Goal
		var due, done string
		if err := rows.Scan(&g.ID, &g.Name, &g.TargetAmount, &g.Currency,
			&due, &g.Priority, &g.Place, &g.Note, &done); err != nil {
			return nil, err
		}
		g.DueDate, g.DoneDate = domain.Date(due), domain.Date(done)
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) AddGoalOp(ctx context.Context, op GoalOp) (int64, error) {
	// Чужа ціль має читатися як ErrNotFound, а не тихо прийматись.
	if err := s.ownsRow(ctx, "goals", op.GoalID); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO goal_ops
		(portfolio_id, goal_id, date, amount, currency, place, note) VALUES (?,?,?,?,?,?,?)`,
		s.pid, op.GoalID, string(op.Date), op.Amount, op.Currency, op.Place, op.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateGoalOp переписує рух, зберігаючи id.
//
// goal_id теж переписується: рух, записаний не в ту ціль, — звичайна
// одруківка, і виправляти її видаленням означало б втратити дату й місце
// (те саме правило, що в шапці CLAUDE.md про повний КРУД).
func (s *Store) UpdateGoalOp(ctx context.Context, op GoalOp) error {
	// Чужа ціль має читатися як ErrNotFound, а не тихо прийматись.
	if err := s.ownsRow(ctx, "goals", op.GoalID); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE goal_ops SET
		goal_id=?, date=?, amount=?, currency=?, place=?, note=? WHERE id=? AND portfolio_id=?`,
		op.GoalID, string(op.Date), op.Amount, op.Currency, op.Place, op.Note, op.ID, s.pid)
	if err != nil {
		return err
	}
	return affectedOne(res, "рух цілі")
}

func (s *Store) DeleteGoalOp(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM goal_ops WHERE id=? AND portfolio_id=?`, id, s.pid)
	return err
}

// ListGoalOps — усі рухи всіх цілей, хронологічно.
//
// Без фільтра за ціллю навмисно: будівник стану читає джерела РІВНО ПО
// РАЗУ (state_sources.go), і запит на ціль означав би N запитів на кожен
// /api/summary. Розкласти по цілях умів би й сам виклик, але це вже
// робота того, кому потрібен зріз.
func (s *Store) ListGoalOps(ctx context.Context) ([]GoalOp, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, goal_id, date, amount, currency, place, note
		FROM goal_ops WHERE portfolio_id=? ORDER BY date, id`, s.pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GoalOp
	for rows.Next() {
		var op GoalOp
		var dt string
		if err := rows.Scan(&op.ID, &op.GoalID, &dt, &op.Amount,
			&op.Currency, &op.Place, &op.Note); err != nil {
			return nil, err
		}
		op.Date = domain.Date(dt)
		out = append(out, op)
	}
	return out, rows.Err()
}
