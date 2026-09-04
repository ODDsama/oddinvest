// Профілі імпорту — як читати виписку не від Inzhur (міграція 0036).
//
// Сирі рядки, як усюди в цьому пакеті: перетворення профілю на розбір
// файлу робить internal/imports, а тут лише збереження й читання.
package store

import (
	"context"
	"fmt"
	"strings"
)

// ImportProfile — розкладка колонок чужої виписки.
//
// Індекси 0-based; -1 означає «колонки немає». Аргументи за саме такий
// набір полів — у шапці міграції 0036.
type ImportProfile struct {
	Name   string
	Format string // xlsx | csv
	Header int
	Date   int
	Op     int
	Ref    int
	Qty    int
	Debit  int
	Credit int
	// Balance/MCC — колонки виписки КАРТКИ (0051): залишок після операції
	// та код категорії. DebtID — до якої картки привʼязаний профіль; 0 =
	// не картковий. Довід — у шапці міграції.
	Balance int
	MCC     int
	DebtID  int64
	// Ops — словник рядками «фраза = kind». Зберігається як є: людина
	// пише його, дивлячись у власну виписку, і бачити його мусить теж як
	// є (див. міграцію).
	Ops  string
	Note string
}

const importProfileCols = `name, format, header, col_date, col_op, col_ref,
	col_qty, col_debit, col_credit, col_balance, col_mcc, debt_id, ops, note`

func (s *Store) ListImportProfiles(ctx context.Context) ([]ImportProfile, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+importProfileCols+` FROM import_profiles WHERE portfolio_id=?
		 ORDER BY name COLLATE NOCASE`, s.pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ImportProfile
	for rows.Next() {
		var p ImportProfile
		if err := rows.Scan(&p.Name, &p.Format, &p.Header, &p.Date, &p.Op, &p.Ref,
			&p.Qty, &p.Debit, &p.Credit, &p.Balance, &p.MCC, &p.DebtID, &p.Ops, &p.Note); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetImportProfile — профіль за назвою; nil, коли такого немає.
//
// nil, а не помилка: «профілю немає» — це відповідь, яку викликач має
// перетворити на 404 сам, а не діагностувати з тексту помилки.
func (s *Store) GetImportProfile(ctx context.Context, name string) (*ImportProfile, error) {
	list, err := s.ListImportProfiles(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if strings.EqualFold(list[i].Name, name) {
			return &list[i], nil
		}
	}
	return nil, nil
}

// SaveImportProfile — створення й правка одним запитом.
//
// UPSERT, а не пара INSERT/UPDATE: ключ тут — назва, яку задає людина, і
// «зберегти» для неї означає те саме, що б вона не робила. Той самий
// прийом, що в SaveRate поруч.
func (s *Store) SaveImportProfile(ctx context.Context, p ImportProfile) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("профіль без назви")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO import_profiles
		(portfolio_id, name, format, header, col_date, col_op, col_ref, col_qty, col_debit,
		 col_credit, col_balance, col_mcc, debt_id, ops, note)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(portfolio_id, name) DO UPDATE SET format=excluded.format, header=excluded.header,
			col_date=excluded.col_date, col_op=excluded.col_op, col_ref=excluded.col_ref,
			col_qty=excluded.col_qty, col_debit=excluded.col_debit,
			col_credit=excluded.col_credit, col_balance=excluded.col_balance,
			col_mcc=excluded.col_mcc, debt_id=excluded.debt_id,
			ops=excluded.ops, note=excluded.note`,
		s.pid, strings.TrimSpace(p.Name), p.Format, p.Header, p.Date, p.Op, p.Ref,
		p.Qty, p.Debit, p.Credit, p.Balance, p.MCC, p.DebtID, p.Ops, p.Note)
	return err
}

func (s *Store) DeleteImportProfile(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM import_profiles WHERE portfolio_id=? AND name=?`, s.pid, name)
	return err
}
