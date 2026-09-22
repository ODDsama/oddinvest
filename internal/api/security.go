// Заголовки безпеки й стеля тіла запиту — для ВСІХ відповідей, і статики, і
// /api/*.
//
// Застосунок виходить в інтернет тунелем, а за замком лежить уся база. Тож
// браузер має знати три речі, яких доти йому ніхто не казав:
//
//   - звідки дозволено виконувати код (CSP). Скрипти — лише свої файли й
//     один вбудований модуль index.html за його sha256; чужий скрипт,
//     підкинутий у розмітку (views ставлять HTML через innerHTML), не
//     виконається навіть тоді, коли екранування десь схибить;
//   - що сторінку не можна вкладати у фрейм (клікджекінг): кнопки «Видати
//     токен» і «Підключити тунель» не мусять натискатись під чужою
//     прозорою сторінкою. Вкладень у дашборди HA немає (перевірено
//     2026-09-22), тож забороняємо без винятків;
//   - що тип файла не вгадується (nosniff).
//
// style-src лишається з 'unsafe-inline' свідомо: розмітка в shadow root
// несе style="--…" з токенами (css-tokens-check.mjs це дозволяє), а стилі
// компонента вставляються з js/styles.js. Заборона зламала б увесь вигляд,
// а ризик від вбудованого стилю — не виконання коду.
//
// Хеш рахується з того самого вшитого index.html, який віддається, — тож
// правка скрипта в ньому не може розійтись із політикою: вони з одного
// файла за побудовою.
package api

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

// maxBodyBytes — стеля тіла будь-якого запиту. Найбільші законні — файл
// виписки (handlers_import.go ріже його на 16 МіБ) і дамп для відновлення
// (одиниці мегабайт на бойовому); 32 МіБ лишає обом запас удвічі, а
// гігабайтний POST у памʼять уже не прочитається.
const maxBodyBytes = 32 << 20

var inlineScriptRe = regexp.MustCompile(`(?s)<script type="module">(.*?)</script>`)

// contentSecurityPolicy — політика з хешами вбудованих скриптів index.html.
var contentSecurityPolicy = sync.OnceValue(func() string {
	return buildCSP(indexHTML())
})

func indexHTML() []byte {
	b, err := fs.ReadFile(webFS, "web/index.html")
	if err != nil {
		// Вшитий файл є за побудовою (go:embed web); без нього сторінки
		// не буде однаково, і політика без хешів лише зробить це явним.
		return nil
	}
	return b
}

func buildCSP(index []byte) string {
	scripts := []string{"'self'"}
	for _, m := range inlineScriptRe.FindAllSubmatch(index, -1) {
		sum := sha256.Sum256(m[1])
		scripts = append(scripts, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + strings.Join(scripts, " "),
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
}

// securityHeaders ставить заголовки й стелю тіла до будь-якого обробника.
func securityHeaders(next http.Handler) http.Handler {
	csp := contentSecurityPolicy()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}
