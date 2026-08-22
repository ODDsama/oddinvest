package store

// Довідники брокерів і фондів.
//
// Назва лишається зовнішнім інтерфейсом: API, бекап і обидва UI знають
// брокера й фонд саме як рядок. У базі натомість живе id. Перетворення
// туди-сюди ховається тут — тому нормалізація не зачепила ні домен, ні
// формат бекапу, і старі бекапи досі відновлюються.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// brokerRef повертає id брокера за назвою, заводячи його за потреби.
//
// Порожня назва дає NULL, а не запис із порожнім іменем: рух без брокера —
// це ВІДСУТНІСТЬ брокера. Раніше порожній рядок був повноцінним значенням,
// і в списках подекуди спливав привид-брокер без назви.
//
// Тип результату — any, щоб nil ліг у SQL як NULL.
func (s *Store) brokerRef(ctx context.Context, name string) (any, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM brokers WHERE name=?`, name).Scan(&id)
	switch {
	case err == sql.ErrNoRows:
		res, err := s.db.ExecContext(ctx, `INSERT INTO brokers(name) VALUES(?)`, name)
		if err != nil {
			return nil, err
		}
		return res.LastInsertId()
	case err != nil:
		return nil, err
	}
	return id, nil
}

// fundRef повертає id фонду за назвою, заводячи його за потреби.
//
// Валюта задається лише при створенні: у вже відомого фонду вона його
// власна властивість, і операція не має права її переписати — саме через
// таку можливість валюта колись і розходилась між рядками одного фонду.
func (s *Store) fundRef(ctx context.Context, name, currency string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("вкажіть фонд")
	}
	if strings.TrimSpace(currency) == "" {
		currency = "UAH"
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM funds WHERE name=?`, name).Scan(&id)
	switch {
	case err == sql.ErrNoRows:
		res, err := s.db.ExecContext(ctx, `INSERT INTO funds(name, currency) VALUES(?,?)`, name, currency)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	case err != nil:
		return 0, err
	}
	return id, nil
}

