// Прогноз капіталу за нинішнім темпом — на будь-який місяць до тридцяти
// років, і сам темп помісячно.
//
// МОДЕЛЬ ТА САМА, що в рядку «За фактом» таблиці projection: рукави без
// плану надходжень (sleeveFactory.buildPlanFree), внесок — виміряний темп,
// ринок — реалістичний. Власної арифметики тут немає навмисно: точки на
// 12, 36, 60 і 120 місяцях мусять збігатися з projection[].with_reinvest_
// actual до копійки, і тест саме це й тримає. Нове тут одне — горизонт:
// доти він був прибитий до чотирьох років із переліку або до дедлайну мети.
//
// ЧОМУ ОКРЕМИЙ МАРШРУТ, А НЕ ПОЛЕ ДОКУМЕНТА СТАНУ. Довід той самий, що при
// /api/fx-shock: документ публікується в MQTT і щодня лягає в добовий
// знімок, а тут 361 точка по сім сум. Контракт, фікстури й інтеграція HA
// не чіпаються зовсім.
//
// ЧОМУ ВСІ ТОЧКИ ОДРАЗУ, А НЕ ?months=N. Повзунок у браузері мусить
// відповідати на кожен рух, а рахувати там не можна (арифметика живе в
// одному місці). Тож сервер віддає відповідь на КОЖЕН місяць, разом із
// «наросло», і браузер лише вибирає точку. Одна симуляція на серію, не на
// точку — ProjectSleevesSeries проходить місяці раз.
//
// ВІДОМА ВАДА, спільна з рядком «За фактом» і названа в state_month.go:
// темп рахує всі зовнішні гроші, зокрема ті, що пішли в резерв і цілі, а
// модель кладе їх у папери. Лікувати треба обидва місця разом.
package engine

import (
	"context"
	"math"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
)

// OutlookMonths — горизонт прогнозу: тридцять років. Довше не має сенсу
// не через модель, а через припущення: ставка після сповзання й знецінення
// — константи, і на шістдесяти роках крива показувала б лише їх.
const OutlookMonths = 360

// paceBarMonths — скільки календарних місяців темпу показувати стовпчиками.
// Удвічі довше за вікно самого темпу (183 дні): видно і те, з чого число
// складене, і те, що було до того.
const paceBarMonths = 12

// OutlookPoint — капітал на кінець одного місяця горизонту.
type OutlookPoint struct {
	Month int         `json:"month"`
	Date  domain.Date `json:"date"`
	// Amount — за нинішнім темпом, у сьогоднішніх гривнях. Нуль у всіх
	// точках, коли темпу немає (OutlookDoc.Why).
	Amount state.Money `json:"amount"`
	// Nominal — те саме гривнею СВОГО місяця: скільки буде на рахунку. У
	// доларовому документі обнуляється: майбутня гривня, поділена на
	// сьогоднішній курс, — число, якого не існує.
	Nominal state.Money `json:"nominal" money:"uah-only"`
	// Contributed — старт плюс внески темпом, без жодного відсотка.
	Contributed state.Money `json:"contributed"`
	// Gain — що наросло понад внесене. Різниця вже перекладених сусідів,
	// щоб у доларовому вигляді три числа сходились на екрані.
	Gain state.Money `json:"gain" money:"diff=amount,contributed"`
	// Low / High — той самий темп за гіршого й кращого ринку (розкид як у
	// віялі прогнозу до дедлайну).
	Low  state.Money `json:"low"`
	High state.Money `json:"high"`
	// Plan — за планом: планові надходження плюс внесок під мету. Колонка
	// «За планом» колишньої таблиці проєкцій.
	Plan state.Money `json:"plan"`
}

// PaceBar — нетто зовнішніх грошей за один календарний місяць.
type PaceBar struct {
	Month  string      `json:"month"` // РРРР-ММ
	Amount state.Money `json:"amount"`
	// InWindow — місяць зачіпає вікно, з якого виміряно темп.
	InWindow bool `json:"in_window"`
}

// OutlookPace — темп і те, з чого він складений.
type OutlookPace struct {
	Monthly    state.Money `json:"monthly"`
	Months     int         `json:"months"`
	WindowDays int         `json:"window_days"`
	Bars       []PaceBar   `json:"bars"`
}

// OutlookDoc — відповідь GET /api/outlook.
type OutlookDoc struct {
	Pace OutlookPace `json:"pace"`
	// Start — із чого стартує модель (projectionStart): капітал без
	// резерву й цілей накопичення.
	Start  state.Money    `json:"start"`
	Points []OutlookPoint `json:"points"`
	// Ставка гривневого рукава сьогодні й куди вона сповзе; знецінення.
	// Для підпису під графіком: числа в точках уже містять їх.
	RatePct         float64 `json:"rate_pct"`
	RateTerminalPct float64 `json:"rate_terminal_pct"`
	GlideYears      float64 `json:"glide_years"`
	DevaluationPct  float64 `json:"devaluation_pct"`
	// Why непорожнє ⇒ темпу немає, і в точках є лише Plan.
	Why string `json:"why,omitempty"`
}

// BuildOutlook — прогноз на сьогоднішньому портфелі.
func (e *Engine) BuildOutlook(ctx context.Context, now time.Time) (*OutlookDoc, error) {
	tap, err := e.realProjectionTap(ctx, now)
	if err != nil {
		return nil, err
	}
	return buildOutlook(tap, OutlookMonths), nil
}

