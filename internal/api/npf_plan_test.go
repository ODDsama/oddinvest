package api

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

// npfPlanEvent — сума події «пенсія стає доступна» на осі профілю.
//
// Читається саме з /api/plan, а не з проєкції: подія й крива капіталу йдуть
// РІЗНИМИ шляхами до того самого числа (AccumCloseValue проти симуляції), і
// тест на подію ловить розходження між ними.
//
// Профіль існує лише за наявності бодай одного потоку плану
// (buildPlanProfile), тож усі тести нижче спершу заводять якийсь потік —
// інакше вимірювати було б нічого.
func npfPlanEvent(t *testing.T, url string) (float64, bool) {
	t.Helper()
	var tl struct {
		Profile *struct {
			Events []struct {
				Kind      string  `json:"kind"`
				AmountUAH float64 `json:"amount_uah"`
			} `json:"events"`
		} `json:"profile"`
	}
	_, body := do(t, "GET", url+"/api/plan", "")
	if err := json.Unmarshal([]byte(body), &tl); err != nil {
		t.Fatalf("розбір /api/plan: %v", err)
	}
	if tl.Profile == nil {
		return 0, false
	}
	for _, e := range tl.Profile.Events {
		if e.Kind == "npf" {
			return e.AmountUAH, true
		}
	}
	return 0, false
}

// seedNPFPlan заводить пенсійний рахунок і базовий потік доходу.
//
// Дата доступу — через шість місяців, а не через двадцять пʼять років, і це
// не спрощення заради тесту: горизонт стрічки за замовчуванням дванадцять
// місяців (to := today.AddMonths(12)), тож подія з 2051-го в неї просто не
// потрапила б, і тест перевіряв би відсутність замість присутності.
func seedNPFPlan(t *testing.T, srvURL string) int64 {
	t.Helper()
	now := time.Now()
	access := now.AddDate(0, 6, 0).Format("2006-01-02")
	navDate := now.Format("2006-01-02")
	acc := `{"name":"Династія","currency":"UAH","nav":"2.0","nav_date":"` + navDate + `",
		"expected_yield_pct":"12","access_date":"` + access + `","income_tax_pct":"13.8",
		"credit_rate_pct":"18","contrib_day":5}`
	resp, b := do(t, "POST", srvURL+"/api/npf-accounts", acc)
	if resp.StatusCode != 201 {
		t.Fatalf("рахунок НПФ: %d %s", resp.StatusCode, b)
	}
	var created struct{ ID int64 }
	if err := json.Unmarshal([]byte(b), &created); err != nil {
		t.Fatal(err)
	}
	// 1000 ₴ за ЧВОПА 2.0 = 500 одиниць.
	op := `{"npf_id":` + strconv.FormatInt(created.ID, 10) + `,"date":"` + navDate + `",
		"amount":"1000","units":"500","broker":"ПУМБ"}`
	if resp, b := do(t, "POST", srvURL+"/api/npf", op); resp.StatusCode != 201 {
		t.Fatalf("внесок: %d %s", resp.StatusCode, b)
	}
	// Потік доходу — щоб профіль узагалі існував.
	salary := `{"name":"Зарплата","kind":"income","amount":"40000","cadence":"month",
		"from_date":"` + navDate + `"}`
	if resp, b := do(t, "POST", srvURL+"/api/plan/flows", salary); resp.StatusCode != 201 {
		t.Fatalf("потік доходу: %d %s", resp.StatusCode, b)
	}
	return created.ID
}

func npfContribFlow(t *testing.T, srvURL, dest string) {
	t.Helper()
	from := time.Now().Format("2006-01-02")
	dst := ""
	if dest != "" {
		dst = `,"dest":"` + dest + `"`
	}
	flow := `{"name":"Внесок","kind":"expense","amount":"1000","cadence":"month",
		"from_date":"` + from + `"` + dst + `}`
	if resp, b := do(t, "POST", srvURL+"/api/plan/flows", flow); resp.StatusCode != 201 {
		t.Fatalf("потік внеску: %d %s", resp.StatusCode, b)
	}
}

