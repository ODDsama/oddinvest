package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	money "github.com/Rhymond/go-money"
)

// allocParts — куди розійшлась сума розкладки, у копійках: подушка, цілі,
// рядки покупок, поза частками і залишок. Разом вони мусять дати рівно
// суму надходження.
func allocParts(p allocPlan) (reserve, goals, lines, free, rest int64) {
	if p.Reserve != nil {
		reserve = p.Reserve.AmountUAH.Minor()
	}
	for _, g := range p.Goals {
		goals += g.AmountUAH.Minor()
	}
	for _, l := range p.Lines {
		lines += l.TotalUAH.Minor()
	}
	return reserve, goals, lines, p.FreeUAH.Minor(), p.RestUAH.Minor()
}

// Розкладка не губить і не вигадує жодної копійки: подушка + цілі + рядки
// + поза частками + залишок == сума надходження.
//
// Доти цього не перевіряв жоден тест, і діра була саме там, де її не
// шукали: цільові частки видів, що в сумі менші за сотню. spreadMonth
// свідомо не віддає нерозподілені відсотки названим видам («ці гроші
// користувач лишив собі»), але розкладка не клала їх і в залишок — вони
// просто зникали з відповіді, і 10 000 ₴ розкладались на 6 000 без жодного
// слова про решту.
func TestAllocateConservesEveryKopeck(t *testing.T) {
	gap := func(fillNow, gapUAH float64) *state.Reserve {
		return &state.Reserve{FillNowUAH: state.Major(fillNow, money.UAH),
			FillMonthUAH: state.Major(fillNow, money.UAH), GapUAH: state.Major(gapUAH, money.UAH)}
	}
	cases := []struct {
		name   string
		kinds  []state.RebalanceRow
		res    *state.Reserve
		goals  []state.Goal
		amount float64
		sug    []suggestion
	}{
		{name: "один вид на сотню", kinds: []state.RebalanceRow{kindRow("bonds", 100, 0)},
			amount: 3400, sug: []suggestion{bondSug("UA0001", 1000, money.UAH)}},
		{name: "частки менші за сотню", kinds: []state.RebalanceRow{kindRow("bonds", 60, 0)},
			amount: 10000, sug: []suggestion{bondSug("UA0001", 1000, money.UAH)}},
		// Вид уже на своїй частці: потреб немає, і весь avail — лишок, який
		// ділиться за частками. 60% з нього йдуть у папери, а 40%
		// нерозподілених мусять лишитись видимими — поза частками.
		{name: "частки менші за сотню, лишок", kinds: []state.RebalanceRow{kindRow("bonds", 60, 66000)},
			amount: 10000, sug: []suggestion{bondSug("UA0001", 1000, money.UAH)}},
		{name: "два види й недобір", kinds: []state.RebalanceRow{kindRow("bonds", 50, 30000), kindRow("deposits", 30, 0)},
			amount: 7777.77, sug: []suggestion{bondSug("UA0001", 1033.33, money.UAH)}},
		{name: "подушка частково", kinds: []state.RebalanceRow{kindRow("bonds", 100, 0)}, res: gap(1234.56, 5000),
			amount: 5000, sug: []suggestion{bondSug("UA0001", 999.99, money.UAH)}},
		{name: "подушка й ціль", kinds: []state.RebalanceRow{kindRow("bonds", 70, 0)}, res: gap(1000, 1000),
			goals: []state.Goal{{ID: 7, Name: "авто", FillNowUAH: state.Major(777.77, money.UAH),
				FillMonthUAH: state.Major(777.77, money.UAH), GapUAH: state.Major(50000, money.UAH)}},
			amount: 9999.99, sug: []suggestion{bondSug("UA0001", 1000, money.UAH)}},
		{name: "доларовий папір", kinds: []state.RebalanceRow{kindRow("bonds", 100, 0)},
			amount: 20000, sug: []suggestion{bondSug("UA0002", 1003.37, money.USD)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := allocDoc(tc.kinds, tc.res)
			doc.Goals = tc.goals
			minor := int64(tc.amount*100 + 0.5)
			got := AllocatePlan(doc, tc.sug, allocRates, ToMoneyJSON(money.New(minor, money.UAH)), tc.amount,
				AllocAllow{ReserveUAH: tc.amount, GoalsUAH: tc.amount}, money.UAH, nil)
			r, g, l, free, rest := allocParts(got)
			if sum := r + g + l + free + rest; sum != minor {
				t.Fatalf("подушка %d + цілі %d + рядки %d + поза частками %d + залишок %d = %d коп., а прийшло %d (різниця %d)\n%s",
					r, g, l, free, rest, sum, minor, minor-sum, dumpAlloc(got))
			}
			if free > 0 && got.FreeWhy == "" {
				t.Errorf("%d коп. поза частками без пояснення", free)
			}
			if avail := got.AvailUAH.Minor(); avail != minor-r-g && got.Reserve == nil && len(got.Goals) == 0 {
				t.Errorf("avail %d, а без вирізок мало бути %d", avail, minor)
			}
		})
	}
}

