// Налаштування сервісу: реєстр ключів, валідація, читання й запис.

package api

import (
	"encoding/json"
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
func depositMinMinorByCur(raw map[string]string) map[string]int64 {
	out := map[string]int64{}
	for _, sp := range []struct {
		cur, key string
		def      int64 // мінорні; 0 = без дефолту
	}{
		{money.USD, "deposit_min_usd", 10000},
		{money.EUR, "deposit_min_eur", 10000},
		{money.UAH, "deposit_min_uah", 0},
	} {
		v := raw[sp.key]
		if v == "" {
			if sp.def > 0 {
				out[sp.cur] = sp.def
			}
			continue
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || f <= 0 {
			continue // явний 0 або сміття = вимкнено
		}
		out[sp.cur] = int64(math.Round(f * 100))
	}
	return out
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	raw, err := s.st.AllSettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Віддаємо РІВНО ключі реєстру, а не все, що лежить у таблиці: старі
	// бекапи можуть повернути ключі, яких у реєстрі вже немає, і в наборі
	// налаштувань їм не місце — PUT їх усе одно не прийме. (Робочий стан
	// джоб — nbu_refreshed_at, ovdp_auctions_polled_through — з 0054 живе
	// в app_state і сюди не потрапляє взагалі.)
	out := make(map[string]string, len(settingsKeys))
	for _, k := range settingsKeys {
		out[k] = raw[k]
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Перевірка ВСЬОГО тіла до першого запису, а не всередині циклу.
	// Доти невідомий ключ у середині мапи лишав записаними ті, що встигли
	// пройти, — половину набору, якої ніхто не просив; а що мапа
	// перебирається в довільному порядку, половина щоразу була інша.
	//
	// Сама перевірка живе в settings_registry.go, бо споживачів у неї
	// тепер двоє: цей запис і превʼю політики.
	if err := validateSettings(req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Запис у витрати ГАСИТЬ спадковий ключ, і це єдиний особливий випадок
	// у цьому циклі.
	//
	// Без нього форма бреше найтихішим чином. Міграція 0038 скопіювала
	// monthly_expenses_uah у нову пару, тож після неї обидва ключі несуть
	// одне число; порожній monthly_expenses читається як «не рахувати», а
	// resolveExpensesUAH у цьому разі лишає гривневе поле спадковому
	// ключу — тобто очищене поле мовчки поверталося б до старого значення,
	// і скасувати ціль резерву стало б неможливо через UI взагалі.
	//
	// Гасимо на БУДЬ-ЯКОМУ записі суми, а не лише на порожньому: пара
	// «сума + валюта» і є тепер джерелом істини, а спадковий ключ існує
	// рівно для бази, якої нова форма ще не торкалась.
	if _, ok := req["monthly_expenses"]; ok {
		req["monthly_expenses_uah"] = ""
	}
	for k, v := range req {
		if err := s.st.SetSetting(r.Context(), k, v); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}
