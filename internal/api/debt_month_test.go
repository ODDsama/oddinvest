package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
)

// Борг ріжеться ПІСЛЯ подушки й ПЕРЕД цілями. Порядок не стилістичний:
// ціль накопичення не росте, а борг росте сам, тож класти на авто, маючи
// живу розстрочку, означає купувати його дорожче рівно на ставку боргу.
func TestAllocateCutsDebtBeforeGoals(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, &state.Reserve{
		GapUAH: 5000, FillMonthUAH: 1000, FillNowUAH: 1000,
	})
	doc.Debt = &state.DebtPlan{
		TotalUAH: 30000, TopRatePct: 49.8, TopName: "Холодильник",
		FillMonthUAH: 2000, FillNowUAH: 2000,
	}
	doc.Goals = []state.Goal{{
		ID: 1, Name: "Авто", GapUAH: 50000,
		FillMonthUAH: 3000, FillNowUAH: 3000,
	}}

	// 5 000 ₴: подушці 1 000, боргу 2 000, цілі — те, що лишилось.
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(500000, money.UAH)), 5000,
		allocAllow{ReserveUAH: 5000, DebtUAH: 5000, GoalsUAH: 5000}, money.UAH, nil)

	if got.Reserve == nil || got.Reserve.AmountUAH != 1000 {
		t.Fatalf("подушка: %+v", got.Reserve)
	}
	if got.Debt == nil || got.Debt.AmountUAH != 2000 {
		t.Fatalf("борг: %+v", got.Debt)
	}
	if got.DebtUAH != 2000 {
		t.Errorf("сума боргу в підсумку %.2f, чекали 2000", got.DebtUAH)
	}
	// Цілі беруть із ЗАЛИШКУ, а не зі всієї суми: гроші не можна віддати
	// двічі.
	if len(got.Goals) != 1 || got.Goals[0].AmountUAH != 2000 {
		t.Fatalf("цілі: %+v", got.Goals)
	}
	if got.AvailUAH != 0 {
		t.Errorf("на папери лишилось %.2f, чекали 0", got.AvailUAH)
	}
	// Найдорожчий борг названий у причині: одне число «разом» не каже, з
	// чого починати.
	if !strings.Contains(got.Debt.Why, "Холодильник") {
		t.Errorf("причина не називає боргу: %q", got.Debt.Why)
	}
}

// Політика «з яких грошей гасити» ріже незалежно від подушки й цілей — і
// мовчазної відмови не буває.
func TestAllocateDebtRespectsOwnPolicy(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	doc.Debt = &state.DebtPlan{
		TotalUAH: 30000, TopRatePct: 49.8, TopName: "Холодильник",
		FillMonthUAH: 2000, FillNowUAH: 2000,
	}
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(500000, money.UAH)), 5000,
		allocAllow{ReserveUAH: 5000, DebtUAH: 0, GoalsUAH: 5000}, money.UAH, nil)

	if got.Debt != nil {
		t.Fatalf("борг узяв заборонені гроші: %+v", got.Debt)
	}
	if got.DebtSkipWhy == "" {
		t.Error("вирізка зникла без пояснення — читається як поломка")
	}
}

