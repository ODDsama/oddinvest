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

// --- каркас ---

var routeToday = domain.Date("2026-08-27")

func fptr(v float64) *float64 { return &v }

// routeDoc-и тут будуються тим самим allocDoc, що й у тестах розкладки, і
// це не економія: маршрут читає з документа РІВНО те саме, що розкладка, і
// другий будівник документа мовчки дозволив би їм розійтись.
func routeSettings(expenses, months, fillSharePct float64) *state.SettingsDoc {
	return &state.SettingsDoc{
		MonthlyExpensesUAH:  fptr(expenses),
		ReserveTargetMonths: fptr(months),
		ReserveFillSharePct: fptr(fillSharePct),
	}
}

// routeFlow — надходження без повернення тіла, тобто чистий купон.
func routeFlow(date string, amountMajor float64, label string) readyFlow {
	return readyFlow{
		Date: domain.Date(date), Amount: int64(amountMajor * 100),
		Label: label, Kind: "bonds",
	}
}

func routeInc(broker, cur string, flows ...readyFlow) incomeAhead {
	return incomeAhead{store.BrokerCur{Broker: broker, Currency: cur}: flows}
}

// routePlans — однаковий план доходу в кожному місяці горизонту.
//
// Однаковий навмисно: тест про стелю подушки перевіряє, що вона
// СКИДАЄТЬСЯ, і різні плани по місяцях сховали б різницю між «скинулась»
// і «порахувалась удруге від іншого числа».
func routePlans(planUAH float64) map[string]*state.MonthPlan {
	out := map[string]*state.MonthPlan{}
	for m := 0; m <= routeHorizonMonths; m++ {
		key := monthKeyAt(routeToday, m)
		out[key] = &state.MonthPlan{Month: key, PlanUAH: planUAH}
	}
	return out
}

// --- головний інваріант ---

// Перша нога маршруту дорівнює розкладці на ту саму суму.
//
// ЦЕ І Є ДОКАЗ, що власної арифметики в маршруті немає жодної: подушка,
// бюджети видів і порядок беруться з allocatePlan, а не рахуються вдруге.
// Той самий прийом, яким закріплено «порожня гіпотеза whatif == /api/summary».
//
// Порівняння через JSON, а не по полях: саме в такому вигляді обидві
// відповіді доходять до браузера, і нове поле, додане лише в один бік,
// мусить це завалити.
//
// ДВІ УМОВИ, і обидві не звуження інваріанта, а його точне формулювання.
// Подія в ПОТОЧНОМУ місяці: у наступному стеля подушки законно
// перераховується з плану того місяця, і розкладка про це не знає, бо їй
// не з чого. Подія — чистий дохід: погашення відкриває розрив свого виду
// ще до розкладки (redeem), і маршрут через це відповість краще за
// /api/allocate на ту саму суму — саме тому, що знає, звідки гроші.
func TestRouteFirstLegEqualsAllocate(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)},
		&state.Reserve{FillNowUAH: 2000, FillMonthUAH: 2000, GapUAH: 40000})
	doc.Settings = routeSettings(10000, 6, 40)
	sug := []suggestion{bondSug("UA0001", 1000, money.UAH)}

	got := buildRoute(doc, sug,
		routeInc("mono", money.UAH, routeFlow("2026-08-28", 5000, "UA0001")),
		routePlans(30000), allocRates, nil, routeToday)

	if len(got.Legs) != 1 {
		t.Fatalf("ніг %d, чекали 1: %+v", len(got.Legs), got)
	}
	want := allocatePlan(doc, sug, allocRates,
		toMoneyJSON(money.New(500000, money.UAH)), 5000, 5000, 5000, money.UAH, nil)

	gotJSON, _ := json.Marshal(got.Legs[0].allocPlan)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("перша нога розійшлася з розкладкою — тобто десь завелась друга арифметика\n"+
			"маршрут:  %s\nрозкладка: %s", gotJSON, wantJSON)
	}
}

