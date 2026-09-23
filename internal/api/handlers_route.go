// GET /api/route — маршрут грошей, які ще тільки прийдуть.
//
// # ЧОМУ ЦЕ НЕ ФАЗА buildState
//
// Маршруту потрібен reinvestSuggestions, а той приймає ГОТОВИЙ документ.
// Усередині buildState це замкнуло б цикл, а обійти цикл можна лише другою
// збіркою порад — тобто SearchBonds на п'ять тисяч паперів на кожному
// POST /api/whatif і кожній публікації в MQTT. Черга задач стоїть на тій
// самій межі й з тієї самої причини (див. buildStateTasked).
//
// # ЧОМУ ЦЕ НЕ ЙДЕ В КОНТРАКТ
//
// Той самий прецедент, що й у кривої аукціонів і плану-таймлайну: дані для
// таблиці, а не сутності Home Assistant. Масив ніг у стані став би
// таблицею-сутністю (від чого інтеграція відмовляється прямо), а окреме
// число «мої гроші наступного місяця» — четвертим поруч із income_12m,
// next_payment і month_plan. Коли така потреба справді з'явиться, їй місце
// атрибутом на наявному сенсорі виплат, а не новою сутністю.
//
// # ЦІНА
//
// buildState + reinvestSuggestions + другий loadSources — рівно стільки ж,
// скільки коштує GET /api/reinvest із датою доступності. Для сторінки, яку
// відкривають свідомо, це прийнятно; заповзти в buildState цьому не можна.
package api

import (
	"net/http"
	"time"
)

func (s *Server) handleRoute(w http.ResponseWriter, r *http.Request) {
	out, err := s.route(r.Context(), time.Now(), r.URL.Query()["pick"])
	if err != nil {
		writeCalcErr(w, err)
		return
	}
	if err := s.present(r.Context(), &out); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
