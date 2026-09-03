package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
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
			// Стеля джерела дорівнює сумі — тобто «джерело не забороняє
			// нічого»: таблиця перевіряє рівень ПОЛІТИКИ, а дозвіл
			// надходження має власні рядки нижче.
			got := reserveEligibleUAH(c.set, c.src, c.amount, c.principal, c.amount)
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
		routePlans(30000), nil, allocRates, nil, nil, routeToday)
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
		routePlans(30000), nil, allocRates, nil, nil, routeToday)
	if len(got.Legs) != 1 {
		t.Fatalf("ніг %d, чекали 1", len(got.Legs))
	}
	want := allocatePlan(doc, sug, allocRates,
		toMoneyJSON(money.New(500000, money.UAH)), 5000,
		allocAllow{
			ReserveUAH: reserveEligibleUAH(doc.Settings, allocFromPortfolio, 5000, 3000, 5000),
			GoalsUAH:   goalsEligibleUAH(doc.Settings, allocFromPortfolio, 5000, 3000, 5000),
		}, money.UAH, nil)

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

// Планових ніг стільки, скільки потоків платить, і кожна датована СВОЇМ
// днем.
//
// Раніше тут стояла одна нога на місяць, датована днем найпізнішого доходу.
// Ціна була видна на живих даних: чотири надходження ставали одним рядком,
// три з них на екрані не існували, а четверте показувалось учетверо більшим
// за себе. Тепер ділиться те саме PlanUAH, у частках чистого внеску кожного
// потоку, — і сума ніг місяця лишається тим самим числом.
func TestPlanAheadSplitsMonthAcrossFlows(t *testing.T) {
	src := &sources{planFlows: []store.PlanFlow{
		planFlow(1, "аванс", "2026-01-05", 15000),
		planFlow(2, "зарплата", "2026-01-17", 25000),
	}}
	flows := planAhead(src, routePlans(30000), routeToday, 2)

	// Місяць 0 (серпень) не рахується: обидва дні вже минули, а LeftUAH у
	// routePlans нульовий. Далі два місяці по дві ноги.
	if len(flows) != 4 {
		t.Fatalf("ніг %d, чекали 4 (два місяці по два потоки): %+v", len(flows), flows)
	}
	want := []struct {
		date   string
		label  string
		amount int64
	}{
		// 30 000 ₴ місяця в частках 15 000 : 25 000.
		{"2026-09-05", "аванс", 1125000},
		{"2026-09-17", "зарплата", 1875000},
		{"2026-10-05", "аванс", 1125000},
		{"2026-10-17", "зарплата", 1875000},
	}
	for i, w := range want {
		if got := flows[i]; string(got.Date) != w.date || got.Label != w.label ||
			got.Amount != w.amount {
			t.Errorf("нога %d: %s %q %d, чекали %s %q %d",
				i, got.Date, got.Label, got.Amount, w.date, w.label, w.amount)
		}
	}
	if flows[0].Basis != basisPlan {
		t.Errorf("основа %q, чекали %q", flows[0].Basis, basisPlan)
	}
	if flows[0].Ref != "" || flows[0].Kind != "" || flows[0].Principal != 0 {
		t.Errorf("планова нога несе зайве: ref=%q kind=%q principal=%d",
			flows[0].Ref, flows[0].Kind, flows[0].Principal)
	}
}

// Сума ніг місяця дорівнює його плану ДО КОПІЙКИ — і це не косметика.
//
// Від того самого PlanUAH рахується стеля подушки й ділиться місяць між
// видами. Розійшовшись тут на копійку, маршрут почав би обіцяти інші гроші,
// ніж ті, з яких порахована стеля, і побачити це можна було б лише на
// незручних сумах — тобто пізно.
func TestPlanAheadSumEqualsMonthPlan(t *testing.T) {
	src := &sources{planFlows: []store.PlanFlow{
		planFlow(1, "перша", "2026-01-03", 3333.33),
		planFlow(2, "друга", "2026-01-11", 3333.33),
		planFlow(3, "третя", "2026-01-19", 3333.34),
	}}
	const planUAH = 10000.01
	flows := planAhead(src, routePlans(planUAH), routeToday, 12)
	if len(flows) == 0 {
		t.Fatal("ніг немає")
	}
	byMonth := map[string]int64{}
	for _, f := range flows {
		byMonth[string(f.Date)[:7]] += f.Amount
	}
	if len(byMonth) != 12 {
		t.Fatalf("місяців %d, чекали 12", len(byMonth))
	}
	for month, sum := range byMonth {
		if sum != 1000001 {
			t.Errorf("%s: сума ніг %d, чекали 1000001 — план місяця", month, sum)
		}
	}
}