// --- стеля подушки ---

// Стеля подушки ЩОМІСЯЦЯ СКИДАЄТЬСЯ, і це найлегша помилка в усьому файлі.
//
// FillNowUAH — не «скільки лишилось до цілі», а частка одного місяця. Прохід
// уперед, який просто зменшував би розрив, віддав би подушці всю стелю з
// першого ж купона й замовк би; прохід, який стелі не поновлює, віддав би їй
// рівно один місяць за весь рік. Правильна відповідь — по стелі щомісяця,
// доки не закриється розрив.
//
// План 30 000 ₴ × 40% = 12 000 ₴ стелі на місяць. Три купони по 20 000 ₴ у
// трьох різних місяцях при розриві 40 000 ₴ мусять дати 12 000 + 12 000 +
// 12 000 = 36 000 ₴.
func TestRouteReserveCeilingResetsEachMonth(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)},
		&state.Reserve{FillNowUAH: 12000, FillMonthUAH: 12000, GapUAH: 40000})
	doc.Settings = routeSettings(10000, 6, 40) // ціль 60 000, стеля 40%
	doc.ReserveUAH = 20000                     // 60 000 − 20 000 = розрив 40 000

	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		routeInc("mono", money.UAH,
			routeFlow("2026-09-10", 20000, "UA0001"),
			routeFlow("2026-10-10", 20000, "UA0001"),
			routeFlow("2026-11-10", 20000, "UA0001")),
		routePlans(30000), allocRates, nil, routeToday)

	if len(got.Legs) != 3 {
		t.Fatalf("ніг %d, чекали 3", len(got.Legs))
	}
	total := 0.0
	for i, leg := range got.Legs {
		if leg.Reserve == nil {
			t.Fatalf("нога %d без вирізки подушки — стеля не поновилась", i)
		}
		if leg.Reserve.AmountUAH != 12000 {
			t.Errorf("нога %d: у подушку %.2f, чекали 12000 (30000 × 40%%)",
				i, leg.Reserve.AmountUAH)
		}
		total += leg.Reserve.AmountUAH
	}
	if total != 36000 {
		t.Errorf("усього в подушку %.2f, чекали 36000 — три місяці по стелі", total)
	}
}

// Дві виплати ОДНОГО місяця ділять одну стелю, а не беруть по стелі кожна.
//
// Дзеркало попереднього тесту: разом вони фіксують, що стеля міряється
// місяцем, а не подією.
func TestRouteReserveCeilingSharedWithinMonth(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)},
		&state.Reserve{FillNowUAH: 12000, FillMonthUAH: 12000, GapUAH: 40000})
	doc.Settings = routeSettings(10000, 6, 40)
	doc.ReserveUAH = 20000

	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		routeInc("mono", money.UAH,
			routeFlow("2026-09-10", 8000, "UA0001"),
			routeFlow("2026-09-20", 8000, "UA0001")),
		routePlans(30000), allocRates, nil, routeToday)

	total := 0.0
	for _, leg := range got.Legs {
		if leg.Reserve != nil {
			total += leg.Reserve.AmountUAH
		}
	}
	if total != 12000 {
		t.Errorf("за місяць у подушку %.2f, чекали 12000 — стеля одна на місяць", total)
	}
}

// Розрив закривається — і подушка замовкає, хай би скільки лишалось стелі.
func TestRouteReserveStopsAtGap(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)},
		&state.Reserve{FillNowUAH: 5000, FillMonthUAH: 5000, GapUAH: 5000})
	doc.Settings = routeSettings(10000, 6, 40)
	doc.ReserveUAH = 55000 // ціль 60 000 → розрив 5 000

	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		routeInc("mono", money.UAH,
			routeFlow("2026-09-10", 20000, "UA0001"),
			routeFlow("2026-10-10", 20000, "UA0001")),
		routePlans(30000), allocRates, nil, routeToday)

	total := 0.0
	for _, leg := range got.Legs {
		if leg.Reserve != nil {
			total += leg.Reserve.AmountUAH
		}
	}
	if total != 5000 {
		t.Errorf("у подушку %.2f, чекали рівно 5000 — більше за розрив класти нема куди", total)
	}
}

