package store

import (
	"context"
	"fmt"
	"strings"
)

// Бекап користувацьких даних. Бекапимо лише те, що введено РУКАМИ й
// невідновне: лоти, продажі, поповнення, конвертації, налаштування,
// статуси виплат і добові знімки. Довідник НБУ, графіки виплат і курси —
// похідні, повертаються командою «Оновити НБУ», тож у бекап не входять.
//
// Формат — сирі колонки в мінорних одиницях (як у БД), із збереженими ID:
// продажі посилаються на лоти за id, і при відновленні цей зв'язок має
// лишитись. Це найнадійніший формат — стабільний і портабельний.

const BackupSchema = 1

type Backup struct {
	Schema        int                 `json:"schema"`
	App           string              `json:"app"`
	ExportedAt    string              `json:"exported_at"`
	Lots          []BackupLot         `json:"lots"`
	Sales         []BackupSale        `json:"sales"`
	Deposits      []BackupDeposit     `json:"deposits"`
	Conversions   []BackupConversion  `json:"conversions"`
	FundOps       []BackupFundOp      `json:"fund_ops"`
	Settings      map[string]string   `json:"settings"`
	PaymentStatus []BackupPayStatus   `json:"payment_status"`
	Snapshots     []BackupSnapshot    `json:"snapshots"`
}

// BackupFundOp — операція з сертифікатами фонду. Без неї бекап був
// неповним: відновлення стерло б усю історію фондів разом із дивідендами
// й податками, а помітно це стало б лише тоді, коли відновлюватись уже
// нема з чого.
type BackupFundOp struct {
	ID       int64  `json:"id"`
	Date     string `json:"date"`
	Fund     string `json:"fund"`
	Kind     string `json:"kind"`
	Qty      int64  `json:"qty"`
	Amount   int64  `json:"amount"`
	Tax      int64  `json:"tax"`
	Currency string `json:"currency"`
	Broker   string `json:"broker"`
	// PairID — друга нога конвертації між фондами. omitempty, щоб бекапи
	// без конвертацій виглядали рівно так само, як раніше.
	PairID int64  `json:"pair_id,omitempty"`
	Note   string `json:"note"`
}

type BackupLot struct {
	ID       int64  `json:"id"`
	ISIN     string `json:"isin"`
	Qty      int64  `json:"qty"`
	Price    int64  `json:"price_per_bond"` // мінорні
	Currency string `json:"currency"`
	BuyDate  string `json:"buy_date"`
	Channel  string `json:"channel"`
	Fee      int64  `json:"fee"`
	Note     string `json:"note"`
}

type BackupSale struct {
	ID       int64  `json:"id"`
	LotID    int64  `json:"lot_id"`
	SaleDate string `json:"sale_date"`
	Qty      int64  `json:"qty"`
	Clean    int64  `json:"clean_per_bond"`
	Accrued  int64  `json:"accrued"`
	Currency string `json:"currency"`
	Note     string `json:"note"`
}

