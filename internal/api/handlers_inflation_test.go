package api

import (
	"encoding/json"
	"net/http"
	"strings"
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
	seedCPIHoled(t, st, n, -1)
}

// seedCPIHoled — те саме, але з ПРОПУЩЕНИМ місяцем під номером skip
// (від найсвіжішого). Повертає назву пропущеного; -1 = без дірки.
func seedCPIHoled(t *testing.T, st *store.Store, n, skip int) string {
	t.Helper()
	ctx := t.Context()
	m := prevMonthStr(monthOf(domain.NewDate(time.Now())))
	hole := ""
	for i := 0; i < n; i++ {
		if i == skip {
			hole = m
			m = prevMonthStr(m)
			continue
		}
		// Проста, але НЕ рівна сітка: рівний ряд дав би перцентиль 50 на
		// будь-якому вікні, тобто перевірку, яка не може не зійтись.
		mom := int64(50 + (i%7)*20)
		yoy := int64(600 + (i%5)*300)
		if err := st.SaveCPI(ctx, store.CPIPoint{Period: m, MoMBP: mom, YoYBP: yoy}); err != nil {
			t.Fatal(err)
		}
		m = prevMonthStr(m)
	}
	return hole
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

// TestInflationSilentOnHoledSeries — ГОЛОВНА перевірка після бойового
// провалу: ряд із діркою дає ПРАВДОПОДІБНЕ, але хибне число, бо
// пропущений місяць просто не множиться. На бойовому 32 дірки занизили
// інфляцію з 10.72 до 7.92%/рік — і жодна наявна перевірка цього не
// помітила, бо звірка з річним темпом дивиться на останню точку, а
// останній рік був цілий.
func TestInflationSilentOnHoledSeries(t *testing.T) {
	srv, st := testServer(t)
	// Ряд із діркою посередині — рівно те, що робить серія 503 від НБУ.
	hole := seedCPIHoled(t, st, 132, 60)

	out := getInflationResp(t, srv.URL)
	if out.Source != "holes" {
		t.Fatalf("ряд із діркою дав джерело %q — число мусить замовкнути", out.Source)
	}
	if out.EffectivePct != 0 {
		t.Fatalf("зіпсоване число все одно опубліковано: %v", out.EffectivePct)
	}
	if out.Note == "" || !strings.Contains(out.Note, hole) {
		t.Fatalf("примітка не називає першої дірки (%s): %q", hole, out.Note)
	}
	if len(out.Windows) != 0 {
		t.Fatalf("вікна порахувались на дірявому ряду: %+v", out.Windows)
	}
}

func getInflationResp(t *testing.T, base string) inflResp {
	t.Helper()
	resp, body := do(t, "GET", base+"/api/inflation", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET дав %d: %s", resp.StatusCode, body)
	}
	var out inflResp
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
