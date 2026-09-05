package store

import (
	"context"
	"database/sql"
)

// CPIPoint — опублікована НБУ пара за один звітний місяць.
//
// Окремий тип поруч із RatePoint, а не спільний «ряд чисел»: у курсу одна
// величина на дату, тут дві, і друга (річна) існує саме для того, щоб
// звіряти першу. Звести їх в одну сутність означало б або втратити
// звірку, або завести в курсів поле, якого в них немає.
type CPIPoint struct {
	Period string // 'YYYY-MM', звітний місяць
	MoMBP  int64  // % до попереднього місяця × 100
	YoYBP  int64  // % до того самого місяця торік × 100
}

// SaveCPI пише точку ідемпотентно: бекфіл і добова джоба перекриваються
// на межі, і перезапис тим самим числом мусить бути дешевою нормою, а не
// приводом розрізняти «вставити» й «оновити» на рівні викликача.
func (s *Store) SaveCPI(ctx context.Context, p CPIPoint) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO cpi_points(period, mom_bp, yoy_bp) VALUES(?,?,?)
		ON CONFLICT(period) DO UPDATE SET mom_bp=excluded.mom_bp, yoy_bp=excluded.yoy_bp`,
		p.Period, p.MoMBP, p.YoYBP)
	return err
}

// NewestCPI — найсвіжіша наявна точка. Порожній Period означає, що ряду
// немає взагалі: джоба з цього починає бекфіл, а картка на цьому мовчить.
func (s *Store) NewestCPI(ctx context.Context) (CPIPoint, error) {
	var p CPIPoint
	err := s.db.QueryRowContext(ctx,
		`SELECT period, mom_bp, yoy_bp FROM cpi_points ORDER BY period DESC LIMIT 1`).
		Scan(&p.Period, &p.MoMBP, &p.YoYBP)
	if err == sql.ErrNoRows {
		return CPIPoint{}, nil
	}
	return p, err
}

// CPISince — увесь ряд від місяця й донині, за зростанням.
//
// Віддається ЦІЛКОМ і без жодної арифметики в SQL — з того самого
// міркування, що й RatesSince поруч: ланцюг рівнів, річний темп і
// перцентиль живуть у domain, де їх видно й тестують. Обсяг малий і
// лишиться малим: один рядок на місяць, ~120 за десять років.
//
// Порожній from означає «усе, що є».
func (s *Store) CPISince(ctx context.Context, from string) ([]CPIPoint, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT period, mom_bp, yoy_bp FROM cpi_points WHERE (?='' OR period>=?) ORDER BY period`,
		from, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CPIPoint
	for rows.Next() {
		var p CPIPoint
		if err := rows.Scan(&p.Period, &p.MoMBP, &p.YoYBP); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CPIMonthCount — скільки місяців у ряду. Тут це просто кількість рядків
// (ключ і є місяць), і окремий метод існує заради того самого, заради
// чого RateMonthCount: рішення «чи потрібен бекфіл» мусить дивитись на
// довжину ІСТОРІЇ, а не на факт наявності бодай чогось.
func (s *Store) CPIMonthCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cpi_points`).Scan(&n)
	return n, err
}
