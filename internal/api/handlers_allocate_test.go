package api

import (
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
)

// allocDoc — портфель, у якому ОВДП добирають до цілі, а решта видів цілей
// не мають. Мінімальний, бо allocatePlan читає з документа рівно чотири
// речі: подушку, рядки ребалансу, капітал і резерв.
func allocDoc(kinds []state.RebalanceRow, res *state.Reserve) *state.Doc {
	return &state.Doc{
		CapitalUAH: 100000, ReserveUAH: 0,
		Rebalance: kinds, Reserve: res,
	}
}

func kindRow(key string, targetPct, currentUAH float64) state.RebalanceRow {
	return state.RebalanceRow{
		Dimension: "kind", Key: key, Currency: money.UAH,
		TargetPct: targetPct, CurrentUAH: currentUAH,
	}
}

// bondSug — порада «папір за 1000 ₴», тобто типовий квиток ОВДП.
func bondSug(isin string, costMajor float64, cur string) suggestion {
	return suggestion{
		Kind: "bond", Label: isin, ISIN: isin, Currency: cur,
		CostPerBond: toMoneyJSON(money.New(int64(costMajor*100), cur)),
		RealPct:     9.4, Reason: "рік 2028",
	}
}

// Курс ×10⁴, як він і лежить у сховищі: 44.0000 ₴/$. Готовим числом, а не
// добутком на константу масштабу: масштаб за межі пакета fx не витікає
// (make fx-boundary), і рядки в state_rebalance_test.go написані так само.
var allocRates = fx.Rates{money.USD: 440000}

// Головне число фази: бюджет ділиться на ціну квитка ВНИЗ, і залишок
// лишається залишком. Половини облігації не буває.
func TestAllocateWholeTicketsOnly(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(340000, money.UAH)), 3400, 3400, 3400, money.UAH, nil)

	if len(got.Lines) != 1 {
		t.Fatalf("рядків %d, чекали 1: %+v", len(got.Lines), got)
	}
	if got.Lines[0].Qty != 3 {
		t.Errorf("кількість %d, чекали 3 (3400 ÷ 1000 вниз)", got.Lines[0].Qty)
	}
	if got.Lines[0].TotalUAH != 3000 {
		t.Errorf("сума рядка %.2f, чекали 3000", got.Lines[0].TotalUAH)
	}
	if got.RestUAH != 400 {
		t.Errorf("залишок %.2f, чекали 400", got.RestUAH)
	}
	if got.RestWhy == "" {
		t.Error("залишок без причини читається як загублені гроші")
	}
	if !got.Lines[0].Addable {
		t.Error("папір мусить класти́сь у план купівель одним рухом")
	}
}

// Подушка забирає своє ПЕРШОЮ, і коли розрив більший за суму — забирає все.
// Хвіст добере наступна відмітка; рядків покупок при цьому бути не може.
func TestAllocateReserveEatsEverything(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)},
		&state.Reserve{FillNowUAH: 9000, FillMonthUAH: 9000, GapUAH: 50000})
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(500000, money.UAH)), 5000, 5000, 5000, money.UAH, nil)

	if got.Reserve == nil || got.Reserve.AmountUAH != 5000 {
		t.Fatalf("вирізка резерву %+v, чекали всі 5000", got.Reserve)
	}
	if got.AvailUAH != 0 || len(got.Lines) != 0 {
		t.Errorf("після подушки нема чого розкладати, а маємо avail=%.2f, рядків %d",
			got.AvailUAH, len(got.Lines))
	}
	if got.Note == "" {
		t.Error("порожня відповідь без причини читається як поломка")
	}
}

// Часткове закриття: подушка бере свою місячну частку, решта йде в папери.
func TestAllocateReserveThenBuys(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)},
		&state.Reserve{FillNowUAH: 2000, FillMonthUAH: 2000, GapUAH: 40000})
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(500000, money.UAH)), 5000, 5000, 5000, money.UAH, nil)

	if got.Reserve == nil || got.Reserve.AmountUAH != 2000 {
		t.Fatalf("вирізка резерву %+v, чекали 2000", got.Reserve)
	}
	if got.AvailUAH != 3000 {
		t.Fatalf("доступно %.2f, чекали 3000", got.AvailUAH)
	}
	if len(got.Lines) != 1 || got.Lines[0].Qty != 3 {
		t.Errorf("чекали 3 папери з 3000 ₴, маємо %+v", got.Lines)
	}
}

// Вид у ПЕРЕКОСІ дістає нуль — це і є «на вирівнювання». Фонди тут удвічі
// понад ціль, тож усі гроші мусять піти в ОВДП, яких бракує.
func TestAllocateSkipsOvershotKind(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{
		kindRow("bonds", 50, 0),
		kindRow("funds", 50, 100000),
	}, nil)
	sug := []suggestion{
		bondSug("UA0001", 1000, money.UAH),
		{
			Kind: "fund", Label: "REIT", Currency: money.UAH,
			CostPerBond: toMoneyJSON(money.New(10000, money.UAH)), RealPct: 3,
		},
	}
	got := allocatePlan(doc, sug, allocRates,
		toMoneyJSON(money.New(500000, money.UAH)), 5000, 5000, 5000, money.UAH, nil)

	for _, l := range got.Lines {
		if l.Kind == "fund" {
			t.Fatalf("фонди вже вдвічі понад ціль — на вирівнювання їм нуль: %+v", got.Lines)
		}
	}
	if len(got.Lines) != 1 || got.Lines[0].Qty != 5 {
		t.Errorf("чекали 5 паперів з 5000 ₴, маємо %+v", got.Lines)
	}
}

