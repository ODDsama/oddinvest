package api

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/engine"
	"github.com/ODDsama/oddinvest/internal/state"
	money "github.com/Rhymond/go-money"
)

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

// monthPlanOf — план місяця зі зведення. Через HTTP, а не через BuildState:
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
	if want := engine.Round2(90_000 - 10_000/float64(days)); math.Abs(e.StartDebtUAH.Major()-want) > 0.01 {
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
	if want := engine.Round2(90_000 + 60_000 - 10_000/float64(days)); math.Abs(e.StartDebtUAH.Major()-want) > 0.01 {
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

// --- планові витрати в стелі витрат (0056) ---
