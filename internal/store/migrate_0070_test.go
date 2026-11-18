package store

import "testing"

// Міграція 0070 перевіряється НА СТАРИХ ДАНИХ двох портфелів, як 0054: уся
// її вага — у зведенні рядків, які на порожній базі не існують. Засівається
// рівно та ситуація, заради якої вона писалась: власник і дружина тримають
// той самий фонд, атрибути й ЧВОПА в них розійшлись, а історія вклеєна
// частинами в кожного.
func TestNPFSharedFundMigration(t *testing.T) {
	db := openRaw(t)
	applyUpTo(t, db, "0070_npf_shared_fund.sql")

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
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

	exec(`INSERT INTO portfolios(id,slug,name) VALUES (2,'wife','Дружина')`)
	// Головний: адміністратор і обіцянка задані, ЧВОПА старіша.
	exec(`INSERT INTO npf_accounts(id,portfolio_id,name,administrator,currency,nav_e6,nav_date,
		expected_yield_bp,yield_simple_years,access_date,contrib_day,note)
		VALUES (5,1,'Династія','АЦПО','UAH',3400000,'2026-06-30',1200,3,'2040-01-01',5,'мій')`)
	// Дружина: той самий фонд, атрибути порожні, ЧВОПА свіжіша.
	exec(`INSERT INTO npf_accounts(id,portfolio_id,name,nav_e6,nav_date,access_date,income_tax_bp,note)
		VALUES (9,2,'Династія',3500000,'2026-09-30','2042-05-05',1380,'її')`)
	// Фонд, який є лише в дружини, і без жодної ЧВОПА.
	exec(`INSERT INTO npf_accounts(id,portfolio_id,name) VALUES (11,2,'Емерит')`)
	// Рахунок із найстаршим id, видалений до міграції: лічильник мусить
	// памʼятати його й після перебудови.
	exec(`INSERT INTO npf_accounts(id,portfolio_id,name) VALUES (20,1,'Закритий')`)
	exec(`DELETE FROM npf_accounts WHERE id=20`)

	exec(`INSERT INTO npf_nav(portfolio_id,npf_id,date,nav_e6) VALUES
		(1,5,'2025-12-31',3300000),
		(1,5,'2026-06-30',3400000),
		(2,9,'2026-06-30',3999999),
		(2,9,'2026-09-30',3500000),
		(2,11,'2026-01-01',1100000)`)
	exec(`INSERT INTO npf_ops(portfolio_id,npf_id,date,units_e6,amount)
		VALUES (2,9,'2026-07-05',288005492,100000)`)

	if err := applyMigration(db, "0070_npf_shared_fund.sql"); err != nil {
		t.Fatal(err)
	}

	// --- ключі знову ввімкнені, жоден рядок не лишився сиротою ---
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

	// --- два фонди на три рахунки; атрибути від головного, ЧВОПА свіжіша ---
	if n := one(`SELECT COUNT(*) FROM npf_funds`); n != 2 {
		t.Fatalf("фондів %d, хочемо 2 (Династія зведена, Емерит окремо)", n)
	}
	if got := str(`SELECT administrator FROM npf_funds WHERE name='Династія'`); got != "АЦПО" {
		t.Errorf("адміністратор = %q, хочемо від головного портфеля", got)
	}
	if got := one(`SELECT expected_yield_bp FROM npf_funds WHERE name='Династія'`); got != 1200 {
		t.Errorf("обіцянка = %d, хочемо 1200 від головного", got)
	}
	if got := one(`SELECT yield_simple_years FROM npf_funds WHERE name='Династія'`); got != 3 {
		t.Errorf("роки простої = %d, хочемо 3", got)
	}
	if nav, on := one(`SELECT nav_e6 FROM npf_funds WHERE name='Династія'`),
		str(`SELECT nav_date FROM npf_funds WHERE name='Династія'`); nav != 3500000 || on != "2026-09-30" {
		t.Errorf("остання ЧВОПА = %d на %s, хочемо найсвіжішу: 3500000 на 2026-09-30", nav, on)
	}

	// --- рахунки зберегли id, портфель і особисте; двоє дивляться в один фонд ---
	if a, b := one(`SELECT fund_id FROM npf_accounts WHERE id=5`),
		one(`SELECT fund_id FROM npf_accounts WHERE id=9`); a != b {
		t.Errorf("рахунки 5 і 9 у різних фондах: %d і %d", a, b)
	}
	if got := str(`SELECT access_date||'|'||note||'|'||income_tax_bp||'|'||portfolio_id
		FROM npf_accounts WHERE id=9`); got != "2042-05-05|її|1380|2" {
		t.Errorf("особисте рахунку 9 = %q", got)
	}
	if got := str(`SELECT access_date||'|'||note||'|'||contrib_day FROM npf_accounts WHERE id=5`); got != "2040-01-01|мій|5" {
		t.Errorf("особисте рахунку 5 = %q", got)
	}
	if got := str(`SELECT f.name FROM npf_accounts a JOIN npf_funds f ON f.id=a.fund_id WHERE a.id=11`); got != "Емерит" {
		t.Errorf("рахунок 11 у фонді %q", got)
	}
	if got := str(`SELECT f.name FROM npf_ops o JOIN npf_accounts a ON a.id=o.npf_id
		JOIN npf_funds f ON f.id=a.fund_id`); got != "Династія" {
		t.Errorf("внесок утратив рахунок: %q", got)
	}

	// --- історія зведена; на спільній даті виграла точка головного ---
	if n := one(`SELECT COUNT(*) FROM npf_fund_nav n JOIN npf_funds f ON f.id=n.fund_id
		WHERE f.name='Династія'`); n != 3 {
		t.Errorf("точок Династії %d, хочемо 3 (дві головного + одна лише в дружини)", n)
	}
	if got := one(`SELECT n.nav_e6 FROM npf_fund_nav n JOIN npf_funds f ON f.id=n.fund_id
		WHERE f.name='Династія' AND n.date='2026-06-30'`); got != 3400000 {
		t.Errorf("спірна точка = %d, хочемо 3400000 головного", got)
	}
	if n := one(`SELECT COUNT(*) FROM npf_fund_nav n JOIN npf_funds f ON f.id=n.fund_id
		WHERE f.name='Емерит'`); n != 1 {
		t.Errorf("точок Емериту %d, хочемо 1", n)
	}
	if n := one(`SELECT COUNT(*) FROM sqlite_master WHERE name='npf_nav'`); n != 0 {
		t.Error("npf_nav мала зникнути: історія тепер у npf_fund_nav")
	}

	// --- лічильник пережив перебудову: id видаленого рахунку не роздається
	// вдруге (plan_flows.dest = 'npf:<id>' — текст без ключа) ---
	exec(`INSERT INTO npf_accounts(portfolio_id,fund_id)
		VALUES (1,(SELECT id FROM npf_funds WHERE name='Емерит'))`)
	if id := one(`SELECT MAX(id) FROM npf_accounts`); id != 21 {
		t.Errorf("новий рахунок дістав id %d, хочемо 21 (20 колись був зайнятий)", id)
	}

	// --- один рахунок на фонд у портфелі ---
	if _, err := db.Exec(`INSERT INTO npf_accounts(portfolio_id,fund_id)
		VALUES (2,(SELECT id FROM npf_funds WHERE name='Династія'))`); err == nil {
		t.Error("другий рахунок у тому самому фонді в одному портфелі мав упасти на UNIQUE")
	}

	// --- каскад портфеля забирає його рахунки, чужі не чіпає ---
	exec(`DELETE FROM portfolios WHERE id=2`)
	if n := one(`SELECT COUNT(*) FROM npf_accounts WHERE portfolio_id=2`); n != 0 {
		t.Errorf("%d рахунків пережили видалення портфеля", n)
	}
	if n := one(`SELECT COUNT(*) FROM npf_accounts WHERE id=5`); n != 1 {
		t.Error("каскад зачепив рахунок головного портфеля")
	}
}
