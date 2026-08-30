package api

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
)

// Гроші місяця діляться між видами ПІСЛЯ подушки Й ПІСЛЯ цілей.
//
// Привід у цих тестів конкретний: доти база поділу віднімала лише подушку,
// а розкладка надходження (POST /api/allocate) віднімала й цілі — і карта
// «Скільки чого за стратегією» обіцяла на стелю цілей більше, ніж
// показувала модалка, яку сама ж і відкриває. Два екрани давали різні
// числа на ті самі гроші.

// splitEnv піднімає найпростіший світ, у якому обидві стелі живі: план
// доходу (без нього стелі нема від чого рахувати), ціль подушки в місяцях
// витрат, ціль накопичення і 100% цілі за видом в ОВДП — щоб уся база
// поділу пішла в один рядок і її можна було прочитати одним числом.
func splitEnv(t *testing.T, goalsSharePct string) string {
	t.Helper()
	srv, _ := testServer(t)

	settings := `{"monthly_expenses":"10000","monthly_expenses_currency":"UAH",
		"reserve_target_months":"6","reserve_fill_share_pct":"30",
		"target_bonds_pct":"100"`
	if goalsSharePct != "" {
		settings += `,"goals_fill_share_pct":"` + goalsSharePct + `"`
	}
	settings += "}"
	if resp, b := do(t, "PUT", srv.URL+"/api/settings", settings); resp.StatusCode >= 300 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"50000.00","cadence":"month",
		  "from_date":"2026-01-01","invest_pct":"100"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("потік: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/goals",
		`{"name":"Авто","amount":"500000","currency":"UAH"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("ціль: %d %s", resp.StatusCode, b)
	}
	// Капітал мусить бути ненульовий: частка від нуля не міряється, і
	// buildRebalance рядків видів у порожньому портфелі свідомо не малює.
	if resp, b := do(t, "POST", srv.URL+"/api/deposits",
		`{"amount":"100000.00","currency":"UAH","date":"2026-01-05","broker":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("внесок: %d %s", resp.StatusCode, b)
	}
	return srv.URL
}

// splitSummary — три числа, з яких складається база поділу, і те, що з неї
// зрештою дісталось видам.
type splitSummary struct {
	PlanUAH      float64
	ReserveUAH   float64
	GoalsUAH     float64
	KindShareUAH float64
}

func readSplit(t *testing.T, base string) splitSummary {
	t.Helper()
	var doc struct {
		MonthPlan *struct {
			PlanUAH float64 `json:"plan_uah"`
		} `json:"month_plan"`
		Reserve *struct {
			FillMonthUAH float64 `json:"fill_month_uah"`
		} `json:"reserve"`
		Goals []struct {
			DoneDate     string  `json:"done_date"`
			FillMonthUAH float64 `json:"fill_month_uah"`
		} `json:"goals"`
		Rebalance []struct {
			Dimension     string  `json:"dimension"`
			Key           string  `json:"key"`
			TargetPct     float64 `json:"target_pct"`
			MonthShareUAH float64 `json:"month_share_uah"`
		} `json:"rebalance"`
	}
	_, body := do(t, "GET", base+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	if doc.MonthPlan == nil {
		t.Fatal("плану місяця немає — стелі нема від чого рахувати")
	}
	out := splitSummary{PlanUAH: doc.MonthPlan.PlanUAH}
	if doc.Reserve != nil {
		out.ReserveUAH = doc.Reserve.FillMonthUAH
	}
	for _, g := range doc.Goals {
		if g.DoneDate == "" {
			out.GoalsUAH += g.FillMonthUAH
		}
	}
	for _, r := range doc.Rebalance {
		if r.Dimension == "kind" && r.TargetPct > 0 {
			out.KindShareUAH += r.MonthShareUAH
		}
	}
	return out
}

// Головний інваріант: те, що дісталось видам, дорівнює плану без обох
// вирізок. При цілі 100% в ОВДП «за часткою» бере рівно всю базу, тож
// рівність читається прямо, без поправок на нерозподілені відсотки.
func TestMonthSplitSubtractsGoalsCeiling(t *testing.T) {
	got := readSplit(t, splitEnv(t, "30"))

	if !(got.GoalsUAH > 0) {
		t.Fatalf("стеля цілей нульова — тест перевіряв би порожнечу: %+v", got)
	}
	want := got.PlanUAH - got.ReserveUAH - got.GoalsUAH
	if math.Abs(got.KindShareUAH-want) > 0.01 {
		t.Errorf("видам дісталось %.2f, а мало %.2f (план %.2f − подушка %.2f − цілі %.2f)",
			got.KindShareUAH, want, got.PlanUAH, got.ReserveUAH, got.GoalsUAH)
	}
}

// Без стелі цілей база лишається такою, якою була: план мінус подушка.
//
// Той, хто про цілі не просив, не мусить побачити жодної зміни — і саме
// це відрізняє виправлення від нового правила.
func TestMonthSplitUnchangedWithoutGoalsCeiling(t *testing.T) {
	got := readSplit(t, splitEnv(t, ""))

	if got.GoalsUAH != 0 {
		t.Fatalf("стеля цілей ожила без налаштування: %.2f", got.GoalsUAH)
	}
	want := got.PlanUAH - got.ReserveUAH
	if math.Abs(got.KindShareUAH-want) > 0.01 {
		t.Errorf("видам дісталось %.2f, а мало %.2f (план %.2f − подушка %.2f)",
			got.KindShareUAH, want, got.PlanUAH, got.ReserveUAH)
	}
}

// Карта й розкладка ділять ОДНУ базу.
//
// Це та сама порода інваріанта, що TestRouteFirstLegEqualsAllocate: два
// екрани, які відповідають на одне питання, мусять сходитись числом, а не
// «приблизно однаково». Розкладка на всі гроші місяця дає avail_uah — і
// воно мусить дорівнювати тому, що карта віддала видам.
//
// Порівняння чесне лише в місяці, у якому ще нічого не відкладено: далі
// розкладка ріже вже за ЗАЛИШКОМ стелі (fill_now), а карта показує СТЕЛЮ
// цілком (fill_month), і ця різниця навмисна в обох.
func TestMonthSplitMatchesAllocateBase(t *testing.T) {
	base := splitEnv(t, "30")
	got := readSplit(t, base)

	var plan struct {
		AvailUAH float64 `json:"avail_uah"`
	}
	body := `{"amount":"` + trimFloat(got.PlanUAH) + `"}`
	_, resp := do(t, "POST", base+"/api/allocate", body)
	if err := json.Unmarshal([]byte(resp), &plan); err != nil {
		t.Fatalf("allocate: %v: %s", err, resp)
	}
	if math.Abs(got.KindShareUAH-plan.AvailUAH) > 0.01 {
		t.Errorf("карта віддала видам %.2f, розкладка ділить %.2f — два числа на ті самі гроші",
			got.KindShareUAH, plan.AvailUAH)
	}
}

func trimFloat(v float64) string {
	b, _ := json.Marshal(math.Round(v*100) / 100)
	return string(b)
}