// TestPlanFlowToNPFGrowsThePensionBucket — внесок із призначенням npf:<id>
// доходить у пенсійний рахунок, а не зникає як звичайна витрата.
//
// Це перевірка ДРУГОЇ половини руху. Перша (мінус у ліквідному) працює сама
// собою, бо внесок заведений витратою; заради другої й зʼявилась колонка
// dest. Без неї проєкція мовчки губила пенсійні гроші — і число лишалось би
// правдоподібним, бо витрата справді має зменшувати капітал.
func TestPlanFlowToNPFGrowsThePensionBucket(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	id := seedNPFPlan(t, srv.URL)

	before, ok := npfPlanEvent(t, srv.URL)
	if !ok {
		t.Fatal("події «пенсія доступна» немає на осі, хоч дата доступу задана")
	}
	npfContribFlow(t, srv.URL, "npf:"+strconv.FormatInt(id, 10))

	after, ok := npfPlanEvent(t, srv.URL)
	if !ok {
		t.Fatal("подія зникла після заведення внеску")
	}
	// Шість внесків по 1000 до дати доступу. Приріст мусить бути того ж
	// порядку, а не копійчаним: інакше вектор дійшов, але не повністю.
	if diff := after - before; diff < 5000 {
		t.Errorf("приріст %.2f замість ~6000 — планові внески не дійшли "+
			"в пенсійний рахунок (було %.2f, стало %.2f)", diff, before, after)
	}
}

// TestPlanFlowWithoutDestDoesNotFeedNPF — та сама витрата БЕЗ призначення
// пенсійний рахунок не годує.
//
// Дзеркало попереднього тесту, і потрібне воно окремо: якби маршрутизація
// ловила будь-яку витрату, перший тест лишався б зеленим, а квартплата тихо
// ставала б пенсійним капіталом.
func TestPlanFlowWithoutDestDoesNotFeedNPF(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	seedNPFPlan(t, srv.URL)

	before, ok := npfPlanEvent(t, srv.URL)
	if !ok {
		t.Fatal("події «пенсія доступна» немає на осі")
	}
	npfContribFlow(t, srv.URL, "")
	after, _ := npfPlanEvent(t, srv.URL)
	if after != before {
		t.Errorf("витрата без призначення змінила пенсійну подію: %.2f → %.2f", before, after)
	}
}

// TestNPFDestSurvivesBackup — призначення потоку переживає відновлення.
//
// Без нього внесок після restore став би звичайною витратою, і пенсійні
// гроші почали б зникати — тихо, бо витрата справді має зменшувати капітал.
func TestNPFDestSurvivesBackup(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	id := seedNPFPlan(t, srv.URL)
	npfContribFlow(t, srv.URL, "npf:"+strconv.FormatInt(id, 10))
	want, ok := npfPlanEvent(t, srv.URL)
	if !ok {
		t.Fatal("події немає ще до бекапу")
	}

	_, dump := do(t, "GET", srv.URL+"/api/backup", "")
	if resp, b := do(t, "POST", srv.URL+"/api/restore", dump); resp.StatusCode != 200 {
		t.Fatalf("відновлення: %d %s", resp.StatusCode, b)
	}
	got, ok := npfPlanEvent(t, srv.URL)
	if !ok {
		t.Fatal("після відновлення пенсійної події немає")
	}
	if got != want {
		t.Errorf("призначення потоку загубилось у бекапі: %.2f замість %.2f", got, want)
	}
}

