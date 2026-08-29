package store

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrate — мінімальний вбудований раннер: таблиця schema_migrations,
// послідовне застосування embed-файлів у транзакціях. Для застосунку з
// однією БД цього достатньо.
//
// Переїзд на golang-migrate механічним НЕ буде, хоч інтерфейс і крихітний:
// версія тут — ім'я файла в TEXT PRIMARY KEY, а не число з прапорцем
// dirty, тож наявні бази довелось би конвертувати; down-міграцій немає
// жодної; 0011 містить date('now','localtime'), тобто не відтворюється;
// а 0010 покладається саме на те, що цей раннер обгортає файл у
// транзакцію (див. коментар у ньому про foreign_keys).
//
// dbPath — де лежить файл бази; порожньо (тести на :memory:) вимикає
// копію перед міграцією.
func migrate(db *sql.DB, dbPath string) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	pending, err := pendingMigrations(db, names)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}
	// КОПІЯ ПЕРЕД ПЕРШОЮ Ж МІГРАЦІЄЮ, і це головна страховка всього
	// пакета. Down-міграцій тут немає ЖОДНОЇ, а застосовуються вони не
	// руками: deploy/proxmox-update.sh робить git pull, збірку й restart,
	// тобто будь-яка нова міграція виконується на живих даних сама, у
	// момент, коли ніхто на неї не дивиться. Доти єдиним, що стояло між
	// помилкою в DDL і невідновними даними, був учорашній JSON-дамп —
	// формат, який відновлює лише те, що хтось не забув у нього вписати.
	//
	// VACUUM INTO, а не копіювання файла: воно віддає узгоджену базу
	// одним викликом, не зупиняючи сервіс і не залежачи від того, що саме
	// зараз у -wal. З'явилось у SQLite 3.27, тобто задовго до будь-якої
	// версії, яку тягне go-sqlite3.
	if dbPath != "" {
		if err := snapshotBeforeMigrate(db, dbPath, pending[0]); err != nil {
			return fmt.Errorf("копія перед міграцією: %w", err)
		}
	}
	for _, name := range pending {
		if err := applyMigration(db, name); err != nil {
			return err
		}
	}
	return nil
}

// pendingMigrations — імена ще не застосованих міграцій, у порядку.
func pendingMigrations(db *sql.DB, names []string) ([]string, error) {
	var out []string
	for _, name := range names {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, name).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			out = append(out, name)
		}
	}
	return out, nil
}

// applyMigration виконує один файл і відмічає його — усе в одній
// транзакції, на що 0010 прямо покладається.
func applyMigration(db *sql.DB, name string) error {
	body, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	// defer, а не відкат у кожній гілці. Гілок сьогодні дві й обидві
	// покриті, але ціна пропущеної — не «зайва транзакція»: при
	// SetMaxOpenConns(1) незакрита транзакція тримає ЄДИНЕ з'єднання
	// процесу, і кожен наступний запит — і HTTP, і джоба — блокується
	// назавжди. defer робить цей клас помилок неможливим наперед.
	defer tx.Rollback() //nolint:errcheck // no-op після Commit
	if _, err := tx.Exec(string(body)); err != nil {
		return fmt.Errorf("міграція %s: %w", name, err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, name); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateBackupKeep — скільки домиграційних копій тримаємо.
const migrateBackupKeep = 3

// snapshotBeforeMigrate кладе копію бази поруч із нею: <ім'я>.pre-<версія>.
//
// Ім'я містить версію, з якої починається пачка, тож із каталогу видно,
// ПЕРЕД чим саме зроблено копію. Якщо така вже є — не перезаписуємо:
// повторний старт того самого бінарника (а systemd має Restart=on-failure)
// інакше затер би добру копію тим станом, який уже частково змігрований.
func snapshotBeforeMigrate(db *sql.DB, dbPath, version string) error {
	dst := dbPath + ".pre-" + strings.TrimSuffix(version, ".sql")
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	// VACUUM INTO відмовляється писати в наявний файл, тож недописаний
	// залишок від перерваної спроби прибираємо самі.
	os.Remove(dst) //nolint:errcheck // «його немає» — теж потрібний результат; про справжню відмову скаже VACUUM INTO нижче
	if _, err := db.Exec(`VACUUM INTO ?`, dst); err != nil {
		return err
	}
	prunePreMigrate(dbPath)
	return nil
}

// prunePreMigrate лишає останні migrateBackupKeep копій. Сортування за
// іменем: у ньому чотиризначний номер міграції, тож лексичний порядок
// збігається з хронологічним.
func prunePreMigrate(dbPath string) {
	dir := filepath.Dir(dbPath)
	prefix := filepath.Base(dbPath) + ".pre-"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			found = append(found, e.Name())
		}
	}
	if len(found) <= migrateBackupKeep {
		return
	}
	sort.Strings(found)
	for _, n := range found[:len(found)-migrateBackupKeep] {
		os.Remove(filepath.Join(dir, n)) //nolint:errcheck // зайва стара копія не привід валити старт
	}
}
