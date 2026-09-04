package jobs

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// TestFleetRefreshesOnceAndPersistsEach — межа флоту: довідник НБУ
// тягнеться ОДИН раз на прогін, а знімок і дамп дістає КОЖЕН портфель у
// своє місце.
func TestFleetRefreshesOnceAndPersistsEach(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "лежить", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	wid, err := st.AddPortfolio(context.Background(), "wife", "Дружина")
	if err != nil {
		t.Fatal(err)
	}
	build := func(context.Context, time.Time) (*state.Doc, error) { return &state.Doc{}, nil }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	nc := nbu.New(srv.URL)
	mainBackup := filepath.Join(dir, "oddinvest-backup.json")
	wifeBackup := filepath.Join(dir, "portfolios", "wife", "oddinvest-backup.json")
	main := New(st, nc, nil, build, log, mainBackup)
	wife := New(st.For(wid), nc, nil, build, log, wifeBackup)
	main.pause, wife.pause = 0, 0

	f := NewFleet(main)
	f.Add("wife", wife)
	ctx := context.Background()
	f.dailyRun(ctx)

	// Один прогін — один похід до НБУ (перший же запит падає на 500, тож
	// далі RefreshAll не йде). Два означали б, що сателіт тягне довідник
	// сам.
	if n := calls.Load(); n != 1 {
		t.Errorf("запитів до НБУ %d, хочемо 1", n)
	}
	today := domain.NewDate(time.Now().In(main.loc))
	for _, s := range []*store.Store{st, st.For(wid)} {
		snaps, err := s.ListSnapshots(ctx, today, today)
		if err != nil {
			t.Fatal(err)
		}
		if len(snaps) != 1 {
			t.Errorf("портфель %d: знімків %d, хочемо 1", s.Portfolio(), len(snaps))
		}
	}
	for _, p := range []string{mainBackup, wifeBackup} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("дампа немає: %v", err)
		}
	}

	// Після Remove сателіт у прогоні не бере участі: його знімок за
	// «завтра» не зʼявиться. Перевіряємо простіше — через довідник:
	// прогін лишається одним походом до НБУ, а руйнувати годинник тут нема
	// чим.
	f.Remove("wife")
	if got := len(f.runners()); got != 1 {
		t.Errorf("після Remove у флоті %d, хочемо 1", got)
	}
}
