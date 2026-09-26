// Статика UI: ETag за вмістом і стиснення gzip, пораховані один раз.
//
// Доти статику віддавав http.FileServer, і кожне відкриття сторінки тягло
// весь UI заново: файли вшиті go:embed з нульовим ModTime, тож
// Last-Modified немає, а без нього й без ETag браузеру нема чим спитати
// «чи змінилось», і noCache (server.go) перепитування перетворював на
// повне вивантаження. Через тунель із телефона це ~2 МБ на кожне
// відкриття — не «~90 КБ», як колись писав коментар до noCache.
//
// Тепер кожен файл має ETag — sha256 його вмісту, — тож перепитування
// відповідається 304 без тіла, а змінений бінарником файл має інший ETag і
// приїжджає свіжим (саме те, заради чого noCache існує). Текстові файли
// стиснуті заздалегідь: вміст незмінний до наступної збірки, і стискати
// його на кожен запит означало б платити процесором за те саме число.
//
// Каталоги НЕ перелічуються: FileServer на «/js/» віддавав список модулів,
// тепер це 404 — переліку файлів застосунку ніхто, крім браузера, не
// просить, а браузер знає, що йому треба.
package api

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

type staticAsset struct {
	body, gz    []byte
	etag, etagZ string // ETag сирого й стиснутого варіанта: це різні байти
	ctype       string
}

// gzipExt — що стискати. Шрифти й картинки вже стиснуті своїм форматом.
var gzipExt = map[string]bool{
	".js": true, ".mjs": true, ".css": true, ".html": true, ".svg": true,
	".json": true, ".webmanifest": true, ".txt": true,
}

var staticAssets = sync.OnceValue(func() map[string]*staticAsset {
	sub, _ := fs.Sub(webFS, "web") //nolint:errcheck // шлях у go:embed — константа, помилка неможлива
	out := map[string]*staticAsset{}
	_ = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error { //nolint:errcheck // вшита ФС: помилки обходу бути не може
		if err != nil || d.IsDir() {
			return nil
		}
		body, rerr := fs.ReadFile(sub, p)
		if rerr != nil {
			return nil
		}
		sum := sha256.Sum256(body)
		tag := hex.EncodeToString(sum[:8])
		a := &staticAsset{body: body, etag: `"` + tag + `"`, etagZ: `"` + tag + `-gz"`}
		ext := path.Ext(p)
		a.ctype = mime.TypeByExtension(ext)
		switch {
		case ext == ".webmanifest":
			a.ctype = "application/manifest+json"
		case a.ctype == "":
			a.ctype = http.DetectContentType(body)
		}
		if gzipExt[ext] {
			var buf bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression) //nolint:errcheck // рівень — константа з пакета
			_, _ = zw.Write(body)                                    //nolint:errcheck // запис у памʼять
			_ = zw.Close()                                           //nolint:errcheck // запис у памʼять
			if buf.Len() < len(body) {
				a.gz = buf.Bytes()
			}
		}
		out[p] = a
		return nil
	})
	return out
})

// staticHandler віддає вшиту статику. Cache-Control ставить noCache —
// тут лише валідатори й кодування.
func staticHandler() http.Handler {
	assets := staticAssets()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := cmp.Or(strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/"), "index.html")
		a, ok := assets[p]
		if !ok {
			http.NotFound(w, r)
			return
		}
		body, etag := a.body, a.etag
		h := w.Header()
		if a.gz != nil {
			h.Add("Vary", "Accept-Encoding")
			if acceptsGzip(r) {
				body, etag = a.gz, a.etagZ
				h.Set("Content-Encoding", "gzip")
			}
		}
		h.Set("ETag", etag)
		h.Set("Content-Type", a.ctype)
		// If-None-Match → 304, HEAD, Content-Length — усе це робить
		// ServeContent, щойно ETag стоїть у заголовку.
		http.ServeContent(w, r, p, time.Time{}, bytes.NewReader(body))
	})
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.TrimSpace(enc) == "gzip" && strings.TrimSpace(q) != "q=0" {
			return true
		}
	}
	return false
}