// Обовʼязкові платежі зменшують гроші місяця, а пільговий оборот картки —
// НІ. Це головна межа фази: побут уже описаний витратами й часткою потоку
// в портфель, і друге його віднімання відняло б те саме двічі.
func TestCardInstallmentsLeaveMonthPlanAlone(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if resp, out := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_expenses":"0","monthly_expenses_currency":"UAH"}`); resp.StatusCode != 204 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, out)
	}
	if resp, out := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"50000","currency":"UAH",
		  "cadence":"month","from_date":"2020-01-01"}`); resp.StatusCode != 201 {
		t.Fatalf("потік: %d %s", resp.StatusCode, out)
	}

	base := monthPlanOf(t, srv.URL)
	if base.PlanUAH <= 0 {
		t.Fatalf("план місяця порожній: %+v", base)
	}

	// Картка з ЖИВИМ пільговим оборотом: борг є, але він увесь у
	// пільговому. Гроші місяця це чіпати не мусить.
	card := addDebt(t, srv.URL, `{"name":"ПУМБ","kind":"card","currency":"UAH",
		"statement_day":"30","apr_pct":"47.88","min_payment_pct":"3"}`)
	if resp, out := do(t, "POST", srv.URL+"/api/debt-marks",
		`{"debt_id":"`+did(card)+`","balance":"-18400","statement_due":"18400"}`); resp.StatusCode != 201 {
		t.Fatalf("звірка: %d %s", resp.StatusCode, out)
	}
	if got := monthPlanOf(t, srv.URL); got.PlanUAH != base.PlanUAH || got.DebtDueUAH != 0 {
		t.Errorf("пільговий оборот зрушив гроші місяця: план %.2f (був %.2f), борг %.2f",
			got.PlanUAH, base.PlanUAH, got.DebtDueUAH)
	}

	// Розстрочка, ПРИВʼЯЗАНА до картки, теж не чіпає портфельних грошей:
	// вона списується з картки, тобто живе в побутовому контурі. Доти її
	// платежі віднімались від плану — на бойових даних 8 606,70 ₴/міс
	// уронили місяць із 26 902 до 18 296, хоча з тих грошей ніхто цих
	// розстрочок не платить.
	if _, err := st.AddDebt(context.Background(), domain.Debt{
		Name: "Холодильник", Kind: domain.DebtInstallment, Currency: money.UAH,
		CardID: card, Principal: 30_000_00, PaymentsTotal: 9,
		FirstPaymentDate: domain.NewDate(time.Now()), FeeMonthBp: 199,
	}); err != nil {
		t.Fatal(err)
	}
	if got := monthPlanOf(t, srv.URL); got.PlanUAH != base.PlanUAH || got.DebtDueUAH != 0 {
		t.Errorf("карткова розстрочка зрушила гроші місяця: план %.2f (був %.2f), борг %.2f",
			got.PlanUAH, base.PlanUAH, got.DebtDueUAH)
	}

	// А САМОСТІЙНА — мусить: її платять з інших грошей, тобто саме з тих,
	// що доходять до портфеля.
	if _, err := st.AddDebt(context.Background(), domain.Debt{
		Name: "Товарна в іншому банку", Kind: domain.DebtInstallment, Currency: money.UAH,
		Principal: 9_000_00, PaymentsTotal: 9,
		FirstPaymentDate: domain.NewDate(time.Now()), FeeMonthBp: 199,
	}); err != nil {
		t.Fatal(err)
	}
	got := monthPlanOf(t, srv.URL)
	if got.DebtDueUAH <= 0 {
		t.Fatalf("обовʼязковий платіж не зʼявився: %+v", got)
	}
	if want := base.PlanUAH - got.DebtDueUAH; got.PlanUAH != want {
		t.Errorf("план місяця %.2f, чекали %.2f (менше рівно на обовʼязковий платіж)",
			got.PlanUAH, want)
	}
	// Дозволена частина зменшується тим самим числом: інакше стеля подушки
	// міряла б від грошей, яких немає.
	if got.PlanReserveUAH != got.PlanUAH {
		t.Errorf("дозволена частина %.2f не збіглася з планом %.2f",
			got.PlanReserveUAH, got.PlanUAH)
	}
}

// Валовий дохід і те, що доходить до портфеля, — РІЗНІ числа, і на
// реальних потоках вони різняться в рази.
//
// Без валового «скільки я можу витрачати» рахувати нема з чого: гроші, які
// не пішли в інструменти, не зникають — вони лягають на картку.
func TestMonthPlanGrossDiffersFromIncome(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	// Частка в портфель 10% — рівно як у живих потоках власника.
	if resp, out := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"100000","currency":"UAH",
		  "cadence":"month","from_date":"2020-01-01","invest_pct":"10"}`); resp.StatusCode != 201 {
		t.Fatalf("потік: %d %s", resp.StatusCode, out)
	}
	got := monthPlanOf(t, srv.URL)
	if got.GrossUAH <= 0 {
		t.Fatalf("валового немає: %+v", got)
	}
	if got.IncomeUAH >= got.GrossUAH {
		t.Errorf("у портфель %.2f не менше за валове %.2f", got.IncomeUAH, got.GrossUAH)
	}
	// Десята частина — саме та пропорція, яку задано потоку.
	if diff := got.GrossUAH/10 - got.IncomeUAH; diff > 0.01 || diff < -0.01 {
		t.Errorf("валове %.2f, у портфель %.2f — чекали десятину", got.GrossUAH, got.IncomeUAH)
	}
}