// Порада в чужій валюті НЕ ховається, але й не мовчить: позначка плюс
// сума, яку доведеться поміняти.
func TestAllocateMarksConversion(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	// Гривневий папір за 1000 ₴ на доларову суму: 500 $ це 22 000 ₴.
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(50000, money.USD)), 22000, 22000, 22000, money.USD, nil)

	if len(got.Lines) != 1 {
		t.Fatalf("рядків %d, чекали 1: %+v", len(got.Lines), got)
	}
	l := got.Lines[0]
	if !l.Convert {
		t.Error("гривневий папір за доларову суму — це конвертація, і мовчати про неї не можна")
	}
	if l.ConvertNative != 500 {
		t.Errorf("міняти %.2f, чекали 500 $ (22000 ÷ 44)", l.ConvertNative)
	}
}

// Внесок у пенсійний бере бюджет виду цілком: порога входу він не має.
// І кладеться в кошик — на відміну від вкладу, у якого в plan_buys немає
// ні строку, ні ставки.
func TestAllocateNPFTakesWholeBudgetAndDepositDoesNot(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{
		kindRow("npf", 50, 0),
		kindRow("deposits", 50, 0),
	}, nil)
	sug := []suggestion{
		{Kind: "npf", Label: "Династія", Currency: money.UAH,
			CostPerBond: toMoneyJSON(money.New(0, money.UAH)), RealPct: 12},
		{Kind: "deposit", Label: "mono", Currency: money.UAH,
			CostPerBond: toMoneyJSON(money.New(100000, money.UAH)), RealPct: 5},
	}
	got := allocatePlan(doc, sug, allocRates,
		toMoneyJSON(money.New(400000, money.UAH)), 4000, 4000, 4000, money.UAH,
		map[string]int64{"Династія": 7})

	var npf, dep *allocLine
	for i := range got.Lines {
		switch got.Lines[i].Kind {
		case "npf":
			npf = &got.Lines[i]
		case "deposit":
			dep = &got.Lines[i]
		}
	}
	if npf == nil {
		t.Fatalf("рядка НПФ немає: %+v", got.Lines)
	}
	if npf.Ref != "7" {
		t.Errorf("ref НПФ %q, чекали id рахунку \"7\"", npf.Ref)
	}
	if !npf.Addable {
		t.Error("внесок у пенсійний plan_buys приймає: сума — усе, що йому треба")
	}
	if npf.TotalUAH != 2000 {
		t.Errorf("внесок %.2f, чекали весь бюджет виду — 2000", npf.TotalUAH)
	}
	if dep == nil {
		t.Fatalf("рядка вкладу немає: %+v", got.Lines)
	}
	if dep.Addable {
		t.Error("вклад у кошик не кладеться: у поради немає ні строку, ні банку для «нового»")
	}
}

// Рахунок із порожнім id — рядка немає взагалі. Вгадувати, у котрий саме
// пенсійний вносити, застосунок не буде.
func TestAllocateNPFWithoutIDSkipped(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("npf", 100, 0)}, nil)
	got := allocatePlan(doc, []suggestion{
		{Kind: "npf", Label: "Династія", Currency: money.UAH,
			CostPerBond: toMoneyJSON(money.New(0, money.UAH))},
	}, allocRates, toMoneyJSON(money.New(400000, money.UAH)), 4000, 4000, 4000, money.UAH, nil)

	if len(got.Lines) != 0 {
		t.Fatalf("без id рахунку рядка бути не може: %+v", got.Lines)
	}
	if got.RestUAH != 4000 {
		t.Errorf("залишок %.2f, чекали всі 4000", got.RestUAH)
	}
}

// Без жодної цілі за видом розкладати нема за яким правилом — і застосунок
// каже це словами, а не мовчазним порожнім списком.
func TestAllocateWithoutKindTargets(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 0, 50000)}, nil)
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(500000, money.UAH)), 5000, 5000, 5000, money.UAH, nil)

	if len(got.Lines) != 0 {
		t.Fatalf("цілей немає — рядків бути не може: %+v", got.Lines)
	}
	if got.Note == "" {
		t.Error("порожня відповідь мусить назвати причину")
	}
	if got.RestUAH != 5000 {
		t.Errorf("залишок %.2f, чекали всі 5000", got.RestUAH)
	}
}

// Стара розкладка НЕ протікає в нову. spreadMonth виходить одразу, коли
// ділити нема чого, і чужих чисел за собою не прибирає — тож рядки
// документа, які вже несуть поділ ПЛАНУ МІСЯЦЯ, мусять обнулятись перед
// викликом. Без цього розкладка 500 ₴ порадила б купити на тридцять тисяч.
func TestAllocateIgnoresMonthSplitFromDoc(t *testing.T) {
	row := kindRow("bonds", 100, 0)
	row.MonthBalanceUAH = 30000 // поділ плану місяця, що вже лежить у документі
	doc := allocDoc([]state.RebalanceRow{row}, nil)
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(50000, money.UAH)), 500, 500, 500, money.UAH, nil)

	for _, l := range got.Lines {
		if l.TotalUAH > 500 {
			t.Fatalf("розкладка 500 ₴ порадила %.2f — числа з плану місяця протекли", l.TotalUAH)
		}
	}
	if got.RestUAH != 500 {
		t.Errorf("залишок %.2f, чекали 500: на квиток 1000 ₴ не вистачає", got.RestUAH)
	}
}