// Поділ зберігає підсумок за будь-яких ваг, і робить це однаково від запуску
// до запуску.
func TestSplitByWeightsConservesTotal(t *testing.T) {
	cases := []struct {
		name  string
		total int64
		w     []float64
	}{
		{"один", 1000001, []float64{1}},
		{"порівну", 1000001, []float64{1, 1}},
		{"чотири", 999997, []float64{1, 1, 1, 1}},
		{"сім різних", 123457, []float64{3, 1, 4, 1, 5, 9, 2}},
		{"з нульовими", 100000, []float64{0, 5, 0, 5}},
		{"домінантна", 100003, []float64{999999, 1}},
		{"усі нульові", 100000, []float64{0, 0}},
		{"нульовий підсумок", 0, []float64{1, 2}},
	}
	for _, c := range cases {
		first := splitByWeights(c.total, c.w)
		var sum int64
		for i, v := range first {
			if v < 0 {
				t.Errorf("%s: відʼємна частка %d", c.name, v)
			}
			if c.w[i] <= 0 && v != 0 {
				t.Errorf("%s: нульова вага взяла %d", c.name, v)
			}
			sum += v
		}
		want := c.total
		if c.name == "усі нульові" {
			// Ділити нема між ким — беззмістовний поділ віддає нулі, а не
			// вигадує собі отримувача.
			want = 0
		}
		if sum != want {
			t.Errorf("%s: сума часток %d, чекали %d (%v)", c.name, sum, want, first)
		}
		for range 8 {
			if got := splitByWeights(c.total, c.w); !slices.Equal(got, first) {
				t.Fatalf("%s: два запуски дали різне: %v проти %v", c.name, got, first)
			}
		}
	}
}

// Потік із нульовою часткою в портфель ноги не дістає.
//
// Саме цей випадок і вирішує, що вага чиста, а не валова: валова віддала б
// цьому потокові шматок PlanUAH, у який він не додав нічого.
func TestPlanAheadZeroShareFlowTakesNothing(t *testing.T) {
	zero := planFlow(2, "бонус", "2026-01-20", 50000)
	zero.InvestBP = 0
	src := &sources{planFlows: []store.PlanFlow{
		planFlow(1, "зарплата", "2026-01-10", 30000),
		zero,
	}}
	flows := planAhead(src, routePlans(30000), routeToday, 1)
	if len(flows) != 1 {
		t.Fatalf("ніг %d, чекали 1: %+v", len(flows), flows)
	}
	if flows[0].Label != "зарплата" || flows[0].Amount != 3000000 {
		t.Errorf("нога %q на %d, чекали зарплату на весь план місяця",
			flows[0].Label, flows[0].Amount)
	}
}

// Поточний місяць пропускає те, що вже минуло й уже відмічене, а нога, що
// лишилась, несе СВОЮ частку — не залишок місяця. Чотири рівні виплати,
// три вже позаду: четверта показує свої 10 000, а не 12 345,67 недонесених
// і не 40 000 плану. Більше за себе саму нога не буває.
func TestPlanAheadCurrentMonthSkipsPastAndMarked(t *testing.T) {
	today := domain.Date("2026-08-16")
	src := &sources{planFlows: []store.PlanFlow{
		planFlow(1, "аванс А", "2026-01-01", 10000),
		planFlow(2, "аванс Б", "2026-01-07", 10000),
		planFlow(3, "зарплата А", "2026-01-15", 10000),
		planFlow(4, "зарплата Б", "2026-01-21", 10000),
	}}
	key := monthKeyAt(today, 0)
	plans := map[string]*state.MonthPlan{key: {
		Month: key, PlanUAH: 40000, PlanReserveUAH: 40000,
		PlanGoalsUAH: 40000, LeftUAH: 12345.67,
	}}

	flows := planAhead(src, plans, today, 0)
	if len(flows) != 1 {
		t.Fatalf("ніг %d, чекали 1 (лишився тільки 21-й день): %+v", len(flows), flows)
	}
	if flows[0].Label != "зарплата Б" || string(flows[0].Date) != "2026-08-21" {
		t.Errorf("нога %q на %s, чекали «зарплата Б» на 2026-08-21",
			flows[0].Label, flows[0].Date)
	}
	if flows[0].Amount != 1000000 {
		t.Errorf("сума %d, чекали 1000000 — своя частка, а не залишок місяця", flows[0].Amount)
	}
}

