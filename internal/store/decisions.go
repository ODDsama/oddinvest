// Журнал рішень — знімок рейтингу помічника на момент купівлі
// (міграція 0035).
//
// Сирі рядки, як plan_buys поруч: тут лише збереження й читання, а
// зіставлення з фактом (дохідність за фактом, зведення по режимах)
// робить internal/api/handlers_decisions.go. Причина та сама, що й там:
// «за фактом» — величина, яка змінюється щодня, і місце їй у відповіді,
// а не в таблиці.
//
// Правки й видалення тут немає, і це не пропуск КРУДу (CLAUDE.md §2).
// Правило «де є операції — там повний КРУД» стосується того, що людина
// ЗАВОДИТЬ: одруківку в лоті треба вміти виправити. Рішення ніхто не
// заводить — воно фіксується самим фактом купівлі, і «виправити» його
// означало б переписати минуле. Помилковий рядок зникає разом зі своєю
// причиною: видали лот — і в журналі просто не буде на що дивитись.
package store

import (
	"context"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Decision — одне рішення й те, що радив помічник у той момент.
type Decision struct {
	ID     int64
	MadeOn domain.Date
	// Kind і Ref — у тих самих словах, що PlanBuy: bond → ISIN,
	// fund → назва фонду, deposit → банк, npf → назва рахунку.
	Kind     string
	Ref      string
	Currency string
	Amount   int64 // мінорні
	// RealPct — обіцянка обраного на момент рішення, %.
	RealPct float64
	// RankPos — яким рядком обране стояло в рейтингу, 1 = верхній.
	// TopLabel порожній, коли обране й було першим.
	RankPos    int
	TopLabel   string
	TopRealPct float64
	RankMode   string
	OpID       int64
	Note       string
}

const decisionCols = `id, made_on, kind, ref, currency, amount, real_pct,
	rank_pos, top_label, top_real_pct, rank_mode, op_id, note`

// ListDecisions — журнал у хронологічному порядку.
func (s *Store) ListDecisions(ctx context.Context) ([]Decision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+decisionCols+` FROM decisions ORDER BY made_on, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Decision
	for rows.Next() {
		var d Decision
		var made string
		if err := rows.Scan(&d.ID, &made, &d.Kind, &d.Ref, &d.Currency, &d.Amount,
			&d.RealPct, &d.RankPos, &d.TopLabel, &d.TopRealPct, &d.RankMode,
			&d.OpID, &d.Note); err != nil {
			return nil, err
		}
		d.MadeOn = domain.Date(made)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) AddDecision(ctx context.Context, d Decision) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO decisions
		(made_on, kind, ref, currency, amount, real_pct,
		 rank_pos, top_label, top_real_pct, rank_mode, op_id, note)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		string(d.MadeOn), d.Kind, d.Ref, d.Currency, d.Amount, d.RealPct,
		d.RankPos, d.TopLabel, d.TopRealPct, d.RankMode, d.OpID, d.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
