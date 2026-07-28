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

// «channels» тут більше немає: список брокерів був CSV-рядком у
// налаштуваннях, а тепер це таблиця brokers із власними ендпойнтами.
var settingsKeys = []string{"usd_target_share_pct", "eur_target_share_pct",
	"assumed_rate_pct", "goal_amount_uah", "goal_date",
	"reinvest_rank", "goal_pessimistic_uah", "goal_realistic_uah", "goal_optimistic_uah",
	"uah_devaluation_pct", "terminal_rate_pct", "rate_glide_years",
	// Вклад як інструмент реінвесту: мінімум і ставка нового вкладу по валютах.
	"deposit_min_usd", "deposit_min_eur", "deposit_min_uah",
	"deposit_rate_usd_pct", "deposit_rate_eur_pct", "deposit_rate_uah_pct",
	// Резерв: скільки коштує місяць життя і на скільки місяців запас.
	"monthly_expenses_uah", "reserve_target_months",
	// import_since рухає сам імпорт, але лишається редагованим: інакше
	// «перезавантажити позаминулий місяць» стало б неможливим взагалі.
	"import_since"}

// numericSettings — ключі, значення яких мусять бути невід'ємним числом.
// Доти перевірявся лише сам ключ: "abc" чи "-5" писались у базу як є, а
// на читанні мовчки підмінялись дефолтом. Тобто налаштування виглядало
// збереженим, поводилось як незбережене, і дізнатись про це не було де.
var numericSettings = map[string]bool{
	"usd_target_share_pct": true, "eur_target_share_pct": true,
	"assumed_rate_pct": true, "goal_amount_uah": true,
	"goal_pessimistic_uah": true, "goal_realistic_uah": true, "goal_optimistic_uah": true,
	"uah_devaluation_pct": true, "terminal_rate_pct": true, "rate_glide_years": true,
	"deposit_min_usd": true, "deposit_min_eur": true, "deposit_min_uah": true,
	"deposit_rate_usd_pct": true, "deposit_rate_eur_pct": true, "deposit_rate_uah_pct": true,
	"monthly_expenses_uah": true, "reserve_target_months": true,
}

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
	allowed := map[string]bool{}
	for _, k := range settingsKeys {
		allowed[k] = true
	}
	for k, v := range req {
		if !allowed[k] {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("невідомий ключ %q", k))
			return
		}
		// Порожнє значення дозволене й означає «прибрати»: саме так
		// знецінення повертається з ручного на виміряне.
		if v != "" && numericSettings[k] {
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
