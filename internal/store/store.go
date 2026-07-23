// Package store — доступ до SQLite. Увесь SQL живе тут; домен працює
// зі структурами domain.* і не знає про БД (шлях міграції на Postgres —
// заміна цього пакета).
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	money "github.com/Rhymond/go-money"
	_ "github.com/mattn/go-sqlite3"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // один writer; для цього навантаження — найпростіша коректність
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// --- лоти ---

// affectedOne перетворює «оновлено 0 рядків» на помилку: без цього PUT за
// неіснуючим id тихо повертав би успіх.
func affectedOne(res sql.Result, what string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s не знайдено", what)
	}
	return nil
}

func (s *Store) AddLot(ctx context.Context, l domain.Lot) (int64, error) {
	fee := int64(0)
	if l.Fee != nil {
		fee = l.Fee.Amount()
	}
	broker, err := s.brokerRef(ctx, l.Channel)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO lots
		(isin, qty, price_per_bond, currency, buy_date, broker_id, note, fee)
		VALUES (?,?,?,?,?,?,?,?)`,
		l.ISIN, l.Qty, l.PricePerBond.Amount(), l.PricePerBond.Currency().Code,
		string(l.BuyDate), broker, l.Note, fee)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateLot переписує лот, зберігаючи id. Саме зі збереженням id: продажі
// посилаються на лот через lot_id, тож «видалити й створити заново» рвало
// б цей зв'язок і мовчки знеособлювало історію продажів.
//
// SQLite сам не дасть зменшити кількість нижче проданої (див. перевірку в
// api), тут лише запис.
func (s *Store) UpdateLot(ctx context.Context, l domain.Lot) error {
	fee := int64(0)
	if l.Fee != nil {
		fee = l.Fee.Amount()
	}
	broker, err := s.brokerRef(ctx, l.Channel)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE lots SET
		isin=?, qty=?, price_per_bond=?, currency=?, buy_date=?, broker_id=?, note=?, fee=?
		WHERE id=?`,
		l.ISIN, l.Qty, l.PricePerBond.Amount(), l.PricePerBond.Currency().Code,
		string(l.BuyDate), broker, l.Note, fee, l.ID)
	if err != nil {
		return err
	}
	return affectedOne(res, "лот")
}

