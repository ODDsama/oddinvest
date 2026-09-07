package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ODDsama/oddinvest/internal/store"
)

// Приховані рядки: порожньо за замовчуванням, кругообіг записи-читання,
// кирилиця з пробілами проходить і відмова на поламаній формі БЕЗ запису.
//
// Кирилиця тут не для повноти: рівно на ній і спіткнувся б позичений
// navOrderIDOK, а «fund:Inzhur MilTech» і «npf:ВПФ Династія» — саме ті
// рядки, заради яких ця ручка існує.
func TestHiddenRows(t *testing.T) {
	srv, st := testServer(t)

	resp, body := do(t, "GET", srv.URL+"/api/hidden-rows", "")
	if resp.StatusCode != http.StatusOK || body != "[]\n" {
		t.Fatalf("порожній список: %d %q", resp.StatusCode, body)
	}

	const want = `["fund:Inzhur Ocean","goal:3","npf:ВПФ Династія"]`
	resp, body = do(t, "PUT", srv.URL+"/api/hidden-rows", want)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("запис: %d %s", resp.StatusCode, body)
	}
	resp, body = do(t, "GET", srv.URL+"/api/hidden-rows", "")
	if resp.StatusCode != http.StatusOK || body != want+"\n" {
		t.Fatalf("читання: %d %q, хочемо %q", resp.StatusCode, body, want)
	}

	long := `"` + strings.Repeat("x", hiddenMaxLen+1) + `"`
	many := make([]string, 0, hiddenMaxIDs+1)
	for i := 0; i <= hiddenMaxIDs; i++ {
		many = append(many, `"fund:`+string(rune('a'+i%26))+string(rune('a'+i/26))+`"`)
	}

	for name, bad := range map[string]string{
		"не масив":        `{"fund":"x"}`,
		"не рядки":        `[3]`,
		"порожній id":     `[""]`,
		"задовгий id":     `[` + long + `]`,
		"керівний символ": `["fund:a\nb"]`,
		"повтор":          `["fund:a","fund:a"]`,
		"забагато":        `[` + strings.Join(many, ",") + `]`,
	} {
		resp, body := do(t, "PUT", srv.URL+"/api/hidden-rows", bad)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d %s, хочемо 400", name, resp.StatusCode, body)
		}
	}

	// Жодна з відмов не торкнулась збереженого: половина набору гірша за
	// відмову цілком, і перевіряється це саме тут.
	got, err := st.HiddenRows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "fund:Inzhur Ocean,goal:3,npf:ВПФ Династія" {
		t.Errorf("після відмов у базі %v", got)
	}

	// Заміна цілком, а не додавання: у режимі видимості знімають позначку
	// так само, як ставлять.
	if resp, body := do(t, "PUT", srv.URL+"/api/hidden-rows", `["goal:3"]`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("заміна: %d %s", resp.StatusCode, body)
	}
	if _, body := do(t, "GET", srv.URL+"/api/hidden-rows", ""); body != "[\"goal:3\"]\n" {
		t.Errorf("після заміни %q", body)
	}
}

// Позначка портфельна. Це і є та єдина причина, з якої вона живе у власній
// таблиці, а не ключем в app_state поруч із порядком рядків: спільне
// сховище сховало б однойменний фонд і в сусідньому портфелі — мовчки, бо
// повідомлення про приховане не існує за задумом.
func TestHiddenRowsPerPortfolio(t *testing.T) {
	srv, _ := testHub(t)
	if resp, body := doP(t, "POST", srv.URL+"/api/portfolios", `{"slug":"wife","name":"Дружина"}`, nil); resp.StatusCode != http.StatusCreated {
		t.Fatalf("створення портфеля: %d %s", resp.StatusCode, body)
	}

	if resp, body := doP(t, "PUT", srv.URL+"/api/hidden-rows", `["fund:Inzhur Ocean"]`, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("запис у головний: %d %s", resp.StatusCode, body)
	}
	if _, body := doP(t, "GET", srv.URL+"/api/hidden-rows", "", inPortfolio("wife")); body != "[]\n" {
		t.Errorf("сателіт бачить приховане головного: %s", body)
	}

	if resp, body := doP(t, "PUT", srv.URL+"/api/hidden-rows", `["goal:1"]`, inPortfolio("wife")); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("запис у сателіт: %d %s", resp.StatusCode, body)
	}
	if _, body := doP(t, "GET", srv.URL+"/api/hidden-rows", "", inPortfolio(store.MainSlug)); body != "[\"fund:Inzhur Ocean\"]\n" {
		t.Errorf("сателіт перезаписав головний: %s", body)
	}
}
