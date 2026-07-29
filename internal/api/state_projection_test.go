package api

import (
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
)

// forecastInput — мінімальний вхід проєкції: капітал, ціль і дедлайн.
// Решта рукавів порожня, бо перевіряємо саме ширину віяла, а не модель.
func forecastInput(t *testing.T, set *state.SettingsDoc) projectionInput {
	t.Helper()
	return projectionInput{
		Capital:  state.Capital{AccountUAH: 100_000},
		Settings: set,
		CashByCur: map[string]int64{
			money.UAH: 100_000_00,
		},
		NominalByCur: map[string]int64{},
		YieldByCur:   map[string]float64{money.UAH: 16},
		Rates:        fx.Rates{},
		Deval:        7,
		Today:        domain.Date("2026-07-15"),
	}
}

func goalSettings(goal, date string) *state.SettingsDoc {
	g := 500_000.0
	_ = goal
	return &state.SettingsDoc{GoalAmountUAH: &g, GoalDate: date}
}

// TestForecastSpreadComesFromSettings — ширина віяла сценаріїв береться з
// налаштувань, а не з констант у коді.
//
// Доти rate_spread_pp і deval_spread_pp були `const` у state_projection.go,
// тобто «песимістично» означало рівно те, що вирішив автор, і змінити це
// можна було лише перезбіркою. Тепер це припущення КОРИСТУВАЧА про ринок.
//
// Нульовий розкид — найчистіша перевірка: три ринкові сценарії мусять
// злитися в один, бо відрізняє їх лише він.
func TestForecastSpreadComesFromSettings(t *testing.T) {
	zero := 0.0
	set := goalSettings("", "2030-01-01")
	set.RateSpreadPP, set.DevalSpreadPP = &zero, &zero

	out := buildProjection(forecastInput(t, set))
	if out.Forecast == nil || len(out.Forecast.Rows) < 3 {
		t.Fatalf("прогнозу немає: %+v", out.Forecast)
	}
	var opt, real, pess float64
	for _, r := range out.Forecast.Rows {
		switch r.Key {
		case "optimistic":
			opt = r.Amount
		case "realistic":
			real = r.Amount
		case "pessimistic":
			pess = r.Amount
		}
	}
	if opt != real || pess != real {
		t.Errorf("за нульового розкиду сценарії розійшлись: опт=%v реал=%v пес=%v — "+
			"отже ширина береться не з налаштувань", opt, real, pess)
	}
}

// TestForecastSpreadDefaultsMatchOldConstants — порожні налаштування
// дають ті самі 3 і 4 п.п., що доти стояли константами.
//
// Без цього винесення констант у налаштування тихо змінило б числа всім,
// хто їх не задавав, — тобто всім наявним профілям.
func TestForecastSpreadDefaultsMatchOldConstants(t *testing.T) {
	bare := buildProjection(forecastInput(t, goalSettings("", "2030-01-01")))

	explicit := goalSettings("", "2030-01-01")
	r, d := defaultRateSpreadPP, defaultDevalSpreadPP
	explicit.RateSpreadPP, explicit.DevalSpreadPP = &r, &d
	same := buildProjection(forecastInput(t, explicit))

	if bare.Forecast == nil || same.Forecast == nil {
		t.Fatal("прогнозу немає")
	}
	for i := range bare.Forecast.Rows {
		a, b := bare.Forecast.Rows[i], same.Forecast.Rows[i]
		if a.Amount != b.Amount {
			t.Errorf("%s: без налаштування %v, із явними 3/4 — %v; спад мусить давати те саме",
				a.Key, a.Amount, b.Amount)
		}
	}
}

// TestForecastCurveEndsAtScenarioAmounts — кінець кривої дорівнює сумі
// того самого сценарію в рядках прогнозу.
//
// Це два числа про одне й те саме на одному екрані: рядок каже «на
// дедлайн буде X», крива веде туди лінією. Розійтись їм не можна —
// читач побачив би лінію, яка закінчується не там, де написано.
//
// Перевіряються ВСІ чотири сценарії. Збіг одного буває випадковим:
// сценарії відрізняються лише допущеннями, і легко зібрати криву для
// одного набору, а підпис узяти з іншого.
func TestForecastCurveEndsAtScenarioAmounts(t *testing.T) {
	in := forecastInput(t, goalSettings("", "2030-01-01"))
	in.ActualMonthly = 12_000
	f := buildProjection(in).Forecast
	if f == nil || f.Curve == nil || len(f.Curve.Points) == 0 {
		t.Fatal("кривої немає")
	}
	last := f.Curve.Points[len(f.Curve.Points)-1]
	if last.Month != f.Months {
		t.Errorf("крива обривається на місяці %d, а дедлайн на %d", last.Month, f.Months)
	}
	byKey := map[string]float64{}
	for _, r := range f.Rows {
		byKey[r.Key] = r.Amount
	}
	for _, c := range []struct {
		key  string
		curv float64
	}{
		{"realistic", last.Plan},
		{"optimistic", last.Optimistic},
		{"pessimistic", last.Pessimistic},
		{"actual", last.Actual},
	} {
		if want, ok := byKey[c.key]; ok && want != c.curv {
			t.Errorf("%s: рядок каже %v, крива веде до %v", c.key, want, c.curv)
		}
	}
	// Ціль лежить у самій кривій, щоб UI не діставав її з іншого місця й
	// не малював лінію проти числа, якого в цьому ж обʼєкті немає.
	if f.Curve.GoalUAH != f.GoalAmount {
		t.Errorf("ціль у кривій %v, а в прогнозі %v", f.Curve.GoalUAH, f.GoalAmount)
	}
}

// TestForecastCurveStaysSmall — точок близько дюжини, а не помісячно.
//
// Помісячна крива на десятирічному горизонті це 120 точок на серію,
// тобто півтисячі чисел у документі заради лінії, у якій сусідні місяці
// візуально не відрізняються. Документ читає ще й Home Assistant.
func TestForecastCurveStaysSmall(t *testing.T) {
	in := forecastInput(t, goalSettings("", "2036-07-01")) // ~10 років
	f := buildProjection(in).Forecast
	if f.Curve == nil {
		t.Fatal("кривої немає")
	}
	if n := len(f.Curve.Points); n > 16 {
		t.Errorf("точок %d за %d місяців — крок мусить рости з горизонтом",
			n, f.Months)
	}
}

// TestForecastWiderSpreadWidensFan — більший розкид розводить сценарії
// далі один від одного. Напрямок, а не конкретні числа: конкретні
// стереже golden.
func TestForecastWiderSpreadWidensFan(t *testing.T) {
	span := func(rate, deval float64) float64 {
		set := goalSettings("", "2030-01-01")
		set.RateSpreadPP, set.DevalSpreadPP = &rate, &deval
		out := buildProjection(forecastInput(t, set))
		var lo, hi float64
		for _, r := range out.Forecast.Rows {
			switch r.Key {
			case "optimistic":
				hi = r.Amount
			case "pessimistic":
				lo = r.Amount
			}
		}
		return hi - lo
	}
	narrow, wide := span(1, 1), span(6, 8)
	if wide <= narrow {
		t.Errorf("ширший розкид дав вужче віяло: %v проти %v", wide, narrow)
	}
}
