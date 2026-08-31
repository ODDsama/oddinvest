// Борги, їхні рухи та звірки з банком.
//
// Окремим файлом, як і цілі: сутностей тут три й вони звʼязані — сам борг,
// журнал рухів під ним і звірки. Довід, чому це не plan_flows, не ціль зі
// знаком мінус і не brokers, лежить у міграції 0045; повторювати його тут
// означало б завести другу копію, яка розійдеться.

package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Види боргу. Рядками, бо їдуть у JSON і в CHECK міграції.
const (
	DebtCard        = "card"
	DebtInstallment = "installment"
)

// Види руху. payment зменшує борг, draw і cash — збільшують; різниця між
// останніми двома в тому, що на cash пільговий період не поширюється
// НІКОЛИ (довід у 0045).
const (
	DebtOpPayment = "payment"
	DebtOpDraw    = "draw"
	DebtOpCash    = "cash"
)

// Debt — кредитна картка або розстрочка.
//
// Одна структура на два види: половина полів у кожному випадку мертва, і
// це названо в міграції поіменно. Дві структури означали б два списки,
// дві черги погашення й два рейтинги — а питання одне.
type Debt struct {
	ID       int64
	Name     string
	Kind     string
	Currency string
	// CardID — розстрочка всередині картки; 0 = самостійний борг (у базі
	// NULL). Нулем, а не вказівником: id автоінкрементні й починаються з
	// одиниці, тож нуль не може означати нічого іншого, а вказівник
	// вимагав би розіменування в кожного читача.
	CardID int64

	// --- картка ---
	LimitAmount     int64
	StatementDay    int64
	APRBp           int64
	APROverdueBp    int64
	MinPaymentBp    int64
	MinPaymentFloor int64
	LateFee         int64

	// --- розстрочка ---
	Principal        int64
	PaymentsTotal    int64
	FirstPaymentDate domain.Date
	FeeMonthBp       int64
	FeeFreeMonths    int64

	OpenedDate domain.Date
	ClosedDate domain.Date
	Place      string
	Note       string
}

// Closed — борг погашено. Методом, а не порівнянням у кожного читача: їх
// буде щонайменше троє (черга, список, рейтинг), і три однакові рядки з
// порожнім літералом розійшлись би на першій правці.
func (d Debt) Closed() bool { return d.ClosedDate != "" }

// IsCard — картка з пільговим циклом, а не розстрочка.
func (d Debt) IsCard() bool { return d.Kind == DebtCard }

// DebtOp — один рух під боргом. Amount завжди додатний, напрям задає Kind
// (правило 0025: одна колонка не відповідає на два питання).
type DebtOp struct {
	ID     int64
	DebtID int64
	Date   domain.Date
	Kind   string
	Amount int64
	Note   string
}

// DebtMark — звірка з додатком банку: три числа одного моменту.
type DebtMark struct {
	ID     int64
	DebtID int64
	Date   domain.Date
	// Balance знакозмінний: плюс — власні гроші на картці, мінус —
	// використаний ліміт.
	Balance      int64
	StatementDue int64
	NonGrace     int64
	Note         string
}

// nullableID перетворює 0 на SQL NULL. Потрібне лише для card_id: FK на
// нуль не існує, а NOT NULL DEFAULT 0 зробив би посилання на неіснуючий
// рядок представимим.
func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

const debtCols = `id, name, kind, currency, card_id, limit_amount, statement_day,
	apr_bp, apr_overdue_bp, min_payment_bp, min_payment_floor, late_fee,
	principal, payments_total, first_payment_date, fee_month_bp, fee_free_months,
	opened_date, closed_date, place, note`

func (s *Store) AddDebt(ctx context.Context, d Debt) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO debts
		(name, kind, currency, card_id, limit_amount, statement_day,
		 apr_bp, apr_overdue_bp, min_payment_bp, min_payment_floor, late_fee,
		 principal, payments_total, first_payment_date, fee_month_bp, fee_free_months,
		 opened_date, closed_date, place, note)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.Name, d.Kind, d.Currency, nullableID(d.CardID), d.LimitAmount, d.StatementDay,
		d.APRBp, d.APROverdueBp, d.MinPaymentBp, d.MinPaymentFloor, d.LateFee,
		d.Principal, d.PaymentsTotal, string(d.FirstPaymentDate), d.FeeMonthBp, d.FeeFreeMonths,
		string(d.OpenedDate), string(d.ClosedDate), d.Place, d.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateDebt переписує борг, зберігаючи id.
