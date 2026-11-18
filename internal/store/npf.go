// Недержавний пенсійний фонд: довідник фондів, рахунки в них, журнал
// внесків і історія ЧВОПА.
//
// Чотири таблиці, і межа між ними — «що воно таке» проти «що я маю»
// (шапка 0070):
//
//   - npf_funds і npf_fund_nav — СПІЛЬНІ для всіх портфелів: назва,
//     адміністратор, валюта, остання відома ЧВОПА, обіцяна дохідність та
//     історія ЧВОПА. Ціна одиниці у фонду одна на всіх учасників, і два
//     портфелі з тим самим фондом не мусять вводити її двічі;
//   - npf_accounts і npf_ops — портфельні: участь людини у фонді (дата
//     доступу, податки, день внеску, виплата, нотатка) та її внески.
//
// Модель при цьому бачить те саме, що й доти: domain.NPFAccount лишився
// пласким, сховище збирає його з рахунку й фонду одним JOIN, а точки
// історії віддає з NPFID рахунку ЦЬОГО портфеля. Спільність — властивість
// схеми, а не річ, про яку мусить знати кожен читач.
//
// Наслідок, який треба памʼятати: правка назви, адміністратора, валюти,
// ЧВОПА чи обіцянки з одного портфеля видна в усіх, де цей фонд є.
//
// Фонд видно ЛИШЕ через рахунок: окремого екрана довідника НПФ немає, тож
// портфель без рахунку у фонді не читає й не править його історію. Це
// суворіше, ніж у fund_prices (там правка за самим id), і навмисно: каталог
// фондів відкритий усім, а тут угаданий id дав би правити чужу криву.
//
// Валідація дат і меж стоїть У СХОВИЩІ, а не лише в обробнику.
// Писати сюди вміє ще й відновлення бекапу, і дата з чужого файла інакше
// лягла б у базу як завгодно, а зламалась би вже в моделі — за три фази
// звідси, де про походження числа вже нічого не відомо. Те саме правило,
// що зафіксоване коментарем до checkFundDate.

