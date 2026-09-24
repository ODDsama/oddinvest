package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

// Відновлення з бекапу лишає страхувальну копію бази — і каже, де вона.
func TestRestoreLeavesSafetyCopy(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	resp, dump := do(t, "GET", srv.URL+"/api/backup", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("бекап: %d", resp.StatusCode)
	}
	resp, body := do(t, "POST", srv.URL+"/api/restore", dump)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("відновлення: %d %s", resp.StatusCode, body)
	}
	var out struct {
		SafetyCopy string `json:"safety_copy"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.SafetyCopy, ".restore-") {
		t.Fatalf("страхувальної копії не названо: %s", body)
	}
	if _, err := os.Stat(out.SafetyCopy); err != nil {
		t.Errorf("копії %s немає на диску: %v", out.SafetyCopy, err)
	}
}
