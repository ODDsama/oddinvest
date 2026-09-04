package store

import (
	"database/sql"
	"testing"
)

// scopedTables — таблиці, що після 0054 несуть portfolio_id. Перелік тут
// явний і повторює міграцію навмисно: тест нижче звіряє його зі схемою в
// обидва боки, тож таблиця, забута в одному з двох місць, видна одразу.
var scopedTables = []string{
	"lots", "sales", "deposits", "conversions", "fund_ops", "term_deposits",
	"deposit_topups", "reserve_ops", "goals", "goal_ops", "debts", "debt_ops",
	"debt_marks", "npf_accounts", "npf_ops", "npf_nav", "plan_flows",
	"plan_flow_revisions", "plan_receipts", "plan_actions", "plan_buys",
	"decisions", "snapshots", "settings", "payment_status", "import_profiles",
	"brokers",
}

// Міграція 0054 перевіряється НА СТАРИХ ДАНИХ, як 0010: найдорожче в ній —
// перебудова батьків із дітьми при вимкнених ключах, і саме це на порожній
// базі не перевіряється нічим. Тут засівається база до 0054 із брокером,
// на який посилається лот, і рахунком НПФ, на який посилається ЧВОПА, —
// рядки, які при увімкнених ключах DROP TABLE забрав би з собою.
func TestPortfoliosMigration(t *testing.T) {
	db := openRaw(t)
	applyUpTo(t, db, "0054_portfolios.sql")

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// Робочий стан джоб поруч зі справжнім налаштуванням: перший має
	// переїхати в app_state, друге — лишитись у settings портфеля 1.
	exec(`INSERT INTO settings(key,value) VALUES
		('nbu_refreshed_at','2026-09-01T06:00:00Z'),
		('ovdp_auctions_polled_through','2026-09-01'),
		('usd_target_share_pct','40')`)
	exec(`INSERT INTO brokers(id,name) VALUES (1,'mono'),(2,'inzhur')`)
	exec(`INSERT INTO lots(id,isin,qty,price_per_bond,currency,buy_date,broker_id,note,fee)
	      VALUES (1,'UA4000239016',3,107715,'UAH','2026-07-16',2,'',0)`)
	exec(`INSERT INTO deposits(id,date,amount,currency,broker_id,note)
	      VALUES (1,'2026-07-01',500000,'UAH',1,'')`)
	exec(`INSERT INTO npf_accounts(id,name) VALUES (1,'Династія')`)
	exec(`INSERT INTO npf_nav(npf_id,date,nav_e6) VALUES (1,'2026-08-01',3472156)`)
	exec(`INSERT INTO snapshots(date,invested_uah,nominal_uah_eq,usd_share_bp,uninvested_uah)
	      VALUES ('2026-09-01',1,2,3,4)`)
	exec(`INSERT INTO payment_status(isin,pay_date,status,marked_at)
	      VALUES ('UA4000239016','2026-09-01','received','2026-09-01T00:00:00Z')`)
	exec(`INSERT INTO import_profiles(name) VALUES ('inzhur')`)

	if err := applyMigration(db, "0054_portfolios.sql"); err != nil {
		t.Fatal(err)
	}

	one := func(q string, args ...any) int64 {
		t.Helper()
		var n int64
		if err := db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	str := func(q string, args ...any) string {
		t.Helper()
		var v string
		if err := db.QueryRow(q, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return v
	}

	// --- ключі знову ввімкнені, і жоден рядок не лишився сиротою ---
	if on := one(`PRAGMA foreign_keys`); on != 1 {
		t.Fatalf("foreign_keys після міграції = %d, мусить бути 1", on)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		rows.Close()
		t.Fatal("foreign_key_check знайшов порушення після міграції")
	}
	rows.Close()

	// --- портфель 1 є, і все наявне тепер його ---
	if got := str(`SELECT slug FROM portfolios WHERE id=1`); got != "main" {
		t.Errorf("portfolios[1].slug = %q", got)
	}
	for _, tbl := range scopedTables {
		if n := one(`SELECT COUNT(*) FROM ` + tbl + ` WHERE portfolio_id<>1`); n != 0 {
			t.Errorf("%s: %d рядків не в портфелі 1", tbl, n)
		}
	}

	// --- робочий стан джоб переїхав, налаштування лишилось ---
	if got := str(`SELECT value FROM app_state WHERE key='nbu_refreshed_at'`); got != "2026-09-01T06:00:00Z" {
		t.Errorf("nbu_refreshed_at не переїхав у app_state: %q", got)
	}
	if n := one(`SELECT COUNT(*) FROM app_state`); n != 2 {
		t.Errorf("app_state: %d рядків, хочемо 2", n)
	}
	// Лічити рядки не можна: 0011 сама заводить import_since. Питаємо
	// про ключі поіменно.
	if n := one(`SELECT COUNT(*) FROM settings WHERE key IN ('nbu_refreshed_at','ovdp_auctions_polled_through')`); n != 0 {
		t.Errorf("settings: %d рядків робочого стану лишились після переїзду", n)
	}
	if got := str(`SELECT value FROM settings WHERE key='usd_target_share_pct'`); got != "40" {
		t.Errorf("usd_target_share_pct = %q, хочемо 40", got)
	}

	// --- перебудовані батьки зберегли id, діти й далі знаходять їх ---
	if got := str(`SELECT b.name FROM lots l JOIN brokers b ON b.id=l.broker_id WHERE l.id=1`); got != "inzhur" {
		t.Errorf("лот утратив брокера: %q", got)
	}
	if got := str(`SELECT b.name FROM deposits d JOIN brokers b ON b.id=d.broker_id WHERE d.id=1`); got != "mono" {
		t.Errorf("поповнення втратило брокера: %q", got)
	}
	if got := str(`SELECT a.name FROM npf_nav n JOIN npf_accounts a ON a.id=n.npf_id`); got != "Династія" {
		t.Errorf("ЧВОПА втратила рахунок: %q", got)
	}
	if n := one(`SELECT COUNT(*) FROM snapshots WHERE date='2026-09-01'`); n != 1 {
		t.Errorf("знімок загублено")
	}
	if n := one(`SELECT COUNT(*) FROM payment_status WHERE isin='UA4000239016'`); n != 1 {
		t.Errorf("статус виплати загублено")
	}
	if n := one(`SELECT COUNT(*) FROM import_profiles WHERE name='inzhur'`); n != 1 {
		t.Errorf("профіль імпорту загублено")
	}
	// Лічильник AUTOINCREMENT пережив перебудову: новий брокер не
	// перевикористає id, який колись мав інший.
	exec(`INSERT INTO brokers(name) VALUES ('privat')`)
	if id := one(`SELECT id FROM brokers WHERE name='privat'`); id != 3 {
		t.Errorf("новий брокер дістав id %d, хочемо 3", id)
	}

	// --- ключі справді стали складеними: другий портфель може мати «mono»,
	// ту саму ставку, знімок за той самий день і той самий профіль ---
	exec(`INSERT INTO portfolios(id,slug,name) VALUES (2,'wife','Дружина')`)
	exec(`INSERT INTO brokers(portfolio_id,name) VALUES (2,'mono')`)
	exec(`INSERT INTO npf_accounts(portfolio_id,name) VALUES (2,'Династія')`)
	exec(`INSERT INTO settings(portfolio_id,key,value) VALUES (2,'usd_target_share_pct','50')`)
	exec(`INSERT INTO snapshots(portfolio_id,date,invested_uah,nominal_uah_eq,usd_share_bp,uninvested_uah)
	      VALUES (2,'2026-09-01',0,0,0,0)`)
	exec(`INSERT INTO payment_status(portfolio_id,isin,pay_date,status,marked_at)
	      VALUES (2,'UA4000239016','2026-09-01','received','2026-09-01T00:00:00Z')`)
	exec(`INSERT INTO import_profiles(portfolio_id,name) VALUES (2,'inzhur')`)

	// --- і каскад: портфель зникає разом з усім своїм, чуже не чіпаючи ---
	exec(`INSERT INTO lots(portfolio_id,isin,qty,price_per_bond,currency,buy_date,broker_id,note,fee)
	      VALUES (2,'UA4000239016',1,100000,'UAH','2026-09-01',
	              (SELECT id FROM brokers WHERE portfolio_id=2),'',0)`)
	exec(`DELETE FROM portfolios WHERE id=2`)
	for _, tbl := range scopedTables {
		if n := one(`SELECT COUNT(*) FROM ` + tbl + ` WHERE portfolio_id=2`); n != 0 {
			t.Errorf("%s: %d рядків пережили видалення портфеля", tbl, n)
		}
	}
	if n := one(`SELECT COUNT(*) FROM lots`); n != 1 {
		t.Errorf("каскад зачепив чужий портфель: лотів %d, хочемо 1", n)
	}
}

// TestScopedTablesMatchSchema — перелік scopedTables і схема кажуть одне й
// те саме: кожна таблиця з переліку має portfolio_id із каскадом на
// portfolios, і жодна таблиця поза переліком portfolio_id не має. Обидва
// боки, бо забути можна в обидва.
func TestScopedTablesMatchSchema(t *testing.T) {
	s := openTest(t)
	want := map[string]bool{}
	for _, tbl := range scopedTables {
		want[tbl] = true
	}
	for _, tbl := range tableNames(t, s.db) {
		has, cascade := portfolioColumn(t, s.db, tbl)
		switch {
		case want[tbl] && !has:
			t.Errorf("%s у scopedTables, але колонки portfolio_id немає", tbl)
		case want[tbl] && !cascade:
			t.Errorf("%s: portfolio_id без ON DELETE CASCADE на portfolios", tbl)
		case !want[tbl] && has:
			t.Errorf("%s має portfolio_id, але в scopedTables не названа", tbl)
		}
	}
}

// portfolioColumn — чи має таблиця portfolio_id і чи веде він каскадом на
// portfolios(id).
func portfolioColumn(t *testing.T, db *sql.DB, table string) (has, cascade bool) {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if name == "portfolio_id" {
			has = true
		}
	}
	rows.Close()
	if !has {
		return false, false
	}
	fks, err := db.Query(`PRAGMA foreign_key_list(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer fks.Close()
	for fks.Next() {
		var id, seq int
		var parent, from, to, onUpdate, onDelete, match string
		if err := fks.Scan(&id, &seq, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatal(err)
		}
		if from == "portfolio_id" && parent == "portfolios" && onDelete == "CASCADE" {
			cascade = true
		}
	}
	return has, cascade
}
