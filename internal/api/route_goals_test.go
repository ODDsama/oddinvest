package api

import (
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/state"
)

// goalsDocFor — документ із однією ціллю й стелею наповнення.
//
// Подушки тут НЕМАЄ навмисно: тести нижче про цілі, і жива подушка
// забирала б своє першою, ховаючи саме те, що перевіряється.
func goalsDocFor(gap, required, moved float64, sharePct float64) *state.Doc {
	d := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	d.Settings = &state.SettingsDoc{GoalsFillSharePct: fptr(sharePct)}
	d.Goals = []state.Goal{{
		ID: 1, Name: "Авто", Currency: money.UAH,
		GapUAH: gap, RequiredUAH: required, MovedUAH: moved,
		DueDate: "2028-01-01", FillMonthUAH: required, FillNowUAH: required - moved,
	}}
	return d
}

// Стеля цілей поновлюється ЩОМІСЯЦЯ, а не роздається за перший купон.
//
// Дзеркало TestRouteReserveCeilingResetsEachMonth, і не для симетрії:
// FillNowUAH — це частка ОДНОГО МІСЯЦЯ, і прохід уперед мусить її
// перераховувати. Саме на цьому місці подушка колись віддала річну норму
// першій же події — пастка, що коштувала червоного тесту.
func TestRouteGoalCeilingResetsEachMonth(t *testing.T) {
	doc := goalsDocFor(100_000, 12_000, 0, 40)

	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		routeInc("mono", money.UAH,
			routeFlow("2026-09-10", 20000, "UA0001"),
			routeFlow("2026-10-10", 20000, "UA0001"),
			routeFlow("2026-11-10", 20000, "UA0001")),
		routePlans(30000), allocRates, nil, routeToday)

	if len(got.Legs) != 3 {
		t.Fatalf("ніг %d, чекали 3", len(got.Legs))
	}
	total := 0.0
	for i, leg := range got.Legs {
		if len(leg.Goals) == 0 {
			t.Fatalf("нога %d без вирізки цілі — стеля не поновилась", i)
		}
		// 30 000 плану × 40% = 12 000 стелі; потрібний темп теж 12 000.
		if leg.GoalsUAH != 12_000 {
			t.Errorf("нога %d: у ціль %.2f, чекали 12000", i, leg.GoalsUAH)
		}
		total += leg.GoalsUAH
	}
	if total != 36_000 {
		t.Errorf("усього в цілі %.2f, чекали 36000 — три місяці по стелі", total)
	}
}

// Дві виплати ОДНОГО місяця ділять одну стелю, а не беруть по стелі кожна.
func TestRouteGoalCeilingSharedWithinMonth(t *testing.T) {
	doc := goalsDocFor(100_000, 12_000, 0, 40)

	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		routeInc("mono", money.UAH,
			routeFlow("2026-09-10", 8000, "UA0001"),
			routeFlow("2026-09-20", 8000, "UA0001")),
		routePlans(30000), allocRates, nil, routeToday)

	total := 0.0
	for _, leg := range got.Legs {
		total += leg.GoalsUAH
	}
	if total != 12_000 {
		t.Errorf("за місяць у цілі %.2f, чекали 12000 — стеля одна на місяць", total)
	}
}

// Розрив закривається — і ціль замовкає, хай би скільки лишалось стелі.
//
// Без цього прохід уперед складав би в ціль гроші й після того, як вона
// зібрана, — тобто малював би маршрут, у якому авто купують двічі.
func TestRouteGoalStopsAtGap(t *testing.T) {
	doc := goalsDocFor(15_000, 12_000, 0, 40)

	got := buildRoute(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		routeInc("mono", money.UAH,
			routeFlow("2026-09-10", 20000, "UA0001"),
			routeFlow("2026-10-10", 20000, "UA0001"),
			routeFlow("2026-11-10", 20000, "UA0001")),
		routePlans(30000), allocRates, nil, routeToday)

	total := 0.0
	for _, leg := range got.Legs {
		total += leg.GoalsUAH
	}
	if total != 15_000 {
		t.Errorf("у ціль пішло %.2f при розриві 15 000 — прохід не побачив, що вона зібрана",
			total)
	}
}