package store

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// npfAccountCols — порядок один на SELECT і на Scan, щоб вони не могли
// розійтись. Колонки з префіксами: `a` — рахунок (npf_accounts), `f` — його
// фонд (npf_funds); запит мусить звести їх саме під цими іменами.
func npfAccountCols() string {
	return `a.id, f.name, f.administrator, f.currency, f.nav_e6, f.nav_date,
		f.expected_yield_bp, f.yield_simple_years, a.access_date,
		a.income_tax_bp, a.credit_rate_bp, a.contrib_day, a.payout_years, a.payout_freq, a.note`
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
	a.Currency = cmp.Or(a.Currency, "UAH")
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
	a.PayoutFreq = cmp.Or(a.PayoutFreq, "month")
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
		FROM npf_accounts a JOIN npf_funds f ON f.id = a.fund_id
		WHERE a.portfolio_id=? ORDER BY f.name COLLATE NOCASE`, s.pid)
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

// npfFundRefIn — id фонду за назвою; якого немає — заводиться з атрибутами
// з `a`. Зведення за ТОЧНОЮ назвою, як у funds (шапка 0070 каже чому).
//
// overwrite вирішує, що робити з наявним фондом, і двоє викликачів
// відповідають по-різному:
//
//   - AddNPFAccount — false. Фонд уже веде інший портфель, і форма «додати
//     рахунок» не має мовчки переписати йому адміністратора чи ЧВОПА тим,
//     що хтось набрав навздогад: властивості фонду задає той, хто його
//     завів, а правлять їх явно — правкою рахунку;
//   - ImportAll — true. Відновлення заміщує: файл везе стан фонду на мить
//     дампа, і саме його людина й просила повернути (той самий контракт,
//     що в циклі по b.Funds).
func npfFundRefIn(ctx context.Context, tx *sql.Tx, a domain.NPFAccount, overwrite bool) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM npf_funds WHERE name=?`, a.Name).Scan(&id)
	if err == sql.ErrNoRows {
		res, err := tx.ExecContext(ctx, `INSERT INTO npf_funds
			(name, administrator, currency, nav_e6, nav_date, expected_yield_bp, yield_simple_years)
			VALUES (?,?,?,?,?,?,?)`,
			a.Name, a.Administrator, a.Currency, a.Nav, string(a.NavDate),
			a.ExpectedYieldBP, a.YieldSimpleYears)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	if err != nil || !overwrite {
		return id, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE npf_funds SET administrator=?, currency=?,
		nav_e6=?, nav_date=?, expected_yield_bp=?, yield_simple_years=? WHERE id=?`,
		a.Administrator, a.Currency, a.Nav, string(a.NavDate),
		a.ExpectedYieldBP, a.YieldSimpleYears, id)
	return id, err
}

// npfFundOfIn — фонд рахунку ЦЬОГО портфеля. Чужий або неіснуючий рахунок —
// ErrNotFound: через нього всі записи у спільні таблиці й дізнаються, чи
// має портфель на них право.
func (s *Store) npfFundOfIn(ctx context.Context, q querier, accountID int64) (int64, error) {
	var fundID int64
	err := q.QueryRowContext(ctx,
		`SELECT fund_id FROM npf_accounts WHERE id=? AND portfolio_id=?`, accountID, s.pid).Scan(&fundID)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("пенсійний рахунок %d %w", accountID, ErrNotFound)
	}
	return fundID, err
}

// AddNPFAccount — рахунок цього портфеля у фонді з назвою a.Name.
//
// Фонд, якого ще немає, заводиться з атрибутами з `a`. Фонд, який уже веде
// інший портфель, ПІДХОПЛЮЄТЬСЯ за назвою — разом з адміністратором,
// валютою, ЧВОПА, її історією й обіцянкою; те, що прийшло в `a` про фонд,
// у цьому разі не діє (npfFundRefIn каже чому). Другий рахунок у тому
// самому фонді в одному портфелі — ErrConflict, а не текст SQLite про
// UNIQUE.
func (s *Store) AddNPFAccount(ctx context.Context, a domain.NPFAccount) (int64, error) {
	a, err := checkNPFAccount(a)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op після Commit
	fundID, err := npfFundRefIn(ctx, tx, a, false)
	if err != nil {
		return 0, err
	}
	var busy int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM npf_accounts WHERE fund_id=? AND portfolio_id=?`, fundID, s.pid).Scan(&busy); err != nil {
		return 0, err
	}
	if busy > 0 {
		return 0, fmt.Errorf("рахунок у фонді %q уже є: %w", a.Name, ErrConflict)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO npf_accounts
		(portfolio_id, fund_id, access_date, income_tax_bp, credit_rate_bp,
		 contrib_day, payout_years, payout_freq, note)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		s.pid, fundID, string(a.AccessDate), a.IncomeTaxBP, a.CreditRateBP,
		a.ContribDay, a.PayoutYears, a.PayoutFreq, a.Note)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// UpdateNPFAccount — правка рахунку БЕЗ видалення: внески тримаються за
// id, тож видалити й створити заново означало б осиротити журнал. Те саме,
// що з RenameFund.
//
// Пише у дві таблиці: спільне (назва, адміністратор, валюта, ЧВОПА,
// обіцянка) — у фонд, тобто для ВСІХ портфелів, де він є; особисте — у
// рахунок. Перейменувати фонд на назву іншого фонду не можна: це було б
// злиттям двох довідкових рядків з їхніми історіями, а такої дії немає
// (шапка 0070) — ErrConflict.
func (s *Store) UpdateNPFAccount(ctx context.Context, a domain.NPFAccount) error {
	a, err := checkNPFAccount(a)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op після Commit
	fundID, err := s.npfFundOfIn(ctx, tx, a.ID)
	if err != nil {
		return err
	}
	var busy int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM npf_funds WHERE name=? AND id<>?`, a.Name, fundID).Scan(&busy); err != nil {
		return err
	}
	if busy > 0 {
		return fmt.Errorf("фонд із назвою %q уже є, а обʼєднання фондів немає: %w", a.Name, ErrConflict)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE npf_funds SET
		name=?, administrator=?, currency=?, nav_e6=?, nav_date=?,
		expected_yield_bp=?, yield_simple_years=? WHERE id=?`,
		a.Name, a.Administrator, a.Currency, a.Nav, string(a.NavDate),
		a.ExpectedYieldBP, a.YieldSimpleYears, fundID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE npf_accounts SET
		access_date=?, income_tax_bp=?, credit_rate_bp=?, contrib_day=?,
		payout_years=?, payout_freq=?, note=?
		WHERE id=? AND portfolio_id=?`,
		string(a.AccessDate), a.IncomeTaxBP, a.CreditRateBP, a.ContribDay,
		a.PayoutYears, a.PayoutFreq, a.Note, a.ID, s.pid); err != nil {
		return err
	}
	return tx.Commit()
}

// SetNPFNav — оновити останню відому ЧВОПА, не чіпаючи решти рахунку.
//
// Окремо від UpdateNPFAccount навмисно: це найчастіша дія («переписав
// число з кабінету»), і вимагати для неї повного тіла рахунку означало б
// або дати формі перезаписати поля, яких вона не показує, або змусити її
// знати про всі дванадцять.
//
// ЧВОПА — властивість фонду (0070): число, оновлене з одного портфеля,
// бачать усі, де цей фонд є. id лишається id РАХУНКУ — ним портфель і
// доводить, що фонд його стосується.
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
		`UPDATE npf_funds SET nav_e6=?, nav_date=? WHERE id=
			(SELECT fund_id FROM npf_accounts WHERE id=? AND portfolio_id=?)`, navE6, date, id, s.pid)
	if err != nil {
		return err
	}
	return affectedOne(res, "пенсійний рахунок")
}

// DeleteNPFAccount — разом із журналом внесків: FK на npf_accounts є, а
// каскаду в схемі немає, тож порядок тут не косметичний.
//
// Фонд та історія ЧВОПА йдуть слідом ЛИШЕ тоді, коли це був останній
// рахунок у фонді в усій базі (pruneOrphanNPFFundsIn): сусідньому портфелю
// його крива лишається.
//
// Однією транзакцією: окремі DELETE, обірвані посередині, лишали б
// рахунок без журналу — тобто позицію з нулем одиниць, яка виглядає як
// справжня.
func (s *Store) DeleteNPFAccount(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op після Commit
	if _, err := tx.ExecContext(ctx, `DELETE FROM npf_ops WHERE npf_id=? AND portfolio_id=?`, id, s.pid); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM npf_accounts WHERE id=? AND portfolio_id=?`, id, s.pid); err != nil {
		return err
	}
	if err := pruneOrphanNPFFundsIn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// pruneOrphanNPFFundsIn — витерти фонди НПФ (з історією ЧВОПА), у яких не
// лишилось жодного рахунку в ЖОДНОМУ портфелі. Дзеркало pruneOrphanFundsIn,
// і запит так само дивиться на рахунки всіх портфелів навмисно (виняток у
// сторожі scope_guard_test.go).
//
// Сирота тут гірший, ніж у фондів. Каталог фондів має свій екран, і зайвий
// рядок у ньому видно й можна стерти. Фонд НПФ видно лише через рахунок:
// без рахунку він невидимий, непідвладний жодній дії — і тримає назву, тож
// наступний рахунок із такою назвою мовчки успадкував би чужу стару ЧВОПА.
//
// Викликачів три, і кожен — місце, де рахунок може зникнути:
// DeleteNPFAccount, ImportAll (після очищення портфеля) і DeletePortfolio
// (каскад забирає рахунки, а фонд каскаду не має). Тригер у схемі зробив би
// те саме одним рядком, але сховав би поведінку від читача коду.
func pruneOrphanNPFFundsIn(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM npf_fund_nav
		WHERE fund_id NOT IN (SELECT fund_id FROM npf_accounts)`); err != nil {
		return fmt.Errorf("очищення історії ЧВОПА: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM npf_funds
		WHERE id NOT IN (SELECT fund_id FROM npf_accounts)`); err != nil {
		return fmt.Errorf("очищення довідника НПФ: %w", err)
	}
	return nil
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
		WHERE o.portfolio_id=?
		ORDER BY o.date, o.id`, s.pid)
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
	// Рахунок чужого портфеля мусить читатись як ErrNotFound, а не тихо
	// прийняти внесок: FK на npf_accounts портфеля не знає.
	if err := s.ownsRow(ctx, "npf_accounts", o.NPFID); err != nil {
		return 0, err
	}
	broker, err := s.brokerRef(ctx, o.Broker)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO npf_ops
		(portfolio_id, npf_id, date, units_e6, amount, broker_id, note) VALUES (?,?,?,?,?,?,?)`,
		s.pid, o.NPFID, string(o.Date), o.Units, o.Amount, broker, o.Note)
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
	// Той самий довід, що в AddNPFOp: чужий рахунок — ErrNotFound, а не
	// мовчазний успіх.
	if err := s.ownsRow(ctx, "npf_accounts", o.NPFID); err != nil {
		return err
	}
	broker, err := s.brokerRef(ctx, o.Broker)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE npf_ops SET
		npf_id=?, date=?, units_e6=?, amount=?, broker_id=?, note=? WHERE id=? AND portfolio_id=?`,
		o.NPFID, string(o.Date), o.Units, o.Amount, broker, o.Note, o.ID, s.pid)
	if err != nil {
		return err
	}
	return affectedOne(res, "внесок")
}

func (s *Store) DeleteNPFOp(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM npf_ops WHERE id=? AND portfolio_id=?`, id, s.pid)
	return err
}

