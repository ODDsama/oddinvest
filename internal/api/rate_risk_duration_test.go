package api

import (
	"math"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
)

// TestModifiedDurationWeightsOwnRates — зведена модифікована дюрація
// дорівнює PV-зваженому середньому ВЛАСНИХ модифікованих дюрацій, а не
// зведеній Маколея, поділеній на зведену ставку.
//
// Різниця виникає рівно там, де валют більше однієї. Модифікована — це
// Маколея, поділена на (1 + y) ТІЄЇ САМОЇ валюти; ділити доларову ногу на
// гривневу ставку означає ділити на 1.165 замість 1.04, тобто занижувати
// її дюрацію приблизно на десяту частину. І рівно на стільки ж
// занижуються всі чотири сценарії ±1/±2 в.п. у грошах — тобто число, за
// яким людина міряє свій процентний ризик.
//
// Golden цього не ловив: у багатій фікстурі валютна нога мала, і
// розбіжність ховалась у сотих.
func TestModifiedDurationWeightsOwnRates(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	d := func(off int) domain.Date { return domain.NewDate(now.AddDate(0, 0, off)) }

	// Дві ноги однакової ваги в гривні, але з дуже різними ставками:
	// гривнева під 16.5%, доларова під 4%. Курс 40 робить $2 500 рівними
	// 100 000 ₴, тож PV-ваги майже однакові й помилку видно в повний зріст.
	rates := fx.Rates{money.USD: 40_0000}
	in := riskInput{
		Rates:      rates,
		YieldPct:   16.0,
		YieldByCur: map[string]float64{money.UAH: 16.5, money.USD: 4.0},
		Cashflow: []domain.CashflowItem{
			{ISIN: "UA1", Date: d(1095), Amount: money.New(100_000_00, money.UAH)},
			{ISIN: "US1", Date: d(1095), Amount: money.New(2_500_00, money.USD)},
		},
		Now: now, Today: today,
	}
	out := buildRisk(in)
	if out.RateRisk == nil {
		t.Fatal("картки процентного ризику немає — тест нічого не перевірив")
	}
	rr := out.RateRisk
	if len(rr.ByCurrency) != 2 {
		t.Fatalf("подюрацій %d, чекали дві: %+v", len(rr.ByCurrency), rr.ByCurrency)
	}

	// Обидві ноги — один потік через три роки, тож Маколея в кожної рівно
	// строк, а модифікована = строк / (1 + власна ставка). Числа тут
	// зашиті навмисно: вони мають зійтися з арифметикою на папері, а не з
	// тим, що поверне код.
	yrs := float64(domain.DaysBetween(today, d(1095))) / 365.0
	wantUAH := yrs / 1.165
	wantUSD := yrs / 1.04
	if math.Abs(rr.ByCurrency[money.UAH]-wantUAH) > 0.01 {
		t.Errorf("гривнева модифікована %.4f, чекали %.4f", rr.ByCurrency[money.UAH], wantUAH)
	}
	if math.Abs(rr.ByCurrency[money.USD]-wantUSD) > 0.01 {
		t.Errorf("доларова модифікована %.4f, чекали %.4f", rr.ByCurrency[money.USD], wantUSD)
	}

	// Зведена мусить лежати МІЖ ними — це і є ознака зважування власними
	// ставками. Стара формула (mac / (1 + зведена)) дала б число нижче за
	// обидві, бо ділила б і доларову ногу на гривневу ставку.
	lo, hi := math.Min(wantUAH, wantUSD), math.Max(wantUAH, wantUSD)
	if rr.ModifiedDur < lo-0.01 || rr.ModifiedDur > hi+0.01 {
		t.Errorf("зведена модифікована %.4f поза межами [%.4f, %.4f] — "+
			"найпевніше вона знову ділиться на зведену ставку", rr.ModifiedDur, lo, hi)
	}
	// І строго вища за те, що дала б стара формула: доказ, що правка кусає.
	old := rr.DurationYears / (1 + in.YieldPct/100)
	if rr.ModifiedDur <= old {
		t.Errorf("зведена %.4f не вища за стару формулу %.4f — правка нічого не змінила",
			rr.ModifiedDur, old)
	}
}
