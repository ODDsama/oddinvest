package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Страхувальна копія перед відновленням з бекапу.
//
// Відновлення ЗАМІНЮЄ дані портфеля, і доти єдиним шляхом назад був
// останній щоденний дамп — усе, внесене після нього, зникало. Тепер перед
// ImportAll робиться копія всієї бази поруч із нею: цілісна (quick_check),
// 0600, з власним префіксом «.restore-», щоб її не зачепили ні прибирання
// копій міграцій, ні відкат деплою, що шукає «.pre-».
func TestSafetyCopy(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.AddGoal(context.Background(), Goal{Name: "Авто", TargetAmount: 1, Currency: "UAH"}); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for i := 0; i < SafetyCopiesKeep+2; i++ {
		p, err := st.SafetyCopy(time.Date(2026, 9, 24, 10, 0, i, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(filepath.Base(p), ".restore-") {
			t.Errorf("копія %s без префікса .restore-", p)
		}
		if err := quickCheck(p); err != nil {
			t.Errorf("копія не пройшла перевірку: %v", err)
		}
		paths = append(paths, p)
	}
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if strings.Contains(e.Name(), ".restore-") {
			n++
		}
	}
	if n != SafetyCopiesKeep {
		t.Errorf("копій лишилось %d, чекали %d — старіші прибираються", n, SafetyCopiesKeep)
	}
	if _, err := os.Stat(paths[len(paths)-1]); err != nil {
		t.Errorf("найсвіжіша копія мала лишитись: %v", err)
	}
}