// Режим виходу вмикає стелю подушки САМ, не питаючи про ставку.
//
// Інакше найбільший борг власника її не вмикав би взагалі: у пільговому
// періоді він коштує нуль, реальна ставка відʼємна, і за загальним порогом
// він проходить як безкоштовний. Але названа дата виходу означає «гроші
// потрібні зараз».
func TestExitModeCapsReserveEvenWithoutRate(t *testing.T) {
	today := domain.Date("2026-09-10")
	card := domain.Debt{
		ID: 1, Kind: domain.DebtCard, Currency: money.UAH,
		StatementDay: 30, APRBp: 4788,
	}
	// Борг є, але весь пільговий: нараховувати ще нема на що.
	marks := []domain.DebtMark{{DebtID: 1, Date: "2026-09-01",
		Balance: -180_000_00, StatementDue: 180_000_00}}

	if debtCapsReserve([]domain.Debt{card}, marks, nil, 7, today) {
		t.Error("пільговий борг сам собою ввімкнув стелю подушки")
	}
	card.ExitBy = "2026-11-30"
	if !debtCapsReserve([]domain.Debt{card}, marks, nil, 7, today) {
		t.Error("названа дата виходу не ввімкнула стелю подушки")
	}
	// Дата в минулому режимом не є.
	card.ExitBy = "2026-01-01"
	if debtCapsReserve([]domain.Debt{card}, marks, nil, 7, today) {
		t.Error("минула дата виходу далі тримає стелю")
	}
}

// ГОЛОВНИЙ ТЕСТ ФАЗИ: потік із часткою в портфель 0% дає ПОВНИЙ валовий
// дохід і нічого не дає портфелю.
//
// Доти охорона «чи платить цього місяця» множила суму на цю частку, тож
// нульова частка читалась як «не платить», і потік зникав цілком — разом
// із валовим доходом. На бойових даних це оголосило дохід власника
// 48 970 ₴/міс замість 191 500 і зробило стелю витрат відʼємною.
func TestMonthPlanGrossCountsZeroInvestFlows(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if resp, out := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата на життя","kind":"income","amount":"100000","currency":"UAH",
		  "cadence":"month","from_date":"2020-01-01","invest_pct":"0"}`); resp.StatusCode != 201 {
		t.Fatalf("потік: %d %s", resp.StatusCode, out)
	}
	got := monthPlanOf(t, srv.URL)
	if got.GrossUAH < 99_999 {
		t.Fatalf("валовий %.2f — потік із нульовою часткою зник", got.GrossUAH)
	}
	if got.IncomeUAH != 0 {
		t.Errorf("у портфель %.2f, чекали нуль: частка ж нульова", got.IncomeUAH)
	}
	// І він рахується джерелом доходу: він таки платить.
	if got.Sources != 1 {
		t.Errorf("джерел %d, чекали 1", got.Sources)
	}
	// Залишок — усе, що не пішло в портфель.
	if diff := got.OnCardUAH - got.GrossUAH; diff > 0.01 || diff < -0.01 {
		t.Errorf("залишок %.2f при валовому %.2f", got.OnCardUAH, got.GrossUAH)
	}
}

// Тотожність залишку: валовий = у портфель + на картку.
func TestMonthPlanOnCardIsGrossMinusIncome(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if resp, out := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"100000","currency":"UAH",
		  "cadence":"month","from_date":"2020-01-01","invest_pct":"20"}`); resp.StatusCode != 201 {
		t.Fatalf("потік: %d %s", resp.StatusCode, out)
	}
	got := monthPlanOf(t, srv.URL)
	if diff := got.GrossUAH - got.IncomeUAH - got.ExtraUAH - got.OnCardUAH; diff > 0.01 || diff < -0.01 {
		t.Errorf("валовий %.2f ≠ портфель %.2f + позапланове %.2f + залишок %.2f",
			got.GrossUAH, got.IncomeUAH, got.ExtraUAH, got.OnCardUAH)
	}
}

