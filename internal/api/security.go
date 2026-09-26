// Заголовки безпеки й стеля тіла запиту — для ВСІХ відповідей, і статики, і
// /api/*.
//
// Застосунок виходить в інтернет тунелем, а за замком лежить уся база. Тож
// браузер має знати три речі, яких доти йому ніхто не казав:
//
//   - звідки дозволено виконувати код (CSP). Скрипти — лише свої файли
//     (вбудованих у index.html немає, точка входу — js/main.js); чужий скрипт,
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
package api

import (
	"net/http"
)

// maxBodyBytes — стеля тіла будь-якого запиту. Найбільші законні — файл
// виписки (handlers_import.go ріже його на 16 МіБ) і дамп для відновлення
// (одиниці мегабайт на бойовому); 32 МіБ лишає обом запас удвічі, а
// гігабайтний POST у памʼять уже не прочитається.
const maxBodyBytes = 32 << 20

const contentSecurityPolicy = "default-src 'self'; script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// securityHeaders ставить заголовки й стелю тіла до будь-якого обробника.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}
