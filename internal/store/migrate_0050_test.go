package store

import "testing"

// Міграція 0050 перевіряється НА СТАРИХ ДАНИХ, як 0043: усе цінне в ній —
// не сама ставка, а те, ЯКІ рядки вона чіпає. На порожній базі проходить
// будь-який UPDATE, зокрема той, що затирає введене руками.
func TestDepositTax23Migration(t *testing.T) {
	db := openRaw(t)
	applyUpTo(t, db, "0050_deposit_tax_23.sql")

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	ins := `INSERT INTO term_deposits(id, currency, principal, rate_bp,
		open_date, maturity_date, tax_bp) VALUES(?,'UAH',?,?,?,?,?)`
	// 1 — старий дефолт форми: його й лагодимо.
	exec(ins, 1, 100_000_00, 1600, "2026-01-15", "2027-01-15", 1950)
	// 2 — ставка, введена руками (пільговий договір): міграція не має права
	// її чіпати, бо власник знає свій договір, а міграція — ні.
	exec(ins, 2, 50_000_00, 1400, "2026-02-01", "2026-08-01", 1800)
	// 3 — «без податку» нулем: теж свідомий вибір, а не пропуск.
	exec(ins, 3, 10_000_00, 1200, "2026-03-01", "2026-09-01", 0)
	// 4 — уже 2300: міграція повторна, і другий прогін нічого не ламає.
	exec(ins, 4, 20_000_00, 1750, "2026-03-01", "2027-03-01", 2300)

	body, err := migrationsFS.ReadFile("migrations/0050_deposit_tax_23.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("0050: %v", err)
	}

	rows, err := db.Query(`SELECT id, tax_bp FROM term_deposits ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[int64]int64{}
	for rows.Next() {
		var id, tax int64
		if err := rows.Scan(&id, &tax); err != nil {
			t.Fatal(err)
		}
		got[id] = tax
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[int64]int64{1: 2300, 2: 1800, 3: 0, 4: 2300}
	if len(got) != len(want) {
		t.Fatalf("жоден рядок не мав зникнути: маємо %d із %d — %v", len(got), len(want), got)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("вклад %d: податок %d, очікували %d", id, got[id], w)
		}
	}
}
