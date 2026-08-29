package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// Міграція 0043 перевіряється НА СТАРИХ ДАНИХ, з тієї ж причини, що й
// 0010: найцінніше в ній — не ALTER TABLE, а перенесення назви брокера в
// посилання. На порожній базі проходить будь-яка нісенітниця.
func TestPlanBuysBrokerMigration(t *testing.T) {
	db := openRaw(t)
	applyUpTo(t, db, "0043_plan_buys_broker.sql")

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO brokers(name) VALUES('Фрідом'), ('Універ')`)
	// Три випадки, які й вирішують: назва є в довіднику; назва з пробілами
	// (форми їх лишають); назви немає взагалі — це «обрати за залишком».
	exec(`INSERT INTO plan_buys(id, kind, ref, qty, broker) VALUES(1,'bond','UA4000227748',10,'Фрідом')`)
	exec(`INSERT INTO plan_buys(id, kind, ref, qty, broker) VALUES(2,'fund','Inzhur',5,'  Універ ')`)
	exec(`INSERT INTO plan_buys(id, kind, ref, qty, broker) VALUES(3,'deposit','ПУМБ',0,'')`)
	// Брокер, якого в довіднику немає: рядок мусить ВЦІЛІТИ, лише
	// втративши привʼязку — це найм'якіший наслідок і про нього сказано в
	// самій міграції.
	exec(`INSERT INTO plan_buys(id, kind, ref, qty, broker) VALUES(4,'bond','UA4000000002',1,'Зниклий')`)

	body, err := migrationsFS.ReadFile("migrations/0043_plan_buys_broker.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("0043: %v", err)
	}

	rows, err := db.Query(`SELECT b.id, COALESCE(br.name,'') FROM plan_buys b
		LEFT JOIN brokers br ON br.id = b.broker_id ORDER BY b.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatal(err)
		}
		got[id] = name
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[int64]string{1: "Фрідом", 2: "Універ", 3: "", 4: ""}
	if len(got) != len(want) {
		t.Fatalf("жоден рядок не мав зникнути: маємо %d із %d — %v", len(got), len(want), got)
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("рядок %d: брокер %q, очікували %q", id, got[id], w)
		}
	}
}

// TestPlanBuyBrokerSurvivesRename — заради чого 0043 і робилась.
//
// До неї план тримався за НАЗВУ, тож виправлення описки в довіднику тихо
// відчіплювало рядок від рахунку: pickBroker переставав знаходити залишок
// там, де він насправді лежить. Перевіряємо не міграцію, а наслідок.
func TestPlanBuyBrokerSurvivesRename(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	id, err := s.AddPlanBuy(ctx, PlanBuy{Kind: BuyBond, Ref: "UA4000227748", Qty: 10, Broker: "Фрідм"})
	if err != nil {
		t.Fatal(err)
	}
	brokers, err := s.ListBrokers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var brokerID int64
	for _, b := range brokers {
		if b.Name == "Фрідм" {
			brokerID = b.ID
		}
	}
	if brokerID == 0 {
		t.Fatal("брокер не завівся з плану купівель")
	}
	if err := s.RenameBroker(ctx, brokerID, "Фрідом"); err != nil {
		t.Fatal(err)
	}

	buys, err := s.ListPlanBuys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range buys {
		if b.ID != id {
			continue
		}
		if b.Broker != "Фрідом" {
			t.Fatalf("план не підхопив перейменування: брокер %q", b.Broker)
		}
		return
	}
	t.Fatal("рядок плану зник")
}

// TestPreMigrateSnapshotOnExistingDB — сценарій, який станеться на бойовій
// машині: база вже на 0041, deploy/proxmox-update.sh перезапускає сервіс,
// і три нові міграції накочуються самі.
//
// Перевіряємо не «копія є», а що вона названа за ПЕРШОЮ незастосованою
// міграцією і зроблена ДО неї: копія, зроблена після, не рятує ні від чого.
func TestPreMigrateSnapshotOnExistingDB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.db")

	// База «як була до оновлення»: усе до 0042, з даними.
	dsn := "file:" + path + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	applyUpTo(t, db, "0042_fk_indexes.sql")
	if _, err := db.Exec(`INSERT INTO brokers(name) VALUES('Фрідом')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO plan_buys(kind, ref, qty, broker)
		VALUES('bond','UA4000227748',10,'Фрідом')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Оновлення: Open накочує 0042-0044.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("оновлення не пройшло: %v", err)
	}
	defer s.Close()

	pre := findPreMigrate(t, dir)
	if !strings.Contains(pre, "0042_fk_indexes") {
		t.Fatalf("копія названа %q, очікували за 0042 — першою незастосованою", pre)
	}

	// У копії мусить бути стан ДО міграцій: колонка broker ще на місці.
	old, err := sql.Open("sqlite3", "file:"+filepath.Join(dir, pre)+"?_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	var n int
	if err := old.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('plan_buys') WHERE name='broker'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("копія знята вже ПІСЛЯ міграції — вона не рятує від помилки в ній")
	}

	// А в самій базі план вцілів і підхопив broker_id.
	buys, err := s.ListPlanBuys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(buys) != 1 || buys[0].Broker != "Фрідом" {
		t.Fatalf("план купівель після оновлення: %+v", buys)
	}
}
