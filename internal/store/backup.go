package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/ODDsama/oddinvest/internal/domain"
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
	Schema      int                `json:"schema"`
	App         string             `json:"app"`
	ExportedAt  string             `json:"exported_at"`
	Lots        []BackupLot        `json:"lots"`
	Sales       []BackupSale       `json:"sales"`
	Deposits    []BackupDeposit    `json:"deposits"`
	Conversions []BackupConversion `json:"conversions"`
	FundOps     []BackupFundOp     `json:"fund_ops"`
	// TermDeposits omitempty: бекапи, зроблені до появи вкладів, читаються
	// без цього поля так само, як раніше — restore просто не створить
	// жодного вкладу.
	TermDeposits []BackupTermDeposit `json:"term_deposits,omitempty"`
	// DepositTopups omitempty з тієї ж причини: старіші бекапи їх не мають.
	DepositTopups []BackupDepositTopup `json:"deposit_topups,omitempty"`
	// ReserveOps omitempty з тієї ж причини: бекапи до появи резерву його
	// не мають, і restore просто не створить жодного руху.
	ReserveOps []BackupReserveOp `json:"reserve_ops,omitempty"`
	// Довідники. omitempty з тієї ж причини, що й усе вище: бекапи, зроблені
	// до їхньої появи, читаються без цих полів так само, як раніше — фонди й
	// брокери відновляться з назв в операціях, рівно як доти.
	Funds         []BackupFund      `json:"funds,omitempty"`
	Brokers       []BackupBroker    `json:"brokers,omitempty"`
	Settings      map[string]string `json:"settings"`
	PaymentStatus []BackupPayStatus `json:"payment_status"`
	Snapshots     []Snapshot        `json:"snapshots"`
}

// BackupFund — рядок довідника фондів (refs.go: Fund).
//
// Довідник довго відновлювався ПОБІЧНИМ ЕФЕКТОМ: фонд заводився з назви й
// валюти першої-ліпшої операції, а все, чого з операцій не вивести —
// обіцяна дохідність, її валюта й день виплати, — мовчки ставало нулем.
// Наслідок був не косметичний. У фонду з обіцянкою 9.5% USD відновлення
// збивало yield_basis з «обіцяно фондом» на «дивіденди після податку»,
// прибирало з календаря всі оцінені виплати (12 → 0) і разом із ними
// next_payout, а income_monthly_now просідав на чверть. Тобто restore тихо
// змінював числа портфеля — найгірший різновид втрати, бо цифри лишаються
// правдоподібними.
//
// Ключ — НАЗВА, а не id: бекап тримається назв усюди (див. ExportAll), і
// саме тому формат пережив нормалізацію.
type BackupFund struct {
	Name     string `json:"name"`
	Currency string `json:"currency"`
	// Три поля нижче — те, чого з операцій не вивести взагалі. omitempty:
	// у фонду без заданої обіцянки вони нулі, і дамп виглядає як раніше.
	ExpectedYieldBP  int64  `json:"expected_yield_bp,omitempty"`
	ExpectedYieldCur string `json:"expected_yield_currency,omitempty"`
	PayoutDay        int64  `json:"payout_day,omitempty"`
}

// BackupBroker — рядок довідника брокерів.
//
// Самих назв в операціях мало: брокер, заведений наперед і ще не вжитий у
// жодному записі, не згадується ніде, тож відновлення його просто губило —
// разом із випадайкою, до якої людина звикла.
type BackupBroker struct {
	Name string `json:"name"`
}

