package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Статика з валідатором і стисненням: незмінений модуль — 304 без тіла,
// текст — gzip, каталог — не перелік файлів.
func TestStaticETagGzipAndNoListing(t *testing.T) {
	srv, _ := testHub(t)
	get := func(path string, hdr map[string]string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		// Власний Accept-Encoding вимикає прозоре розпакування транспорту:
		// перевіряємо саме те, що пішло по дроту.
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	r := get("/js/app.js", map[string]string{"Accept-Encoding": "gzip"})
	if r.StatusCode != 200 || r.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("app.js: %d, encoding %q — чекали gzip", r.StatusCode, r.Header.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); !strings.Contains(string(b), "odd-invest-app") {
		t.Error("розпакований app.js не схожий на app.js")
	}
	etag := r.Header.Get("ETag")
	if etag == "" || !strings.Contains(r.Header.Get("Content-Type"), "javascript") {
		t.Errorf("ETag %q, Content-Type %q", etag, r.Header.Get("Content-Type"))
	}

	if r := get("/js/app.js", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": etag}); r.StatusCode != http.StatusNotModified {
		t.Errorf("перепитування тим самим ETag: %d, чекали 304", r.StatusCode)
	}
	// Без gzip — інший варіант байтів, інший ETag: старий не мусить підійти.
	if r := get("/js/app.js", map[string]string{"Accept-Encoding": "identity", "If-None-Match": etag}); r.StatusCode != 200 {
		t.Errorf("ETag стиснутого варіанта підійшов до сирого: %d", r.StatusCode)
	}
	if r := get("/js/", nil); r.StatusCode != http.StatusNotFound {
		t.Errorf("каталог: %d, чекали 404 замість переліку файлів", r.StatusCode)
	}
	if r := get("/", nil); r.StatusCode != 200 || !strings.HasPrefix(r.Header.Get("Content-Type"), "text/html") {
		t.Errorf("корінь: %d %q", r.StatusCode, r.Header.Get("Content-Type"))
	}
	if r := get("/manifest.webmanifest", nil); r.Header.Get("Content-Type") != "application/manifest+json" {
		t.Errorf("маніфест: %q", r.Header.Get("Content-Type"))
	}
}