func (s *Store) DeleteLot(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM lots WHERE id=?`, id)
	return err
}

func (s *Store) ListLots(ctx context.Context) ([]domain.Lot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT l.id, l.isin, l.qty, l.price_per_bond,
		l.currency, l.buy_date, COALESCE(b.name,''), l.note, l.fee
		FROM lots l LEFT JOIN brokers b ON b.id = l.broker_id
		ORDER BY l.buy_date, l.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Lot
	for rows.Next() {
		var l domain.Lot
		var minor, fee int64
		var cur, bd string
		if err := rows.Scan(&l.ID, &l.ISIN, &l.Qty, &minor, &cur, &bd, &l.Channel, &l.Note, &fee); err != nil {
			return nil, err
		}
		l.PricePerBond = money.New(minor, cur)
		l.Fee = money.New(fee, cur)
		l.BuyDate = domain.Date(bd)
		out = append(out, l)
	}
	return out, rows.Err()
}

// --- продажі ---

func (s *Store) AddSale(ctx context.Context, sl domain.Sale) (int64, error) {
	// валідація: не продати більше, ніж залишилось у лоті
	var lotQty int64
	var lotCur string
	err := s.db.QueryRowContext(ctx, `SELECT qty, currency FROM lots WHERE id=?`, sl.LotID).
		Scan(&lotQty, &lotCur)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("лот %d не існує", sl.LotID)
	}
	if err != nil {
		return 0, err
	}
	if lotCur != sl.CleanPerBond.Currency().Code {
		return 0, fmt.Errorf("валюта продажу %s не збігається з валютою лота %s",
			sl.CleanPerBond.Currency().Code, lotCur)
	}
	var sold sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT SUM(qty) FROM sales WHERE lot_id=?`, sl.LotID).Scan(&sold); err != nil {
		return 0, err
	}
	if sold.Int64+sl.Qty > lotQty {
		return 0, fmt.Errorf("продаж %d + продано раніше %d > лот %d", sl.Qty, sold.Int64, lotQty)
	}
	accrued := int64(0)
	if sl.Accrued != nil {
		accrued = sl.Accrued.Amount()
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO sales
		(lot_id, sale_date, qty, clean_per_bond, accrued, currency, note)
		VALUES (?,?,?,?,?,?,?)`,
		sl.LotID, string(sl.SaleDate), sl.Qty, sl.CleanPerBond.Amount(), accrued, lotCur, sl.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListSales(ctx context.Context) ([]domain.Sale, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, lot_id, sale_date, qty,
		clean_per_bond, accrued, currency, note FROM sales ORDER BY sale_date, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Sale
	for rows.Next() {
		var sl domain.Sale
		var clean, accrued int64
		var cur, sd string
		if err := rows.Scan(&sl.ID, &sl.LotID, &sd, &sl.Qty, &clean, &accrued, &cur, &sl.Note); err != nil {
			return nil, err
		}
		sl.SaleDate = domain.Date(sd)
		sl.CleanPerBond = money.New(clean, cur)
		sl.Accrued = money.New(accrued, cur)
		out = append(out, sl)
	}
	return out, rows.Err()
}

// --- рахунок (гаманець) ---

// Deposit — ручне поповнення (+) або зняття (−) рахунку у своїй валюті.
type Deposit struct {
	ID       int64
	Date     domain.Date
	Amount   int64 // мінорні; + поповнення / − зняття
	Currency string
	Broker   string // mono / inzhur / …; рахунки роздільні
	Note     string
}

func (s *Store) AddDeposit(ctx context.Context, d Deposit) (int64, error) {
	broker, err := s.brokerRef(ctx, d.Broker)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO deposits (date, amount, currency, broker_id, note)
		VALUES (?,?,?,?,?)`, string(d.Date), d.Amount, d.Currency, broker, d.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateDeposit переписує поповнення, зберігаючи id.
func (s *Store) UpdateDeposit(ctx context.Context, d Deposit) error {
	broker, err := s.brokerRef(ctx, d.Broker)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE deposits SET
		date=?, amount=?, currency=?, broker_id=?, note=? WHERE id=?`,
		string(d.Date), d.Amount, d.Currency, broker, d.Note, d.ID)
	if err != nil {
		return err
	}
	return affectedOne(res, "поповнення")
}

func (s *Store) DeleteDeposit(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM deposits WHERE id=?`, id)
	return err
}

func (s *Store) ListDeposits(ctx context.Context) ([]Deposit, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id, d.date, d.amount, d.currency,
		COALESCE(b.name,''), d.note
		FROM deposits d LEFT JOIN brokers b ON b.id = d.broker_id
		ORDER BY d.date, d.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Deposit
	for rows.Next() {
		var d Deposit
		var dt string
		if err := rows.Scan(&d.ID, &dt, &d.Amount, &d.Currency, &d.Broker, &d.Note); err != nil {
			return nil, err
		}
		d.Date = domain.Date(dt)
		out = append(out, d)
	}
	return out, rows.Err()
}

// BrokerCur — ключ балансу: рахунки роздільні, тож гроші живуть у розрізі
// (брокер × валюта), а не просто по валютах.
type BrokerCur struct {
	Broker   string
	Currency string
}

