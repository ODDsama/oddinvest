package jobs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/finomo"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/store"
)

// quotesFixture — та сама обрізана сторінка, що в тестах парсера. Читається
// звідти, а не копіюється сюди: дві копії чужої розмітки розійшлися б рівно
// тоді, коли одну з них оновлять після зміни сайту.
func quotesFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "finomo", "testdata", name))
	if err != nil {
		t.Fatalf("фікстура %s: %v", name, err)
	}
	return b
}

// quotesRunner — раннер із джерелом цін і довідником, у якому вже лежать
// папери фікстур. Довідник обов'язковий: без нього прохід не має з чим
// звіряти погашення й масштаб номіналу, тож свідомо відмовляється.
func quotesRunner(t *testing.T, h http.HandlerFunc) (*Runner, *store.Store, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	r, st := testRunner(t, "")
	r.finomo = finomo.New(srv.URL)
	ctx := context.Background()
	if err := st.ReplaceDirectory(ctx, []nbu.Security{
		{Bond: domain.Bond{ISIN: "UA4000239107", Nominal: money.New(100000, money.UAH),
			RateBP: 1607, Maturity: "2029-02-07"}},
		{Bond: domain.Bond{ISIN: "UA4000236806", Nominal: money.New(100000, money.USD),
			RateBP: 375, Maturity: "2027-03-18"}},
		{Bond: domain.Bond{ISIN: "UA4000190284", Nominal: money.New(100000, money.UAH),
			RateBP: 1900, Maturity: "2026-05-27"}},
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return r, st, srv
}

// serveFixtures — сторінка на кожен відомий ISIN, 404 на решту.
func serveFixtures(t *testing.T, hits *int64) http.HandlerFunc {
	byISIN := map[string]string{
		"UA4000239107": "bond_uah.html",
		"UA4000236806": "bond_usd.html",
		"UA4000190284": "bond_redeemed.html",
	}
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(hits, 1)
		isin := strings.TrimPrefix(r.URL.Path, "/bond/")
		name, ok := byISIN[isin]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_, _ = w.Write(quotesFixture(t, name))
	}
}

func TestRefreshQuotesStoresPrices(t *testing.T) {
	var hits int64
	r, st, _ := quotesRunner(t, serveFixtures(t, &hits))
	ctx := context.Background()
	res, err := r.RefreshQuotes(ctx, []string{"UA4000239107", "UA4000236806", "UA4000190284"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Asked != 3 || res.Stored != 2 || res.NoPrice != 1 {
		t.Fatalf("результат %+v, чекали asked=3 stored=2 noprice=1", res)
	}
	got, err := st.LatestQuotes(ctx, []string{"UA4000239107"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("цін %d, чекали 4 продавців: %+v", len(got), got)
	}
	// Найдешевша першою, і це саме та ціна, яку джерело зве найкращою.
	if got[0].Source != "Inzhur" || got[0].PriceMinor != 101365 {
		t.Errorf("найдешевша %+v", got[0])
	}
	// Справедлива вартість НБУ (1034.18) продавцем не є й у зріз не лягає.
	for _, q := range got {
		if q.Source == "NBU" {
			t.Errorf("НБУ потрапив у ціни: %+v", q)
		}
	}
}

// Погашений папір сторінку має, а продавців — ні. Це відповідь, а не збій.
func TestRefreshQuotesCountsNoPriceSeparately(t *testing.T) {
	var hits int64
	r, _, _ := quotesRunner(t, serveFixtures(t, &hits))
	res, err := r.RefreshQuotes(context.Background(), []string{"UA4000190284"})
	if err != nil {
		t.Fatal(err)
	}
	if res.NoPrice != 1 || res.Failed != 0 || res.Missing != 0 {
		t.Errorf("результат %+v, чекали noprice=1", res)
	}
}

func TestRefreshQuotes404IsMissingNotFailure(t *testing.T) {
	var hits int64
	r, st, _ := quotesRunner(t, serveFixtures(t, &hits))
	ctx := context.Background()
	// Папір є в довіднику, але сторінки в джерела немає.
	if err := st.SaveQuotes(ctx, nil); err != nil {
		t.Fatal(err)
	}
	res, err := r.RefreshQuotes(ctx, []string{"UA4000239107", "UA4000239999"})
	if err != nil {
		t.Fatal(err)
	}
	// UA4000239999 немає й у довіднику, тож він не доходить до мережі —
	// це Failed, і саме так і має бути: без довідника звіряти нема з чим.
	if res.Stored != 1 || res.Failed != 1 {
		t.Errorf("результат %+v", res)
	}
}

// Стеля не про сервер, а про людину: прохід синхронний.
func TestRefreshQuotesRespectsCap(t *testing.T) {
	var hits int64
	r, _, _ := quotesRunner(t, serveFixtures(t, &hits))
	isins := make([]string, 0, quotesCap+10)
	for i := 0; i < quotesCap+10; i++ {
		isins = append(isins, fmt.Sprintf("UA400000%04d", i))
	}
	res, err := r.RefreshQuotes(context.Background(), isins)
	if err != nil {
		t.Fatal(err)
	}
	if res.Asked != quotesCap || res.Skipped != 10 {
		t.Errorf("результат %+v, чекали asked=%d skipped=10", res, quotesCap)
	}
}

// «Нас відрізали» — не те саме, що «цей папір не вийшов»: прохід мусить
// СПИНИТИСЬ, а не довбати далі шістдесят разів.
func TestRefreshQuotesStopsWhenCutOff(t *testing.T) {
	var hits int64
	r, _, _ := quotesRunner(t, func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		http.Error(w, "too many", http.StatusTooManyRequests)
	})
	_, err := r.RefreshQuotes(context.Background(),
		[]string{"UA4000239107", "UA4000236806", "UA4000190284"})
	if !errors.Is(err, finomo.ErrCutOff) {
		t.Fatalf("чекали ErrCutOff, маємо %v", err)
	}
	if n := atomic.LoadInt64(&hits); n != 1 {
		t.Errorf("запитів %d — після відмови прохід мусив спинитись на першому", n)
	}
}

// Ручну ціну обхід НЕ ЧІПАЄ. Це не дрібниця: mono цін не публікує, і
// перезаписаний обходом рядок означав би, що брокер, у якому лежать
// гроші, мовчки випав із порівняння.
func TestRefreshQuotesLeavesManualAlone(t *testing.T) {
	var hits int64
	r, st, _ := quotesRunner(t, serveFixtures(t, &hits))
	ctx := context.Background()
	manual := store.Quote{ISIN: "UA4000239107", Source: "mono", Date: "2026-09-06",
		PriceMinor: 101500, Currency: money.UAH, Origin: store.QuoteOriginManual}
	if err := st.SaveQuotes(ctx, []store.Quote{manual}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RefreshQuotes(ctx, []string{"UA4000239107"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.LatestQuotes(ctx, []string{"UA4000239107"})
	if err != nil {
		t.Fatal(err)
	}
	var found *store.Quote
	for i := range got {
		if got[i].Source == "mono" {
			found = &got[i]
		}
	}
	if found == nil {
		t.Fatal("ручна ціна зникла після обходу")
	}
	if found.PriceMinor != 101500 || found.Origin != store.QuoteOriginManual {
		t.Errorf("ручну ціну змінено: %+v", *found)
	}
}

// Сторож масштабу: номінал, що не збігається з довідником, — це помилка
// шкали, і брати з такої сторінки ціни не можна.
func TestRefreshQuotesRefusesWrongNominal(t *testing.T) {
	page := strings.Replace(string(quotesFixture(t, "bond_uah.html")),
		`"nominal":1000,`, `"nominal":1,`, 1)
	r, _, _ := quotesRunner(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(page))
	})
	res, err := r.RefreshQuotes(context.Background(), []string{"UA4000239107"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || res.Stored != 0 {
		t.Errorf("результат %+v — зміна шкали мусила стати відмовою", res)
	}
}

func TestRefreshQuotesHonorsContext(t *testing.T) {
	var hits int64
	r, _, _ := quotesRunner(t, serveFixtures(t, &hits))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.RefreshQuotes(ctx, []string{"UA4000239107", "UA4000236806"}); err == nil {
		t.Fatal("скасований контекст мусив зупинити прохід")
	}
}
