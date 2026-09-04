package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// Портфелі (0054): вимір «чий» над усіма користувацькими таблицями.
//
// Довідник тут МАЛИЙ навмисно — id, slug, назва. Стратегія, брокери,
// подушка й борги портфеля живуть у своїх таблицях під його portfolio_id,
// а не в колонках цього рядка: портфель — це не набір параметрів, а
// простір, у якому лежить усе інше. Тому й немає тут «поточного» портфеля:
// хто поточний, вирішує запит (заголовок X-Portfolio), а не база.
//
// Методи цього файла — ЄДИНІ, що не звужуються до s.pid: вони про перелік
// портфелів, а не про вміст одного з них.

type Portfolio struct {
	ID        int64  `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// slugRe — латиниця, цифри, дефіс, до 32 знаків. Slug іде в заголовок HTTP
// і в адресу (?p=…), де українська назва не живе; сам він людині не
// показується — для цього є Name.
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// checkPortfolio — перевірка в сховищі, а не лише в обробнику, з того самого
// доводу, що й checkFundDate: писати сюди може не лише форма.
func checkPortfolio(slug, name string) (string, string, error) {
	slug = strings.TrimSpace(slug)
	name = strings.TrimSpace(name)
	if !slugRe.MatchString(slug) {
		return "", "", fmt.Errorf("ідентифікатор портфеля: латинські літери, цифри й дефіс, до 32 знаків; маємо %q", slug)
	}
	if name == "" {
		return "", "", fmt.Errorf("портфель без назви")
	}
	return slug, name, nil
}

func (s *Store) ListPortfolios(ctx context.Context) ([]Portfolio, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, slug, name, created_at FROM portfolios ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Portfolio{}
	for rows.Next() {
		var p Portfolio
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PortfolioBySlug — портфель за ідентифікатором; nil, коли такого немає.
// nil, а не помилка: «портфеля немає» — це відповідь 404, яку викликач має
// дати сам (той самий довід, що в GetImportProfile).
func (s *Store) PortfolioBySlug(ctx context.Context, slug string) (*Portfolio, error) {
	var p Portfolio
	err := s.db.QueryRowContext(ctx,
		`SELECT id, slug, name, created_at FROM portfolios WHERE slug=?`, slug).
		Scan(&p.ID, &p.Slug, &p.Name, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// AddPortfolio — новий порожній портфель. Порожній СПРАВДІ: без
// налаштувань, без брокерів, без import_since — усе це його власник
// заводить сам, стратегію зокрема (заради іншої стратегії портфелі й
// існують). Повторний slug — ErrConflict, а не текст SQLite.
func (s *Store) AddPortfolio(ctx context.Context, slug, name string) (int64, error) {
	slug, name, err := checkPortfolio(slug, name)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO portfolios(slug, name) VALUES(?,?)`, slug, name)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return 0, fmt.Errorf("портфель %q уже є: %w", slug, ErrConflict)
		}
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) RenamePortfolio(ctx context.Context, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("портфель без назви")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE portfolios SET name=? WHERE id=?`, name, id)
	if err != nil {
		return err
	}
	return affectedOne(res, "портфель")
}

// DeletePortfolio — стерти портфель РАЗОМ з усім його вмістом.
//
// Один DELETE, а не перелік таблиць: кожна колонка portfolio_id іде з
// ON DELETE CASCADE (0054), і TestScopedTablesMatchSchema стоїть на тому,
// що жодна без нього не лишилась. Перелік тут означав би два джерела
// правди про те, що таке «вміст портфеля».
//
// Головний портфель не стирається: у нього дивиться Home Assistant, і саме
// його дані міграція перевела з одноосібних часів. Порожнім його робить
// restore порожнього бекапу, а не видалення.
func (s *Store) DeletePortfolio(ctx context.Context, id int64) error {
	if id == MainPortfolio {
		return fmt.Errorf("головний портфель не стирається")
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM portfolios WHERE id=?`, id)
	if err != nil {
		return err
	}
	return affectedOne(res, "портфель")
}
