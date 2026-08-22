// Позначки ціни сертифіката (0034): CRUD над fund_prices.
//
// Валідація стоїть тут, а не лише в обробнику, з тієї ж причини, що й у
// refs.go: писати в цю таблицю вміє ще й відновлення бекапу, і ціна з
// чужого файла інакше лягла б у базу як завгодно, а зламалась би вже в
// моделі, за три фази звідси.
package store

import (
	"context"
	"fmt"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// ListFundPrices — усі позначки з назвою фонду.
//
// Назва береться JOIN'ом, як у ListFundOps: сховище тримається за fund_id,
// домен працює за назвою, і межа між ними лишається там, де стояла. FundID
// при цьому теж віддається — його читає екран довідника, де рядок
// фільтрується саме за id (він переживає перейменування, а назва ні).
func (s *Store) ListFundPrices(ctx context.Context) ([]domain.FundPrice, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, p.fund_id, f.name, p.date, p.price_e4
		FROM fund_prices p JOIN funds f ON f.id = p.fund_id
		ORDER BY f.name COLLATE NOCASE, p.date`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.FundPrice{}
	for rows.Next() {
		var p domain.FundPrice
		var d string
		if err := rows.Scan(&p.ID, &p.FundID, &p.Fund, &d, &p.Price); err != nil {
			return nil, err
		}
		p.Date = domain.Date(d)
		out = append(out, p)
	}
	return out, rows.Err()
}

// checkFundPrice — спільна перевірка однієї точки.
func checkFundPrice(p domain.FundPrice) (string, error) {
	d, err := checkFundDate("дата ціни", string(p.Date))
	if err != nil {
		return "", err
	}
	if d == "" {
		return "", fmt.Errorf("вкажіть дату")
	}
	if p.Price <= 0 {
		return "", fmt.Errorf("ціна має бути додатною")
	}
	return d, nil
}

// AddFundPricePoints — вклеїти пачку позначок.
//
// Пачкою, а не по одній, бо саме так їх і беруть: опублікована фондом
// історія — це таблиця, і заводити її по рядку означало б не заводити
// зовсім. Одна позначка «на сьогодні» — та сама операція з одним рядком, і
// другого методу заради неї немає.
//
// Одна транзакція на всю пачку: половина вклеєної історії гірша за жодну —
// на ній порахувалась би дохідність за відрізок, якого ніхто не вибирав.
// UPSERT за (fund_id, date) з тієї ж причини, що UNIQUE у схемі: дві різні
// ціни на один день — не історія, а суперечність, і остання виграє.
func (s *Store) AddFundPricePoints(ctx context.Context, fundID int64, pts []domain.FundPrice) (int, error) {
	if fundID <= 0 {
		return 0, fmt.Errorf("вкажіть фонд")
	}
	dates := make([]string, len(pts))
	for i, p := range pts {
		d, err := checkFundPrice(p)
		if err != nil {
			return 0, fmt.Errorf("рядок %d: %w", i+1, err)
		}
		dates[i] = d
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit нижче; тут відкат уже некерований.
	for i, p := range pts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO fund_prices (fund_id, date, price_e4)
			VALUES (?,?,?) ON CONFLICT(fund_id, date) DO UPDATE SET price_e4=excluded.price_e4`,
			fundID, dates[i], p.Price); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(pts), nil
}

// UpdateFundPricePoint переписує одну позначку, зберігаючи id і фонд.
//
// Дата входить в UNIQUE(fund_id, date), тож перенесення точки на зайняту
// дату — не помилка вводу, а зіткнення з уже наявним записом, і воно має
// називатись своїм ім'ям: ErrConflict дає 409, а не 400. Дзеркалить
// UpdateNPFNavPoint.
func (s *Store) UpdateFundPricePoint(ctx context.Context, p domain.FundPrice) error {
	d, err := checkFundPrice(p)
	if err != nil {
		return err
	}
	var busy int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM fund_prices WHERE date=? AND id<>?
			AND fund_id=(SELECT fund_id FROM fund_prices WHERE id=?)`,
		d, p.ID, p.ID).Scan(&busy); err != nil {
		return err
	}
	if busy > 0 {
		return fmt.Errorf("позначка на %s уже є: %w", d, ErrConflict)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE fund_prices SET date=?, price_e4=? WHERE id=?`, d, p.Price, p.ID)
	if err != nil {
		return err
	}
	return affectedOne(res, "позначка ціни")
}

func (s *Store) DeleteFundPricePoint(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM fund_prices WHERE id=?`, id)
	if err != nil {
		return err
	}
	return affectedOne(res, "позначка ціни")
}
