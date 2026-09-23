// Налаштування сервісу: реєстр ключів, валідація, читання й запис.

package api

import (
	"encoding/json"
	"github.com/ODDsama/oddinvest/internal/settings"
	"net/http"
)

// Ключі, які приймає API, і перевірка «мусить бути числом» виводяться з
// internal/settings. Окремих списків тут більше немає: доти їх було
// два, і розійтись вони могли мовчки.
//
// «channels» немає ні там, ні тут: список брокерів був CSV-рядком у
// налаштуваннях, а тепер це таблиця brokers із власними ендпойнтами.

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
	out := make(map[string]string, len(settings.Keys))
	for _, k := range settings.Keys {
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
	// Сама перевірка живе в internal/settings, бо споживачів у неї
	// тепер двоє: цей запис і превʼю політики.
	if err := settings.Validate(req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Запис у витрати ГАСИТЬ спадковий ключ, і це єдиний особливий випадок
	// у цьому циклі.
	//
	// Без нього форма бреше найтихішим чином. Міграція 0038 скопіювала
	// monthly_expenses_uah у нову пару, тож після неї обидва ключі несуть
	// одне число; порожній monthly_expenses читається як «не рахувати», а
	// settings.ResolveExpensesUAH у цьому разі лишає гривневе поле спадковому
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
