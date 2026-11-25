package store

import "testing"

// 0072: «виписка» дістається брокеру з назвою inzhur (у будь-якому регістрі)
// і тому, під ким імпорт уже писав операції фондів; решта лишається «руками».
func TestBrokerEntryMigration(t *testing.T) {
	db := openRaw(t)
	applyUpTo(t, db, "0072_broker_entry.sql")
	for _, q := range []string{
		`INSERT INTO brokers(portfolio_id, name) VALUES(1, 'Inzhur')`,
		`INSERT INTO brokers(portfolio_id, name) VALUES(1, 'старий брокер')`,
		`INSERT INTO brokers(portfolio_id, name) VALUES(1, 'ICU')`,
		`INSERT INTO funds(name, currency) VALUES('REIT', 'UAH')`,
		`INSERT INTO fund_ops(portfolio_id, date, fund_id, kind, qty, amount, broker_id, note)
			VALUES(1, '2026-01-10', 1, 'buy', 1, 1000, 2, 'виписка')`,
		`INSERT INTO fund_ops(portfolio_id, date, fund_id, kind, qty, amount, broker_id, note)
			VALUES(1, '2026-01-11', 1, 'buy', 1, 1000, 3, 'руками')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	body, err := migrationsFS.ReadFile("migrations/0072_broker_entry.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("0072: %v", err)
	}
	rows, err := db.Query(`SELECT name, entry FROM brokers ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var name, entry string
		if err := rows.Scan(&name, &entry); err != nil {
			t.Fatal(err)
		}
		got[name] = entry
	}
	want := map[string]string{"Inzhur": "statement", "старий брокер": "statement", "ICU": ""}
	for name, entry := range want {
		if got[name] != entry {
			t.Errorf("%s: entry=%q, чекали %q", name, got[name], entry)
		}
	}
}
