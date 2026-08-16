package api

import (
	"math"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// Головний інваріант фази «План»: порожній план не має чіпати проєкцію
// взагалі. nil і порожні-але-не-nil слайси PlanFlows/PlanActions мусять
// давати РІВНО той самий документ, що й до фази «План».
func TestEmptyPlanMatchesBaseline(t *testing.T) {
	set := goalSettings("", "2030-07-15")
	base := buildProjection(forecastInput(t, set))

	in := forecastInput(t, set)
	in.PlanFlows = []store.PlanFlow{}
	in.PlanActions = []store.PlanAction{}
	got := buildProjection(in)

	if len(base.Rows) != len(got.Rows) {
		t.Fatalf("різна кількість рядків проєкції: %d vs %d", len(base.Rows), len(got.Rows))
	}
	for i := range base.Rows {
		if base.Rows[i] != got.Rows[i] {
			t.Errorf("рядок %d розійшовся: %+v vs %+v", i, base.Rows[i], got.Rows[i])
		}
	}
	if base.ContribM != got.ContribM {
		t.Errorf("ContribM розійшовся: %v vs %v", base.ContribM, got.ContribM)
	}
	if (base.Forecast == nil) != (got.Forecast == nil) {
		t.Fatalf("Forecast: один nil, другий ні")
	}
	if len(base.Forecast.Rows) != len(got.Forecast.Rows) {
		t.Fatalf("різна кількість сценаріїв: %d vs %d", len(base.Forecast.Rows), len(got.Forecast.Rows))
	}
	for i := range base.Forecast.Rows {
		if base.Forecast.Rows[i].Amount != got.Forecast.Rows[i].Amount {
			t.Errorf("сценарій %s: сума розійшлась: %v vs %v",
				base.Forecast.Rows[i].Key, base.Forecast.Rows[i].Amount, got.Forecast.Rows[i].Amount)
		}
	}
}

// Реальний потік доходу мусить дійти до кривої: без цілі (ContribM=0)
// увесь приріст над базовим сценарієм — рівно від плану.
func TestPlanFlowFeedsProjection(t *testing.T) {
	set := &state.SettingsDoc{} // без цілі — ContribM=0, ефект видно чисто від плану
	in := forecastInput(t, set)
	base := buildProjection(in)

	withPlan := in
	withPlan.PlanFlows = []store.PlanFlow{{
		Name: "Зарплата", Kind: "income", Amount: 1_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-07-15", InvestBP: 10000,
	}}
	got := buildProjection(withPlan)

	if len(base.Rows) != len(got.Rows) {
		t.Fatalf("різна кількість рядків: %d vs %d", len(base.Rows), len(got.Rows))
	}
	for i := range base.Rows {
		if got.Rows[i].WithReinvest <= base.Rows[i].WithReinvest {
			t.Errorf("%d р.: план мав додати капітал, маємо %.2f (з планом) vs %.2f (без)",
				base.Rows[i].Years, got.Rows[i].WithReinvest, base.Rows[i].WithReinvest)
		}
	}
}

// Витратний потік симетрично зменшує капітал.
func TestPlanExpenseFlowReducesProjection(t *testing.T) {
	set := &state.SettingsDoc{}
	in := forecastInput(t, set)
	base := buildProjection(in)

	withExpense := in
	withExpense.PlanFlows = []store.PlanFlow{{
		Name: "Оренда", Kind: "expense", Amount: 500_000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-07-15", InvestBP: 10000,
	}}
	got := buildProjection(withExpense)

	for i := range base.Rows {
		if got.Rows[i].WithReinvest >= base.Rows[i].WithReinvest {
			t.Errorf("%d р.: витратний потік мав зменшити капітал, маємо %.2f (з витратою) vs %.2f (без)",
				base.Rows[i].Years, got.Rows[i].WithReinvest, base.Rows[i].WithReinvest)
		}
	}
}

// invest_pct — яка частка потоку доходить до портфеля. 0% не має
// впливати на проєкцію взагалі: гроші йдуть повз, а не в портфель.
func TestPlanFlowInvestPctZeroHasNoEffect(t *testing.T) {
	set := &state.SettingsDoc{}
	in := forecastInput(t, set)
	base := buildProjection(in)

	withZero := in
	withZero.PlanFlows = []store.PlanFlow{{
		Name: "Стороннє", Kind: "income", Amount: 1_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-07-15", InvestBP: 0,
	}}
	got := buildProjection(withZero)

	for i := range base.Rows {
		if base.Rows[i].WithReinvest != got.Rows[i].WithReinvest {
			t.Errorf("%d р.: invest_pct=0 мав не вплинути, маємо %.2f vs %.2f",
				base.Rows[i].Years, got.Rows[i].WithReinvest, base.Rows[i].WithReinvest)
		}
	}
}

// ТОТОЖНІСТЬ, на якій стоїть підсумок таблиці: сума колонки «дає ₴/міс»
// по рядках == plan_provides_uah у плитці вгорі.
//
// Обидва боки — те саме означення з переставленими сумами (середнє за 12
// місяців від суми потоків проти суми середніх по потоку), тож розійтись
// їм можна рівно одним способом: якщо вікно усереднення буде записане
// двічі. Набір навмисно перебирає всі гілки — періодичності, індексацію,
// валюту, обмежене вікно, разові в межах і поза межами року, витрату.
func TestPlanFlowProvidesSumsToPlanProvides(t *testing.T) {
	in := forecastInput(t, goalSettings("", "2030-07-15"))
	in.Rates = fx.Rates{"USD": 420000}
	in.PlanFlows = []store.PlanFlow{
		{Name: "зарплата", Kind: "income", Amount: 4_000_000, Currency: "UAH",
			Cadence: "month", FromDate: "2026-07-15", InvestBP: 4000},
		{Name: "квартальна", Kind: "income", Amount: 3_000_000, Currency: "UAH",
			Cadence: "quarter", FromDate: "2026-08-15", InvestBP: 10000},
		{Name: "річна з індексацією", Kind: "income", Amount: 12_000_000, Currency: "UAH",
			Cadence: "year", FromDate: "2026-09-15", GrowthBP: 1000, InvestBP: 10000},
		{Name: "доларова", Kind: "income", Amount: 50_000, Currency: "USD",
			Cadence: "month", FromDate: "2026-07-15", InvestBP: 10000},
		{Name: "премія скоро", Kind: "income", Amount: 5_000_000, Currency: "UAH",
			Cadence: "once", FromDate: "2026-10-15", InvestBP: 10000},
		{Name: "премія нескоро", Kind: "income", Amount: 5_000_000, Currency: "UAH",
			Cadence: "once", FromDate: "2029-01-15", InvestBP: 10000},
		{Name: "оренда", Kind: "expense", Amount: 1_500_000, Currency: "UAH",
			Cadence: "month", FromDate: "2026-07-15", InvestBP: 10000},
		{Name: "курси", Kind: "expense", Amount: 800_000, Currency: "UAH",
			Cadence: "month", FromDate: "2026-07-15", UntilDate: "2026-12-15", InvestBP: 10000},
	}

	var sum float64
	for _, f := range in.PlanFlows {
		sum += planFlowProvidesUAH(f, in.Today, in.Rates, planProvidesMonths, nil)
	}
	got := buildProjection(in).PlanProvidesUAH
	if math.Abs(round2(sum)-got) > 0.005 {
		t.Fatalf("сума колонки %.2f ≠ плитка %.2f", round2(sum), got)
	}
	if got == 0 {
		t.Fatal("тест нічого не перевірив: обидва боки нулі")
	}

	// Та сама тотожність, але З ВІДМІТКАМИ. Це головний сторож фази:
	// відмітки заходять у чотири місця (колонка таблиці, planTotal, профіль,
	// історія), і забутий marks в одному з них проявився б саме тут —
	// таблиця показувала б чистий план, а плитка над нею вже з нулями.
	// Потоки в цьому наборі йдуть без id (усі нулі), тож спершу роздамо їх:
	// planMarks шукає саме за id, і на нульових вона мовчала б.
	for i := range in.PlanFlows {
		in.PlanFlows[i].ID = int64(i + 1)
	}
	// Гривнева зарплата не прийшла зовсім, доларова прийшла меншою — обидві
	// гілки, і нативна теж.
	in.PlanReceipts = []store.PlanReceipt{
		{FlowID: 1, Month: monthKeyAt(in.Today, 1), Amount: 0, Currency: "UAH"},
		{FlowID: 4, Month: monthKeyAt(in.Today, 2), Amount: 20_000, Currency: "USD"},
	}
	marks := newPlanMarks(in.PlanReceipts)
	var sumM float64
	for _, f := range in.PlanFlows {
		sumM += planFlowProvidesUAH(f, in.Today, in.Rates, planProvidesMonths, marks)
	}
	gotM := buildProjection(in).PlanProvidesUAH
	if math.Abs(round2(sumM)-gotM) > 0.005 {
		t.Fatalf("з відмітками: сума колонки %.2f ≠ плитка %.2f", round2(sumM), gotM)
	}
	// І відмітки мусять справді щось змінити — інакше тест зелений даремно.
	if math.Abs(gotM-got) < 0.005 {
		t.Fatalf("відмітки не вплинули на «План дає»: було %.2f, стало %.2f", got, gotM)
	}
}

// Відмітка міняє СВІЙ місяць і не чіпає сусідні.
//
// Найпростіша властивість, і найлегша до втрати: варто накласти факт не на
// місяць, а на потік — і один нуль обнулив би зарплату назавжди.
func TestMarkOverridesPlanForItsMonthOnly(t *testing.T) {
	today := domain.Date("2026-07-15")
	f := store.PlanFlow{
		ID: 1, Name: "Зарплата", Kind: "income", Amount: 4_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-01-17", InvestBP: 10000,
	}
	marks := newPlanMarks([]store.PlanReceipt{
		{FlowID: 1, Month: monthKeyAt(today, 2), Amount: 1_000_000},
	})
	if got := planFlowNative(f, today, 1, marks); got != 40000 {
		t.Errorf("місяць 1 (без відмітки) мав лишитись 40000, маємо %.2f", got)
	}
	if got := planFlowNative(f, today, 2, marks); got != 10000 {
		t.Errorf("місяць 2 (відмічений) мав дати 10000, маємо %.2f", got)
	}
	if got := planFlowNative(f, today, 3, marks); got != 40000 {
		t.Errorf("місяць 3 (без відмітки) мав лишитись 40000, маємо %.2f", got)
	}
	// Минуле читається тим самим ключем місяця, тож відмітка на -2 має
	// діяти так само, як на +2. Інакше історія й прогноз розуміли б слово
	// «травень» по-різному.
	past := newPlanMarks([]store.PlanReceipt{
		{FlowID: 1, Month: monthKeyAt(today, -2), Amount: 500_000},
	})
	if got := planFlowNativePast(f, today, -2, past); got != 5000 {
		t.Errorf("минулий відмічений місяць мав дати 5000, маємо %.2f", got)
	}
	if got := planFlowNativePast(f, today, -3, past); got != 40000 {
		t.Errorf("сусідній минулий місяць мав лишитись 40000, маємо %.2f", got)
	}
}

// Нуль у відмітці — це «не прийшло», а не «відмітки немає».
//
// Обидва стани в коді виглядають як нульова сума, і саме тому їх
// розрізняє наявність КЛЮЧА, а не значення. Сплутавши їх, застосунок
// перестав би розрізняти єдині два стани, заради яких фіча існує.
func TestZeroMarkIsNotAbsentMark(t *testing.T) {
	today := domain.Date("2026-07-15")
	f := store.PlanFlow{
		ID: 1, Kind: "income", Amount: 4_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-01-17", InvestBP: 10000,
	}
	zero := newPlanMarks([]store.PlanReceipt{
		{FlowID: 1, Month: monthKeyAt(today, 1), Amount: 0},
	})
	if got := planFlowNative(f, today, 1, zero); got != 0 {
		t.Errorf("відмічений нуль мав дати 0, маємо %.2f", got)
	}
	if got := planFlowNative(f, today, 1, nil); got != 40000 {
		t.Errorf("без відмітки мало лишитись 40000, маємо %.2f", got)
	}
}

// Відмітка заміщає СУМУ, а не факт виплати: у місяці, коли потік мовчить,
// вона не створює грошей із нічого.
//
// Сценарій, який це ловить, цілком буденний: квартальний потік відмітили,
// потім перевели на щомісячний — або навпаки. Гілка накладання стоїть
// ПІСЛЯ перевірок періодичності саме тому, що зверху застаріла відмітка
// почала б тихо платити в чужому місяці.
func TestMarkDoesNotCreateAPaymentPlanDoesNotHave(t *testing.T) {
	today := domain.Date("2026-07-15")
	q := store.PlanFlow{
		ID: 1, Kind: "income", Amount: 3_000_000, Currency: "UAH",
		Cadence: "quarter", FromDate: "2026-08-15", InvestBP: 10000,
	}
	// Місяць 2 у квартального потоку порожній (платить 1, 4, 7…).
	if got := planFlowNative(q, today, 2, nil); got != 0 {
		t.Fatalf("передумова: місяць 2 мав бути порожній, маємо %.2f", got)
	}
	stale := newPlanMarks([]store.PlanReceipt{
		{FlowID: 1, Month: monthKeyAt(today, 2), Amount: 3_000_000},
	})
	if got := planFlowNative(q, today, 2, stale); got != 0 {
		t.Errorf("застаріла відмітка створила виплату з нічого: %.2f", got)
	}
	// А у свій місяць — заміщає як належить.
	live := newPlanMarks([]store.PlanReceipt{
		{FlowID: 1, Month: monthKeyAt(today, 1), Amount: 1_000_000},
	})
	if got := planFlowNative(q, today, 1, live); got != 10000 {
		t.Errorf("відмітка у свій місяць мала дати 10000, маємо %.2f", got)
	}
}

// «Валове» лишається валовим і для відмічених місяців.
//
// planFlowGrossUAH працює фокусом — копією потоку зі InvestBP = 10000, — і
// саме тому гілка накладання бере частку З ПОТОКУ, а не з відмітки: інакше
// фокус перестав би діяти рівно там, де відмітка є, і колонка «повне ₴/міс»
// показувала б у відмічених місяцях уже урізану суму.
func TestMarkedGrossIgnoresInvestShare(t *testing.T) {
	today := domain.Date("2026-07-15")
	f := store.PlanFlow{
		ID: 1, Kind: "income", Amount: 4_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-01-17", InvestBP: 2500,
	}
	// Відмічено 1 000 000 (10 000 ₴) у кожному з 12 місяців вікна.
	var rs []store.PlanReceipt
	for m := 1; m <= planProvidesMonths; m++ {
		rs = append(rs, store.PlanReceipt{FlowID: 1, Month: monthKeyAt(today, m), Amount: 1_000_000})
	}
	marks := newPlanMarks(rs)
	if got := planFlowGrossUAH(f, today, fx.Rates{}, planProvidesMonths, marks); got != 10000 {
		t.Errorf("валове мало бути 10000, маємо %.2f", got)
	}
	if got := planFlowProvidesUAH(f, today, fx.Rates{}, planProvidesMonths, marks); got != 2500 {
		t.Errorf("у портфель мало бути 2500 (25%%), маємо %.2f", got)
	}
}

// Індексація до відміченого місяця не застосовується: сума з відмітки — це
// те, що прийшло насправді, і множити її на очікуване зростання означало б
// виправляти факт припущенням.
func TestMarkIgnoresIndexation(t *testing.T) {
	today := domain.Date("2026-07-15")
	f := store.PlanFlow{
		ID: 1, Kind: "income", Amount: 1_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-07-15", GrowthBP: 10000, InvestBP: 10000,
	}
	// Через рік індексація подвоює: місяць 13 без відмітки дає 20 000.
	if got := planFlowNative(f, today, 13, nil); got != 20000 {
		t.Fatalf("передумова: місяць 13 мав дати 20000, маємо %.2f", got)
	}
	marks := newPlanMarks([]store.PlanReceipt{
		{FlowID: 1, Month: monthKeyAt(today, 13), Amount: 1_000_000},
	})
	if got := planFlowNative(f, today, 13, marks); got != 10000 {
		t.Errorf("відмічений місяць мав дати рівно 10000, маємо %.2f", got)
	}
}

// Потік, закритий заднім числом, не платить БІЛЬШЕ НІЧОГО.
//
// Доти дата кінця обрізалась знизу одиницею — так само, як дата початку, —
// і це давало end = 1, а перевірка ловила лише m > end. На першому місяці
// умова була хибна, тож джерело доходу, якого вже пів року немає, платило
// ще один раз: зайвий місяць у проєкції й зайва 1/12 у колонці.
//
// Для дати ПОЧАТКУ те саме обрізання лишається правильним, і сусідній
// підтест це стереже: потік, заведений торік, уже діє.
func TestPlanFlowClosedInPastPaysNothing(t *testing.T) {
	today := domain.Date("2026-07-15")
	closed := store.PlanFlow{
		Name: "Стара робота", Kind: "income", Amount: 4_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2024-01-01", UntilDate: "2026-01-31", InvestBP: 10000,
	}
	for m := 1; m <= 24; m++ {
		if got := planFlowNative(closed, today, m, nil); got != 0 {
			t.Fatalf("місяць %d: закритий торік потік мав дати 0, маємо %.2f", m, got)
		}
	}
	if got := planFlowProvidesUAH(closed, today, fx.Rates{}, planProvidesMonths, nil); got != 0 {
		t.Errorf("колонка «дає ₴/міс» мала бути 0, маємо %.2f", got)
	}

	// Контроль: початок у минулому й далі означає «вже діє».
	running := closed
	running.UntilDate = ""
	if got := planFlowNative(running, today, 1, nil); got != 40000 {
		t.Errorf("потік, заведений торік і не закритий, мав платити 40000, маємо %.2f", got)
	}

	// МЕЖА, на якій легко перестаратись у ДРУГИЙ бік: кінець ЦЬОГО місяця
	// теж дає нуль, бо місяць 1 — це НАСТУПНИЙ місяць (профіль підписує
	// точки як today.AddMonths(m) від m=1). Потік, що закінчується до
	// початку вікна, у вікно не потрапляє.
	//
	// Саме цей випадок робить кнопка «⇗»: закриваючи старий рядок
	// напередодні нової зарплати з 1-го числа наступного місяця, вона
	// ставить кінець поточного — і перекриття з новим рядком не виникає.
	thisMonth := closed
	thisMonth.UntilDate = "2026-07-31" // той самий місяць, що й today
	for m := 1; m <= 3; m++ {
		if got := planFlowNative(thisMonth, today, m, nil); got != 0 {
			t.Errorf("місяць %d: кінець поточного місяця мав дати 0, маємо %.2f", m, got)
		}
	}
	// І симетрично: наступник, що починається з 1-го наступного місяця,
	// платить із першого ж місяця вікна — разом вони дають рівно один
	// потік без діри й без перекриття.
	next := closed
	next.FromDate, next.UntilDate, next.Amount = "2026-08-01", "", 5_000_000
	if got := planFlowNative(next, today, 1, nil); got != 50000 {
		t.Errorf("наступник мав платити 50000 з першого місяця, маємо %.2f", got)
	}

	// І далі по осі: останній місяць дії платить, наступний — уже ні.
	ending := closed
	ending.UntilDate = "2026-09-15" // +2 місяці від today
	if got := planFlowNative(ending, today, 2, nil); got == 0 {
		t.Error("останній місяць дії мав заплатити")
	}
	if got := planFlowNative(ending, today, 3, nil); got != 0 {
		t.Errorf("після дати кінця мав дати 0, маємо %.2f", got)
	}
}

// Разова стаття всередині річного вікна дає суму/12, поза ним — рівно
// нуль. Це та межа, на якій колонка найлегше починає брехати.
func TestPlanFlowProvidesOnceWindow(t *testing.T) {
	today := domain.Date("2026-07-15")
	near := store.PlanFlow{Name: "премія", Kind: "income", Amount: 12_000_000, Currency: "UAH",
		Cadence: "once", FromDate: "2026-10-15", InvestBP: 10000}
	far := near
	far.FromDate = "2029-01-15"

	if got := planFlowProvidesUAH(near, today, fx.Rates{}, planProvidesMonths, nil); math.Abs(got-10000) > 0.005 {
		t.Errorf("разова у вікні мала дати 120000/12 = 10000, маємо %.2f", got)
	}
	if got := planFlowProvidesUAH(far, today, fx.Rates{}, planProvidesMonths, nil); got != 0 {
		t.Errorf("разова поза вікном мала дати 0, маємо %.2f", got)
	}
}

// marketRows — три ринкові сценарії прогнозу (без рядка «За фактом»).
func marketRows(t *testing.T, in projectionInput) []state.ForecastRow {
	t.Helper()
	out := buildProjection(in)
	if out.Forecast == nil {
		t.Fatal("прогнозу немає — тест нічого не перевірить")
	}
	var rows []state.ForecastRow
	for _, r := range out.Forecast.Rows {
		if r.Key != "actual" {
			rows = append(rows, r)
		}
	}
	if len(rows) != 3 {
		t.Fatalf("мало бути 3 ринкові сценарії, маємо %d", len(rows))
	}
	return rows
}

// Інваріант 1, найсильніший: БЕЗ плану «треба з нуля» і «треба понад план»
// — це те саме число, точно.
//
// Тримається бітово, а не приблизно: newSleeveFactory завжди виділяє
// f.plan[cur], тож єдина різниця плановільного шляху — nil замість
// занулених слайсів, а contribAt перевіряє на nil і додає той самий нуль.
//
// ПАСТКА: не порівнювати самі []domain.Sleeve через reflect.DeepEqual —
// вони різняться рівно цим nil-проти-порожнього, і тест завалився б на
// істинному інваріанті. Порівнюємо числа.
func TestRequiredTotalEqualsRequiredOnEmptyPlan(t *testing.T) {
	in := forecastInput(t, goalSettings("", "2030-07-15"))
	for _, r := range marketRows(t, in) {
		if r.RequiredMonthly != r.RequiredTotalMonthly {
			t.Errorf("%s: без плану числа мали збігтись, маємо %.2f проти %.2f",
				r.Key, r.RequiredTotalMonthly, r.RequiredMonthly)
		}
		if r.RequiredMonthly == 0 {
			t.Errorf("%s: тест нічого не перевірив — обидва нулі", r.Key)
		}
	}
}

// realisticRow — реалістичний сценарій прогнозу для заданого входу.
func realisticRow(t *testing.T, in projectionInput) (state.ForecastRow, projectionPhase) {
	t.Helper()
	out := buildProjection(in)
	if out.Forecast == nil {
		t.Fatal("прогнозу немає — тест нічого не перевірить")
	}
	for _, r := range out.Forecast.Rows {
		if r.Key == "realistic" {
			return r, out
		}
	}
	t.Fatal("реалістичного сценарію немає")
	return state.ForecastRow{}, out
}

// Інваріант 2: на РІВНОМУ плані «треба з нуля» = «дає план» + «бракує» —
// але ЛИШЕ поки плану справді бракує.
//
// Друга половина тесту фіксує межу, на яку легко натрапити випадково:
// «бракує» впирається в нуль знизу (більше, ніж досить, — це все одно
// нуль), тож щойно план перекриває ціль, тотожність перестає триматись, і
// «треба» стає МЕНШИМ за сам план. Це не вада: питання «скільки бракує»
// просто не має відʼємної відповіді.
//
// Допуск 1 ₴, а не 0.02: три незалежні round2 плюс 60 ітерацій бісекції.
// Сама рівність можлива лише тому, що потік рівний і гривневий — тоді
// 12-місячне середнє плану й плаский внесок бісекції взаємозамінні.
func TestRequiredTotalMinusPlanEqualsGapOnFlatPlan(t *testing.T) {
	flow := func(amount int64) []store.PlanFlow {
		return []store.PlanFlow{{
			Name: "Зарплата", Kind: "income", Amount: amount, Currency: "UAH",
			Cadence: "month", FromDate: "2026-07-15", InvestBP: 10000,
		}}
	}

	// План, якого НЕ ВИСТАЧАЄ: 3 000 ₴/міс проти потрібних ~7 900.
	in := forecastInput(t, goalSettings("", "2030-07-15"))
	in.PlanFlows = flow(300_000)
	real, out := realisticRow(t, in)
	if real.RequiredMonthly <= 0 {
		t.Fatalf("план мав лишити нестачу, інакше тотожність не перевіряється: бракує=%.2f",
			real.RequiredMonthly)
	}
	if d := real.RequiredTotalMonthly - out.PlanProvidesUAH - real.RequiredMonthly; math.Abs(d) > 1 {
		t.Errorf("треба(%.2f) − план(%.2f) − бракує(%.2f) = %.2f, мало бути ~0",
			real.RequiredTotalMonthly, out.PlanProvidesUAH, real.RequiredMonthly, d)
	}

	// План, якого З ЛИШКОМ: «бракує» лягає в нуль, і «треба» лишається
	// меншим за план — саме так це й має читатись на екрані («із запасом»).
	rich := forecastInput(t, goalSettings("", "2030-07-15"))
	rich.PlanFlows = flow(2_000_000)
	realRich, outRich := realisticRow(t, rich)
	if realRich.RequiredMonthly != 0 {
		t.Errorf("план із лишком мав дати нульову нестачу, маємо %.2f", realRich.RequiredMonthly)
	}
	if realRich.RequiredTotalMonthly >= outRich.PlanProvidesUAH {
		t.Errorf("треба(%.2f) мало лишитись меншим за план(%.2f)",
			realRich.RequiredTotalMonthly, outRich.PlanProvidesUAH)
	}
	// І головне: саме «треба» не залежить від того, який у тебе план.
	if math.Abs(realRich.RequiredTotalMonthly-real.RequiredTotalMonthly) > 1 {
		t.Errorf("«треба з нуля» не сміє залежати від плану: %.2f проти %.2f",
			realRich.RequiredTotalMonthly, real.RequiredTotalMonthly)
	}
}

// Інваріант 3: на НЕРІВНОМУ плані лишається сама нерівність, і це не
// послаблення тесту, а зафіксована причина.
//
// Рівність із попереднього тесту тут НЕ тримається, бо (а) plan_provides
// — середнє за 12 місяців, а план діє на весь горизонт до дедлайну, тож
// разова премія дає суму/12 у число й нічого в роки 2-5; і (б) ранні
// гроші компаундяться довше, тож фронт-навантажений план вартий більше за
// рівний потік того самого середнього.
//
// ПЕРЕДУМОВА: нерівність тримається, поки місячний вектор плану
// невідʼємний. Витратні потоки можуть зробити «бракує» БІЛЬШИМ за «треба
// з нуля» — це законно, а не баг, тож тест ганяє самі доходи.
func TestRequiredTotalOnlyDominatesGapOnUnevenPlan(t *testing.T) {
	in := forecastInput(t, goalSettings("", "2030-07-15"))
	in.PlanFlows = []store.PlanFlow{
		{Name: "Премія", Kind: "income", Amount: 15_000_000, Currency: "UAH",
			Cadence: "once", FromDate: "2026-10-15", InvestBP: 10000},
		{Name: "Квартальна", Kind: "income", Amount: 6_000_000, Currency: "UAH",
			Cadence: "quarter", FromDate: "2026-08-15", InvestBP: 10000},
	}
	out := buildProjection(in)
	var real state.ForecastRow
	for _, r := range out.Forecast.Rows {
		if r.Key == "realistic" {
			real = r
		}
	}
	if real.RequiredTotalMonthly < real.RequiredMonthly {
		t.Errorf("треба з нуля (%.2f) не може бути меншим за нестачу понад план (%.2f)",
			real.RequiredTotalMonthly, real.RequiredMonthly)
	}
	// І документуємо, що проста рівність тут саме НЕ виконується.
	if d := math.Abs(real.RequiredTotalMonthly - out.PlanProvidesUAH - real.RequiredMonthly); d <= 1 {
		t.Errorf("на нерівному плані рівність не мала триматись, а розбіжність лише %.2f — "+
			"схоже, план став рівним і тест перевіряє не те, що заявляє", d)
	}
}

// Потік, заведений у валюті, не всихає на горизонті.
//
// Доти він переводився в гривню СЬОГОДНІШНІМ курсом і в такому вигляді
// лягав у вектор, а рукав потім чесно ділив його на курс, ЩО РОСТЕ
// (sleeves.go:contribAt) — тобто модель з'їдала рівно множник знецінення,
// і $500/міс до кінця десятирічного горизонту перетворювались на ~$275.
//
// Перевірка будується так, щоб різниця могла бути ЛИШЕ у валютній
// поведінці: частка USD = 100%, тож гривневий потік теж іде цілком у
// доларовий рукав, а суми рівні за сьогоднішнім курсом ($500 = 21 000 ₴
// при 42 ₴/$).
func TestPlanForeignFlowKeepsItsCurrency(t *testing.T) {
	hundred := 100.0
	base := func(deval float64) projectionInput {
		set := &state.SettingsDoc{USDTargetSharePct: &hundred} // без цілі: ContribM=0
		in := forecastInput(t, set)
		in.Rates = fx.Rates{"USD": 420000} // 42.0000 ₴/$
		in.Deval = deval
		// Наявну гривневу готівку прибираємо: підсумок міряний у
		// СЬОГОДНІШНІХ гривнях, тож її реальна вартість сама по собі
		// їде від знецінення — і ховала б те, що ми тут міряємо.
		in.Capital = state.Capital{}
		in.CashByCur = map[string]int64{}
		return in
	}
	usdFlow := store.PlanFlow{
		Name: "Зарплата", Kind: "income", Amount: 50_000, Currency: "USD",
		Cadence: "month", FromDate: "2026-07-15", InvestBP: 10000,
	}
	uahFlow := store.PlanFlow{
		Name: "Зарплата", Kind: "income", Amount: 2_100_000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-07-15", InvestBP: 10000,
	}
	run := func(deval float64, fl store.PlanFlow) []state.ProjectionRow {
		in := base(deval)
		in.PlanFlows = []store.PlanFlow{fl}
		return buildProjection(in).Rows
	}

	// Без знецінення долар і гривня — те саме: обидва дають однаковий
	// капітал, бо курс не рухається.
	flat, flatUAH := run(0, usdFlow), run(0, uahFlow)
	for i := range flat {
		if d := math.Abs(flat[i].WithReinvest - flatUAH[i].WithReinvest); d > 1 {
			t.Errorf("%d р. без знецінення: долар і гривня мали збігтись, розбіжність %.2f",
				flat[i].Years, d)
		}
	}

	// Зі знеціненням гривневий потік купує щомісяця менше доларів, а
	// доларовий лишається доларовим — тож він мусить дати СТРОГО більше.
	usd, uah := run(10, usdFlow), run(10, uahFlow)
	for i := range usd {
		if usd[i].WithReinvest <= uah[i].WithReinvest {
			t.Errorf("%d р.: доларовий потік мав дати більше за гривневий (%.2f vs %.2f) — "+
				"схоже, він знову проходить через сьогоднішній курс",
				usd[i].Years, usd[i].WithReinvest, uah[i].WithReinvest)
		}
	}
	// І сам доларовий потік не має залежати від знецінення гривні взагалі:
	// його рукав живе у своїй валюті.
	for i := range usd {
		if d := math.Abs(usd[i].WithReinvest - flat[i].WithReinvest); d > 1 {
			t.Errorf("%d р.: знецінення гривні зрушило доларовий потік на %.2f", usd[i].Years, d)
		}
	}
}

// Дія set_shares перенаправляє МАЙБУТНІ (від дати дії) внески плану в
// іншу валюту — сама наявність доларового рукава в результаті й доводить,
// що дія дійшла до симуляції.
func TestPlanSetSharesRoutesFutureContributions(t *testing.T) {
	set := goalSettings("", "2030-07-15")
	in := forecastInput(t, set)
	in.Rates = fx.Rates{"USD": 420000} // 42.0000 ₴/$
	in.PlanFlows = []store.PlanFlow{{
		Name: "Дохід", Kind: "income", Amount: 10_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-07-15", InvestBP: 10000,
	}}

	amounts := func(p projectionPhase) map[string]float64 {
		out := map[string]float64{}
		if p.Forecast == nil {
			return out
		}
		for _, r := range p.Forecast.Rows {
			if r.Key != "realistic" {
				continue
			}
			for _, s := range r.ByCurrency {
				out[s.Currency] = s.Amount
			}
		}
		return out
	}

	base := amounts(buildProjection(in))
	if base["USD"] != 0 {
		t.Fatalf("без дії долара в плані не мало бути: %v", base)
	}

	withAction := in
	usd := int64(10000) // 100%
	withAction.PlanActions = []store.PlanAction{
		{Date: "2026-08-15", Type: "set_shares", USDBP: usd, EURBP: -1},
	}
	got := amounts(buildProjection(withAction))
	if got["USD"] <= 0 {
		t.Errorf("після set_shares(USD=100%%) дохід мав піти в долар, маємо %v", got)
	}
}

// Дія lock мусить дійти до симуляції: капітал на горизонті — не той
// самий, що й без неї (ставка замка відрізняється від дохідності
// портфеля, тож нейтральний збіг виключено), і лишається додатним і
// скінченним, тобто модель не зламалась.
func TestPlanLockActionAppliesToProjection(t *testing.T) {
	set := &state.SettingsDoc{}
	in := forecastInput(t, set)
	base := buildProjection(in)

	withLock := in
	withLock.PlanActions = []store.PlanAction{{
		Date: "2026-12-15", Type: "lock", USDBP: -1, EURBP: -1,
		Amount: 5_000_000, Currency: "UAH", RateBP: 2000, Months: 12, Name: "тест",
	}}
	got := buildProjection(withLock)

	for i := range base.Rows {
		if got.Rows[i].WithReinvest == base.Rows[i].WithReinvest {
			t.Errorf("%d р.: замок мав змінити капітал (ставка 20%% проти дохідності портфеля), маємо %.2f обидва рази",
				base.Rows[i].Years, got.Rows[i].WithReinvest)
		}
		if got.Rows[i].WithReinvest <= 0 {
			t.Errorf("%d р.: капітал із замком має лишатись додатним, маємо %.2f", base.Rows[i].Years, got.Rows[i].WithReinvest)
		}
	}
}

// Розгортання плану НАЗАД у часі. Це окрема функція саме тому, що для
// минулого дата початку не підтягується до першого місяця, а дата «до» не
// стирає потік із минулого: обидва підтягування правильні лише вперед.
func TestPlanFlowNativePast(t *testing.T) {
	today := domain.Date("2026-08-15")

	old := store.PlanFlow{
		Name: "стара зарплата", Kind: "income", Amount: 3_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2025-03-10", UntilDate: "2026-05-20", InvestBP: 10000,
	}
	// Закрита «⇗» у травні — у квітні (m=-4) ще платила, у липні (m=-1) уже ні.
	if got := planFlowNativePast(old, today, -4, nil); got != 30000 {
		t.Errorf("квітень: закритий у травні потік мав ще платити 30000, маємо %v", got)
	}
	if got := planFlowNativePast(old, today, -1, nil); got != 0 {
		t.Errorf("липень: потік, закритий у травні, платити не мав, маємо %v", got)
	}
	// Вперед поведінка та сама, що й була: закритий торік не платить нічого.
	if got := planFlowNative(old, today, 1, nil); got != 0 {
		t.Errorf("уперед закритий потік мав давати 0, маємо %v", got)
	}

	// Заведений цього тижня в минулому НЕ платив: підтягування start до
	// одиниці («уже діє») чинне лише для майбутнього.
	fresh := store.PlanFlow{
		Name: "нова", Kind: "income", Amount: 1_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-08-12", InvestBP: 10000,
	}
	if got := planFlowNativePast(fresh, today, -1, nil); got != 0 {
		t.Errorf("липень: потік із 12 серпня в липні не платив, маємо %v", got)
	}
	if got := planFlowNative(fresh, today, 1, nil); got != 10000 {
		t.Errorf("уперед потік із 12 серпня мав платити 10000, маємо %v", got)
	}

	// Разова виплата минулого місяця влучає рівно у свій місяць.
	once := store.PlanFlow{
		Name: "премія", Kind: "income", Amount: 2_500_000, Currency: "UAH",
		Cadence: "once", FromDate: "2026-06-01", InvestBP: 10000,
	}
	if got := planFlowNativePast(once, today, -2, nil); got != 25000 {
		t.Errorf("червень: разова мала дати 25000, маємо %v", got)
	}
	if got := planFlowNativePast(once, today, -3, nil); got != 0 {
		t.Errorf("травень: разової червневої там бути не мало, маємо %v", got)
	}

	// Частка в портфель і знак витрати діють назад так само, як уперед:
	// ядро в обох напрямків одне.
	exp := store.PlanFlow{
		Name: "оренда", Kind: "expense", Amount: 1_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2025-01-10", InvestBP: 5000,
	}
	if got := planFlowNativePast(exp, today, -6, nil); got != -5000 {
		t.Errorf("витрата з часткою 50%% мала дати -5000, маємо %v", got)
	}
}

// Місяць ПЕРЕДАЧІ платить один раз, а не двічі.
//
// Це намір, записаний у самій кнопці «⇗»: вона закриває старий рядок
// напередодні нової дати, «щоб місяць зміни не оплатили обидва рядки». Доти
// намір не виконувався — monthOffsetRaw бачить лише рік і місяць, тож 16 і
// 17 травня для нього однакові, і травень платив 21 041 ₴ замість 8 941 ₴.
func TestPlanFlowHandoverMonthPaysOnce(t *testing.T) {
	today := domain.Date("2026-08-15")
	// Зарплата 17-го числа, підвищена з 17 травня: старий рядок закритий
	// 16-м, як це робить «⇗».
	old := store.PlanFlow{
		Name: "стара", Kind: "income", Amount: 1_700_000, Currency: "UAH",
		Cadence: "month", FromDate: "2025-11-17", UntilDate: "2026-05-16", InvestBP: 10000,
	}
	next := store.PlanFlow{
		Name: "нова", Kind: "income", Amount: 2_000_000, Currency: "UAH",
		Cadence: "month", FromDate: "2026-05-17", InvestBP: 10000,
	}
	const may = -3 // травень 2026 відносно 15 серпня 2026

	if got := planFlowNativePast(old, today, may, nil); got != 0 {
		t.Errorf("стара зарплата закрита 16-м, а платіжний день 17-й — мала дати 0, маємо %v", got)
	}
	if got := planFlowNativePast(next, today, may, nil); got != 20000 {
		t.Errorf("нова зарплата з 17 травня мала заплатити 20000, маємо %v", got)
	}
	if got := planFlowNativePast(old, today, may-1, nil); got != 17000 {
		t.Errorf("квітень: стара зарплата ще діяла й мала дати 17000, маємо %v", got)
	}

	// Зворотний випадок: зміна з дати ПІЗНІШОЇ за платіжний день. Оклад
	// приходить 5-го, нова сума з 20-го — того місяця справді приходять
	// обидві виплати, і модель мусить це показати, а не «виправити».
	early := old
	early.FromDate, early.UntilDate = "2025-11-05", "2026-05-19"
	if got := planFlowNativePast(early, today, may, nil); got != 17000 {
		t.Errorf("оклад 5-го, закритий 19-м, того місяця ще приходив — чекали 17000, маємо %v", got)
	}

	// І межа рівності: закриття рівно в платіжний день ще платить.
	same := old
	same.UntilDate = "2026-05-17"
	if got := planFlowNativePast(same, today, may, nil); got != 17000 {
		t.Errorf("закриття рівно в платіжний день мало ще заплатити 17000, маємо %v", got)
	}
}
