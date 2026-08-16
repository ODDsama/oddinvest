package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// thisMonth / monthPlus — місяці відносно сьогодні, як їх бачить бекенд.
// Через ту саму арифметику над роком і місяцем, що monthKeyAt: тест, який
// рахував би місяці інакше, ловив би не помилку, а розбіжність із собою.
func monthPlus(n int) string {
	now := time.Now()
	t := now.Year()*12 + int(now.Month()) - 1 + n
	return fmt.Sprintf("%04d-%02d", t/12, t%12+1)
}

// Чеклист розгортається з плану: завів зарплату — і в поточному місяці
// з'явився рядок, який можна відмітити одним тиком.
func TestPlanExpectedComesFromTheFlow(t *testing.T) {
	srv, _ := testServer(t)

	// Потік почався давно, щоб він точно платив і в минулих місяцях вікна.
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"40000.00","cadence":"month",`+
			`"from_date":"2024-01-17","invest_pct":"25"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання потоку: %d %s", resp.StatusCode, b)
	}

	doc := planDoc(t, srv.URL)
	now := monthPlus(0)
	var cur *expectedReceipt
	for i := range doc.Expected {
		if doc.Expected[i].Month == now {
			cur = &doc.Expected[i]
		}
	}
	if cur == nil {
		t.Fatalf("у чеклисті немає поточного місяця: %+v", doc.Expected)
	}
	// Сума ВАЛОВА — та, що прийде на руки, а не та, що дійде до портфеля.
	if cur.Amount.Amount != "40000.00" {
		t.Errorf("планова сума мала бути валова 40000.00, маємо %q", cur.Amount.Amount)
	}
	if cur.PlanUAH != 10000 {
		t.Errorf("plan_uah мав бути 10000 (25%% від 40000), маємо %v", cur.PlanUAH)
	}
	if cur.DueDate != now+"-17" {
		t.Errorf("платіжний день мав бути 17-те: %q", cur.DueDate)
	}
	if cur.Receipt != nil {
		t.Errorf("щойно заведений потік не мав бути відмічений: %+v", cur.Receipt)
	}
	// Вікно — рік назад і рік уперед, тобто 25 місяців на щомісячному потоці.
	if len(doc.Expected) != 25 {
		t.Errorf("вікно чеклиста мало дати 25 рядків, маємо %d", len(doc.Expected))
	}
}

// Відмітка на МАЙБУТНІЙ місяць заміщає план — і це видно наскрізь, у
// plan_provides_uah. Саме заради цього фіча й потрібна: «відпускні прийшли
// наперед, далі два місяці нуль» перестає бути знанням у голові.
func TestFutureMarkLowersPlanProvides(t *testing.T) {
	srv, _ := testServer(t)
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"12000.00","cadence":"month",`+
			`"from_date":"2024-01-17"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання потоку: %d %s", resp.StatusCode, b)
	}
	id := planFlowID(t, srv.URL)

	before := planProvides(t, srv.URL)
	if before != 12000 {
		t.Fatalf("передумова: план мав давати 12000, маємо %v", before)
	}

	// Два наступні місяці — нуль.
	for _, m := range []string{monthPlus(1), monthPlus(2)} {
		body := fmt.Sprintf(`{"flow_id":%d,"month":%q,"amount":"0"}`, id, m)
		if resp, b := do(t, "POST", srv.URL+"/api/plan/receipts", body); resp.StatusCode != http.StatusCreated {
			t.Fatalf("відмітка %s: %d %s", m, resp.StatusCode, b)
		}
	}

	// Вікно 12 місяців, два з них нульові → 12000 × 10/12 = 10000.
	if got := planProvides(t, srv.URL); got != 10000 {
		t.Errorf("після двох нулів план мав давати 10000, маємо %v", got)
	}
	// І та сама тотожність наскрізь: сума колонки == плитка.
	if rows := planFlowRows(t, srv.URL); len(rows) != 1 || rows[0].ProvidesUAH != 10000 {
		t.Errorf("колонка «дає ₴/міс» розійшлася з плиткою: %+v", rows)
	}

	// Знята відмітка повертає все як було — відмітки нічого не ламають
	// незворотно.
	recs := planReceiptIDs(t, srv.URL)
	for _, rid := range recs {
		if resp, b := do(t, "DELETE", fmt.Sprintf("%s/api/plan/receipts/%d", srv.URL, rid), ""); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("зняття відмітки %d: %d %s", rid, resp.StatusCode, b)
		}
	}
	if got := planProvides(t, srv.URL); got != before {
		t.Errorf("після зняття відміток план мав повернутись до %v, маємо %v", before, got)
	}
}

