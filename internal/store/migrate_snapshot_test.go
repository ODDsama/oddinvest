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