// Валютний потік важить своїм гривневим еквівалентом — тим самим, яким він
// увійшов у план місяця.
func TestPlanAheadForeignFlowUsesRates(t *testing.T) {
	usd := planFlow(2, "валютна", "2026-01-20", 1000)
	usd.Currency = money.USD
	src := &sources{
		planFlows: []store.PlanFlow{planFlow(1, "гривнева", "2026-01-10", 10000), usd},
		rates:     fx.Rates{money.USD: 400000}, // 40,0000 ₴/$
	}
	flows := planAhead(src, routePlans(50000), routeToday, 1)
	if len(flows) != 2 {
		t.Fatalf("ніг %d, чекали 2: %+v", len(flows), flows)
	}
	// 10 000 ₴ проти 40 000 ₴ — частки 1:4 від 50 000 ₴.
	if flows[0].Amount != 1000000 || flows[1].Amount != 4000000 {
		t.Errorf("частки %d і %d, чекали 1000000 і 4000000",
			flows[0].Amount, flows[1].Amount)
	}
}

// Нога несе ДОЗВІЛ СВОГО ПОТОКУ, і розкладка його читає.
//
// Це той самий аргумент, що заступив у route.go відмову «дозвіл джерела сюди
// не передається»: відколи нога дорівнює потокові, ділити дозвіл нема чого.
func TestRoutePlanLegCarriesItsOwnUses(t *testing.T) {
	onlyInvest := planFlow(1, "лише в папери", "2026-01-05", 20000)
	onlyInvest.Uses = "invest"
	src := &sources{planFlows: []store.PlanFlow{
		onlyInvest,
		planFlow(2, "будь-куди", "2026-01-17", 20000),
	}}
	flows := planAhead(src, routePlans(40000), routeToday, 1)
	if len(flows) != 2 {
		t.Fatalf("ніг %d, чекали 2: %+v", len(flows), flows)
	}
	if flows[0].Uses != "invest" || flows[1].Uses != "" {
		t.Fatalf("дозволи %q і %q, чекали invest і порожній", flows[0].Uses, flows[1].Uses)
	}

	doc := routeReserveDoc("any")
	inc := incomeAhead{store.BrokerCur{Broker: noBrokerLabel, Currency: money.UAH}: flows}
	got := buildRoute(doc, nil, inc, routePlans(40000), nil, allocRates, nil, nil, routeToday)
	if len(got.Legs) != 2 {
		t.Fatalf("ніг маршруту %d, чекали 2", len(got.Legs))
	}
	if got.Legs[0].Reserve != nil {
		t.Errorf("подушка взяла з ноги, якій це заборонено: %+v", got.Legs[0].Reserve)
	}
	if !strings.Contains(got.Legs[0].ReserveSkipWhy, "надходження") {
		t.Errorf("причина не вказує на саме надходження: %q", got.Legs[0].ReserveSkipWhy)
	}
	if got.Legs[1].Reserve == nil || got.Legs[1].Reserve.AmountUAH <= 0 {
		t.Errorf("подушка мовчить на нозі, якій це дозволено: %+v", got.Legs[1].Reserve)
	}
}