type BackupDeposit struct {
	ID       int64  `json:"id"`
	Date     string `json:"date"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Broker   string `json:"broker"`
	Note     string `json:"note"`
}

type BackupConversion struct {
	ID           int64  `json:"id"`
	Date         string `json:"date"`
	FromCurrency string `json:"from_currency"`
	FromAmount   int64  `json:"from_amount"`
	ToCurrency   string `json:"to_currency"`
	ToAmount     int64  `json:"to_amount"`
	Broker       string `json:"broker"`
	Note         string `json:"note"`
}

type BackupPayStatus struct {
	ISIN     string `json:"isin"`
	PayDate  string `json:"pay_date"`
	Status   string `json:"status"`
	MarkedAt string `json:"marked_at"`
}

type BackupSnapshot struct {
	Date          string `json:"date"`
	InvestedUAH   int64  `json:"invested_uah"`
	NominalUAHEq  int64  `json:"nominal_uah_eq"`
	USDShareBP    int64  `json:"usd_share_bp"`
	UninvestedUAH int64  `json:"uninvested_uah"`
}

// ExportAll читає всі користувацькі таблиці в один знімок.
func (s *Store) ExportAll(ctx context.Context) (*Backup, error) {
	b := &Backup{Schema: BackupSchema, App: "oddinvest", Settings: map[string]string{}}

	// Бекап тримає НАЗВИ брокерів і фондів, а не їхні id. Так формат
	// пережив нормалізацію: файли, вивантажені до появи довідників,
	// відновлюються далі без жодних перетворень.
	if err := s.scan(ctx, `SELECT l.id,l.isin,l.qty,l.price_per_bond,l.currency,l.buy_date,
		COALESCE(b.name,''),l.fee,l.note
		FROM lots l LEFT JOIN brokers b ON b.id=l.broker_id ORDER BY l.id`,
		func(scan func(...any) error) error {
			var r BackupLot
			if err := scan(&r.ID, &r.ISIN, &r.Qty, &r.Price, &r.Currency, &r.BuyDate, &r.Channel, &r.Fee, &r.Note); err != nil {
				return err
			}
			b.Lots = append(b.Lots, r)
			return nil
		}); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id,lot_id,sale_date,qty,clean_per_bond,accrued,currency,note FROM sales ORDER BY id`,
		func(scan func(...any) error) error {
			var r BackupSale
			if err := scan(&r.ID, &r.LotID, &r.SaleDate, &r.Qty, &r.Clean, &r.Accrued, &r.Currency, &r.Note); err != nil {
				return err
			}
			b.Sales = append(b.Sales, r)
			return nil
		}); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT d.id,d.date,d.amount,d.currency,COALESCE(b.name,''),d.note
		FROM deposits d LEFT JOIN brokers b ON b.id=d.broker_id ORDER BY d.id`,
		func(scan func(...any) error) error {
			var r BackupDeposit
			if err := scan(&r.ID, &r.Date, &r.Amount, &r.Currency, &r.Broker, &r.Note); err != nil {
				return err
			}
			b.Deposits = append(b.Deposits, r)
			return nil
		}); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT c.id,c.date,c.from_currency,c.from_amount,c.to_currency,
		c.to_amount,COALESCE(b.name,''),c.note
		FROM conversions c LEFT JOIN brokers b ON b.id=c.broker_id ORDER BY c.id`,
		func(scan func(...any) error) error {
			var r BackupConversion
			if err := scan(&r.ID, &r.Date, &r.FromCurrency, &r.FromAmount, &r.ToCurrency, &r.ToAmount, &r.Broker, &r.Note); err != nil {
				return err
			}
			b.Conversions = append(b.Conversions, r)
			return nil
		}); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT o.id,o.date,f.name,o.kind,o.qty,o.amount,o.tax,f.currency,
		COALESCE(b.name,''),COALESCE(o.pair_id,0),o.note
		FROM fund_ops o JOIN funds f ON f.id=o.fund_id
		LEFT JOIN brokers b ON b.id=o.broker_id ORDER BY o.id`,
		func(scan func(...any) error) error {
			var r BackupFundOp
			if err := scan(&r.ID, &r.Date, &r.Fund, &r.Kind, &r.Qty, &r.Amount, &r.Tax,
				&r.Currency, &r.Broker, &r.PairID, &r.Note); err != nil {
				return err
			}
			b.FundOps = append(b.FundOps, r)
			return nil
		}); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT key,value FROM settings`,
		func(scan func(...any) error) error {
			var k, v string
			if err := scan(&k, &v); err != nil {
				return err
			}
			b.Settings[k] = v
			return nil
		}); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT isin,pay_date,status,marked_at FROM payment_status`,
		func(scan func(...any) error) error {
			var r BackupPayStatus
			if err := scan(&r.ISIN, &r.PayDate, &r.Status, &r.MarkedAt); err != nil {
				return err
			}
			b.PaymentStatus = append(b.PaymentStatus, r)
			return nil
		}); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT date,invested_uah,nominal_uah_eq,usd_share_bp,uninvested_uah FROM snapshots ORDER BY date`,
		func(scan func(...any) error) error {
			var r BackupSnapshot
			if err := scan(&r.Date, &r.InvestedUAH, &r.NominalUAHEq, &r.USDShareBP, &r.UninvestedUAH); err != nil {
				return err
			}
			b.Snapshots = append(b.Snapshots, r)
			return nil
		}); err != nil {
		return nil, err
	}
	return b, nil
}