// BackupReserveOp — рух резерву. Резерв невідновний так само, як лоти:
// це журнал, який більше ніде не записаний, і без нього restore залишив
// би капітал заниженим рівно на суму матраца.
type BackupReserveOp struct {
	ID       int64  `json:"id"`
	Date     string `json:"date"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Place    string `json:"place"`
	Note     string `json:"note"`
}

// BackupDepositTopup — поповнення вкладу. Без нього відновлення втратило б
// докладені суми, і баланс вкладу занизився б до суми відкриття.
type BackupDepositTopup struct {
	ID        int64  `json:"id"`
	DepositID int64  `json:"deposit_id"`
	Date      string `json:"date"`
	Amount    int64  `json:"amount"`
}

// BackupTermDeposit — банківський вклад. Без нього відновлення стерло б
// усі вклади разом зі ставками й строками, а помітно це стало б тоді,
// коли відновлюватись уже нема з чого.
type BackupTermDeposit struct {
	ID           int64  `json:"id"`
	Bank         string `json:"bank"`
	Currency     string `json:"currency"`
	Principal    int64  `json:"principal"`
	RateBP       int64  `json:"rate_bp"`
	OpenDate     string `json:"open_date"`
	MaturityDate string `json:"maturity_date"`
	Payout       string `json:"payout"`
	Capitalized  bool   `json:"capitalized,omitempty"`
	TaxBP        int64  `json:"tax_bp"`
	ClosedDate   string `json:"closed_date,omitempty"`
	ClosedAmount int64  `json:"closed_amount,omitempty"`
	Note         string `json:"note"`
	// Replenishable omitempty: старіші бекапи поля не мають, і відновлення
	// прочитає їх як «не поповнюваний» — це типове значення колонки.
	Replenishable bool `json:"replenishable,omitempty"`
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

// Знімок у бекапі — це та сама Snapshot (snapshot.go), із її json-тегами.
//
// Окрема BackupSnapshot тут була доти, і саме вона показує, чому дублі
// небезпечні: довго вона несла лише перші п'ять полів, і відновлення
// мовчки стирало решту — місячну ціль, рахунок і фонди. Помітно це стало
// б тоді, коли відновлюватись уже нема з чого, а крива «Як росте» після
// restore просіла б рівно на вартість фондів.

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
	if err := s.scan(ctx, `SELECT `+termDepositCols()+`
		FROM term_deposits d LEFT JOIN brokers b ON b.id=d.broker_id ORDER BY d.id`,
		func(scan func(...any) error) error {
			var r BackupTermDeposit
			var capInt, replInt int64
			if err := scan(&r.ID, &r.Bank, &r.Currency, &r.Principal, &r.RateBP,
				&r.OpenDate, &r.MaturityDate, &r.Payout, &capInt, &r.TaxBP,
				&r.ClosedDate, &r.ClosedAmount, &r.Note, &replInt); err != nil {
				return err
			}
			r.Capitalized = capInt != 0
			r.Replenishable = replInt != 0
			b.TermDeposits = append(b.TermDeposits, r)
			return nil
		}); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id, deposit_id, date, amount FROM deposit_topups ORDER BY id`,
		func(scan func(...any) error) error {
			var r BackupDepositTopup
			if err := scan(&r.ID, &r.DepositID, &r.Date, &r.Amount); err != nil {
				return err
			}
			b.DepositTopups = append(b.DepositTopups, r)
			return nil
		}); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id,date,amount,currency,place,note FROM reserve_ops ORDER BY id`,
		func(scan func(...any) error) error {
			var r BackupReserveOp
			if err := scan(&r.ID, &r.Date, &r.Amount, &r.Currency, &r.Place, &r.Note); err != nil {
				return err
			}
			b.ReserveOps = append(b.ReserveOps, r)
			return nil
		}); err != nil {
		return nil, err
	}
	// Довідники — цілком, а не лише тими рядками, які згадані в операціях.
	// Порядок за назвою, як у ListFunds/ListBrokers: дамп того самого стану
	// має бути тим самим файлом.
	if err := s.scan(ctx, `SELECT name,currency,expected_yield_bp,expected_yield_currency,payout_day
		FROM funds ORDER BY name COLLATE NOCASE`,
		func(scan func(...any) error) error {
			var r BackupFund
			if err := scan(&r.Name, &r.Currency, &r.ExpectedYieldBP, &r.ExpectedYieldCur, &r.PayoutDay); err != nil {
				return err
			}
			b.Funds = append(b.Funds, r)
			return nil
		}); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT name FROM brokers ORDER BY name COLLATE NOCASE`,
		func(scan func(...any) error) error {
			var r BackupBroker
			if err := scan(&r.Name); err != nil {
				return err
			}
			b.Brokers = append(b.Brokers, r)
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
	// Колонки й цілі для Scan — з реєстру (snapshot.go). Тут це важить
	// найбільше: s.scan приймає func(...any), тож типи не перевіряються
	// взагалі, і переплутаний порядок був би тихим до останнього.
	if err := s.scan(ctx, `SELECT `+snapshotColumnList()+` FROM snapshots ORDER BY date`,
		func(scan func(...any) error) error {
			var r Snapshot
			var d string
			if err := scan(snapshotScanTargets(&r, &d)...); err != nil {
				return err
			}
			r.Date = domain.Date(d)
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
	//
	// term_deposits і deposit_topups сюди не потрапили, коли вклади
	// з'явились, і наслідок був не «дублікати після відновлення», а
	// повна відмова: term_deposits.broker_id посилається на brokers, тож
	// DELETE FROM brokers упирався у FK, і restore падав у будь-кого, хто
	// має бодай один вклад. Поповнення — перед самим вкладом, вони його
	// діти.
	//
	// reserve_ops — той самий випадок, що й вклади: бекап тримає id, тож
	// забути її в цьому переліку означає не «дублікати», а відмову
	// відновлення на UNIQUE(id) у будь-кого, хто має бодай один рух
	// резерву. Перевірено тестом.
	for _, t := range []string{"sales", "lots", "deposits", "conversions", "fund_ops",
		"deposit_topups", "term_deposits", "reserve_ops",
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

	// Довідники — ПЕРШИМИ й цілком, доки жодна операція їх ще не завела
	// побічним ефектом. Порядок тут і є суттю виправлення: fundRef задає
	// валюту ЛИШЕ при створенні й нічого не знає про обіцяну дохідність,
	// тож фонд, заведений з операції, назавжди лишався б із нулями в
	// трьох полях, яких з операцій не вивести. Заселивши довідник наперед,
	// ми робимо подальший fundRef простим пошуком у мапі.
	//
	// Старі дампи цих переліків не мають — обидва цикли просто не
	// виконуються, і відновлення йде рівно тим шляхом, що й доти.
	for _, br := range b.Brokers {
		if _, err := brokerRef(br.Name); err != nil {
			return fmt.Errorf("брокер %q: %w", br.Name, err)
		}
	}
	for _, f := range b.Funds {
		id, err := fundRef(f.Name, f.Currency)
		if err != nil {
			return fmt.Errorf("фонд %q: %w", f.Name, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE funds SET expected_yield_bp=?,
			expected_yield_currency=?, payout_day=? WHERE id=?`,
			f.ExpectedYieldBP, strings.TrimSpace(f.ExpectedYieldCur), f.PayoutDay, id); err != nil {
			return fmt.Errorf("фонд %q: %w", f.Name, err)
		}
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
	for _, d := range b.TermDeposits {
		broker, err := brokerRef(d.Bank)
		if err != nil {
			return fmt.Errorf("вклад %d: %w", d.ID, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO term_deposits (id,broker_id,currency,principal,rate_bp,open_date,
			 maturity_date,payout,capitalized,tax_bp,closed_date,closed_amount,note,replenishable)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			d.ID, broker, d.Currency, d.Principal, d.RateBP, d.OpenDate, d.MaturityDate,
			d.Payout, boolInt(d.Capitalized), d.TaxBP, d.ClosedDate, d.ClosedAmount, d.Note,
			boolInt(d.Replenishable)); err != nil {
			return fmt.Errorf("вклад %d: %w", d.ID, err)
		}
	}
	// Поповнення — після вкладів: FK на term_deposits.
	for _, t := range b.DepositTopups {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO deposit_topups (id,deposit_id,date,amount) VALUES (?,?,?,?)`,
			t.ID, t.DepositID, t.Date, t.Amount); err != nil {
			return fmt.Errorf("поповнення вкладу %d: %w", t.ID, err)
		}
	}
	for _, r := range b.ReserveOps {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO reserve_ops (id,date,amount,currency,place,note) VALUES (?,?,?,?,?,?)`,
			r.ID, r.Date, r.Amount, r.Currency, r.Place, r.Note); err != nil {
			return fmt.Errorf("рух резерву %d: %w", r.ID, err)
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
		// Забута тут колонка означала б, що відновлення мовчки її обнуляє —
		// і саме це вже двічі ставалось. Перелік і аргументи з реєстру.
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO snapshots (`+snapshotColumnList()+`)
			 VALUES (`+snapshotPlaceholders()+`)`,
			snapshotArgs(&sn)...); err != nil {
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
