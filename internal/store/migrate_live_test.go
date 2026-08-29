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

// TestLiveBackupRoundTrip — справжній бекап відновлюється в НОВУ схему й
// вивантажується назад без утрат.
//
// Тест поруч (вище) перевіряє одну міграцію на старих даних. Цей — інше
// питання й ширше: чи переживе РЕАЛЬНИЙ портфель повний цикл
// «відновити → вивантажити» на схемі з усіма міграціями. Саме цим циклом
// людина користується, коли переносить базу або рятує її після
// пошкодження, і саме в ньому втрата виглядає найправдоподібніше:
// повертається щось, числа схожі, а рядка бракує.
//
// Ловить, зокрема, обидві правки 0043/0044 у бекапі: plan_buys тепер
// зберігає broker_id, а не назву (у дамп і з дампа йде назва), а статус
// виплати зводиться до 'received' на вході.
//
//	ODDINVEST_LIVE_BACKUP=/шлях/backup.json go test ./internal/store -run Live -v
func TestLiveBackupRoundTrip(t *testing.T) {
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

	s, err := Open(filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatalf("міграції не пройшли на новій базі: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.ImportAll(ctx, &before); err != nil {
		t.Fatalf("справжній бекап не відновлюється: %v", err)
	}
	after, err := s.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// exported_at ставить гендлер, а не ExportAll — рівняємо решту.
	after.ExportedAt = before.ExportedAt
	if !reflect.DeepEqual(&before, after) {
		reportBackupDiff(t, &before, after)
	}

	// Цілісність після відновлення — окремо від рівності: FK міг лишитись
	// висіти, а порівняння структур цього не бачить.
	var orphan int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&orphan); err != nil {
		t.Fatal(err)
	}
	if orphan != 0 {
		t.Errorf("після відновлення осиротілих рядків: %d", orphan)
	}
	var integrity string
	if err := s.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatal(err)
	}
	if integrity != "ok" {
		t.Errorf("integrity_check: %s", integrity)
	}
}

// reportBackupDiff називає ПОЛЕ, яке розійшлось, а не вивалює дві
// структури: у бекапі два десятки зрізів, і «not deep equal» на них не
// каже нічого. Значень не друкуємо — це справжній портфель.
func reportBackupDiff(t *testing.T, before, after *Backup) {
	t.Helper()
	bv, av := reflect.ValueOf(before).Elem(), reflect.ValueOf(after).Elem()
	for i := 0; i < bv.NumField(); i++ {
		f := bv.Type().Field(i)
		b, a := bv.Field(i), av.Field(i)
		if reflect.DeepEqual(b.Interface(), a.Interface()) {
			continue
		}
		switch b.Kind() {
		case reflect.Slice, reflect.Map:
			t.Errorf("%s: було %d, стало %d", f.Name, b.Len(), a.Len())
		default:
			t.Errorf("%s: розійшлось", f.Name)
		}
	}
}