func (s *Store) UpdateDebt(ctx context.Context, d Debt) error {
	res, err := s.db.ExecContext(ctx, `UPDATE debts SET
		name=?, kind=?, currency=?, card_id=?, limit_amount=?, statement_day=?,
		apr_bp=?, apr_overdue_bp=?, min_payment_bp=?, min_payment_floor=?, late_fee=?,
		principal=?, payments_total=?, first_payment_date=?, fee_month_bp=?, fee_free_months=?,
		opened_date=?, closed_date=?, place=?, note=? WHERE id=?`,
		d.Name, d.Kind, d.Currency, nullableID(d.CardID), d.LimitAmount, d.StatementDay,
		d.APRBp, d.APROverdueBp, d.MinPaymentBp, d.MinPaymentFloor, d.LateFee,
		d.Principal, d.PaymentsTotal, string(d.FirstPaymentDate), d.FeeMonthBp, d.FeeFreeMonths,
		string(d.OpenedDate), string(d.ClosedDate), d.Place, d.Note, d.ID)
	if err != nil {
		return err
	}
	return affectedOne(res, "борг")
}

// DeleteDebt відмовляє, доки під боргом є рухи, звірки або прив'язані
// розстрочки.
//
// Каскаду немає навмисно — той самий прецедент, що з фондом і позначками
// цін (refs.go) та з ціллю (goals.go): борг можна прибрати й помилково, а
// разом із ним пішов би журнал, якого немає більше ніде. Без цієї
// перевірки видалення падало б сирою помилкою FK, тобто говорило б те
// саме, але незрозуміло.
//
// Погашений борг видаляється НЕ ЦИМ шляхом: у нього є closed_date, і
// закрити борг — не те саме, що стерти, ніби його не було.
func (s *Store) DeleteDebt(ctx context.Context, id int64) error {
	for _, c := range []struct{ q, what string }{
		{`SELECT COUNT(*) FROM debt_ops WHERE debt_id=?`, "рухів"},
		{`SELECT COUNT(*) FROM debt_marks WHERE debt_id=?`, "звірок"},
		{`SELECT COUNT(*) FROM debts WHERE card_id=?`, "прив'язаних розстрочок"},
	} {
		var used int
		if err := s.db.QueryRowContext(ctx, c.q, id).Scan(&used); err != nil {
			return err
		}
		if used > 0 {
			return fmt.Errorf("під боргом %d %s — спершу видали їх або познач борг погашеним", used, c.what)
		}
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM debts WHERE id=?`, id)
	if err != nil {
		return err
	}
	return affectedOne(res, "борг")
}

// ListDebts — усі борги в порядку, у якому їх читає людина: спершу картки,
// під ними їхні розстрочки, далі самостійні.
//
// Порядок один на всіх читачів і задається тут, а не сортуванням у
// кожного, — рівно з того доводу, що в ListGoals. Черга ПОГАШЕННЯ це не
// він: вона впорядкована ставкою й живе в domain/debt.go, бо залежить від
// стратегії, яку обрала людина.
func (s *Store) ListDebts(ctx context.Context) ([]Debt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+debtCols+`
		FROM debts ORDER BY COALESCE(card_id, id), card_id IS NOT NULL, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Debt
	for rows.Next() {
		var d Debt
		var card sql.NullInt64
		var first, opened, closed string
		if err := rows.Scan(&d.ID, &d.Name, &d.Kind, &d.Currency, &card,
			&d.LimitAmount, &d.StatementDay, &d.APRBp, &d.APROverdueBp,
			&d.MinPaymentBp, &d.MinPaymentFloor, &d.LateFee,
			&d.Principal, &d.PaymentsTotal, &first, &d.FeeMonthBp, &d.FeeFreeMonths,
			&opened, &closed, &d.Place, &d.Note); err != nil {
			return nil, err
		}
		d.CardID = card.Int64
		d.FirstPaymentDate = domain.Date(first)
		d.OpenedDate, d.ClosedDate = domain.Date(opened), domain.Date(closed)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) AddDebtOp(ctx context.Context, op DebtOp) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO debt_ops
		(debt_id, date, kind, amount, note) VALUES (?,?,?,?,?)`,
		op.DebtID, string(op.Date), op.Kind, op.Amount, op.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateDebtOp переписує рух, зберігаючи id. debt_id теж переписується:
// рух, записаний не в той борг, — звичайна одруківка (те саме правило, що
// в UpdateGoalOp).
func (s *Store) UpdateDebtOp(ctx context.Context, op DebtOp) error {
	res, err := s.db.ExecContext(ctx, `UPDATE debt_ops SET
		debt_id=?, date=?, kind=?, amount=?, note=? WHERE id=?`,
		op.DebtID, string(op.Date), op.Kind, op.Amount, op.Note, op.ID)
	if err != nil {
		return err
	}
	return affectedOne(res, "рух боргу")
}

func (s *Store) DeleteDebtOp(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM debt_ops WHERE id=?`, id)
	return err
}

