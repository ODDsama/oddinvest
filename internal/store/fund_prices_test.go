package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"

	money "github.com/Rhymond/go-money"
)

// seedFund заводить фонд першою операцією — рівно так, як він і зʼявляється
// в житті: окремого POST у довідника немає.
func seedFund(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	ctx := context.Background()
	if _, err := s.AddFundOp(ctx, domain.FundOp{
		Date: "2026-01-10", Fund: name, Kind: domain.FundBuy,
		Qty: 5, Amount: 500000, Currency: money.UAH,
	}); err != nil {
		t.Fatal(err)
	}
	funds, err := s.ListFunds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range funds {
		if f.Name == name {
			return f.ID
		}
	}
	t.Fatalf("фонд %q не завівся", name)
	return 0
}

// Дві позначки на один день — не історія, а суперечність: лишається одна, і
// виграє остання.
func TestFundPricesUniquePerDayOverwrites(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	id := seedFund(t, s, "МілТех")

	for _, price := range []int64{1_000_000, 1_060_000} {
		if _, err := s.AddFundPricePoints(ctx, id, []domain.FundPrice{
			{Date: "2026-07-10", Price: price},
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListFundPrices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("на один день мала лишитись одна точка, маємо %d", len(got))
	}
	if got[0].Price != 1_060_000 {
		t.Errorf("виграти мала остання ціна, маємо %d", got[0].Price)
	}
	if got[0].FundID != id || got[0].Fund != "МілТех" {
		t.Errorf("точка мала знати свій фонд і за id, і за назвою: %+v", got[0])
	}
}

// Пачка або заходить уся, або не заходить зовсім: половина вклеєної історії
// гірша за жодну, бо на ній порахувалась би дохідність за відрізок, якого
// ніхто не вибирав.
func TestFundPricesBadRowRejectsWholeBatch(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	id := seedFund(t, s, "МілТех")

	_, err := s.AddFundPricePoints(ctx, id, []domain.FundPrice{
		{Date: "2026-07-10", Price: 1_000_000},
		{Date: "10.07.2026", Price: 1_060_000},
	})
	if err == nil {
		t.Fatal("зіпсована дата мала завалити пачку")
	}
	if !strings.Contains(err.Error(), "рядок 2") {
		t.Errorf("помилка мала назвати номер рядка, маємо %q", err)
	}
	got, _ := s.ListFundPrices(ctx) //nolint:errcheck // помилку читання перевіряє сусідній тест
	if len(got) != 0 {
		t.Errorf("не мало залишитись жодної точки, маємо %d", len(got))
	}
}

// Перенесення точки на зайняту дату — зіткнення з наявним записом, а не
// помилка вводу: 409, а не 400.
func TestFundPriceUpdateToBusyDateConflicts(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	id := seedFund(t, s, "МілТех")
	if _, err := s.AddFundPricePoints(ctx, id, []domain.FundPrice{
		{Date: "2026-07-10", Price: 1_000_000},
		{Date: "2026-08-10", Price: 1_060_000},
	}); err != nil {
		t.Fatal(err)
	}
	pts, err := s.ListFundPrices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = s.UpdateFundPricePoint(ctx, domain.FundPrice{
		ID: pts[1].ID, Date: pts[0].Date, Price: 999_000,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("очікували ErrConflict, маємо %v", err)
	}
}

// Позначки тримаються за fund_id, тож виправлення описки в назві їх не
// відчіпляє — саме заради цього 0010 і перебудувала fund_ops.
func TestRenameFundKeepsPrices(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	id := seedFund(t, s, "МілТэх")
	if _, err := s.AddFundPricePoints(ctx, id, []domain.FundPrice{
		{Date: "2026-07-10", Price: 1_060_000},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameFund(ctx, id, Fund{Name: "Inzhur MilTech", Currency: money.UAH}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListFundPrices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Fund != "Inzhur MilTech" || got[0].Price != 1_060_000 {
		t.Fatalf("позначка мала пережити перейменування: %+v", got)
	}
}

// Видалення фонду з позначками відмовляє СВОЇМИ словами. Без цієї
// перевірки воно падало б сирою помилкою FK — тобто казало б те саме,
// тільки незрозуміло. Каскаду тут немає навмисно: фонд можна прибрати з
// довідника й помилково, а разом із ним пішла б історія, якої немає більше
// ніде.
func TestDeleteFundRefusesWithPrices(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	id := seedFund(t, s, "МілТех")
	// Операції прибираємо — щоб спрацювала саме перевірка позначок.
	ops, err := s.ListFundOps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		if err := s.DeleteFundOp(ctx, o.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddFundPricePoints(ctx, id, []domain.FundPrice{
		{Date: "2026-07-10", Price: 1_060_000},
	}); err != nil {
		t.Fatal(err)
	}
	err = s.DeleteFund(ctx, id)
	if err == nil || !strings.Contains(err.Error(), "позначок ціни") {
		t.Fatalf("очікували відмову з поясненням, маємо %v", err)
	}
}

// Бекап возить позначки цілими. Без цього поля відновлення тихо повертало б
// ринкову вартість до собівартості, а дохідність — до нуля.
func TestBackupRoundTripKeepsFundPrices(t *testing.T) {
	ctx := context.Background()
	src := openTest(t)
	id := seedFund(t, src, "Inzhur MilTech")
	if _, err := src.AddFundPricePoints(ctx, id, []domain.FundPrice{
		{Date: "2025-07-01", Price: 850_000},
		{Date: "2026-07-01", Price: 1_060_000},
	}); err != nil {
		t.Fatal(err)
	}

	dump, err := src.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk Backup
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}

	dst := openTest(t)
	if err := dst.ImportAll(ctx, &onDisk); err != nil {
		t.Fatalf("імпорт: %v", err)
	}
	got, err := dst.ListFundPrices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("після відновлення %d позначок замість 2", len(got))
	}
	if got[0].Date != "2025-07-01" || got[0].Price != 850_000 ||
		got[1].Date != "2026-07-01" || got[1].Price != 1_060_000 {
		t.Errorf("позначки приїхали не ті: %+v", got)
	}
	if got[0].Fund != "Inzhur MilTech" {
		t.Errorf("позначка мала привʼязатись до свого фонду, маємо %q", got[0].Fund)
	}
}

// Відновлення бази, що вже має позначки, НЕ падає на FK. Ловить забуту
// fund_prices у переліку DELETE FROM: без неї DELETE FROM funds упирається
// у зовнішній ключ, і відновлення відмовляє цілком.
func TestImportOverExistingFundPrices(t *testing.T) {
	ctx := context.Background()
	src := openTest(t)
	seedFund(t, src, "Inzhur REIT")
	dump, err := src.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}

	dst := openTest(t)
	id := seedFund(t, dst, "МілТех")
	if _, err := dst.AddFundPricePoints(ctx, id, []domain.FundPrice{
		{Date: "2026-07-10", Price: 1_060_000},
	}); err != nil {
		t.Fatal(err)
	}
	if err := dst.ImportAll(ctx, dump); err != nil {
		t.Fatalf("відновлення поверх наявних позначок мало пройти: %v", err)
	}
	got, err := dst.ListFundPrices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("відновлення ЗАМІЩУЄ, тож старих позначок мало не лишитись: %+v", got)
	}
}

// Вид фонду перевіряється в СХОВИЩІ, а не лише в обробнику, бо писати в
// довідник уміє ще й відновлення бекапу.
func TestFundKindValidated(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	id := seedFund(t, s, "Inzhur REIT")
	base := Fund{Name: "Inzhur REIT", Currency: money.UAH, PayoutDay: 10}

	ok := base
	ok.Kind = FundReinvesting
	if err := s.RenameFund(ctx, id, ok); err != nil {
		t.Fatalf("фонд, що докуповує сертифікати, мав прийнятись: %v", err)
	}

	// Без дня виплати такий фонд мовчки не породив би жодного потоку —
	// налаштування виглядало б увімкненим, не роблячи нічого.
	noDay := base
	noDay.Kind, noDay.PayoutDay = FundReinvesting, 0
	if err := s.RenameFund(ctx, id, noDay); err == nil {
		t.Error("реінвест без дня виплати мав бути відхилений")
	}

	junk := base
	junk.Kind = "щось"
	if err := s.RenameFund(ctx, id, junk); err == nil {
		t.Error("сміттєвий вид мав бути відхилений")
	}
}