func dumpAlloc(p allocPlan) string {
	s := fmt.Sprintf("avail=%.2f rest=%.2f (%s) free=%.2f note=%q\n",
		p.AvailUAH.Major(), p.RestUAH.Major(), p.RestWhy, p.FreeUAH.Major(), p.Note)
	for _, l := range p.Lines {
		s += fmt.Sprintf("  %s %s ×%d = %.2f\n", l.Kind, l.Ref, l.Qty, l.TotalUAH.Major())
	}
	return s
}

// Бюджети видів із spreadMonth у сумі дають рівно avail, до копійки, коли
// частки покривають сотню: гроші місяця не губляться між рядками на
// округленні кожного з них окремо.
func TestSpreadMonthBudgetsSumToAvail(t *testing.T) {
	for _, avail := range []float64{100, 3333.33, 10000, 12345.67, 99999.99} {
		rows := []state.RebalanceRow{kindRow("bonds", 33.3, 1000), kindRow("deposits", 33.3, 5000),
			kindRow("funds", 33.4, 0)}
		spreadMonth(rows, avail, 6000)
		var sum int64
		for _, r := range rows {
			sum += r.MonthBalanceUAH.Minor()
		}
		if want := int64(avail*100 + 0.5); sum != want {
			t.Errorf("avail %.2f: бюджети в сумі %d коп., чекали %d", avail, sum, want)
		}
	}
}

// Нерозподілені відсотки не віддаються ні названим видам, ні другому
// проходу, ні залишку: 60% із 10 000 ₴ — у папери, 4 000 ₴ — поза частками,
// з поясненням, і маршрут їх далі не везе.
func TestAllocateUnassignedShareStaysFree(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 60, 66000)}, nil)
	got := AllocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)}, allocRates,
		ToMoneyJSON(money.New(1_000_000, money.UAH)), 10000,
		AllocAllow{ReserveUAH: 10000, GoalsUAH: 10000}, money.UAH, nil)
	if got.FreeUAH.Minor() != 400_000 {
		t.Fatalf("поза частками %.2f, чекали 4000\n%s", got.FreeUAH.Major(), dumpAlloc(got))
	}
	if got.RestUAH.Minor() != 0 {
		t.Errorf("залишок %.2f: нерозподілене не мало потрапити туди, звідки маршрут везе далі",
			got.RestUAH.Major())
	}
	if !strings.Contains(got.FreeWhy, "60%") {
		t.Errorf("пояснення мало назвати суму часток: %q", got.FreeWhy)
	}
}

// Крок лота — ціна паперу, перекладена ЄДИНОЮ точкою конвертації
// (fx.ToUAH, банківське округлення до копійки), а не float-добутком ціни
// на курс. Доти рядок на двадцять доларових паперів розходився з «ціна за
// штуку × кількість» на копійки, бо кожен папір віз свою дріб копійки.
func TestAllocateLotStepUsesFXToUAH(t *testing.T) {
	rates := fx.Rates{money.USD: 412345} // 41.2345 ₴/$
	sug := []suggestion{bondSug("UA0002", 1003.37, money.USD)}
	unit, err := fx.ToUAH(money.New(100337, money.USD), rates)
	if err != nil {
		t.Fatal(err)
	}
	budget := float64(20*unit.Amount()+50) / 100
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	got := AllocatePlan(doc, sug, rates, ToMoneyJSON(money.New(int64(budget*100+0.5), money.UAH)), budget,
		AllocAllow{}, money.UAH, nil)
	if len(got.Lines) != 1 || got.Lines[0].Qty != 20 {
		t.Fatalf("чекали 20 паперів:\n%s", dumpAlloc(got))
	}
	if want := 20 * unit.Amount(); got.Lines[0].TotalUAH.Minor() != want {
		t.Errorf("рядок %d коп., а 20 × fx.ToUAH(ціни) = %d", got.Lines[0].TotalUAH.Minor(), want)
	}
}
