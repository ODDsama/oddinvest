// Механічна межа DeleteBroker.
//
// Той самий рід перевірки, що в backup_coverage_test.go, і з тієї ж
// причини: перелік таблиць у DeleteBroker ведеться руками, а розходження
// зі схемою виявляє не тест, а користувач — сирим текстом SQLite замість
// зрозумілої відмови.
package store

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"testing"
)

// TestDeleteBrokerCoversAllRefs — DeleteBroker бачить УСІ таблиці з
// broker_id.
//
// Перевірка не читає код, а робить те, що робив би користувач: заводить
// брокера, ставить на нього посилання по черзі в кожній такій таблиці й
// вимагає зрозумілої відмови. Забута таблиця дасть тут не «видалилось
// зайве», а сиру помилку FK — те саме, що бачив користувач до правки.
func TestDeleteBrokerCoversAllRefs(t *testing.T) {
	refs := brokerRefTables(t, openTest(t).db)
	if len(refs) == 0 {
		t.Fatal("жодної таблиці з FK на brokers — перевірка втратила сенс")
	}
	t.Logf("таблиць із broker_id: %d — %v", len(refs), refs)

	for _, tbl := range refs {
		t.Run(tbl, func(t *testing.T) {
			s := openTest(t)
			ctx := context.Background()
			id := seedBroker(t, s.db, "Тест")
			seedBrokerRef(t, s.db, tbl, id)

			err := s.DeleteBroker(ctx, id)
			if err == nil {
				t.Fatalf("%s: брокера видалено попри посилання", tbl)
			}
			// Сира помилка FK означає, що перевірка проґавила таблицю й
			// відмову дала база — саме та біда, яку тест і стереже.
			if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
				t.Fatalf("%s: DeleteBroker не рахує цю таблицю — відмовила база: %v\n"+
					"додай (SELECT COUNT(*) FROM %s WHERE broker_id=?) у DeleteBroker",
					tbl, err, tbl)
			}
		})
	}

	// І навпаки: вільного брокера видалити можна.
	s := openTest(t)
	id := seedBroker(t, s.db, "Нікому не потрібен")
	if err := s.DeleteBroker(context.Background(), id); err != nil {
		t.Fatalf("вільного брокера не видалено: %v", err)
	}
}

// brokerRefTables — таблиці, чий зовнішній ключ веде в brokers.
//
// Питаємо PRAGMA foreign_key_list, а не шукаємо колонку на ім'я:
// відмовляє видаленню саме ключ, тож і перелічувати треба ключі.
func brokerRefTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var out []string
	for _, tbl := range tableNames(t, db) {
		var n int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_foreign_key_list(?) WHERE "table"='brokers'`, tbl).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			out = append(out, tbl)
		}
	}
	sort.Strings(out)
	return out
}

func seedBroker(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO brokers(name) VALUES(?)`, name)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// seedBrokerRef кладе в таблицю мінімальний рядок, що посилається на
// брокера: лише NOT NULL без DEFAULT плюс broker_id.
func seedBrokerRef(t *testing.T, db *sql.DB, table string, brokerID int64) {
	t.Helper()
	// Батьки, яких вимагають окремі випадки.
	if _, err := db.Exec(`INSERT OR IGNORE INTO funds(name, currency) VALUES('Фонд', 'UAH')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO npf_accounts(name) VALUES('НПФ')`); err != nil {
		t.Fatal(err)
	}
	var q string
	switch table {
	case "lots":
		q = `INSERT INTO lots(isin, qty, price_per_bond, currency, buy_date, broker_id)
		     VALUES('UA4000000001', 1, 100000, 'UAH', '2026-01-01', ?)`
	case "deposits":
		q = `INSERT INTO deposits(date, amount, broker_id) VALUES('2026-01-01', 100000, ?)`
	case "conversions":
		q = `INSERT INTO conversions(date, from_currency, from_amount, to_currency, to_amount, broker_id)
		     VALUES('2026-01-01', 'UAH', 100000, 'USD', 2400, ?)`
	case "fund_ops":
		q = `INSERT INTO fund_ops(date, fund_id, kind, qty, amount, broker_id)
		     VALUES('2026-01-01', (SELECT id FROM funds LIMIT 1), 'buy', 1, 100000, ?)`
	case "term_deposits":
		q = `INSERT INTO term_deposits(principal, rate_bp, open_date, maturity_date, broker_id)
		     VALUES(100000, 1500, '2026-01-01', '2027-01-01', ?)`
	case "npf_ops":
		q = `INSERT INTO npf_ops(npf_id, date, units_e6, amount, broker_id)
		     VALUES((SELECT id FROM npf_accounts LIMIT 1), '2026-01-01', 1000000, 100000, ?)`
	case "plan_buys":
		q = `INSERT INTO plan_buys(kind, ref, qty, broker_id)
		     VALUES('bond', 'UA4000000001', 1, ?)`
	default:
		t.Fatalf("нова таблиця з FK на brokers: %s — допиши їй посів тут "+
			"і рядок у DeleteBroker", table)
	}
	if _, err := db.Exec(q, brokerID); err != nil {
		t.Fatalf("посів %s: %v", table, err)
	}
}
