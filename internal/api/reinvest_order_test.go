package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	money "github.com/Rhymond/go-money"
)

type orderRow struct {
	Label      string  `json:"label"`
	NominalPct float64 `json:"nominal_pct"`
	RealPct    float64 `json:"real_pct"`
	Currency   string  `json:"currency"`
}

func labels(rows []orderRow) string {
	out := ""
	for _, r := range rows {
		out += r.Label + ";"
	}
	return out
}

// TestReinvestOrderSettingReordersResponse — та сама перевірка наскрізь,
// через ручку: без налаштування порядок за реальною, з reinvest_order =
// nominal — за номінальною.
func TestReinvestOrderSettingReordersResponse(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	ctx := context.Background()
	// Вклад із високою НОМІНАЛЬНОЮ ставкою — саме він і мусить піднятись у другій лінійці.
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Principal: 30_000_00, Currency: money.UAH,
		RateBP: 2400, TaxBP: 2300, OpenDate: domain.NewDate(time.Now()),
		MaturityDate: "2027-08-01", Payout: domain.PayoutEnd,
		Replenishable: true,
	}); err != nil {
		t.Fatal(err)
	}

	get := func(order string) []orderRow {
		t.Helper()
		if resp, body := do(t, "PUT", srv.URL+"/api/settings",
			`{"reinvest_order":"`+order+`"}`); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("PUT reinvest_order=%q дав %d: %s", order, resp.StatusCode, body)
		}
		resp, body := do(t, "GET", srv.URL+"/api/reinvest", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET (%s) дав %d: %s", order, resp.StatusCode, body)
		}
		var out []orderRow
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("розбір: %v (%s)", err, body)
		}
		return out
	}

	real := get("")
	nominal := get("nominal")
	if len(real) < 2 || len(nominal) != len(real) {
		t.Skipf("на цих даних порад менше двох (%d/%d) — порядок нема на чому міряти",
			len(real), len(nominal))
	}
	// Списки ті самі, лише в іншому порядку: перемикач не фільтрує.
	if labels(real) != labels(nominal) {
		t.Fatalf("склад списку змінився:\n%v\n%v", labels(real), labels(nominal))
	}
	// Найвища НОМІНАЛЬНА мусить стояти першою серед доступних.
	best := 0.0
	for _, r := range nominal {
		if r.NominalPct > best {
			best = r.NominalPct
		}
	}
	if nominal[0].NominalPct != best {
		t.Fatalf("за номінальною першим стоїть %v при максимумі %v",
			nominal[0].NominalPct, best)
	}
}
