// Package jobs — фонові процеси: добове оновлення довідника НБУ і курсу,
// щоденний знімок портфеля, публікація стану в MQTT.
package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/mqtt"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

type Runner struct {
	st    *store.Store
	nbu   *nbu.Client
	pub   *mqtt.Publisher // nil = MQTT вимкнено
	build func(ctx context.Context, now time.Time) (*state.Doc, error)
	log   *slog.Logger
	loc   *time.Location
}

func New(st *store.Store, nc *nbu.Client, pub *mqtt.Publisher,
	build func(ctx context.Context, now time.Time) (*state.Doc, error), log *slog.Logger) *Runner {
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		loc = time.FixedZone("EET", 2*3600)
	}
	return &Runner{st: st, nbu: nc, pub: pub, build: build, log: log, loc: loc}
}

// RefreshAll — довідник НБУ + курс USD + знімок + публікація.
func (r *Runner) RefreshAll(ctx context.Context) error {
	secs, err := r.nbu.Securities(ctx)
	if err != nil {
		return err
	}
	if err := r.st.ReplaceDirectory(ctx, secs, time.Now()); err != nil {
		return err
	}
	r.log.Info("довідник НБУ оновлено", "паперів", len(secs))

	rateDate := domain.NewDate(time.Now().In(r.loc))
	for _, code := range []string{"USD", "EUR"} {
		rate, err := r.nbu.Rate(ctx, code)
		if err != nil {
			r.log.Warn("курс недоступний", "code", code, "err", err) // не фатально: працюємо на останньому
			continue
		}
		if err := r.st.SaveRate(ctx, code, rate, rateDate); err != nil {
			return err
		}
		r.log.Info("курс збережено", "code", code, "rate_e4", rate)
	}

	if err := r.Snapshot(ctx); err != nil {
		r.log.Warn("знімок не збережено", "err", err)
	}
	return r.PublishState(ctx)
}

// Snapshot зберігає добовий знімок агрегатів для майбутнього графіка
// «факт vs модель».
func (r *Runner) Snapshot(ctx context.Context) error {
	doc, err := r.build(ctx, time.Now())
	if err != nil {
		return err
	}
	today := domain.NewDate(time.Now().In(r.loc))
	return r.st.SaveSnapshot(ctx, today,
		int64(doc.InvestedUAH*100), int64(doc.NominalUAHEq*100),
		int64(doc.USDSharePct*100), int64(doc.UninvestedUAH*100),
		int64(doc.MonthTargetUAH*100), int64(doc.AccountUAH*100))
}

func (r *Runner) PublishState(ctx context.Context) error {
	if r.pub == nil {
		return nil
	}
	doc, err := r.build(ctx, time.Now())
	if err != nil {
		return err
	}
	b, err := doc.JSON()
	if err != nil {
		return err
	}
	return r.pub.PublishState(b)
}

// RunDaily — цикл: щодня о 06:10 Києва RefreshAll.
func (r *Runner) RunDaily(ctx context.Context) {
	for {
		now := time.Now().In(r.loc)
		next := time.Date(now.Year(), now.Month(), now.Day(), 6, 10, 0, 0, r.loc)
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		r.log.Info("наступне оновлення", "at", next.Format(time.RFC3339))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			if err := r.RefreshAll(cctx); err != nil {
				r.log.Error("добове оновлення", "err", err)
			}
			cancel()
		}
	}
}
