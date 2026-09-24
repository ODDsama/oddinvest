package api

import (
	"testing"

	"github.com/ODDsama/oddinvest/internal/imports"
)

// Новий водяний знак — не «сьогодні», а межа розглянутого, і не пізніше
// за найраніший пропущений рядок: той має зайти, щойно причину виправлять.
func TestNextImportSince(t *testing.T) {
	rows := []outRow{{Date: "2026-09-10"}, {Date: "2026-09-20"}}
	if got := nextImportSince("2026-09-01", rows, nil); got != "2026-09-20" {
		t.Errorf("без пропусків — найпізніший розглянутий: %s", got)
	}
	skipped := []imports.Skipped{{Date: "2026-09-12", Reason: "додай позначку ціни"}}
	if got := nextImportSince("2026-09-01", rows, skipped); got != "2026-09-12" {
		t.Errorf("пропущений 12-го мусить лишитись видимим: %s", got)
	}
	if got := nextImportSince("2026-09-01", nil, nil); got != "2026-09-01" {
		t.Errorf("порожній файл знака не рухає: %s", got)
	}
}

// Водяний знак — свій у кожного профілю; без нього — старий спільний.
func TestImportSincePerProfile(t *testing.T) {
	srv, _ := testServer(t)
	if resp, b := do(t, "PUT", srv.URL+"/api/settings", `{"import_since":"2026-01-01"}`); resp.StatusCode >= 300 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, b)
	}
	get := func(p string) string {
		_, raw := do(t, "GET", srv.URL+"/api/import/since?profile="+p, "")
		return raw
	}
	if got := get("mono"); got != "{\"since\":\"2026-01-01\"}\n" {
		t.Errorf("без свого знака — спільний старий: %q", got)
	}
	if resp, b := do(t, "PUT", srv.URL+"/api/import/since?profile=mono", `{"since":"2026-09-25"}`); resp.StatusCode != 204 {
		t.Fatalf("PUT: %d %s", resp.StatusCode, b)
	}
	if got := get("mono"); got != "{\"since\":\"2026-09-25\"}\n" {
		t.Errorf("свій знак mono: %q", got)
	}
	if got := get("inzhur"); got != "{\"since\":\"2026-01-01\"}\n" {
		t.Errorf("знак mono не мав зачепити inzhur: %q", got)
	}
	if resp, _ := do(t, "PUT", srv.URL+"/api/import/since?profile=mono", `{"since":"25.09.2026"}`); resp.StatusCode != 400 {
		t.Errorf("крива дата — 400, а не %d", resp.StatusCode)
	}
}
