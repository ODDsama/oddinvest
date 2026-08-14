package store

import (
	"context"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
)

func auction(date, isin, num, cur, bucket string, incomeBP, days int64) nbu.Auction {
	return nbu.Auction{
		Date: domain.Date(date), ISIN: isin, Num: num, Currency: cur,
		Bucket: bucket, IncomeBP: incomeBP, DaysToRepay: days,
	}
}

// Головний інваріант цієї таблиці: історія аукціонів переживає щоденне
// перезаливання довідника. ReplaceDirectory робить `DELETE FROM bonds`, і
// якби рівні розміщень жили на `bonds` (або мали на них FK), вони
// зникали б щоранку — причому НЕПОМІТНО: на екрані просто не з'являлась
// би крива.
func TestAuctionsSurviveDirectoryReplace(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SaveAuctions(ctx, []nbu.Auction{
		auction("2026-08-11", "UA4000239016", "91", money.UAH, "1y", 1519, 343),
	}); err != nil {
		t.Fatal(err)
	}
	// Довідник наливається заново — і навіть без цього паперу.
	if err := s.ReplaceDirectory(ctx, []nbu.Security{{
		Bond: domain.Bond{ISIN: "UA4000227748", Nominal: money.New(100000, money.UAH),
			RateBP: 1655, Maturity: "2027-03-17"},
	}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := s.LastAuctionByISIN(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["UA4000239016"].IncomeBP != 1519 {
		t.Fatalf("аукціон не пережив перезаливання довідника: %+v", got)
	}
}

// Повторний бекфіл нічого не дублює й нічого не коштує: ключ (день,
// папір, номер) робить запис ідемпотентним. Без цього кожен прогін
// множив би рядки, а «остання дохідність» ставала б лотереєю.
func TestSaveAuctionsIsIdempotent(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	rows := []nbu.Auction{
		auction("2026-08-11", "UA4000239016", "91", money.UAH, "1y", 1519, 343),
		auction("2026-08-11", "UA4000239040", "92", money.UAH, "1.5y", 1565, 623),
	}
	for i := 0; i < 3; i++ {
		if err := s.SaveAuctions(ctx, rows); err != nil {
			t.Fatal(err)
		}
	}
	days, err := s.CountAuctionDays(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if days != 1 {
		t.Errorf("днів аукціонів = %d, хочемо 1", days)
	}
	// Переспів того самого розміщення з виправленим рівнем має замінити
	// рядок, а не додати другий: НБУ уточнює числа заднім числом.
	rows[0].IncomeBP = 1521
	if err := s.SaveAuctions(ctx, rows[:1]); err != nil {
		t.Fatal(err)
	}
	last, err := s.LastAuctionByISIN(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if last["UA4000239016"].IncomeBP != 1521 {
		t.Errorf("уточнений рівень не замінив старий: %+v", last["UA4000239016"])
	}
}

func TestLastAuctionByISINTakesNewest(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SaveAuctions(ctx, []nbu.Auction{
		auction("2026-06-02", "UA4000239016", "70", money.UAH, "1y", 1480, 400),
		auction("2026-08-11", "UA4000239016", "91", money.UAH, "1y", 1519, 343),
		auction("2026-07-07", "UA4000239016", "80", money.UAH, "1y", 1500, 370),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.LastAuctionByISIN(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := got["UA4000239016"]
	// Разом із датою мусять приїхати поля САМЕ того рядка, а не будь-якого
	// з групи: на цьому й тримається «востаннє розміщували під стільки».
	if p.Date != domain.Date("2026-08-11") || p.IncomeBP != 1519 || p.Days != 343 {
		t.Errorf("останнє розміщення: %+v", p)
	}
	newest, err := s.NewestAuctionDate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if newest != domain.Date("2026-08-11") {
		t.Errorf("найсвіжіший день = %q", newest)
	}
}

// TestAuctionPointCarriesDemandAndVolume — з рядка приїжджає ВЕСЬ рядок.
//
// Міграція 0024 завела дванадцять колонок, а читалось шість: попит
// (bid-to-cover), смуга прийнятих заявок, обсяг розміщення, номер аукціону
// й дата погашення писались щодня й не читались назад ніде. Помилка була
// не в записі й не в схемі, а рівно в переліку колонок SELECT — місці,
// яке нічим не падає: Scan звіряє КІЛЬКІСТЬ, тож поки обидва переліки
// однаково короткі, все «працює».
func TestAuctionPointCarriesDemandAndVolume(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SaveAuctions(ctx, []nbu.Auction{{
		Date: "2026-08-11", ISIN: "UA4000239016", Num: "91", Currency: money.UAH,
		Bucket: "1y", DaysToRepay: 343, RepayDate: "2027-07-20", IncomeBP: 1519,
		MinBP: 1490, MaxBP: 1540, BTCx100: 235, SoldMinor: 5_200_000_000_00,
	}}); err != nil {
		t.Fatal(err)
	}
	check := func(what string, p AuctionPoint) {
		t.Helper()
		if p.MinBP != 1490 || p.MaxBP != 1540 {
			t.Errorf("%s: смуга прийнятих заявок не доїхала: %+v", what, p)
		}
		if p.BTCx100 != 235 {
			t.Errorf("%s: попит не доїхав: btc=%d", what, p.BTCx100)
		}
		if p.SoldMinor != 5_200_000_000_00 {
			t.Errorf("%s: обсяг не доїхав: sold=%d", what, p.SoldMinor)
		}
		if p.Num != "91" || p.RepayDate != domain.Date("2027-07-20") {
			t.Errorf("%s: номер/дата погашення не доїхали: %+v", what, p)
		}
	}
	// Обидва шляхи вичитування спільні (auctionCols), і саме тому
	// перевіряються обидва: розійшовшись, вони розійшлись би мовчки.
	byISIN, err := s.LastAuctionByISIN(ctx)
	if err != nil {
		t.Fatal(err)
	}
	check("LastAuctionByISIN", byISIN["UA4000239016"])

	byBucket, err := s.AuctionLatestByBucket(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(byBucket) != 1 {
		t.Fatalf("хочемо один строк, маємо %d", len(byBucket))
	}
	check("AuctionLatestByBucket", byBucket[0])
}

// Порожня база не помилка, а звичайний стан свіжої інсталяції: доти,
// доки бекфіл не відпрацював, кривої просто немає.
func TestNewestAuctionDateOnEmpty(t *testing.T) {
	s := openTest(t)
	got, err := s.NewestAuctionDate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("на порожній базі хочемо порожню дату, маємо %q", got)
	}
}

// Крива читається зліва направо за СТРОКОМ, а назви строків сортуються за
// абеткою неправильно: «1.5y» < «1y» < «2y». Тому впорядкування йде за
// днями до погашення.
func TestAuctionLatestByBucketOrdersByTerm(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SaveAuctions(ctx, []nbu.Auction{
		auction("2026-08-11", "UA4000239107", "93", money.UAH, "2y", 1610, 910),
		auction("2026-08-11", "UA4000239040", "92", money.UAH, "1.5y", 1565, 623),
		auction("2026-08-11", "UA4000239016", "91", money.UAH, "1y", 1519, 343),
		auction("2026-07-07", "UA4000239016", "80", money.UAH, "1y", 1500, 370),
		auction("2026-08-11", "UA4000239065", "94", money.EUR, "1.5y", 320, 548),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.AuctionLatestByBucket(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("пар (валюта, строк) хочемо 4, маємо %d: %+v", len(got), got)
	}
	want := []struct {
		cur, bucket string
		income      int64
	}{
		{money.EUR, "1.5y", 320},
		{money.UAH, "1y", 1519}, // саме свіжий рівень, не липневий
		{money.UAH, "1.5y", 1565},
		{money.UAH, "2y", 1610},
	}
	for i, w := range want {
		if got[i].Currency != w.cur || got[i].Bucket != w.bucket || got[i].IncomeBP != w.income {
			t.Errorf("рядок %d = %+v, хочемо %s/%s/%d", i, got[i], w.cur, w.bucket, w.income)
		}
	}
}
