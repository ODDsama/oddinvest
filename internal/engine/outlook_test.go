package engine

import (
	"math"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
)

// outlookFixture — вхід із темпом і метою, та сама пара «проєкція +
// прогноз», яку будівник стану віддає через projectionTap.
func outlookFixture(t *testing.T, actual float64) (projectionPhase, *OutlookDoc) {
	t.Helper()
	in := forecastInput(t, goalSettings("", "2030-07-15"))
	in.ActualMonthly = actual
	prj := buildProjection(in)
	return prj, buildOutlook(projectionTap{in: in, contribM: prj.ContribM}, OutlookMonths, OutlookPortfolio)
}

// mixedInput — портфель із усіх кошиків одразу: ОВДП, вклад, фонд і резерв.
// Потрібен розрізам: на портфелі з одного фонду кожна смуга, крім однієї,
// була б нульовою, і тест сходився б сам із собою.
func mixedInput(t *testing.T) projectionInput {
	t.Helper()
	in := forecastInput(t, goalSettings("", "2030-07-15"))
	in.ActualMonthly = 5_000
	in.NominalByCur = map[string]int64{money.UAH: 5_000_000} // 50 000 ₴ номіналу
	in.DepositBodyByCur = map[string]float64{money.UAH: 25_000}
	in.Capital.BondsUAH = state.Major(50_000, money.UAH)
	in.Capital.DepositsUAH = state.Major(25_000, money.UAH)
	in.Capital.ReserveUAH = state.Major(40_000, money.UAH)
	in.Capital.ReserveByCur = map[string]state.Money{money.UAH: state.Major(40_000, money.UAH)}
	return in
}

func bandOf(bands []OutlookBand, key string) *OutlookBand {
	for i := range bands {
		if bands[i].Key == key {
			return &bands[i]
		}
	}
	return nil
}

// СМУГИ СХОДЯТЬСЯ ДО ЛІНІЇ. Графік малює їх складеними одна на одну під
// загальною лінією, а плитки показують числа смуг поруч із головним — тож
// у кожному з трьох розрізів і в обох обсягах сума смуг мусить бути самою
// лінією в кожній точці.
func TestOutlookBandsSumToLine(t *testing.T) {
	in := mixedInput(t)
	bonds, funds := 60.0, 20.0
	in.Settings.TargetBondsPct, in.Settings.TargetFundsPct = &bonds, &funds
	for _, scope := range OutlookScopes {
		doc := buildOutlook(projectionTap{in: in}, OutlookMonths, scope)
		for name, bands := range map[string][]OutlookBand{
			"валюти": doc.ByCurrency, "походження": doc.ByOrigin, "види": doc.ByKind,
		} {
			// У фікстурі одна валюта, тож у розрізі за валютами смуга одна
			// (в «усіх грошах» — ще резерв); решта мусить мати кілька.
			if len(bands) == 0 || (name != "валюти" && len(bands) < 2) {
				t.Fatalf("%s/%s: смуг %d — тест нічого не перевірив", scope, name, len(bands))
			}
			for i, p := range doc.Points {
				sum := 0.0
				for _, b := range bands {
					sum += b.Values[i].Major()
				}
				// Кожна смуга округлена до копійки окремо.
				if d := math.Abs(sum - p.Amount.Major()); d > 0.01*float64(len(bands)+1) {
					t.Fatalf("%s/%s, міс %d: смуги в сумі %.2f, а лінія %.2f",
						scope, name, p.Month, sum, p.Amount.Major())
				}
			}
		}
	}
}

