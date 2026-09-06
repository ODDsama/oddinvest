package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/store"
)

// planServer — портфель whatIfServer плюс усе, без чого ЦІЛІ мовчать:
// витрати (з них береться поріг незалежності), ціль капіталу з дедлайном
// і ставка гривневого вкладу. Без них independence і forecast — nil, і
// половина тестів нижче перевіряла б відсутність полів.
func planServer(t *testing.T) (string, *store.Store) {
	t.Helper()
	ctx := context.Background()
	srv, st := testServer(t)
	seed(t, st)
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: domain.NewDate(time.Now()), Amount: 1_000_000_00,
		Currency: money.UAH, Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddLot(ctx, domain.Lot{
		ISIN: "UA4000227748", Qty: 5, PricePerBond: money.New(99500, money.UAH),
		BuyDate: domain.NewDate(time.Now().AddDate(0, 0, -10)), Channel: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"monthly_expenses_uah": "30000",
		// Ціль навмисно НЕДОСЯЖНА за десять років: при досяжній goal_pct
		// упирається в 100% і перестає бути чутливою — тест тоді перевіряв
		// би стелю, а не те, чи доїхав замок до прогнозу.
		"goal_amount_uah":      "100000000",
		"goal_date":            time.Now().AddDate(10, 0, 0).Format("2006-01-02"),
		"deposit_rate_uah_pct": "14",
	} {
		if err := st.SetSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	return srv.URL, st
}

// seedCatalogFund — фонд у ДОВІДНИКУ з видом, обіцянкою й податками.
//
// Довідник наповнюється операцією (інакше рядка фонду просто немає), а
// вид і обіцянка дописуються правкою: окремого «створити фонд» у сховищі
// немає навмисно — фонд існує рівно доти, доки є його операції.
//
// Операція навмисно ПРОДАНА назад тим самим днем: тест міряє планований
// фонд, якого в портфелі ще немає, і залишок позиції зсував би капітал.
func seedCatalogFund(t *testing.T, st *store.Store, name, kind string,
	yieldBP int64, closeDate string) {

	t.Helper()
	ctx := context.Background()
	day := domain.NewDate(time.Now().AddDate(0, 0, -20))
	for _, op := range []domain.FundOp{
		{Date: day, Fund: name, Kind: domain.FundBuy,
			Qty: 1, Amount: 100, Currency: money.UAH, Broker: "mono"},
		{Date: day, Fund: name, Kind: domain.FundSell,
			Qty: 1, Amount: 100, Currency: money.UAH, Broker: "mono"},
	} {
		if _, err := st.AddFundOp(ctx, op); err != nil {
			t.Fatal(err)
		}
	}
	funds, err := st.ListFunds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range funds {
		if f.Name != name {
			continue
		}
		f.Kind, f.ExpectedYieldBP, f.CloseDate = kind, yieldBP, closeDate
		f.Currency = money.UAH
		if err := st.RenameFund(ctx, f.ID, f); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("фонд %q не зʼявився в довіднику", name)
}

// seedHeldFund — фонд, який СПРАВДІ лежить у портфелі: qty > 0 і відома
// ціна.
//
// ДЗЕРКАЛО seedCatalogFund, і вся різниця в ньому: той навмисно продає
// куплене назад, бо міряє фонд, якого ще немає. Тут потрібне протилежне —
// саме наявний залишок і був множником фантомного капіталу.
//
// ЦІНА З ЧОТИРМА ЗНАКАМИ — НЕСУЧА, а не просто «схожа на справжню».
// 1 973 500 коп на 1738 сертифікатів дає 11,3550 ₴, і саме ці чотири
// знаки не влазять у копійку, якою ходить planBuyFundPrice. На круглій
// ціні копійчаний канал занулився б, і тест на нього пройшов би й на
// несправному коді.
const (
	heldFundQty    int64 = 1738
	heldFundAmount int64 = 1_973_500 // 11,3550 ₴ за сертифікат
)

func seedHeldFund(t *testing.T, st *store.Store, name string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: domain.NewDate(time.Now().AddDate(0, 0, -20)), Fund: name,
		Kind: domain.FundBuy, Qty: heldFundQty, Amount: heldFundAmount,
		Currency: money.UAH, Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	funds, err := st.ListFunds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range funds {
		if f.Name != name {
			continue
		}
		f.Currency = money.UAH
		if err := st.RenameFund(ctx, f.ID, f); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("фонд %q не зʼявився в довіднику", name)
}

// fundRow / fundsView — те, що треба знати про позицію фонду, щоб міряти
// дельти. Іменованим типом, а не анонімною структурою: він повертається
// з fund() і читається в кожному тесті нижче.
type fundRow struct {
	Fund          string  `json:"fund"`
	Qty           int64   `json:"qty"`
	LastPrice     float64 `json:"last_price"`
	LastPriceDate string  `json:"last_price_date"`
	PriceMarked   bool    `json:"price_marked"`
	PriceStale    bool    `json:"price_stale"`
	MarketValue   float64 `json:"market_value"`
}

type fundsView struct {
	CapitalUAH float64   `json:"capital_uah"`
	Funds      []fundRow `json:"funds"`
	Rebalance  []struct {
		Dimension  string  `json:"dimension"`
		Key        string  `json:"key"`
		CurrentUAH float64 `json:"current_uah"`
	} `json:"rebalance"`
}

func fundsOf(t *testing.T, raw string) fundsView {
	t.Helper()
	var v fundsView
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (v fundsView) fund(t *testing.T, name string) fundRow {
	t.Helper()
	for _, f := range v.Funds {
		if f.Fund == name {
			return f
		}
	}
	t.Fatalf("позиції %q немає в документі", name)
	return fundRow{}
}

func (v fundsView) kindUAH(key string) float64 {
	for _, r := range v.Rebalance {
		if r.Dimension == "kind" && r.Key == key {
			return r.CurrentUAH
		}
	}
	return 0
}

// planFundBody — рядок плану на купівлю сертифіката через N місяців.
// Ціна за штуку задається явно: фонда в портфелі немає, тож узяти її
// нема звідки (state_plan_buys.go).
func planFundBody(name string, months int) string {
	when := time.Now().AddDate(0, months, 0).Format("2006-01-02")
	return `{"draft":[{"kind":"fund","ref":"` + name + `","qty":1000,` +
		`"unit_price":"100","currency":"UAH","broker":"mono","buy_date":"` + when + `"}]}`
}

func whatIf(t *testing.T, url, body string) (int, string) {
	t.Helper()
	resp, out := do(t, "POST", url+"/api/whatif", body)
	return resp.StatusCode, out
}

// stripVolatile — те саме, що в TestWhatIfEmptyPlanMatchesSummary: два
// поля, які рухаються самі, прибираємо, решта мусить збігатись до символу.
func stripVolatile(t *testing.T, s string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "generated_at")
	delete(m, "tasks")
	// idle_cost — порада, як і tasks (довід у handlers_whatif_test.go).
	delete(m, "idle_cost")
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func afterOf(t *testing.T, body string) string {
	t.Helper()
	var got struct {
		After json.RawMessage `json:"after"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	return string(got.After)
}

// Порожній ЗБЕРЕЖЕНИЙ план — той самий інваріант, що й порожня чернетка.
// Обидві форми тримаються окремо: перша перевіряє прийом гіпотези, друга —
// що читання порожньої таблиці нічого не домішує.
func TestWhatIfEmptySavedPlanMatchesSummary(t *testing.T) {
	url, _ := planServer(t)
	_, summary := do(t, "GET", url+"/api/summary", "")
	code, body := whatIf(t, url, `{}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if a, b := stripVolatile(t, summary), stripVolatile(t, afterOf(t, body)); a != b {
		t.Errorf("порожній збережений план змінив стан:\n%s\n%s", a, b)
	}
}

// Збережений рядок і та сама чернетка мусять дати БАЙТ У БАЙТ те саме.
// Це тотожність, яка не дає двом шляхам розійтись: превʼю під час
// введення показувало б одне, а збережений план — інше, і жодне з двох
// не було б очевидно неправильним.
func TestWhatIfSavedPlanEqualsSameDraft(t *testing.T) {
	url, st := planServer(t)
	const draft = `{"kind":"bond","ref":"UA4000227748","qty":3,"broker":"mono"}`
	code, asDraft := whatIf(t, url, `{"saved":false,"draft":[`+draft+`]}`)
	if code != http.StatusOK {
		t.Fatalf("чернетка: %d %s", code, asDraft)
	}
	if _, err := st.AddPlanBuy(context.Background(), store.PlanBuy{
		Kind: store.BuyBond, Ref: "UA4000227748", Qty: 3, Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	code, asSaved := whatIf(t, url, `{}`)
	if code != http.StatusOK {
		t.Fatalf("збережений: %d %s", code, asSaved)
	}
	if a, b := stripVolatile(t, afterOf(t, asDraft)), stripVolatile(t, afterOf(t, asSaved)); a != b {
		t.Errorf("чернетка й збережений рядок дали різне:\n%s\n%s", a, b)
	}
}

// Правка рядка — це «виключити збережений і додати чернетку», і вона
// мусить збігтись із набором, у якому виправлений рядок просто стоїть
// замість старого. Без цієї рівності превʼю правки показувало б стан,
// якого після збереження не буде.
func TestWhatIfExcludeReproducesEdit(t *testing.T) {
	url, st := planServer(t)
	ctx := context.Background()
	a := store.PlanBuy{Kind: store.BuyBond, Ref: "UA4000227748", Qty: 1, Broker: "mono"}
	b := store.PlanBuy{Kind: store.BuyBond, Ref: "UA4000227748", Qty: 2, Broker: "mono"}
	if _, err := st.AddPlanBuy(ctx, a); err != nil {
		t.Fatal(err)
	}
	idB, err := st.AddPlanBuy(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	const edited = `{"kind":"bond","ref":"UA4000227748","qty":7,"broker":"mono"}`
	code, viaExclude := whatIf(t, url,
		`{"exclude":[`+itoa(int(idB))+`],"draft":[`+edited+`]}`)
	if code != http.StatusOK {
		t.Fatalf("виключення: %d %s", code, viaExclude)
	}
	code, viaDraft := whatIf(t, url,
		`{"saved":false,"draft":[{"kind":"bond","ref":"UA4000227748","qty":1,"broker":"mono"},`+edited+`]}`)
	if code != http.StatusOK {
		t.Fatalf("чернетки: %d %s", code, viaDraft)
	}
	if x, y := stripVolatile(t, afterOf(t, viaExclude)), stripVolatile(t, afterOf(t, viaDraft)); x != y {
		t.Errorf("правка через exclude розійшлась із прямим набором:\n%s\n%s", x, y)
	}
}

type goalsView struct {
	CapitalUAH  float64                       `json:"capital_uah"`
	NominalUAH  float64                       `json:"nominal_uah_eq"`
	DepositsUAH float64                       `json:"deposits_uah"`
	USDSharePct float64                       `json:"usd_share_pct"`
	NPFUAH      float64                       `json:"npf_uah"`
	Brokers     map[string]map[string]float64 `json:"brokers"`
	// MonthTargetUAH — скільки треба вносити щомісяця, щоб дійти до цілі.
	// САМЕ це число рухається від покупки, а не forecast.goal_pct: той у
	// реалістичному сценарії дорівнює 100 ЗА ПОБУДОВОЮ (внесок виводиться
	// з цілі бісекцією, тож прогноз завжди сходиться рівно на ціль). Тест,
	// написаний на goal_pct, перевіряв би стелю, а не вплив.
	MonthTargetUAH float64 `json:"month_target_uah"`
	Reserve        *struct {
		UAH    float64 `json:"uah"`
		Months float64 `json:"months"`
	} `json:"reserve"`
	Independence *struct {
		PlanMonths int     `json:"plan_months"`
		PlanDate   string  `json:"plan_date"`
		CapitalUAH float64 `json:"capital_uah"`
	} `json:"independence"`
}

func goalsOf(t *testing.T, raw string) goalsView {
	t.Helper()
	var v goalsView
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// ГОЛОВНИЙ тест усього рішення про майбутню дату.
//
// Покупка, запланована на наступний рік, не має права зрушити жодне
// СЬОГОДНІШНЄ число — інакше екран показував би папір, якого ще немає, у
// знаменнику валютних часток, і капітал, за який ще не заплачено. Але
// зрушити ЦІЛІ вона мусить: саме заради цього рядок і має дату.
func TestWhatIfFutureRowDoesNotMoveToday(t *testing.T) {
	url, _ := planServer(t)
	_, summary := do(t, "GET", url+"/api/summary", "")
	before := goalsOf(t, summary)

	when := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	code, body := whatIf(t, url, `{"draft":[{"kind":"deposit","ref":"privat",`+
		`"amount":"300000","currency":"UAH","months":12,"rate_pct":"16","buy_date":"`+when+`"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	after := goalsOf(t, afterOf(t, body))

	if after.CapitalUAH != before.CapitalUAH {
		t.Errorf("капітал зрушив на %.2f — майбутня покупка потрапила в портфель",
			after.CapitalUAH-before.CapitalUAH)
	}
	if after.DepositsUAH != before.DepositsUAH {
		t.Errorf("вклади зрушили на %.2f", after.DepositsUAH-before.DepositsUAH)
	}
	if after.USDSharePct != before.USDSharePct {
		t.Errorf("валютна частка зрушила на %.4f в.п.", after.USDSharePct-before.USDSharePct)
	}
	if !sameBrokers(before.Brokers, after.Brokers) {
		t.Errorf("готівка брокерів зрушила: %+v проти %+v", before.Brokers, after.Brokers)
	}
	// А ЦІЛІ — мусять. Обидві: скільки треба вносити щомісяця, щоб дійти
	// до цілі капіталу, і скільки капіталу стоятиме за точкою
	// незалежності. Якщо не зрушила жодна — замок не доїхав до прогнозу,
	// і вся різниця між «зараз» і «потім» звелась до нічого.
	if before.MonthTargetUAH == 0 {
		t.Fatal("місячного плану немає — ціль і дедлайн не задані?")
	}
	if after.MonthTargetUAH == before.MonthTargetUAH {
		t.Errorf("місячний план не зрушив (%.2f) — замок не доїхав до прогнозу",
			after.MonthTargetUAH)
	}
	if before.Independence == nil || after.Independence == nil {
		t.Fatal("точки незалежності немає — витрати не задані?")
	}
	if after.Independence.CapitalUAH == before.Independence.CapitalUAH {
		t.Errorf("капітал у точці незалежності не зрушив (%.2f)",
			after.Independence.CapitalUAH)
	}
}

// ГОЛОВНИЙ ТЕСТ КОМІТА: планована купівля хорошого фонду не сміє
// ПОГІРШУВАТИ прогноз.
//
// Доти вона це робила. Накопичувальний фонд у плані прикидався замком, а
// тіло замка не росте (domain/projection.go: компаундиться лише
// invested) — гроші виймались із пулу реінвесту, де вони працювали за
// ставкою рукава, і клались туди, де вони лежать. Купівля фонду, який
// обіцяє БІЛЬШЕ за рукав, робила «треба вносити щомісяця» більшим.
//
// Напрямок тут перевіряється навмисно, а не сама лише нерівність: усі
// наявні тести майбутнього рядка питали `!=`, і помилка знаку прожила б
// під ними скільки завгодно.
func TestWhatIfPlannedFundBuyDoesNotWorsenForecast(t *testing.T) {
	url, st := planServer(t)
	seedCatalogFund(t, st, "Накопичувальний", store.FundAccumulating, 3000, "")

	_, summary := do(t, "GET", url+"/api/summary", "")
	before := goalsOf(t, summary)
	if before.MonthTargetUAH == 0 {
		t.Fatal("місячного плану немає — ціль і дедлайн не задані?")
	}

	code, body := whatIf(t, url, planFundBody("Накопичувальний", 6))
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	after := goalsOf(t, afterOf(t, body))
	if after.MonthTargetUAH >= before.MonthTargetUAH {
		t.Errorf("покупка фонду під 30%% не здешевила місячний план: %.2f → %.2f",
			before.MonthTargetUAH, after.MonthTargetUAH)
	}
}

// ТЕСТ, ЯКИЙ РОЗРІЗНЯЄ ДВІ МОДЕЛІ, і єдиний, що це вміє.
//
// За ОДНАКОВОЇ обіцяної ставки накопичувальний фонд мусить дати більше за
// розподільний. Це не домовленість, а арифметика: накопичувальний
// компаундить усередині себе (accum.go), а розподільний платить простий
// купон від тіла, яке не росте (Dist, і замок як його модель). За 30% на
// шість років різниця виходить у рази.
//
// Доти обидва йшли ОДНИМ каналом — замком, — тобто накопичувальному
// приписувалась чужа, гірша механіка. На цій фікстурі це коштувало 73%
// користі від покупки: місячний план дешевшав на 898 ₴ замість 3282 ₴.
// Напрямок при цьому лишався правильним в обох випадках, і саме тому
// перевірка знаку тут нічого не ловить — потрібне порівняння двох видів.
func TestWhatIfAccumulatingFundBeatsDistributingAtSameRate(t *testing.T) {
	url, st := planServer(t)
	seedCatalogFund(t, st, "Накопичувальний", store.FundAccumulating, 3000, "")
	seedCatalogFund(t, st, "Розподільний", store.FundDistributing, 3000, "")

	target := func(name string) float64 {
		t.Helper()
		code, body := whatIf(t, url, planFundBody(name, 6))
		if code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, code, body)
		}
		return goalsOf(t, afterOf(t, body)).MonthTargetUAH
	}
	accum, dist := target("Накопичувальний"), target("Розподільний")
	if accum == 0 || dist == 0 {
		t.Fatal("місячного плану немає — ціль і дедлайн не задані?")
	}
	// Менший місячний план = більша користь від покупки.
	if accum >= dist {
		t.Errorf("накопичувальний не переграв розподільного за тієї самої ставки: "+
			"%.2f проти %.2f — обидва пішли одним каналом", accum, dist)
	}
}

// СТОРОЖ ВАЛЮТИ. Рукав валюти, у якій сьогодні порожньо, фабрика
// пропускає — і без окремої згадки про планований фонд покупка в такій
// валюті зникла б БЕЗ ПОМИЛКИ: рукав просто не зібрався б, а всі числа
// лишились би правдоподібними. Це той клас втрати, який не видно ніяк,
// крім прицільного тесту.
func TestWhatIfPlannedFundBuyInAbsentCurrency(t *testing.T) {
	url, st := planServer(t)
	seedCatalogFund(t, st, "Долар", store.FundAccumulating, 3000, "")

	_, summary := do(t, "GET", url+"/api/summary", "")
	before := goalsOf(t, summary)
	if before.MonthTargetUAH == 0 {
		t.Fatal("місячного плану немає")
	}
	when := time.Now().AddDate(0, 6, 0).Format("2006-01-02")
	code, body := whatIf(t, url, `{"draft":[{"kind":"fund","ref":"Долар","qty":1000,`+
		`"unit_price":"100","currency":"USD","broker":"mono","buy_date":"`+when+`"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	after := goalsOf(t, afterOf(t, body))
	if after.MonthTargetUAH == before.MonthTargetUAH {
		t.Errorf("покупка у валюті, якої в портфелі немає, зникла безслідно (%.2f)",
			after.MonthTargetUAH)
	}
}

// ПОВТОРЮВАНІСТЬ. Фабрика рукавів збирає їх ШІСТЬ разів під різні
// сценарії, і якби планована позиція дописувалась у той самий зріз, а не
// в копію, другий прогін бачив би внески першого. Числа при цьому
// лишились би цілком правдоподібними — саме тому це окремий тест, а не
// сподівання на уважність.
func TestWhatIfPlannedFundBuyIsRepeatable(t *testing.T) {
	url, st := planServer(t)
	seedCatalogFund(t, st, "Накопичувальний", store.FundAccumulating, 3000, "")

	body := planFundBody("Накопичувальний", 6)
	code, first := whatIf(t, url, body)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, first)
	}
	code, second := whatIf(t, url, body)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, second)
	}
	if a, b := stripDoc(t, []byte(afterOf(t, first))), stripDoc(t, []byte(afterOf(t, second))); a != b {
		t.Error("два однакові запити дали різні документи — планована позиція мутує вхід фабрики")
	}
}

// ГОЛОВНИЙ ТЕСТ ЦЬОГО ВИПРАВЛЕННЯ — і дзеркало до
// TestWhatIfFirstBuyOfUnheldBondCountsAtNominal, якого для фондів не було
// зовсім.
//
// Купівля вище ринку коштує рівно ПЕРЕПЛАТУ: q × (ціна_плану −
// ціна_позиції). Доти вона коштувала ще й Qty₀ × ту саму різницю, тобто
// переоцінювала весь наявний пакет: на живих даних покупка на 46 ₴
// додавала 352 ₴ капіталу.
//
// Перевірок дві навмисно. Рівність ловить величину, а окрема верхня межа
// ловить ПОРЯДОК: без неї «капітал зрушив» проходило б і на несправному
// коді, бо він теж дає ненуль — просто в сто разів більший.
func TestWhatIfBuyOfHeldFundMovesCapitalByOverpaymentOnly(t *testing.T) {
	url, st := planServer(t)
	seedHeldFund(t, st, "Inzhur REIT")

	_, summary := do(t, "GET", url+"/api/summary", "")
	before := fundsOf(t, summary)
	pos := before.fund(t, "Inzhur REIT")
	if pos.Qty != heldFundQty {
		t.Fatalf("позиція не зібралась: %d сертифікатів", pos.Qty)
	}

	const qty, price = 4, 11.56
	code, body := whatIf(t, url, `{"draft":[{"kind":"fund","ref":"Inzhur REIT",`+
		`"qty":4,"unit_price":"11.56","currency":"UAH","broker":"mono"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	after := fundsOf(t, afterOf(t, body))

	want := -qty * (price - pos.LastPrice)
	got := after.CapitalUAH - before.CapitalUAH
	if math.Abs(got-want) > 0.02 {
		t.Errorf("капітал мав зрушити на переплату %.2f, зрушив на %.2f", want, got)
	}
	// Порядок: переплата обмежена ПОКУПКОЮ, а не пакетом. Фантомний
	// доданок на 1738 сертифікатах дав би тут сотні гривень.
	if math.Abs(got) > 5 {
		t.Errorf("капітал зрушив на %.2f — це порядок «переоцінили весь пакет», "+
			"а не «переплатили за 4 сертифікати»", got)
	}
}

// Той самий випадок БЕЗ ручної ціни. Тут канал інший: planBuyFundPrice
// віддає ціну копійками, а позиція тримає чотири знаки, тож розбіжність
// виникає з самого округлення. Вона теж мусить бути обмежена покупкою.
func TestWhatIfBuyOfHeldFundAtPositionPriceBarelyMovesCapital(t *testing.T) {
	url, st := planServer(t)
	seedHeldFund(t, st, "Inzhur REIT")

	_, summary := do(t, "GET", url+"/api/summary", "")
	before := fundsOf(t, summary)

	const qty = 4
	code, body := whatIf(t, url, `{"draft":[{"kind":"fund","ref":"Inzhur REIT",`+
		`"qty":4,"currency":"UAH","broker":"mono"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	after := fundsOf(t, afterOf(t, body))
	if got := math.Abs(after.CapitalUAH - before.CapitalUAH); got > qty*0.01 {
		t.Errorf("копійчане округлення коштувало %.2f — це більше за %d × 0,01, "+
			"тобто похибка знову міряється пакетом, а не покупкою", got, qty)
	}
}

// Симптом, який видно людині: частка виду. Вона мусить зрушити на
// ПОКУПКУ, а не на переоцінений пакет.
func TestWhatIfBuyOfHeldFundMovesKindShareByPurchaseSize(t *testing.T) {
	url, st := planServer(t)
	seedHeldFund(t, st, "Inzhur REIT")

	_, summary := do(t, "GET", url+"/api/summary", "")
	before := fundsOf(t, summary)
	pos := before.fund(t, "Inzhur REIT")

	const qty = 4
	code, body := whatIf(t, url, `{"draft":[{"kind":"fund","ref":"Inzhur REIT",`+
		`"qty":4,"currency":"UAH","broker":"mono"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	after := fundsOf(t, afterOf(t, body))

	want := qty * pos.LastPrice
	got := after.kindUAH("funds") - before.kindUAH("funds")
	if math.Abs(got-want) > 0.05 {
		t.Errorf("фонди мали вирости на %.2f (чотири сертифікати), виросли на %.2f", want, got)
	}
}

// ПРЕВʼЮ НЕ Є НОВОЮ ЦІНОЮ. Синтетична операція датована сьогодні, тобто
// свіжіша за будь-яку виписку й за будь-яку ручну позначку, — і без
// сторожа вона мовчки «освіжала» позицію: ціна, дата, PriceMarked і
// PriceStale усі змінювались, а повернути правильні було нічим.
//
// Тест дивиться саме на показ, і це навмисно: капітал може зійтись, поки
// дата вже поїхала (див. ризик 1 у плані фази).
func TestWhatIfPreviewLeavesFundPriceAndMarks(t *testing.T) {
	url, st := planServer(t)
	seedHeldFund(t, st, "Inzhur REIT")

	_, summary := do(t, "GET", url+"/api/summary", "")
	was := fundsOf(t, summary).fund(t, "Inzhur REIT")

	code, body := whatIf(t, url, `{"draft":[{"kind":"fund","ref":"Inzhur REIT",`+
		`"qty":4,"unit_price":"11.56","currency":"UAH","broker":"mono"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	now := fundsOf(t, afterOf(t, body)).fund(t, "Inzhur REIT")

	if now.LastPrice != was.LastPrice || now.LastPriceDate != was.LastPriceDate {
		t.Errorf("превʼю переписало ціну позиції: %.4f від %s замість %.4f від %s",
			now.LastPrice, now.LastPriceDate, was.LastPrice, was.LastPriceDate)
	}
	if now.PriceMarked != was.PriceMarked || now.PriceStale != was.PriceStale {
		t.Errorf("превʼю перекинуло прапорці ціни: marked %v→%v, stale %v→%v",
			was.PriceMarked, now.PriceMarked, was.PriceStale, now.PriceStale)
	}
	// А сертифікати — додались: сторож спиняє ціну, не покупку.
	if now.Qty != was.Qty+4 {
		t.Errorf("сертифікати не додались: %d замість %d", now.Qty, was.Qty+4)
	}
}

// Другий бік сторожа на всьому HTTP-шляху: фонд, якого в портфелі ЩЕ
// НЕМАЄ, ціну отримати мусить. Наївне «ніколи не ставити ціну» лишило б
// LastPrice нулем, і капітал просів би на всю покупку.
func TestWhatIfFirstBuyOfUnheldFundCountsAtItsPrice(t *testing.T) {
	url, st := planServer(t)
	seedCatalogFund(t, st, "Новий", store.FundDistributing, 3000, "")

	_, summary := do(t, "GET", url+"/api/summary", "")
	before := fundsOf(t, summary)

	code, body := whatIf(t, url, `{"draft":[{"kind":"fund","ref":"Новий",`+
		`"qty":10,"unit_price":"100","currency":"UAH","broker":"mono"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	after := fundsOf(t, afterOf(t, body))

	if got := math.Abs(after.CapitalUAH - before.CapitalUAH); got > 0.05 {
		t.Errorf("перша покупка фонду мала лишити капітал на місці, зрушила на %.2f", got)
	}
	if got := after.kindUAH("funds") - before.kindUAH("funds"); math.Abs(got-1000) > 0.05 {
		t.Errorf("фонди мали вирости на всю покупку (1000), виросли на %.2f", got)
	}
}

func sameBrokers(a, b map[string]map[string]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for name, byCur := range a {
		other, ok := b[name]
		if !ok || len(other) != len(byCur) {
			return false
		}
		for cur, v := range byCur {
			if other[cur] != v {
				return false
			}
		}
	}
	return true
}

// lastDayThisMonth / firstDayNextMonth — дві дати обабіч межі, і саме
// вони роблять тести нижче детермінованими за будь-якого дня запуску.
//
// Останній день поточного місяця завжди >= сьогодні (тобто ніколи не
// прострочений) і завжди в тому самому місяці. Перше число наступного —
// найщільніша можлива «майбутня» дата. Разом вони затискають межу з
// обох боків, чого не робив жоден наявний тест: усі вони датовані через
// рік або два й далекої гілки не покидають.
func lastDayThisMonth() string {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).
		AddDate(0, 1, -1).Format("2006-01-02")
}

func firstDayNextMonth() string {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).
		AddDate(0, 1, 0).Format("2006-01-02")
}

// ДЗЕРКАЛО до TestWhatIfFutureRowDoesNotMoveToday: рядок ЦЬОГО місяця
// сьогоднішні числа рухати МУСИТЬ.
//
// Доти він не рухав нічого й ніде — у портфель не входив, бо майбутній,
// а в прогнозі його разова половина зникала на нулі monthOffsetRaw. Саме
// цей випадок і привів до всієї серії: два рядки на завтра, а картка
// «Що зміниться» майже мовчить.
func TestWhatIfThisMonthRowMovesToday(t *testing.T) {
	url, _ := planServer(t)
	_, summary := do(t, "GET", url+"/api/summary", "")
	before := goalsOf(t, summary)

	code, body := whatIf(t, url, `{"draft":[{"kind":"bond","ref":"UA4000227748",`+
		`"qty":10,"broker":"mono","buy_date":"`+lastDayThisMonth()+`"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	after := goalsOf(t, afterOf(t, body))
	if after.CapitalUAH == before.CapitalUAH {
		t.Errorf("капітал не зрушив (%.2f) — рядок цього місяця не доїхав до портфеля",
			after.CapitalUAH)
	}
	var line struct {
		Basket struct {
			Lines []struct {
				Future  bool `json:"future"`
				Overdue bool `json:"overdue"`
			} `json:"lines"`
		} `json:"basket"`
	}
	if err := json.Unmarshal([]byte(body), &line); err != nil {
		t.Fatal(err)
	}
	if len(line.Basket.Lines) != 1 {
		t.Fatalf("мав бути один рядок, маємо %d", len(line.Basket.Lines))
	}
	if line.Basket.Lines[0].Future {
		t.Error("рядок цього місяця позначено майбутнім")
	}
	if line.Basket.Lines[0].Overdue {
		t.Error("рядок цього місяця позначено простроченим — підпис у таблиці збреше")
	}
}

// МЕЖА З ДРУГОГО БОКУ, і вона щільна: перше число наступного місяця вже
// майбутнє. Наявні тести стоять на «+1 рік» і межі не торкаються, тож
// зсув порога на місяць пройшов би під ними непоміченим.
func TestWhatIfNextMonthRowDoesNotMoveToday(t *testing.T) {
	url, _ := planServer(t)
	_, summary := do(t, "GET", url+"/api/summary", "")
	before := goalsOf(t, summary)

	code, body := whatIf(t, url, `{"draft":[{"kind":"bond","ref":"UA4000227748",`+
		`"qty":10,"broker":"mono","buy_date":"`+firstDayNextMonth()+`"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	after := goalsOf(t, afterOf(t, body))
	if after.CapitalUAH != before.CapitalUAH {
		t.Errorf("капітал зрушив на %.2f — рядок наступного місяця потрапив у портфель",
			after.CapitalUAH-before.CapitalUAH)
	}
}

// НАСЛІДОК, ПРИЙНЯТИЙ СВІДОМО, і тест стоїть тут саме тому, щоб його не
// відкотили мовчки. Рядок цього місяця вже в портфелі, і готівку брокера
// за нього вже списано, — отже він мусить рахуватись і в нестачі.
// Виключити його означало б показати наслідок (залишок упав) без рядка,
// який називає причину.
func TestWhatIfThisMonthRowCountsInShortfall(t *testing.T) {
	url, _ := planServer(t)
	code, body := whatIf(t, url, `{"draft":[{"kind":"deposit","ref":"privat",`+
		`"amount":"90000000","currency":"UAH","months":12,"rate_pct":"16","buy_date":"`+
		lastDayThisMonth()+`"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var got struct {
		Basket struct {
			Shorts []struct {
				Broker string `json:"broker"`
			} `json:"shorts"`
		} `json:"basket"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Basket.Shorts) == 0 {
		t.Error("нестачі немає — рядок цього місяця не порахували, хоч гроші за нього вже списані")
	}
}

// Вклад цього місяця без ставки лишається ЧЕСНОЮ ВІДМОВОЮ.
//
// Доти перевірка стояла всередині майбутньої гілки, і поки «майбутнє»
// починалось із завтра, це збігалось. Після зміни порога вклад цього
// місяця пішов у портфель — і без підняття перевірки мовчазний RateBP: 0
// дав би нарахування на нуль там, де раніше було 400.
// TestWhatIfRejectsUnresolvableRate датований «+1 рік» і проходив би,
// поки це відбувається.
func TestWhatIfRejectsUnresolvableRateThisMonth(t *testing.T) {
	url, st := planServer(t)
	if err := st.SetSetting(context.Background(), "deposit_rate_uah_pct", ""); err != nil {
		t.Fatal(err)
	}
	code, body := whatIf(t, url, `{"draft":[{"kind":"deposit","ref":"privat",`+
		`"amount":"100000","currency":"UAH","months":12,"buy_date":"`+
		lastDayThisMonth()+`"}]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("мав бути 400, маємо %d %s", code, body)
	}
}

// Нестача — питання про СЬОГОДНІШНІЙ залишок, і майбутній рядок його не
// ставить. Підсумок при цьому його містить: «скільки я збираюсь
// витратити» рахує все.
func TestWhatIfFutureRowHasNoShortfall(t *testing.T) {
	url, _ := planServer(t)
	when := time.Now().AddDate(2, 0, 0).Format("2006-01-02")
	code, body := whatIf(t, url, `{"draft":[{"kind":"deposit","ref":"privat",`+
		`"amount":"90000000","currency":"UAH","months":12,"rate_pct":"16","buy_date":"`+when+`"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var got struct {
		Basket basketDoc `json:"basket"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Basket.Shorts) != 0 {
		t.Errorf("майбутній рядок оголошено нестачею: %+v", got.Basket.Shorts)
	}
	if len(got.Basket.Totals) != 1 || got.Basket.Totals[0].Amount != "90000000.00" {
		t.Errorf("підсумок не містить майбутнього рядка: %+v", got.Basket.Totals)
	}
	if len(got.Basket.Lines) != 1 || !got.Basket.Lines[0].Future {
		t.Errorf("рядок не позначено майбутнім: %+v", got.Basket.Lines)
	}
}

// Сьогоднішній вклад рухає рівно те, що рухав би справжній: тіло в
// капітал, гроші з рахунку банку.
func TestWhatIfDepositMovesCapitalAndBank(t *testing.T) {
	url, _ := planServer(t)
	_, summary := do(t, "GET", url+"/api/summary", "")
	before := goalsOf(t, summary)
	code, body := whatIf(t, url, `{"draft":[{"kind":"deposit","ref":"mono",`+
		`"amount":"300000","currency":"UAH","months":12,"rate_pct":"16"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	after := goalsOf(t, afterOf(t, body))
	if d := after.DepositsUAH - before.DepositsUAH; d != 300000 {
		t.Errorf("вклади зросли на %.2f, хочемо 300000", d)
	}
	if d := after.Brokers["mono"]["UAH"] - before.Brokers["mono"]["UAH"]; d != -300000 {
		t.Errorf("гривня в mono змінилась на %.2f, хочемо -300000", d)
	}
}

// Резервний вклад — це ПОДУШКА, а не вклад (правило 0032). Єдине, що
// втримає його на гіпотетичному шляху: без цього тесту прапорець тихо
// перестав би працювати саме тут, і картка впливу казала б «без змін»
// там, де подушка насправді виросла.
func TestWhatIfReserveDepositMovesCushionNotDeposits(t *testing.T) {
	url, _ := planServer(t)
	_, summary := do(t, "GET", url+"/api/summary", "")
	before := goalsOf(t, summary)
	code, body := whatIf(t, url, `{"draft":[{"kind":"deposit","ref":"mono",`+
		`"amount":"300000","currency":"UAH","months":12,"rate_pct":"16","is_reserve":true}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	after := goalsOf(t, afterOf(t, body))
	if after.DepositsUAH != before.DepositsUAH {
		t.Errorf("резервний вклад потрапив у вклади: %.2f → %.2f",
			before.DepositsUAH, after.DepositsUAH)
	}
	if after.Reserve == nil || before.Reserve == nil {
		t.Fatalf("резерву немає в документі")
	}
	if d := after.Reserve.UAH - before.Reserve.UAH; d != 300000 {
		t.Errorf("подушка зросла на %.2f, хочемо 300000", d)
	}
	if after.Reserve.Months <= before.Reserve.Months {
		t.Errorf("місяці подушки не зросли: %.2f → %.2f", before.Reserve.Months, after.Reserve.Months)
	}
}

// Внесок у пенсійний: капітал НПФ росте, рахунок дебетовано.
func TestWhatIfNPFContributionMovesAccount(t *testing.T) {
	url, st := planServer(t)
	id, err := st.AddNPFAccount(context.Background(), domain.NPFAccount{
		Name: "Династія", Administrator: "ЦПО", Currency: money.UAH,
		Nav: 3_472156, NavDate: domain.NewDate(time.Now()),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, summary := do(t, "GET", url+"/api/summary", "")
	before := goalsOf(t, summary)
	code, body := whatIf(t, url,
		`{"draft":[{"kind":"npf","ref":"`+itoa(int(id))+`","amount":"4000","broker":"mono"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	after := goalsOf(t, afterOf(t, body))
	if d := after.NPFUAH - before.NPFUAH; d < 3999 || d > 4001 {
		t.Errorf("пенсійний виріс на %.2f, хочемо ≈4000", d)
	}
	if d := after.Brokers["mono"]["UAH"] - before.Brokers["mono"]["UAH"]; d != -4000 {
		t.Errorf("гривня в mono змінилась на %.2f, хочемо -4000", d)
	}
}

// Ставка, якої нема звідки взяти, — 400 з іменем винуватця, а не тихий
// нуль. Замок під 0% не «нічого не змінює»: він забирає гроші з рукава,
// який ріс би за ставкою, і тихо занижує ціль (див. planLockFlows).
func TestWhatIfRejectsUnresolvableRate(t *testing.T) {
	url, st := planServer(t)
	if err := st.SetSetting(context.Background(), "deposit_rate_uah_pct", ""); err != nil {
		t.Fatal(err)
	}
	when := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	code, body := whatIf(t, url, `{"draft":[{"kind":"deposit","ref":"privat",`+
		`"amount":"300000","currency":"UAH","months":12,"buy_date":"`+when+`"}]}`)
	if code != http.StatusBadRequest {
		t.Fatalf("хочемо 400, маємо %d %s", code, body)
	}
	if !strings.Contains(body, "ставк") {
		t.Errorf("помилка мовчить про причину: %s", body)
	}
}

// Синтетика живе рівно один запит. Якби вона писалась у сховище,
// кожне превʼю дописувало б план, і той ріс би сам собою.
func TestWhatIfSyntheticPlanIsNotPersisted(t *testing.T) {
	url, st := planServer(t)
	seedCatalogFund(t, st, "Накопичувальний", store.FundAccumulating, 3000, "")
	when := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	// Обидва канали разом: замок (вклад) і планований фонд. Другий
	// синтетики в plan_actions не лишає взагалі — він живе власним
	// каналом гіпотези, — але саме тому його варто перевірити тут:
	// «нічого не записалось» має лишитись правдою і для нього.
	code, body := whatIf(t, url, `{"draft":[{"kind":"deposit","ref":"privat",`+
		`"amount":"300000","currency":"UAH","months":12,"rate_pct":"16","buy_date":"`+when+`"},`+
		`{"kind":"fund","ref":"Накопичувальний","qty":100,"unit_price":"100",`+
		`"currency":"UAH","broker":"mono","buy_date":"`+when+`"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	for _, path := range []string{"/api/plan/actions", "/api/plan/flows"} {
		_, out := do(t, "GET", url+path, "")
		if strings.TrimSpace(out) != "[]" {
			t.Errorf("%s після whatif не порожній: %s", path, out)
		}
	}
}

// ПАПІР, ЯКОГО В ПОРТФЕЛІ ЩЕ НЕМАЄ, мусить увійти в капітал номіналом.
//
// Спіймано вживу, не тестом, і причина показова: loadSources тягне
// довідник рівно для тих ISIN, що зустрічаються в РЕАЛЬНИХ лотах, а
// гіпотеза дописується після нього. Лот без свого Bond не має ні
// номіналу, ні графіка — гроші з рахунку списувались, а капітал просідав
// рівно на їхню суму, ніби папір коштує нуль. Усі попередні тести цього
// не бачили, бо купували папір, який у портфелі вже був — саме тому цей
// купує ІНШИЙ.
func TestWhatIfFirstBuyOfUnheldBondCountsAtNominal(t *testing.T) {
	url, st := planServer(t)
	seedSecondBond(t, st)
	_, summary := do(t, "GET", url+"/api/summary", "")
	before := goalsOf(t, summary)

	code, body := whatIf(t, url, `{"draft":[{"kind":"bond","ref":"UA4000999999","qty":2,"broker":"mono"}]}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var got struct {
		After struct {
			CapitalUAH float64 `json:"capital_uah"`
			NominalUAH float64 `json:"nominal_uah_eq"`
		} `json:"after"`
		Basket basketDoc `json:"basket"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	// Номінал портфеля виріс рівно на 2 000: два папери по 1 000.
	if d := got.After.NominalUAH - before.NominalUAH; d != 2000 {
		t.Errorf("номінал зріс на %.2f, хочемо 2000 — довідник паперу не доїхав разом із лотом", d)
	}
	// Капітал змінюється рівно на «номінал мінус заплачене», тобто на
	// мінус НКД: за нього платять, а капіталом він не стає. Головне тут —
	// що просідання НЕ дорівнює всій ціні покупки.
	spent, err := domain.ParseDecimalToMinor(got.Basket.Totals[0].Amount, money.UAH)
	if err != nil {
		t.Fatal(err)
	}
	want := before.CapitalUAH + 2000 - float64(spent)/100
	if diff := got.After.CapitalUAH - want; diff > 0.01 || diff < -0.01 {
		t.Errorf("капітал %.2f, хочемо %.2f (було %.2f + номінал 2000 − заплачено %.2f)",
			got.After.CapitalUAH, want, before.CapitalUAH, float64(spent)/100)
	}
}

// seedSecondBond — папір, якого в портфелі НЕМАЄ й не буде: seed() кладе
// лот лише на UA4000227748.
func seedSecondBond(t *testing.T, st *store.Store) {
	t.Helper()
	secs := []nbu.Security{{
		Bond: domain.Bond{ISIN: "UA4000999999", Nominal: money.New(100000, money.UAH),
			RateBP: 1500, Maturity: "2029-03-17", Descr: "ще не куплені"},
		Payments: []domain.Payment{
			{ISIN: "UA4000999999", PayDate: "2027-03-17", Type: domain.PayCoupon, PerBond: money.New(7500, money.UAH)},
			{ISIN: "UA4000999999", PayDate: "2029-03-17", Type: domain.PayCoupon, PerBond: money.New(7500, money.UAH)},
			{ISIN: "UA4000999999", PayDate: "2029-03-17", Type: domain.PayRedemption, PerBond: money.New(100000, money.UAH)},
		},
	}, {
		Bond: domain.Bond{ISIN: "UA4000227748", Nominal: money.New(100000, money.UAH),
			RateBP: 1655, Maturity: "2027-03-17", Descr: "гривневі військові"},
		Payments: []domain.Payment{
			{ISIN: "UA4000227748", PayDate: "2026-09-16", Type: domain.PayCoupon, PerBond: money.New(8275, money.UAH)},
			{ISIN: "UA4000227748", PayDate: "2027-03-17", Type: domain.PayCoupon, PerBond: money.New(8275, money.UAH)},
			{ISIN: "UA4000227748", PayDate: "2027-03-17", Type: domain.PayRedemption, PerBond: money.New(100000, money.UAH)},
		},
	}}
	if err := st.ReplaceDirectory(context.Background(), secs, time.Now()); err != nil {
		t.Fatal(err)
	}
}
