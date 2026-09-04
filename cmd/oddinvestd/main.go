// oddinvestd — сервіс обліку інвестиційного портфеля: REST + веб-UI + MQTT-стан для
// Home Assistant. Деплой: LXC + systemd (див. deploy/).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	// База часових зон — у бінарнику. Без неї LoadLocation залежить від
	// /usr/share/zoneinfo, якого в контейнері може не бути, і тоді зона
	// мовчки лишається серверною — тобто рівно той стан, який лагодить
	// setKyivLocal нижче. ~450 КіБ за гарантію, що зона є завжди.
	_ "time/tzdata"

	"github.com/ODDsama/oddinvest/internal/api"
	"github.com/ODDsama/oddinvest/internal/config"
	"github.com/ODDsama/oddinvest/internal/jobs"
	"github.com/ODDsama/oddinvest/internal/mqtt"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/store"
	"github.com/ODDsama/oddinvest/internal/tunnel"
)

// setKyivLocal робить календар процесу київським, а не серверним.
//
// LXC живе в UTC, а весь internal/api бере голий time.Now() — 54 місця, з
// яких buildStateWith робить today і від якого далі рахується місяць, день і
// кожне порівняння дат. Через це з півночі до 03:00 за Києвом (взимку до
// 02:00) застосунок вважав, що ще вчора. У пересічний день це непомітно, а
// першого числа це не «година різниці», а весь місячний блок від чужого
// місяця: 1 вересня о 00:57 верхній екран показував серпневі 210% виконання
// плану й 56 358 ₴ внесеного, тоді як у новому місяці не внесено ще нічого.
//
// ОДНИМ ПРИСВОЄННЯМ, а не поправкою в кожному виклику. Місць 54, і забуте
// п'ятдесят п'яте не впало б тестом — воно просто раз на місяць називало б
// інший місяць, і шукали б це знову вночі. time.Local читають усі: time.Now(),
// domain.NewDate поверх нього, мітки журналу. Присвоєння безпечне рівно тут —
// до старту горутин і до першого читання зони.
//
// Збережені мітки від цього не змінюються: у сховищі вони скрізь пишуться
// через .UTC().Format(RFC3339) явно.
//
// НЕ через Environment=TZ у юніті: тоді правильність залежала б від файла,
// якого немає ні в тестах, ні при запуску з-під розробника, ні в Dockerfile.
func setKyivLocal() {
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		return // недосяжно: база зон вшита імпортом time/tzdata
	}
	time.Local = loc
}

func main() {
	setKyivLocal() // до всього іншого: далі кожен time.Now() уже київський
	// Прапорець рівно один, і він не про роботу сервісу, а про доступ до
	// нього: забутий пароль інакше не скинеш — секрети лежать у базі
	// хешами (internal/api/auth.go). Тунель при цьому не чіпається:
	// забутий пароль не привід рвати звʼязок, який працює.
	resetAuth := flag.Bool("reset-auth", false,
		"стерти пароль, сесії й токен HA — наступний вхід задасть пароль заново")
	flag.Parse()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg := config.Load()

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("відкриття БД", "path", cfg.DBPath, "err", err)
		os.Exit(1)
	}
	defer st.Close() //nolint:errcheck // закриття БД на виході; реагувати вже нічим

	// ПІСЛЯ store.Open (там міграції), ДО сервера: скидання — це окрема
	// команда, а не режим роботи.
	if *resetAuth {
		if err := st.ResetAuth(context.Background()); err != nil {
			log.Error("скидання пароля", "err", err)
			os.Exit(1)
		}
		log.Info("пароль, сесії й токен HA стерто — відкрий застосунок і задай пароль заново")
		return
	}

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
	// Тунель назовні (internal/tunnel). Створюється тут, а не в api:
	// йому потрібні шлях бази (HOME для конектора) і адреса
	// прослуховування, тобто те, що знає лише main.
	tun := tunnel.NewManager(st, log, "", cfg.ACMEURL,
		tunnel.OriginFromAddr(cfg.HTTPAddr), tunnel.HomeFor(cfg.DBPath))
	srv.SetTunnel(tun)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	fleet := jobs.NewFleet(runner)
	go fleet.RunDaily(ctx)
	// Конектор тунелю, якщо він налаштований: перезапуск сервісу не має
	// вимагати повторного «Підключити» на сторінці.
	tun.Start(ctx)
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

	// ОДИН обробник на обидва слухачі: Handler() щоразу будує новий mux зі
	// ста двадцятьма маршрутами й окремою вбудованою статикою, і два
	// екземпляри означали б дві копії того самого без жодної потреби.
	h := srv.Handler()

	httpSrv := &http.Server{Addr: cfg.HTTPAddr, Handler: h}
	go func() {
		log.Info("http слухає", "addr", cfg.HTTPAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http", "err", err)
			stop()
		}
	}()

	// HTTPS — щоб та сама адреса працювала ВДОМА, повз Cloudflare
	// (internal/tunnel/cert.go). Сертифікат береться з менеджера на кожне
	// рукостискання, тож видача працює без перезапуску демона.
	//
	// Невдача цього слухача НЕ валить сервіс: без права на 443 або із
	// зайнятим портом застосунок лишається застосунком, лише без
	// локального домену. Через це тут ListenAndServeTLS не годиться — його
	// помилку не відрізнити від зупинки, — а stop() свідомо не кличемо.
	var tlsSrv *http.Server
	if cfg.HTTPSAddr != "" {
		tlsSrv = &http.Server{
			Addr: cfg.HTTPSAddr, Handler: h,
			TLSConfig: &tls.Config{GetCertificate: tun.Certificate, MinVersion: tls.VersionTLS12},
		}
		if ln, err := net.Listen("tcp", cfg.HTTPSAddr); err != nil {
			log.Warn("https не слухає — локальний доступ по домену не працюватиме",
				"addr", cfg.HTTPSAddr, "err", err)
			tlsSrv = nil
		} else {
			go func() {
				log.Info("https слухає", "addr", cfg.HTTPSAddr)
				if err := tlsSrv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Error("https", "err", err)
				}
			}()
		}
	}

	<-ctx.Done()
	log.Info("зупинка…")
	// Конектор — дочірній процес, і на нього ЧЕКАЮТЬ: інакше systemd уб'є
	// його разом із демоном, а Cloudflare ще хвилину вважатиме тунель
	// живим і слатиме в нікуди. Перед HTTP: спершу зачиняємо двері
	// назовні, потім свій порт.
	tun.Close()
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shCtx); err != nil {
		log.Error("зупинка HTTP", "err", err)
	}
	if tlsSrv != nil {
		if err := tlsSrv.Shutdown(shCtx); err != nil {
			log.Error("зупинка HTTPS", "err", err)
		}
	}
}