// Broker — рядок довідника брокерів.
type Broker struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// ListBrokers — довідник плюс ті, що вже зустрічались в операціях.
// Раніше цей список був CSV-рядком у settings('channels').
func (s *Store) ListBrokers(ctx context.Context) ([]Broker, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM brokers ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Broker{}
	for rows.Next() {
		var b Broker
		if err := rows.Scan(&b.ID, &b.Name); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) AddBroker(ctx context.Context, name string) (int64, error) {
	id, err := s.brokerRef(ctx, name)
	if err != nil {
		return 0, err
	}
	if id == nil {
		return 0, fmt.Errorf("вкажіть назву брокера")
	}
	return id.(int64), nil
}

// RenameBroker — те, чого не було з CSV-списком узагалі: перейменування
// підхоплюють усі лоти, поповнення, конвертації й операції фондів разом,
// бо вони посилаються на id, а не на текст.
func (s *Store) RenameBroker(ctx context.Context, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("вкажіть назву брокера")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE brokers SET name=? WHERE id=?`, name, id)
	if err != nil {
		return err
	}
	return affectedOne(res, "брокера")
}

// DeleteBroker прибирає брокера, лише якщо на нього ніхто не посилається:
// мовчки знеособити півсотні лотів — не те, чого чекають від кнопки «✕».
func (s *Store) DeleteBroker(ctx context.Context, id int64) error {
	var used int
	if err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM lots        WHERE broker_id=?) +
		(SELECT COUNT(*) FROM deposits    WHERE broker_id=?) +
		(SELECT COUNT(*) FROM conversions WHERE broker_id=?) +
		(SELECT COUNT(*) FROM fund_ops    WHERE broker_id=?)`,
		id, id, id, id).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return fmt.Errorf("брокера використано в %d записах — спершу перенеси їх", used)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM brokers WHERE id=?`, id)
	if err != nil {
		return err
	}
	return affectedOne(res, "брокера")
}

// Fund — рядок довідника фондів.
//
// ExpectedYieldBP — обіцяна фондом дохідність × 100 (9.5% = 950), 0 = не
// задана. ExpectedYieldCur — валюта, В ЯКІЙ вона обіцяна: сертифікат може
// бути гривневим, а обіцянка доларовою, і тоді гривневий штраф за
// знецінення до неї не застосовується. Порожньо = валюта самого фонду.
// PayoutDay — число місяця, коли платять дивіденди (0 = невідомо).
//
// Kind, CloseDate, BuyUntil, IncomeTaxBP і YieldSimpleYears — те, чим
// строковий фонд відрізняється від безстрокового (міграція 0022).
// Порожні для звичайного REIT, і доти застосунок поводиться як раніше.
type Fund struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	Currency         string `json:"currency"`
	ExpectedYieldBP  int64  `json:"expected_yield_bp"`
	ExpectedYieldCur string `json:"expected_yield_currency"`
	PayoutDay        int64  `json:"payout_day"`
	// Kind — FundDistributing ('') або FundAccumulating ('accum').
	Kind string `json:"kind"`
	// CloseDate — коли фонд закривається й повертає гроші; BuyUntil —
	// остання дата, коли його ще можна купити. Порожньо = безстроковий,
	// купувати можна завжди.
	CloseDate string `json:"close_date"`
	BuyUntil  string `json:"buy_until"`
	// IncomeTaxBP — податок на дохід фонду × 100 (14% = 1400), коли фонд
	// доживає до закриття й віддає дохід дивідендами. ExitTaxBP — податок
	// при ДОСТРОКОВОМУ виході, тобто на різницю між купівлею й продажем
	// сертифікатів (для Inzhur MilTech це 23%). Настає рівно одна з двох
	// подій, тож і чисел два.
	IncomeTaxBP int64 `json:"income_tax_bp"`
	ExitTaxBP   int64 `json:"exit_tax_bp"`
	// YieldSimpleYears — за скільки років обіцянка задана ПРОСТОЮ
	// середньорічною. 0 = обіцянка складна, як усі ставки застосунку.
	YieldSimpleYears int64 `json:"yield_simple_years"`
}

// Вид фонду. Розподільний лишається порожнім рядком навмисно: усі наявні
// записи такі, і міграція не мусить їх переписувати.
const (
	FundDistributing = ""
	FundAccumulating = "accum"
)

// checkFundDate — порожньо або YYYY-MM-DD, інакше помилка з назвою поля.
//
// Перевірка стоїть у сховищі, а не лише в обробнику, бо писати в довідник
// уміє ще й відновлення бекапу: дата з чужого файла інакше лягла б у базу
// як завгодно, а зламалась би вже в моделі, за три фази звідси.
func checkFundDate(what, v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return "", fmt.Errorf("%s: очікується РРРР-ММ-ДД, маємо %q", what, v)
	}
	return v, nil
}

func (s *Store) ListFunds(ctx context.Context) ([]Fund, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, currency,
		expected_yield_bp, expected_yield_currency, payout_day,
		kind, close_date, buy_until, income_tax_bp, yield_simple_years,
		exit_tax_bp
		FROM funds ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Fund{}
	for rows.Next() {
		var f Fund
		if err := rows.Scan(&f.ID, &f.Name, &f.Currency,
			&f.ExpectedYieldBP, &f.ExpectedYieldCur, &f.PayoutDay,
			&f.Kind, &f.CloseDate, &f.BuyUntil, &f.IncomeTaxBP,
			&f.YieldSimpleYears, &f.ExitTaxBP); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// RenameFund — виправлення назви більше не розщеплює позицію: операції
// тримаються за id. Досі друкарська помилка в назві мовчки створювала
// другий фонд, і помічалось це вже за фактом.
func (s *Store) RenameFund(ctx context.Context, id int64, f Fund) error {
	name := strings.TrimSpace(f.Name)
	if name == "" {
		return fmt.Errorf("вкажіть назву фонду")
	}
	cur := strings.TrimSpace(f.Currency)
	if cur == "" {
		cur = "UAH"
	}
	if f.PayoutDay < 0 || f.PayoutDay > 31 {
		return fmt.Errorf("день виплати має бути від 1 до 31")
	}
	if f.ExpectedYieldBP < 0 {
		return fmt.Errorf("обіцяна дохідність не може бути відʼємною")
	}
	kind := strings.TrimSpace(f.Kind)
	if kind != FundDistributing && kind != FundAccumulating {
		return fmt.Errorf("вид фонду має бути порожнім (розподільний) або %q", FundAccumulating)
	}
	closeDate, err := checkFundDate("дата закриття", f.CloseDate)
	if err != nil {
		return err
	}
	buyUntil, err := checkFundDate("остання дата купівлі", f.BuyUntil)
	if err != nil {
		return err
	}
	if f.IncomeTaxBP < 0 || f.IncomeTaxBP >= 10000 {
		return fmt.Errorf("податок на дохід має бути від 0 до 100%%")
	}
	if f.ExitTaxBP < 0 || f.ExitTaxBP >= 10000 {
		return fmt.Errorf("податок при виході має бути від 0 до 100%%")
	}
	// Стеля на строк обіцянки — не примха: yield_simple_years стоїть у
	// показнику 1/n, і нуль там дав би ділення на нуль, а сто років —
	// число, яке нічого не означає.
	if f.YieldSimpleYears < 0 || f.YieldSimpleYears > 50 {
		return fmt.Errorf("строк простої дохідності має бути від 1 до 50 років")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE funds SET name=?, currency=?,
		expected_yield_bp=?, expected_yield_currency=?, payout_day=?,
		kind=?, close_date=?, buy_until=?, income_tax_bp=?, yield_simple_years=?,
		exit_tax_bp=? WHERE id=?`,
		name, cur, f.ExpectedYieldBP, strings.TrimSpace(f.ExpectedYieldCur), f.PayoutDay,
		kind, closeDate, buyUntil, f.IncomeTaxBP, f.YieldSimpleYears, f.ExitTaxBP, id)
	if err != nil {
		return err
	}
	return affectedOne(res, "фонд")
}

// DeleteFund — лише порожній: у фонду з операціями видалення означало б
// тихо втратити історію дивідендів разом із податками.
func (s *Store) DeleteFund(ctx context.Context, id int64) error {
	var used int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fund_ops WHERE fund_id=?`, id).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return fmt.Errorf("у фонді %d операцій — спершу видали їх", used)
	}
	// Позначки ціни (0034) — так само перешкода, і відмова тут навмисна
	// замість каскаду. У НПФ видаляти npf_nav разом із рахунком було б
	// законно: рахунок і є єдиним сенсом тієї історії. Тут фонд можна
	// прибрати з довідника й помилково — а разом із ним пішла б вклеєна
	// руками історія цін, якої немає більше ніде. Без цієї перевірки
	// видалення падало б сирою помилкою FK, тобто говорило б те саме, але
	// незрозуміло.
	var marked int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fund_prices WHERE fund_id=?`, id).Scan(&marked); err != nil {
		return err
	}
	if marked > 0 {
		return fmt.Errorf("у фонді %d позначок ціни — спершу видали їх", marked)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM funds WHERE id=?`, id)
	if err != nil {
		return err
	}
	return affectedOne(res, "фонд")
}