// «Усі гроші» додає резерв і цілі — і нічого більше. Різниця між двома
// обсягами в кожній точці мусить бути рівно смугою outside, а старт —
// усім капіталом, тим самим числом, що на плитці «Капітал».
func TestOutlookScopeAllAddsOnlyOutside(t *testing.T) {
	in := mixedInput(t)
	port := buildOutlook(projectionTap{in: in}, OutlookMonths, OutlookPortfolio)
	all := buildOutlook(projectionTap{in: in}, OutlookMonths, OutlookAll)

	if bandOf(port.ByOrigin, bandOutside) != nil {
		t.Error("у портфельному обсязі зʼявилась смуга резерву")
	}
	out := bandOf(all.ByOrigin, bandOutside)
	if out == nil {
		t.Fatal("в «усіх грошах» немає смуги резерву й цілей")
	}
	if d := math.Abs(all.Start.Major() - in.Capital.TotalUAH()); d > 0.01 {
		t.Errorf("старт «усіх грошей» %.2f, а весь капітал %.2f", all.Start.Major(), in.Capital.TotalUAH())
	}
	if out.Values[0].Major() != 40_000 {
		t.Errorf("резерв на старті %.2f, а внесено 40 000", out.Values[0].Major())
	}
	// Гривневий резерв знецінюється: за десять років він коштує менше.
	if out.Values[120].Major() >= out.Values[0].Major() {
		t.Errorf("гривня під матрацом не знецінилась: %.2f → %.2f",
			out.Values[0].Major(), out.Values[120].Major())
	}
	for i := range all.Points {
		a, p := all.Points[i], port.Points[i]
		if d := math.Abs(a.Amount.Major() - p.Amount.Major() - out.Values[i].Major()); d > 0.02 {
			t.Fatalf("міс %d: різниця обсягів %.2f, а смуга резерву %.2f",
				a.Month, a.Amount.Major()-p.Amount.Major(), out.Values[i].Major())
		}
		// Наросле від резерву не залежить: він не росте.
		if d := math.Abs(a.Gain.Major() - p.Gain.Major()); d > 0.02 {
			t.Fatalf("міс %d: резерв змінив «наросло»: %.2f проти %.2f", a.Month, a.Gain.Major(), p.Gain.Major())
		}
		if d := math.Abs(a.Income.Major() - p.Income.Major()); d > 0.001 {
			t.Fatalf("міс %d: резерв змінив дохід", a.Month)
		}
	}
}

// Нові гроші за видами — за частками стратегії, і лише за ними. Без
// часток вони стоять окремою смугою «new»; із частками 60 / 20 ця смуга
// зникає, а її гроші лягають у ОВДП і фонди як 75 / 25.
func TestOutlookKindSplitFollowsStrategy(t *testing.T) {
	in := mixedInput(t)
	bare := buildOutlook(projectionTap{in: in}, OutlookMonths, OutlookPortfolio)
	fresh := bandOf(bare.ByKind, bandNew)
	if fresh == nil || len(bare.Strategy) != 0 {
		t.Fatalf("без часток нові гроші мусять стояти окремо: %+v", bare.Strategy)
	}

	bonds, funds := 60.0, 20.0
	in.Settings.TargetBondsPct, in.Settings.TargetFundsPct = &bonds, &funds
	doc := buildOutlook(projectionTap{in: in}, OutlookMonths, OutlookPortfolio)
	if bandOf(doc.ByKind, bandNew) != nil {
		t.Error("частки задано, а смуга «new» лишилась")
	}
	if len(doc.Strategy) != 2 || doc.Strategy[0].Pct != 75 || doc.Strategy[1].Pct != 25 {
		t.Fatalf("частки не знормовано до 75/25: %+v", doc.Strategy)
	}
	const m = 120
	if fresh.Values[m].Major() <= 0 {
		t.Fatal("тест нічого не перевірив — нових грошей за десять років немає")
	}
	for key, share := range map[string]float64{kindBonds: 0.75, kindFunds: 0.25, kindDeposits: 0} {
		got := bandOf(doc.ByKind, key).Values[m].Major() - bandOf(bare.ByKind, key).Values[m].Major()
		want := fresh.Values[m].Major() * share
		if math.Abs(got-want) > 0.02 {
			t.Errorf("%s: з нових грошей дісталось %.2f, а за часткою %.0f%% мало бути %.2f",
				key, got, share*100, want)
		}
	}
}

// «На чому засновано» називає ті самі рукави, якими пораховано лінію:
// внески по валютах у сумі дають темп, а суми на старті — старт моделі.
func TestOutlookSleevesDescribeTheLine(t *testing.T) {
	in := mixedInput(t)
	doc := buildOutlook(projectionTap{in: in}, OutlookMonths, OutlookPortfolio)
	if len(doc.Sleeves) == 0 {
		t.Fatal("рукавів немає")
	}
	var contrib, have float64
	for _, s := range doc.Sleeves {
		contrib += s.ContribMonthly.Major()
		have += s.Amount.Major() // лише гривня у фікстурі
		if s.RatePct <= 0 || s.RateSource == "" {
			t.Errorf("%s: ставка без числа чи джерела: %+v", s.Currency, s)
		}
	}
	if math.Abs(contrib-in.ActualMonthly) > 0.01 {
		t.Errorf("внески рукавів %.2f, а темп %.2f", contrib, in.ActualMonthly)
	}
	if math.Abs(have-doc.Points[0].Amount.Major()) > 0.01 {
		t.Errorf("у рукавах на старті %.2f, а лінія починається з %.2f", have, doc.Points[0].Amount.Major())
	}
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
	doc := buildOutlook(projectionTap{in: in, contribM: prj.ContribM}, OutlookMonths, OutlookPortfolio)

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
