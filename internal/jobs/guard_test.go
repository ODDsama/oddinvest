package jobs

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

// Паніка у фоновій задачі стає помилкою, а не смертю процесу: саме на
// цьому тримається повтор добового прогону замість циклу падінь.
func TestGuardTurnsPanicIntoError(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	run := func() (err error) {
		defer guard(log, "проба", &err)
		panic("state.Money: add UAH і USD")
	}
	if err := run(); err == nil {
		t.Fatal("паніка не стала помилкою")
	}
	// Без іменованого результату — просто не падає.
	func() {
		defer guard(log, "проба", nil)
		panic("ще одна")
	}()
}

func TestSleepCtxStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	sleepCtx(ctx, time.Minute)
	if d := time.Since(start); d > time.Second {
		t.Errorf("скасована пауза тривала %v", d)
	}
}
