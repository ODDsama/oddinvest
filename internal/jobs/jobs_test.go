package jobs

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// auctionStub — НБУ, який рахує звернення й віддає один аукціон на
// заданий день. Порожньо на будь-який інший, як і справжній.
type auctionStub struct {
	srv   *httptest.Server
	calls atomic.Int64
	day   string // DD.MM.YYYY єдиного аукціонного дня
}

func newAuctionStub(t *testing.T, day string) *auctionStub {
	t.Helper()
	st := &auctionStub{day: day}
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.calls.Add(1)
		q := r.URL.Query().Get("date")
		// Без дати — останній аукціонний день; з датою — лише той день.
		// Порожній st.day означає «аукціонів не було взагалі».
		if st.day != "" && (q == "" || q == st.day) {
			w.Write([]byte(`[{"AuctionDate":"` + st.day + `","AuctionNum":"91","ValCode":"UAH",` +
				`"StockCode":"UA4000239016","RepayDate":"21.07.2027","DaysToRepay":343,` +
				`"Bucket":"1y","IncomeLevel":15.19,"MinLevel":15.05,"MaxLevel":15.34,` +
				`"BTC":1.7,"VolumeSold":713761000}]`)) //nolint:errcheck // тестовий стенд
			return
		}
		w.Write([]byte(`[]`)) //nolint:errcheck // тестовий стенд
	}))
	t.Cleanup(st.srv.Close)
	return st
}

func testRunner(t *testing.T, base string) (*Runner, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	build := func(context.Context, time.Time) (*state.Doc, error) { return &state.Doc{}, nil }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r := New(st, nbu.New(base), nil, build, log, "")
	// Витримка між запитами тут ні до чого: перевіряємо, СКІЛЬКИ запитів
	// іде, а не як повільно. З бойовими 250 мс тест на стелю чекав би 15
	// секунд рівно ні на що.
	r.pause = 0
	return r, st
}

// runnerToday — сьогодні очима самої джоби. Вона живе за Києвом, а тест
// може бігти під UTC: пізно ввечері це різні дати, і зіставляти знак із
// локальним «сьогодні» означало б падати щодня між 21:00 і опівніччю.
func runnerToday(r *Runner) string {
	return string(domain.NewDate(time.Now().In(r.loc)))
}

// Обіцянка, заради якої вся стратегія опитування й вигадана: в усталеному
// режимі — РІВНО ОДИН запит на добу. Тримається вона на тому, що запит без
// дати віддає останній аукціонний день: якщо він не новіший за знак, до
// якого ми дійшли, то між ними нічого немає й перебирати дати нема чого.
func TestRefreshAuctionsSteadyStateIsOneRequest(t *testing.T) {
	today := time.Now()
	stub := newAuctionStub(t, today.Format("02.01.2006"))
	r, st := testRunner(t, stub.srv.URL)
	ctx := context.Background()

	// Перший прогін: історію не тягне (це справа бекфілу), лише ставить знак.
	if err := r.RefreshAuctions(ctx); err != nil {
		t.Fatal(err)
	}
	if got := stub.calls.Load(); got != 1 {
		t.Fatalf("перший прогін: запитів %d, хочемо 1", got)
	}
	// І в наступні доби — теж по одному: нового аукціону не з'явилось.
	for i := 0; i < 3; i++ {
		if err := r.RefreshAuctions(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := stub.calls.Load(); got != 4 {
		t.Errorf("усталений режим: запитів %d на 4 прогони, хочемо 4", got)
	}
	// Дані при цьому таки збереглись.
	last, err := st.LastAuctionByISIN(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if last["UA4000239016"].IncomeBP != 1519 {
		t.Errorf("рівень не збережено: %+v", last)
	}
	// Знак стоїть на сьогодні, тож завтрашній прогін догонятиме один день,
	// а не всю історію.
	through, err := st.GetSetting(ctx, auctionWatermark)
	if err != nil {
		t.Fatal(err)
	}
	if through != runnerToday(r) {
		t.Errorf("знак = %q, хочемо сьогоднішню дату", through)
	}
}

// Пропущені дні догоняються по одному — але не більше стелі за прогін:
// RefreshAll живе під п'ятихвилинним контекстом, а сервіс міг стояти
// місяцями, і перебір усіх дат з'їв би його до знімка й публікації.
func TestRefreshAuctionsCatchupIsCapped(t *testing.T) {
	today := time.Now()
	stub := newAuctionStub(t, today.Format("02.01.2006"))
	r, st := testRunner(t, stub.srv.URL)
	ctx := context.Background()

	// Знак річної давнини — сервіс стояв рік.
	old := domain.NewDate(today.AddDate(0, 0, -400))
	if err := st.SetSetting(ctx, auctionWatermark, string(old)); err != nil {
		t.Fatal(err)
	}
	if err := r.RefreshAuctions(ctx); err != nil {
		t.Fatal(err)
	}
	// 1 запит без дати + не більше стелі з датами.
	got := stub.calls.Load()
	if got > auctionCatchupCap+1 {
		t.Errorf("запитів %d — стеля %d мала обрізати догін", got, auctionCatchupCap)
	}
	if got < 2 {
		t.Errorf("запитів %d — догін мав відбутись узагалі", got)
	}
	// Після догону знак пересунуто на сьогодні, інакше наступний прогін
	// довбав би ті самі дати щодоби.
	through, err := st.GetSetting(ctx, auctionWatermark)
	if err != nil {
		t.Fatal(err)
	}
	if through != runnerToday(r) {
		t.Errorf("знак після догону = %q", through)
	}
}

// День без аукціону — звичайна відповідь. Джоба не має ні падати, ні
// вважати це приводом щось перезапитувати.
func TestRefreshAuctionsSurvivesEmptyDays(t *testing.T) {
	stub := newAuctionStub(t, "") // аукціонів не було взагалі
	r, st := testRunner(t, stub.srv.URL)
	ctx := context.Background()
	if err := r.RefreshAuctions(ctx); err != nil {
		t.Fatalf("порожня відповідь не мала бути помилкою: %v", err)
	}
	days, err := st.CountAuctionDays(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if days != 0 {
		t.Errorf("днів у базі %d, хочемо 0", days)
	}
}

// Бекфіл не запускається, коли історія вже є: 365 запитів на кожен
// перезапуск сервісу — це саме те зловживання, від якого відмовляється
// бекфіл курсів поруч.
func TestBackfillAuctionsIfThinSkipsWhenFilled(t *testing.T) {
	stub := newAuctionStub(t, time.Now().Format("02.01.2006"))
	r, st := testRunner(t, stub.srv.URL)
	ctx := context.Background()
	// Наливаємо «достатньо» днів повз мережу.
	var rows []nbu.Auction
	for i := 0; i < 5; i++ {
		d := domain.NewDate(time.Now().AddDate(0, 0, -i*7))
		rows = append(rows, nbu.Auction{Date: d, ISIN: "UA4000239016", Num: "91",
			Currency: "UAH", Bucket: "1y", IncomeBP: 1519, DaysToRepay: 343})
	}
	if err := st.SaveAuctions(ctx, rows); err != nil {
		t.Fatal(err)
	}
	r.BackfillAuctionsIfThin(ctx, 52, 5)
	if got := stub.calls.Load(); got != 0 {
		t.Errorf("бекфіл сходив у НБУ %d разів, хоч історії досить", got)
	}
}
