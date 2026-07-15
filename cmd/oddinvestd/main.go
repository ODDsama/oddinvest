// oddinvestd — сервіс обліку інвестиційного портфеля: REST + веб-UI + MQTT-стан для
// Home Assistant. Деплой: LXC + systemd (див. deploy/).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ODDsama/oddinvest/internal/api"
	"github.com/ODDsama/oddinvest/internal/config"
	"github.com/ODDsama/oddinvest/internal/jobs"
	"github.com/ODDsama/oddinvest/internal/mqtt"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg := config.Load()

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("відкриття БД", "path", cfg.DBPath, "err", err)
		os.Exit(1)
	}
	defer st.Close()

	var pub *mqtt.Publisher
	if cfg.MQTTAddr != "" {
		pub, err = mqtt.New(cfg.MQTTAddr, cfg.MQTTUser, cfg.MQTTPass, cfg.MQTTPrefix, "oddinvestd")
		if err != nil {
			log.Error("mqtt", "err", err) // не фатально: працюємо без публікації
		} else {
			defer pub.Close()
		}
	}

	nc := nbu.New(cfg.NBUBase)

	// злам циклічної залежності api <-> jobs: сервер створюється без
	// refresher-а, runner отримує збірку стану від сервера, потім
	// refresher доєднується до сервера.
	srv := api.New(st, nil, log)
	runner := jobs.New(st, nc, pub, srv.BuildStateDoc, log)
	srv = api.New(st, runner, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go runner.RunDaily(ctx)
	// стартова публікація, якщо стан уже є
	go func() {
		c, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if err := runner.PublishState(c); err != nil {
			log.Warn("стартова публікація", "err", err)
		}
	}()

	httpSrv := &http.Server{Addr: cfg.HTTPAddr, Handler: srv.Handler()}
	go func() {
		log.Info("http слухає", "addr", cfg.HTTPAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("зупинка…")
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpSrv.Shutdown(shCtx)
}
