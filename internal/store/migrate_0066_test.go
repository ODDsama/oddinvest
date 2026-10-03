package store

import "testing"

// 0066 прибирає таблицю профілів і водяні знаки чужих виписок, а знак
// Inzhur лишає: без нього перший імпорт після оновлення перебирав би всю
// історію з нуля.
func TestDropImportProfilesMigration(t *testing.T) {
	db := openRaw(t)
	applyUpTo(t, db, "0066_drop_import_profiles.sql")
	for _, k := range []string{"import_since:inzhur@1", "import_since:mono@1", "import_since:inzhur@2", "nav_order"} {
		if _, err := db.Exec(`INSERT INTO app_state(key,value) VALUES(?, '2026-09-01')`, k); err != nil {
			t.Fatal(err)
		}
	}
	body, err := migrationsFS.ReadFile("migrations/0066_drop_import_profiles.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("0066: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='import_profiles'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("таблиця профілів лишилась (%d, %v)", n, err)
	}
	for k, want := range map[string]int{
		"import_since:inzhur@1": 1, "import_since:inzhur@2": 1, "nav_order": 1, "import_since:mono@1": 0,
	} {
		if err := db.QueryRow(`SELECT COUNT(*) FROM app_state WHERE key=?`, k).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("%s: %d рядків, чекали %d", k, n, want)
		}
	}
}