// TestTaxCreditsStayOutOfTotals — знижка показується окремо й НЕ протікає в
// загальні суми звіту.
//
// «Разом» відповідає на питання «скільки податку з мене взяли». Домішавши
// туди повернення, застосунок відповідав би на нього заниженим числом — а
// заразом зламав би арифметику рядка: у by_kind стоїть
// net = gross − tax і rate = tax/gross, і від'ємний рядок робить безглуздими
// всі чотири числа.
func TestTaxCreditsStayOutOfTotals(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	year := time.Now().Year()
	navDate := time.Now().Format("2006-01-02")

	// Знижку вмикає САМЕ річний ПДФО: без нього оцінка нульова.
	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"npf_credit_pdfo_year_uah":"40000","npf_credit_cap_month_uah":"4660"}`); resp.StatusCode != 204 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, b)
	}
	acc := `{"name":"Династія","currency":"UAH","nav":"2.0","nav_date":"` + navDate + `",
		"credit_rate_pct":"18"}`
	resp, b := do(t, "POST", srv.URL+"/api/npf-accounts", acc)
	if resp.StatusCode != 201 {
		t.Fatalf("рахунок: %d %s", resp.StatusCode, b)
	}
	var created struct{ ID int64 }
	if err := json.Unmarshal([]byte(b), &created); err != nil {
		t.Fatal(err)
	}
	op := `{"npf_id":` + strconv.FormatInt(created.ID, 10) + `,"date":"` + navDate + `",
		"amount":"1000","units":"500"}`
	if resp, b := do(t, "POST", srv.URL+"/api/npf", op); resp.StatusCode != 201 {
		t.Fatalf("внесок: %d %s", resp.StatusCode, b)
	}

	var tax struct {
		GrossUAH float64 `json:"gross_uah"`
		TaxUAH   float64 `json:"tax_uah"`
		ByKind   []struct {
			Kind string `json:"kind"`
		} `json:"by_kind"`
		Credits []struct {
			Kind   string  `json:"kind"`
			NetUAH float64 `json:"net_uah"`
			TaxUAH float64 `json:"tax_uah"`
		} `json:"credits"`
		Note string `json:"note"`
	}
	_, body := do(t, "GET", srv.URL+"/api/tax?year="+strconv.Itoa(year), "")
	if err := json.Unmarshal([]byte(body), &tax); err != nil {
		t.Fatalf("розбір /api/tax: %v\n%s", err, body)
	}
	if len(tax.Credits) != 1 {
		t.Fatalf("очікували один рядок знижки, маємо %d: %s", len(tax.Credits), body)
	}
	// 1000 ₴ внеску × 18% = 180 ₴.
	if got := tax.Credits[0].NetUAH; got != 180 {
		t.Errorf("знижка %.2f, очікували 180", got)
	}
	if tax.Credits[0].TaxUAH >= 0 {
		t.Errorf("у блоці повернень «податок» мусить бути відʼємним, маємо %.2f", tax.Credits[0].TaxUAH)
	}
	// Головне: у by_kind її немає, і в загальні суми вона не входить.
	for _, l := range tax.ByKind {
		if l.Kind == "npf_credit" {
			t.Error("знижка потрапила в by_kind — там арифметика net = gross − tax")
		}
	}
	if tax.TaxUAH < 0 {
		t.Errorf("загальний податок став відʼємним (%.2f) — знижка протекла в «Разом»", tax.TaxUAH)
	}
	if tax.Note == "" {
		t.Error("поруч зі знижкою мусить стояти застереження, що це оцінка")
	}
}

// TestTaxCreditsAbsentForCustomRange — на довільному відрізку from/to знижки
// немає.
//
// Ліміт у неї місячний, а стеля річна, тож на трьох тижнях вона не означала
// б нічого. Показати там число означало б видати випадкову суму за оцінку.
func TestTaxCreditsAbsentForCustomRange(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	navDate := time.Now().Format("2006-01-02")
	if resp, _ := do(t, "PUT", srv.URL+"/api/settings",
		`{"npf_credit_pdfo_year_uah":"40000"}`); resp.StatusCode != 204 {
		t.Fatal("налаштування")
	}
	acc := `{"name":"Династія","currency":"UAH","nav":"2.0","nav_date":"` + navDate + `",
		"credit_rate_pct":"18"}`
	resp, b := do(t, "POST", srv.URL+"/api/npf-accounts", acc)
	if resp.StatusCode != 201 {
		t.Fatalf("рахунок: %d %s", resp.StatusCode, b)
	}
	var created struct{ ID int64 }
	if err := json.Unmarshal([]byte(b), &created); err != nil {
		t.Fatal(err)
	}
	op := `{"npf_id":` + strconv.FormatInt(created.ID, 10) + `,"date":"` + navDate + `",
		"amount":"1000","units":"500"}`
	if resp, _ := do(t, "POST", srv.URL+"/api/npf", op); resp.StatusCode != 201 {
		t.Fatal("внесок")
	}
	var tax struct {
		Credits []struct{} `json:"credits"`
	}
	_, body := do(t, "GET", srv.URL+"/api/tax?from=2026-01-01&to=2026-03-31", "")
	if err := json.Unmarshal([]byte(body), &tax); err != nil {
		t.Fatal(err)
	}
	if len(tax.Credits) != 0 {
		t.Errorf("на довільному відрізку знижки бути не має, маємо %d", len(tax.Credits))
	}
}

// TestNPFSuggestionAlwaysBelowLiquid — замкнена пропозиція стоїть нижче за
// ліквідну в УСІХ режимах ранжування, навіть коли її дохідність вища.
//
// Це і є весь сенс стадії 5. Штрафувати RealPct коефіцієнтом неліквідності
// було б вигаданим числом у головній колонці списку; замість цього замок
// вирішує ПОРЯДОК — твердження про непорівнянність, а не про якість. Тест
// прогонить усі чотири режими, бо саме «в усіх» тут і є вимогою: у режимі
// «за дохідністю» спокуса пустити вищий рядок нагору найсильніша.
func TestNPFSuggestionAlwaysBelowLiquid(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	navDate := time.Now().Format("2006-01-02")
	access := time.Now().AddDate(25, 0, 0).Format("2006-01-02")

	// Вклад із помірною ставкою — ліквідна альтернатива.
	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"deposit_rate_uah_pct":"16","deposit_min_uah":"5000"}`); resp.StatusCode != 204 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, b)
	}
	// Пенсійний рахунок із ЗАВИЩЕНОЮ обіцянкою: 40% — щоб його реальна
	// дохідність гарантовано перебила вклад. Якби порядок вирішувала
	// дохідність, він став би першим.
	acc := `{"name":"Династія","currency":"UAH","nav":"2.0","nav_date":"` + navDate + `",
		"expected_yield_pct":"40","access_date":"` + access + `","income_tax_pct":"13.8",
		"payout_years":10,"payout_freq":"month"}`
	resp, b := do(t, "POST", srv.URL+"/api/npf-accounts", acc)
	if resp.StatusCode != 201 {
		t.Fatalf("рахунок: %d %s", resp.StatusCode, b)
	}
	var created struct{ ID int64 }
	if err := json.Unmarshal([]byte(b), &created); err != nil {
		t.Fatal(err)
	}
	op := `{"npf_id":` + strconv.FormatInt(created.ID, 10) + `,"date":"` + navDate + `",
		"amount":"1000","units":"500"}`
	if resp, b := do(t, "POST", srv.URL+"/api/npf", op); resp.StatusCode != 201 {
		t.Fatalf("внесок: %d %s", resp.StatusCode, b)
	}

	type row struct {
		Kind    string  `json:"kind"`
		Locked  bool    `json:"locked"`
		RealPct float64 `json:"real_pct"`
		Basis   string  `json:"yield_basis"`
	}
	for _, rank := range []string{"plan", "rate", "short", "ladder"} {
		if resp, b := do(t, "PUT", srv.URL+"/api/settings",
			`{"reinvest_rank":"`+rank+`"}`); resp.StatusCode != 204 {
			t.Fatalf("режим %s: %d %s", rank, resp.StatusCode, b)
		}
		var rows []row
		_, body := do(t, "GET", srv.URL+"/api/reinvest", "")
		if err := json.Unmarshal([]byte(body), &rows); err != nil {
			t.Fatalf("режим %s: розбір: %v\n%s", rank, err, body)
		}
		var npfAt, lastLiquidAt = -1, -1
		var npfReal, liquidReal float64
		for i, r := range rows {
			if r.Kind == "npf" {
				npfAt, npfReal = i, r.RealPct
				if !r.Locked {
					t.Errorf("режим %s: рядок НПФ без прапорця locked", rank)
				}
				if !strings.Contains(r.Basis, "замкнено") {
					t.Errorf("режим %s: основа не називає замок: %q", rank, r.Basis)
				}
				continue
			}
			lastLiquidAt = i
			if r.RealPct > liquidReal {
				liquidReal = r.RealPct
			}
		}
		if npfAt < 0 {
			t.Fatalf("режим %s: НПФ узагалі немає в переліку", rank)
		}
		if lastLiquidAt < 0 {
			t.Fatalf("режим %s: ліквідних пропозицій немає, порівнювати нема з чим", rank)
		}
		if npfAt < lastLiquidAt {
			t.Errorf("режим %s: НПФ на позиції %d, а ліквідне тягнеться до %d — "+
				"замкнене обігнало ліквідне", rank, npfAt, lastLiquidAt)
		}
		// І сам сенс перевірки: він обігнав би, якби порядок вирішувала
		// дохідність.
		if npfReal <= liquidReal {
			t.Errorf("режим %s: дохідність НПФ (%.2f) не вища за ліквідну (%.2f) — "+
				"тест перестав перевіряти те, для чого написаний", rank, npfReal, liquidReal)
		}
	}
}
