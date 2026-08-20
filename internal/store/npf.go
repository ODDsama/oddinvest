// Недержавний пенсійний фонд: довідник рахунків, журнал внесків і точки
// ЧВОПА.
//
// Три таблиці, а не одна, і причини в шапці міграції 0028. Тут важливо
// інше: валідація дат і меж стоїть У СХОВИЩІ, а не лише в обробнику.
// Писати сюди вміє ще й відновлення бекапу, і дата з чужого файла інакше
// лягла б у базу як завгодно, а зламалась би вже в моделі — за три фази
// звідси, де про походження числа вже нічого не відомо. Те саме правило,
// що зафіксоване коментарем до checkFundDate.

package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// npfAccountCols — порядок один на SELECT і на Scan, щоб вони не могли
// розійтись.
func npfAccountCols() string {
	return `id, name, administrator, currency, nav_e6, nav_date,
		expected_yield_bp, yield_simple_years, access_date,
		income_tax_bp, credit_rate_bp, contrib_day, payout_years, payout_freq, note`
}

func scanNPFAccount(rows *sql.Rows) (domain.NPFAccount, error) {
	var a domain.NPFAccount
	var navDate, access string
	if err := rows.Scan(&a.ID, &a.Name, &a.Administrator, &a.Currency,
		&a.Nav, &navDate, &a.ExpectedYieldBP, &a.YieldSimpleYears, &access,
		&a.IncomeTaxBP, &a.CreditRateBP, &a.ContribDay,
		&a.PayoutYears, &a.PayoutFreq, &a.Note); err != nil {
		return a, err
	}
	a.NavDate, a.AccessDate = domain.Date(navDate), domain.Date(access)
	return a, nil
}

// checkNPFAccount — перевірки, спільні для запису й відновлення.
//
// Повертає нормалізований рахунок: обрізані пробіли, валюта за
// замовчуванням, дати у вивіреному вигляді.
func checkNPFAccount(a domain.NPFAccount) (domain.NPFAccount, error) {
	a.Name = strings.TrimSpace(a.Name)
	if a.Name == "" {
		return a, fmt.Errorf("вкажіть назву фонду")
	}
	a.Administrator = strings.TrimSpace(a.Administrator)
	a.Currency = strings.TrimSpace(a.Currency)
	if a.Currency == "" {
		a.Currency = "UAH"
	}
	if a.Nav < 0 {
		return a, fmt.Errorf("ЧВОПА не може бути відʼємною")
	}
	navDate, err := checkFundDate("дата ЧВОПА", string(a.NavDate))
	if err != nil {
		return a, err
	}
	access, err := checkFundDate("дата доступу", string(a.AccessDate))
	if err != nil {
		return a, err
	}
	a.NavDate, a.AccessDate = domain.Date(navDate), domain.Date(access)
	// ЧВОПА без дати — це число, за яким не скажеш, чи воно свіже. Модель
	// порівнює дати, обираючи найсвіжіше джерело, і порожня дата там
	// програла б будь-якому внеску, тобто ручне оновлення тихо не діяло б.
	if a.Nav > 0 && a.NavDate == "" {
		return a, fmt.Errorf("вкажіть дату ЧВОПА")
	}
	if a.ExpectedYieldBP < 0 {
		return a, fmt.Errorf("обіцяна дохідність не може бути відʼємною")
	}
	// Стеля на строк обіцянки — та сама, що у фондів: нуль дав би ділення
	// на нуль у показнику 1/n, а сто років — число, яке нічого не означає.
	if a.YieldSimpleYears < 0 || a.YieldSimpleYears > 50 {
		return a, fmt.Errorf("строк простої дохідності має бути від 1 до 50 років")
	}
	if a.IncomeTaxBP < 0 || a.IncomeTaxBP >= 10000 {
		return a, fmt.Errorf("податок на виплату має бути від 0 до 100%%")
	}
	if a.CreditRateBP < 0 || a.CreditRateBP >= 10000 {
		return a, fmt.Errorf("ставка податкової знижки має бути від 0 до 100%%")
	}
	if a.ContribDay < 0 || a.ContribDay > 31 {
		return a, fmt.Errorf("день внеску має бути від 1 до 31")
	}
	// Стеля на строк виплати — 50 років: більше за це не строк, а
	// описка, а нижня межа нуль означає «разово» (сума нижче межі
	// одноразової виплати).
	if a.PayoutYears < 0 || a.PayoutYears > 50 {
		return a, fmt.Errorf("строк виплати має бути від 0 до 50 років")
	}
	a.PayoutFreq = strings.TrimSpace(a.PayoutFreq)
	if a.PayoutFreq == "" {
		a.PayoutFreq = "month"
	}
	switch a.PayoutFreq {
	case "month", "quarter", "year":
	default:
		return a, fmt.Errorf("періодичність виплати має бути month, quarter або year, маємо %q",
			a.PayoutFreq)
	}
	return a, nil
}