// DepositsByBrokerCurrency — сума поповнень/знять по (брокер, валюта).
func (s *Store) DepositsByBrokerCurrency(ctx context.Context) (map[BrokerCur]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT COALESCE(b.name,''), d.currency, SUM(d.amount)
		 FROM deposits d LEFT JOIN brokers b ON b.id = d.broker_id
		 GROUP BY b.name, d.currency`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[BrokerCur]int64{}
	for rows.Next() {
		var k BrokerCur
		var sum int64
		if err := rows.Scan(&k.Broker, &k.Currency, &sum); err != nil {
			return nil, err
		}
		out[k] = sum
	}
	return out, rows.Err()
}

// Conversion — обмін: віддав FromAmount[FromCurrency] → отримав ToAmount[ToCurrency].
type Conversion struct {
	ID           int64
	Date         domain.Date
	FromCurrency string
	FromAmount   int64
	ToCurrency   string
	ToAmount     int64
	Broker       string // обмін відбувається всередині одного рахунку
	Note         string
}

func (s *Store) AddConversion(ctx context.Context, c Conversion) (int64, error) {
	broker, err := s.brokerRef(ctx, c.Broker)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO conversions
		(date, from_currency, from_amount, to_currency, to_amount, broker_id, note) VALUES (?,?,?,?,?,?,?)`,
		string(c.Date), c.FromCurrency, c.FromAmount, c.ToCurrency, c.ToAmount, broker, c.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateConversion переписує конвертацію, зберігаючи id.
func (s *Store) UpdateConversion(ctx context.Context, c Conversion) error {
	broker, err := s.brokerRef(ctx, c.Broker)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE conversions SET
		date=?, from_currency=?, from_amount=?, to_currency=?, to_amount=?, broker_id=?, note=?
		WHERE id=?`,
		string(c.Date), c.FromCurrency, c.FromAmount, c.ToCurrency, c.ToAmount,
		broker, c.Note, c.ID)
	if err != nil {
		return err
	}
	return affectedOne(res, "конвертацію")
}

func (s *Store) DeleteConversion(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM conversions WHERE id=?`, id)
	return err
}

func (s *Store) ListConversions(ctx context.Context) ([]Conversion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.date, c.from_currency, c.from_amount,
		c.to_currency, c.to_amount, COALESCE(b.name,''), c.note
		FROM conversions c LEFT JOIN brokers b ON b.id = c.broker_id
		ORDER BY c.date, c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversion
	for rows.Next() {
		var c Conversion
		var dt string
		if err := rows.Scan(&c.ID, &dt, &c.FromCurrency, &c.FromAmount, &c.ToCurrency, &c.ToAmount, &c.Broker, &c.Note); err != nil {
			return nil, err
		}
		c.Date = domain.Date(dt)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ConversionsNetByBroker — чистий рух по (брокер, валюта): обмін не
// переносить гроші між рахунками, тож обидві ноги лягають на один брокер.
func (s *Store) ConversionsNetByBroker(ctx context.Context) (map[BrokerCur]int64, error) {
	convs, err := s.ListConversions(ctx)
	if err != nil {
		return nil, err
	}
	out := map[BrokerCur]int64{}
	for _, c := range convs {
		out[BrokerCur{c.Broker, c.FromCurrency}] -= c.FromAmount
		out[BrokerCur{c.Broker, c.ToCurrency}] += c.ToAmount
	}
	return out, nil
}

// MinNominalByCurrency — найменший номінал у довіднику по кожній валюті
// (проксі «ціни найдешевшого паперу» для заклику до реінвестиції).
func (s *Store) MinNominalByCurrency(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT currency, MIN(nominal) FROM bonds GROUP BY currency`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var cur string
		var minNom int64
		if err := rows.Scan(&cur, &minNom); err != nil {
			return nil, err
		}
		out[cur] = minNom
	}
	return out, rows.Err()
}

// AvgRateByCurrency — середня купонна ставка непогашених паперів довідника
// по валютах, у відсотках. Потрібна як запасна дохідність для валюти, яку
// користувач планує купувати, але ще не має: без неї валютний рукав
// проєкції довелося б рахувати під нуль або під вигадану константу.
func (s *Store) AvgRateByCurrency(ctx context.Context, today domain.Date) (map[string]float64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT currency, AVG(rate_bp) FROM bonds WHERE maturity > ? GROUP BY currency`, string(today))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var cur string
		var avgBP float64
		if err := rows.Scan(&cur, &avgBP); err != nil {
			return nil, err
		}
		out[cur] = avgBP / 100
	}
	return out, rows.Err()
}

// --- довідник ---

