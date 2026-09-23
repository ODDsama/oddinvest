// Дрібні помічники HTTP-шару: розбір шляху й відповіді. Гроші в JSON —
// у format.go, читання portfolio/rates — в engine.go: їх потребує й
// розрахунок, а не лише обробник.

package api

import (
	"encoding/json"
	"errors"
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

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// writeCalcErr — помилка розрахунку: 400, коли винне питання
// (BadRequestError — невідомий папір, криве поле), інакше 500. Одне місце,
// щоб «описка у формі» ніде не ставала «зламався сервер».
func writeCalcErr(w http.ResponseWriter, err error) {
	var bad BadRequestError
	if errors.As(err, &bad) {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}

// --- handlers ---