func (s *Store) ListNPFAccounts(ctx context.Context) ([]domain.NPFAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+npfAccountCols()+`
		FROM npf_accounts ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.NPFAccount{}
	for rows.Next() {
		a, err := scanNPFAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) AddNPFAccount(ctx context.Context, a domain.NPFAccount) (int64, error) {
	a, err := checkNPFAccount(a)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO npf_accounts
		(name, administrator, currency, nav_e6, nav_date, expected_yield_bp,
		 yield_simple_years, access_date, income_tax_bp, credit_rate_bp,
		 contrib_day, payout_years, payout_freq, note)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.Name, a.Administrator, a.Currency, a.Nav, string(a.NavDate),
		a.ExpectedYieldBP, a.YieldSimpleYears, string(a.AccessDate),
		a.IncomeTaxBP, a.CreditRateBP, a.ContribDay,
		a.PayoutYears, a.PayoutFreq, a.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateNPFAccount — правка рахунку БЕЗ видалення: внески й точки ЧВОПА
// тримаються за id, тож видалити й створити заново означало б осиротити
// журнал. Те саме, що з RenameFund.
func (s *Store) UpdateNPFAccount(ctx context.Context, a domain.NPFAccount) error {
	a, err := checkNPFAccount(a)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE npf_accounts SET
		name=?, administrator=?, currency=?, nav_e6=?, nav_date=?,
		expected_yield_bp=?, yield_simple_years=?, access_date=?,
		income_tax_bp=?, credit_rate_bp=?, contrib_day=?,
		payout_years=?, payout_freq=?, note=?
		WHERE id=?`,
		a.Name, a.Administrator, a.Currency, a.Nav, string(a.NavDate),
		a.ExpectedYieldBP, a.YieldSimpleYears, string(a.AccessDate),
		a.IncomeTaxBP, a.CreditRateBP, a.ContribDay,
		a.PayoutYears, a.PayoutFreq, a.Note, a.ID)
	if err != nil {
		return err
	}
	return affectedOne(res, "пенсійний рахунок")
}

// SetNPFNav — оновити останню відому ЧВОПА, не чіпаючи решти рахунку.
//
// Окремо від UpdateNPFAccount навмисно: це найчастіша дія («переписав
// число з кабінету»), і вимагати для неї повного тіла рахунку означало б
// або дати формі перезаписати поля, яких вона не показує, або змусити її
// знати про всі дванадцять.
func (s *Store) SetNPFNav(ctx context.Context, id, navE6 int64, on domain.Date) error {
	if navE6 <= 0 {
		return fmt.Errorf("ЧВОПА має бути додатною")
	}
	date, err := checkFundDate("дата ЧВОПА", string(on))
	if err != nil {
		return err
	}
	if date == "" {
		return fmt.Errorf("вкажіть дату ЧВОПА")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE npf_accounts SET nav_e6=?, nav_date=? WHERE id=?`, navE6, date, id)
	if err != nil {
		return err
	}
	return affectedOne(res, "пенсійний рахунок")
}

// DeleteNPFAccount — разом із журналом і точками: FK на npf_accounts є, а
// каскаду в схемі немає, тож порядок тут не косметичний.
func (s *Store) DeleteNPFAccount(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM npf_ops WHERE npf_id=?`, id); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM npf_nav WHERE npf_id=?`, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM npf_accounts WHERE id=?`, id)
	return err
}

// --- журнал внесків ---

func checkNPFOp(o domain.NPFOp) (domain.NPFOp, error) {
	if o.NPFID <= 0 {
		return o, fmt.Errorf("вкажіть пенсійний рахунок")
	}
	if _, err := checkFundDate("дата внеску", string(o.Date)); err != nil {
		return o, err
	}
	if o.Date == "" {
		return o, fmt.Errorf("вкажіть дату внеску")
	}
	// Обидва додатні: іншого напрямку в цього журналу немає. Нуль одиниць
	// при ненульовій сумі — не «внесок без одиниць», а втрачений рядок
	// виписки: з нього не вивести ні ЧВОПА, ні залишку.
	if o.Amount <= 0 {
		return o, fmt.Errorf("сума внеску має бути додатною")
	}
	if o.Units <= 0 {
		return o, fmt.Errorf("вкажіть, скільки одиниць зараховано")
	}
	o.Broker = strings.TrimSpace(o.Broker)
	o.Note = strings.TrimSpace(o.Note)
	return o, nil
}