// ReplaceDirectory атомарно оновлює кеш довідника НБУ (весь ринок).
func (s *Store) ReplaceDirectory(ctx context.Context, secs []nbu.Security, fetchedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM payments`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM bonds`); err != nil {
		return err
	}
	bstmt, err := tx.Prepare(`INSERT INTO bonds
		(isin, nominal, currency, rate_bp, maturity, descr, fetched_at)
		VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer bstmt.Close()
	pstmt, err := tx.Prepare(`INSERT OR REPLACE INTO payments
		(isin, pay_date, pay_type, per_bond) VALUES (?,?,?,?)`)
	if err != nil {
		return err
	}
	defer pstmt.Close()
	ft := fetchedAt.UTC().Format(time.RFC3339)
	for _, sec := range secs {
		b := sec.Bond
		if _, err := bstmt.Exec(b.ISIN, b.Nominal.Amount(), b.Nominal.Currency().Code,
			b.RateBP, string(b.Maturity), b.Descr, ft); err != nil {
			return fmt.Errorf("bond %s: %w", b.ISIN, err)
		}
		for _, p := range sec.Payments {
			if _, err := pstmt.Exec(p.ISIN, string(p.PayDate), int(p.Type), p.PerBond.Amount()); err != nil {
				return fmt.Errorf("payment %s %s: %w", p.ISIN, p.PayDate, err)
			}
		}
	}
	return tx.Commit()
}

func scanBond(rows *sql.Rows) (domain.Bond, error) {
	var b domain.Bond
	var nom, rate int64
	var cur, mat string
	err := rows.Scan(&b.ISIN, &nom, &cur, &rate, &mat, &b.Descr)
	if err != nil {
		return b, err
	}
	b.Nominal = money.New(nom, cur)
	b.RateBP = rate
	b.Maturity = domain.Date(mat)
	return b, nil
}

func (s *Store) GetBond(ctx context.Context, isin string) (*domain.Bond, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT isin, nominal, currency, rate_bp,
		maturity, descr FROM bonds WHERE isin=?`, isin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, nil
	}
	b, err := scanBond(rows)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// SearchBonds — пошук по довіднику для автокомпліта і фільтрів UI.
func (s *Store) SearchBonds(ctx context.Context, q, currency string, matFrom, matTo domain.Date, limit int) ([]domain.Bond, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	sqlq := `SELECT isin, nominal, currency, rate_bp, maturity, descr FROM bonds WHERE 1=1`
	args := []any{}
	if q != "" {
		sqlq += ` AND (isin LIKE ? OR descr LIKE ?)`
		args = append(args, "%"+q+"%", "%"+q+"%")
	}
	if currency != "" {
		sqlq += ` AND currency = ?`
		args = append(args, currency)
	}
	if matFrom != "" {
		sqlq += ` AND maturity >= ?`
		args = append(args, string(matFrom))
	}
	if matTo != "" {
		sqlq += ` AND maturity <= ?`
		args = append(args, string(matTo))
	}
	sqlq += ` ORDER BY maturity LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Bond
	for rows.Next() {
		b, err := scanBond(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BondsFor — довідник по ISIN портфеля у вигляді map для домену.
func (s *Store) BondsFor(ctx context.Context, isins []string) (map[string]domain.Bond, error) {
	out := map[string]domain.Bond{}
	for _, isin := range isins {
		b, err := s.GetBond(ctx, isin)
		if err != nil {
			return nil, err
		}
		if b != nil {
			out[isin] = *b
		}
	}
	return out, nil
}

func (s *Store) PaymentsFor(ctx context.Context, isins []string) ([]domain.Payment, error) {
	var out []domain.Payment
	for _, isin := range isins {
		rows, err := s.db.QueryContext(ctx, `SELECT p.isin, p.pay_date, p.pay_type, p.per_bond, b.currency
			FROM payments p JOIN bonds b ON b.isin = p.isin WHERE p.isin=? ORDER BY p.pay_date`, isin)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p domain.Payment
			var pd, cur string
			var typ int
			var minor int64
			if err := rows.Scan(&p.ISIN, &pd, &typ, &minor, &cur); err != nil {
				rows.Close()
				return nil, err
			}
			p.PayDate = domain.Date(pd)
			p.Type = domain.PayType(typ)
			p.PerBond = money.New(minor, cur)
			out = append(out, p)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// --- налаштування, курси, знімки, статуси ---

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) SaveRate(ctx context.Context, code string, rateE4 int64, date domain.Date) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO fx_rates(code, rate_e4, date) VALUES(?,?,?)
		ON CONFLICT(code, date) DO UPDATE SET rate_e4=excluded.rate_e4`,
		code, rateE4, string(date))
	return err
}

