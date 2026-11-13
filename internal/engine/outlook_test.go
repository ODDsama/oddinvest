package engine

import (
	"math"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
)

// outlookFixture — вхід із темпом і метою, та сама пара «проєкція +
// прогноз», яку будівник стану віддає через projectionTap.
func outlookFixture(t *testing.T, actual float64) (projectionPhase, *OutlookDoc) {
	t.Helper()
	in := forecastInput(t, goalSettings("", "2030-07-15"))
	in.ActualMonthly = actual
	prj := buildProjection(in)
	return prj, buildOutlook(projectionTap{in: in, contribM: prj.ContribM}, OutlookMonths)
}

// ГОЛОВНИЙ ТЕСТ. Прогноз із повзунком не має власної арифметики: на тих
// роках, які вміла стара таблиця, він мусить назвати ТІ САМІ числа.
//
// Таблицю «Проєкції капіталу» з екрана прибрано, але поле projection у
// документі лишилось (його читає картка доходу), тож два числа про одне й
// те саме досі можуть опинитись поруч — і розійтись їм не можна.
func TestOutlookMatchesProjectionRows(t *testing.T) {
	prj, doc := outlookFixture(t, 5_000)
	if len(doc.Points) != OutlookMonths+1 {
		t.Fatalf("точок %d, а має бути по одній на місяць і нульова: %d",
			len(doc.Points), OutlookMonths+1)
	}
	if len(prj.Rows) == 0 {
		t.Fatal("тест нічого не перевірив — рядків проєкції немає")
	}
	for _, row := range prj.Rows {
		pt := doc.Points[row.Years*12]
		if pt.Month != row.Years*12 {
			t.Fatalf("точка %d стоїть не на своєму місяці: %d", row.Years*12, pt.Month)
		}
		if row.WithReinvestActual.Major() == 0 {
			t.Fatalf("%d р.: тест нічого не перевірив — «за фактом» нульове", row.Years)
		}
		if d := math.Abs(pt.Amount.Major() - row.WithReinvestActual.Major()); d > 0.01 {
			t.Errorf("%d р. за фактом: прогноз %.2f, таблиця %.2f", row.Years,
				pt.Amount.Major(), row.WithReinvestActual.Major())
		}
		if d := math.Abs(pt.Plan.Major() - row.WithReinvest.Major()); d > 0.01 {
			t.Errorf("%d р. за планом: прогноз %.2f, таблиця %.2f", row.Years,
				pt.Plan.Major(), row.WithReinvest.Major())
		}
	}
}

// Дохід на графіку — ті самі числа, що картка «Пасивний дохід» знала на
// чотирьох роках, і той самий місяць незалежності, який вона називає
// датою. Крива, що перетинає лінію «місяць життя» не там, де написано
// «квітень 2080», була б двома відповідями на одне питання.
func TestOutlookIncomeMatchesProjectionAndIndependence(t *testing.T) {
	set := goalSettings("", "2030-07-15")
	target := 3_000.0
	set.MonthlyExpensesUAH = &target
	in := forecastInput(t, set)
	in.ActualMonthly = 5_000
	prj := buildProjection(in)
	doc := buildOutlook(projectionTap{in: in, contribM: prj.ContribM}, OutlookMonths)

	for _, row := range prj.Rows {
		pt := doc.Points[row.Years*12]
		if row.IncomeMonthlyActual.Major() <= 0 {
			t.Fatalf("%d р.: тест нічого не перевірив — дохід за фактом нульовий", row.Years)
		}
		if d := math.Abs(pt.Income.Major() - row.IncomeMonthlyActual.Major()); d > 0.01 {
			t.Errorf("%d р. дохід за фактом: прогноз %.2f, таблиця %.2f", row.Years,
				pt.Income.Major(), row.IncomeMonthlyActual.Major())
		}
		if d := math.Abs(pt.IncomePlan.Major() - row.IncomeMonthly.Major()); d > 0.01 {
			t.Errorf("%d р. дохід за планом: прогноз %.2f, таблиця %.2f", row.Years,
				pt.IncomePlan.Major(), row.IncomeMonthly.Major())
		}
	}

	ind := prj.Independence
	if ind == nil || ind.ActualMonths <= 0 || ind.ActualMonths > OutlookMonths {
		t.Fatalf("тест нічого не перевірив — незалежність за фактом поза горизонтом: %+v", ind)
	}
	first := 0
	for _, p := range doc.Points {
		if p.Income.Major() >= target {
			first = p.Month
			break
		}
	}
	if first != ind.ActualMonths {
		t.Errorf("крива доходу сягає цілі на місяці %d, а картка називає %d", first, ind.ActualMonths)
	}
}