// --- точки ЧВОПА ---

// ListNPFNav — історія ЧВОПА фондів, у яких цей портфель має рахунок.
//
// Точки спільні (npf_fund_nav), але NPFID у відповіді — id рахунку ЦЬОГО
// портфеля, а не фонду: саме за ним модель (domain.NPFNavPoints) і екран
// добирають точки до позиції, і їм не треба знати, що крива одна на двох.
func (s *Store) ListNPFNav(ctx context.Context) ([]domain.NPFNav, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT n.id, a.id, n.date, n.nav_e6 FROM npf_fund_nav n
			JOIN npf_accounts a ON a.fund_id = n.fund_id
			WHERE a.portfolio_id=? ORDER BY a.id, n.date`, s.pid)
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
// UPSERT за (fund_id, date) з тієї ж причини, що UNIQUE у схемі: дві різні
// ЧВОПА на один день — не історія, а суперечність, і остання виграє.
//
// npfID — id РАХУНКУ, як і доти: точки лягають в історію його фонду й
// видні всім портфелям, де цей фонд є (0070).
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
	// Рахунок чужого портфеля мусить читатись як ErrNotFound, а не тихо
	// прийняти пачку в історію фонду, якого цей портфель не тримає.
	fundID, err := s.npfFundOfIn(ctx, tx, npfID)
	if err != nil {
		return 0, err
	}
	for _, p := range pts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO npf_fund_nav (fund_id, date, nav_e6)
			VALUES (?,?,?) ON CONFLICT(fund_id, date) DO UPDATE SET nav_e6=excluded.nav_e6`,
			fundID, string(p.Date), p.Nav); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(pts), nil
}

