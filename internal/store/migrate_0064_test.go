package store

import "testing"

// 0064 переводить законні ставки (2300, 1950) у «за законом» (−1) і не
// чіпає введених руками — як і 0050, перевіряється на старих даних.
func TestDepositTaxByLawMigration(t *testing.T) {
	db := openRaw(t)
	applyUpTo(t, db, "0064_deposit_tax_by_law.sql")
	ins := `INSERT INTO term_deposits(id, currency, principal, rate_bp,
		open_date, maturity_date, tax_bp) VALUES(?,'UAH',?,?,?,?,?)`
	for _, r := range [][]any{
		{1, 100_000_00, 1600, "2024-06-15", "2025-06-15", 2300}, // дефолт після 0050
		{2, 50_000_00, 1400, "2023-02-01", "2023-08-01", 1950},  // зі старого дампу
		{3, 50_000_00, 1400, "2026-02-01", "2026-08-01", 1800},  // пільговий договір
		{4, 10_000_00, 1200, "2026-03-01", "2026-09-01", 0},     // «без податку»
	} {
		if _, err := db.Exec(ins, r...); err != nil {
			t.Fatal(err)
		}
	}
	body, err := migrationsFS.ReadFile("migrations/0064_deposit_tax_by_law.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("0064: %v", err)
	}
	want := map[int64]int64{1: -1, 2: -1, 3: 1800, 4: 0}
	for id, w := range want {
		var got int64
		if err := db.QueryRow(`SELECT tax_bp FROM term_deposits WHERE id=?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Errorf("вклад %d: податок %d, чекали %d", id, got, w)
		}
	}
}
