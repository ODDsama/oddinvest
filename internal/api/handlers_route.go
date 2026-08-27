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

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
)

func (s *Server) handleRoute(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	today := domain.NewDate(now)

	doc, err := s.buildState(r.Context(), now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	sug, err := s.reinvestSuggestions(r.Context(), now, doc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Другий прохід по джерелах — рівно той самий, що й у annotateReady, і
	// з тієї самої причини: прив'язати виплату до брокера можна лише через
	// лоти, а документ брокера у виплатах не несе (і не має нести — він іде
	// в MQTT).
	src, err := s.loadSources(r.Context(), today)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	inc, err := s.futureIncome(src, today)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// План доходу по місяцях горизонту — заради стелі подушки, і лише
	// заради неї. depositedUAH нульовий у КОЖНОМУ місяці, поточний
	// включно: те число потрібне для «лишилось закинути», якого маршрут не
	// показує, а стеля рахується від PlanUAH і на нього не дивиться.
	// Поточний місяць маршрут однаково бере з документа (див. newRouteCarry).
	plans := make(map[string]*state.MonthPlan, routeHorizonMonths+1)
	for m := 0; m <= routeHorizonMonths; m++ {
		plans[monthKeyAt(today, m)] = buildMonthPlan(src, src.rates, today, m, 0)
	}

	writeJSON(w, http.StatusOK, buildRoute(doc, sug, inc, plans, src.rates,
		s.npfIDByName(r.Context()), today))
}
