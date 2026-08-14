package store

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLotsRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, err := s.AddLot(ctx, domain.Lot{
		ISIN: "UA4000227748", Qty: 5,
		PricePerBond: money.New(99500, money.UAH),
		BuyDate:      "2026-07-01", Channel: "Дія",
	})
	if err != nil {
		t.Fatal(err)
	}
	lots, err := s.ListLots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(lots) != 1 || lots[0].ID != id || lots[0].PricePerBond.Amount() != 99500 ||
		lots[0].PricePerBond.Currency().Code != "UAH" || lots[0].BuyDate != "2026-07-01" {
		t.Fatalf("round-trip: %+v", lots)
	}
}

func TestSaleValidation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, _ := s.AddLot(ctx, domain.Lot{ISIN: "UA1", Qty: 5,
		PricePerBond: money.New(99500, money.UAH), BuyDate: "2026-07-01"})

	// продаж більше залишку
	_, err := s.AddSale(ctx, domain.Sale{LotID: id, SaleDate: "2026-08-01", Qty: 6,
		CleanPerBond: money.New(100000, money.UAH)})
	if err == nil {
		t.Fatal("очікували помилку oversell")
	}
	// валютна невідповідність
	_, err = s.AddSale(ctx, domain.Sale{LotID: id, SaleDate: "2026-08-01", Qty: 1,
		CleanPerBond: money.New(100000, money.USD)})
	if err == nil {
		t.Fatal("очікували помилку валют")
	}
	// нормальний продаж, двічі по 3 — друга має впасти
	if _, err = s.AddSale(ctx, domain.Sale{LotID: id, SaleDate: "2026-08-01", Qty: 3,
		CleanPerBond: money.New(100000, money.UAH), Accrued: money.New(500, money.UAH)}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddSale(ctx, domain.Sale{LotID: id, SaleDate: "2026-09-01", Qty: 3,
		CleanPerBond: money.New(100000, money.UAH)}); err == nil {
		t.Fatal("сумарний oversell має падати")
	}
}