// ImportAll ЗАМІНЮЄ всі користувацькі дані вмістом бекапу. Атомарно: або
// вся база замінюється, або нічого (при помилці транзакція відкочується).
// Довідник НБУ/графіки/курси не чіпаються — вони похідні.
func (s *Store) ImportAll(ctx context.Context, b *Backup) error {
	if b == nil || b.Schema != BackupSchema {
		return fmt.Errorf("несумісний бекап (schema=%d, очікуємо %d)", b.Schema, BackupSchema)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op після Commit

	// діти → батьки, щоб не спіткнутись об FK. Довідники йдуть ОСТАННІМИ
	// і заповнюються заново з назв у бекапі: тримати бекап у назвах, а не
	// в id, означає, що відновлення не залежить від того, які id були в
	// базі-джерелі.
	for _, t := range []string{"sales", "lots", "deposits", "conversions", "fund_ops",
		"settings", "payment_status", "snapshots", "funds", "brokers"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+t); err != nil {
			return fmt.Errorf("очищення %s: %w", t, err)
		}
	}

	brokers := map[string]any{}
	brokerRef := func(name string) (any, error) {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, nil
		}
		if id, ok := brokers[name]; ok {
			return id, nil
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO brokers(name) VALUES(?)`, name)
		if err != nil {
			return nil, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		brokers[name] = id
		return id, nil
	}
	funds := map[string]int64{}
	fundRef := func(name, currency string) (int64, error) {
		name = strings.TrimSpace(name)
		if name == "" {
			return 0, fmt.Errorf("операція без фонду")
		}
		if id, ok := funds[name]; ok {
			return id, nil
		}
		if strings.TrimSpace(currency) == "" {
			currency = "UAH"
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO funds(name,currency) VALUES(?,?)`, name, currency)
		if err != nil {
			return 0, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return 0, err
		}
		funds[name] = id
		return id, nil
	}

	for _, l := range b.Lots {
		broker, err := brokerRef(l.Channel)
		if err != nil {
			return fmt.Errorf("лот %d: %w", l.ID, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO lots (id,isin,qty,price_per_bond,currency,buy_date,broker_id,fee,note) VALUES (?,?,?,?,?,?,?,?,?)`,
			l.ID, l.ISIN, l.Qty, l.Price, l.Currency, l.BuyDate, broker, l.Fee, l.Note); err != nil {
			return fmt.Errorf("лот %d: %w", l.ID, err)
		}
	}
	for _, sl := range b.Sales {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sales (id,lot_id,sale_date,qty,clean_per_bond,accrued,currency,note) VALUES (?,?,?,?,?,?,?,?)`,
			sl.ID, sl.LotID, sl.SaleDate, sl.Qty, sl.Clean, sl.Accrued, sl.Currency, sl.Note); err != nil {
			return fmt.Errorf("продаж %d: %w", sl.ID, err)
		}
	}
	for _, d := range b.Deposits {
		broker, err := brokerRef(d.Broker)
		if err != nil {
			return fmt.Errorf("поповнення %d: %w", d.ID, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO deposits (id,date,amount,currency,broker_id,note) VALUES (?,?,?,?,?,?)`,
			d.ID, d.Date, d.Amount, d.Currency, broker, d.Note); err != nil {
			return fmt.Errorf("поповнення %d: %w", d.ID, err)
		}
	}
	for _, c := range b.Conversions {
		broker, err := brokerRef(c.Broker)
		if err != nil {
			return fmt.Errorf("конвертація %d: %w", c.ID, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO conversions (id,date,from_currency,from_amount,to_currency,to_amount,broker_id,note) VALUES (?,?,?,?,?,?,?,?)`,
			c.ID, c.Date, c.FromCurrency, c.FromAmount, c.ToCurrency, c.ToAmount, broker, c.Note); err != nil {
			return fmt.Errorf("конвертація %d: %w", c.ID, err)
		}
	}
	// Операції фондів — двома заходами: pair_id посилається на інший
	// рядок цієї ж таблиці, і при вставці «за один прохід» друга нога
	// конвертації вказувала б на рядок, якого ще немає.
	for _, op := range b.FundOps {
		fund, err := fundRef(op.Fund, op.Currency)
		if err != nil {
			return fmt.Errorf("операція фонду %d: %w", op.ID, err)
		}
		broker, err := brokerRef(op.Broker)
		if err != nil {
			return fmt.Errorf("операція фонду %d: %w", op.ID, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO fund_ops (id,date,fund_id,kind,qty,amount,tax,broker_id,note) VALUES (?,?,?,?,?,?,?,?,?)`,
			op.ID, op.Date, fund, op.Kind, op.Qty, op.Amount, op.Tax, broker, op.Note); err != nil {
			return fmt.Errorf("операція фонду %d: %w", op.ID, err)
		}
	}
	for _, op := range b.FundOps {
		if op.PairID == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE fund_ops SET pair_id=? WHERE id=?`,
			op.PairID, op.ID); err != nil {
			return fmt.Errorf("конвертація фонду %d: %w", op.ID, err)
		}
	}
	for k, v := range b.Settings {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key,value) VALUES (?,?)`, k, v); err != nil {
			return fmt.Errorf("налаштування %q: %w", k, err)
		}
	}
	for _, p := range b.PaymentStatus {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO payment_status (isin,pay_date,status,marked_at) VALUES (?,?,?,?)`,
			p.ISIN, p.PayDate, p.Status, p.MarkedAt); err != nil {
			return fmt.Errorf("статус виплати %s/%s: %w", p.ISIN, p.PayDate, err)
		}
	}
	for _, sn := range b.Snapshots {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO snapshots (date,invested_uah,nominal_uah_eq,usd_share_bp,uninvested_uah) VALUES (?,?,?,?,?)`,
			sn.Date, sn.InvestedUAH, sn.NominalUAHEq, sn.USDShareBP, sn.UninvestedUAH); err != nil {
			return fmt.Errorf("знімок %s: %w", sn.Date, err)
		}
	}
	return tx.Commit()
}

// scan — дрібний хелпер: виконати запит і прогнати кожен рядок через fn.
func (s *Store) scan(ctx context.Context, query string, fn func(scan func(...any) error) error) error {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows.Scan); err != nil {
			return err
		}
	}
	return rows.Err()
}
