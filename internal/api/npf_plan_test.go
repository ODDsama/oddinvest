package api

import (
	"encoding/json"
	"strconv"
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