func TestDirectoryReplaceAndSearch(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	secs := []nbu.Security{
		{Bond: domain.Bond{ISIN: "UA1", Nominal: money.New(100000, money.UAH),
			RateBP: 1655, Maturity: "2027-03-17", Descr: "гривневі"},
			Payments: []domain.Payment{{ISIN: "UA1", PayDate: "2026-09-16",
				Type: domain.PayCoupon, PerBond: money.New(8275, money.UAH)}}},
		{Bond: domain.Bond{ISIN: "US1", Nominal: money.New(100000, money.USD),
			RateBP: 324, Maturity: "2027-09-17", Descr: "доларові"}},
	}
	if err := s.ReplaceDirectory(ctx, secs, time.Now()); err != nil {
		t.Fatal(err)
	}
	found, err := s.SearchBonds(ctx, "", "USD", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ISIN != "US1" {
		t.Fatalf("пошук по валюті: %+v", found)
	}
	pays, err := s.PaymentsFor(ctx, []string{"UA1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pays) != 1 || pays[0].PerBond.Amount() != 8275 {
		t.Fatalf("payments: %+v", pays)
	}
	// повторний replace не дублює
	if err := s.ReplaceDirectory(ctx, secs, time.Now()); err != nil {
		t.Fatal(err)
	}
	all, _ := s.SearchBonds(ctx, "", "", "", "", 10)
	if len(all) != 2 {
		t.Fatalf("після повторного replace: %d паперів", len(all))
	}
}

func TestSettingsRatesSnapshotsStatuses(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SetSetting(ctx, "monthly_target_uah", "500000"); err != nil {
		t.Fatal(err)
	}
	v, _ := s.GetSetting(ctx, "monthly_target_uah")
	if v != "500000" {
		t.Fatalf("setting: %s", v)
	}
	if err := s.SaveRate(ctx, "USD", 441234, "2026-07-15"); err != nil {
		t.Fatal(err)
	}
	r, _ := s.LatestRate(ctx, "USD")
	if r != 441234 {
		t.Fatalf("rate: %d", r)
	}
	if err := s.SaveSnapshot(ctx, Snapshot{Date: "2026-07-15", InvestedUAH: 100,
		NominalUAHEq: 200, USDShareBP: 5000, MonthTargetUAH: 500000,
		AccountUAH: 700, FundsUAH: 900}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSnapshot(ctx, Snapshot{Date: "2026-07-15", InvestedUAH: 150,
		NominalUAHEq: 250, USDShareBP: 5100, UninvestedUAH: 10, MonthTargetUAH: 600000,
		AccountUAH: 800, FundsUAH: 950}); err != nil {
		t.Fatal(err) // upsert того ж дня
	}
	if snaps, serr := s.ListSnapshots(ctx, "", ""); serr != nil || len(snaps) != 1 ||
		snaps[0].FundsUAH != 950 {
		t.Fatalf("знімок після upsert: %+v (%v)", snaps, serr)
	}
	if err := s.SetPaymentStatus(ctx, "UA1", "2026-09-16", "received"); err != nil {
		t.Fatal(err)
	}
	st, _ := s.PaymentStatuses(ctx)
	if st["UA1|2026-09-16"] != "received" {
		t.Fatalf("status: %+v", st)
	}
	if err := s.SetPaymentStatus(ctx, "UA1", "2026-09-16", "spent"); err == nil {
		t.Fatal("невалідний статус має падати")
	}
	// Скасування прибирає рядок; повторне — не помилка (нічого знімати).
	if err := s.ClearPaymentStatus(ctx, "UA1", "2026-09-16"); err != nil {
		t.Fatal(err)
	}
	st, _ = s.PaymentStatuses(ctx)
	if _, ok := st["UA1|2026-09-16"]; ok {
		t.Fatalf("після скасування статусу не має бути: %+v", st)
	}
	if err := s.ClearPaymentStatus(ctx, "UA1", "2026-09-16"); err != nil {
		t.Fatal("повторне скасування має бути безшумним")
	}
}

// Міграція 0015 виводить прапорець із ДОКАЗУ: вклад, який уже
// поповнювали, поповнюваний за визначенням — інакше після оновлення
// користувач мусив би вручну відновлювати те, що видно з даних.
// Міграція 0017 зводить «перевкладено» до «отримано». Обидва однаково
// означали «гроші надійшли» — різниця була лише в дисциплінарній
// позначці, яку прибрано. Виплата не має ані зникнути, ані втратити
// позначку: інакше після оновлення вона перестала б бути зарахованою на
// рахунок, і баланс просів би рівно на неї.
func TestReinvestedStatusFoldedIntoReceived(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Стан «до міграції»: пишемо старий статус повз SetPaymentStatus,
	// який його вже не приймає.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO payment_status(isin, pay_date, status, marked_at)
		VALUES('UA4000227748','2026-07-20','reinvested','2026-07-20T10:00:00Z'),
		      ('UA4000227748','2026-09-16','received','2026-09-16T10:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE payment_status SET status = 'received' WHERE status = 'reinvested'`); err != nil {
		t.Fatal(err)
	}

	got, err := s.PaymentStatuses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("жодна виплата не мала зникнути, маємо %d: %v", len(got), got)
	}
	for key, st := range got {
		if st != "received" {
			t.Errorf("%s: статус %q, очікували received", key, st)
		}
	}

	// І новий статус більше не приймається — щоб він не з'явився знову.
	if err := s.SetPaymentStatus(ctx, "UA4000227748", "2026-07-20", "reinvested"); err == nil {
		t.Error("«перевкладено» мало бути відхилено")
	}
	if err := s.SetPaymentStatus(ctx, "UA4000227748", "2026-07-20", "received"); err != nil {
		t.Errorf("«отримано» мало пройти: %v", err)
	}
}

func TestReplenishableBackfilledFromExistingTopups(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	mk := func(bank string) int64 {
		id, err := s.AddTermDeposit(ctx, domain.Deposit{
			Bank: bank, Currency: "UAH", Principal: 10000000, RateBP: 1600,
			OpenDate: "2026-01-15", MaturityDate: "2027-01-15",
			Payout: domain.PayoutEnd, TaxBP: 1950,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	withTopup := mk("ПУМБ")
	plain := mk("Приват")
	if _, err := s.AddDepositTopup(ctx, domain.DepositTopup{
		DepositID: withTopup, Date: "2026-02-15", Amount: 10000000,
	}); err != nil {
		t.Fatal(err)
	}

	// Проганяємо backfill так, як це зробила б міграція на живій БД:
	// колонка вже є (0015 застосувалась при Open), тож імітуємо стан
	// «до неї» — скидаємо прапорець і повторюємо UPDATE з міграції.
	if _, err := s.db.ExecContext(ctx, `UPDATE term_deposits SET replenishable=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE term_deposits SET replenishable = 1
		WHERE id IN (SELECT DISTINCT deposit_id FROM deposit_topups)`); err != nil {
		t.Fatal(err)
	}

	deps, err := s.ListTermDeposits(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]bool{}
	for _, d := range deps {
		got[d.ID] = d.Replenishable
	}
	if !got[withTopup] {
		t.Error("вклад із поповненням мав стати поповнюваним")
	}
	if got[plain] {
		t.Error("вклад без поповнень мав лишитись непоповнюваним")
	}
}

// Статус виплати лишається рівно один.
//
// На цьому тримається domain.Arrived: доти три копії предиката
// перевіряли різне («received»/«reinvested» проти будь-якого непорожнього),
// і розійтись вони не могли саме тому, що інших значень у базі не буває —
// CHECK у міграції 0001, UPDATE у 0017 і ця перевірка. Щойно з'явиться
// третій статус, злиття доведеться переглянути свідомо, а не виявити
// потім за розбіжністю звітів.
func TestPaymentStatusAcceptsOnlyReceived(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SetPaymentStatus(ctx, "UA1", "2026-07-15", "received"); err != nil {
		t.Fatalf("«received» мав прийнятись: %v", err)
	}
	for _, bad := range []string{"reinvested", "pending", "", "RECEIVED"} {
		if err := s.SetPaymentStatus(ctx, "UA1", "2026-07-15", bad); err == nil {
			t.Errorf("статус %q мав бути відхилений", bad)
		}
	}
}

// Знімок мусить пережити повний цикл БЕЗ втрат у жодній колонці.
//
// Колонку доводилось вписувати у вісім місць, і десять із них ламались
// тихо. Найтихіше — перелік ON CONFLICT: пропущена там колонка
// вставляється, але при повторному записі того самого дня не оновлюється
// НІКОЛИ. Далі позиційні Scan і VALUES(?,…), де сусідні int64 можна
// поміняти місцями без наслідків для компілятора.
//
// Тому кожна колонка отримує СВОЄ, помітно різне значення: збіг двох
// сусідніх приховав би саме ту помилку, заради якої тест і написаний.
func TestSnapshotSurvivesEveryColumn(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	cols := SnapshotColumns()
	var sn Snapshot
	sn.Date = "2026-07-15"
	for i, c := range cols {
		// 1001, 2002, 3003… — і різні, і не схожі на індекс.
		setSnapshotValue(t, &sn, c, int64((i+1)*1001))
	}

	if err := s.SaveSnapshot(ctx, sn); err != nil {
		t.Fatal(err)
	}
	check := func(stage string, got []Snapshot) {
		t.Helper()
		if len(got) != 1 {
			t.Fatalf("%s: очікували один знімок, маємо %d", stage, len(got))
		}
		for i, c := range cols {
			want := int64((i + 1) * 1001)
			if v := SnapshotValue(&got[0], c); v != want {
				t.Errorf("%s: колонка %s = %d, чекали %d", stage, c, v, want)
			}
		}
	}
	got, err := s.ListSnapshots(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	check("після запису", got)

	// Повторний запис того самого дня — саме тут мовчить забутий
	// ON CONFLICT. Значення зсуваємо, щоб «не оновилось» було видно.
	for i, c := range cols {
		setSnapshotValue(t, &sn, c, int64((i+1)*1001)+7)
	}
	if err := s.SaveSnapshot(ctx, sn); err != nil {
		t.Fatal(err)
	}
	got, err = s.ListSnapshots(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("upsert мав лишити один рядок, маємо %d", len(got))
	}
	for i, c := range cols {
		want := int64((i+1)*1001) + 7
		if v := SnapshotValue(&got[0], c); v != want {
			t.Errorf("повторний запис: колонка %s лишилась %d замість %d — "+
				"її немає в ON CONFLICT DO UPDATE SET", c, v, want)
		}
	}

	// І бекап: забута там колонка означає, що відновлення її мовчки
	// обнуляє — це вже двічі ставалось.
	dump, err := s.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ImportAll(ctx, dump); err != nil {
		t.Fatal(err)
	}
	got, err = s.ListSnapshots(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("після відновлення мав лишитись один знімок, маємо %d", len(got))
	}
	for i, c := range cols {
		want := int64((i+1)*1001) + 7
		if v := SnapshotValue(&got[0], c); v != want {
			t.Errorf("бекап загубив колонку %s: %d замість %d", c, v, want)
		}
	}
}

// setSnapshotValue — запис у поле за іменем колонки. Тестовий двійник
// SnapshotValue; окремо, бо в проді писати знімок по імені нікому не треба.
func setSnapshotValue(t *testing.T, sn *Snapshot, col string, v int64) {
	t.Helper()
	for _, c := range snapshotCols {
		if c.Name == col {
			*c.Ptr(sn) = v
			return
		}
	}
	t.Fatalf("невідома колонка %q", col)
}

// Реєстр колонок мусить показувати на поле з тим самим іменем.
//
// Круговий тест вище цього не побачить: якщо переставити два вказівники
// місцями, запис і читання підуть тим самим переплутаним шляхом і
// зійдуться. А в БАЗІ при цьому лежатиме колонка funds_uah зі значенням
// вкладів — і саме так воно й поїде в /api/snapshots під чужим ключем.
//
// Тому звіряємо реєстр зі структурою напряму: json-тег поля мусить
// збігатися з іменем колонки, на яке вказує акцесор.
func TestSnapshotRegistryPointsAtMatchingField(t *testing.T) {
	typ := reflect.TypeOf(Snapshot{})
	byTag := map[string]int{}
	for i := 0; i < typ.NumField(); i++ {
		tag := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if tag != "" && tag != "-" {
			byTag[tag] = i
		}
	}
	for _, c := range snapshotCols {
		idx, ok := byTag[c.Name]
		if !ok {
			t.Errorf("колонка %q не має поля з таким json-тегом", c.Name)
			continue
		}
		var sn Snapshot
		const sentinel = 987654
		*c.Ptr(&sn) = sentinel
		got := reflect.ValueOf(sn).Field(idx).Int()
		if got != sentinel {
			t.Errorf("колонка %q пише не у своє поле: %s лишилось %d",
				c.Name, typ.Field(idx).Name, got)
		}
	}
	if len(snapshotCols)+1 != len(byTag) {
		t.Errorf("колонок %d + date, а полів зі json-тегом %d — щось описане лише з одного боку",
			len(snapshotCols), len(byTag))
	}
}

// TestRatePointOnOrBeforeCarriesItsDate — курс приходить разом із датою,
// з якої його взято.
//
// Сама дата важить не менше за число. Історія накопичена ПОМІСЯЧНО за
// десять років назад і поденно лише вперед, тож для події 2019 року
// найближча попередня точка може відставати на тижні. Без дати це
// відставання невидиме, і оцінка виглядає як факт; із датою викликач
// може його виміряти й сказати вголос (див. asOfRates у internal/api).
func TestRatePointOnOrBeforeCarriesItsDate(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SaveRate(ctx, money.USD, 27_0000, "2022-02-01"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRate(ctx, money.USD, 44_0000, "2026-07-15"); err != nil {
		t.Fatal(err)
	}

	// Подія між точками бере ПОПЕРЕДНЮ: наступна оцінювала б минуле
	// курсом, якого тоді ще не існувало.
	p, err := s.RatePointOnOrBefore(ctx, money.USD, "2022-03-20")
	if err != nil {
		t.Fatal(err)
	}
	if p.RateE4 != 27_0000 || p.Date != domain.Date("2022-02-01") {
		t.Errorf("між точками: %+v", p)
	}

	// Точний збіг — сам себе.
	if p, _ := s.RatePointOnOrBefore(ctx, money.USD, "2026-07-15"); p.RateE4 != 44_0000 {
		t.Errorf("точний збіг: %+v", p)
	}

	// До початку історії — порожньо, а не мовчазна підміна найближчим
	// НАСТУПНИМ курсом.
	if p, _ := s.RatePointOnOrBefore(ctx, money.USD, "2019-01-01"); p.RateE4 != 0 || p.Date != "" {
		t.Errorf("до початку історії: %+v", p)
	}

	// Стара обгортка лишається сумісною: вона тепер той самий запит.
	if r, _ := s.RateOnOrBefore(ctx, money.USD, "2022-03-20"); r != 27_0000 {
		t.Errorf("RateOnOrBefore = %d", r)
	}
}
