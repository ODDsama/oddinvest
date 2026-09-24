package api

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
)

// --- стеля цілей: чиста арифметика ---

// --- черга в розкладці надходження ---

// Подушка забирає своє ПЕРШОЮ, цілі — з того, що лишилось.
//
// Порядок не стилістичний: аварія не має дати й може статись завтра, а річ,
// на яку збирають, дату має. Пустити цілі поперед подушки означало б
// платити за передбачуване з грошей, відкладених на непередбачуване.
func TestAllocateReserveBeforeGoals(t *testing.T) {
	srv, _ := testServer(t)

	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_expenses":"10000","monthly_expenses_currency":"UAH","reserve_target_months":"6",
		  "reserve_fill_share_pct":"30","goals_fill_share_pct":"30","target_bonds_pct":"100"}`); resp.StatusCode >= 300 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, b)
	}
	// План доходу — без нього стелі нема від чого рахувати.
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"50000.00","cadence":"month",
		  "from_date":"2026-01-01","invest_pct":"100"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("потік: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/goals",
		`{"name":"Авто","amount":"500000","currency":"UAH"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("ціль: %d %s", resp.StatusCode, b)
	}

	var plan struct {
		Reserve *struct {
			AmountUAH float64 `json:"amount_uah"`
		} `json:"reserve"`
		Goals []struct {
			Name      string  `json:"name"`
			AmountUAH float64 `json:"amount_uah"`
		} `json:"goals"`
		GoalsUAH float64 `json:"goals_uah"`
		AvailUAH float64 `json:"avail_uah"`
		Note     string  `json:"note"`
	}
	_, body := do(t, "POST", srv.URL+"/api/allocate", `{"amount":"20000"}`)
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		t.Fatalf("allocate: %v: %s", err, body)
	}

	// Стеля подушки — 30% від 50 000 = 15 000; стеля цілей — стільки ж, але
	// різати їй уже нема з чого: подушка забрала 15 000 із 20 000, лишилось
	// 5 000, і саме вони йдуть у ціль.
	if plan.Reserve == nil || math.Abs(plan.Reserve.AmountUAH-15_000) > 0.01 {
		t.Fatalf("подушка взяла %+v, чекали 15 000", plan.Reserve)
	}
	if len(plan.Goals) != 1 || math.Abs(plan.Goals[0].AmountUAH-5_000) > 0.01 {
		t.Fatalf("цілі взяли %+v, чекали 5 000 залишку", plan.Goals)
	}
	if plan.Goals[0].Name != "Авто" {
		t.Errorf("вирізка не названа поіменно: %q", plan.Goals[0].Name)
	}
	// Усе розібрано — і нота мусить назвати ОБОХ, а не саму подушку.
	if plan.AvailUAH > 0.01 {
		t.Errorf("до паперів дійшло %.2f, а мало нічого", plan.AvailUAH)
	}
	if plan.Note == "" || !strings.Contains(plan.Note, "цілі") {
		t.Errorf("нота не називає цілей: %q", plan.Note)
	}
}

