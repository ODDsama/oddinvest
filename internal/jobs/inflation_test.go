package jobs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/store"
)

// cpiNBU — НБУ, що віддає ІСЦ лише за місяці зі списку published.
//
// Ключ у списку — параметр date= (тобто МІСЯЦЬ ПІСЛЯ звітного), бо саме
// його бачить сервер; так тест заразом стереже й сам зсув. Решта місяців
// — порожній масив, тобто «ще не опубліковано».
func cpiNBU(t *testing.T, published map[string]float64, asked *[]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d := r.URL.Query().Get("date")
		if asked != nil {
			*asked = append(*asked, d)
		}
		mom, ok := published[d]
		if !ok {
			w.Write([]byte(`[]`))
			return
		}
		fmt.Fprintf(w, `[
		  {"id_api":"prices_price_cpi_","mcrd081":"Total","ku":null,"tzep":"PCPM_","value":%v},
		  {"id_api":"prices_price_cpi_","mcrd081":"Total","ku":null,"tzep":"PCCM_","value":9.9}
		]`, mom)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func monthsAgo(loc *time.Location, n int) string {
	return monthOf(time.Now().In(loc).AddDate(0, -n, 0))
}

func TestRefreshCPIAdvancesWatermark(t *testing.T) {
	r, st := dailyRunner(t, "", "")
	ctx := context.Background()

	// Опубліковано два останні завершені місяці.
	one, two := monthsAgo(r.loc, 1), monthsAgo(r.loc, 2)
	published := map[string]float64{
		strings.ReplaceAll(nextMonth(one), "-", ""): 1.4,
		strings.ReplaceAll(nextMonth(two), "-", ""): 0.6,
	}
	r.nbu = nbu.New(cpiNBU(t, published, nil))

	// Знак стоїть на три місяці назад — джоба мусить дійти до останнього.
	if err := st.SetAppState(ctx, cpiWatermark, monthsAgo(r.loc, 3)); err != nil {
		t.Fatal(err)
	}
	if err := r.RefreshCPI(ctx); err != nil {
		t.Fatal(err)
	}

	mark, err := st.GetAppState(ctx, cpiWatermark)
	if err != nil {
		t.Fatal(err)
	}
	if mark != one {
		t.Fatalf("знак = %q, хочемо %q", mark, one)
	}
	n, err := st.CPIMonthCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("збережено місяців %d, хочемо 2", n)
	}
}

// TestRefreshCPIStopsAtUnpublishedMonth — перший тиждень кожного місяця
// ІСЦ за попередній ще не вийшов. Це не помилка, знак не рухається, і
// завтра джоба спробує той самий місяць знову.
func TestRefreshCPIStopsAtUnpublishedMonth(t *testing.T) {
	r, st := dailyRunner(t, "", "")
	ctx := context.Background()

	two := monthsAgo(r.loc, 2)
	published := map[string]float64{strings.ReplaceAll(nextMonth(two), "-", ""): 0.6}
	r.nbu = nbu.New(cpiNBU(t, published, nil))

	if err := st.SetAppState(ctx, cpiWatermark, monthsAgo(r.loc, 3)); err != nil {
		t.Fatal(err)
	}
	if err := r.RefreshCPI(ctx); err != nil {
		t.Fatalf("неопублікований місяць не мусить бути помилкою: %v", err)
	}

	mark, err := st.GetAppState(ctx, cpiWatermark)
	if err != nil {
		t.Fatal(err)
	}
	if mark != two {
		t.Fatalf("знак = %q, хочемо %q — він мусить стояти на останньому ОПУБЛІКОВАНОМУ", mark, two)
	}
}

// TestRefreshCPIWithoutWatermarkTakesLastMonthOnly — на свіжій базі
// добова джоба не перебирає роки: історію добирає бекфіл.
func TestRefreshCPIWithoutWatermarkTakesLastMonthOnly(t *testing.T) {
	r, st := dailyRunner(t, "", "")
	ctx := context.Background()

	var asked []string
	one := monthsAgo(r.loc, 1)
	published := map[string]float64{strings.ReplaceAll(nextMonth(one), "-", ""): 1.4}
	r.nbu = nbu.New(cpiNBU(t, published, &asked))

	if err := r.RefreshCPI(ctx); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 {
		t.Fatalf("запитів %d, хочемо 1: %v", len(asked), asked)
	}
	n, _ := st.CPIMonthCount(ctx)
	if n != 1 {
		t.Fatalf("збережено місяців %d", n)
	}
}

// TestBackfillCPISkipsMissingMonths — ряд НБУ починається близько 2008
// року, тож давні місяці законно порожні. Бекфіл їх РАХУЄ й іде далі —
// протилежно до RefreshCPI, і протилежність навмисна.
func TestBackfillCPISkipsMissingMonths(t *testing.T) {
	r, st := dailyRunner(t, "", "")
	ctx := context.Background()

	one := monthsAgo(r.loc, 1)
	published := map[string]float64{strings.ReplaceAll(nextMonth(one), "-", ""): 1.4}
	r.nbu = nbu.New(cpiNBU(t, published, nil))

	if err := r.BackfillCPI(ctx, 1); err != nil {
		t.Fatalf("порожні місяці не мусять валити бекфіл: %v", err)
	}
	n, err := st.CPIMonthCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("збережено місяців %d, хочемо 1", n)
	}
}

func TestBackfillCPISkipsWhenThick(t *testing.T) {
	r, st := dailyRunner(t, "", "")
	ctx := context.Background()

	var asked []string
	r.nbu = nbu.New(cpiNBU(t, nil, &asked))
	if err := st.SaveCPI(ctx, store.CPIPoint{Period: "2024-12", MoMBP: 140, YoYBP: 1200}); err != nil {
		t.Fatal(err)
	}

	r.BackfillCPIIfThin(ctx, 10, 1) // маємо 1, треба 1 — тягнути нема чого
	if len(asked) != 0 {
		t.Fatalf("бекфіл побіг при достатній історії: %v", asked)
	}
}
