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
type Fund struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	Currency         string `json:"currency"`
	ExpectedYieldBP  int64  `json:"expected_yield_bp"`
	ExpectedYieldCur string `json:"expected_yield_currency"`
	PayoutDay        int64  `json:"payout_day"`
}

func (s *Store) ListFunds(ctx context.Context) ([]Fund, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, currency,
		expected_yield_bp, expected_yield_currency, payout_day
		FROM funds ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Fund{}
	for rows.Next() {
		var f Fund
		if err := rows.Scan(&f.ID, &f.Name, &f.Currency,
			&f.ExpectedYieldBP, &f.ExpectedYieldCur, &f.PayoutDay); err != nil {
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
	res, err := s.db.ExecContext(ctx, `UPDATE funds SET name=?, currency=?,
		expected_yield_bp=?, expected_yield_currency=?, payout_day=? WHERE id=?`,
		name, cur, f.ExpectedYieldBP, strings.TrimSpace(f.ExpectedYieldCur), f.PayoutDay, id)
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
	res, err := s.db.ExecContext(ctx, `DELETE FROM funds WHERE id=?`, id)
	if err != nil {
		return err
	}
	return affectedOne(res, "фонд")
}