// Те, що екран показує поруч, мусить сходитись між собою в КОЖНІЙ точці:
// браузер нічого не рахує й не може підправити.
func TestOutlookPointInvariants(t *testing.T) {
	_, doc := outlookFixture(t, 5_000)
	if doc.Why != "" {
		t.Fatalf("темп є, а прогноз відмовився: %q", doc.Why)
	}
	if d := math.Abs(doc.Points[0].Amount.Major() - doc.Start.Major()); d > 0.01 {
		t.Errorf("нульова точка %.2f не дорівнює старту %.2f",
			doc.Points[0].Amount.Major(), doc.Start.Major())
	}
	if got := doc.Points[12].Date; got != domain.Date("2027-07-15") {
		t.Errorf("дата дванадцятого місяця %s, а сьогодні 2026-07-15", got)
	}
	for _, p := range doc.Points {
		a := p.Amount.Major()
		if p.Low.Major() > a+0.01 || p.High.Major() < a-0.01 {
			t.Fatalf("міс %d: коридор %.2f…%.2f не охоплює суму %.2f",
				p.Month, p.Low.Major(), p.High.Major(), a)
		}
		// Гривня знецінюється, тож на рахунку намальовано не менше, ніж це
		// коштує сьогоднішніми.
		if p.Nominal.Major() < a-0.01 {
			t.Fatalf("міс %d: номінал %.2f менший за сьогоднішні %.2f", p.Month, p.Nominal.Major(), a)
		}
		if d := math.Abs(p.Gain.Major() - (a - p.Contributed.Major())); d > 0.01 {
			t.Fatalf("міс %d: наросло %.2f ≠ сума %.2f − внесено %.2f",
				p.Month, p.Gain.Major(), a, p.Contributed.Major())
		}
	}
	last := doc.Points[OutlookMonths]
	if last.Gain.Major() <= 0 {
		t.Errorf("за тридцять років під 16%% нічого не наросло: %.2f", last.Gain.Major())
	}
}

// Без темпу прогноз «за фактом» не вигадується: причина названа, а лінія
// плану лишається — вона від темпу не залежить.
func TestOutlookWithoutPace(t *testing.T) {
	_, doc := outlookFixture(t, 0)
	if doc.Why == "" {
		t.Error("темпу немає, а причини не названо")
	}
	p := doc.Points[120]
	if p.Amount.Major() != 0 || p.Gain.Major() != 0 || p.Nominal.Major() != 0 {
		t.Errorf("темпу немає, а сума за фактом є: %+v", p)
	}
	if p.Plan.Major() <= 0 {
		t.Errorf("лінія плану зникла разом із темпом: %.2f", p.Plan.Major())
	}
}

// Стовпчики темпу: календарні місяці, нетто, і лише минуле.
func TestPaceBars(t *testing.T) {
	today := domain.Date("2026-07-15")
	uah := func(d string, v int64) moneyMove {
		return moneyMove{Date: domain.Date(d), Amount: v, Currency: money.UAH}
	}
	bars := paceBars([]moneyMove{
		uah("2026-07-01", 100_000), // +1000 цього місяця
		uah("2026-07-10", -30_000), // −300 того ж місяця: нетто
		uah("2026-05-20", 200_000), // +2000 у травні
		uah("2025-07-31", 900_000), // давніше за дванадцять місяців
		uah("2026-08-01", 500_000), // майбутнє — ще не сталось
		{Date: "", Amount: 1, Currency: money.UAH},
	}, fx.Rates{}, today)

	if len(bars) != paceBarMonths {
		t.Fatalf("стовпчиків %d, а місяців %d", len(bars), paceBarMonths)
	}
	if bars[0].Month != "2025-08" || bars[len(bars)-1].Month != "2026-07" {
		t.Fatalf("межі не ті: %s … %s", bars[0].Month, bars[len(bars)-1].Month)
	}
	want := map[string]float64{"2026-07": 700, "2026-05": 2000}
	var total float64
	for _, b := range bars {
		total += b.Amount.Major()
		if b.Amount.Major() != want[b.Month] {
			t.Errorf("%s: %.2f, а чекали %.2f", b.Month, b.Amount.Major(), want[b.Month])
		}
		// Вікно темпу — 183 дні, тобто від 13 січня: січень зачеплений,
		// грудень — ні.
		if in := b.Month >= "2026-01"; b.InWindow != in {
			t.Errorf("%s: у вікні=%v, а має бути %v", b.Month, b.InWindow, in)
		}
	}
	if total != 2700 {
		t.Errorf("разом %.2f: у стовпчики потрапило зайве або щось загубилось", total)
	}
}
