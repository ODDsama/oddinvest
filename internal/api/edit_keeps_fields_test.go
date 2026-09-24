package api

import (
	"encoding/json"
	"strconv"
	"testing"
)

// Правка на місці не стирає полів, яких рядок не показує.
//
// Рядок довідника НПФ правиться полями без нотатки, а PUT замінює
// рахунок цілком. Відсутня нотатка = лишити як є; порожній рядок =
// стерти навмисно.
func TestNPFAccountEditKeepsNote(t *testing.T) {
	srv, _ := testServer(t)
	resp, b := do(t, "POST", srv.URL+"/api/npf-accounts",
		`{"name":"Династія","currency":"UAH","note":"договір 17/3"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("рахунок: %d %s", resp.StatusCode, b)
	}
	var created struct{ ID int64 }
	if err := json.Unmarshal([]byte(b), &created); err != nil {
		t.Fatal(err)
	}
	url := srv.URL + "/api/npf-accounts/" + strconv.FormatInt(created.ID, 10)
	note := func() string {
		_, raw := do(t, "GET", srv.URL+"/api/npf-accounts", "")
		var rows []struct{ Note string }
		if err := json.Unmarshal([]byte(raw), &rows); err != nil || len(rows) != 1 {
			t.Fatalf("список: %v %s", err, raw)
		}
		return rows[0].Note
	}

	if resp, b := do(t, "PUT", url, `{"name":"Династія","currency":"UAH","contrib_day":5}`); resp.StatusCode != 204 {
		t.Fatalf("правка: %d %s", resp.StatusCode, b)
	}
	if got := note(); got != "договір 17/3" {
		t.Errorf("правка без нотатки стерла її: %q", got)
	}
	if resp, b := do(t, "PUT", url, `{"name":"Династія","currency":"UAH","note":""}`); resp.StatusCode != 204 {
		t.Fatalf("правка: %d %s", resp.StatusCode, b)
	}
	if got := note(); got != "" {
		t.Errorf("явний порожній рядок мав стерти нотатку: %q", got)
	}
}
