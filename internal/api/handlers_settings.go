// Налаштування сервісу: реєстр ключів, валідація, читання й запис.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	money "github.com/Rhymond/go-money"
)

// Ключі, які приймає API, і перевірка «мусить бути числом» виводяться з
// settings_registry.go. Окремих списків тут більше немає: доти їх було
// два, і розійтись вони могли мовчки.
//
// «channels» немає ні там, ні тут: список брокерів був CSV-рядком у
// налаштуваннях, а тепер це таблиця brokers із власними ендпойнтами.

// depositMinMinorByCur — мінімальне вкладення у вклад по валютах, у МІНОРНИХ
// одиницях. Це водночас поріг «простій готовий до реінвесту» і крок поради
// «відкрити новий вклад». USD/EUR за замовчуванням 100.00 (=10000 мінорних):
// порожній ключ = дефолт, явний 0 (чи сміття) = вимкнено (валюти в мапі
// немає). UAH — лише якщо задано явно.
func (s *Server) depositMinMinorByCur(ctx context.Context) map[string]int64 {
	out := map[string]int64{}
	for _, sp := range []struct {
		cur, key string
		def      int64 // мінорні; 0 = без дефолту
	}{
		{money.USD, "deposit_min_usd", 10000},
		{money.EUR, "deposit_min_eur", 10000},
		{money.UAH, "deposit_min_uah", 0},
	} {
		raw, _ := s.st.GetSetting(ctx, sp.key) //nolint:errcheck // порожньо = не задано, далі йде дефолт валюти
		if raw == "" {
			if sp.def > 0 {
				out[sp.cur] = sp.def
			}
			continue
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil || f <= 0 {
			continue // явний 0 або сміття = вимкнено
		}
		out[sp.cur] = int64(math.Round(f * 100))
	}
	return out
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{}
	for _, k := range settingsKeys {
		v, err := s.st.GetSetting(r.Context(), k)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	for k, v := range req {
		d, ok := settingsByKey[k]
		if !ok {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("невідомий ключ %q", k))
			return
		}
		// Порожнє значення дозволене й означає «прибрати»: саме так
		// знецінення повертається з ручного на виміряне.
		if v != "" && d.numeric() {
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("%s: %q не число", k, v))
				return
			}
			if f < 0 {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("%s: від'ємне значення %v", k, f))
				return
			}
		}
		if err := s.st.SetSetting(r.Context(), k, v); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}