// Два потоки з однаковою назвою й одним днем не міняються місцями між
// запусками: сортування стабільне, а зводити їх не можна — дозволи різні.
func TestPlanAheadLegsAreDeterministic(t *testing.T) {
	a := planFlow(1, "зарплата", "2026-01-10", 10000)
	a.Uses = "invest"
	b := planFlow(2, "зарплата", "2026-01-10", 30000)
	src := &sources{planFlows: []store.PlanFlow{a, b}}

	first := planAhead(src, routePlans(40000), routeToday, 3)
	if len(first) == 0 {
		t.Fatal("ніг немає")
	}
	for range 8 {
		got := planAhead(src, routePlans(40000), routeToday, 3)
		if !slices.Equal(got, first) {
			t.Fatalf("два запуски дали різне:\n%+v\n%+v", got, first)
		}
	}
	if first[0].Uses != "invest" {
		t.Errorf("перша нога має дозвіл %q, чекали invest — порядок поплив",
			first[0].Uses)
	}
}

// Поточний місяць несе СВОЮ частку плану, а «лишилось закинути» (LeftUAH)
// ноги не зменшує.
//
// Тут стояло протилежне твердження — «віддає лише недонесене», — і воно
// брехало про виписку: 2 201 ₴, закинуті 1-го числа з інших грошей, урізали
// аванс на 4 500 ₴ до 3 400 ₴. Переказ банку від чужого поповнення меншим
// не стає; меншим стає лише те, що план іще просить, — а це число живе на
// картці плану, не в нозі (довід — у шапці planAhead).
func TestPlanAheadCurrentMonthCarriesOwnShare(t *testing.T) {
	src := &sources{planFlows: []store.PlanFlow{planFlow(1, "зарплата", "2026-01-29", 40000)}}
	plans := routePlans(30000)
	key := monthKeyAt(routeToday, 0)
	// Дозволені суми дорівнюють плану: цей тест про політику, а не про
	// дозвіл джерела (для нього — TestPlanLegCappedByAllowedPlan).
	plans[key] = &state.MonthPlan{Month: key, PlanUAH: 30000,
		PlanReserveUAH: 30000, PlanGoalsUAH: 30000, LeftUAH: 4000}

	flows := planAhead(src, plans, routeToday, 0)
	if len(flows) != 1 {
		t.Fatalf("ніг %d, чекали 1: %+v", len(flows), flows)
	}
	if flows[0].Amount != 3000000 {
		t.Errorf("сума %d, чекали 3000000 — своя частка плану, LeftUAH ноги не ріже",
			flows[0].Amount)
	}
}

// Живий випадок: два рівні потоки ФБК по 4 500 ₴, план 9 000 ₴, 2 201 ₴
// уже закинуто з інших грошей. Обидві ноги — по 4 500 ₴, а не по 3 399,50.
func TestPlanAheadCurrentMonthIgnoresDeposits(t *testing.T) {
	src := &sources{planFlows: []store.PlanFlow{
		planFlow(1, "аванс", "2026-01-28", 4500),
		planFlow(2, "зарплата", "2026-01-30", 4500),
	}}
	plans := routePlans(9000)
	key := monthKeyAt(routeToday, 0)
	plans[key] = &state.MonthPlan{Month: key, PlanUAH: 9000,
		PlanReserveUAH: 9000, PlanGoalsUAH: 9000, LeftUAH: 6799}

	flows := planAhead(src, plans, routeToday, 0)
	if len(flows) != 2 {
		t.Fatalf("ніг %d, чекали 2: %+v", len(flows), flows)
	}
	for _, f := range flows {
		if f.Amount != 450000 {
			t.Errorf("нога %q несе %d, чекали 450000 — чуже поповнення її не ріже",
				f.Label, f.Amount)
		}
	}
}