// Стеля подушки на час боргу вмикається лише від боргу, що коштує РЕАЛЬНИХ
// грошей. Безвідсоткова розстрочка «частинами» її не вмикає — і це
// виходить само собою з порогу «реальна ставка вище нуля».
func TestDebtCapsReserveIgnoresFreeInstallment(t *testing.T) {
	today := domain.Date("2026-09-10")
	free := domain.Debt{
		ID: 1, Kind: domain.DebtInstallment, Currency: money.UAH,
		Principal: 30_000_00, PaymentsTotal: 9, FirstPaymentDate: "2026-09-30",
	}
	if debtCapsReserve([]domain.Debt{free}, nil, nil, 7, today) {
		t.Error("безкоштовна розстрочка ввімкнула стелю подушки")
	}

	paid := free
	paid.FeeMonthBp = 199
	if !debtCapsReserve([]domain.Debt{paid}, nil, nil, 7, today) {
		t.Error("розстрочка під ~50%% не ввімкнула стелю подушки")
	}

	// Закритий борг не вмикає нічого: стеля самогасна за побудовою.
	closed := paid
	closed.ClosedDate = "2026-09-01"
	if debtCapsReserve([]domain.Debt{closed}, nil, nil, 7, today) {
		t.Error("погашений борг далі тримає стелю подушки")
	}
}

