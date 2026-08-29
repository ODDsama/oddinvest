package api

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ODDsama/oddinvest/internal/store"
)

// TestLoadSourcesFailsLoudOnBrokenRead — збій читання таблиці валить
// збірку документа, а не віддає нулі.
//
// Тест дорожчий за звичайний саме тому, що дешевого способу відтворити
// цю біду немає: потрібна база, у якої ЧАСТИНА таблиць читається, а одна
// ні. Тому таблицю прибирає друге зʼєднання до того самого файла — так,
// як її прибрав би збій, а не як її прибрала б міграція.
//
// Що саме стерегти. Доти сімнадцять читань у loadSources ковтали помилку,
// і зламане читання npf_ops віддавало порожній зріз. Порожній зріз — це
// нуль у документі, а документ іде в MQTT і щодня лягає в добовий
// знімок: збій сховища записувався в історію як «пенсійних внесків того
// дня не було» і ставав невідрізненним від правди. Тому перевіряємо не
// текст помилки, а сам факт, що вона є.
func TestLoadSourcesFailsLoudOnBrokenRead(t *testing.T) {
	// Таблиці, які раніше читались «мʼяко». Кожна — окремий підтест: одна
	// забута в майбутньому правці не сховається за рештою.
	broken := []string{
		"fund_ops", "fund_prices", "term_deposits", "deposits",
		"goals", "goal_ops", "npf_accounts", "npf_ops", "npf_nav",
		"plan_flows", "plan_actions", "plan_receipts", "plan_buys",
		"brokers", "funds", "ovdp_auctions", "fx_rates",
	}
	for _, tbl := range broken {
		t.Run(tbl, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "t.db")
			st, err := store.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()

			log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
			srv := New(st, nil, log)

			// Спершу переконуємось, що на цілій базі документ будується:
			// інакше тест зеленів би з будь-якої іншої причини.
			if _, err := srv.BuildStateDoc(context.Background(), time.Now()); err != nil {
				t.Fatalf("на цілій базі документ не зібрався: %v", err)
			}

			dropTable(t, path, tbl)

			if _, err := srv.BuildStateDoc(context.Background(), time.Now()); err == nil {
				t.Fatalf("%s недоступна, а документ зібрався — читання ковтає помилку "+
					"й видасть нулі в MQTT і в добовий знімок", tbl)
			}
		})
	}
}

// dropTable прибирає таблицю окремим зʼєднанням до того самого файла.
func dropTable(t *testing.T, path, table string) {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("DROP TABLE " + table); err != nil {
		t.Fatalf("не прибрав %s: %v", table, err)
	}
}
