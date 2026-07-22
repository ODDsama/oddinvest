package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Перевірка міграції 0010 на СПРАВЖНІХ даних.
//
// Синтетична фікстура доводить, що SQL робить те, що написано; вона не
// доводить, що переїде саме твоя база — з її брокерами, фондами, порожніми
// полями й записами, які накопичились за роки. Тому тут беремо знімок
// живого бекапу (до міграції), розгортаємо його в СТАРІЙ схемі, проганяємо
// міграцію й перевіряємо, що вивантаження після неї збігається з тим, що
// було до, рядок у рядок.
//
// Файл зі знімком у репозиторій не потрапляє — це реальний портфель, а
// репозиторій публічний. Без нього тест пропускається:
//
//	ODDINVEST_LIVE_BACKUP=/tmp/live/backup.json go test ./internal/store -run Live -v
func TestMigrationOnLiveBackup(t *testing.T) {
	path := os.Getenv("ODDINVEST_LIVE_BACKUP")
	if path == "" {
		t.Skip("ODDINVEST_LIVE_BACKUP не задано — перевірка на живих даних пропущена")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var before Backup
	if err := json.Unmarshal(raw, &before); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(t.TempDir(), "live.db")
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	applyUpTo(t, db, "0010_normalize.sql")

	// Розгортаємо знімок СТАРИМИ вставками — так, як його записала б
	// база до нормалізації.
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s\n%v", q, err)
		}
	}
	for _, l := range before.Lots {
		exec(`INSERT INTO lots (id,isin,qty,price_per_bond,currency,buy_date,channel,fee,note)
		      VALUES (?,?,?,?,?,?,?,?,?)`,
			l.ID, l.ISIN, l.Qty, l.Price, l.Currency, l.BuyDate, l.Channel, l.Fee, l.Note)
	}
	for _, s := range before.Sales {
		exec(`INSERT INTO sales (id,lot_id,sale_date,qty,clean_per_bond,accrued,currency,note)
		      VALUES (?,?,?,?,?,?,?,?)`,
			s.ID, s.LotID, s.SaleDate, s.Qty, s.Clean, s.Accrued, s.Currency, s.Note)
	}
	for _, d := range before.Deposits {
		exec(`INSERT INTO deposits (id,date,amount,currency,broker,note) VALUES (?,?,?,?,?,?)`,
			d.ID, d.Date, d.Amount, d.Currency, d.Broker, d.Note)
	}
	for _, c := range before.Conversions {
		exec(`INSERT INTO conversions (id,date,from_currency,from_amount,to_currency,to_amount,broker,note)
		      VALUES (?,?,?,?,?,?,?,?)`,
			c.ID, c.Date, c.FromCurrency, c.FromAmount, c.ToCurrency, c.ToAmount, c.Broker, c.Note)
	}
	for _, o := range before.FundOps {
		exec(`INSERT INTO fund_ops (id,date,fund,kind,qty,amount,tax,currency,broker,note)
		      VALUES (?,?,?,?,?,?,?,?,?,?)`,
			o.ID, o.Date, o.Fund, o.Kind, o.Qty, o.Amount, o.Tax, o.Currency, o.Broker, o.Note)
	}
	for k, v := range before.Settings {
		exec(`INSERT INTO settings (key,value) VALUES (?,?)`, k, v)
	}
	for _, p := range before.PaymentStatus {
		exec(`INSERT INTO payment_status (isin,pay_date,status,marked_at) VALUES (?,?,?,?)`,
			p.ISIN, p.PayDate, p.Status, p.MarkedAt)
	}
	db.Close()

	// А тепер — звичайне відкриття, яке докотить 0010.
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("міграція живої бази: %v", err)
	}
	defer st.Close()

	after, err := st.ExportAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	eq := func(name string, got, want any) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s розійшлись після міграції:\n після: %+v\n до:    %+v", name, got, want)
		}
	}
	eq("лоти", after.Lots, before.Lots)
	eq("продажі", after.Sales, before.Sales)
	eq("поповнення", after.Deposits, before.Deposits)
	eq("конвертації", after.Conversions, before.Conversions)
	eq("операції фондів", after.FundOps, before.FundOps)
	eq("статуси виплат", after.PaymentStatus, before.PaymentStatus)

	// channels зникає свідомо: список брокерів тепер таблиця.
	wantSettings := map[string]string{}
	for k, v := range before.Settings {
		if k != "channels" {
			wantSettings[k] = v
		}
	}
	eq("налаштування", after.Settings, wantSettings)

	// І окремо — що довідники таки наповнились, а не лишились порожні
	// при «однаковому» вивантаженні (таке дало б збіг на порожній базі).
	brokers, err := st.ListBrokers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	funds, err := st.ListFunds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("перенесено: %d лотів, %d поповнень, %d операцій фондів; довідники: %d брокерів, %d фондів",
		len(after.Lots), len(after.Deposits), len(after.FundOps), len(brokers), len(funds))
	if len(before.FundOps) > 0 && len(funds) == 0 {
		t.Error("операції фондів є, а довідник фондів порожній")
	}
	if len(before.Lots) > 0 && len(brokers) == 0 {
		t.Error("лоти є, а довідник брокерів порожній")
	}
}
