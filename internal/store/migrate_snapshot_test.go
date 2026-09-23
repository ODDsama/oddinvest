// Домиграційна копія бази.
//
// Страховка, заради якої вона й з'явилась: down-міграцій немає ЖОДНОЇ, а
// накочуються вони самі, при старті сервісу після deploy/proxmox-update.sh.
package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPreMigrateSnapshot — перед пачкою міграцій поруч із базою лягає її
// копія.
//
// Страховка, заради якої все й затівалось: down-міграцій немає жодної, а
// накочуються вони самі, при старті сервісу після deploy/proxmox-update.sh.
// Перевіряємо, що копія зʼявляється, що вона придатна до читання й що
// повторний старт її НЕ перезаписує — інакше рестарт після невдалої
// міграції затер би добрий стан частково зміграваним.
func TestPreMigrateSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.db")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	pre := findPreMigrate(t, dir)
	if pre == "" {
		t.Fatal("копії перед міграцією немає")
	}
	if !strings.Contains(pre, "0001_init") {
		t.Errorf("копія названа за %q, очікували перед першою міграцією", pre)
	}
	info, err := os.Stat(filepath.Join(dir, pre))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("копія порожня")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Другий Open: незастосованих міграцій немає, тож нової копії теж.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if again := findPreMigrate(t, dir); again != pre {
		t.Errorf("другий старт зробив ще одну копію: було %q, стало %q", pre, again)
	}
}

func findPreMigrate(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".pre-") {
			return e.Name()
		}
	}
	return ""
}

// Напівзаписана копія перед міграцією не вважається страховкою.
//
// VACUUM INTO писав одразу в кінцеве імʼя, а наявна копія з тим самим
// імʼям пропускалась без перевірки. Переривання посеред копіювання (диск
// повний, SIGTERM від відкату деплою) лишало обрізаний файл — і наступний
// старт мігрував базу, маючи за страховку сміття. Тепер копія пишеться в
// .tmp і перейменовується лише після quick_check, а наявна бита копія
// переробляється: pending[0] у її імені ще не застосований, тож база досі
// в стані «до» і перезапис безпечний.
func TestPreMigrateBrokenCopyIsRedone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.db")
	broken := path + ".pre-0001_init"
	if err := os.WriteFile(broken, []byte("SQLite format 3\x00обрізано"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := quickCheck(broken); err != nil {
		t.Fatalf("копія лишилась битою: %v", err)
	}
	if _, err := os.Stat(broken + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("по собі лишився .tmp: %v", err)
	}
	if info, err := os.Stat(broken); err == nil && info.Mode().Perm() != 0o600 {
		t.Errorf("права копії %v — у ній секрети, мусить бути 0600", info.Mode().Perm())
	}
}

// База, новіша за бінарник, не відкривається.
//
// Бігун міграцій дивився лише на ВІДОМІ йому файли. Відкат деплою
// запускав старий бінарник над схемою, яку змінила нова міграція, і той
// мовчки писав рядки, що порушують нові інваріанти (нові колонки без
// значень, сентинели −1 як нулі). Тепер старт відмовляє з поясненням, що
// робити: відновити копію .pre-<версія> або поставити новіший бінарник.
func TestNewerSchemaRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO schema_migrations(version) VALUES('9999_future.sql')`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if s2, err := Open(path); err == nil {
		s2.Close()
		t.Fatal("старий бінарник відкрив базу з міграцією, якої не знає")
	} else if !strings.Contains(err.Error(), "9999_future") {
		t.Errorf("помилка не називає невідому міграцію: %v", err)
	}
}
