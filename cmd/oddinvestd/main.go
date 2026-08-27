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
	"path/filepath"
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
	defer st.Close() //nolint:errcheck // закриття БД на виході; реагувати вже нічим

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
	// щоденний JSON-дамп поряд із БД — потрапляє в бекап Proxmox і
	// переживає навіть пошкодження SQLite-файла
	backupPath := filepath.Join(filepath.Dir(cfg.DBPath), "oddinvest-backup.json")
	runner := jobs.New(st, nc, pub, srv.BuildStateDoc, log, backupPath)
	srv = api.New(st, runner, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go runner.RunDaily(ctx)
	// Історія курсу за десять років — з неї вимірюється знецінення
	// гривні, а без неї застосунок відкочується на припущену шістку.
	// У фоні й лише коли історії справді мало: це ~120 запитів до НБУ
	// на валюту, разово, і сервіс не має через них чекати на старті.
	//
	// ВАЛЮТ ДВІ, а не одна, і друга зʼявилась разом із розкладкою валютної
	// частки між доларом і євро. Щоденна джоба пише обидва курси, але
	// бекфілився лише долар — тож у євро точки були з дня встановлення
	// демона. Наслідки видно рівно там, де євро стає часткою портфеля:
	// картка «Курс серед історії» мовчить про вікна в 3 і 10 років, бо їм
	// бракує точок, а зведений XIRR у гривні працює за правилом «усе або
	// нічого на пропущений курс» — один старий євро-потік без курсу на свою
	// дату здатен забрати всю відповідь.
	//
	// Послідовно в одній горутині: два цикли по чужому сервісу паралельно —
	// рівно те зловживання, від якого відмовляється сама BackfillRates.
	// Тридцяти хвилин вистачає з великим запасом (≈240 запитів × 250 мс).
	go func() {
		c, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		runner.BackfillIfThin(c, "USD", 10, 100)
		runner.BackfillIfThin(c, "EUR", 10, 100)
	}()
	// Історія аукціонів Мінфіну за рік — з неї будується єдиний у
	// застосунку орієнтир «скільки ринок платить за строк». ~365 запитів
	// разово, теж у фоні й теж лише коли історії справді мало.
	go func() {
		c, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		runner.BackfillAuctionsIfThin(c, 52, 20)
	}()
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
	if err := httpSrv.Shutdown(shCtx); err != nil {
		log.Error("зупинка HTTP", "err", err)
	}
}
