package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// --- з яких грошей ріже стеля подушки ---

// Таблиця три рівні × три природи грошей.
//
// Природ саме три, а не два: між «планове надходження» і «дохід портфеля»
// стоїть повернення власного тіла, і воно не є ні тим, ні тим. Рівень
// "redeem" існує рівно заради нього, і без цієї таблиці різниця між ним і
// сусідами трималася б на одному рядку switch.
func TestReserveEligibleUAH(t *testing.T) {
	set := func(v string) *state.SettingsDoc { return &state.SettingsDoc{ReserveFillFrom: v} }

	cases := []struct {
		name                    string
		set                     *state.SettingsDoc
		src                     string
		amount, principal, want float64
	}{
		// Дефолт — поведінка ДО появи ключа, і це головний рядок таблиці:
		// налаштування, яке мовчки вимкнуло б подушку, було б найгіршим
		// виглядом помилки.
		{"немає налаштувань: план", nil, allocFromPlan, 5000, 0, 5000},
		{"немає налаштувань: купон", nil, allocFromPortfolio, 5000, 0, 5000},
		{"порожньо: купон", set(""), allocFromPortfolio, 5000, 0, 5000},
		{"сміття читається як any", set("щось"), allocFromPortfolio, 5000, 0, 5000},
		{"any: погашення", set("any"), allocFromPortfolio, 5000, 5000, 5000},

		{"redeem: план цілком", set("redeem"), allocFromPlan, 5000, 0, 5000},
		{"redeem: купон нічого", set("redeem"), allocFromPortfolio, 5000, 0, 0},
		{"redeem: погашення цілком", set("redeem"), allocFromPortfolio, 5000, 5000, 5000},
		// Заради цього рядка вирізка й міряється ЧАСТКОЮ, а не подією:
		// зведений «купон 817 + погашення 10 000 того самого дня» — одна
		// подія з двома природами.
		{"redeem: купон+погашення — лише тіло", set("redeem"), allocFromPortfolio, 10817, 10000, 10000},
		// Тіло приходить із події, сума — з горщика, і в маршруті другий
		// буває меншим: частину вже витратили на папери.
		{"redeem: тіло більше за горщик", set("redeem"), allocFromPortfolio, 900, 5000, 900},

		{"plan: план цілком", set("plan"), allocFromPlan, 5000, 0, 5000},
		{"plan: купон нічого", set("plan"), allocFromPortfolio, 5000, 0, 0},
		{"plan: погашення теж нічого", set("plan"), allocFromPortfolio, 5000, 5000, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := reserveEligibleUAH(c.set, c.src, c.amount, c.principal)
			if got != c.want {
				t.Errorf("дозволено %.2f, чекали %.2f", got, c.want)
			}
		})
	}
}

// Невідоме значення ключа PUT не приймає взагалі.
//
// Строкові ключі доти не перевірялись ніяк, і для reinvest_rank це було
// нешкідливо — невідомий режим просто не збігався з жодною гілкою
// рейтингу. Тут інакше: описка мовчки вимкнула б поповнення подушки, і
// шукати причину довелось би в чужих числах.
func TestValidateSettingsChecksEnum(t *testing.T) {
	for _, v := range []string{"any", "redeem", "plan", ""} {
		if err := validateSettings(map[string]string{"reserve_fill_from": v}); err != nil {
			t.Errorf("значення %q відхилене: %v", v, err)
		}
	}
	err := validateSettings(map[string]string{"reserve_fill_from": "plann"})
	if err == nil {
		t.Fatal("описка пройшла — подушку можна вимкнути непоміченим ключем")
	}
	if !strings.Contains(err.Error(), "reserve_fill_from") {
		t.Errorf("помилка не називає ключа: %v", err)
	}
}

// --- маршрут ---

// routeRedeem — надходження, яке цілком є поверненням тіла.
func routeRedeem(date string, amountMajor float64, label string) readyFlow {
	f := routeFlow(date, amountMajor, label)
	f.Principal = f.Amount
	return f
}

// routeReserveDoc — портфель із живим розривом подушки й ціллю в ОВДП.
func routeReserveDoc(fillFrom string) *state.Doc {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)},
		&state.Reserve{FillNowUAH: 12000, FillMonthUAH: 12000, GapUAH: 40000})
	doc.Settings = routeSettings(10000, 6, 40)
	doc.Settings.ReserveFillFrom = fillFrom
	doc.ReserveUAH = 20000
	return doc
}

func routeOnce(doc *state.Doc, flows ...readyFlow) routeDoc {
	return buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		routeInc("mono", money.UAH, flows...),
		routePlans(30000), allocRates, nil, routeToday)
}

