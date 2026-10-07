package store

import "testing"

// 0069: місце, що збігається з назвою брокера свого портфеля, стає
// посиланням; решта (готівка, сейф, незнайома назва) лишається текстом.
func TestReserveGoalBankMigration(t *testing.T) {
	db := openRaw(t)
	applyUpTo(t, db, "0069_reserve_goal_bank.sql")
	for _, q := range []string{
		`INSERT INTO brokers(portfolio_id, name) VALUES(1, 'Mono')`,
		`INSERT INTO reserve_ops(date, amount, currency, place) VALUES('2026-07-01', 100, 'EUR', ' mono ')`,
		`INSERT INTO reserve_ops(date, amount, currency, place) VALUES('2026-07-02', 200, 'UAH', 'готівка')`,
		`INSERT INTO goals(name, target_amount, currency) VALUES('авто', 1000, 'USD')`,
		`INSERT INTO goal_ops(goal_id, date, amount, currency, place) VALUES(1, '2026-07-03', 300, 'UAH', 'MONO')`,
		`INSERT INTO goal_ops(goal_id, date, amount, currency, place) VALUES(1, '2026-07-04', 400, 'UAH', 'сейф')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	body, err := migrationsFS.ReadFile("migrations/0069_reserve_goal_bank.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("0069: %v", err)
	}
	for _, tbl := range []string{"reserve_ops", "goal_ops"} {
		rows, err := db.Query(`SELECT place, COALESCE(broker_id, 0) FROM ` + tbl + ` ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		var got [][2]any
		for rows.Next() {
			var place string
			var bid int64
			if err := rows.Scan(&place, &bid); err != nil {
				t.Fatal(err)
			}
			got = append(got, [2]any{place, bid})
		}
		rows.Close()
		if len(got) != 2 || got[0] != [2]any{"", int64(1)} || got[1][1] != int64(0) || got[1][0] == "" {
			t.Errorf("%s: %v", tbl, got)
		}
	}
}
