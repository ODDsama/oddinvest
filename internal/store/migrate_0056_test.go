package store

import "testing"

// 0056 перевіряється НА СТАРИХ ДАНИХ у тій частині, де це має сенс:
// таблиця нова, тож переносити нічого, але каскад від портфеля й CHECK на
// paid_from на порожній базі не перевіряються нічим. Саме вони тут і
// стоять: CHECK читає АРИФМЕТИКА (який контур платить), а каскад — єдине,
// що прибирає витрати разом із видаленим портфелем.
func TestPlanExpensesMigration(t *testing.T) {
	db := openRaw(t)
	applyUpTo(t, db, "0056_plan_expenses.sql")

	if _, err := db.Exec(`INSERT INTO portfolios(id,slug,name) VALUES (2,'wife','Дружина')`); err != nil {
		t.Fatal(err)
	}
	if err := applyMigration(db, "0056_plan_expenses.sql"); err != nil {
		t.Fatal(err)
	}

	// Типове значення — 'card': побутовий контур, бо саме там за побудовою
	// живе «все інше». Рядок без paid_from мусить прийти саме туди, інакше
	// перша ж витрата, заведена старим клієнтом, тиснула б не на той бік.
	if _, err := db.Exec(`INSERT INTO plan_expenses(id,name,amount,currency,due_date)
		VALUES (1,'Котел',3000000,'UAH','2026-11-15')`); err != nil {
		t.Fatal(err)
	}
	var from, paid string
	var pid int64
	if err := db.QueryRow(
		`SELECT paid_from, paid_date, portfolio_id FROM plan_expenses WHERE id=1`).
		Scan(&from, &paid, &pid); err != nil {
		t.Fatal(err)
	}
	if from != "card" {
		t.Errorf("типовий контур %q, а мав бути card", from)
	}
	if paid != "" {
		t.Errorf("нова витрата приходить сплаченою (paid_date=%q) — вона щойно заведена", paid)
	}
	if pid != MainPortfolio {
		t.Errorf("витрата лягла в портфель %d, а мала в головний", pid)
	}

	// Описка в контурі мусить упасть тут, а не мовчки перекинути гроші не
	// в той бік: цей набір читає арифметика, і помилку в ньому не видно
	// ніде, крім чисел, які вже неправильні.
	if _, err := db.Exec(`INSERT INTO plan_expenses(name,amount,currency,due_date,paid_from)
		VALUES ('Гуми',800000,'UAH','2027-03-01','wallet')`); err == nil {
		t.Error("paid_from='wallet' прийнято, хоча CHECK мав його відхилити")
	}

	// Каскад: витрати чужого портфеля йдуть разом із ним. Без нього
	// видалений портфель лишав би по собі рядки, які нікому не належать.
	if _, err := db.Exec(`INSERT INTO plan_expenses(portfolio_id,name,amount,currency,due_date)
		VALUES (2,'Страховка',1200000,'UAH','2026-10-14')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM portfolios WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM plan_expenses`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("після видалення портфеля лишилось %d витрат, а мала лишитись 1", n)
	}

	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Error("після 0056 є висячі зовнішні ключі")
	}
}
