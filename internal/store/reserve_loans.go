package store

import (
	"context"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// --- позики в самого себе (0057) ---

// ReserveLoan — обіцянка повернути в подушку те, що з неї взято, і з
// відсотком. Тіла тут немає навмисно: воно є сумою руху OpID, і друга
// копія розійшлася б із ним на першій же правці (довід — шапка 0057).
//
// TakenAmount/TakenCurrency/TakenDate заповнює ListReserveLoans із того
// самого руху — читачеві позики потрібні всі чотири числа одразу, а
// збирати їх приєднанням у кожному викликачеві означало б повторити
// JOIN у пʼятьох місцях.
type ReserveLoan struct {
	ID      int64
	OpID    int64
	RateBP  int64  // %×100, знімок на момент узяття
	DueDate string // порожньо = без дедлайну
	Note    string
	// Прочитане з reserve_ops, не своє.
	TakenDate     domain.Date
	TakenAmount   int64 // мінорні, ДОДАТНЕ (тіло позики)
	TakenCurrency string
}

func (s *Store) AddReserveLoan(ctx context.Context, l ReserveLoan) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO reserve_loans (portfolio_id, op_id, rate_bp, due_date, note)
		VALUES (?,?,?,?,?)`, s.pid, l.OpID, l.RateBP, l.DueDate, l.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateReserveLoan міняє лише умови позики. OpID не переписується: рух,
// який її відкрив, і є її тілом, а перевісити позику на інше зняття
// означало б задним числом змінити і суму, і дату.
func (s *Store) UpdateReserveLoan(ctx context.Context, l ReserveLoan) error {
	res, err := s.db.ExecContext(ctx, `UPDATE reserve_loans SET
		rate_bp=?, due_date=?, note=? WHERE id=? AND portfolio_id=?`,
		l.RateBP, l.DueDate, l.Note, l.ID, s.pid)
	if err != nil {
		return err
	}
	return affectedOne(res, "позику з резерву")
}

// DeleteReserveLoan знімає з руху статус позики — сам рух лишається.
// Повернення, привʼязані до неї, відчіпляються: інакше вони показували б
// на порожнечу, а FK лишився б цілим лише в SQLite без foreign_keys.
func (s *Store) DeleteReserveLoan(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE reserve_ops SET loan_id=NULL WHERE loan_id=? AND portfolio_id=?`, id, s.pid); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM reserve_loans WHERE id=? AND portfolio_id=?`, id, s.pid)
	if err != nil {
		return err
	}
	if err := affectedOne(res, "позику з резерву"); err != nil {
		return err
	}
	return tx.Commit()
}

// ListReserveLoans — позики в хронологічному порядку УЗЯТТЯ, бо саме він
// задає чергу FIFO при поверненні.
func (s *Store) ListReserveLoans(ctx context.Context) ([]ReserveLoan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT l.id, l.op_id, l.rate_bp, l.due_date, l.note,
			o.date, o.amount, o.currency
		FROM reserve_loans l JOIN reserve_ops o ON o.id = l.op_id
		WHERE l.portfolio_id=? ORDER BY o.date, l.id`, s.pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReserveLoan
	for rows.Next() {
		var l ReserveLoan
		var dt string
		if err := rows.Scan(&l.ID, &l.OpID, &l.RateBP, &l.DueDate, &l.Note,
			&dt, &l.TakenAmount, &l.TakenCurrency); err != nil {
			return nil, err
		}
		l.TakenDate = domain.Date(dt)
		// Зняття в журналі відʼємне; тіло позики — додатне.
		if l.TakenAmount < 0 {
			l.TakenAmount = -l.TakenAmount
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
