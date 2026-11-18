// Дрібні помічники HTTP-шару: розбір шляху й відповіді. Гроші в JSON —
// у engine/format.go, читання portfolio/rates — в engine/engine.go: їх потребує й
// розрахунок, а не лише обробник.

package api

import (
	"encoding/json"
	"errors"
	"github.com/ODDsama/oddinvest/internal/engine"
	"log/slog"
	"net/http"
	"strconv"
)

// pathID — {id} зі шляху. Три обробники розбирали його однаково, а з
// появою PUT стало б шість копій.
func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// writeErr — відповідь-помилка. 4xx несе текст як є: це розмова з автором
// запиту про його ж запит. 500 — ні: там текст SQL, шляхи файлів і нутрощі
// сховища, а частина маршрутів (вхід) відповідає й без пароля. Подробиці
// лягають у журнал, клієнт дістає одне речення.
//
// Рівно 500, а не всі 5xx: 502 («НБУ не відповів») і 501/503 («тунель у
// цій збірці недоступний») пишуться для людини й кажуть, що робити далі.
func writeErr(w http.ResponseWriter, code int, err error) {
	if code == http.StatusInternalServerError {
		slog.Error("відповідь 500", "err", err)
		writeJSON(w, code, map[string]string{"error": "внутрішня помилка сервера — подробиці в журналі (journalctl -u oddinvestd)"})
		return
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// writeCalcErr — помилка розрахунку: 400, коли винне питання
// (BadRequestError — невідомий папір, криве поле), інакше 500. Одне місце,
// щоб «описка у формі» ніде не ставала «зламався сервер».
func writeCalcErr(w http.ResponseWriter, err error) {
	var bad engine.BadRequestError
	if errors.As(err, &bad) {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}

// --- handlers ---
