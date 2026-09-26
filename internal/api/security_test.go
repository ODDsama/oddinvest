package api

import (
	"context"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/ODDsama/oddinvest/internal/store"
)

// Вбудованого скрипта в index.html бути не може: script-src 'self' без
// хеша, і браузер такий скрипт не виконає — сторінка лишиться порожньою
// на бойовому, а Go такої помилки не бачить.
func TestIndexHasNoInlineScript(t *testing.T) {
	index, err := fs.ReadFile(webFS, "web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(index), "<script type=\"module\">") {
		t.Error("у index.html вбудований модуль — винеси його в js/main.js")
	}
}

// Заголовки стоять на будь-якій відповіді — і статиці, і API, і помилці.
func TestSecurityHeadersEverywhere(t *testing.T) {
	srv, _ := testHub(t)
	for _, path := range []string{"/", "/api/summary", "/api/nope"} {
		resp, _ := doP(t, "GET", srv.URL+path, "", nil)
		for k, v := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "DENY",
			"Referrer-Policy":        "same-origin",
		} {
			if got := resp.Header.Get(k); got != v {
				t.Errorf("%s: %s = %q, чекали %q", path, k, got, v)
			}
		}
		if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("%s: CSP без frame-ancestors: %q", path, csp)
		}
	}
}

// Тіло понад стелю не читається в памʼять до кінця: обробник дістає
// помилку читання й відповідає помилкою, а не 2xx.
func TestBodyOverLimitRejected(t *testing.T) {
	srv, _ := testHub(t)
	big := `{"note":"` + strings.Repeat("x", maxBodyBytes+1) + `"}`
	resp, _ := doP(t, "POST", srv.URL+"/api/deposits", big, nil)
	if resp.StatusCode < http.StatusBadRequest {
		t.Errorf("тіло понад %d байт прийнято: %d", maxBodyBytes, resp.StatusCode)
	}
}

// /healthz відповідає без сесії, з версією збірки й останньою міграцією:
// саме це звіряє lxc-deploy.sh після підміни бінарника.
func TestHealthzWithoutSessionReportsVersionAndMigration(t *testing.T) {
	srv, _ := testHub(t)
	resp, body := doP(t, "GET", srv.URL+"/healthz", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %d %s", resp.StatusCode, body)
	}
	for _, want := range []string{`"status":"ok"`, `"version":"` + Version + `"`, `"migration":"00`} {
		if !strings.Contains(body, want) {
			t.Errorf("healthz не містить %s: %s", want, body)
		}
	}
}

// /healthz несе здоровʼя добового прогону — дату дампу й цілісність, — але
// без подробиць пошкодження: ендпойнт відкритий без замка.
func TestHealthzReportsJobHealthWithoutDetails(t *testing.T) {
	srv, st := testHub(t)
	ctx := context.Background()
	if err := st.SetOwnState(ctx, store.BackupAtKey, "2026-09-20"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAppState(ctx, store.IntegrityKey, "Page 7 is never used"); err != nil {
		t.Fatal(err)
	}
	resp, body := doP(t, "GET", srv.URL+"/healthz", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %d %s — пропущений бекап не привід валити перевірку деплою", resp.StatusCode, body)
	}
	for _, want := range []string{`"backup_at":"2026-09-20"`, `"integrity":"broken"`} {
		if !strings.Contains(body, want) {
			t.Errorf("healthz не містить %s: %s", want, body)
		}
	}
	if strings.Contains(body, "Page 7") {
		t.Errorf("подробиці пошкодження вийшли назовні: %s", body)
	}
}
