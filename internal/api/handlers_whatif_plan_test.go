package api

import (
	"context"
	"encoding/json"
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
	url, _ := planServer(t)
	when := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	code, body := whatIf(t, url, `{"draft":[{"kind":"deposit","ref":"privat",`+
		`"amount":"300000","currency":"UAH","months":12,"rate_pct":"16","buy_date":"`+when+`"}]}`)
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
