package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Потоки й дії плану — CRUD за зразком продажів/поповнень: додати, дістати
// назад, поправити, видалити. Розгортання в проєкцію перевіряється окремо
// в internal/domain (sleeves_test.go) і state_projection_test.go.
func TestPlanFlowCRUD(t *testing.T) {
	srv, _ := testServer(t)

	resp, body := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"40000.00","cadence":"month","from_date":"2026-09-01","invest_pct":"30"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання потоку: %d %s", resp.StatusCode, body)
	}

	_, body = do(t, "GET", srv.URL+"/api/plan/flows", "")
	for _, want := range []string{`"name":"Зарплата"`, `"kind":"income"`, `"cadence":"month"`, `"invest_pct":30`} {
		if !strings.Contains(body, want) {
			t.Errorf("список потоків не містить %s: %s", want, body)
		}
	}

	// невідомий kind відхиляється
	if resp, _ := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"х","kind":"transfer","amount":"1.00","cadence":"once","from_date":"2026-09-01"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("невідомий kind мав дати 400, маємо %d", resp.StatusCode)
	}
	// нульова сума відхиляється
	if resp, _ := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"х","kind":"expense","amount":"0","cadence":"once","from_date":"2026-09-01"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("нульова сума мала дати 400, маємо %d", resp.StatusCode)
	}

	if resp, body := do(t, "PUT", srv.URL+"/api/plan/flows/1",
		`{"name":"Зарплата","kind":"income","amount":"45000.00","cadence":"month","from_date":"2026-09-01","invest_pct":"30"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("правка потоку: %d %s", resp.StatusCode, body)
	}
	_, body = do(t, "GET", srv.URL+"/api/plan/flows", "")
	if !strings.Contains(body, `"45000.00"`) {
		t.Errorf("правка не застосувалась: %s", body)
	}

	if resp, body := do(t, "DELETE", srv.URL+"/api/plan/flows/1", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("видалення потоку: %d %s", resp.StatusCode, body)
	}
	if _, body := do(t, "GET", srv.URL+"/api/plan/flows", ""); body != "[]\n" {
		t.Errorf("після видалення список мав спорожніти: %s", body)
	}
}

// set_shares/lock — ключова відмінність від решти CRUD у застосунку:
// відсоткові поля мають сентинел -1 «не задано», бо 0 тут легальна ціль.
func TestPlanActionCRUD(t *testing.T) {
	srv, _ := testServer(t)

	resp, body := do(t, "POST", srv.URL+"/api/plan/actions",
		`{"date":"2027-01-01","type":"set_shares","usd_share_pct":"0","name":"без долара"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("set_shares: %d %s", resp.StatusCode, body)
	}
	_, body = do(t, "GET", srv.URL+"/api/plan/actions", "")
	// нуль — легальна задана частка, і поле мусить зʼявитись у відповіді
	if !strings.Contains(body, `"usd_share_pct":0`) {
		t.Errorf("нульова частка мала лишитись видимою (не сплутана з «не задано»): %s", body)
	}
	if strings.Contains(body, `"eur_share_pct"`) {
		t.Errorf("незадана eur-частка не мала потрапити у відповідь: %s", body)
	}

	// сума часток понад 100% відхиляється
	if resp, _ := do(t, "POST", srv.URL+"/api/plan/actions",
		`{"date":"2027-01-01","type":"set_shares","usd_share_pct":"60","eur_share_pct":"50"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("сума часток понад 100%% мала дати 400, маємо %d", resp.StatusCode)
	}
	// жодної частки не задано
	if resp, _ := do(t, "POST", srv.URL+"/api/plan/actions",
		`{"date":"2027-01-01","type":"set_shares"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("set_shares без жодної частки мав дати 400, маємо %d", resp.StatusCode)
	}

	resp, body = do(t, "POST", srv.URL+"/api/plan/actions",
		`{"date":"2026-12-01","type":"lock","amount":"50000.00","rate_pct":"25","months":36,"name":"MilTech"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("lock: %d %s", resp.StatusCode, body)
	}
	// lock без назви відхиляється — вона підписує стрічку
	if resp, _ := do(t, "POST", srv.URL+"/api/plan/actions",
		`{"date":"2026-12-01","type":"lock","amount":"1000.00","rate_pct":"10","months":12}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("lock без назви мав дати 400, маємо %d", resp.StatusCode)
	}

	_, body = do(t, "GET", srv.URL+"/api/plan/actions", "")
	if !strings.Contains(body, `"MilTech"`) || !strings.Contains(body, `"50000.00"`) {
		t.Errorf("lock-дія не потрапила у список: %s", body)
	}

	// Правка дії доти не мала тесту взагалі, хоча PUT — повна заміна рядка
	// тим самим валідатором, що й POST: пропущене поле не «лишається як
	// було», а стирається.
	if resp, body := do(t, "PUT", srv.URL+"/api/plan/actions/2",
		`{"date":"2026-12-01","type":"lock","amount":"70000.00","rate_pct":"25","months":36,"name":"MilTech"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("правка дії: %d %s", resp.StatusCode, body)
	}
	_, body = do(t, "GET", srv.URL+"/api/plan/actions", "")
	if !strings.Contains(body, `"70000.00"`) {
		t.Errorf("правка дії не застосувалась: %s", body)
	}
	// Правка lock без назви відхиляється так само, як і додавання.
	if resp, _ := do(t, "PUT", srv.URL+"/api/plan/actions/2",
		`{"date":"2026-12-01","type":"lock","amount":"70000.00","rate_pct":"25","months":36}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("правка lock без назви мала дати 400, маємо %d", resp.StatusCode)
	}

	if resp, body := do(t, "DELETE", srv.URL+"/api/plan/actions/2", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("видалення дії: %d %s", resp.StatusCode, body)
	}
	if _, body := do(t, "GET", srv.URL+"/api/plan/actions", ""); strings.Contains(body, "MilTech") {
		t.Errorf("видалена дія лишилась у списку: %s", body)
	}
}

// Профіль надходжень стоїть на тому самому ядрі, що й колонка та плитка,
// тож середнє його net за перші 12 місяців мусить збігтися з
// plan_provides_uah. Це не декоративна перевірка: якби профіль рахувався
// власною арифметикою, картинка розійшлася б із числом просто над нею.
func TestPlanProfileMatchesProvides(t *testing.T) {
	srv, _ := testServer(t)

	for _, b := range []string{
		`{"name":"Зарплата","kind":"income","amount":"40000.00","cadence":"month","from_date":"2020-01-01","invest_pct":"60"}`,
		`{"name":"Премія","kind":"income","amount":"120000.00","cadence":"year","from_date":"2020-03-01"}`,
		`{"name":"Оренда","kind":"expense","amount":"16000.00","cadence":"month","from_date":"2020-01-01"}`,
	} {
		if resp, body := do(t, "POST", srv.URL+"/api/plan/flows", b); resp.StatusCode != http.StatusCreated {
			t.Fatalf("потік: %d %s", resp.StatusCode, body)
		}
	}

	_, body := do(t, "GET", srv.URL+"/api/plan", "")
	var doc struct {
		Profile *struct {
			StepMonths int `json:"step_months"`
			Series     []struct {
				Name string `json:"name"`
				Kind string `json:"kind"`
			} `json:"series"`
			Points []struct {
				Date string  `json:"date"`
				Net  float64 `json:"net"`
			} `json:"points"`
		} `json:"profile"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("розбір /api/plan: %v", err)
	}
	if doc.Profile == nil {
		t.Fatal("профілю немає у відповіді")
	}
	if len(doc.Profile.Series) != 3 {
		t.Fatalf("рядів мало бути 3, маємо %d", len(doc.Profile.Series))
	}
	if len(doc.Profile.Points) < 12 {
		t.Fatalf("точок мало бути щонайменше 12, маємо %d", len(doc.Profile.Points))
	}
	if doc.Profile.StepMonths != 1 {
		t.Errorf("на короткому горизонті крок мав лишитись місячним, маємо %d", doc.Profile.StepMonths)
	}

	var sum float64
	for i := 0; i < 12; i++ {
		sum += doc.Profile.Points[i].Net
	}
	avg := sum / 12

	_, body = do(t, "GET", srv.URL+"/api/summary", "")
	var s struct {
		PlanProvidesUAH float64 `json:"plan_provides_uah"`
	}
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("розбір зведення: %v", err)
	}
	if diff := avg - s.PlanProvidesUAH; diff > 0.01 || diff < -0.01 {
		t.Errorf("середнє профілю %.2f ≠ plan_provides_uah %.2f", avg, s.PlanProvidesUAH)
	}
}

// Колонка «дає ₴/міс» приїжджає з бекенда, а не рахується в браузері:
// інакше періодичність, індексація, курс і частка в портфель були б
// означені вдруге — у JS, — і розійшлися б із плиткою при першій же правці.
func TestPlanFlowRowProvides(t *testing.T) {
	srv, _ := testServer(t)

	if resp, body := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"40000.00","cadence":"month","from_date":"2020-01-01","invest_pct":"40"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання потоку: %d %s", resp.StatusCode, body)
	}

	_, body := do(t, "GET", srv.URL+"/api/plan/flows", "")
	var rows []struct {
		ProvidesUAH float64 `json:"provides_uah"`
		GrossUAH    float64 `json:"gross_uah"`
	}
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("розбір списку: %v (%s)", err, body)
	}
	if len(rows) != 1 {
		t.Fatalf("мав бути 1 рядок, маємо %d", len(rows))
	}
	// 40 000 щомісяця, у портфель іде 40% → 16 000 ₴/міс; брутто — 40 000.
	if rows[0].ProvidesUAH != 16000 {
		t.Errorf("provides_uah: хотіли 16000, маємо %v", rows[0].ProvidesUAH)
	}
	if rows[0].GrossUAH != 40000 {
		t.Errorf("gross_uah: хотіли 40000, маємо %v", rows[0].GrossUAH)
	}

	// І та сама тотожність наскрізь через HTTP: сума колонки == плитка.
	_, body = do(t, "GET", srv.URL+"/api/summary", "")
	var sum struct {
		PlanProvidesUAH float64 `json:"plan_provides_uah"`
	}
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("розбір зведення: %v", err)
	}
	if sum.PlanProvidesUAH != rows[0].ProvidesUAH {
		t.Errorf("плитка %v ≠ сума колонки %v", sum.PlanProvidesUAH, rows[0].ProvidesUAH)
	}
}

// «Такого id немає» — це 404, а не 400: доти обидва випадки віддавали один
// код, і клієнт не міг відрізнити власну дурницю від рядка, видаленого в
// сусідній вкладці. Видалення при цьому взагалі мовчало — 204 на будь-який
// id, тобто «видалено» звучало однаково і коли видаляти було нічого.
func TestPlanMissingIDIsNotFound(t *testing.T) {
	srv, _ := testServer(t)

	flow := `{"name":"Зарплата","kind":"income","amount":"1.00","cadence":"month","from_date":"2026-09-01"}`
	action := `{"date":"2027-01-01","type":"set_shares","usd_share_pct":"10"}`
	for _, c := range []struct {
		method, path, body string
	}{
		{"PUT", "/api/plan/flows/999", flow},
		{"DELETE", "/api/plan/flows/999", ""},
		{"PUT", "/api/plan/actions/999", action},
		{"DELETE", "/api/plan/actions/999", ""},
	} {
		if resp, body := do(t, c.method, srv.URL+c.path, c.body); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s: мали 404, маємо %d %s", c.method, c.path, resp.StatusCode, body)
		}
	}
}

// Індексація складається щороку на весь горизонт, тож описка в один
// порядок робить ціль «досягнутою» мовчки.
func TestPlanFlowGrowthBounds(t *testing.T) {
	srv, _ := testServer(t)
	body := func(g string) string {
		return `{"name":"Зарплата","kind":"income","amount":"100.00","cadence":"month",` +
			`"from_date":"2026-09-01","growth_pct":"` + g + `"}`
	}
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows", body("1000")); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("індексація 1000%%/рік мала дати 400, маємо %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows", body("-100")); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("індексація -100%% мала дати 400, маємо %d %s", resp.StatusCode, b)
	}
	// Межі включно з нею самою лишаються прохідними.
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows", body("100")); resp.StatusCode != http.StatusCreated {
		t.Errorf("індексація 100%%/рік мала пройти, маємо %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows", body("-50")); resp.StatusCode != http.StatusCreated {
		t.Errorf("спадний потік -50%%/рік мав пройти, маємо %d %s", resp.StatusCode, b)
	}
}

// GET /api/plan збирає потоки, дії, терміни інструментів і віхи одним
// документом для стрічки часу. Головне тут — що ВСЕ це доходить до
// відповіді, а не лише summary, і що суми конвертовані в гривню (валютний
// потік теж має віддати amount_uah).
func TestPlanTimelineDoc(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)

	do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"40000.00","cadence":"month","from_date":"2026-06-01","invest_pct":"40"}`)
	do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Бонус","kind":"income","amount":"500.00","currency":"USD","cadence":"once","from_date":"2026-09-01"}`)
	do(t, "POST", srv.URL+"/api/plan/actions",
		`{"date":"2026-12-15","type":"lock","amount":"50000.00","rate_pct":"20","months":24,"name":"MilTech"}`)
	if resp, body := do(t, "PUT", srv.URL+"/api/settings",
		`{"goal_amount_uah":"1000000","goal_date":"2030-01-01"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put settings: %d %s", resp.StatusCode, body)
	}
	if resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, body)
	}

	resp, body := do(t, "GET", srv.URL+"/api/plan", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("plan: %d %s", resp.StatusCode, body)
	}
	var got struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Flows []struct {
			Name      string  `json:"name"`
			AmountUAH float64 `json:"amount_uah"`
		} `json:"flows"`
		Actions []struct {
			Type      string  `json:"type"`
			Name      string  `json:"name"`
			AmountUAH float64 `json:"amount_uah"`
		} `json:"actions"`
		Instruments []struct {
			Kind  string `json:"kind"`
			Label string `json:"label"`
			To    string `json:"to"`
		} `json:"instruments"`
		Milestones []struct {
			Label string `json:"label"`
		} `json:"milestones"`
		Curve []struct {
			Date string  `json:"date"`
			Plan float64 `json:"plan"`
		} `json:"curve"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("plan не парситься: %v — %s", err, body)
	}

	if got.From == "" || got.To == "" {
		t.Errorf("from/to мають бути заповнені: %+v", got)
	}
	if len(got.Flows) != 2 {
		t.Fatalf("очікували 2 потоки, маємо %d", len(got.Flows))
	}
	byName := map[string]float64{}
	for _, f := range got.Flows {
		byName[f.Name] = f.AmountUAH
	}
	if byName["Зарплата"] != 16000 { // 40000 × 40%
		t.Errorf("зарплата мала дати 16000 у гривні, маємо %v", byName["Зарплата"])
	}
	if byName["Бонус"] <= 0 {
		t.Errorf("доларовий потік мав конвертуватись у додатну гривневу суму, маємо %v", byName["Бонус"])
	}

	if len(got.Actions) != 1 || got.Actions[0].Name != "MilTech" || got.Actions[0].AmountUAH != 50000 {
		t.Errorf("дія lock не дійшла до документа: %+v", got.Actions)
	}

	foundBond := false
	for _, i := range got.Instruments {
		if i.Kind == "bond" && i.Label == "UA4000227748" {
			foundBond = true
		}
	}
	if !foundBond {
		t.Errorf("погашення утримуваної облігації мало зʼявитись серед instruments: %+v", got.Instruments)
	}

	labels := map[string]bool{}
	for _, m := range got.Milestones {
		labels[m.Label] = true
	}
	if !labels["сьогодні"] || !labels["дедлайн цілі"] {
		t.Errorf("віхи «сьогодні» й «дедлайн цілі» мали бути присутні: %+v", got.Milestones)
	}

	if len(got.Curve) < 2 {
		t.Fatalf("крива мала прийти хоча б із двома точками, маємо %d", len(got.Curve))
	}
	if got.Curve[0].Date != got.From {
		t.Errorf("перша точка кривої мала бути на сьогодні (%s), маємо %s", got.From, got.Curve[0].Date)
	}
}
