package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// Повне коло КРУД, як у решти сутностей: сутність, яку можна створити,
// мусить піддаватись правці й видаленню (CLAUDE.md §2).
func TestPlanBuysCRUDOverAPI(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	url := srv.URL

	resp, body := do(t, "POST", url+"/api/plan/buys",
		`{"kind":"bond","ref":"UA4000227748","qty":3,"broker":"mono"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("створення: %d %s", resp.StatusCode, body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}

	_, body = do(t, "GET", url+"/api/plan/buys", "")
	var rows []planBuyRow
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Qty != 3 || rows[0].Ref != "UA4000227748" {
		t.Fatalf("список не той: %s", body)
	}

	resp, body = do(t, "PUT", url+"/api/plan/buys/"+strconv.FormatInt(created.ID, 10),
		`{"kind":"deposit","ref":"privat","amount":"5000","currency":"UAH","months":12,"rate_pct":"14.5","is_reserve":true}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("правка: %d %s", resp.StatusCode, body)
	}
	_, body = do(t, "GET", url+"/api/plan/buys", "")
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatal(err)
	}
	// Гроші й відсоток їздять рядком і мусять повернутись тими самими:
	// коло «форма → база → форма» не має права округлювати.
	if rows[0].Kind != "deposit" || rows[0].Amount != "5000.00" ||
		rows[0].RatePct != "14.50" || !rows[0].IsReserve || rows[0].Months != 12 {
		t.Fatalf("правка не доїхала: %s", body)
	}

	if resp, body = do(t, "DELETE", url+"/api/plan/buys/"+strconv.FormatInt(created.ID, 10), ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("видалення: %d %s", resp.StatusCode, body)
	}
	// 404, а не 400: клієнт мусить відрізнити «я надіслав дурницю» від
	// «рядок уже видалили в іншій вкладці» (див. writeStoreErr).
	if resp, _ = do(t, "DELETE", url+"/api/plan/buys/"+strconv.FormatInt(created.ID, 10), ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("повторне видалення дало %d, хочемо 404", resp.StatusCode)
	}
}

// Перевірки форми, кожна з яких боронить конкретне число далі за течією.
func TestPlanBuysValidation(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	url := srv.URL

	cases := []struct{ why, body string }{
		{"невідомий вид", `{"kind":"gold","ref":"x","qty":1}`},
		{"папір без ISIN", `{"kind":"bond","qty":1}`},
		{"нульова кількість", `{"kind":"bond","ref":"UA4000227748","qty":0}`},
		{"ціна вручну для паперу", `{"kind":"bond","ref":"UA4000227748","qty":1,"unit_price":"990"}`},
		{"вклад без банку", `{"kind":"deposit","amount":"5000","months":12}`},
		{"вклад без строку", `{"kind":"deposit","ref":"privat","amount":"5000"}`},
		{"вклад без суми", `{"kind":"deposit","ref":"privat","months":12}`},
		{"внесок без рахунку", `{"kind":"npf","ref":"Династія","amount":"4000"}`},
		{"крива дата", `{"kind":"bond","ref":"UA4000227748","qty":1,"buy_date":"колись"}`},
	}
	for _, c := range cases {
		resp, body := do(t, "POST", url+"/api/plan/buys", c.body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: дало %d (%s), хочемо 400", c.why, resp.StatusCode, body)
		}
	}

	// Минула дата — НЕ помилка: це прострочений намір, і рахується він як
	// «зараз». Інакше рядок, заведений учора, ламав би екран сьогодні.
	resp, body := do(t, "POST", url+"/api/plan/buys",
		`{"kind":"bond","ref":"UA4000227748","qty":1,"buy_date":"2020-01-01"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("минула дата дала %d (%s), а мала лишитись простроченим наміром", resp.StatusCode, body)
	}
}