// --- накопичення ---

// Дрібні надходження чекають одне одного, доки не набереться цілий квиток.
//
// 340 + 1 200 + 900 при квитку 1 000 ₴: перші дві ноги нічого не купують,
// третя купує два папери й називає, з чого гроші склались.
func TestRoutePoolsUntilWholeTicket(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		routeInc("mono", money.UAH,
			routeFlow("2026-09-10", 340, "UA0001"),
			routeFlow("2026-10-10", 1200, "UA0002"),
			routeFlow("2026-11-10", 900, "UA0003")),
		routePlans(0), allocRates, nil, routeToday)

	if len(got.Legs) != 3 {
		t.Fatalf("ніг %d, чекали 3", len(got.Legs))
	}
	if n := len(got.Legs[0].Lines); n != 0 {
		t.Errorf("перша нога купила %d рядків — на квиток не набралось", n)
	}
	if got.Legs[1].CarryInUAH != 340 {
		t.Errorf("у другу ногу перенесено %.2f, чекали 340", got.Legs[1].CarryInUAH)
	}
	// Надійде саме 1 200 — решта горщика чекала з минулого разу. Це те
	// число, яким нога підписується на екрані, і плутати його з горщиком
	// означало б обіцяти прихід, якого банк не зробить.
	if got.Legs[1].InflowUAH != 1200 {
		t.Errorf("надійде %.2f, чекали 1200 — це подія, а не горщик",
			got.Legs[1].InflowUAH)
	}
	if got.Legs[1].AmountUAH != 1540 {
		t.Errorf("горщик %.2f, чекали 1540 = 340 + 1200", got.Legs[1].AmountUAH)
	}
	// 340 + 1 200 = 1 540 → один папір, 540 лишається.
	if n := len(got.Legs[1].Lines); n != 1 || got.Legs[1].Lines[0].Qty != 1 {
		t.Fatalf("друга нога: %+v — чекали один папір", got.Legs[1].Lines)
	}
	if got.Legs[1].Via == nil {
		t.Error("нога, що витратила гроші з двох надходжень, мусить їх назвати")
	}
	if got.Legs[2].CarryInUAH != 540 {
		t.Errorf("у третю ногу перенесено %.2f, чекали 540", got.Legs[2].CarryInUAH)
	}
	// 540 + 900 = 1 440 → ще один папір.
	if n := len(got.Legs[2].Lines); n != 1 || got.Legs[2].Lines[0].Qty != 1 {
		t.Fatalf("третя нога: %+v — чекали один папір", got.Legs[2].Lines)
	}
}

// Гроші в різних брокерів НЕ складаються: гривня в mono не купить папір в
// inzhur. Ті самі суми, розкидані по двох рахунках, не дають квитка.
func TestRouteSeparatePools(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	inc := incomeAhead{
		store.BrokerCur{Broker: "mono", Currency: money.UAH}: []readyFlow{
			routeFlow("2026-09-10", 600, "UA0001")},
		store.BrokerCur{Broker: "inzhur", Currency: money.UAH}: []readyFlow{
			routeFlow("2026-09-11", 600, "UA0002")},
	}
	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		inc, routePlans(0), allocRates, nil, routeToday)

	for i, leg := range got.Legs {
		if len(leg.Lines) != 0 {
			t.Errorf("нога %d (%s) купила щось на 600 ₴ при квитку 1000 ₴: %+v",
				i, leg.Broker, leg.Lines)
		}
		if leg.CarryInUAH != 0 {
			t.Errorf("нога %d дістала перенос %.2f — горщики різних брокерів злились",
				i, leg.CarryInUAH)
		}
	}
}

// --- перенос стану ---