func (s *Store) LatestRate(ctx context.Context, code string) (int64, error) {
	var r int64
	err := s.db.QueryRowContext(ctx,
		`SELECT rate_e4 FROM fx_rates WHERE code=? ORDER BY date DESC LIMIT 1`, code).Scan(&r)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return r, err
}

// SaveSnapshot приймає структуру, а не вісім позиційних int64: у такому
// списку сусідні аргументи однакового типу рано чи пізно міняються
// місцями, і помітно це стає вже на графіку.
func (s *Store) SaveSnapshot(ctx context.Context, sn Snapshot) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO snapshots
		(date, invested_uah, nominal_uah_eq, usd_share_bp, uninvested_uah, month_target_uah, account_uah, funds_uah)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(date) DO UPDATE SET invested_uah=excluded.invested_uah,
		nominal_uah_eq=excluded.nominal_uah_eq, usd_share_bp=excluded.usd_share_bp,
		uninvested_uah=excluded.uninvested_uah, month_target_uah=excluded.month_target_uah,
		account_uah=excluded.account_uah, funds_uah=excluded.funds_uah`,
		string(sn.Date), sn.InvestedUAH, sn.NominalUAHEq, sn.USDShareBP,
		sn.UninvestedUAH, sn.MonthTargetUAH, sn.AccountUAH, sn.FundsUAH)
	return err
}

func (s *Store) SetPaymentStatus(ctx context.Context, isin string, payDate domain.Date, status string) error {
	if status != "received" && status != "reinvested" {
		return fmt.Errorf("невалідний статус %q", status)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO payment_status(isin, pay_date, status, marked_at)
		VALUES(?,?,?,?) ON CONFLICT(isin, pay_date) DO UPDATE SET
		status=excluded.status, marked_at=excluded.marked_at`,
		isin, string(payDate), status, time.Now().UTC().Format(time.RFC3339))
	return err
}

