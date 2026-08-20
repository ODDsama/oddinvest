package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// Поповнення вкладу й точку ЧВОПА доти можна було лише додати й видалити:
// одруківку в сумі виправляли видаленням із повторним набором. Обидва PUT
// закривають цю дірку, і обидва тести перевіряють не лише «зберігає», а й
// що неіснуючий id більше не мовчить (404) — доти видалення чужого id
// поверталось успіхом.

func TestUpdateDepositTopup(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2026-01-10", Amount: 100000000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	body := `{"bank":"ПУМБ","currency":"UAH","principal":"100000.00","rate_pct":"16",` +
		`"open_date":"2026-01-15","maturity_date":"2027-01-15","payout":"end","replenishable":true}`
	resp, b := do(t, "POST", srv.URL+"/api/term-deposits", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("створення вкладу: %d %s", resp.StatusCode, b)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(b), &created); err != nil {
		t.Fatal(err)
	}
	dep := created.ID

	resp, b = do(t, "POST", srv.URL+"/api/term-deposits/"+i64(dep)+"/topups",
		`{"date":"2026-02-01","amount":"5000.00"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("поповнення: %d %s", resp.StatusCode, b)
	}
	var top struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(b), &top); err != nil {
		t.Fatal(err)
	}

	path := srv.URL + "/api/term-deposits/" + i64(dep) + "/topups/" + i64(top.ID)
	if resp, b = do(t, "PUT", path, `{"date":"2026-03-01","amount":"7500.00"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("правка поповнення: %d %s", resp.StatusCode, b)
	}
	_, b = do(t, "GET", srv.URL+"/api/term-deposits", "")
	if !strings.Contains(b, "7500.00") || strings.Contains(b, "5000.00") {
		t.Errorf("правка не відбилась у списку: %s", b)
	}

	// Неіснуюче поповнення — 404, а не тихий успіх.
	bad := srv.URL + "/api/term-deposits/" + i64(dep) + "/topups/99999"
	if resp, _ = do(t, "PUT", bad, `{"date":"2026-03-01","amount":"1.00"}`); resp.StatusCode != http.StatusNotFound {
		t.Errorf("правка чужого поповнення мала дати 404, маємо %d", resp.StatusCode)
	}
	if resp, _ = do(t, "DELETE", bad, ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("видалення чужого поповнення мало дати 404, маємо %d", resp.StatusCode)
	}
}

func TestUpdateNPFNavPoint(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	id, err := st.AddNPFAccount(ctx, domain.NPFAccount{Name: "ОТП Пенсія", Currency: "UAH"})
	if err != nil {
		t.Fatal(err)
	}
	resp, b := do(t, "POST", srv.URL+"/api/npf-nav",
		`{"npf_id":`+i64(id)+`,"points":[{"date":"2026-01-31","nav":"2.500000"},`+
			`{"date":"2026-02-28","nav":"2.600000"}]}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("вклеювання точок: %d %s", resp.StatusCode, b)
	}
	pts, err := st.ListNPFNav(ctx)
	if err != nil || len(pts) != 2 {
		t.Fatalf("очікували 2 точки, маємо %v (%v)", len(pts), err)
	}

	first := pts[0]
	path := srv.URL + "/api/npf-nav/" + i64(first.ID)
	if resp, b = do(t, "PUT", path, `{"date":"2026-01-31","nav":"2.550000"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("правка точки: %d %s", resp.StatusCode, b)
	}
	_, b = do(t, "GET", srv.URL+"/api/npf-nav", "")
	if !strings.Contains(b, "2.55") {
		t.Errorf("виправлена ЧВОПА не видно: %s", b)
	}

	// Перенесення на зайняту дату — конфлікт, а не «невірний ввід».
	if resp, _ = do(t, "PUT", path, `{"date":"2026-02-28","nav":"2.550000"}`); resp.StatusCode != http.StatusConflict {
		t.Errorf("зіткнення дат мало дати 409, маємо %d", resp.StatusCode)
	}
	if resp, _ = do(t, "PUT", srv.URL+"/api/npf-nav/99999", `{"date":"2026-05-05","nav":"1.0"}`); resp.StatusCode != http.StatusNotFound {
		t.Errorf("правка неіснуючої точки мала дати 404, маємо %d", resp.StatusCode)
	}
}

// Локальний перетворювач: itoa() у сусідньому тесті бере int, а всі id тут
// int64, і зводити їх приведенням означало б мовчки різати на 32 бітах.
func i64(n int64) string { return strconv.FormatInt(n, 10) }