// npfNavMine — умова «точка належить фонду, в якому цей портфель має
// рахунок». Стоїть у правці й видаленні точки: історія спільна, але
// портфель без рахунку у фонді не має діставатись до неї за вгаданим id.
const npfNavMine = `fund_id IN (SELECT fund_id FROM npf_accounts WHERE portfolio_id=?)`

// UpdateNPFNavPoint переписує одну точку ЧВОПА, зберігаючи id і фонд.
//
// Крива ЧВОПА — найдорожчі за працею дані в базі (їх заводять руками з
// звітів фонду, рік за роком), і доти єдиним способом виправити одну
// одруківку було видалити точку й вписати заново. Одна зайва дія на
// найдорожчих даних — саме там, де ціна помилки найвища.
//
// Дата входить в UNIQUE(fund_id, date), тож перенесення точки на зайняту
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
		`SELECT COUNT(*) FROM npf_fund_nav WHERE date=? AND id<>?
			AND fund_id=(SELECT fund_id FROM npf_fund_nav WHERE id=? AND `+npfNavMine+`)`,
		string(n.Date), n.ID, n.ID, s.pid).Scan(&busy); err != nil {
		return err
	}
	if busy > 0 {
		return fmt.Errorf("точка на %s уже є: %w", n.Date, ErrConflict)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE npf_fund_nav SET date=?, nav_e6=? WHERE id=? AND `+npfNavMine, string(n.Date), n.Nav, n.ID, s.pid)
	if err != nil {
		return err
	}
	return affectedOne(res, "точка ЧВОПА")
}

func (s *Store) DeleteNPFNavPoint(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM npf_fund_nav WHERE id=? AND `+npfNavMine, id, s.pid)
	if err != nil {
		return err
	}
	return affectedOne(res, "точка ЧВОПА")
}
