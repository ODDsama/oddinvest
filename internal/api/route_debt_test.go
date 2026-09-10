package api

import (
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/state"
)

// --- борг у проході вперед (route.go, «Борг») ---

// routeDebtDoc — документ із боргом під ставкою 3 000 ₴.
//
// СТЕЛІ ДОСТРОКОВОГО ТУТ БІЛЬШЕ НЕМАЄ: маршрут не веде гроші в борг
// (фаза 45). Борг у проході тане ЛИШЕ за графіком обовʼязкових платежів —
// саме це решта тестів файла й перевіряє.
func routeDebtDoc() (*state.Doc, map[string]*state.MonthPlan) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	doc.Settings = routeSettings(10000, 6, 40)
	doc.Debt = &state.DebtPlan{
		TotalUAH: state.Major(3000, money.UAH), TopName: "Розстрочка", TopRatePct: 40,
	}
	return doc, routePlans(30000)
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

// debtCuts тут БІЛЬШЕ НЕМАЄ: вирізок «Борг» на ногах не буває, бо маршрут
// не веде гроші в дострокове погашення (фаза 45). Усе, що лишилось про
// борг у проході, — його танення за ГРАФІКОМ обовʼязкових платежів, і
// перевіряється воно колонкою debt_left_uah таблиці місяців.