// Купон при «лише з планових» подушки не годує — і каже, ЧОМУ.
//
// Друга половина тесту важить не менше за першу: зникла вирізка без
// причини читається як поломка, а не як рішення, яке людина сама й
// ухвалила.
func TestRouteCouponSkipsReserveWhenPlanOnly(t *testing.T) {
	got := routeOnce(routeReserveDoc("plan"), routeFlow("2026-09-10", 20000, "UA0001"))

	if len(got.Legs) != 1 {
		t.Fatalf("ніг %d, чекали 1", len(got.Legs))
	}
	leg := got.Legs[0]
	if leg.Reserve != nil {
		t.Errorf("подушка взяла %.2f — купон за політикою в неї не йде", leg.Reserve.AmountUAH)
	}
	if leg.ReserveSkipWhy == "" {
		t.Error("вирізка зникла мовчки — причина мусить бути названа")
	}
	if len(leg.Lines) == 0 {
		t.Error("гроші нікуди не пішли — купон мусив піти в папери")
	}
}

// Погашення при «з планових і погашень» подушку годує, при «лише з
// планових» — ні. Дзеркальна пара, і саме вона тримає середній рівень.
func TestRouteRedemptionFollowsLevel(t *testing.T) {
	redeem := routeOnce(routeReserveDoc("redeem"), routeRedeem("2026-09-10", 20000, "UA0001"))
	if len(redeem.Legs) != 1 || redeem.Legs[0].Reserve == nil {
		t.Fatalf("redeem: подушка нічого не взяла: %+v", redeem.Legs)
	}
	if got := redeem.Legs[0].Reserve.AmountUAH; got != 12000 {
		t.Errorf("redeem: у подушку %.2f, чекали 12000 — стеля місяця", got)
	}

	plan := routeOnce(routeReserveDoc("plan"), routeRedeem("2026-09-10", 20000, "UA0001"))
	if len(plan.Legs) != 1 {
		t.Fatalf("plan: ніг %d, чекали 1", len(plan.Legs))
	}
	if plan.Legs[0].Reserve != nil {
		t.Errorf("plan: подушка взяла %.2f — тіло за цією політикою теж не її",
			plan.Legs[0].Reserve.AmountUAH)
	}
}

// Зведена подія «купон + погашення того самого дня» віддає подушці РІВНО
// тіло. Це і є той випадок, заради якого дозволене міряється часткою суми,
// а не одним словом на всю подію.
func TestRouteMixedEventGivesReserveOnlyPrincipal(t *testing.T) {
	// Стеля 12 000 більша за тіло, тож обмежує саме тіло, а не вона.
	ev := routeFlow("2026-09-10", 10817, "UA0001")
	ev.Principal = 1000000 // 10 000 ₴ тіла, решта — купон

	got := routeOnce(routeReserveDoc("redeem"), ev)
	if len(got.Legs) != 1 || got.Legs[0].Reserve == nil {
		t.Fatalf("подушка нічого не взяла: %+v", got.Legs)
	}
	if v := got.Legs[0].Reserve.AmountUAH; v != 10000 {
		t.Errorf("у подушку %.2f, чекали 10000 — рівно тіло", v)
	}
	if got.Legs[0].ReserveSkipWhy == "" {
		t.Error("подушка недобрала стелю через політику й не сказала цього")
	}
}

// Перша нога маршруту дорівнює розкладці Й ТУТ.
//
// Розширення головного інваріанта на нову вісь: якщо джерело доходить до
// однієї з двох відповідей інакше, ніж до другої, сторінка маршруту вестиме
// купон у папери, а модалка того ж дня різатиме з нього подушку.
func TestRouteFirstLegEqualsAllocateWithSource(t *testing.T) {
	doc := routeReserveDoc("redeem")
	sug := []suggestion{bondSug("UA0001", 1000, money.UAH)}
	ev := routeFlow("2026-08-28", 5000, "UA0001")
	ev.Principal = 300000 // 3 000 ₴ тіла

	got := buildRoute(doc, sug, routeInc("mono", money.UAH, ev),
		routePlans(30000), allocRates, nil, routeToday)
	if len(got.Legs) != 1 {
		t.Fatalf("ніг %d, чекали 1", len(got.Legs))
	}
	want := allocatePlan(doc, sug, allocRates,
		toMoneyJSON(money.New(500000, money.UAH)), 5000,
		reserveEligibleUAH(doc.Settings, allocFromPortfolio, 5000, 3000),
		money.UAH, nil)

	if got.Legs[0].Reserve == nil || want.Reserve == nil {
		t.Fatalf("подушка мовчить в одному з двох: маршрут %+v, розкладка %+v",
			got.Legs[0].Reserve, want.Reserve)
	}
	if got.Legs[0].Reserve.AmountUAH != want.Reserve.AmountUAH {
		t.Errorf("маршрут дав подушці %.2f, розкладка %.2f — завелась друга арифметика",
			got.Legs[0].Reserve.AmountUAH, want.Reserve.AmountUAH)
	}
}