// Розрив виду ЗМЕНШУЄТЬСЯ від покупки: другі гроші йдуть уже в інший вид.
//
// Без цього маршрут вантажив би весь рік в один вид, бо його розрив
// лишався б таким самим, як сьогодні.
func TestRouteKindDeficitShrinks(t *testing.T) {
	// Ціль порівну, ОВДП порожні, фонди набрані. Перші гроші мусять піти в
	// ОВДП; після великої покупки перевага має перейти до фондів.
	doc := allocDoc([]state.RebalanceRow{
		kindRow("bonds", 50, 0),
		kindRow("funds", 50, 50000),
	}, nil)
	doc.CapitalUAH = 100000

	sug := []suggestion{
		bondSug("UA0001", 1000, money.UAH),
		{Kind: "fund", Label: "ІНЖУР", Currency: money.UAH,
			CostPerBond: toMoneyJSON(money.New(100000, money.UAH)),
			RealPct:     8.1, Reason: "сертифікат"},
	}
	got := buildRoute(doc, sug,
		routeInc("mono", money.UAH,
			routeFlow("2026-09-10", 40000, "UA0001"),
			routeFlow("2026-10-10", 40000, "UA0002")),
		routePlans(0), allocRates, nil, routeToday)

	if len(got.Legs) != 2 {
		t.Fatalf("ніг %d, чекали 2", len(got.Legs))
	}
	bondsIn := func(leg routeLeg) float64 {
		v := 0.0
		for _, l := range leg.Lines {
			if l.Kind == "bond" {
				v += l.TotalUAH
			}
		}
		return v
	}
	first, second := bondsIn(got.Legs[0]), bondsIn(got.Legs[1])
	if first <= 0 {
		t.Fatalf("перші гроші не пішли в порожній вид: %+v", got.Legs[0].Lines)
	}
	if second >= first {
		t.Errorf("в ОВДП пішло %.2f, потім %.2f — розрив виду не зменшився, "+
			"тобто перенос між подіями не працює", first, second)
	}
}

// Повернення тіла НЕ є приростом капіталу, і вид, що погасився, худне.
//
// Купон на 10 000 ₴ і погашення на 10 000 ₴ — це різні події для проходу:
// перша додає капіталу, друга лише перекладає власне з паперу в готівку.
// Без різниці маршрут завищував би капітал і занижував би розрив ОВДП саме
// в місяці погашення.
func TestRoutePrincipalIsNotIncome(t *testing.T) {
	mk := func(principal int64) *routeCarry {
		doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 50000)}, nil)
		doc.CapitalUAH = 100000
		c := newRouteCarry(doc, routeToday)
		// Той самий порядок, що й у проході: тіло виходить із виду ДО
		// розкладки, дохід стає капіталом ПІСЛЯ.
		c.redeem(float64(principal)/100, "bonds")
		c.earn(10000 - float64(principal)/100)
		return c
	}
	coupon := mk(0)
	if coupon.capitalUAH != 110000 {
		t.Errorf("купон: капітал %.2f, чекали 110000 — купон це нові гроші", coupon.capitalUAH)
	}
	if coupon.kindUAH["bonds"] != 50000 {
		t.Errorf("купон: ОВДП %.2f, чекали 50000 — купон паперів не зменшує",
			coupon.kindUAH["bonds"])
	}

	redeem := mk(1000000)
	if redeem.capitalUAH != 100000 {
		t.Errorf("погашення: капітал %.2f, чекали 100000 — тіло лише змінило форму",
			redeem.capitalUAH)
	}
	if redeem.kindUAH["bonds"] != 40000 {
		t.Errorf("погашення: ОВДП %.2f, чекали 40000 — папери погасились",
			redeem.kindUAH["bonds"])
	}
}