// Борг тане за графіком, і ТІЛЬКИ за ним: розстрочка на три місяці
// закривається сама. Без графіка (debt == nil) він не тане взагалі —
// контраст і є суттю тесту.
func TestRouteDebtLeftFollowsSchedule(t *testing.T) {
	sug := []suggestion{bondSug("UA0001", 1000, money.UAH)}

	// БЕЗ ГРАФІКА БОРГ НЕ ТАНЕ ВЗАГАЛІ. Доти він танув від вирізок
	// дострокового на кожній нозі; тепер маршрут у борг не веде, тож
	// єдине, що його зменшує, — графік обовʼязкових платежів.
	doc, plans := routeDebtDoc()
	old := buildRoute(doc, sug, routeDebtFlows(), plans, nil, allocRates, nil, nil, routeToday)
	for i, r := range old.Months {
		if r.DebtLeftUAH != 3000 {
			t.Fatalf("місяць %d: борг %.2f, чекали 3000 — без графіка танути нема від чого",
				i, r.DebtLeftUAH)
		}
	}

	// Тіло йде за графіком у вересні, жовтні й листопаді (зсуви 1..3 від
	// 27 серпня): 3 000 → 2 000 → 1 000 → 0.
	doc, plans = routeDebtDoc()
	got := buildRoute(doc, sug, routeDebtFlows(), plans, routeDebtAhead(1000, 1, 2, 3),
		allocRates, nil, nil, routeToday)
	if len(got.Months) != routeHorizonMonths+1 {
		t.Fatalf("рядків months %d, чекали %d", len(got.Months), routeHorizonMonths+1)
	}
	for m, want := range map[int]float64{0: 3000, 1: 2000, 2: 1000, 3: 0, 4: 0} {
		if r := got.Months[m]; r.DebtLeftUAH != want {
			t.Errorf("місяць %d: лишається %.2f, чекали %.2f", m, r.DebtLeftUAH, want)
		}
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
	// Тіло списується у ЖОВТНІ й ЛИСТОПАДІ, хоч ноги там немає: графік не
	// чекає на купон. До грудня борг уже нульовий.
	if got.Months[1].DebtLeftUAH != 3000 {
		t.Errorf("вересень: %.2f, чекали 3000 — графік починається з жовтня",
			got.Months[1].DebtLeftUAH)
	}
	if got.Months[2].DebtLeftUAH != 2000 {
		t.Errorf("жовтень: %.2f, чекали 2000", got.Months[2].DebtLeftUAH)
	}
	// Графік має рівно два платежі по 1 000 ₴, тож 3 000 − 2 000 = 1 000
	// лишаються під ставкою до кінця горизонту. Доти цей хвіст доїдали
	// вирізки дострокового на ногах — тепер їх немає, і борг чесно стоїть.
	for m := 3; m <= routeHorizonMonths; m++ {
		if got.Months[m].DebtLeftUAH != 1000 {
			t.Errorf("місяць +%d: лишається %.2f, чекали 1000", m, got.Months[m].DebtLeftUAH)
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

// --- планові витрати на горизонті (0056) ---

// Планова витрата з картки стоїть у таблиці «Борг на горизонті» окремою
// колонкою — у своєму місяці, а не розмазана. Ноги маршруту вона не чіпає:
// це не портфельні гроші.
func TestRouteMonthsShowCardPlanned(t *testing.T) {
	sug := []suggestion{bondSug("UA0001", 1000, money.UAH)}
	doc, plans := routeDebtDoc()
	debt := map[string]routeDebtMonth{}
	for m := 0; m <= routeHorizonMonths; m++ {
		row := routeDebtMonth{DueUAH: 2000}
		if m == 2 {
			row.PlannedUAH = 30000 // котел
		}
		debt[monthKeyAt(routeToday, m)] = row
	}
	got := buildRoute(doc, sug, routeDebtFlows(), plans, debt, allocRates, nil, nil, routeToday)
	for m, r := range got.Months {
		want := 0.0
		if m == 2 {
			want = 30000
		}
		if r.PlannedUAH != want {
			t.Errorf("місяць +%d: планові %.2f, чекали %.2f", m, r.PlannedUAH, want)
		}
	}
}

// СТОРОЖ. Місяць ПІСЛЯ разової витрати не має показувати «тут щось
// закрилось»: drop означає, що платити стало менше НАЗАВЖДИ (розстрочка
// доплачена, картка звільнилась), а котел зник просто тому, що він разовий.
// І дзеркально — місяць самого котла не має гасити drop, який справді
// стався поруч.
func TestRouteDropIgnoresPlanned(t *testing.T) {
	sug := []suggestion{bondSug("UA0001", 1000, money.UAH)}
	doc, plans := routeDebtDoc()
	debt := map[string]routeDebtMonth{}
	for m := 0; m <= routeHorizonMonths; m++ {
		row := routeDebtMonth{}
		if m <= 3 {
			row.DueUAH = 2000 // закривається після четвертого місяця
		}
		if m == 1 {
			row.PlannedUAH = 30000
		}
		debt[monthKeyAt(routeToday, m)] = row
	}
	got := buildRoute(doc, sug, routeDebtFlows(), plans, debt, allocRates, nil, nil, routeToday)
	for m, r := range got.Months {
		want := 0.0
		if m == 4 {
			want = 2000 // саме тут обовʼязкове справді скінчилось
		}
		if r.DropUAH != want {
			t.Errorf("місяць +%d: drop %.2f, чекали %.2f — разова витрата "+
				"нічого не закриває, тож у drop не входить", m, r.DropUAH, want)
		}
	}
}

// Портфельна планова витрата худне ноги сама, без окремої ноги: вони
// діляться з PlanUAH, а той уже за вирахуванням витрати. Ногу «мінус
// котел» заводити не можна — нога маршруту це горщик грошей, що ПРИЙДУТЬ.
func TestRouteLegsShrinkOnPlanPlanned(t *testing.T) {
	sug := []suggestion{bondSug("UA0001", 1000, money.UAH)}
	doc, plans := routeDebtDoc()
	base := buildRoute(doc, sug, routeDebtFlows(), plans, nil, allocRates, nil, nil, routeToday)

	doc2, plans2 := routeDebtDoc()
	// Те саме, що зробив би buildMonthPlan із витратою 10 000 у вересні.
	sep := monthKeyAt(routeToday, 1)
	plans2[sep].PlannedUAH = state.Major(10000, money.UAH)
	plans2[sep].PlanUAH = plans2[sep].PlanUAH.Sub(state.Major(10000, money.UAH))
	with := buildRoute(doc2, sug, routeDebtFlows(), plans2, nil, allocRates, nil, nil, routeToday)

	if len(base.Months) == 0 || len(with.Months) == 0 {
		t.Skip("таблиці місяців без боргу немає — перевіряємо самі ноги")
	}
	if with.Months[1].PlanUAH >= base.Months[1].PlanUAH {
		t.Errorf("вересень: план %.2f не менший за %.2f — витрата не дійшла до маршруту",
			with.Months[1].PlanUAH, base.Months[1].PlanUAH)
	}
	if diff := base.Months[1].PlanUAH - with.Months[1].PlanUAH; diff != 10000 {
		t.Errorf("план схуд на %.2f, чекали рівно 10000", diff)
	}
}
