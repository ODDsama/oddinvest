package api

import (
	"testing"

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
