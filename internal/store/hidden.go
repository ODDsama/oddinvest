// Приховані рядки майстер-списку — те, що власник відмітив «не показувати».
//
// Довід, чому це власна портфельна таблиця, а не ключ у app_state поруч із
// порядком рядків, — у міграції 0060. Тут лише два методи: список і заміна
// списку цілком.
//
// ЗАМІНА ЦІЛКОМ, А НЕ ДОДАВАННЯ/ВИДАЛЕННЯ ПО ОДНОМУ. Питання, яке ставить
// UI, звучить «ось що тепер приховано», а не «сховай ще оцей»: у режимі
// видимості перемикають кілька рядків підряд, і кожен хід летить на бекенд
// цілим списком (так само, як порядок рядків). Дві ручки замість однієї
// коштували б узгодження станів там, де його нема потреби мати.
package store

import (
	"context"
	"fmt"
)

// HiddenRows — ідентифікатори прихованих рядків цього портфеля.
//
// Порядок сталий (за row_id), хоч UI на нього й не дивиться: без ORDER BY
// SQLite вільний віддати рядки як завгодно, і тест, що звіряє списки, почав
// би моргати не через код.
func (s *Store) HiddenRows(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT row_id FROM hidden_rows WHERE portfolio_id=? ORDER BY row_id`, s.pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Порожній зріз, а не nil: значення їде в JSON, і nil дав би там null
	// замість [] — тобто клієнт мусив би вміти обидва.
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetHiddenRows — замінити список прихованих рядків цього портфеля.
//
// Атомарно: половина набору гірша за відмову цілком — рівно той самий
// довід, що в handlers_settings.go, лише тут ще й дешевший, бо записів
// одиниці.
func (s *Store) SetHiddenRows(ctx context.Context, ids []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // після Commit це no-op, а до нього — саме те, що треба
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM hidden_rows WHERE portfolio_id=?`, s.pid); err != nil {
		return fmt.Errorf("очищення прихованих рядків: %w", err)
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO hidden_rows (portfolio_id, row_id) VALUES (?,?)`, s.pid, id); err != nil {
			return fmt.Errorf("прихований рядок %q: %w", id, err)
		}
	}
	return tx.Commit()
}
