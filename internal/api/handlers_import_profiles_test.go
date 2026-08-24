package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"
)

// postCSV надсилає CSV як multipart-файл — так само, як це робить форма.
func postCSV(t *testing.T, url, csv string) (*http.Response, string) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "statement.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(csv)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", url, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp, buf.String()
}

const monoProfile = `{"format":"csv","header":1,"col_date":0,"col_op":1,
	"col_ref":2,"col_qty":3,"col_debit":4,"col_credit":5,
	"ops":"Поповнення = deposit\nКупівля = fund_buy\nДивіденди = dividend"}`

const monoCSV = "Дата,Операція,Папір,Кількість,Надійшло,Списано\n" +
	"2026-08-01,Поповнення рахунку,,,10000.00,\n" +
	"2026-08-03,Купівля,ФОНД,12,,1200.00\n"

func saveProfile(t *testing.T, url, name, body string) {
	t.Helper()
	resp, got := do(t, "PUT", url+"/api/import/profiles/"+name, body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("збереження профілю: %d %s", resp.StatusCode, got)
	}
}

type importOut struct {
	Rows []struct {
		Kind   string `json:"kind"`
		Fund   string `json:"fund"`
		Qty    int64  `json:"qty"`
		Amount string `json:"amount"`
		Exists bool   `json:"exists"`
	} `json:"rows"`
	Skipped []struct {
		Reason string `json:"reason"`
	} `json:"skipped"`
	Imported int `json:"imported"`
	New      int `json:"new"`
}

func parseImportOut(t *testing.T, body string) importOut {
	t.Helper()
	var out importOut
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("розбір відповіді: %v (%s)", err, body)
	}
	return out
}

// Наскрізний шлях чужої виписки: профіль → перегляд → запис.
func TestImportByProfileCSV(t *testing.T) {
	srv, st := testServer(t)
	importSince(t, st, "2026-01-01")
	saveProfile(t, srv.URL, "mono", monoProfile)

	resp, body := postCSV(t, srv.URL+"/api/import?profile=mono&dry=1", monoCSV)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("перегляд: %d %s", resp.StatusCode, body)
	}
	dry := parseImportOut(t, body)
	if len(dry.Skipped) != 0 {
		t.Errorf("нічого не мало пропаститись: %+v", dry.Skipped)
	}
	if dry.New != 2 || dry.Imported != 0 {
		t.Errorf("перегляд: нових %d, записано %d — очікували 2 і 0", dry.New, dry.Imported)
	}

	resp, body = postCSV(t, srv.URL+"/api/import?profile=mono", monoCSV)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("імпорт: %d %s", resp.StatusCode, body)
	}
	if got := parseImportOut(t, body); got.Imported != 2 {
		t.Errorf("записано %d, очікували 2", got.Imported)
	}

	// Другий прогін того самого файлу нічого не додає: дедуплікація й
	// водяний знак працюють для чужої виписки так само, як для Inzhur.
	// Це головна перевірка всього напряму — механізми після розбору
	// формату не знають і знати не мусять.
	resp, body = postCSV(t, srv.URL+"/api/import?profile=mono", monoCSV)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("повторний імпорт: %d %s", resp.StatusCode, body)
	}
	if got := parseImportOut(t, body); got.Imported != 0 {
		t.Errorf("повторний імпорт записав %d рядків — дедуплікація не спрацювала", got.Imported)
	}
}

// Профіль, якого немає, — 404, а не мовчазний відкат до розбору Inzhur.
// Мовчазний відкат означав би, що чужа виписка заходить чужим розбирачем
// і лягає невідомо як.
func TestImportUnknownProfileIs404(t *testing.T) {
	srv, _ := testServer(t)
	resp, body := postCSV(t, srv.URL+"/api/import?profile=нема&dry=1", monoCSV)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("%d, очікували 404 (%s)", resp.StatusCode, body)
	}
}

// Назва вбудованого розбору зайнята: профіль із нею ніколи б не спрацював,
// бо /api/import віддає «inzhur» вбудованому шляху ще до сховища.
func TestImportProfileCannotShadowInzhur(t *testing.T) {
	srv, _ := testServer(t)
	resp, body := do(t, "PUT", srv.URL+"/api/import/profiles/inzhur", monoProfile)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("%d, очікували 409 (%s)", resp.StatusCode, body)
	}
}

// Зламаний словник операцій ловиться в мить збереження, а не через місяць
// під час імпорту — тобто тоді, коли людина ще дивиться саме на профіль.
func TestImportProfileValidatedOnSave(t *testing.T) {
	srv, _ := testServer(t)
	for _, c := range []struct{ name, body string }{
		{"невідомий вид", `{"col_date":0,"col_op":1,"col_debit":4,"ops":"Поповнення = попка"}`},
		{"порожній словник", `{"col_date":0,"col_op":1,"col_debit":4,"ops":""}`},
		{"без колонки суми", `{"col_date":0,"col_op":1,"col_debit":-1,"col_credit":-1,"ops":"Поповнення = deposit"}`},
		{"без колонки дати", `{"col_date":-1,"col_op":1,"col_debit":4,"ops":"Поповнення = deposit"}`},
	} {
		resp, body := do(t, "PUT", srv.URL+"/api/import/profiles/x", c.body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d, очікували 400 (%s)", c.name, resp.StatusCode, body)
		}
	}
}

// Повний КРУД, як вимагає CLAUDE.md §2: профіль вивіряють по власній
// виписці, і одруківка в номері колонки має виправлятись, а не змушувати
// набирати все заново.
func TestImportProfileCRUD(t *testing.T) {
	srv, _ := testServer(t)
	saveProfile(t, srv.URL, "mono", monoProfile)

	resp, body := do(t, "GET", srv.URL+"/api/import/profiles", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("список: %d %s", resp.StatusCode, body)
	}
	var list []importProfileJSON
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "mono" || list[0].Format != "csv" {
		t.Fatalf("список не той: %+v", list)
	}

	// Правка тією ж назвою — не другий рядок, а зміна першого.
	saveProfile(t, srv.URL, "mono", `{"format":"xlsx","header":2,"col_date":0,
		"col_op":1,"col_debit":4,"ops":"Поповнення = deposit"}`)
	_, body = do(t, "GET", srv.URL+"/api/import/profiles", "")
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Header != 2 || list[0].Format != "xlsx" {
		t.Fatalf("правка не застосувалась або завела другий рядок: %+v", list)
	}

	if resp, body := do(t, "DELETE", srv.URL+"/api/import/profiles/mono", ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("видалення: %d %s", resp.StatusCode, body)
	}
	_, body = do(t, "GET", srv.URL+"/api/import/profiles", "")
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("після видалення лишилось %d", len(list))
	}
}

// Профілі переживають бекап і відновлення: індекси колонок і словник
// операцій людина вивіряє руками, і з жодних операцій їх не вивести.
func TestImportProfilesSurviveBackupRestore(t *testing.T) {
	srv, _ := testServer(t)
	saveProfile(t, srv.URL, "mono", monoProfile)

	resp, dump := do(t, "GET", srv.URL+"/api/backup", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("бекап: %d %s", resp.StatusCode, dump)
	}
	if resp, body := do(t, "POST", srv.URL+"/api/restore", dump); resp.StatusCode != http.StatusOK {
		t.Fatalf("відновлення: %d %s", resp.StatusCode, body)
	}
	_, body := do(t, "GET", srv.URL+"/api/import/profiles", "")
	var list []importProfileJSON
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "mono" || list[0].Qty != 3 {
		t.Errorf("профіль не пережив відновлення: %+v", list)
	}
}
