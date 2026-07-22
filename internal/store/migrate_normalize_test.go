package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
)

// Міграція 0010 перевіряється НА СТАРИХ ДАНИХ, а не на порожній базі:
// найцінніше в ній — не DDL, а перенесення вільного тексту в довідники.
// На порожній базі проходить будь-яка нісенітниця.
//
// Тому тут схема накочується до 0009, засівається так, як виглядає
// справжня база, і лише тоді застосовується 0010.

// applyUpTo накочує міграції за іменами, доки не перетне stopBefore.
//
// Застосоване записується в schema_migrations так само, як це робить
// migrate(): інакше наступний Open спробував би накотити 0001 повторно й
// упав би на «table bonds already exists».
func applyUpTo(t *testing.T, db *sql.DB, stopBefore string) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		if stopBefore != "" && name >= stopBefore {
			break
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("міграція %s: %v", name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, name); err != nil {
			t.Fatal(err)
		}
	}
}

func openRaw(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on",
		filepath.Join(t.TempDir(), "old.db"))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNormalizeMigration(t *testing.T) {
	db := openRaw(t)
	applyUpTo(t, db, "0010_normalize.sql")

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// Брокер згадується в чотирьох таблицях по-різному, плюс у CSV-списку
	// є брокер, якого в жодній операції ще немає — його теж треба забрати.
	exec(`INSERT INTO settings(key,value) VALUES('channels','mono, inzhur, privat24')`)
	exec(`INSERT INTO lots(id,isin,qty,price_per_bond,currency,buy_date,channel,note,fee)
	      VALUES (1,'UA4000239016',3,107715,'UAH','2026-07-16','mono','',0),
	             (2,'UA4000238992',1,104904,'UAH','2026-07-18','','без брокера',0)`)
	// Продажі — саме те, що каскадом стерла б перебудова таблиці lots.
	exec(`INSERT INTO sales(id,lot_id,sale_date,qty,clean_per_bond,accrued,currency,note)
	      VALUES (1,1,'2026-08-01',1,101000,500,'UAH','частину продав')`)
	exec(`INSERT INTO deposits(id,date,amount,currency,broker,note)
	      VALUES (1,'2026-07-01',500000,'UAH','mono','')`)
	exec(`INSERT INTO conversions(id,date,from_currency,from_amount,to_currency,to_amount,broker,note)
	      VALUES (1,'2026-07-03','UAH',200000,'USD',4500,'mono','')`)
	// Два фонди; у другого валюта відрізняється — саме її й має підхопити
	// довідник, а не першу-ліпшу.
	exec(`INSERT INTO fund_ops(id,date,fund,kind,qty,amount,tax,currency,broker,note)
	      VALUES (1,'2024-04-02','Inzhur Ocean','buy',2,805174,0,'UAH','inzhur','виписка'),
	             (2,'2024-05-07','Inzhur Ocean','dividend',0,10160,1066,'UAH','inzhur','виписка'),
	             (3,'2025-01-10','Global REIT','buy',5,50000,0,'USD','ibkr','')`)

	body, err := migrationsFS.ReadFile("migrations/0010_normalize.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("0010: %v", err)
	}

	// --- довідник брокерів зібрався з усіх джерел, включно з CSV ---
	var brokers []string
	rows, err := db.Query(`SELECT name FROM brokers ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		brokers = append(brokers, n)
	}
	rows.Close()
	want := []string{"ibkr", "inzhur", "mono", "privat24"}
	if fmt.Sprint(brokers) != fmt.Sprint(want) {
		t.Errorf("брокери: маємо %v, хочемо %v", brokers, want)
	}

	// --- фонд узяв валюту зі своєї операції, а не з чужої ---
	var cur string
	if err := db.QueryRow(`SELECT currency FROM funds WHERE name='Global REIT'`).Scan(&cur); err != nil {
		t.Fatal(err)
	}
	if cur != "USD" {
		t.Errorf("валюта Global REIT: маємо %q, хочемо USD", cur)
	}

	// --- посилання проставились, порожній брокер лишився порожнім ---
	var lotBroker sql.NullString
	if err := db.QueryRow(`SELECT b.name FROM lots l LEFT JOIN brokers b ON b.id=l.broker_id
	                       WHERE l.id=1`).Scan(&lotBroker); err != nil {
		t.Fatal(err)
	}
	if lotBroker.String != "mono" {
		t.Errorf("брокер лота 1: маємо %q, хочемо mono", lotBroker.String)
	}
	var orphan sql.NullInt64
	if err := db.QueryRow(`SELECT broker_id FROM lots WHERE id=2`).Scan(&orphan); err != nil {
		t.Fatal(err)
	}
	if orphan.Valid {
		t.Errorf("лот без брокера отримав broker_id=%d — мав лишитись NULL", orphan.Int64)
	}

	// --- продажі ВИЖИЛИ: саме їх стерла б перебудова lots із каскадом ---
	var sales int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sales`).Scan(&sales); err != nil {
		t.Fatal(err)
	}
	if sales != 1 {
		t.Errorf("продажів після міграції %d, а був 1 — спрацював ON DELETE CASCADE", sales)
	}

	// --- fund_ops перевʼязані на довідник, id збережені ---
	var fundName string
	var pair sql.NullInt64
	if err := db.QueryRow(`SELECT f.name, o.pair_id FROM fund_ops o
	                       JOIN funds f ON f.id=o.fund_id WHERE o.id=2`).Scan(&fundName, &pair); err != nil {
		t.Fatal(err)
	}
	if fundName != "Inzhur Ocean" {
		t.Errorf("фонд операції 2: маємо %q", fundName)
	}
	if pair.Valid {
		t.Errorf("pair_id мав лишитись порожнім, маємо %d", pair.Int64)
	}

	// --- CSV-список більше не зберігається ---
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key='channels'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("settings('channels') лишився — список брокерів тепер таблиця")
	}

	// --- старих колонок немає ---
	for _, q := range []string{
		`SELECT channel FROM lots`, `SELECT broker FROM deposits`,
		`SELECT broker FROM conversions`, `SELECT fund FROM fund_ops`,
		`SELECT currency FROM fund_ops`,
	} {
		if _, err := db.Exec(q); err == nil {
			t.Errorf("колонка ще на місці: %s", q)
		}
	}
}
