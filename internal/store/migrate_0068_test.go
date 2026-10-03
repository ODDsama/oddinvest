package store

import "testing"

// 0068 прибирає рахунки: таблиці поповнень і конвертацій і дві колонки
// знімка. Решта знімка мусить уціліти рядок у рядок — на ній стоять крива
// «Як росте», дельта за 30 днів і «Ціна моїх рішень».
func TestDropCashAccountsMigration(t *testing.T) {
	db := openRaw(t)
	applyUpTo(t, db, "0068_drop_cash_accounts.sql")
	for _, q := range []string{
		`INSERT INTO deposits(date, amount, currency) VALUES('2026-07-01', 500000, 'UAH')`,
		`INSERT INTO conversions(date, from_currency, from_amount, to_currency, to_amount)
		 VALUES('2026-07-03', 'UAH', 200000, 'USD', 4500)`,
		`INSERT INTO snapshots(date, invested_uah, nominal_uah_eq, usd_share_bp, uninvested_uah,
		 account_uah, idle_uah, funds_uah) VALUES('2026-09-01', 1, 2, 3, 4, 700, 5, 900)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	body, err := migrationsFS.ReadFile("migrations/0068_drop_cash_accounts.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("0068: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE name IN ('deposits', 'conversions')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("таблиці рахунків лишились (%d, %v)", n, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('snapshots')
		WHERE name IN ('account_uah', 'idle_uah')`).Scan(&n); err != nil || n != 0 {
		t.Errorf("колонки знімка лишились (%d, %v)", n, err)
	}
	var funds, unin int64
	if err := db.QueryRow(`SELECT funds_uah, uninvested_uah FROM snapshots WHERE date='2026-09-01'`).
		Scan(&funds, &unin); err != nil || funds != 900 || unin != 4 {
		t.Errorf("знімок пошкоджено: funds=%d uninvested=%d (%v)", funds, unin, err)
	}
}