// buildOutlook — чиста частина: вхід проєкції → точки й стовпчики.
func buildOutlook(tap projectionTap, months int) *OutlookDoc {
	in := tap.in
	factory := newSleeveFactory(in)
	rateSpreadPP, devalSpreadPP := projectionSpreads(in.Settings)
	p0 := projectionStart(in.Capital)

	out := &OutlookDoc{
		Pace: OutlookPace{
			Monthly:    state.Major(in.ActualMonthly, money.UAH),
			WindowDays: actualWindowDays,
			Bars:       paceBars(tap.moves, in.Rates, in.Today),
		},
		Start:          state.Major(p0, money.UAH),
		GlideYears:     factory.glideYears,
		DevaluationPct: domain.Round2(in.Deval),
	}

	planSleeves := factory.build(tap.contribM, 0)
	// Ставка — гривневого рукава, як і в рядках прогнозу до дедлайну: під
	// неї росте основна валюта портфеля. Від внеску вона не залежить, тож
	// береться з рукавів плану, які є завжди.
	for _, s := range planSleeves {
		if s.Currency == money.UAH {
			out.RatePct = domain.Round2(s.RatePct)
			out.RateTerminalPct = domain.Round2(s.RateTerminalPct)
		}
	}
	plan := domain.ProjectSleevesSeries(planSleeves, in.Deval, months, 1)
	var actual, low, high []domain.SeriesPoint
	if in.ActualMonthly > 0 {
		actual = domain.ProjectSleevesSeries(factory.buildPlanFree(in.ActualMonthly, 0),
			in.Deval, months, 1)
		low = domain.ProjectSleevesSeries(factory.buildPlanFree(in.ActualMonthly, -rateSpreadPP),
			in.Deval+devalSpreadPP, months, 1)
		high = domain.ProjectSleevesSeries(factory.buildPlanFree(in.ActualMonthly, rateSpreadPP),
			math.Max(0, in.Deval-devalSpreadPP), months, 1)
	} else {
		out.Why = "Темпу ще немає: за останні пів року нових грошей у портфель не додалось."
	}

	// Довжина — з серії плану: вона є завжди, а на порожньому портфелі
	// (жодного рукава) складається з нулів, і графік чесно лежить на осі.
	uah := func(s []domain.SeriesPoint, i int) state.Money {
		if i >= len(s) {
			return state.Major(0, money.UAH)
		}
		return state.Major(domain.Round2(s[i].UAH), money.UAH)
	}
	for i, p := range plan {
		pt := OutlookPoint{
			Month: p.Month, Date: in.Today.AddMonthsClamp(p.Month),
			Plan:   uah(plan, i),
			Amount: uah(actual, i), Low: uah(low, i), High: uah(high, i),
		}
		if i < len(actual) {
			pt.Nominal = state.Major(domain.Round2(actual[i].NominalUAH), money.UAH)
			pt.Contributed = state.Major(domain.Round2(
				domain.RealContributed(p0, in.ActualMonthly, in.Deval, p.Month)), money.UAH)
			pt.Gain = state.Major(pt.Amount.Major()-pt.Contributed.Major(), money.UAH)
		}
		out.Points = append(out.Points, pt)
	}
	// Скільки місяців історії стоїть за числом темпу — те саме, що
	// actual_months документа стану; тут рахується з тих самих рухів.
	out.Pace.Months = paceSpanMonths(tap.moves, in.Today)
	return out
}

// paceSpanMonths — на скільки місяців поділено суму у вікні темпу.
// Близнюк блоку в buildMonth (state_month.go): той самий відбір і той
// самий paceMonths, лише без суми.
func paceSpanMonths(moves []moneyMove, today domain.Date) int {
	first, seen := today, false
	for _, m := range moves {
		if n := domain.DaysBetween(m.Date, today); n < 0 || n > actualWindowDays {
			continue
		}
		seen = true
		if m.Date.Before(first) {
			first = m.Date
		}
	}
	if !seen {
		return 0
	}
	return int(paceMonths(first, today) + 0.5)
}

// paceBars — нетто зовнішніх грошей по календарних місяцях, від
// найдавнішого до поточного, за СЬОГОДНІШНІМ курсом (як і сам темп).
//
// Місяці календарні, а вікно темпу — ковзні 183 дні, тож середнє стовпчиків
// у вікні НЕ дорівнює темпу, і так має бути: крайній місяць вікна входить
// у стовпчик цілим, а в темп — лише днями після межі. Лінію середнього
// малюють із Pace.Monthly, а не з цих стовпчиків.
func paceBars(moves []moneyMove, rates fx.Rates, today domain.Date) []PaceBar {
	t := today.Time()
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	windowFrom := today.AddDays(-actualWindowDays)

	sums := map[string]int64{}
	for _, m := range moves {
		if !m.Date.Valid() || m.Date.After(today) {
			continue
		}
		if u, err := fx.ToUAH(money.New(m.Amount, m.Currency), rates); err == nil {
			sums[string(m.Date)[:7]] += u.Amount()
		}
	}
	out := make([]PaceBar, 0, paceBarMonths)
	for i := paceBarMonths - 1; i >= 0; i-- {
		mo := first.AddDate(0, -i, 0)
		key := mo.Format("2006-01")
		// Місяць у вікні, якщо його останній день не раніший за межу вікна.
		lastDay := domain.NewDate(mo.AddDate(0, 1, -1))
		out = append(out, PaceBar{
			Month:    key,
			Amount:   state.Major(float64(sums[key])/100, money.UAH),
			InWindow: !lastDay.Before(windowFrom),
		})
	}
	return out
}