// Документ, з якого будується маршрут, НЕ мутується.
//
// Той самий *state.Doc далі читають черга задач і обробник, і мовчки
// просунутий у них капітал був би найгіршим виглядом помилки —
// правдоподібним.
func TestRouteDoesNotMutateDoc(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)},
		&state.Reserve{FillNowUAH: 2000, FillMonthUAH: 2000, GapUAH: 40000})
	doc.Settings = routeSettings(10000, 6, 40)
	before, _ := json.Marshal(doc)

	buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		routeInc("mono", money.UAH,
			routeFlow("2026-09-10", 20000, "UA0001"),
			routeFlow("2026-10-10", 20000, "UA0002")),
		routePlans(30000), allocRates, nil, routeToday)

	after, _ := json.Marshal(doc)
	if string(before) != string(after) {
		t.Errorf("маршрут змінив документ:\nбуло:  %s\nстало: %s", before, after)
	}
}

// --- межі ---

// Горизонт — рік. Виплата за ним у маршрут не потрапляє.
func TestRouteHorizonIsTwelveMonths(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		routeInc("mono", money.UAH,
			routeFlow("2027-08-20", 5000, "UA0001"),  // у межах
			routeFlow("2027-09-10", 5000, "UA0002")), // за межею
		routePlans(0), allocRates, nil, routeToday)

	if len(got.Legs) != 1 {
		t.Fatalf("ніг %d, чекали 1 — за горизонтом маршрут мовчить: %+v", len(got.Legs), got)
	}
	if got.To != "2027-08-27" {
		t.Errorf("горизонт %q, чекали 2027-08-27", got.To)
	}
}

// Порожній маршрут повертає названу причину, а не мовчазний нуль.
func TestRouteEmptyHasReason(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	got := buildRoute(doc, nil, incomeAhead{}, routePlans(0),
		allocRates, nil, routeToday)

	if len(got.Legs) != 0 {
		t.Fatalf("ніг %d, чекали 0", len(got.Legs))
	}
	if got.Note == "" {
		t.Error("порожня відповідь без причини читається як поломка")
	}
	if got.Legs == nil {
		t.Error("legs мусить бути [] а не null — інакше браузер отримає null.length")
	}
}

// Два прогони на тих самих даних дають той самий маршрут.
//
// incomeAhead — мапа, а Go обходить її в довільному порядку. Без повного
// ключа сортування дві виплати одного дня в різних брокерів мінялися б
// місцями, а разом із ними — і те, кому дістанеться стеля подушки.
func TestRouteDeterministic(t *testing.T) {
	inc := incomeAhead{}
	for _, b := range []string{"mono", "inzhur", "privat", "sense"} {
		inc[store.BrokerCur{Broker: b, Currency: money.UAH}] = []readyFlow{
			routeFlow("2026-09-10", 12000, "UA0001")}
	}
	run := func() string {
		doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)},
			&state.Reserve{FillNowUAH: 12000, FillMonthUAH: 12000, GapUAH: 40000})
		doc.Settings = routeSettings(10000, 6, 40)
		doc.ReserveUAH = 20000
		b, _ := json.Marshal(buildRoute(doc,
			[]suggestion{bondSug("UA0001", 1000, money.UAH)},
			inc, routePlans(30000), allocRates, nil, routeToday))
		return string(b)
	}
	first := run()
	for i := 0; i < 8; i++ {
		if got := run(); got != first {
			t.Fatalf("прогін %d розійшовся з першим:\n%s\n%s", i+2, first, got)
		}
	}
}

// --- крізь HTTP ---

