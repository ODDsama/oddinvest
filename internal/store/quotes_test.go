package store

import (
	"context"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

func q(isin, source string, date domain.Date, minor int64, origin string) Quote {
	return Quote{ISIN: isin, Source: source, Date: date, PriceMinor: minor,
		Currency: money.UAH, Origin: origin}
}

func TestSaveQuotesIsIdempotent(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	one := q("UA4000239107", "Inzhur", "2026-09-06", 101365, QuoteOriginFinomo)
	if err := s.SaveQuotes(ctx, []Quote{one}); err != nil {
		t.Fatal(err)
	}
	one.PriceMinor = 101400
	if err := s.SaveQuotes(ctx, []Quote{one}); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestQuotes(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("рядків %d, чекали 1: %+v", len(got), got)
	}
	if got[0].PriceMinor != 101400 {
		t.Errorf("ціна %d, чекали переписану 101400", got[0].PriceMinor)
	}
}

// Ключ несе дату, тож новий день додає рядок, а не переписує старий.
// LatestQuotes при цьому мусить віддати ЛИШЕ найсвіжіший по кожному
// продавцю: інакше вчорашня ціна змагалась би з сьогоднішньою за право
// бути найдешевшою й регулярно вигравала б.
func TestLatestQuotesTakesNewestPerSource(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SaveQuotes(ctx, []Quote{
		q("UA4000239107", "Inzhur", "2026-09-05", 100900, QuoteOriginFinomo),
		q("UA4000239107", "Inzhur", "2026-09-06", 101365, QuoteOriginFinomo),
		q("UA4000239107", "Privat24", "2026-09-06", 101953, QuoteOriginFinomo),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestQuotes(ctx, []string{"ua4000239107"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("рядків %d, чекали 2: %+v", len(got), got)
	}
	// Порядок: найдешевший першим — на ньому тримається «у кого дешевше».
	if got[0].Source != "Inzhur" || got[0].PriceMinor != 101365 {
		t.Errorf("перший рядок %+v", got[0])
	}
	if got[1].Source != "Privat24" {
		t.Errorf("другий рядок %+v", got[1])
	}
}

func TestLatestQuotesFiltersByISIN(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SaveQuotes(ctx, []Quote{
		q("UA4000239107", "Inzhur", "2026-09-06", 101365, QuoteOriginFinomo),
		q("UA4000239081", "Inzhur", "2026-09-06", 100798, QuoteOriginFinomo),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestQuotes(ctx, []string{"UA4000239081"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ISIN != "UA4000239081" {
		t.Fatalf("фільтр не спрацював: %+v", got)
	}
}

// Видаляти можна ЛИШЕ ручну ціну: рядок обходу людина не заводила, і
// стирати його з екрана нема сенсу — наступне натискання поверне його.
func TestDeleteManualQuoteOnlyTouchesManual(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SaveQuotes(ctx, []Quote{
		q("UA4000239107", "mono", "2026-09-06", 101500, QuoteOriginManual),
		q("UA4000239107", "Inzhur", "2026-09-06", 101365, QuoteOriginFinomo),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteManualQuote(ctx, "UA4000239107", "Inzhur", "2026-09-06"); err == nil {
		t.Error("рядок обходу не мусив видалятись")
	}
	if err := s.DeleteManualQuote(ctx, "ua4000239107", "mono", "2026-09-06"); err != nil {
		t.Fatalf("ручна ціна мусила видалитись: %v", err)
	}
	got, err := s.LatestQuotes(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Source != "Inzhur" {
		t.Errorf("лишилось %+v", got)
	}
}

func TestSaveQuotesRejectsNonsense(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	bad := []Quote{
		q("", "Inzhur", "2026-09-06", 101365, QuoteOriginFinomo),
		q("UA4000239107", "", "2026-09-06", 101365, QuoteOriginFinomo),
		q("UA4000239107", "Inzhur", "2026-09-06", 0, QuoteOriginFinomo),
		q("UA4000239107", "Inzhur", "не дата", 101365, QuoteOriginFinomo),
	}
	for i, v := range bad {
		if err := s.SaveQuotes(ctx, []Quote{v}); err == nil {
			t.Errorf("рядок %d мусив бути відхилений: %+v", i, v)
		}
	}
}

// Ціни спільні для портфелів (0059: у дружини той самий папір у того
// самого брокера коштує стільки ж). Відсутність колонки portfolio_id це
// доводить лише в схемі; що обидва портфелі БАЧАТЬ одні рядки — не
// доводить ніщо, крім цього тесту.
func TestQuotesAreSharedBetweenPortfolios(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if err := s.SaveQuotes(ctx, []Quote{
		q("UA4000239107", "Inzhur", "2026-09-06", 101365, QuoteOriginFinomo),
	}); err != nil {
		t.Fatal(err)
	}
	pid, err := s.AddPortfolio(ctx, "wife", "дружина")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.For(pid).LatestQuotes(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("сателіт бачить %d рядків, чекали 1", len(got))
	}
}

func TestBrokerQuoteSourceRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	id, err := s.AddBroker(ctx, "mono")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetBrokerQuoteSource(ctx, id, "mono"); err != nil {
		t.Fatal(err)
	}
	bs, err := s.ListBrokers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 1 || bs[0].QuoteSource != "mono" {
		t.Fatalf("зіставлення не збереглось: %+v", bs)
	}
	// Порожній рядок знімає зіставлення — це стан, а не видалення брокера.
	if err := s.SetBrokerQuoteSource(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	if bs, _ = s.ListBrokers(ctx); len(bs) != 1 || bs[0].QuoteSource != "" {
		t.Fatalf("зіставлення не знялось: %+v", bs)
	}
}
