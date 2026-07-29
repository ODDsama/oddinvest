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