// monthPlanOf — план місяця зі зведення. Через HTTP, а не через buildState:
// саме те, що бачить екран, і саме там ловиться поле, яке перестало
// доїжджати до контракту.
func monthPlanOf(t *testing.T, url string) state.MonthPlan {
	t.Helper()
	resp, out := do(t, "GET", url+"/api/summary", "")
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/summary: %d %s", resp.StatusCode, out)
	}
	var doc struct {
		MonthPlan *state.MonthPlan `json:"month_plan"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.MonthPlan == nil {
		t.Fatal("у зведенні немає плану місяця")
	}
	return *doc.MonthPlan
}

// Прохід балансу картки доходить до нуля, і сума погашеного дорівнює
// боргу.
func TestDebtExitScheduleReachesZero(t *testing.T) {
	rows := []debtMonthRow{
		{gross: 100_000, invest: 20_000},
		{gross: 100_000, invest: 20_000},
		{gross: 100_000, invest: 20_000},
	}
	got := debtExitWalk(rows, 120_000, 40_000, "2026-09-01", 0)
	if len(got) != 3 {
		t.Fatalf("кроків %d, чекали 3: 120 000 ÷ (100 000 − 20 000 − 40 000)", len(got))
	}
	if got[len(got)-1].LeftUAH != 0 {
		t.Errorf("останній крок лишає %.2f боргу", got[len(got)-1].LeftUAH)
	}
	// Місяці НЕ однакові за побудовою — кожен несе свої числа, а не
	// середнє: інакше стрибок темпу не пояснити.
	if got[0].GrossUAH != 100_000 || got[0].InvestUAH != 20_000 || got[0].SpendUAH != 40_000 {
		t.Errorf("рядок не називає своїх чисел: %+v", got[0])
	}
	// Крок за кроком борг меншає рівно на профіцит.
	if got[0].LeftUAH != 80_000 || got[1].LeftUAH != 40_000 {
		t.Errorf("хід проходу: %.2f → %.2f", got[0].LeftUAH, got[1].LeftUAH)
	}
}

// Коли витрати зʼїдають усе, що приходить, таблиці НЕМАЄ: двадцять чотири
// однакові рядки — не відповідь, а спосіб не сказати «виходу не буде».
func TestDebtExitScheduleStopsWhenDebtGrows(t *testing.T) {
	rows := []debtMonthRow{
		{gross: 100_000, invest: 20_000},
		{gross: 100_000, invest: 20_000},
	}
	if got := debtExitWalk(rows, 120_000, 80_000, "2026-09-01", 0); got != nil {
		t.Errorf("прохід намалював %d рядків при нульовому профіциті", len(got))
	}
	// Перший місяць може бути слабким (одна зарплата скінчилась, друга ще
	// не почалась) — але якщо далі темп є, таблиця будується.
	rows[0] = debtMonthRow{gross: 100_000, invest: 20_000}
	if got := debtExitWalk(rows, 120_000, 79_000, "2026-09-01", 0); len(got) == 0 {
		t.Error("живий профіцит не дав жодного кроку")
	}
}

// Прожитий місяць у прохід НЕ входить.
//
// Спіймано власником на екрані 31 серпня: борг у звірці — це вже результат
// серпня (дохід прийшов, витрати сталися), а прохід ставив серпень першим
// кроком і обіцяв погашення, яке або вже відбулось, або вже ні. Той самий
// місяць рахувався двічі.
func TestDebtExitWalkSkipsMonthAlreadyLived(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if resp, out := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_expenses":"10000","monthly_expenses_currency":"UAH"}`); resp.StatusCode != 204 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, out)
	}
	if resp, out := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"60000","currency":"UAH",
		  "cadence":"month","from_date":"2020-01-05","invest_pct":"0"}`); resp.StatusCode != 201 {
		t.Fatalf("потік: %d %s", resp.StatusCode, out)
	}

	today := time.Now()
	exit := today.AddDate(0, 3, 0).Format("2006-01-02")
	card := addDebt(t, srv.URL, `{"name":"Картка","kind":"card","currency":"UAH",
		"statement_day":"30","apr_pct":"47.88","min_payment_pct":"3",
		"exit_by":"`+exit+`"}`)
	// Звірка СЬОГОДНІШНЯ: усе, що мало прийти цього місяця, уже в балансі.
	if resp, out := do(t, "POST", srv.URL+"/api/debt-marks",
		`{"debt_id":"`+did(card)+`","balance":"-90000","statement_due":"90000"}`); resp.StatusCode != 201 {
		t.Fatalf("звірка: %d %s", resp.StatusCode, out)
	}

	resp, out := do(t, "GET", srv.URL+"/api/summary", "")
	if resp.StatusCode != 200 {
		t.Fatal(out)
	}
	var doc struct {
		Debt *state.DebtPlan `json:"debt"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Debt == nil || doc.Debt.Exit == nil {
		t.Fatalf("блоку виходу немає: %s", out)
	}
	sch := doc.Debt.Exit.Schedule
	if len(sch) == 0 {
		t.Fatal("прохід порожній")
	}
	// Перший крок — НАСТУПНИЙ місяць, а не поточний.
	if now := today.Format("2006-01"); sch[0].Month == now {
		t.Errorf("прохід починається з поточного місяця %s, який уже прожитий", now)
	}
	// Наступний місяць рахуємо від ПЕРШОГО числа: today.AddDate(0,1,0) на
	// 31 серпня дає 1 жовтня (у вересні 30 днів), і тест ловив би не ту
	// ваду, а власне переповнення.
	first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
	if want := first.AddDate(0, 1, 0).Format("2006-01"); sch[0].Month != want {
		t.Errorf("перший крок %s, чекали %s", sch[0].Month, want)
	}
}