// Валідація. Кожен рядок тут — окремий спосіб мовчки зіпсувати числа, і
// саме тому всі вони 400, а не «прийнято й якось порахувалось».
func TestPlanReceiptValidation(t *testing.T) {
	srv, _ := testServer(t)
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"40000.00","cadence":"month",`+
			`"from_date":"2024-01-17"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання потоку: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Комуналка","kind":"expense","amount":"3000.00","cadence":"month",`+
			`"from_date":"2024-01-05"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання витрати: %d %s", resp.StatusCode, b)
	}
	rows := planFlowRows(t, srv.URL)
	var income, expense int64
	for _, r := range rows {
		if r.Kind == "income" {
			income = r.ID
		} else {
			expense = r.ID
		}
	}

	now, past, future := monthPlus(0), monthPlus(-1), monthPlus(1)
	for _, c := range []struct {
		why, body string
		want      int
	}{
		{"кривий місяць", fmt.Sprintf(`{"flow_id":%d,"month":"2026-13","amount":"1"}`, income), 400},
		{"місяць із днем", fmt.Sprintf(`{"flow_id":%d,"month":"2026-08-31","amount":"1"}`, income), 400},
		{"від'ємна сума", fmt.Sprintf(`{"flow_id":%d,"month":%q,"amount":"-5"}`, income, now), 400},
		{"порожня сума", fmt.Sprintf(`{"flow_id":%d,"month":%q,"amount":""}`, income, now), 400},
		{"чужа валюта", fmt.Sprintf(`{"flow_id":%d,"month":%q,"amount":"1","currency":"USD"}`, income, now), 400},
		{"витрата", fmt.Sprintf(`{"flow_id":%d,"month":%q,"amount":"1"}`, expense, now), 400},
		{"неіснуючий потік", fmt.Sprintf(`{"flow_id":999,"month":%q,"amount":"1"}`, now), 404},
		{"«інше» без назви", fmt.Sprintf(`{"flow_id":0,"month":%q,"amount":"1"}`, past), 400},
		{"«інше» в майбутньому", fmt.Sprintf(`{"flow_id":0,"month":%q,"name":"Премія","amount":"1"}`, future), 400},
		// А це — прохідне: нуль легальний і є суттю фічі.
		{"нуль", fmt.Sprintf(`{"flow_id":%d,"month":%q,"amount":"0"}`, income, now), 201},
		// Другий раз на той самий місяць — 409, а не 400: це гонка з
		// сусідньою вкладкою, і лікується вона перезавантаженням.
		{"дубль", fmt.Sprintf(`{"flow_id":%d,"month":%q,"amount":"0"}`, income, now), 409},
		{"«інше» за минулий", fmt.Sprintf(`{"flow_id":0,"month":%q,"name":"Премія","amount":"1"}`, past), 201},
	} {
		resp, b := do(t, "POST", srv.URL+"/api/plan/receipts", c.body)
		if resp.StatusCode != c.want {
			t.Errorf("%s: мали %d, маємо %d %s", c.why, c.want, resp.StatusCode, b)
		}
	}

	if resp, b := do(t, "PUT", srv.URL+"/api/plan/receipts/999",
		fmt.Sprintf(`{"flow_id":%d,"month":%q,"amount":"1"}`, income, now)); resp.StatusCode != http.StatusNotFound {
		t.Errorf("правка неіснуючої відмітки мала дати 404, маємо %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "DELETE", srv.URL+"/api/plan/receipts/999", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("видалення неіснуючої відмітки мало дати 404, маємо %d %s", resp.StatusCode, b)
	}
}

// --- дрібні читачі відповіді, щоб тести вище лишались про суть ---

type planDocResp struct {
	Expected []expectedReceipt  `json:"expected"`
	Receipts []receiptRow       `json:"receipts"`
	History  []planHistoryPoint `json:"history"`
}

func planDoc(t *testing.T, base string) planDocResp {
	t.Helper()
	_, body := do(t, "GET", base+"/api/plan", "")
	var doc planDocResp
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("розбір /api/plan: %v (%s)", err, body)
	}
	return doc
}

type planFlowIDRow struct {
	ID          int64   `json:"id"`
	Kind        string  `json:"kind"`
	ProvidesUAH float64 `json:"provides_uah"`
}

func planFlowRows(t *testing.T, base string) []planFlowIDRow {
	t.Helper()
	_, body := do(t, "GET", base+"/api/plan/flows", "")
	var rows []planFlowIDRow
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("розбір потоків: %v (%s)", err, body)
	}
	return rows
}

func planFlowID(t *testing.T, base string) int64 {
	t.Helper()
	rows := planFlowRows(t, base)
	if len(rows) == 0 {
		t.Fatal("потоків немає")
	}
	return rows[0].ID
}

func planReceiptIDs(t *testing.T, base string) []int64 {
	t.Helper()
	_, body := do(t, "GET", base+"/api/plan/receipts", "")
	var rows []receiptRow
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("розбір відміток: %v (%s)", err, body)
	}
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func planProvides(t *testing.T, base string) float64 {
	t.Helper()
	_, body := do(t, "GET", base+"/api/summary", "")
	var sum struct {
		PlanProvidesUAH float64 `json:"plan_provides_uah"`
	}
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("розбір зведення: %v", err)
	}
	return sum.PlanProvidesUAH
}