// Порожній ключ лишає маршрут таким самим, яким він був до появи політики.
func TestRouteEmptyFillFromBehavesAsAny(t *testing.T) {
	any := routeOnce(routeReserveDoc("any"), routeFlow("2026-09-10", 20000, "UA0001"))
	none := routeOnce(routeReserveDoc(""), routeFlow("2026-09-10", 20000, "UA0001"))

	if len(any.Legs) != 1 || len(none.Legs) != 1 {
		t.Fatalf("ніг %d і %d, чекали по одній", len(any.Legs), len(none.Legs))
	}
	if any.Legs[0].Reserve == nil || none.Legs[0].Reserve == nil {
		t.Fatal("подушка мовчить там, де політики немає")
	}
	if any.Legs[0].Reserve.AmountUAH != none.Legs[0].Reserve.AmountUAH {
		t.Errorf("порожньо дало %.2f, any — %.2f", none.Legs[0].Reserve.AmountUAH,
			any.Legs[0].Reserve.AmountUAH)
	}
	if none.Legs[0].ReserveSkipWhy != "" {
		t.Errorf("причина там, де ніщо не заблоковане: %q", none.Legs[0].ReserveSkipWhy)
	}
}

// --- плановий дохід у маршруті ---

func planFlow(id int64, name, from string, amountMajor float64) store.PlanFlow {
	return store.PlanFlow{
		ID: id, Name: name, Kind: "income",
		Amount: int64(amountMajor * 100), Currency: money.UAH,
		Cadence: "month", FromDate: domain.Date(from), InvestBP: 10000,
	}
}

// Планова нога є, датована днем НАЙПІЗНІШОГО доходу місяця й підписана
// планом.
//
// День саме найпізніший: раніше за нього місячна сума ще неповна, і
// поставити її першим числом означало б обіцяти гроші, яких того дня немає.
func TestPlanAheadLegIsDatedAndNamed(t *testing.T) {
	src := &sources{planFlows: []store.PlanFlow{
		planFlow(1, "аванс", "2026-01-05", 15000),
		planFlow(2, "зарплата", "2026-01-17", 25000),
	}}
	flows := planAhead(src, routePlans(30000), routeToday, 2)

	// Місяць 0 (серпень) не рахується: обидва дні вже минули, а LeftUAH у
	// routePlans нульовий.
	if len(flows) != 2 {
		t.Fatalf("ніг %d, чекали 2 (вересень і жовтень): %+v", len(flows), flows)
	}
	if flows[0].Date != domain.Date("2026-09-17") {
		t.Errorf("дата %q, чекали 2026-09-17 — день найпізнішого доходу місяця", flows[0].Date)
	}
	if flows[0].Amount != 3000000 {
		t.Errorf("сума %d, чекали 3000000 — PlanUAH місяця", flows[0].Amount)
	}
	if flows[0].Basis != basisPlan {
		t.Errorf("основа %q, чекали %q", flows[0].Basis, basisPlan)
	}
	if flows[0].Ref != "" || flows[0].Kind != "" || flows[0].Principal != 0 {
		t.Errorf("планова нога несе зайве: ref=%q kind=%q principal=%d",
			flows[0].Ref, flows[0].Kind, flows[0].Principal)
	}
}

// Поточний місяць віддає ЛИШЕ НЕДОНЕСЕНЕ.
//
// Інакше маршрут повів би вдруге гроші, які вже принесли: перший раз їх
// порахував підсумок місяця, другий — сам маршрут.
func TestPlanAheadCurrentMonthUsesLeft(t *testing.T) {
	src := &sources{planFlows: []store.PlanFlow{planFlow(1, "зарплата", "2026-01-29", 40000)}}
	plans := routePlans(30000)
	key := monthKeyAt(routeToday, 0)
	plans[key] = &state.MonthPlan{Month: key, PlanUAH: 30000, LeftUAH: 4000}

	flows := planAhead(src, plans, routeToday, 0)
	if len(flows) != 1 {
		t.Fatalf("ніг %d, чекали 1: %+v", len(flows), flows)
	}
	if flows[0].Amount != 400000 {
		t.Errorf("сума %d, чекали 400000 — лишилось закинути, а не весь план", flows[0].Amount)
	}
}

