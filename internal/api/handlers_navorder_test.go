package api

import (
	"context"
	"net/http"
	"testing"
)

// Порядок рядків: порожньо за замовчуванням, кругообіг записи-читання і
// відмова на поламаній формі БЕЗ запису.
//
// Остання половина й є та, заради якої тест написаний. Валідація, що
// падає після першого запису, лишає в базі половину набору — і саме
// такий випадок уже ловили в PUT /api/settings.
func TestNavOrder(t *testing.T) {
	srv, st := testServer(t)

	resp, body := do(t, "GET", srv.URL+"/api/nav-order", "")
	if resp.StatusCode != http.StatusOK || body != "{}\n" {
		t.Fatalf("порожній порядок: %d %q", resp.StatusCode, body)
	}

	const want = `{"plan":["goal","debts","inflow"],"policy":["mix","strategy"]}`
	resp, body = do(t, "PUT", srv.URL+"/api/nav-order", want)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("запис порядку: %d %s", resp.StatusCode, body)
	}
	resp, body = do(t, "GET", srv.URL+"/api/nav-order", "")
	if resp.StatusCode != http.StatusOK || body != want+"\n" {
		t.Fatalf("читання порядку: %d %q, хочемо %q", resp.StatusCode, body, want)
	}

	long := "x"
	for len(long) <= navOrderMaxLen {
		long += "x"
	}
	many := `{"plan":[`
	for i := 0; i <= navOrderMaxIDs; i++ {
		if i > 0 {
			many += ","
		}
		many += `"a` + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `"`
	}
	many += `]}`

	for name, bad := range map[string]string{
		"не масив":     `{"plan":"debts"}`,
		"не об'єкт":    `["debts"]`,
		"порожній id":  `{"plan":[""]}`,
		"задовгий id":  `{"plan":["` + long + `"]}`,
		"чужі символи": `{"plan":["debts","../etc"]}`,
		"повтор":       `{"plan":["debts","debts"]}`,
		"забагато":     many,
	} {
		resp, body := do(t, "PUT", srv.URL+"/api/nav-order", bad)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d %s, хочемо 400", name, resp.StatusCode, body)
		}
	}

	// Жодна з відмов не торкнулась збереженого.
	got, err := st.GetAppState(context.Background(), navOrderKey)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("після відмов у базі %q, хочемо %q", got, want)
	}
}

// Зіпсований JSON у базі — порожній порядок, а не 500. Список малюється
// ДО того, як стане відомо, чи вподобання читається; 500 тут погасив би
// застосунок цілком заради рядка, ціна якого — одне «Скинути».
func TestNavOrderBrokenBlob(t *testing.T) {
	srv, st := testServer(t)
	if err := st.SetAppState(context.Background(), navOrderKey, "{зламано"); err != nil {
		t.Fatal(err)
	}
	resp, body := do(t, "GET", srv.URL+"/api/nav-order", "")
	if resp.StatusCode != http.StatusOK || body != "{}\n" {
		t.Fatalf("зіпсований порядок: %d %q", resp.StatusCode, body)
	}
}