// Позапланове в ноги не розмазується: воно вже прийшло відміткою «інше» і
// дати не має. План 12 000 із 2 000 позапланових → єдиний потік несе 10 000.
func TestPlanAheadExtraIsNotSpread(t *testing.T) {
	src := &sources{planFlows: []store.PlanFlow{planFlow(1, "зарплата", "2026-01-29", 10000)}}
	plans := routePlans(12000)
	key := monthKeyAt(routeToday, 0)
	plans[key] = &state.MonthPlan{Month: key, PlanUAH: 12000, ExtraUAH: 2000,
		PlanReserveUAH: 12000, PlanGoalsUAH: 12000}

	flows := planAhead(src, plans, routeToday, 0)
	if len(flows) != 1 {
		t.Fatalf("ніг %d, чекали 1: %+v", len(flows), flows)
	}
	if flows[0].Amount != 1000000 {
		t.Errorf("сума %d, чекали 1000000 — без позапланового", flows[0].Amount)
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
		inc, routePlans(0), nil, allocRates, nil, nil, routeToday)

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

// --- дозвіл джерела в маршруті (0041) ---

// Планова нога ріже подушці не більше за ДОЗВОЛЕНУ частину плану.
//
// Стеля джерела приходить сюди числом (month_plan.plan_reserve_uah), а не
// словом «можна». Без цього маршрут обіцяв би подушці гроші, яких сам
// план їй не дає, і сторінка маршруту розійшлася б із карткою резерву.
func TestRoutePlanLegCappedByAllowedPlan(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)},
		&state.Reserve{FillNowUAH: 5000, FillMonthUAH: 5000, GapUAH: 90000})
	// Стеля 100%: питання тесту — скільки дозволяє ПЛАН, а не скільки
	// відрізає частка. Розрив великий, тож обрізати вирізку нічим, крім
	// самого дозволу.
	doc.Settings = routeSettings(20000, 6, 100)
	doc.ReserveUAH = 30000

	plan := routeFlow("2026-09-17", 6000, "план місяця")
	plan.Basis = basisPlan
	inc := incomeAhead{store.BrokerCur{Broker: noBrokerLabel, Currency: money.UAH}: {plan}}

	plans := routePlans(6000)
	key := monthKeyAt(routeToday, 1)
	// Із 6 000 ₴ місяця подушці дозволено лише 1 500: решта — дохід,
	// позначений «не в подушку».
	plans[key] = &state.MonthPlan{Month: key, PlanUAH: 6000,
		PlanReserveUAH: 1500, PlanGoalsUAH: 6000}

	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		inc, plans, nil, allocRates, nil, nil, routeToday)

	if len(got.Legs) != 1 {
		t.Fatalf("ніг %d, чекали 1: %+v", len(got.Legs), got.Legs)
	}
	res := got.Legs[0].Reserve
	if res == nil {
		t.Fatal("вирізки подушки немає зовсім — 1 500 ₴ їй дозволені")
	}
	if res.AmountUAH != 1500 {
		t.Errorf("подушка взяла %v, чекали 1500 — стелю ріже дозволена частина плану",
			res.AmountUAH)
	}
}

// СТЕЛЯ МІСЯЦЯ ЗВʼЯЗУЄ ВСІХ, і купон теж. Наслідок незвичний, тому
// закріплений тестом: місяць, увесь дохід якого позначений «не в
// подушку», не дає їй нічого — навіть із купона, якому політика
// (reserve_fill_from: any) не забороняє нічого.
//
// І це правильно: стеля відповідає на питання «скільки подушці належить
// ЦЬОГО МІСЯЦЯ», а належить їй частка від дозволених грошей місяця. Нуль
// дозволених — нуль частки, і звідки прийшли гроші, цього не змінює.
// Політика й дозвіл ріжуть різні речі: перша — джерело вирізки, другий —
// її розмір.
func TestRouteMonthCeilingBindsCouponToo(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)},
		&state.Reserve{FillNowUAH: 5000, FillMonthUAH: 5000, GapUAH: 90000})
	doc.Settings = routeSettings(20000, 6, 100)
	doc.ReserveUAH = 30000

	inc := routeInc("mono", money.UAH, routeFlow("2026-09-10", 6000, "UA0001"))
	plans := routePlans(6000)
	key := monthKeyAt(routeToday, 1)
	plans[key] = &state.MonthPlan{Month: key, PlanUAH: 6000,
		PlanReserveUAH: 0, PlanGoalsUAH: 6000}

	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		inc, plans, nil, allocRates, nil, nil, routeToday)

	if len(got.Legs) != 1 {
		t.Fatalf("ніг %d, чекали 1", len(got.Legs))
	}
	if res := got.Legs[0].Reserve; res != nil {
		t.Errorf("вирізка %+v — цього місяця подушці не належить нічого", res)
	}
	// Гроші при цьому не зникли: усі пішли в папери.
	if got.Legs[0].AvailUAH != 6000 {
		t.Errorf("доступно %v, чекали всі 6000", got.Legs[0].AvailUAH)
	}
}
