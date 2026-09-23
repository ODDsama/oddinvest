package api

import (
	"encoding/json"
	"github.com/ODDsama/oddinvest/internal/engine"
	"net/http"
	"strings"
	"testing"
)

// --- з яких грошей ріже стеля подушки ---

// --- маршрут ---

// --- плановий дохід у маршруті ---

// --- наскрізь ---

// Плановий дохід доходить до маршруту через справжній обробник.
//
// Наскрізний навмисно, і з тієї самої причини, що й у купона: між planAhead
// і людиною стоять buildMonthPlan, підміна поточного місяця документом і
// домішування в incomeAhead, а кожен із цих кроків уміє віддати порожнечу,
// якої модульний тест не побачить.
func TestRouteEndpointSeesPlanIncome(t *testing.T) {
	srv, _ := testServer(t)
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"40000.00","cadence":"month",`+
			`"from_date":"2024-01-17","invest_pct":"50"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання потоку: %d %s", resp.StatusCode, b)
	}

	_, body := do(t, "GET", srv.URL+"/api/route", "")
	var got engine.RouteDoc
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("маршрут не розбирається: %v — %s", err, body)
	}
	var plan *engine.RouteLeg
	for i := range got.Legs {
		if got.Legs[i].Basis == engine.BasisPlan {
			plan = &got.Legs[i]
			break
		}
	}
	if plan == nil {
		t.Fatalf("планової ноги в маршруті немає: %s", body)
	}
	if plan.Broker != engine.NoBrokerLabel {
		t.Errorf("планова нога в брокера %q — планових грошей у брокера ще немає", plan.Broker)
	}
	if plan.Ref != "" {
		t.Errorf("планова нога несе ref %q — кнопка «Прийшло» писала б не в ту таблицю", plan.Ref)
	}
	if plan.InflowUAH.Major() <= 0 {
		t.Errorf("планова нога на %.2f ₴ — надходження без грошей не буває", plan.InflowUAH.Major())
	}
}

// Ключ доходить із PUT /api/settings і повертається назад тим самим.
func TestSettingsRoundTripsFillFrom(t *testing.T) {
	srv, _ := testServer(t)
	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"reserve_fill_from":"plan"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("запис: %d %s", resp.StatusCode, b)
	}
	_, body := do(t, "GET", srv.URL+"/api/settings", "")
	if !strings.Contains(body, `"reserve_fill_from":"plan"`) {
		t.Errorf("налаштування не повернулось: %s", body)
	}
	if resp, _ := do(t, "PUT", srv.URL+"/api/settings",
		`{"reserve_fill_from":"plann"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("описку прийнято зі статусом %d — подушку можна вимкнути непоміченим ключем",
			resp.StatusCode)
	}
}

// POST /api/allocate приймає джерело й тіло — і тіло в НАТИВНІЙ валюті.
//
// Гривневе число тут віддало б подушці рівно курс, і помітили б це лише на
// валютній нозі маршруту.
func TestAllocateEndpointTakesSourceAndPrincipal(t *testing.T) {
	srv, _ := testServer(t)
	if resp, b := do(t, "POST", srv.URL+"/api/allocate",
		`{"amount":"5000.00","currency":"UAH","source":"portfolio","principal":"3000.00"}`,
	); resp.StatusCode != http.StatusOK {
		t.Fatalf("розкладка: %d %s", resp.StatusCode, b)
	}
	if resp, _ := do(t, "POST", srv.URL+"/api/allocate",
		`{"amount":"5000.00","principal":"-1"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("відʼємне тіло прийнято зі статусом %d", resp.StatusCode)
	}
}

// --- дозвіл джерела в маршруті (0041) ---