func (s *Store) ListNPFOps(ctx context.Context) ([]domain.NPFOp, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.id, o.npf_id, o.date,
		o.units_e6, o.amount, COALESCE(b.name,''), o.note
		FROM npf_ops o LEFT JOIN brokers b ON b.id = o.broker_id
		ORDER BY o.date, o.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.NPFOp{}
	for rows.Next() {
		var o domain.NPFOp
		var d string
		if err := rows.Scan(&o.ID, &o.NPFID, &d, &o.Units, &o.Amount,
			&o.Broker, &o.Note); err != nil {
			return nil, err
		}
		o.Date = domain.Date(d)
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) AddNPFOp(ctx context.Context, o domain.NPFOp) (int64, error) {
	o, err := checkNPFOp(o)
	if err != nil {
		return 0, err
	}
	broker, err := s.brokerRef(ctx, o.Broker)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO npf_ops
		(npf_id, date, units_e6, amount, broker_id, note) VALUES (?,?,?,?,?,?)`,
		o.NPFID, string(o.Date), o.Units, o.Amount, broker, o.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateNPFOp(ctx context.Context, o domain.NPFOp) error {
	o, err := checkNPFOp(o)
	if err != nil {
		return err
	}
	broker, err := s.brokerRef(ctx, o.Broker)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE npf_ops SET
		npf_id=?, date=?, units_e6=?, amount=?, broker_id=?, note=? WHERE id=?`,
		o.NPFID, string(o.Date), o.Units, o.Amount, broker, o.Note, o.ID)
	if err != nil {
		return err
	}
	return affectedOne(res, "внесок")
}

func (s *Store) DeleteNPFOp(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM npf_ops WHERE id=?`, id)
	return err
}

// --- точки ЧВОПА ---

func (s *Store) ListNPFNav(ctx context.Context) ([]domain.NPFNav, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, npf_id, date, nav_e6 FROM npf_nav ORDER BY npf_id, date`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.NPFNav{}
	for rows.Next() {
		var n domain.NPFNav
		var d string
		if err := rows.Scan(&n.ID, &n.NPFID, &d, &n.Nav); err != nil {
			return nil, err
		}
		n.Date = domain.Date(d)
		out = append(out, n)
	}
	return out, rows.Err()
}

// AddNPFNavPoints — вклеїти пачку точок ЧВОПА.
//
// Пачкою, а не по одній, бо саме так їх і беруть: опублікована фондом
// історія — це таблиця за багато років, і заводити її по рядку означало б
// не заводити зовсім.
//
// Одна транзакція на всю пачку: половина вклеєної історії гірша за жодну —
// на ній порахувалась би дохідність за відрізок, якого ніхто не вибирав.
// UPSERT за (npf_id, date) з тієї ж причини, що UNIQUE у схемі: дві різні
// ЧВОПА на один день — не історія, а суперечність, і остання виграє.
func (s *Store) AddNPFNavPoints(ctx context.Context, npfID int64, pts []domain.NPFNav) (int, error) {
	if npfID <= 0 {
		return 0, fmt.Errorf("вкажіть пенсійний рахунок")
	}
	for i, p := range pts {
		if _, err := checkFundDate("дата ЧВОПА", string(p.Date)); err != nil {
			return 0, fmt.Errorf("рядок %d: %w", i+1, err)
		}
		if p.Date == "" {
			return 0, fmt.Errorf("рядок %d: вкажіть дату", i+1)
		}
		if p.Nav <= 0 {
			return 0, fmt.Errorf("рядок %d: ЧВОПА має бути додатною", i+1)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // Commit нижче; тут відкат уже некерований.
	for _, p := range pts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO npf_nav (npf_id, date, nav_e6)
			VALUES (?,?,?) ON CONFLICT(npf_id, date) DO UPDATE SET nav_e6=excluded.nav_e6`,
			npfID, string(p.Date), p.Nav); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(pts), nil
}

// UpdateNPFNavPoint переписує одну точку ЧВОПА, зберігаючи id і рахунок.
//
// Крива ЧВОПА — найдорожчі за працею дані в базі (їх заводять руками з
// звітів фонду, рік за роком), і доти єдиним способом виправити одну
// одруківку було видалити точку й вписати заново. Одна зайва дія на
// найдорожчих даних — саме там, де ціна помилки найвища.
//
// Дата входить в UNIQUE(npf_id, date), тож перенесення точки на зайняту
// дату — не помилка вводу, а зіткнення з уже наявним записом, і воно має
// називатись своїм ім'ям: ErrConflict дає 409, а не 400.
func (s *Store) UpdateNPFNavPoint(ctx context.Context, n domain.NPFNav) error {
	if n.Nav <= 0 {
		return fmt.Errorf("ЧВОПА має бути додатною")
	}
	if _, err := checkFundDate("дата ЧВОПА", string(n.Date)); err != nil {
		return err
	}
	var busy int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM npf_nav WHERE date=? AND id<>?
			AND npf_id=(SELECT npf_id FROM npf_nav WHERE id=?)`,
		string(n.Date), n.ID, n.ID).Scan(&busy); err != nil {
		return err
	}
	if busy > 0 {
		return fmt.Errorf("точка на %s уже є: %w", n.Date, ErrConflict)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE npf_nav SET date=?, nav_e6=? WHERE id=?`, string(n.Date), n.Nav, n.ID)
	if err != nil {
		return err
	}
	return affectedOne(res, "точка ЧВОПА")
}

func (s *Store) DeleteNPFNavPoint(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM npf_nav WHERE id=?`, id)
	if err != nil {
		return err
	}
	return affectedOne(res, "точка ЧВОПА")
}