// Планових потоків немає — немає й ніг. Не нуль гривень: «плану доходу
// немає» і «план обіцяє нуль» це різні твердження, і друге тут неправда.
func TestPlanAheadSilentWithoutFlows(t *testing.T) {
	if flows := planAhead(&sources{}, routePlans(30000), routeToday, 12); flows != nil {
		t.Errorf("ноги без плану доходу: %+v", flows)
	}
}

// Планові гроші йдуть у СВІЙ горщик і не складаються з купоном у mono.
//
// Наслідок того, що брокера в них немає, і назвати його треба вголос:
// зарплата ще на картці, а не в брокера, і вдавати протилежне означало б
// обіцяти квиток, який не збереться.
func TestRoutePlanLegHasItsOwnPot(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	plan := routeFlow("2026-09-17", 600, "план місяця")
	plan.Basis = basisPlan

	inc := routeInc("mono", money.UAH, routeFlow("2026-09-10", 600, "UA0001"))
	inc[store.BrokerCur{Broker: noBrokerLabel, Currency: money.UAH}] = []readyFlow{plan}

	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		inc, routePlans(0), allocRates, nil, routeToday)

	if len(got.Legs) != 2 {
		t.Fatalf("ніг %d, чекали 2", len(got.Legs))
	}
	for i, leg := range got.Legs {
		if len(leg.Lines) != 0 {
			t.Errorf("нога %d купила на %d рядків — 600 ₴ квитка за 1000 ₴ не беруть",
				i, len(leg.Lines))
		}
		if leg.CarryInUAH != 0 {
			t.Errorf("нога %d дістала %.2f з чужого горщика", i, leg.CarryInUAH)
		}
	}
}

// --- наскрізь ---

// Плановий дохід доходить до маршруту через справжній обробник.
//
// Наскрізний навмисно, і з тієї самої причини, що й у купона: між planAhead
// і людиною стоять buildMonthPlan, підміна поточного місяця документом і
// домішування в incomeAhead, а кожен із цих кроків уміє віддати порожнечу,
// якої модульний тест не побачить.
func TestRouteEndpointSeesPlanIncome(t *testing.T) {
	srv, _ := testServer(t)
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"40000.00","cadence":"month",`+
			`"from_date":"2024-01-17","invest_pct":"50"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання потоку: %d %s", resp.StatusCode, b)
	}

	_, body := do(t, "GET", srv.URL+"/api/route", "")
	var got routeDoc
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("маршрут не розбирається: %v — %s", err, body)
	}
	var plan *routeLeg
	for i := range got.Legs {
		if got.Legs[i].Basis == basisPlan {
			plan = &got.Legs[i]
			break
		}
	}
	if plan == nil {
		t.Fatalf("планової ноги в маршруті немає: %s", body)
	}
	if plan.Broker != noBrokerLabel {
		t.Errorf("планова нога в брокера %q — планових грошей у брокера ще немає", plan.Broker)
	}
	if plan.Ref != "" {
		t.Errorf("планова нога несе ref %q — кнопка «Прийшло» писала б не в ту таблицю", plan.Ref)
	}
	if plan.InflowUAH <= 0 {
		t.Errorf("планова нога на %.2f ₴ — надходження без грошей не буває", plan.InflowUAH)
	}
}

// Ключ доходить із PUT /api/settings і повертається назад тим самим.
func TestSettingsRoundTripsFillFrom(t *testing.T) {
	srv, _ := testServer(t)
	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"reserve_fill_from":"plan"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("запис: %d %s", resp.StatusCode, b)
	}
	_, body := do(t, "GET", srv.URL+"/api/settings", "")
	if !strings.Contains(body, `"reserve_fill_from":"plan"`) {
		t.Errorf("налаштування не повернулось: %s", body)
	}
	if resp, _ := do(t, "PUT", srv.URL+"/api/settings",
		`{"reserve_fill_from":"plann"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("описку прийнято зі статусом %d — подушку можна вимкнути непоміченим ключем",
			resp.StatusCode)
	}
}

// POST /api/allocate приймає джерело й тіло — і тіло в НАТИВНІЙ валюті.
//
// Гривневе число тут віддало б подушці рівно курс, і помітили б це лише на
// валютній нозі маршруту.
func TestAllocateEndpointTakesSourceAndPrincipal(t *testing.T) {
	srv, _ := testServer(t)
	if resp, b := do(t, "POST", srv.URL+"/api/allocate",
		`{"amount":"5000.00","currency":"UAH","source":"portfolio","principal":"3000.00"}`,
	); resp.StatusCode != http.StatusOK {
		t.Fatalf("розкладка: %d %s", resp.StatusCode, b)
	}
	if resp, _ := do(t, "POST", srv.URL+"/api/allocate",
		`{"amount":"5000.00","principal":"-1"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("відʼємне тіло прийнято зі статусом %d", resp.StatusCode)
	}
}
