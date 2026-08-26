package api

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
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

// Дохід портфеля на профілі НЕ сміє потрапити в net.
//
// net — це план, і на ньому стоїть рівність із плиткою «План дає»
// (TestPlanProfileMatchesProvides). Купон ОВДП чи дивіденд фонду — гроші,
// які портфель заробляє сам, а не які ти вносиш; змішавши їх, ми або
// зламали б ту рівність, або вдавали б, що купон це твій внесок.
func TestPlanProfileKeepsPortfolioIncomeApartFromNet(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	// Саме ЛОТ, а не лише довідник: купон нараховується на те, чим
	// володієш, тож без покупки профіль лишився б без доходу й тест
	// перевіряв би порожнечу.
	if resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":50,"price_per_bond":"995.00","buy_date":"2026-07-01"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, body)
	}

	if resp, body := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"40000.00","cadence":"month","from_date":"2020-01-01"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("потік: %d %s", resp.StatusCode, body)
	}

	_, body := do(t, "GET", srv.URL+"/api/plan", "")
	var doc struct {
		Profile *struct {
			Points []struct {
				Net    float64 `json:"net"`
				Income float64 `json:"income"`
			} `json:"points"`
			Events []struct {
				Kind      string  `json:"kind"`
				AmountUAH float64 `json:"amount_uah"`
			} `json:"events"`
		} `json:"profile"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("розбір /api/plan: %v", err)
	}
	if doc.Profile == nil {
		t.Fatal("профілю немає")
	}

	// Кожна точка: net — рівно план, без домішки доходу.
	for i, p := range doc.Profile.Points {
		if p.Net != 40000 {
			t.Fatalf("точка %d: net = %.2f, а мав лишитись планом 40000", i, p.Net)
		}
	}
	// І сам дохід десь таки є — інакше тест перевіряв би порожнечу.
	if !hasPositive(doc.Profile.Points) {
		t.Error("портфель із паперами мав дати хоч один місяць доходу")
	}
	// Погашення в дохід не входять — вони окремими подіями.
	for _, e := range doc.Profile.Events {
		if e.AmountUAH <= 0 {
			t.Errorf("подія %s без суми", e.Kind)
		}
	}
}

func hasPositive(points []struct {
	Net    float64 `json:"net"`
	Income float64 `json:"income"`
}) bool {
	for _, p := range points {
		if p.Income > 0 {
			return true
		}
	}
	return false
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

// Три різні відповіді на «скільки це в гривні» — і кожна про своє.
//
// AmountUAH — сама сума за курсом; MonthlyUAH — стала ставка на місяць
// (сума ÷ період); NextMonthUAH — найближчий місяць плану. Розділити їх
// довелось саме через разову виплату: у колонці «₴/міс» вона показувала
// дванадцяту частину премії, тобто число, яке не дорівнює ні сумі, ні
// тому, що прийде.
func TestPlanFlowRowMonthlyAndNextMonth(t *testing.T) {
	srv, _ := testServer(t)
	now := time.Now()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	nextMonth := first.AddDate(0, 1, 0).Format("2006-01-02")
	// Поза дванадцятимісячним вікном: там provides/gross обнуляються, а
	// amount_uah зобов'язаний вижити.
	farFuture := first.AddDate(2, 1, 0).Format("2006-01-02")

	for _, body := range []string{
		`{"name":"місячна","kind":"income","amount":"40000.00","cadence":"month","from_date":"2020-01-01","invest_pct":"40"}`,
		`{"name":"квартальна","kind":"income","amount":"30000.00","cadence":"quarter","from_date":"2020-01-01","invest_pct":"100"}`,
		`{"name":"річна","kind":"income","amount":"120000.00","cadence":"year","from_date":"2020-01-01","invest_pct":"100"}`,
		`{"name":"разова скоро","kind":"income","amount":"5000.00","cadence":"once","from_date":"` + nextMonth + `","invest_pct":"100"}`,
		`{"name":"разова нескоро","kind":"income","amount":"7000.00","cadence":"once","from_date":"` + farFuture + `","invest_pct":"100"}`,
	} {
		if resp, got := do(t, "POST", srv.URL+"/api/plan/flows", body); resp.StatusCode != http.StatusCreated {
			t.Fatalf("додавання потоку: %d %s", resp.StatusCode, got)
		}
	}

	_, body := do(t, "GET", srv.URL+"/api/plan/flows", "")
	var rows []struct {
		Name            string  `json:"name"`
		AmountUAH       float64 `json:"amount_uah"`
		MonthlyUAH      float64 `json:"monthly_uah"`
		MonthlyGrossUAH float64 `json:"monthly_gross_uah"`
		NextMonthUAH    float64 `json:"next_month_uah"`
		GrossUAH        float64 `json:"gross_uah"`
	}
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("розбір списку: %v (%s)", err, body)
	}
	byName := map[string]int{}
	for i, r := range rows {
		byName[r.Name] = i
	}

	for _, c := range []struct {
		name                            string
		amount, monthly, gross, nextMon float64
	}{
		// 40 000 щомісяця, 40% у портфель. Брутто тут дорівнює сумі лише
		// тому, що період місячний.
		{"місячна", 40000, 16000, 40000, 16000},
		// 30 000 щокварталу — це 10 000 щомісяця, і брутто теж 10 000:
		// «щомісяця» ділиться на період ДО частки, інакше множення
		// «повне × частка = щомісяця» в рядку не сходилось би. А наступного
		// місяця приходить увесь платіж, тобто 30 000.
		{"квартальна", 30000, 10000, 10000, 30000},
		{"річна", 120000, 10000, 10000, 120000},
		// Разова: «щомісяця» в неї немає взагалі, а наступного місяця
		// приходить уся сума.
		{"разова скоро", 5000, 0, 0, 5000},
		{"разова нескоро", 7000, 0, 0, 0},
	} {
		i, ok := byName[c.name]
		if !ok {
			t.Fatalf("рядка «%s» немає у відповіді", c.name)
		}
		r := rows[i]
		if r.AmountUAH != c.amount || r.MonthlyUAH != c.monthly ||
			r.MonthlyGrossUAH != c.gross || r.NextMonthUAH != c.nextMon {
			t.Errorf("%s: маємо сума=%v щомісяця=%v брутто=%v наступний=%v, чекали %v/%v/%v/%v",
				c.name, r.AmountUAH, r.MonthlyUAH, r.MonthlyGrossUAH, r.NextMonthUAH,
				c.amount, c.monthly, c.gross, c.nextMon)
		}
	}

	// Головне, заради чого amount_uah окреме поле: разова ПОЗА вікном має
	// нульовий gross_uah, тож gross×12 у браузері показав би, що премії
	// немає взагалі.
	far := rows[byName["разова нескоро"]]
	if far.GrossUAH != 0 {
		t.Errorf("разова поза вікном мала дати gross_uah 0, маємо %v", far.GrossUAH)
	}
	if far.AmountUAH != 7000 {
		t.Errorf("а її справжня сума мала лишитись 7000, маємо %v", far.AmountUAH)
	}
}

// Множення в рядку мусить сходитись: «повне ₴/міс» × частка = «щомісяця».
// Саме заради цієї перевірки очима колонки й розводились, тож вона варта
// власного тесту, а не приміток у сусідньому.
func TestPlanFlowRowMonthlyMatchesShare(t *testing.T) {
	srv, _ := testServer(t)
	for _, body := range []string{
		`{"name":"десята","kind":"income","amount":"2000.00","cadence":"month","from_date":"2020-01-01","invest_pct":"10"}`,
		`{"name":"третина кварталу","kind":"income","amount":"9000.00","cadence":"quarter","from_date":"2020-01-01","invest_pct":"25"}`,
		`{"name":"витрата","kind":"expense","amount":"1200.00","cadence":"year","from_date":"2020-01-01","invest_pct":"100"}`,
	} {
		if resp, got := do(t, "POST", srv.URL+"/api/plan/flows", body); resp.StatusCode != http.StatusCreated {
			t.Fatalf("додавання потоку: %d %s", resp.StatusCode, got)
		}
	}
	_, body := do(t, "GET", srv.URL+"/api/plan/flows", "")
	var rows []struct {
		Name            string  `json:"name"`
		InvestPct       float64 `json:"invest_pct"`
		MonthlyUAH      float64 `json:"monthly_uah"`
		MonthlyGrossUAH float64 `json:"monthly_gross_uah"`
	}
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("розбір списку: %v (%s)", err, body)
	}
	if len(rows) != 3 {
		t.Fatalf("мало бути 3 рядки, маємо %d", len(rows))
	}
	for _, r := range rows {
		want := r.MonthlyGrossUAH * r.InvestPct / 100
		if math.Abs(want-r.MonthlyUAH) > 0.005 {
			t.Errorf("%s: %v × %v%% = %v, а «щомісяця» каже %v",
				r.Name, r.MonthlyGrossUAH, r.InvestPct, want, r.MonthlyUAH)
		}
	}
}

// Закритий потік не дає нічого «щомісяця».
//
// Асиметрія з датою ПОЧАТКУ навмисна: потік, що стартує за пів року, свою
// ставку має (і колонка «З» каже, коли вона почнеться), а закритий «⇗» —
// уже ні. Без цього стара й нова зарплати підсумувались би разом, і
// «щомісячний дохід» показав би обидві.
func TestPlanFlowMonthlyIgnoresClosedFlows(t *testing.T) {
	srv, _ := testServer(t)
	now := time.Now()
	past := now.AddDate(0, -3, 0).Format("2006-01-02")
	future := now.AddDate(1, 0, 0).Format("2006-01-02")

	for _, body := range []string{
		`{"name":"стара","kind":"income","amount":"30000.00","cadence":"month","from_date":"2020-01-01","until_date":"` + past + `","invest_pct":"100"}`,
		`{"name":"нова","kind":"income","amount":"40000.00","cadence":"month","from_date":"` + past + `","invest_pct":"100"}`,
		`{"name":"майбутня","kind":"income","amount":"50000.00","cadence":"month","from_date":"` + future + `","invest_pct":"100"}`,
	} {
		if resp, got := do(t, "POST", srv.URL+"/api/plan/flows", body); resp.StatusCode != http.StatusCreated {
			t.Fatalf("додавання потоку: %d %s", resp.StatusCode, got)
		}
	}
	_, body := do(t, "GET", srv.URL+"/api/plan/flows", "")
	var rows []struct {
		Name       string  `json:"name"`
		MonthlyUAH float64 `json:"monthly_uah"`
		AmountUAH  float64 `json:"amount_uah"`
	}
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("розбір списку: %v (%s)", err, body)
	}
	want := map[string]float64{"стара": 0, "нова": 40000, "майбутня": 50000}
	for _, r := range rows {
		if r.MonthlyUAH != want[r.Name] {
			t.Errorf("%s: «щомісяця» мало бути %v, маємо %v", r.Name, want[r.Name], r.MonthlyUAH)
		}
		// Сума закритого потоку лишається видимою — рядок і далі про щось
		// розповідає, просто вже не про майбутнє.
		if r.AmountUAH == 0 {
			t.Errorf("%s: сума за курсом не мала обнулятись", r.Name)
		}
	}
}

// Призначення має лише витрата.
//
// Доти форма показувала селект обом видам, а проєкція читала його рівно для
// відʼємних місячних сум — тобто на доході поле не робило нічого, зате
// список малював під рядком пігулку «переказ, а не витрата». Застосунок
// стверджував рух, якого не було в жодному його числі.
//
// Тихе стирання тут не годиться так само, як мовчазний нуль: людина вибрала
// рахунок і мала б право думати, що вибір записався.
func TestPlanFlowDestOnlyForExpense(t *testing.T) {
	srv, _ := testServer(t)

	if resp, body := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Бонус","kind":"income","amount":"250.00","currency":"USD","cadence":"once",`+
			`"from_date":"2026-09-01","dest":"npf:1"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("призначення на доході мало дати 400, маємо %d %s", resp.StatusCode, body)
	}
	// Та сама перевірка на правці: PUT тут повна заміна рядка, і пройти повз
	// неї було б рівно тим самим станом, лише через інші двері.
	if resp, body := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Внесок","kind":"expense","amount":"1000.00","cadence":"month",`+
			`"from_date":"2026-09-01","dest":"npf:1"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("призначення на витраті мало пройти: %d %s", resp.StatusCode, body)
	}
	if resp, body := do(t, "PUT", srv.URL+"/api/plan/flows/1",
		`{"name":"Внесок","kind":"income","amount":"1000.00","cadence":"month",`+
			`"from_date":"2026-09-01","dest":"npf:1"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("правка на дохід із призначенням мала дати 400, маємо %d %s", resp.StatusCode, body)
	}
	// Дохід БЕЗ призначення — звичайний рядок, і жодного dest у ньому.
	if resp, body := do(t, "PUT", srv.URL+"/api/plan/flows/1",
		`{"name":"Внесок","kind":"income","amount":"1000.00","cadence":"month",`+
			`"from_date":"2026-09-01"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("дохід без призначення мав пройти: %d %s", resp.StatusCode, body)
	}
	if _, body := do(t, "GET", srv.URL+"/api/plan/flows", ""); strings.Contains(body, `"dest"`) {
		t.Errorf("dest лишився на доході: %s", body)
	}
}

// Вичерпаний потік позначається бекендом, а не вгадується двома датами в
// браузері. Дванадцять нулів попереду однаково дає й потік, який почнеться
// на третій рік, — і він не завершений, а ще не почався.
func TestPlanFlowExpired(t *testing.T) {
	today := domain.Date("2026-08-24")
	cases := []struct {
		name string
		flow store.PlanFlow
		want bool
	}{
		{"дата «до» позаду", store.PlanFlow{
			Cadence: "month", FromDate: "2025-01-17", UntilDate: "2026-08-17"}, true},
		{"дата «до» цього місяця — уперед платежів уже немає", store.PlanFlow{
			Cadence: "month", FromDate: "2025-01-17", UntilDate: "2026-08-31"}, true},
		{"дата «до» наступного місяця", store.PlanFlow{
			Cadence: "month", FromDate: "2025-01-17", UntilDate: "2026-09-30"}, false},
		{"разова, чий місяць минув", store.PlanFlow{
			Cadence: "once", FromDate: "2026-08-10"}, true},
		{"разова попереду", store.PlanFlow{
			Cadence: "once", FromDate: "2026-09-01", UntilDate: "2026-09-30"}, false},
		{"безстроковий щомісячний", store.PlanFlow{
			Cadence: "month", FromDate: "2026-01-15"}, false},
		{"почнеться аж за два роки — не завершений, а ще не почався", store.PlanFlow{
			Cadence: "month", FromDate: "2028-09-01"}, false},
	}
	for _, c := range cases {
		if got := planFlowExpired(c.flow, today); got != c.want {
			t.Errorf("%s: expired=%v, чекали %v", c.name, got, c.want)
		}
	}
}
