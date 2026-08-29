package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Витрати в доларах — і ціль резерву, що їде разом із курсом.
//
// Головне, що тут перевіряється, — НЕ саме множення, а те, що воно
// доходить до цілі подушки. Поле monthly_expenses_uah читають вісім
// місць, і всі вісім вважають число гривнями; якби переклад загубився,
// ціль у 6 місяців вийшла б у 6 × 1 500 = 9 000 ₴ замість 9 000 $ — тобто
// подушка виглядала б зібраною вп'ятдесятеро раніше, ніж вона є.
//
// Другий рядок тесту — про те, що ціль ЖИВА: та сама сума в доларах при
// іншому курсі дає іншу гривневу ціль без жодної правки налаштувань.
// Саме заради цього валюта й заводилась: вписана руками гривня стоїть на
// місці й тихо занижується, доки курс іде вгору.
func TestExpensesInUSDConvertAtTodayRate(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	today := domain.NewDate(time.Now())

	if err := st.SaveRate(ctx, "USD", 40_0000, today); err != nil {
		t.Fatal(err)
	}
	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_expenses":"1500","monthly_expenses_currency":"USD","reserve_target_months":"6"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, b)
	}

	read := func() (expUAH, targetUAH float64) {
		t.Helper()
		var got struct {
			Settings struct {
				Expenses    *float64 `json:"monthly_expenses"`
				Currency    string   `json:"monthly_expenses_currency"`
				ExpensesUAH *float64 `json:"monthly_expenses_uah"`
			} `json:"settings"`
			Reserve *struct {
				TargetUAH float64 `json:"target_uah"`
			} `json:"reserve"`
		}
		_, body := do(t, "GET", srv.URL+"/api/summary", "")
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("summary: %v: %s", err, body)
		}
		// Введене лишається введеним: гривневе поле виводиться з нього, а
		// не заміщає його. Інакше форма показувала б користувачеві гривні
		// там, куди він вписав долари.
		if got.Settings.Expenses == nil || *got.Settings.Expenses != 1500 ||
			got.Settings.Currency != "USD" {
			t.Fatalf("введені витрати не збереглись: %+v", got.Settings)
		}
		if got.Settings.ExpensesUAH == nil {
			t.Fatal("monthly_expenses_uah порожній — переклад не дійшов до документа")
		}
		if got.Reserve == nil {
			t.Fatal("картки резерву немає — цілі рахувати нема від чого")
		}
		return *got.Settings.ExpensesUAH, got.Reserve.TargetUAH
	}

	exp, target := read()
	if math.Abs(exp-60_000) > 0.01 {
		t.Errorf("витрати в гривні = %.2f, а $1 500 × 40 = 60 000", exp)
	}
	if math.Abs(target-360_000) > 0.01 {
		t.Errorf("ціль резерву = %.2f, а 6 × 60 000 = 360 000", target)
	}

	// Курс зрушив — ціль поїхала за ним, налаштування не чіпали.
	if err := st.SaveRate(ctx, "USD", 50_0000, today); err != nil {
		t.Fatal(err)
	}
	exp, target = read()
	if math.Abs(exp-75_000) > 0.01 {
		t.Errorf("після зміни курсу витрати = %.2f, чекали 75 000", exp)
	}
	if math.Abs(target-450_000) > 0.01 {
		t.Errorf("після зміни курсу ціль = %.2f, чекали 450 000", target)
	}
}

// База, старша за міграцію 0038: у ній лежить лише monthly_expenses_uah.
//
// Спадковий ключ мусить і далі працювати сам по собі — інакше оновлення
// застосунку мовчки вимкнуло б резерв тому, хто про це не просив. Це та
// сама роль, що в goal_*_uah після 0008, і перевіряти її треба саме
// тестом: у коді вона виглядає як відсутність рядка.
func TestLegacyExpensesKeyStillFeedsReserve(t *testing.T) {
	srv, _ := testServer(t)

	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_expenses_uah":"25000","reserve_target_months":"4"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, b)
	}
	var got struct {
		Reserve *struct {
			TargetUAH          float64 `json:"target_uah"`
			MonthlyExpensesUAH float64 `json:"monthly_expenses_uah"`
		} `json:"reserve"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	if got.Reserve == nil {
		t.Fatal("спадковий ключ перестав заводити картку резерву")
	}
	if math.Abs(got.Reserve.MonthlyExpensesUAH-25_000) > 0.01 {
		t.Errorf("витрати = %.2f, чекали 25 000 зі спадкового ключа",
			got.Reserve.MonthlyExpensesUAH)
	}
	if math.Abs(got.Reserve.TargetUAH-100_000) > 0.01 {
		t.Errorf("ціль резерву = %.2f, а 4 × 25 000 = 100 000", got.Reserve.TargetUAH)
	}
}