// ListDebtOps — усі рухи всіх боргів, хронологічно. Без фільтра за боргом
// навмисно: будівник стану читає джерела рівно по разу (state_sources.go).
func (s *Store) ListDebtOps(ctx context.Context) ([]DebtOp, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, debt_id, date, kind, amount, note
		FROM debt_ops ORDER BY date, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DebtOp
	for rows.Next() {
		var op DebtOp
		var dt string
		if err := rows.Scan(&op.ID, &op.DebtID, &dt, &op.Kind, &op.Amount, &op.Note); err != nil {
			return nil, err
		}
		op.Date = domain.Date(dt)
		out = append(out, op)
	}
	return out, rows.Err()
}

// AddDebtMark вклеює звірку; повторна на ту саму дату ПЕРЕПИСУЄ попередню.
//
// Дзеркало AddFundPricePoints і з того самого доводу: дві звірки на одну
// дату — це виправлення одруківки, а не дві правди про той самий день.
func (s *Store) AddDebtMark(ctx context.Context, m DebtMark) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO debt_marks
		(debt_id, date, balance, statement_due, non_grace, note) VALUES (?,?,?,?,?,?)
		ON CONFLICT(debt_id, date) DO UPDATE SET
			balance=excluded.balance, statement_due=excluded.statement_due,
			non_grace=excluded.non_grace, note=excluded.note`,
		m.DebtID, string(m.Date), m.Balance, m.StatementDue, m.NonGrace, m.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateDebtMark переписує звірку, зберігаючи id і борг.
//
// Борг НЕ переписується, на відміну від руху: звірка це знімок ОДНОГО
// рахунку, і перенести її на інший борг означало б не виправити помилку, а
// приписати чужому рахунку чужий баланс. Помилилися боргом — видаліть і
// зніміть заново, це два числа.
func (s *Store) UpdateDebtMark(ctx context.Context, m DebtMark) error {
	res, err := s.db.ExecContext(ctx, `UPDATE debt_marks SET
		date=?, balance=?, statement_due=?, non_grace=?, note=? WHERE id=?`,
		string(m.Date), m.Balance, m.StatementDue, m.NonGrace, m.Note, m.ID)
	if err != nil {
		return err
	}
	return affectedOne(res, "звірку боргу")
}

func (s *Store) DeleteDebtMark(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM debt_marks WHERE id=?`, id)
	return err
}

// ListDebtMarks — усі звірки всіх боргів, хронологічно.
func (s *Store) ListDebtMarks(ctx context.Context) ([]DebtMark, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, debt_id, date, balance,
		statement_due, non_grace, note FROM debt_marks ORDER BY date, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DebtMark
	for rows.Next() {
		var m DebtMark
		var dt string
		if err := rows.Scan(&m.ID, &m.DebtID, &dt, &m.Balance,
			&m.StatementDue, &m.NonGrace, &m.Note); err != nil {
			return nil, err
		}
		m.Date = domain.Date(dt)
		out = append(out, m)
	}
	return out, rows.Err()
}
