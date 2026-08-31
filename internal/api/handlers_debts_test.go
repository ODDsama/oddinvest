package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// did — id боргу рядком для шляху й тіла запиту.
func did(n int64) string { return strconv.FormatInt(n, 10) }

// addDebt заводить борг і повертає його id.
func addDebt(t *testing.T, url, body string) int64 {
	t.Helper()
	resp, out := do(t, "POST", url+"/api/debts", body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/debts: %d %s", resp.StatusCode, out)
	}
	var got struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	return got.ID
}

func TestDebtsAPICRUD(t *testing.T) {
	srv, _ := testServer(t)

	card := addDebt(t, srv.URL, `{"name":"ПУМБ ВсеМожу","kind":"card","currency":"UAH",
		"limit":"200000","statement_day":"30","apr_pct":"47.88","apr_overdue_pct":"62",
		"min_payment_pct":"3","late_fee":"100","opened_date":"2024-05-01"}`)
	inCard := addDebt(t, srv.URL, `{"name":"Холодильник","kind":"installment","currency":"UAH",
		"card_id":"`+did(card)+`","principal":"30000","payments_total":"9",
		"first_payment_date":"2026-09-30","fee_month_pct":"1.99"}`)

	resp, out := do(t, "GET", srv.URL+"/api/debts", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/debts: %d %s", resp.StatusCode, out)
	}
	// Відсотки повертаються ВІДСОТКАМИ, а не базисними пунктами: форма
	// показує те саме, що ввела людина.
	if !strings.Contains(out, `"apr_pct":47.88`) || !strings.Contains(out, `"apr_overdue_pct":62`) {
		t.Errorf("ставки картки не повернулись відсотками: %s", out)
	}
	if !strings.Contains(out, `"fee_month_pct":1.99`) {
		t.Errorf("комісія розстрочки не повернулась: %s", out)
	}
	if !strings.Contains(out, `"statement_day":30`) {
		t.Errorf("розрахункова дата не повернулась: %s", out)
	}

	if resp, out = do(t, "PUT", srv.URL+"/api/debts/"+did(card),
		`{"name":"ПУМБ ВсеМожу","kind":"card","currency":"UAH","limit":"250000",
		 "statement_day":"30","apr_pct":"47.88","min_payment_pct":"3"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT картки: %d %s", resp.StatusCode, out)
	}

	// Картку з привʼязаною розстрочкою видалити не можна — і це 400 з
	// причиною, а не 500.
	if resp, out = do(t, "DELETE", srv.URL+"/api/debts/"+did(card), ""); resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(out, "розстрочок") {
		t.Fatalf("DELETE картки з розстрочкою: %d %s", resp.StatusCode, out)
	}
	if resp, out = do(t, "DELETE", srv.URL+"/api/debts/"+did(inCard), ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE розстрочки: %d %s", resp.StatusCode, out)
	}
	if resp, out = do(t, "DELETE", srv.URL+"/api/debts/"+did(card), ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE картки: %d %s", resp.StatusCode, out)
	}
}

// Поєднання, представимі в таблиці й безглузді за суттю, мусить відхиляти
// API — саме тому checkDebtShape живе тут, а не в CHECK міграції: звідти
// прийшов би текст рушія замість речення для людини.
func TestDebtsAPIRejectsNonsense(t *testing.T) {
	srv, _ := testServer(t)

	card := addDebt(t, srv.URL, `{"name":"Картка","kind":"card","currency":"UAH",
		"statement_day":"30","apr_pct":"47.88"}`)
	inst := addDebt(t, srv.URL, `{"name":"Розстрочка","kind":"installment","currency":"UAH",
		"principal":"12000","payments_total":"12","first_payment_date":"2026-10-05"}`)

	for _, c := range []struct{ name, body, want string }{
		{"картка без розрахункової дати",
			`{"name":"К","kind":"card","currency":"UAH","apr_pct":"47"}`,
			"розрахункова дата"},
		// Спіймано на живих даних: власник поставив у «Погашено» дату, до
		// якої треба ВНЕСТИ, і борг зник з усіх чисел одразу — з черги, з
		// пільгового блоку, з обовʼязкових платежів місяця й із документа
		// стану. Мовчазне зникнення гірше за будь-яку помилку вводу.
		{"погашено наперед",
			`{"name":"К","kind":"card","currency":"UAH","statement_day":"30",
			  "apr_pct":"47.88","closed_date":"2099-01-01"}`,
			"наперед її не ставлять"},
		// Друга половина того самого випадку: картка збереглась без ставки,
		// і сторінка стала арифметикою ні про що — нуль у мінімалці, нуль у
		// ціні помилки й ОСТАННЄ місце в черзі для боргу під 47,88%.
		{"картка без ставки",
			`{"name":"К","kind":"card","currency":"UAH","statement_day":"30"}`,
			"потрібна ставка"},
		{"невідомий вид",
			`{"name":"К","kind":"loan","currency":"UAH"}`,
			"невідомий вид боргу"},
		{"розстрочка без графіка",
			`{"name":"Р","kind":"installment","currency":"UAH","principal":"1000"}`,
			"кількість платежів"},
		{"розстрочка без тіла",
			`{"name":"Р","kind":"installment","currency":"UAH","payments_total":"6",
			  "first_payment_date":"2026-10-05"}`,
			"тіло"},
		{"місяців без комісії більше, ніж платежів",
			`{"name":"Р","kind":"installment","currency":"UAH","principal":"1000",
			  "payments_total":"3","first_payment_date":"2026-10-05","fee_free_months":"6"}`,
			"без комісії більше"},
		{"картка всередині картки",
			`{"name":"К","kind":"card","currency":"UAH","statement_day":"10",
			  "card_id":"` + did(card) + `"}`,
			"всередині картки"},
		{"розстрочка всередині розстрочки",
			`{"name":"Р","kind":"installment","currency":"UAH","principal":"1000",
			  "payments_total":"3","first_payment_date":"2026-10-05","card_id":"` + did(inst) + `"}`,
			"не картка"},
		{"чужа валюта всередині картки",
			`{"name":"Р","kind":"installment","currency":"USD","principal":"1000",
			  "payments_total":"3","first_payment_date":"2026-10-05","card_id":"` + did(card) + `"}`,
			"двох одиниць"},
	} {
		resp, out := do(t, "POST", srv.URL+"/api/debts", c.body)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(out, c.want) {
			t.Errorf("%s: %d %s (чекали 400 з %q)", c.name, resp.StatusCode, out, c.want)
		}
	}
}

// Напрям руху несе ВИД, а не знак: відʼємна сума при вигляді «унесено»
// означала б те саме, що покупка, і два записи означали б одне.
func TestDebtOpsAPIDirectionIsKind(t *testing.T) {
	srv, _ := testServer(t)
	card := addDebt(t, srv.URL, `{"name":"Картка","kind":"card","currency":"UAH",
		"statement_day":"30","apr_pct":"47.88"}`)

	resp, out := do(t, "POST", srv.URL+"/api/debt-ops",
		`{"debt_id":"`+did(card)+`","date":"2026-08-05","kind":"payment","amount":"40000"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST руху: %d %s", resp.StatusCode, out)
	}
	for _, c := range []struct{ name, body, want string }{
		{"відʼємна сума",
			`{"debt_id":"` + did(card) + `","kind":"payment","amount":"-100"}`,
			"напрям задає вид"},
		{"нульова сума",
			`{"debt_id":"` + did(card) + `","kind":"draw","amount":"0"}`,
			"напрям задає вид"},
		{"невідомий вид руху",
			`{"debt_id":"` + did(card) + `","kind":"interest","amount":"100"}`,
			"невідомий вид руху"},
		{"без боргу",
			`{"kind":"payment","amount":"100"}`,
			"до якого боргу"},
		{"борг, якого немає",
			`{"debt_id":"999","kind":"payment","amount":"100"}`,
			"немає"},
	} {
		resp, out := do(t, "POST", srv.URL+"/api/debt-ops", c.body)
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(out, c.want) {
			t.Errorf("%s: %d %s (чекали 400 з %q)", c.name, resp.StatusCode, out, c.want)
		}
	}

	// Валюта руху береться з БОРГУ, а не з тіла запиту: рух під гривневою
	// карткою в доларах не означає нічого.
	resp, out = do(t, "GET", srv.URL+"/api/debt-ops", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(out, `"currency":"UAH"`) {
		t.Errorf("валюта руху: %d %s", resp.StatusCode, out)
	}
}

// Звірка: баланс знакозмінний, борг — ні; повторна на ту саму дату
// переписує попередню.
func TestDebtMarksAPI(t *testing.T) {
	srv, _ := testServer(t)
	card := addDebt(t, srv.URL, `{"name":"Картка","kind":"card","currency":"UAH",
		"statement_day":"30","apr_pct":"47.88"}`)

	// Мінус — використаний ліміт. Це нормальний стан половини місяця, а не
	// помилка вводу.
	resp, out := do(t, "POST", srv.URL+"/api/debt-marks",
		`{"debt_id":"`+did(card)+`","date":"2026-08-30","balance":"-3000",
		  "statement_due":"18400","non_grace":"5000"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST звірки: %d %s", resp.StatusCode, out)
	}
	// Плюс — власні гроші на картці, і це та сама колонка.
	if resp, out = do(t, "POST", srv.URL+"/api/debt-marks",
		`{"debt_id":"`+did(card)+`","date":"2026-08-30","balance":"12000",
		  "statement_due":"18400","note":"перечитав"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("повторна звірка: %d %s", resp.StatusCode, out)
	}

	resp, out = do(t, "GET", srv.URL+"/api/debt-marks", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatal(out)
	}
	var marks []struct {
		Balance struct {
			Amount string `json:"amount"`
		} `json:"balance"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal([]byte(out), &marks); err != nil {
		t.Fatal(err)
	}
	if len(marks) != 1 {
		t.Fatalf("звірок на одну дату %d, чекали 1: %s", len(marks), out)
	}
	if marks[0].Balance.Amount != "12000.00" || marks[0].Note != "перечитав" {
		t.Errorf("повторна звірка не переписала першу: %+v", marks[0])
	}

	if resp, out = do(t, "POST", srv.URL+"/api/debt-marks",
		`{"debt_id":"`+did(card)+`","date":"2026-08-31","balance":"0",
		  "statement_due":"1000","non_grace":"5000"}`); resp.StatusCode != http.StatusBadRequest ||
		!strings.Contains(out, "поза пільговим більше") {
		t.Errorf("непільгова частина більша за всю суму: %d %s", resp.StatusCode, out)
	}
}
