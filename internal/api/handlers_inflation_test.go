package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

type inflResp struct {
	EffectivePct float64 `json:"effective_pct"`
	Source       string  `json:"source"`
	NowPct       float64 `json:"now_pct"`
	NowMonth     string  `json:"now_month"`
	Note         string  `json:"note"`
	Windows      []struct {
		Years int     `json:"years"`
		Pct   float64 `json:"pct"`
		From  string  `json:"from"`
		To    string  `json:"to"`
	} `json:"windows"`
	Place []struct {
		Years      int     `json:"years"`
		Points     int     `json:"points"`
		Percentile float64 `json:"percentile"`
		MedianPct  float64 `json:"median_pct"`
	} `json:"place"`
}

// TestInflationSilentWithoutSeries — порожній ряд не є помилкою й не дає
// нулів: це стан першого запуску, і картка мусить сказати саме це.
func TestInflationSilentWithoutSeries(t *testing.T) {
	srv, _ := testServer(t)
	resp, body := do(t, "GET", srv.URL+"/api/inflation", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET дав %d: %s", resp.StatusCode, body)
	}
	var out inflResp
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.Source != "none" || out.EffectivePct != 0 || len(out.Windows) != 0 {
		t.Fatalf("порожній ряд дав числа: %+v", out)
	}
	if out.Note == "" {
		t.Fatal("мовчазна відсутність без пояснення")
	}
}

// TestInflationEndpointShape — на справжньому ряду ручка віддає чинне
// число, вікна й місце нинішнього темпу серед історії.
func TestInflationEndpointShape(t *testing.T) {
	srv, st := testServer(t)
	seedCPI(t, st, 132) // одинадцять років по місяцю

	resp, body := do(t, "GET", srv.URL+"/api/inflation", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET дав %d: %s", resp.StatusCode, body)
	}
	var out inflResp
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if out.Source != "measured" {
		t.Fatalf("джерело %q при повному ряду", out.Source)
	}
	if out.EffectivePct <= 0 {
		t.Fatalf("чинне значення %v", out.EffectivePct)
	}
	if len(out.Windows) < 3 {
		t.Fatalf("вікон %d — очікували 1/3/5/10", len(out.Windows))
	}
	// Довше вікно мусить спиратись на більше місяців — інакше відбір за
	// датою мовчки не працює.
	if len(out.Place) < 2 || out.Place[0].Points >= out.Place[len(out.Place)-1].Points {
		t.Fatalf("вікна перцентиля не ростуть: %+v", out.Place)
	}
	if out.NowMonth == "" {
		t.Fatal("не названо, за який місяць нинішній темп")
	}
}

// seedCPI кладе n місяців ряду, що закінчується минулим місяцем: рівно
// так, як його наповнює бекфіл.
func seedCPI(t *testing.T, st *store.Store, n int) {
	t.Helper()
	ctx := t.Context()
	m := prevMonthStr(monthOf(domain.NewDate(time.Now())))
	for i := 0; i < n; i++ {
		// Проста, але НЕ рівна сітка: рівний ряд дав би перцентиль 50 на
		// будь-якому вікні, тобто перевірку, яка не може не зійтись.
		mom := int64(50 + (i%7)*20)
		yoy := int64(600 + (i%5)*300)
		if err := st.SaveCPI(ctx, store.CPIPoint{Period: m, MoMBP: mom, YoYBP: yoy}); err != nil {
			t.Fatal(err)
		}
		m = prevMonthStr(m)
	}
}

// prevMonthStr — попередній місяць 'YYYY-MM'. Свій, а не з jobs: пакети
// різні, а правило одне рядкове.
func prevMonthStr(m string) string {
	d, err := domain.ParseDate(m + "-01")
	if err != nil {
		return m
	}
	return string(d.AddMonths(-1))[:7]
}