// План виходу СПІЛЬНИЙ на всі картки з живою датою — і найбільший борг з
// нього не зникає через чужу, ближчу дату.
//
// Спіймано власником на бойових даних: друга картка (борг 5 212 ₴, вихід
// 30.10) перебила першу (182 317 ₴, вихід 31.10), і застосунок оголосив
// стелю витрат 216 805 ₴/міс — тобто планував вихід із боргу, меншого за
// саму цю стелю, а про справжній борг мовчав.
func TestDebtExitCoversAllCardsWithTarget(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if resp, out := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_expenses":"10000","monthly_expenses_currency":"UAH"}`); resp.StatusCode != 204 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, out)
	}
	if resp, out := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"200000","currency":"UAH",
		  "cadence":"month","from_date":"2020-01-05","invest_pct":"0"}`); resp.StatusCode != 201 {
		t.Fatalf("потік: %d %s", resp.StatusCode, out)
	}

	today := time.Now()
	// БЛИЖЧА дата — у меншої картки. Саме ця пара й ламала розрахунок.
	small := addDebt(t, srv.URL, `{"name":"mono Чорна","kind":"card","currency":"UAH",
		"statement_day":"30","apr_pct":"40","min_payment_pct":"3",
		"exit_by":"`+today.AddDate(0, 2, 0).Format("2006-01-02")+`"}`)
	big := addDebt(t, srv.URL, `{"name":"ПУМБ","kind":"card","currency":"UAH",
		"statement_day":"30","apr_pct":"47.88","min_payment_pct":"3",
		"exit_by":"`+today.AddDate(0, 3, 0).Format("2006-01-02")+`"}`)
	for _, m := range []string{
		`{"debt_id":"` + did(small) + `","balance":"-6000","statement_due":"6000"}`,
		`{"debt_id":"` + did(big) + `","balance":"-180000","statement_due":"180000"}`,
	} {
		if resp, out := do(t, "POST", srv.URL+"/api/debt-marks", m); resp.StatusCode != 201 {
			t.Fatalf("звірка: %d %s", resp.StatusCode, out)
		}
	}

	exit := exitOf(t, srv.URL)
	if len(exit.Cards) != 2 {
		t.Fatalf("у плані %d карток, чекали дві: %+v", len(exit.Cards), exit.Cards)
	}
	if !strings.Contains(strings.Join(exit.Cards, " "), "ПУМБ") {
		t.Errorf("найбільший борг випав із плану: %+v", exit.Cards)
	}
	// Потреба — СУМА по картках, кожна за власною датою. У малої картки
	// всього 6 000 боргу, тож будь-яке число більше за нього доводить, що
	// велику порахували; беремо із запасом.
	if exit.NeedPerMonthUAH < 20_000 {
		t.Errorf("треба звільняти %.2f — це потреба самої лише малої картки",
			exit.NeedPerMonthUAH)
	}
}

// Карткові розстрочки віднімаються від того, що лишається на картці:
// стеля витрат менша рівно на їхні щомісячні платежі.
//
// Вони не тіло до погашення (за рішенням власника «вийти з ліміту» — це
// звести в нуль КАРТКИ), але з картки списуються, тож на витрати їх
// витратити вже не можна.
func TestDebtExitSubtractsCardInstallments(t *testing.T) {
	in := domain.CardExitInput{
		DebtUAH: 180_000_00, GrossUAH: 200_000_00, InvestUAH: 0,
		SpendUAH: 40_000_00, ExitBy: "2026-12-31",
		Today: domain.Date("2026-09-30"), Months: 3,
	}
	base := domain.CardExit(in)
	in.InstallmentUAH = 8_606_70
	with := domain.CardExit(in)

	if !base.Known || !with.Known {
		t.Fatalf("розрахунку немає: %+v / %+v", base, with)
	}
	if diff := base.SpendCap - with.SpendCap; diff != 8_606_70 {
		t.Errorf("стеля впала на %d, чекали рівно платіж розстрочок 860670", diff)
	}
	// І рядок «якщо й портфельні гроші підуть на картку» рахує з того
	// самого залишку — інакше два числа поруч суперечили б одне одному.
	if diff := base.WithInvestSpendCap - with.WithInvestSpendCap; diff != 8_606_70 {
		t.Errorf("другий рядок впав на %d, чекали 860670", diff)
	}
	// Дата виходу за нинішніми витратами теж відсувається: грошей на
	// погашення лишається менше.
	if with.ETADate <= base.ETADate {
		t.Errorf("дата виходу %s не пізніша за %s", with.ETADate, base.ETADate)
	}
}

// exitOf — блок виходу з документа стану.
func exitOf(t *testing.T, base string) *state.DebtExit {
	t.Helper()
	resp, out := do(t, "GET", base+"/api/summary", "")
	if resp.StatusCode != 200 {
		t.Fatal(out)
	}
	var doc struct {
		Debt *state.DebtPlan `json:"debt"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Debt == nil || doc.Debt.Exit == nil {
		t.Fatalf("блоку виходу немає: %s", out)
	}
	return doc.Debt.Exit
}