// Порожня база віддає 200 і порожній СПИСОК, а не null і не 500.
//
// null тут дорожчий за здається: у браузері він падає на legs.length
// усередині рендера, тобто сторінка гасне цілком, і причина в консолі
// виглядає як помилка розмітки, а не як порожній портфель.
func TestRouteEndpointEmptyDB(t *testing.T) {
	srv, _ := testServer(t)
	resp, body := do(t, "GET", srv.URL+"/api/route", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("маршрут: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `"legs":[]`) {
		t.Errorf("порожній маршрут мусить нести legs:[], а несе: %s", body)
	}
	if !strings.Contains(body, `"note":`) {
		t.Errorf("порожня відповідь без причини читається як поломка: %s", body)
	}
}

// Живий портфель: купон із довідника НБУ доходить до маршруту й веде туди,
// куди веде політика.
//
// Тест наскрізний навмисно — між buildRoute і людиною стоять ще чотири
// кроки (buildState, рейтинг, джерела, futureIncome), і кожен із них уміє
// віддати порожнечу, яку модульний тест не побачить.
func TestRouteEndpointSeesScheduledCoupon(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":50,"price_per_bond":"995.00",`+
			`"buy_date":"2026-07-01","channel":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, body)
	}

	_, body := do(t, "GET", srv.URL+"/api/route", "")
	var got routeDoc
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("маршрут не розбирається: %v — %s", err, body)
	}
	if len(got.Legs) == 0 {
		t.Fatalf("купон 2026-09-16 не дійшов до маршруту: %s", body)
	}
	prev := ""
	for i, leg := range got.Legs {
		if leg.Date < prev {
			t.Errorf("нога %d датована %s після %s — маршрут іде не в часі", i, leg.Date, prev)
		}
		prev = leg.Date
		if leg.Broker != "mono" {
			t.Errorf("нога %d у брокера %q, чекали mono — купон кредитує рахунок покупки",
				i, leg.Broker)
		}
		if leg.AmountUAH <= 0 {
			t.Errorf("нога %d на %.2f ₴ — надходження без грошей не буває", i, leg.AmountUAH)
		}
	}
	if got.From == "" || got.To == "" {
		t.Errorf("маршрут без названих меж горизонту: %s", body)
	}
}

// --- основа надходження ---

// Основа їде з подією й називається словом, а не порожнім рядком.
func TestRouteLegNamesItsBasis(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	div := routeFlow("2026-09-10", 500, "Inzhur REIT")
	div.Kind, div.Basis = "funds", basisEstimate

	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		routeInc("inzhur", money.UAH, div), routePlans(0), allocRates, nil, routeToday)

	if len(got.Legs) != 1 {
		t.Fatalf("ніг %d, чекали 1", len(got.Legs))
	}
	if got.Legs[0].Basis != basisEstimate {
		t.Errorf("основа %q, чекали %q — оцінка мусить лишатись видимою",
			got.Legs[0].Basis, basisEstimate)
	}
}

// Зобовʼязання називається СЛОВОМ, а не порожнечею.
//
// futureIncome лишає Basis порожнім, і це правильно всередині: інших основ
// вона не знає. Але порожня комірка в колонці «основа» читається як
// «невідомо», а не як «портфель це винен», тож назовні воно мусить бути
// названим.
func TestRouteOwedBasisIsNamed(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		routeInc("mono", money.UAH, routeFlow("2026-09-10", 500, "UA0001")),
		routePlans(0), allocRates, nil, routeToday)

	if got.Legs[0].Basis != basisOwed {
		t.Errorf("основа %q, чекали %q", got.Legs[0].Basis, basisOwed)
	}
}

// Горщик, у якому зійшлись зобовʼязання й оцінка, каже про це прямо.
//
// ЦЕ І Є ТЕ, ЩО ДОЗВОЛЯЄ МАРШРУТУ ПОКАЗУВАТИ ОЦІНКИ там, де дата «коли
// вистачить» їх не показує: припущення не зникає в спільному числі, воно
// лишається підписаним. Купон 600 ₴ і дивіденд 600 ₴ на одному рахунку
// складаються в квиток за 1 000 ₴ — і ця нога basisMixed, а не basisOwed.
func TestRouteMixedPotSaysSo(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	div := routeFlow("2026-10-10", 600, "Inzhur REIT")
	div.Kind, div.Basis = "funds", basisEstimate

	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		routeInc("inzhur", money.UAH,
			routeFlow("2026-09-10", 600, "UA0001"), div),
		routePlans(0), allocRates, nil, routeToday)

	if len(got.Legs) != 2 {
		t.Fatalf("ніг %d, чекали 2", len(got.Legs))
	}
	if got.Legs[0].Basis != basisOwed {
		t.Errorf("перша нога: основа %q, чекали %q", got.Legs[0].Basis, basisOwed)
	}
	if got.Legs[1].Basis != basisMixed {
		t.Errorf("друга нога: основа %q, чекали %q — у горщику зійшлись купон і оцінка",
			got.Legs[1].Basis, basisMixed)
	}
	if len(got.Legs[1].Lines) != 1 {
		t.Fatalf("600 + 600 мали скластись у папір за 1000: %+v", got.Legs[1].Lines)
	}
}

// Спорожнілий горщик не переносить чужу основу на наступні гроші.
func TestRoutePotBasisResetsWhenEmpty(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	div := routeFlow("2026-09-10", 1000, "Inzhur REIT")
	div.Kind, div.Basis = "funds", basisEstimate

	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		routeInc("inzhur", money.UAH, div,
			routeFlow("2026-10-10", 1000, "UA0001")),
		routePlans(0), allocRates, nil, routeToday)

	if got.Legs[0].Basis != basisEstimate {
		t.Fatalf("перша нога: основа %q, чекали %q", got.Legs[0].Basis, basisEstimate)
	}
	if got.Legs[0].RestUAH != 0 {
		t.Fatalf("перша нога мала витратити все: залишок %.2f", got.Legs[0].RestUAH)
	}
	if got.Legs[1].Basis != basisOwed {
		t.Errorf("друга нога: основа %q, чекали %q — оцінка вже пішла в діло",
			got.Legs[1].Basis, basisOwed)
	}
}

// Брокер оціненого дивіденду — мажоритарний, а нічия дає «—».
//
// Точної відповіді немає в принципі: операція фонду несе брокера, виплата
// ні. Ділити оцінку пропорційно означало б розбити її на суми, менші за
// будь-який квиток, тобто зробити маршрут гіршим заради видимості точності.
func TestFundBrokerIsMajorityAndTieIsNobody(t *testing.T) {
	ops := []domain.FundOp{
		{Fund: "REIT", Kind: domain.FundBuy, Broker: "inzhur", Amount: 300_00},
		{Fund: "REIT", Kind: domain.FundBuy, Broker: "mono", Amount: 100_00},
		// Продаж не рахується: питання «де його купували», а не «скільки лишилось».
		{Fund: "REIT", Kind: domain.FundSell, Broker: "mono", Amount: 900_00},
	}
	if got := fundBroker(ops, "REIT"); got != "inzhur" {
		t.Errorf("брокер %q, чекали inzhur — там куплено більше", got)
	}
	tie := []domain.FundOp{
		{Fund: "REIT", Kind: domain.FundBuy, Broker: "inzhur", Amount: 200_00},
		{Fund: "REIT", Kind: domain.FundBuy, Broker: "mono", Amount: 200_00},
	}
	if got := fundBroker(tie, "REIT"); got != noBrokerLabel {
		t.Errorf("нічия дала %q, чекали %q — вгадувати навмання не можна",
			got, noBrokerLabel)
	}
	if got := fundBroker(nil, "REIT"); got != noBrokerLabel {
		t.Errorf("без операцій %q, чекали %q", got, noBrokerLabel)
	}
}

// --- закріплення ---

// Закріпити можна лише те, про що сьогоднішня ціна ще щось каже.
//
// Сторінка сама пише, що ціна кроку тут сьогоднішня; закріпити конкретний
// ISIN на липень наступного року означало б зафіксувати рівно те число, про
// яке ми щойно сказали «ми його не знаємо». Вікно те саме taskSoonDays, що
// й у черги задач.
func TestAnnotatePlannedPinWindow(t *testing.T) {
	line := allocLine{Kind: "bond", Ref: "UA0001", Addable: true}
	legs := []routeLeg{
		{Date: "2026-09-10", Broker: "mono", Currency: money.UAH,
			allocPlan: allocPlan{Lines: []allocLine{line}}},
		{Date: "2027-06-10", Broker: "mono", Currency: money.UAH,
			allocPlan: allocPlan{Lines: []allocLine{line}}},
	}
	annotatePlanned(legs, nil, routeToday)

	if !legs[0].Pinnable {
		t.Error("нога за два тижні мусить закріплюватись")
	}
	if legs[1].Pinnable {
		t.Error("нога через рік не мусить: її ціна сьогодні невідома")
	}
}

// Нога без жодного рядка, що кладеться в план, кнопки не дістає.
//
// У вкладу такого рядка немає взагалі (allocLine.Addable), і кнопка, яка
// нічого не записує, гірша за її відсутність.
func TestAnnotatePlannedNeedsAddableLine(t *testing.T) {
	legs := []routeLeg{
		{Date: "2026-09-10", Broker: "ПУМБ", Currency: money.UAH,
			allocPlan: allocPlan{Lines: []allocLine{{Kind: "deposit", Addable: false}}}},
		{Date: "2026-09-11", Broker: "mono", Currency: money.UAH,
			allocPlan: allocPlan{}},
	}
	annotatePlanned(legs, nil, routeToday)
	for i, leg := range legs {
		if leg.Pinnable {
			t.Errorf("нога %d закріплюється, хоч класти в план нічого: %+v", i, leg.Lines)
		}
	}
}

// Уже закріплене видно — і кнопка зникає.
//
// ЗБІГ ЗА ТРЬОМА ПОЛЯМИ, а не самою датою: 28 жовтня в живому портфелі три
// різні ноги в двох брокерів, і позначка на всіх трьох через один
// закріплений рядок читалась би як «усе вирішено».
func TestAnnotatePlannedMatchesDateBrokerCurrency(t *testing.T) {
	line := allocLine{Kind: "bond", Ref: "UA0001", Addable: true}
	legs := []routeLeg{
		{Date: "2026-09-10", Broker: "mono", Currency: money.UAH,
			allocPlan: allocPlan{Lines: []allocLine{line}}},
		{Date: "2026-09-10", Broker: "inzhur", Currency: money.UAH,
			allocPlan: allocPlan{Lines: []allocLine{line}}},
	}
	annotatePlanned(legs, []store.PlanBuy{
		{Kind: "bond", Ref: "UA0001", BuyDate: "2026-09-10",
			Broker: "mono", Currency: money.UAH},
		// Валюта в рядку не названа — «вивести із сутності». Такий рядок
		// мусить збігтись: інакше власне закріплення, зроблене кнопкою
		// «Закріпити», сторінка не побачила б ніколи (спіймано вживу).
		{Kind: "npf", Ref: "1", BuyDate: "2026-09-10", Broker: "mono"},
		// «Купую зараз» дати не має й до жодної ноги не належить.
		{Kind: "bond", Ref: "UA0002", Broker: "mono", Currency: money.UAH},
		// Чужа валюта на тій самій даті й рахунку — не про цю ногу.
		{Kind: "bond", Ref: "UA0003", BuyDate: "2026-09-10",
			Broker: "mono", Currency: money.USD},
	}, routeToday)

	if len(legs[0].Planned) != 2 ||
		legs[0].Planned[0] != "UA0001" || legs[0].Planned[1] != "1" {
		t.Errorf("нога mono: у плані %v, чекали [UA0001 1] — рядок без валюти "+
			"теж належить цій нозі, а доларовий ні", legs[0].Planned)
	}
	if legs[0].Pinnable {
		t.Error("закріплена нога не мусить пропонувати закріпитись удруге")
	}
	if len(legs[1].Planned) != 0 {
		t.Errorf("нога inzhur дістала чужий рядок плану: %v — збіг мусить бути "+
			"за датою, рахунком І валютою", legs[1].Planned)
	}
	if !legs[1].Pinnable {
		t.Error("незакріплена нога того ж дня мусить лишатись закріплюваною")
	}
}
