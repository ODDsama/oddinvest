package api

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
)

// НАДГРОБОК: ДОСТРОКОВЕ ПОГАШЕННЯ НЕ ЗАБИРАЄ ПОРТФЕЛЬНИХ ГРОШЕЙ.
//
// Тут стояли два тести — TestAllocateCutsDebtBeforeGoals (борг ріжеться
// після подушки й перед цілями) і TestAllocateDebtRespectsOwnPolicy
// (політика «з яких грошей гасити»). Обидва описували вирізку, якої більше
// немає: вона брала частку від PlanDebtUAH, тобто від грошей, призначених
// у портфель, і зменшувала базу, від якої міряються цільові частки видів.
//
// Замість них — один тест на протилежне твердження, і саме він тут
// найпотрібніший: симетрія з подушкою й цілями здається очевидною («борг
// під пʼятдесят відсотків дорожчий за будь-який вид»), тож наступний автор
// потягнеться повернути вирізку саме сюди.
func TestAllocateLeavesDebtOutOfPortfolioMoney(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, &state.Reserve{
		GapUAH: state.Major(5000, money.UAH), FillMonthUAH: state.Major(1000, money.UAH), FillNowUAH: state.Major(1000, money.UAH), FillFromUAH: state.Major(5000, money.UAH),
	})
	// Борг живий, дорогий і зі стелею — тобто все, що колись вмикало вирізку.
	doc.Debt = &state.DebtPlan{
		TotalUAH: state.Major(30000, money.UAH), TopRatePct: 49.8, TopName: "Холодильник",
		FillMonthUAH: state.Major(2000, money.UAH), FillNowUAH: state.Major(2000, money.UAH),
	}

	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(500000, money.UAH)), 5000,
		allocAllow{ReserveUAH: 5000, GoalsUAH: 5000}, money.UAH, nil)

	// Подушка своє бере — вона з портфельних грошей і далі ріже.
	if got.Reserve == nil || got.Reserve.AmountUAH.Major() != 1000 {
		t.Fatalf("подушка: %+v", got.Reserve)
	}
	// А на папери йде ВСЯ решта: 5 000 − 1 000 = 4 000, чотири квитки по
	// 1 000. Була б вирізка боргу — лишилось би два.
	if got.AvailUAH.Major() != 4000 {
		t.Errorf("на папери %.2f, чекали 4000: борг більше не ріже портфельних грошей",
			got.AvailUAH.Major())
	}
	spent := 0.0
	for _, l := range got.Lines {
		spent += l.TotalUAH.Major()
	}
	if spent != 4000 {
		t.Errorf("куплено на %.2f, чекали 4000", spent)
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
	if base.PlanUAH.Major() <= 0 {
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
	if got := monthPlanOf(t, srv.URL); got.PlanUAH != base.PlanUAH || got.DebtDueUAH.Major() != 0 {
		t.Errorf("пільговий оборот зрушив гроші місяця: план %.2f (був %.2f), борг %.2f",
			got.PlanUAH.Major(), base.PlanUAH.Major(), got.DebtDueUAH.Major())
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
	if got := monthPlanOf(t, srv.URL); got.PlanUAH != base.PlanUAH || got.DebtDueUAH.Major() != 0 {
		t.Errorf("карткова розстрочка зрушила гроші місяця: план %.2f (був %.2f), борг %.2f",
			got.PlanUAH.Major(), base.PlanUAH.Major(), got.DebtDueUAH.Major())
	}

	// А САМОСТІЙНА — мусить, І САМЕ НА ЦІЙ ФІКСТУРІ.
	//
	// Потік тут без invest_pct, тобто на 100 %: до портфеля доходить усе,
	// непортфельних грошей немає взагалі (OnCardUAH == 0), і гасити
	// обовʼязковий платіж більше нема з чого. Це МЕЖОВИЙ випадок правила
	// «спершу платять непортфельні» — той, у якому воно нічого не міняє.
	//
	// Абзац тут, щоб наступний читач не визнав цей тест суперечністю до
	// TestMandatoryDebtPaidFromNonPortfolioMoney: там частка 10 %, тут
	// 100 %, і обидва пришпилюють ту саму формулу з різних боків.
	if _, err := st.AddDebt(context.Background(), domain.Debt{
		Name: "Товарна в іншому банку", Kind: domain.DebtInstallment, Currency: money.UAH,
		Principal: 9_000_00, PaymentsTotal: 9,
		FirstPaymentDate: domain.NewDate(time.Now()), FeeMonthBp: 199,
	}); err != nil {
		t.Fatal(err)
	}
	got := monthPlanOf(t, srv.URL)
	if got.DebtDueUAH.Major() <= 0 {
		t.Fatalf("обовʼязковий платіж не зʼявився: %+v", got)
	}
	if want := base.PlanUAH.Major() - got.DebtDueUAH.Major(); got.PlanUAH.Major() != want {
		t.Errorf("план місяця %.2f, чекали %.2f (менше рівно на обовʼязковий платіж)",
			got.PlanUAH.Major(), want)
	}
	// Дозволена частина зменшується тим самим числом: інакше стеля подушки
	// міряла б від грошей, яких немає.
	if got.PlanReserveUAH != got.PlanUAH {
		t.Errorf("дозволена частина %.2f не збіглася з планом %.2f",
			got.PlanReserveUAH.Major(), got.PlanUAH.Major())
	}
}

// ОБОВʼЯЗКОВИЙ ПЛАТІЖ НЕ ЗМЕНШУЄ ПЛАНУ, ПОКИ Є НЕПОРТФЕЛЬНІ ГРОШІ.
//
// Довід простий: частка в портфель 10 % означає, що решта 90 % доходу до
// портфеля не доходить і йде на життя й на борг. Відняти розстрочку ще й
// від тих 10 % означає заплатити її двічі — раз неявно часткою, раз явно.
//
// Саме це застосунок і робив на бойових даних: план місяця казав
// 6 592,20 ₴ там, де в портфель планувалось завести 9 000 ₴.
func TestMandatoryDebtPaidFromNonPortfolioMoney(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if resp, out := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_expenses":"0","monthly_expenses_currency":"UAH"}`); resp.StatusCode != 204 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, out)
	}
	// 100 000 ₴ під 10 %: у портфель 10 000, на картці 90 000.
	if resp, out := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"100000","currency":"UAH",
		  "cadence":"month","from_date":"2020-01-01","invest_pct":"10"}`); resp.StatusCode != 201 {
		t.Fatalf("потік: %d %s", resp.StatusCode, out)
	}
	if _, err := st.AddDebt(context.Background(), domain.Debt{
		Name: "Товарна в іншому банку", Kind: domain.DebtInstallment, Currency: money.UAH,
		Principal: 9_000_00, PaymentsTotal: 9,
		FirstPaymentDate: domain.NewDate(time.Now()), FeeMonthBp: 199,
	}); err != nil {
		t.Fatal(err)
	}

	got := monthPlanOf(t, srv.URL)
	if got.DebtDueUAH.Major() <= 0 {
		t.Fatalf("обовʼязковий платіж не зʼявився: %+v", got)
	}
	if got.OnCardUAH.Cmp(got.DebtDueUAH) <= 0 {
		t.Fatalf("тест нічого не перевіряє: на картці %.2f при платежі %.2f",
			got.OnCardUAH.Major(), got.DebtDueUAH.Major())
	}
	if got.DebtFromPlanUAH.Major() != 0 {
		t.Errorf("на портфельні гроші лягло %.2f, хоч на картці %.2f — вистачало з запасом",
			got.DebtFromPlanUAH.Major(), got.OnCardUAH.Major())
	}
	if got.PlanUAH != got.IncomeUAH {
		t.Errorf("план місяця %.2f, чекали %.2f — рівно те, що доходить до портфеля",
			got.PlanUAH.Major(), got.IncomeUAH.Major())
	}
	if got.PlanReserveUAH != got.PlanUAH {
		t.Errorf("дозволена частина %.2f розійшлася з планом %.2f",
			got.PlanReserveUAH.Major(), got.PlanUAH.Major())
	}
}

// А коли непортфельних грошей БРАКУЄ — план бере на себе рівно
// переповнення, не більше. Це другий бік тієї самої формули, і без нього
// перший можна було б задовольнити, просто перестав віднімати борг.
func TestMandatoryDebtOverflowsIntoMonthPlan(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if resp, out := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_expenses":"0","monthly_expenses_currency":"UAH"}`); resp.StatusCode != 204 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, out)
	}
	// 10 000 ₴ під 90 %: у портфель 9 000, на картці лише 1 000.
	if resp, out := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"10000","currency":"UAH",
		  "cadence":"month","from_date":"2020-01-01","invest_pct":"90"}`); resp.StatusCode != 201 {
		t.Fatalf("потік: %d %s", resp.StatusCode, out)
	}
	// Платіж ~1 179 ₴ (тіло 1 000 + комісія 1,99 % від 9 000) — більший за
	// тисячу, що лишається на картці.
	if _, err := st.AddDebt(context.Background(), domain.Debt{
		Name: "Товарна в іншому банку", Kind: domain.DebtInstallment, Currency: money.UAH,
		Principal: 9_000_00, PaymentsTotal: 9,
		FirstPaymentDate: domain.NewDate(time.Now()), FeeMonthBp: 199,
	}); err != nil {
		t.Fatal(err)
	}

	got := monthPlanOf(t, srv.URL)
	if got.DebtDueUAH.Cmp(got.OnCardUAH) <= 0 {
		t.Fatalf("тест нічого не перевіряє: платіж %.2f, на картці %.2f — переповнення немає",
			got.DebtDueUAH.Major(), got.OnCardUAH.Major())
	}
	if want := got.DebtDueUAH.Major() - got.OnCardUAH.Major(); math.Abs(got.DebtFromPlanUAH.Major()-want) > 0.005 {
		t.Errorf("на портфельні гроші лягло %.2f, чекали %.2f (платіж мінус картка)",
			got.DebtFromPlanUAH.Major(), want)
	}
	if want := got.IncomeUAH.Major() - got.DebtFromPlanUAH.Major(); math.Abs(got.PlanUAH.Major()-want) > 0.005 {
		t.Errorf("план місяця %.2f, чекали %.2f", got.PlanUAH.Major(), want)
	}
	// І головне: стара формула віднімала ВЕСЬ платіж, тобто була строго
	// гіршою. Без цієї перевірки тест задовольнило б і повернення до неї.
	if old := got.IncomeUAH.Major() - got.DebtDueUAH.Major(); got.PlanUAH.Major() <= old {
		t.Errorf("план %.2f не кращий за старий %.2f — картка не поглинула нічого",
			got.PlanUAH.Major(), old)
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
	if got.GrossUAH.Major() <= 0 {
		t.Fatalf("валового немає: %+v", got)
	}
	if got.IncomeUAH.Cmp(got.GrossUAH) >= 0 {
		t.Errorf("у портфель %.2f не менше за валове %.2f", got.IncomeUAH.Major(), got.GrossUAH.Major())
	}
	// Десята частина — саме та пропорція, яку задано потоку.
	if diff := got.GrossUAH.Major()/10 - got.IncomeUAH.Major(); diff > 0.01 || diff < -0.01 {
		t.Errorf("валове %.2f, у портфель %.2f — чекали десятину", got.GrossUAH.Major(), got.IncomeUAH.Major())
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
	if got.GrossUAH.Major() < 99_999 {
		t.Fatalf("валовий %.2f — потік із нульовою часткою зник", got.GrossUAH.Major())
	}
	if got.IncomeUAH.Major() != 0 {
		t.Errorf("у портфель %.2f, чекали нуль: частка ж нульова", got.IncomeUAH.Major())
	}
	// І він рахується джерелом доходу: він таки платить.
	if got.Sources != 1 {
		t.Errorf("джерел %d, чекали 1", got.Sources)
	}
	// Залишок — усе, що не пішло в портфель.
	if diff := got.OnCardUAH.Major() - got.GrossUAH.Major(); diff > 0.01 || diff < -0.01 {
		t.Errorf("залишок %.2f при валовому %.2f", got.OnCardUAH.Major(), got.GrossUAH.Major())
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
	if diff := got.GrossUAH.Major() - got.IncomeUAH.Major() - got.ExtraUAH.Major() - got.OnCardUAH.Major(); diff > 0.01 || diff < -0.01 {
		t.Errorf("валовий %.2f ≠ портфель %.2f + позапланове %.2f + залишок %.2f",
			got.GrossUAH.Major(), got.IncomeUAH.Major(), got.ExtraUAH.Major(), got.OnCardUAH.Major())
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

	// Комісія Є і договір її при достроковому СКАСОВУЄ — лише такий борг
	// вмикає стелю: у ньому дострокові гроші справді щось скасовують.
	paid := free
	paid.FeeMonthBp = 199
	paid.FeeOnPrepay = domain.DebtFeeCancel
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
	if got[len(got)-1].LeftUAH.Major() != 0 {
		t.Errorf("останній крок лишає %.2f боргу", got[len(got)-1].LeftUAH.Major())
	}
	// Місяці НЕ однакові за побудовою — кожен несе свої числа, а не
	// середнє: інакше стрибок темпу не пояснити.
	if got[0].GrossUAH.Major() != 100_000 || got[0].InvestUAH.Major() != 20_000 || got[0].SpendUAH.Major() != 40_000 {
		t.Errorf("рядок не називає своїх чисел: %+v", got[0])
	}
	// Крок за кроком борг меншає рівно на профіцит.
	if got[0].LeftUAH.Major() != 80_000 || got[1].LeftUAH.Major() != 40_000 {
		t.Errorf("хід проходу: %.2f → %.2f", got[0].LeftUAH.Major(), got[1].LeftUAH.Major())
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
	// не почалась) — але якщо далі темп є, таблиця будується, і слабкий
	// місяць у ній стоїть із нульовим кроком.
	rows[0] = debtMonthRow{gross: 30_000, invest: 20_000}
	got := debtExitWalk(rows, 120_000, 79_000, "2026-09-01", 0)
	if len(got) != 2 {
		t.Fatalf("живий профіцит у другому місяці дав %d кроків, чекали 2", len(got))
	}
	if got[0].LeftUAH.Major() != 120_000 || got[1].LeftUAH.Major() != 119_000 {
		t.Errorf("хід проходу: %.2f → %.2f, чекали 120 000 → 119 000", got[0].LeftUAH.Major(), got[1].LeftUAH.Major())
	}
}

// Прожитий місяць у прохід НЕ входить.
//
// Спіймано власником на екрані 31 серпня: борг у звірці — це вже результат
// серпня (дохід прийшов, витрати сталися), а прохід ставив серпень першим
// кроком і обіцяв погашення, яке або вже відбулось, або вже ні. Той самий
// місяць рахувався двічі.
//
// Платіжний день потоку — 1-ше, тобто на будь-яку сьогоднішню звірку
// зарплата місяця вже прийшла: після звірки в цьому місяці не платить ніщо.
func TestDebtExitWalkSkipsMonthAlreadyLived(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if resp, out := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_expenses":"10000","monthly_expenses_currency":"UAH"}`); resp.StatusCode != 204 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, out)
	}
	if resp, out := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"60000","currency":"UAH",
		  "cadence":"month","from_date":"2020-01-01","invest_pct":"0"}`); resp.StatusCode != 201 {
		t.Fatalf("потік: %d %s", resp.StatusCode, out)
	}

	today := time.Now()
	// Від ПЕРШОГО числа: today.AddDate(0,3,0) на 31 серпня переповнюється, і
	// тест ловив би не ту ваду.
	first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
	exit := first.AddDate(0, 3, 0).Format("2006-01-02")
	card := addDebt(t, srv.URL, `{"name":"Картка","kind":"card","currency":"UAH",
		"statement_day":"30","apr_pct":"47.88","min_payment_pct":"3",
		"exit_by":"`+exit+`"}`)
	// Звірка СЬОГОДНІШНЯ: усе, що мало прийти цього місяця, уже в балансі.
	if resp, out := do(t, "POST", srv.URL+"/api/debt-marks",
		`{"debt_id":"`+did(card)+`","balance":"-90000","statement_due":"90000"}`); resp.StatusCode != 201 {
		t.Fatalf("звірка: %d %s", resp.StatusCode, out)
	}

	e := exitOf(t, srv.URL)
	sch := e.Schedule
	if len(sch) == 0 {
		t.Fatal("прохід порожній")
	}
	// Перший крок — НАСТУПНИЙ місяць, а не поточний, і цілий.
	if now := today.Format("2006-01"); sch[0].Month == now {
		t.Errorf("прохід починається з поточного місяця %s, який уже прожитий", now)
	}
	if want := first.AddDate(0, 1, 0).Format("2006-01"); sch[0].Month != want {
		t.Errorf("перший крок %s, чекали %s", sch[0].Month, want)
	}
	if sch[0].SpendUAH.Major() != 10_000 {
		t.Errorf("повний місяць має бути цілим: %+v", sch[0])
	}
	if e.Months != 3 {
		t.Errorf("місяців %.2f, чекали рівно 3: наступний, ще два, і дата на 1-ше", e.Months)
	}
	// Відновлювати борг на початок нема чого: вікно починається з
	// наступного місяця, і на його початок борг — той, що зараз.
	if e.MarkDate != "" || e.StartDebtUAH.Major() != 90_000 || e.DebtNowUAH.Major() != 90_000 {
		t.Errorf("борг на початок %.2f (звірка %q), чекали 90 000 без відновлення",
			e.StartDebtUAH.Major(), e.MarkDate)
	}
}

// Місяць звірки НЕ прожитий, поки в ньому ще платить хоч один потік, — і
// входить він ЦІЛИМ, від боргу на його початок.
//
// Спіймано власником 1 вересня: звірка того ж дня викидала весь вересень,
// хоч аванс 7-го й зарплати 15-го та 21-го були попереду. Вікно звужувалось
// до одного жовтня, «ще можна залізти» рахувалось із одного місяця, а «за
// твоїм темпом» поруч рахувало вересень і казало 21 вересня. Перша правка
// зробила місяць хвостом «з 2-го» — і власник спіймав уже її: «воно пляше
// від теперішнього мінуса, а не від мінуса на початок періоду».
//
// Потік платить в останній день місяця (день 31 обрізається до довжини
// місяця), звірка — 1-го числа: до неї не прийшло нічого, після — уся
// зарплата.
func TestDebtExitStartsFromMonthStart(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if resp, out := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_expenses":"10000","monthly_expenses_currency":"UAH"}`); resp.StatusCode != 204 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, out)
	}
	if resp, out := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"60000","currency":"UAH",
		  "cadence":"month","from_date":"2020-01-31","invest_pct":"0"}`); resp.StatusCode != 201 {
		t.Fatalf("потік: %d %s", resp.StatusCode, out)
	}

	today := time.Now()
	first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
	days := first.AddDate(0, 1, 0).AddDate(0, 0, -1).Day()
	exit := first.AddDate(0, 3, 0).Format("2006-01-02")
	card := addDebt(t, srv.URL, `{"name":"Картка","kind":"card","currency":"UAH",
		"statement_day":"30","apr_pct":"47.88","min_payment_pct":"3",
		"exit_by":"`+exit+`"}`)
	if resp, out := do(t, "POST", srv.URL+"/api/debt-marks",
		`{"debt_id":"`+did(card)+`","date":"`+first.Format("2006-01-02")+`",
		  "balance":"-90000","statement_due":"90000"}`); resp.StatusCode != 201 {
		t.Fatalf("звірка: %d %s", resp.StatusCode, out)
	}

	e := exitOf(t, srv.URL)
	sch := e.Schedule
	if len(sch) == 0 {
		t.Fatal("прохід порожній")
	}
	// Перший крок — ПОТОЧНИЙ місяць, цілий: уся зарплата, усі витрати.
	if want := today.Format("2006-01"); sch[0].Month != want {
		t.Fatalf("перший крок %s, чекали поточний %s", sch[0].Month, want)
	}
	if sch[0].GrossUAH.Major() != 60_000 || sch[0].SpendUAH.Major() != 10_000 {
		t.Errorf("місяць звірки має бути цілим: %+v", sch[0])
	}
	if e.Months != 4 {
		t.Errorf("місяців %.2f, чекали 4: цей і три наступні", e.Months)
	}
	// Борг на початок відновлено зі звірки 1-го: доходу до неї не було,
	// витрати — за один прожитий день.
	if e.MarkDate != first.Format("2006-01-02") || e.PaidBeforeMarkUAH.Major() != 0 {
		t.Errorf("звірка %q, прийшло до неї %.2f — чекали %s і 0", e.MarkDate, e.PaidBeforeMarkUAH.Major(), first.Format("2006-01-02"))
	}
	if want := round2(90_000 - 10_000/float64(days)); math.Abs(e.StartDebtUAH.Major()-want) > 0.01 {
		t.Errorf("борг на початок %.2f, чекали %.2f (90 000 мінус день витрат)", e.StartDebtUAH.Major(), want)
	}
	if e.DebtNowUAH.Major() != 90_000 {
		t.Errorf("борг зараз %.2f, чекали 90 000", e.DebtNowUAH.Major())
	}
	// Тотожність запасу: місяці × (стеля − витрати) = Σ профіцитів − борг
	// на початок; гранична глибина — від боргу ЗАРАЗ.
	if want := e.Months * (e.SpendCapUAH.Major() - e.SpendUsedUAH.Major()); math.Abs(e.HeadroomUAH.Major()-want) > 0.05 {
		t.Errorf("запас %.2f, чекали %.2f", e.HeadroomUAH.Major(), want)
	}
	if want := e.DebtNowUAH.Major() + e.HeadroomUAH.Major(); math.Abs(e.MaxDebtUAH.Major()-want) > 0.05 {
		t.Errorf("гранична глибина %.2f, чекали %.2f", e.MaxDebtUAH.Major(), want)
	}
	if sch[len(sch)-1].LeftUAH.Major() != 0 {
		t.Errorf("прохід не доходить до нуля: %+v", sch)
	}
}

// Те, що прийшло ДО звірки, повертається в борг на початок: аванс 1-го
// вже зменшив мінус, який показує звірка того ж дня, а місяць рахується
// цілим — разом із цим авансом.
func TestDebtExitRebuildsStartDebtFromPaidBefore(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if resp, out := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_expenses":"10000","monthly_expenses_currency":"UAH"}`); resp.StatusCode != 204 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, out)
	}
	for _, day := range []string{"01", "31"} {
		if resp, out := do(t, "POST", srv.URL+"/api/plan/flows",
			`{"name":"Зарплата `+day+`","kind":"income","amount":"60000","currency":"UAH",
			  "cadence":"month","from_date":"2020-01-`+day+`","invest_pct":"0"}`); resp.StatusCode != 201 {
			t.Fatalf("потік: %d %s", resp.StatusCode, out)
		}
	}
	today := time.Now()
	first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
	days := first.AddDate(0, 1, 0).AddDate(0, 0, -1).Day()
	exit := first.AddDate(0, 3, 0).Format("2006-01-02")
	card := addDebt(t, srv.URL, `{"name":"Картка","kind":"card","currency":"UAH",
		"statement_day":"30","apr_pct":"47.88","min_payment_pct":"3",
		"exit_by":"`+exit+`"}`)
	if resp, out := do(t, "POST", srv.URL+"/api/debt-marks",
		`{"debt_id":"`+did(card)+`","date":"`+first.Format("2006-01-02")+`",
		  "balance":"-90000","statement_due":"90000"}`); resp.StatusCode != 201 {
		t.Fatalf("звірка: %d %s", resp.StatusCode, out)
	}

	e := exitOf(t, srv.URL)
	if e.PaidBeforeMarkUAH.Major() != 60_000 {
		t.Errorf("прийшло до звірки %.2f, чекали зарплату 1-го — 60 000", e.PaidBeforeMarkUAH.Major())
	}
	if want := round2(90_000 + 60_000 - 10_000/float64(days)); math.Abs(e.StartDebtUAH.Major()-want) > 0.01 {
		t.Errorf("борг на початок %.2f, чекали %.2f", e.StartDebtUAH.Major(), want)
	}
	if len(e.Schedule) == 0 || e.Schedule[0].GrossUAH.Major() != 120_000 {
		t.Errorf("місяць звірки має нести обидві зарплати: %+v", e.Schedule)
	}
}