// ClearPaymentStatus знімає позначку з виплати. Скасування — це саме
// ВІДСУТНІСТЬ рядка, а не третій статус: раз позначка тепер рухає гроші
// (виплата, датована сьогодні чи наперед, лягає на рахунок лише після
// неї), помилковий клік має повертати стан рівно до того, який був до
// нього, — а не додавати ще одне значення, яке довелось би тлумачити.
func (s *Store) ClearPaymentStatus(ctx context.Context, isin string, payDate domain.Date) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM payment_status WHERE isin=? AND pay_date=?`, isin, string(payDate))
	return err
}

func (s *Store) PaymentStatuses(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT isin, pay_date, status FROM payment_status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var isin, pd, st string
		if err := rows.Scan(&isin, &pd, &st); err != nil {
			return nil, err
		}
		out[isin+"|"+pd] = st
	}
	return out, rows.Err()
}

// Snapshot — рядок добового знімка для графіка «факт vs модель».
type Snapshot struct {
	Date           domain.Date
	InvestedUAH    int64
	NominalUAHEq   int64
	USDShareBP     int64
	UninvestedUAH  int64
	MonthTargetUAH int64
	AccountUAH     int64
	// FundsUAH — сертифікати фондів у грн-екв. Нуль у старих рядках
	// означає «тоді не рахували», а не «не було».
	FundsUAH int64
}

func (s *Store) ListSnapshots(ctx context.Context, from, to domain.Date) ([]Snapshot, error) {
	sqlq := `SELECT date, invested_uah, nominal_uah_eq, usd_share_bp, uninvested_uah,
		month_target_uah, account_uah, funds_uah FROM snapshots WHERE 1=1`
	args := []any{}
	if from != "" {
		sqlq += ` AND date >= ?`
		args = append(args, string(from))
	}
	if to != "" {
		sqlq += ` AND date <= ?`
		args = append(args, string(to))
	}
	sqlq += ` ORDER BY date`
	rows, err := s.db.QueryContext(ctx, sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		var sn Snapshot
		var d string
		if err := rows.Scan(&d, &sn.InvestedUAH, &sn.NominalUAHEq, &sn.USDShareBP, &sn.UninvestedUAH, &sn.MonthTargetUAH, &sn.AccountUAH, &sn.FundsUAH); err != nil {
			return nil, err
		}
		sn.Date = domain.Date(d)
		out = append(out, sn)
	}
	return out, rows.Err()
}

// --- сертифікати фондів ---

// Журнал операцій фонду. Позиція — це сальдо журналу (див.
// domain.FundPositions), тож окремої таблиці позицій немає: одне джерело
// правди замість двох, які неминуче розійшлись би.

// refsFor розв'язує назви фонду й брокера в id, заводячи їх за потреби.
// Валюта операції тут востаннє щось означає: далі вона властивість фонду.
func (s *Store) refsFor(ctx context.Context, op domain.FundOp) (int64, any, error) {
	fund, err := s.fundRef(ctx, op.Fund, op.Currency)
	if err != nil {
		return 0, nil, err
	}
	broker, err := s.brokerRef(ctx, op.Broker)
	if err != nil {
		return 0, nil, err
	}
	return fund, broker, nil
}

func (s *Store) AddFundOp(ctx context.Context, op domain.FundOp) (int64, error) {
	fund, broker, err := s.refsFor(ctx, op)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO fund_ops
		(date, fund_id, kind, qty, amount, tax, broker_id, pair_id, note)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		string(op.Date), fund, string(op.Kind), op.Qty, op.Amount, op.Tax,
		broker, nullID(op.PairID), op.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateFundOp(ctx context.Context, op domain.FundOp) error {
	fund, broker, err := s.refsFor(ctx, op)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE fund_ops SET
		date=?, fund_id=?, kind=?, qty=?, amount=?, tax=?, broker_id=?, pair_id=?, note=?
		WHERE id=?`,
		string(op.Date), fund, string(op.Kind), op.Qty, op.Amount, op.Tax,
		broker, nullID(op.PairID), op.Note, op.ID)
	if err != nil {
		return err
	}
	return affectedOne(res, "операцію фонду")
}

// nullID перетворює 0 на SQL NULL: «немає пари» — це відсутність
// посилання, а не посилання на неіснуючий рядок 0.
func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func (s *Store) DeleteFundOp(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM fund_ops WHERE id=?`, id)
	return err
}

// ListFundOps повертає журнал У ХРОНОЛОГІЧНОМУ порядку: собівартість
// рахується послідовно, тож інший порядок дав би інший результат.
func (s *Store) ListFundOps(ctx context.Context) ([]domain.FundOp, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.id, o.date, f.name, o.kind, o.qty, o.amount,
		o.tax, f.currency, COALESCE(b.name,''), COALESCE(o.pair_id,0), o.note
		FROM fund_ops o
		JOIN funds f ON f.id = o.fund_id
		LEFT JOIN brokers b ON b.id = o.broker_id
		ORDER BY o.date, o.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.FundOp
	for rows.Next() {
		var op domain.FundOp
		var d, kind string
		if err := rows.Scan(&op.ID, &d, &op.Fund, &kind, &op.Qty, &op.Amount,
			&op.Tax, &op.Currency, &op.Broker, &op.PairID, &op.Note); err != nil {
			return nil, err
		}
		op.Date, op.Kind = domain.Date(d), domain.FundOpKind(kind)
		out = append(out, op)
	}
	return out, rows.Err()
}

// FundOpExists — чи вже є така операція. Потрібно імпорту виписки: файл
// щомісяця містить і старі рядки, тож без перевірки повторний імпорт
// подвоїв би позицію.
func (s *Store) FundOpExists(ctx context.Context, op domain.FundOp) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fund_ops o
		JOIN funds f ON f.id = o.fund_id
		WHERE o.date=? AND f.name=? AND o.kind=? AND o.qty=? AND o.amount=?`,
		string(op.Date), op.Fund, string(op.Kind), op.Qty, op.Amount).Scan(&n)
	return n > 0, err
}
