package api

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// planExpenseRow — той самий рядок, що бачить форма правки. Розбирається
// сирою структурою навмисно: якби тест читав його через типи пакета,
// перейменоване JSON-поле пройшло б повз нього, а UI зламався б.
type planExpenseRow struct {
	ID     int64 `json:"id"`
	Name   string
	Amount struct {
		Amount   string
		Currency string
	}
	DueDate  string `json:"due_date"`
	PaidFrom string `json:"paid_from"`
	PaidDate string `json:"paid_date"`
	Place    string
	Note     string
}

func listPlanExpenses(t *testing.T, srv string) []planExpenseRow {
	t.Helper()
	resp, b := do(t, "GET", srv+"/api/plan/expenses", "")
	if resp.StatusCode != 200 {
		t.Fatalf("список планових витрат: %d %s", resp.StatusCode, b)
	}
	var out []planExpenseRow
	if err := json.Unmarshal([]byte(b), &out); err != nil {
		t.Fatalf("розбір списку: %v %s", err, b)
	}
	return out
}

func TestPlanExpensesCRUDEndpoints(t *testing.T) {
	srv, _ := testServer(t)

	resp, b := do(t, "POST", srv.URL+"/api/plan/expenses",
		`{"name":"Котел","amount":"30000.00","due_date":"2026-11-15","paid_from":"card","place":"ПУМБ"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("створення: %d %s", resp.StatusCode, b)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(b), &created); err != nil || created.ID == 0 {
		t.Fatalf("id витрати: %v %s", err, b)
	}

	got := listPlanExpenses(t, srv.URL)
	if len(got) != 1 {
		t.Fatalf("у списку %d витрат", len(got))
	}
	if got[0].Amount.Amount != "30000.00" || got[0].Amount.Currency != "UAH" {
		t.Errorf("сума поїхала: %+v", got[0].Amount)
	}
	// Контур за замовчуванням не вигадується клієнтом: порожнє поле мусить
	// повернутись як card, бо саме там живе «все інше».
	if resp, b := do(t, "POST", srv.URL+"/api/plan/expenses",
		`{"name":"Страховка","amount":"4200.00","due_date":"2026-10-14"}`); resp.StatusCode != 201 {
		t.Fatalf("витрата без контуру: %d %s", resp.StatusCode, b)
	}
	got = listPlanExpenses(t, srv.URL)
	if len(got) != 2 || got[0].Name != "Страховка" || got[0].PaidFrom != "card" {
		t.Fatalf("порядок або типовий контур поїхали: %+v", got)
	}

	// Правка — повна заміна, і саме нею робиться «сплачено».
	id := strconv.FormatInt(created.ID, 10)
	if resp, b := do(t, "PUT", srv.URL+"/api/plan/expenses/"+id,
		`{"name":"Котел","amount":"30000.00","due_date":"2026-11-15","paid_from":"card",`+
			`"place":"ПУМБ","paid_date":"2026-11-14"}`); resp.StatusCode != 204 {
		t.Fatalf("правка: %d %s", resp.StatusCode, b)
	}
	got = listPlanExpenses(t, srv.URL)
	if got[1].PaidDate != "2026-11-14" {
		t.Errorf("позначка «сплачено» не збереглась: %+v", got[1])
	}

	if resp, b := do(t, "DELETE", srv.URL+"/api/plan/expenses/"+id, ""); resp.StatusCode != 204 {
		t.Fatalf("видалення: %d %s", resp.StatusCode, b)
	}
	if got = listPlanExpenses(t, srv.URL); len(got) != 1 {
		t.Fatalf("після видалення лишилось %d", len(got))
	}
	// Видалення неіснуючої — 404, а не тиша.
	if resp, _ := do(t, "DELETE", srv.URL+"/api/plan/expenses/"+id, ""); resp.StatusCode != 404 {
		t.Errorf("повторне видалення дало %d, очікували 404", resp.StatusCode)
	}
}

// Кожна відмова мусить називати причину українською: сира помилка CHECK-а
// або парсера дати нічого не каже тому, хто заповнює форму.
func TestPlanExpenseRejectsBadInput(t *testing.T) {
	srv, _ := testServer(t)
	for _, c := range []struct {
		what, body, want string
	}{
		{"без назви", `{"name":" ","amount":"100.00","due_date":"2026-11-15"}`, "без назви"},
		{"нульова сума", `{"name":"Котел","amount":"0","due_date":"2026-11-15"}`, "більшою за нуль"},
		{"відʼємна сума", `{"name":"Котел","amount":"-100.00","due_date":"2026-11-15"}`, "більшою за нуль"},
		{"без дати", `{"name":"Котел","amount":"100.00"}`, "дата витрати"},
		{"зіпсована дата", `{"name":"Котел","amount":"100.00","due_date":"15.11.2026"}`, "дата витрати"},
		{"чужий контур", `{"name":"Котел","amount":"100.00","due_date":"2026-11-15","paid_from":"wallet"}`, "невідомий контур"},
		{"зіпсована дата сплати", `{"name":"Котел","amount":"100.00","due_date":"2026-11-15","paid_date":"вчора"}`, "дата сплати"},
	} {
		resp, b := do(t, "POST", srv.URL+"/api/plan/expenses", c.body)
		if resp.StatusCode != 400 {
			t.Errorf("%s: %d, очікували 400 (%s)", c.what, resp.StatusCode, b)
			continue
		}
		if !strings.Contains(b, c.want) {
			t.Errorf("%s: причина %q не містить %q", c.what, b, c.want)
		}
	}
}

// Пастка повної заміни: тіло, зібране з рядка задля однієї лише позначки,
// мусить донести решту полів. Кнопка «Сплачено» в UI робить саме це, і
// загублене note виглядало б як тихе псування даних.
func TestPlanExpenseMarkPaidKeepsFields(t *testing.T) {
	srv, _ := testServer(t)
	resp, b := do(t, "POST", srv.URL+"/api/plan/expenses",
		`{"name":"Котел","amount":"30000.00","currency":"USD","due_date":"2026-11-15",`+
			`"paid_from":"plan","place":"ПУМБ","note":"з розстрочкою"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("створення: %d %s", resp.StatusCode, b)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(b), &created); err != nil {
		t.Fatal(err)
	}
	row := listPlanExpenses(t, srv.URL)[0]

	// Рівно те, що робить UI: узяти рядок як є й додати дату.
	body, err := json.Marshal(map[string]string{
		"name": row.Name, "amount": row.Amount.Amount, "currency": row.Amount.Currency,
		"due_date": row.DueDate, "paid_from": row.PaidFrom, "place": row.Place,
		"note": row.Note, "paid_date": "2026-11-14",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp, b := do(t, "PUT", srv.URL+"/api/plan/expenses/"+
		strconv.FormatInt(created.ID, 10), string(body)); resp.StatusCode != 204 {
		t.Fatalf("позначка «сплачено»: %d %s", resp.StatusCode, b)
	}

	got := listPlanExpenses(t, srv.URL)[0]
	if got.PaidDate != "2026-11-14" {
		t.Errorf("позначка не лягла: %+v", got)
	}
	if got.Name != "Котел" || got.Amount.Amount != "30000.00" ||
		got.Amount.Currency != "USD" || got.DueDate != "2026-11-15" ||
		got.PaidFrom != "plan" || got.Place != "ПУМБ" || got.Note != "з розстрочкою" {
		t.Errorf("повна заміна витерла поля: %+v", got)
	}
}
