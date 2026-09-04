package jobs

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// deadNBU — НБУ, який лежить. Саме той стан, у якому раніше зникав бекап.
func deadNBU(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "боляче", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func dailyRunner(t *testing.T, base, backupPath string) (*Runner, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	build := func(context.Context, time.Time) (*state.Doc, error) { return &state.Doc{}, nil }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := New(st, nbu.New(base), nil, build, log, backupPath)
	r.pause = 0
	return r, st
}

// TestBackupSurvivesDeadNBU — головна перевірка всієї перебудови добової
// джоби.
//
// Доти Snapshot і dumpBackup стояли в хвості RefreshAll, за трьома
// `return err` поспіль. Варто було НБУ віддати 500 — і того дня не
// зʼявлялось ні точки на кривій, ні дампа невідновних даних, а сказано
// про це було лише рядком у журналі. Тобто наявність єдиної копії лотів,
// вкладів і плану залежала від чужого HTTP-сервера, який до цих даних не
// має жодного стосунку.
func TestBackupSurvivesDeadNBU(t *testing.T) {
	dir := t.TempDir()
	backup := filepath.Join(dir, "oddinvest-backup.json")
	r, _ := dailyRunner(t, deadNBU(t), backup)

	// Довідник НБУ справді недоступний — інакше перевірка нічого не варта.
	if err := r.RefreshAll(context.Background()); err == nil {
		t.Fatal("НБУ мав відмовити")
	}

	NewFleet(r).dailyRun(context.Background())

	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("бекапу немає попри те, що дані на місці: %v", err)
	}
	if n := len(generations(t, dir)); n != 1 {
		t.Fatalf("поколінь бекапу %d, очікували 1", n)
	}
}

// TestSnapshotSurvivesDeadNBU — те саме про добовий знімок: крива не має
// втрачати день через чужу недоступність.
func TestSnapshotSurvivesDeadNBU(t *testing.T) {
	r, st := dailyRunner(t, deadNBU(t), "")
	ctx := context.Background()

	NewFleet(r).dailyRun(ctx)

	today := domain.NewDate(time.Now().In(r.loc))
	snaps, err := st.ListSnapshots(ctx, today, today)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 {
		t.Fatalf("знімків за сьогодні %d, очікували 1", len(snaps))
	}
}

// TestBackupRotates — покоління накопичуються, а не перезаписують одне
// одного, і зайві прибираються.
//
// Одного файла було мало не через диск: тихий регрес в ExportAll
// знищував ЄДИНУ добру копію рівно за добу.
func TestBackupRotates(t *testing.T) {
	dir := t.TempDir()
	backup := filepath.Join(dir, "oddinvest-backup.json")
	r, _ := dailyRunner(t, deadNBU(t), backup)
	ctx := context.Background()

	// Покоління за минулі дні кладемо руками: іменем керує дата, а
	// підмінити годинник тут нема чим і не варто.
	for i := 1; i <= backupKeep+5; i++ {
		day := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		name := filepath.Join(dir, "oddinvest-backup-"+day+".json")
		if err := os.WriteFile(name, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r.dumpBackup(ctx)

	gens := generations(t, dir)
	if len(gens) != backupKeep {
		t.Fatalf("поколінь %d, очікували %d: %v", len(gens), backupKeep, gens)
	}
	// Прибирати мусило НАЙСТАРІШІ: сьогоднішнє покоління на місці.
	today := "oddinvest-backup-" + time.Now().In(r.loc).Format("2006-01-02") + ".json"
	if _, err := os.Stat(filepath.Join(dir, today)); err != nil {
		t.Fatalf("сьогоднішнє покоління прибрали: %v", err)
	}

	// І дамп читається як бекап, а не як будь-що.
	data, err := os.ReadFile(filepath.Join(dir, today))
	if err != nil {
		t.Fatal(err)
	}
	var b store.Backup
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("дамп не читається: %v", err)
	}
	if b.Schema != store.BackupSchema {
		t.Errorf("schema %d, очікували %d", b.Schema, store.BackupSchema)
	}
}

// generations — датовані покоління бекапу в каталозі (без незмінного імені).
func generations(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "oddinvest-backup-") && strings.HasSuffix(n, ".json") {
			out = append(out, n)
		}
	}
	return out
}