// Політика «звідки наповнювати» в цілей СВОЯ.
//
// Подушку багато хто свідомо наповнює лише зарплатою, а цілі — усім, що
// прийде. Спільний ключ віддав би обом найсуворішу з двох політик, а
// зникла вирізка без пояснення читається як поломка — тому перевіряється
// ще й причина.
func TestGoalsFillFromIsIndependentOfReserve(t *testing.T) {
	srv, _ := testServer(t)

	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"goals_fill_share_pct":"50","goals_fill_from":"plan","target_bonds_pct":"100"}`); resp.StatusCode >= 300 {
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

	var plan struct {
		GoalsUAH     float64 `json:"goals_uah"`
		GoalsSkipWhy string  `json:"goals_skip_why"`
	}
	// Купон — це дохід ПОРТФЕЛЯ, а політика каже «лише плановий».
	_, body := do(t, "POST", srv.URL+"/api/allocate",
		`{"amount":"20000","source":"portfolio"}`)
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		t.Fatalf("allocate: %v: %s", err, body)
	}
	if plan.GoalsUAH > 0.01 {
		t.Errorf("цілі взяли %.2f з купона при політиці «лише планові»", plan.GoalsUAH)
	}
	if plan.GoalsSkipWhy == "" {
		t.Error("вирізка зникла мовчки — це читається як поломка, а не як рішення")
	}
}

// --- дозвіл конкретного надходження (0041) ---

// SOURCE_REF І Є ПРИЧИНА, ЧОМУ ДОЗВІЛ ЖИВЕ НЕ ЛИШЕ В СТЕЛІ МІСЯЦЯ.
//
// Стеля вже звужена (month_plan.plan_reserve_uah), але вона агрегатна:
// «цього місяця подушці належить 15 000» не знає, ЯКЕ саме надходження ти
// тримаєш у руках. Дозволені гроші могли прийти із зарплати, а розкладаєш
// ти оренду — і вирізки в неї бути не має.
//
// Наскрізно через справжній обробник навмисно: між дозволом і людиною
// стоять пошук потоку за посиланням, зведення з політикою й сама
// розкладка, і кожна з цих ланок уміє мовчки все дозволити.
func TestAllocateSourceRefRespectsFlowPermission(t *testing.T) {
	srv, _ := testServer(t)

	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_expenses":"10000","monthly_expenses_currency":"UAH","reserve_target_months":"6",
		  "reserve_fill_share_pct":"30","target_bonds_pct":"100"}`); resp.StatusCode >= 300 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, b)
	}
	// Зарплата подушці дозволена, оренда — ні. Стеля місяця при цьому
	// НЕ нульова: саме тому агрегатного числа тут не досить.
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"50000.00","cadence":"month",
		  "from_date":"2026-01-01","invest_pct":"100"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("зарплата: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Оренда","kind":"income","amount":"20000.00","cadence":"month",
		  "from_date":"2026-01-01","invest_pct":"100","uses":["invest"]}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("оренда: %d %s", resp.StatusCode, b)
	}

	var plan struct {
		Reserve *struct {
			AmountUAH float64 `json:"amount_uah"`
		} `json:"reserve"`
		ReserveSkipWhy string  `json:"reserve_skip_why"`
		AvailUAH       float64 `json:"avail_uah"`
	}
	get := func(bodyReq string) {
		plan.Reserve, plan.ReserveSkipWhy, plan.AvailUAH = nil, "", 0
		_, body := do(t, "POST", srv.URL+"/api/allocate", bodyReq)
		if err := json.Unmarshal([]byte(body), &plan); err != nil {
			t.Fatalf("allocate: %v: %s", err, body)
		}
	}

	// Без посилання — поведінка до появи поля: вирізка є.
	get(`{"amount":"10000"}`)
	if plan.Reserve == nil || plan.Reserve.AmountUAH <= 0 {
		t.Fatalf("без source_ref вирізки немає: %+v — тест перевіряв би не те", plan.Reserve)
	}
	before := plan.Reserve.AmountUAH

	// Зарплата — та сама вирізка: дозвіл у неї повний.
	get(`{"amount":"10000","source_ref":"flow:1"}`)
	if plan.Reserve == nil || math.Abs(plan.Reserve.AmountUAH-before) > 0.01 {
		t.Errorf("зарплата дала вирізку %+v, чекали ту саму %.2f", plan.Reserve, before)
	}

	// Оренда — вирізки немає, і причина називає САМЕ ЇЇ.
	get(`{"amount":"10000","source_ref":"flow:2"}`)
	if plan.Reserve != nil {
		t.Errorf("оренда дала вирізку %+v — вона подушці заборонена", plan.Reserve)
	}
	if !strings.Contains(plan.ReserveSkipWhy, "надходження") {
		t.Errorf("причина %q не про джерело: вона повела б у «Політику», де все правильно",
			plan.ReserveSkipWhy)
	}
	if math.Abs(plan.AvailUAH-10_000) > 0.01 {
		t.Errorf("до паперів дійшло %.2f, чекали всі 10 000", plan.AvailUAH)
	}
}

// Невідоме посилання — ПОМИЛКА, а не «обмежень немає».
//
// Мовчазний дефолт тут був би найгіршим виглядом збою: розкладка
// виглядала б звичайною й різала б подушку з грошей, яким це заборонено.
func TestAllocateUnknownSourceRefFails(t *testing.T) {
	srv, _ := testServer(t)

	if resp, _ := do(t, "POST", srv.URL+"/api/allocate",
		`{"amount":"1000","source_ref":"flow:404"}`); resp.StatusCode != http.StatusNotFound {
		t.Errorf("неіснуючий потік мав дати 404, маємо %d", resp.StatusCode)
	}
	if resp, _ := do(t, "POST", srv.URL+"/api/allocate",
		`{"amount":"1000","source_ref":"хтозна"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("сміття в посиланні мало дати 400, маємо %d", resp.StatusCode)
	}
}

// Кнопка «Розкласти» пише КНИЖКОВІ суми, хоч би в чому звітував документ.
//
// Модалка записувала подушку й цілі з amount_uah — числа, яке презентер
// уже переклав у валюту звітності, — під currency "UAH". У доларовому
// вигляді 15 000 ₴ ставали записом на ~340 ₴. Тепер поруч є book_uah
// (тег native): те саме число в гривні, якого переклад не чіпає, і пише
// саме його.
func TestAllocateCarriesBookAmountsInReportCurrency(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st) // курс USD 44.1234
	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_expenses":"10000","monthly_expenses_currency":"UAH","reserve_target_months":"6",
		  "reserve_fill_share_pct":"30","goals_fill_share_pct":"30","target_bonds_pct":"100",
		  "report_currency":"USD"}`); resp.StatusCode >= 300 {
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
	var plan struct {
		Reserve *struct {
			AmountUAH float64 `json:"amount_uah"`
			BookUAH   float64 `json:"book_uah"`
		} `json:"reserve"`
		Goals []struct {
			AmountUAH float64 `json:"amount_uah"`
			BookUAH   float64 `json:"book_uah"`
		} `json:"goals"`
	}
	_, body := do(t, "POST", srv.URL+"/api/allocate", `{"amount":"20000"}`)
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		t.Fatalf("allocate: %v: %s", err, body)
	}
	if plan.Reserve == nil || math.Abs(plan.Reserve.BookUAH-15_000) > 0.01 {
		t.Fatalf("подушка для запису %+v, чекали 15 000 ₴", plan.Reserve)
	}
	if math.Abs(plan.Reserve.AmountUAH-15_000) < 1 {
		t.Errorf("показна сума мала бути в доларах, а лишилась гривнею: %.2f", plan.Reserve.AmountUAH)
	}
	if len(plan.Goals) != 1 || math.Abs(plan.Goals[0].BookUAH-5_000) > 0.01 {
		t.Fatalf("ціль для запису %+v, чекали 5 000 ₴", plan.Goals)
	}
}
