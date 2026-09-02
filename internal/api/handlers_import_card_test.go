package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

const cardCSV = "Дата,Опис,MCC,Сума,Залишок\n" +
	"2026-08-01,Зарплата,,25000.00,-5000.00\n" +
	"2026-08-03,Покупка АТБ,5411,-1200.00,-6200.00\n" +
	"2026-08-05,Зняття готівки,6011,-2000.00,-8200.00\n" +
	"2026-08-20,Покупка Rozetka,5732,-3000.00,-11200.00\n"

// Виписка картки: надходження й готівка стають рухами боргу, покупки —
// лише сумою витрат, а звірка пишеться ЛИШЕ з введеною сумою виписки і
// з залишком останнього рядка. Другий прогін не подвоює нічого.
func TestImportCardStatement(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	importSince(t, st, "2026-01-01")
	cardID, err := st.AddDebt(ctx, domain.Debt{Name: "ПУМБ", Kind: domain.DebtCard,
		Currency: money.UAH, StatementDay: 30, LimitAmount: 20000000})
	if err != nil {
		t.Fatal(err)
	}
	// Без картки картковий вид у профілі не зберігається.
	if resp, _ := do(t, "PUT", srv.URL+"/api/import/profiles/card",
		`{"format":"csv","header":1,"col_date":0,"col_op":1,"col_debit":3,"col_balance":4,"col_mcc":2,
		"ops":"Зарплата = card_in"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("профіль із card_in без картки мав дати 400, дав %d", resp.StatusCode)
	}
	saveProfile(t, srv.URL, "card", `{"format":"csv","header":1,"col_date":0,"col_op":1,
		"col_debit":3,"col_balance":4,"col_mcc":2,"debt_id":`+itoa(int(cardID))+`,
		"ops":"Зарплата = card_in\nЗняття готівки = card_cash\nПокупка = card_out"}`)

	type out struct {
		importOut
		Card struct {
			BalanceRaw  string `json:"balance_raw"`
			BalanceDate string `json:"balance_date"`
			Spend       []struct {
				Month   string  `json:"month"`
				OutUAH  float64 `json:"out_uah"`
				InUAH   float64 `json:"in_uah"`
				CashUAH float64 `json:"cash_uah"`
			} `json:"spend"`
			CashSincePrevMark float64 `json:"cash_since_prev_mark"`
			MarkWritten       bool    `json:"mark_written"`
			MarkNote          string  `json:"mark_note"`
		} `json:"card"`
	}
	parse := func(body string) out {
		var o out
		if err := json.Unmarshal([]byte(body), &o); err != nil {
			t.Fatalf("%v: %s", err, body)
		}
		return o
	}

	// Перегляд: два рухи нові (зарплата й готівка), покупки враховані як
	// витрати, звірки немає — сума виписки не введена.
	resp, body := postCSV(t, srv.URL+"/api/import?profile=card&dry=1", cardCSV)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("перегляд: %d %s", resp.StatusCode, body)
	}
	dry := parse(body)
	if dry.New != 2 || dry.Imported != 0 || len(dry.Skipped) != 0 {
		t.Errorf("перегляд: нових %d, записано %d, пропущено %d — чекали 2/0/0",
			dry.New, dry.Imported, len(dry.Skipped))
	}
	if len(dry.Card.Spend) != 1 || dry.Card.Spend[0].OutUAH != 4200 ||
		dry.Card.Spend[0].InUAH != 25000 || dry.Card.Spend[0].CashUAH != 2000 {
		t.Errorf("витрати місяця %+v, чекали out 4 200 / in 25 000 / cash 2 000", dry.Card.Spend)
	}
	if dry.Card.BalanceDate != "2026-08-20" || dry.Card.BalanceRaw == "" || dry.Card.MarkWritten {
		t.Errorf("залишок мав бути з останнього рядка й без звірки: %+v", dry.Card)
	}

	// Імпорт без суми виписки — рухи пишуться, звірка ні.
	resp, body = postCSV(t, srv.URL+"/api/import?profile=card", cardCSV)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("імпорт: %d %s", resp.StatusCode, body)
	}
	got := parse(body)
	if got.Imported != 2 || got.Card.MarkWritten {
		t.Errorf("імпорт без mark_due: записано %d, звірка %v — чекали 2 і false", got.Imported, got.Card.MarkWritten)
	}
	ops, _ := st.ListDebtOps(ctx)
	if len(ops) != 2 || ops[0].Kind != domain.DebtOpPayment || ops[1].Kind != domain.DebtOpCash {
		t.Errorf("рухи боргу %+v, чекали платіж і готівку", ops)
	}

	// Повторний імпорт із сумою виписки: рухів не додає, звірку пише з
	// залишком файлу й готівкою після попередньої звірки (її не було —
	// уся готівка файлу). Водяний знак після імпорту стоїть на сьогодні
	// й відсік би серпень цілком — посуваємо його, як це робить людина
	// полем «враховувати зміни від».
	importSince(t, st, "2026-01-01")
	resp, body = postCSV(t, srv.URL+"/api/import?profile=card&mark_due=1500.50", cardCSV)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("повторний імпорт: %d %s", resp.StatusCode, body)
	}
	again := parse(body)
	if again.New != 0 || !again.Card.MarkWritten {
		t.Errorf("повторно: нових %d, звірка %v (%s)", again.New, again.Card.MarkWritten, again.Card.MarkNote)
	}
	marks, _ := st.ListDebtMarks(ctx)
	if len(marks) != 1 || marks[0].Date != "2026-08-20" || marks[0].Balance != -1120000 ||
		marks[0].StatementDue != 150050 || marks[0].NonGrace != 200000 {
		t.Errorf("звірка %+v, чекали 2026-08-20 / −11 200 / 1 500,50 / готівка 2 000", marks)
	}
	// Третій раз на ту саму дату — звірка не дублюється.
	importSince(t, st, "2026-01-01")
	_, body = postCSV(t, srv.URL+"/api/import?profile=card&mark_due=1500.50", cardCSV)
	if third := parse(body); third.Card.MarkWritten || third.Imported != 0 {
		t.Errorf("звірка на ту саму дату записалась удруге: %+v", third.Card)
	}
	// Знак залишку можна перевизначити руками (mark_balance).
	if _, err := st.AddDebt(ctx, domain.Debt{Name: "друга", Kind: domain.DebtCard, Currency: money.UAH}); err != nil {
		t.Fatal(err)
	}
}
