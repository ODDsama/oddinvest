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
	"strings"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
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
	// routeIncome, а не futureIncome: маршрут бачить іще й оцінені дивіденди
	// фондів, кожен зі своєю названою основою. Чому це можна тут і не можна
	// в даті «коли вистачить» — у шапці routeIncome.
	inc, err := s.routeIncome(src, today, routeHorizonMonths)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// План доходу по місяцях горизонту. Двом читачам: стелі подушки (вона
	// рахується від PlanUAH) і планових ніг маршруту.
	//
	// depositedUAH нульовий у МАЙБУТНІХ місяцях — у місяць, який ще не
	// настав, ніхто нічого не закидав. Поточний береться з документа готовим:
	// там уже стоїть справжнє «лишилось закинути», і перерахувати його тут
	// нулем означало б повести в маршрут гроші, які вже принесли. Для стелі
	// підміна безпечна за побудовою: поточний місяць enterMonth пропускає
	// мовчки (див. newRouteCarry), і плану того місяця вона не читає.
	plans := make(map[string]*state.MonthPlan, routeHorizonMonths+1)
	for m := 0; m <= routeHorizonMonths; m++ {
		plans[monthKeyAt(today, m)] = buildMonthPlan(src, src.rates, today, m, 0, "")
	}
	if doc.MonthPlan != nil {
		plans[monthKeyAt(today, 0)] = doc.MonthPlan
	}

	// Плановий дохід — окремим збирачем і ОКРЕМИМ ДОДАВАННЯМ, а не всередині
	// routeIncome: тій потрібні plans, яких у неї немає й бути не має (вона
	// про розклад портфеля), а futureIncome чіпати не можна взагалі — її
	// незмінність тримає регресійний тест, і саме на ній стоїть відмова
	// показувати намір у даті «коли вистачить».
	if flows := planAhead(src, plans, today, routeHorizonMonths); len(flows) > 0 {
		k := store.BrokerCur{Broker: noBrokerLabel, Currency: money.UAH}
		inc[k] = append(inc[k], flows...)
		sortFlows(inc[k])
	}

	picks, err := routePicks(r.URL.Query()["pick"], sug)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	out := buildRoute(doc, sug, inc, plans, src.rates,
		s.npfIDByName(r.Context()), picks, today)
	// План купівель — окремим проходом поверх готових ніг: аргумент при
	// annotatePlanned. Рядки вже лежать у джерелах, другого читання немає.
	annotatePlanned(out.Legs, src.planBuys, today)
	writeJSON(w, http.StatusOK, out)
}

// routePickSep — роздільник у значенні ?pick=. Вертикальна риска, бо її
// немає ні в даті, ні в ISIN, ні в коді валюти, а в назві брокера вона
// була б дивиною, якої досі ніхто не завів.
const routePickSep = "|"

// routePicks — вибір паперів по ногах із параметрів запиту.
//
// ПАРАМЕТРОМ ЗАПИТУ, А НЕ ТАБЛИЦЕЮ, і це не спрощення. Маршрут — вигляд, а
// не стан (шапка annotatePlanned): вибір живе, доки людина дивиться на
// сторінку, і щойно ногу закріплено, його тримає plan_buys — рядком із тим
// самим ISIN. Третє місце, де лежав би «майбутній папір», розійшлося б із
// планом купівель при першій же правці того плану.
//
// Формат одного значення — <дата>|<брокер>|<валюта>|<ISIN>, чотири частини
// routeKey плюс сам вибір; кілька ніг — кілька параметрів pick. Брокер без
// рахунку йде тим самим «—», яким його показує нога (noBrokerLabel).
//
// ISIN перевіряється проти порад тим самим pickSuggestion, що й у
// POST /api/allocate: перша нога маршруту дорівнює розкладці, і відмова
// на невідомий папір мусить бути тією самою в обох. Ключ ноги натомість не
// перевіряється (див. buildRoute): нога могла зникнути з розкладу, і це
// не привід валити всю сторінку.
func routePicks(raw []string, sug []suggestion) (map[routeKey]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	picks := make(map[routeKey]string, len(raw))
	for _, v := range raw {
		parts := strings.SplitN(v, routePickSep, 4)
		if len(parts) != 4 {
			return nil, badRequestf("pick: чекали <дата>|<брокер>|<валюта>|<ISIN>, отримали %q", v)
		}
		isin, err := pickSuggestion(sug, parts[3])
		if err != nil {
			return nil, err
		}
		if isin == "" {
			return nil, badRequestf("pick: порожній ISIN у %q", v)
		}
		picks[routeKey{
			Date: strings.TrimSpace(parts[0]), Broker: strings.TrimSpace(parts[1]),
			Currency: orUAH(strings.TrimSpace(parts[2])),
		}] = isin
	}
	return picks, nil
}
