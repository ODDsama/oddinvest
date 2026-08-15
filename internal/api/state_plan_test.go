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
		sum += planFlowProvidesUAH(f, in.Today, in.Rates, planProvidesMonths)
	}
	got := buildProjection(in).PlanProvidesUAH
	if math.Abs(round2(sum)-got) > 0.005 {
		t.Fatalf("сума колонки %.2f ≠ плитка %.2f", round2(sum), got)
	}
	if got == 0 {
		t.Fatal("тест нічого не перевірив: обидва боки нулі")
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

	if got := planFlowProvidesUAH(near, today, fx.Rates{}, planProvidesMonths); math.Abs(got-10000) > 0.005 {
		t.Errorf("разова у вікні мала дати 120000/12 = 10000, маємо %.2f", got)
	}
	if got := planFlowProvidesUAH(far, today, fx.Rates{}, planProvidesMonths); got != 0 {
		t.Errorf("разова поза вікном мала дати 0, маємо %.2f", got)
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
