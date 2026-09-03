package api

import (
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/state"
)

// --- борг у проході вперед (route.go, «Борг») ---

// routeDebtDoc — документ із боргом під ставкою 3 000 ₴ і місячною стелею
// дострокового 1 000 ₴ (100 % від дозволеної частини плану в 1 000 ₴).
func routeDebtDoc() (*state.Doc, map[string]*state.MonthPlan) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	doc.Settings = routeSettings(10000, 6, 40)
	doc.Settings.DebtFillSharePct = fptr(100)
	doc.Debt = &state.DebtPlan{
		TotalUAH: 3000, FillNowUAH: 1000, FillMonthUAH: 1000,
		TopName: "Розстрочка", TopRatePct: 40,
	}
	plans := routePlans(30000)
	for _, mp := range plans {
		mp.PlanDebtUAH = 1000
	}
	return doc, plans
}

// routeDebtAhead — тіло за графіком по 1 000 ₴ у названих місяцях (зсув
// від сьогодні), без обовʼязкового й без карткових розстрочок.
func routeDebtAhead(principal float64, months ...int) map[string]routeDebtMonth {
	out := map[string]routeDebtMonth{}
	for _, m := range months {
		out[monthKeyAt(routeToday, m)] = routeDebtMonth{PrincipalUAH: principal}
	}
	return out
}

func routeDebtFlows() incomeAhead {
	return routeInc("mono", money.UAH,
		routeFlow("2026-09-10", 5000, "UA0001"),
		routeFlow("2026-10-10", 5000, "UA0001"),
		routeFlow("2026-11-10", 5000, "UA0001"),
		routeFlow("2026-12-10", 5000, "UA0001"))
}

func debtCuts(legs []routeLeg) []float64 {
	out := make([]float64, len(legs))
	for i, l := range legs {
		if l.Debt != nil {
			out[i] = l.Debt.AmountUAH
		}
	}
	return out
}

// Борг тане за графіком: розстрочка на три місяці закривається сама, і
// вирізок «Борг» після цього немає — хай би скільки лишалось стелі.
//
// Без графіка (debt == nil) прохід тане лише від дострокових платежів:
// 3 000 ₴ по 1 000 ₴ на місяць — три ноги з вирізкою. Це стара поведінка, і
// вона лишається чинною там, де графіка немає; контраст і є суттю тесту.
func TestRouteDebtLeftFollowsSchedule(t *testing.T) {
	sug := []suggestion{bondSug("UA0001", 1000, money.UAH)}

	doc, plans := routeDebtDoc()
	old := buildRoute(doc, sug, routeDebtFlows(), plans, nil, allocRates, nil, nil, routeToday)
	if got := debtCuts(old.Legs); len(got) != 4 || got[0] != 1000 || got[1] != 1000 || got[2] != 1000 || got[3] != 0 {
		t.Fatalf("без графіка вирізки боргу %v, чекали [1000 1000 1000 0]", got)
	}

	// Тіло йде за графіком у вересні, жовтні й листопаді (зсуви 1..3 від
	// 27 серпня): у вересень прохід входить із 2 000, ріже 1 000 → 1 000; у
	// жовтень входить із 0 — вирізки немає.
	doc, plans = routeDebtDoc()
	got := buildRoute(doc, sug, routeDebtFlows(), plans, routeDebtAhead(1000, 1, 2, 3),
		allocRates, nil, nil, routeToday)
	if cuts := debtCuts(got.Legs); len(cuts) != 4 || cuts[0] != 1000 || cuts[1] != 0 || cuts[2] != 0 || cuts[3] != 0 {
		t.Fatalf("з графіком вирізки боргу %v, чекали [1000 0 0 0]", cuts)
	}
	// Таблиця місяців: борг на кінець вересня 1 000, далі 0; дострокове у
	// вересні — 1 000.
	if len(got.Months) != routeHorizonMonths+1 {
		t.Fatalf("рядків months %d, чекали %d", len(got.Months), routeHorizonMonths+1)
	}
	if r := got.Months[1]; r.DebtLeftUAH != 1000 || r.PrepayUAH != 1000 {
		t.Errorf("вересень: лишається %.2f (чекали 1000), достроково %.2f (чекали 1000)",
			r.DebtLeftUAH, r.PrepayUAH)
	}
	if r := got.Months[2]; r.DebtLeftUAH != 0 || r.PrepayUAH != 0 {
		t.Errorf("жовтень: лишається %.2f, достроково %.2f — чекали нулі", r.DebtLeftUAH, r.PrepayUAH)
	}
	if got.Months[0].DebtLeftUAH != 3000 {
		t.Errorf("поточний місяць береться з документа: %.2f, чекали 3000", got.Months[0].DebtLeftUAH)
	}
}

// Місяць без надходжень борг усе одно списує: графік не чекає на купон.
// Ноги лише у вересні й грудні; тіло за графіком у жовтні й листопаді —
// у грудень прохід входить із 3 000 − 1 000 (вересень) − 2 000 = 0.
func TestRouteDebtMeltsInMonthsWithoutLegs(t *testing.T) {
	sug := []suggestion{bondSug("UA0001", 1000, money.UAH)}
	doc, plans := routeDebtDoc()
	got := buildRoute(doc, sug, routeInc("mono", money.UAH,
		routeFlow("2026-09-10", 5000, "UA0001"),
		routeFlow("2026-12-10", 5000, "UA0001")),
		plans, routeDebtAhead(1000, 2, 3), allocRates, nil, nil, routeToday)
	if cuts := debtCuts(got.Legs); len(cuts) != 2 || cuts[0] != 1000 || cuts[1] != 0 {
		t.Fatalf("вирізки %v, чекали [1000 0]", cuts)
	}
	for m := 3; m <= routeHorizonMonths; m++ {
		if got.Months[m].DebtLeftUAH != 0 {
			t.Errorf("місяць +%d: лишається %.2f, чекали 0", m, got.Months[m].DebtLeftUAH)
		}
	}
}

// Таблиця місяців називає, де платежів стає менше: обовʼязкове 2 500 три
// місяці поспіль і нуль далі → у четвертому місяці drop 2 500. Без боргу в
// документі таблиці немає взагалі.
func TestRouteMonthsNameTheDrop(t *testing.T) {
	sug := []suggestion{bondSug("UA0001", 1000, money.UAH)}
	doc, plans := routeDebtDoc()
	debt := map[string]routeDebtMonth{}
	for m := 0; m <= 3; m++ {
		debt[monthKeyAt(routeToday, m)] = routeDebtMonth{DueUAH: 2000, CardInstUAH: 500}
	}
	got := buildRoute(doc, sug, routeDebtFlows(), plans, debt, allocRates, nil, nil, routeToday)
	for m, r := range got.Months {
		want := 0.0
		if m == 4 {
			want = 2500
		}
		if r.DropUAH != want {
			t.Errorf("місяць +%d: drop %.2f, чекали %.2f", m, r.DropUAH, want)
		}
	}
	if got.Months[1].DebtDueUAH != 2000 || got.Months[1].CardInstUAH != 500 {
		t.Errorf("вересень: обовʼязкове %.2f / карткові %.2f, чекали 2000 / 500",
			got.Months[1].DebtDueUAH, got.Months[1].CardInstUAH)
	}

	plain := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	plain.Settings = routeSettings(10000, 6, 40)
	if out := buildRoute(plain, sug, routeDebtFlows(), plans, debt, allocRates, nil, nil, routeToday); out.Months != nil {
		t.Errorf("без боргу в документі таблиця мусить мовчати, маємо %d рядків", len(out.Months))
	}
}