// Обернене питання до стелі: на скільки ще можна залізти в ліміт. Запас —
// стеля мінус витрати за всі місяці разом, гранична глибина — борг плюс
// запас, а межа самого ліміту банку показується лише тоді, коли ліміт
// заданий: «не заданий» і «вибраний до нуля» — різні відповіді.
func TestDebtExitHeadroomMatchesCap(t *testing.T) {
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
	exit := time.Now().AddDate(0, 3, 0).Format("2006-01-02")
	card := addDebt(t, srv.URL, `{"name":"Картка","kind":"card","currency":"UAH",
		"limit":"150000","statement_day":"30","apr_pct":"47.88","min_payment_pct":"3",
		"exit_by":"`+exit+`"}`)
	if resp, out := do(t, "POST", srv.URL+"/api/debt-marks",
		`{"debt_id":"`+did(card)+`","balance":"-90000","statement_due":"90000"}`); resp.StatusCode != 201 {
		t.Fatalf("звірка: %d %s", resp.StatusCode, out)
	}

	e := exitOf(t, srv.URL)
	if e == nil {
		t.Fatal("блоку виходу немає")
	}
	if e.Months < 1 || e.SpendCapUAH.Cmp(e.SpendUsedUAH) <= 0 {
		t.Fatalf("фікстура не дає запасу: %+v", e)
	}
	want := e.Months * (e.SpendCapUAH.Major() - e.SpendUsedUAH.Major())
	if math.Abs(e.HeadroomUAH.Major()-want) > 0.05 {
		t.Errorf("запас %.2f, чекали %.2f (%.0f міс × (%.2f − %.2f))",
			e.HeadroomUAH.Major(), want, e.Months, e.SpendCapUAH.Major(), e.SpendUsedUAH.Major())
	}
	if want := 90_000 + e.HeadroomUAH.Major(); math.Abs(e.MaxDebtUAH.Major()-want) > 0.05 {
		t.Errorf("гранична глибина %.2f, чекали %.2f", e.MaxDebtUAH.Major(), want)
	}
	if e.LimitLeftUAH == nil {
		t.Fatal("ліміт заданий, а «скільки ще дозволяє ліміт» не порахували")
	}
	if (*e.LimitLeftUAH).Major() != 60_000 {
		t.Errorf("ліміт дозволяє %.2f, чекали 60 000", (*e.LimitLeftUAH).Major())
	}
	if math.Abs(e.WithInvestHeadroomUAH.Major()-e.HeadroomUAH.Major()) > 0.05 {
		t.Errorf("при нульовій інвестчастці запаси мусять збігатись: %.2f і %.2f",
			e.WithInvestHeadroomUAH.Major(), e.HeadroomUAH.Major())
	}

	// Картка без ліміту — межі ліміту немає, а не «нуль».
	srv2, st2 := testServer(t)
	seed(t, st2)
	if resp, out := do(t, "POST", srv2.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"60000","currency":"UAH",
		  "cadence":"month","from_date":"2020-01-05","invest_pct":"0"}`); resp.StatusCode != 201 {
		t.Fatalf("потік: %d %s", resp.StatusCode, out)
	}
	card2 := addDebt(t, srv2.URL, `{"name":"Картка","kind":"card","currency":"UAH",
		"statement_day":"30","apr_pct":"47.88","min_payment_pct":"3",
		"exit_by":"`+exit+`"}`)
	if resp, out := do(t, "POST", srv2.URL+"/api/debt-marks",
		`{"debt_id":"`+did(card2)+`","balance":"-90000","statement_due":"90000"}`); resp.StatusCode != 201 {
		t.Fatalf("звірка: %d %s", resp.StatusCode, out)
	}
	if e := exitOf(t, srv2.URL); e == nil || e.LimitLeftUAH != nil {
		t.Errorf("без ліміту «скільки ще дозволяє ліміт» мусить бути відсутнім: %+v", e)
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
	if exit.NeedPerMonthUAH.Major() < 20_000 {
		t.Errorf("треба звільняти %.2f — це потреба самої лише малої картки",
			exit.NeedPerMonthUAH.Major())
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

// Рубіж покриття рахує МАЙБУТНІ ПЛАТЕЖІ, а не залишок тіла: візьмуть із
// власника тіло разом із комісіями, і на розстрочці, комісії якої не
// скасовуються, різниця між цими двома числами і є вся ціна помилки.
func TestDebtCoverCountsFuturePaymentsWithFees(t *testing.T) {
	today := domain.Date("2026-09-10")
	// 30 000 на 9 платежів, комісія 1,99% від початкової суми = 597 ₴/міс.
	// Перший платіж 30.09 — попереду всі девʼять.
	inst := domain.Debt{
		ID: 1, Kind: domain.DebtInstallment, Currency: money.UAH,
		Principal: 30_000_00, PaymentsTotal: 9, FirstPaymentDate: "2026-09-30",
		FeeMonthBp: 199, FeeOnPrepay: domain.DebtFeeKeep,
	}
	got := debtCoverUAH([]domain.Debt{inst}, nil, nil, fx.Rates{}, today)
	const want = 30_000 + 9*597 // тіло плюс девʼять комісій
	if got != want {
		t.Errorf("покриття %.2f, чекали %d — тіло разом із комісіями", got, want)
	}

	// Закритий борг не покривають: закривати нема чого.
	closed := inst
	closed.ClosedDate = "2026-09-01"
	if v := debtCoverUAH([]domain.Debt{closed}, nil, nil, fx.Rates{}, today); v != 0 {
		t.Errorf("погашений борг просить покриття %.2f", v)
	}
}

// Борг, який не можна погасити достроково, стелі подушки НЕ вмикає.
//
// Стеля стоїть на думці «гроші зараз корисніші в борзі»; там, де їх у борг
// подіти нікуди, обрізана подушка дала б менше грошей на руках при тому
// самому борзі.
func TestDebtCapsReserveIgnoresStickyFee(t *testing.T) {
	today := domain.Date("2026-09-10")
	sticky := domain.Debt{
		ID: 1, Kind: domain.DebtInstallment, Currency: money.UAH,
		Principal: 30_000_00, PaymentsTotal: 9, FirstPaymentDate: "2026-09-30",
		FeeMonthBp: 199, FeeOnPrepay: domain.DebtFeeKeep,
	}
	if debtCapsReserve([]domain.Debt{sticky}, nil, nil, 7, today) {
		t.Error("борг, який не можна погасити раніше, обрізав подушку")
	}
	// Незвірений договір поводиться так само: припускати скасування комісій
	// означало б обрізати подушку на підставі здогадки.
	unknown := sticky
	unknown.FeeOnPrepay = ""
	if debtCapsReserve([]domain.Debt{unknown}, nil, nil, 7, today) {
		t.Error("незвірений договір обрізав подушку")
	}
}

// --- планові витрати в стелі витрат (0056) ---

// Планова витрата з КАРТКИ віднімається від того, що лишається на картці:
// стеля витрат менша рівно на неї. Той самий механізм, що з розстрочками,
// і саме тому вона окремим доданком, а не всередині SpendUAH: витрата —
// подія, а SpendUAH — ритм.
func TestDebtExitSpendCapDropsOnCardPlanned(t *testing.T) {
	in := domain.CardExitInput{
		DebtUAH: 180_000_00, GrossUAH: 200_000_00, InvestUAH: 0,
		SpendUAH: 40_000_00, ExitBy: "2026-12-31",
		Today: domain.Date("2026-09-30"), Months: 3,
	}
	base := domain.CardExit(in)
	in.PlannedUAH = 30_000_00 // котел, розмазаний по трьох місяцях вікна
	with := domain.CardExit(in)

	if !base.Known || !with.Known {
		t.Fatalf("розрахунку немає: %+v / %+v", base, with)
	}
	if diff := base.SpendCap - with.SpendCap; diff != 30_000_00 {
		t.Errorf("стеля впала на %d, чекали рівно планову витрату 3000000", diff)
	}
	// Другий рядок міряється з того самого залишку — інакше два числа
	// поруч суперечили б одне одному.
	if diff := base.WithInvestSpendCap - with.WithInvestSpendCap; diff != 30_000_00 {
		t.Errorf("другий рядок впав на %d, чекали 3000000", diff)
	}
	if with.ETADate <= base.ETADate {
		t.Errorf("дата виходу %s не пізніша за %s", with.ETADate, base.ETADate)
	}
}

// СТОРОЖ ПРОТИ ПОДВІЙНОГО РАХУНКУ, дзеркальний до
// TestMonthPlanCardPlannedDoesNotTouchPlan. Витрата з ПОРТФЕЛЬНИХ грошей
// уже відбилась у плані місяця; зайшовши ще й у стелю витрат, вона
// забрала б удвічі більше, ніж коштує — рівно те, що вже коштувало
// 8 606,70 ₴/міс карткових розстрочок.
func TestDebtExitPlanPlannedDoesNotTouchCap(t *testing.T) {
	today := domain.Date("2026-09-10")
	src := &sources{
		planExpenses: []domain.PlanExpense{
			{Name: "Ремонт", Amount: 5_000_00, Currency: money.UAH,
				DueDate: "2026-10-15", PaidFrom: domain.PaidFromPlan},
			{Name: "Котел", Amount: 3_000_00, Currency: money.UAH,
				DueDate: "2026-10-20", PaidFrom: domain.PaidFromCard},
		},
	}
	if got := plannedInMonth(src, fx.Rates{}, today, 1, "", domain.PaidFromCard); got != 3000 {
		t.Errorf("картковий контур узяв %v, чекали самі 3000 — портфельна витрата "+
			"вже відбилась у плані місяця", got)
	}
	if got := plannedInMonth(src, fx.Rates{}, today, 1, "", domain.PaidFromPlan); got != 5000 {
		t.Errorf("портфельний контур узяв %v, чекали 5000", got)
	}
}

// У розкладі витрата стоїть у СВОЄМУ місяці, а не розмазана по вікну —
// заради цього розклад і потрібен поруч із середнім числом.
func TestDebtExitWalkShowsPlannedInItsMonth(t *testing.T) {
	rows := []debtMonthRow{
		{gross: 100_000, invest: 0},
		{gross: 100_000, invest: 0, planned: 30_000},
		{gross: 100_000, invest: 0},
	}
	got := debtExitWalk(rows, 200_000, 40_000, domain.Date("2026-09-10"), 0)
	if len(got) != 3 {
		t.Fatalf("у розкладі %d місяців, чекали 3: %+v", len(got), got)
	}
	if got[0].PlannedUAH.Major() != 0 || got[2].PlannedUAH.Major() != 0 {
		t.Errorf("витрата розмазалась на сусідні місяці: %+v", got)
	}
	if got[1].PlannedUAH.Major() != 30_000 {
		t.Errorf("у своєму місяці витрата %v, чекали 30000", got[1].PlannedUAH.Major())
	}
	// І борг у тому місяці меншає повільніше рівно на неї: 60 000 проти
	// 30 000 гасіння.
	if drop := got[0].LeftUAH.Major() - got[1].LeftUAH.Major(); drop != 30_000 {
		t.Errorf("у місяці витрати борг упав на %v, чекали 30000 замість звичних 60000", drop)
	}
}
