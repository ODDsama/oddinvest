package api

import (
	"strings"
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
		allocRates, toMoneyJSON(money.New(340000, money.UAH)), 3400,
		allocAllow{ReserveUAH: 3400, GoalsUAH: 3400}, money.UAH, nil)

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
		allocRates, toMoneyJSON(money.New(500000, money.UAH)), 5000,
		allocAllow{ReserveUAH: 5000, GoalsUAH: 5000}, money.UAH, nil)

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
		allocRates, toMoneyJSON(money.New(500000, money.UAH)), 5000,
		allocAllow{ReserveUAH: 5000, GoalsUAH: 5000}, money.UAH, nil)

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
		toMoneyJSON(money.New(500000, money.UAH)), 5000,
		allocAllow{ReserveUAH: 5000, GoalsUAH: 5000}, money.UAH, nil)

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
		allocRates, toMoneyJSON(money.New(50000, money.USD)), 22000,
		allocAllow{ReserveUAH: 22000, GoalsUAH: 22000}, money.USD, nil)

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
		toMoneyJSON(money.New(400000, money.UAH)), 4000,
		allocAllow{ReserveUAH: 4000, GoalsUAH: 4000}, money.UAH,
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
	}, allocRates, toMoneyJSON(money.New(400000, money.UAH)), 4000,
		allocAllow{ReserveUAH: 4000, GoalsUAH: 4000}, money.UAH, nil)

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
		allocRates, toMoneyJSON(money.New(500000, money.UAH)), 5000,
		allocAllow{ReserveUAH: 5000, GoalsUAH: 5000}, money.UAH, nil)

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
		allocRates, toMoneyJSON(money.New(50000, money.UAH)), 500,
		allocAllow{ReserveUAH: 500, GoalsUAH: 500}, money.UAH, nil)

	for _, l := range got.Lines {
		if l.TotalUAH > 500 {
			t.Fatalf("розкладка 500 ₴ порадила %.2f — числа з плану місяця протекли", l.TotalUAH)
		}
	}
	if got.RestUAH != 500 {
		t.Errorf("залишок %.2f, чекали 500: на квиток 1000 ₴ не вистачає", got.RestUAH)
	}
}

// --- дозвіл самого надходження (0041) ---
//
// Політика (reserve_fill_from) каже, З ЯКИХ ГРОШЕЙ узагалі можна
// наповнювати подушку; дозвіл каже, чи можна з ЦИХ конкретних. Друга межа
// поверх першої, і жодна з них не має права розширити іншу.

// Заборонене джерело не дає вирізки — і причина називає САМЕ ЙОГО.
//
// Текст перевіряється не з педантизму: рядок «її наповнює лише плановий
// дохід» під відміткою планового доходу читається як поломка застосунку.
func TestAllocateSourceForbidsReserve(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)},
		&state.Reserve{FillNowUAH: 2000, FillMonthUAH: 2000, GapUAH: 50000})

	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(500000, money.UAH)), 5000,
		// Стеля подушки нульова, бо джерело їй заборонене; цілям — ні.
		allocAllow{ReserveUAH: 0, GoalsUAH: 5000, Uses: "goals,invest"},
		money.UAH, nil)

	if got.Reserve != nil {
		t.Errorf("вирізка подушки %+v — джерело їй заборонене", got.Reserve)
	}
	if got.ReserveSkipWhy == "" {
		t.Fatal("вирізки немає й причини немає — це читається як поломка")
	}
	if !strings.Contains(got.ReserveSkipWhy, "надходження") {
		t.Errorf("причина %q не називає джерело — вона веде в «Політику», де все правильно",
			got.ReserveSkipWhy)
	}
	// Гроші не зникли: усе, що подушка не взяла, пішло в папери.
	if got.AvailUAH != 5000 {
		t.Errorf("доступно %v, очікували всі 5000", got.AvailUAH)
	}
}

// Заборонений ВИД гаситься ціллю, а не викидається після поділу: його
// частка перетікає в дозволені види, і сума розкладки лишається сумою
// надходження. Інакше застосунок казав би «не набралось на крок» там, де
// правда інша — «цим грошам туди не можна».
func TestAllocateForbiddenNPFRedistributes(t *testing.T) {
	rows := []state.RebalanceRow{kindRow("bonds", 50, 0), kindRow("npf", 50, 0)}
	sug := []suggestion{
		bondSug("UA0001", 1000, money.UAH),
		{Kind: "npf", Label: "Династія", Currency: money.UAH,
			CostPerBond: toMoneyJSON(money.New(0, money.UAH))},
	}
	npf := map[string]int64{"Династія": 7}

	free := allocatePlan(allocDoc(rows, nil), sug, allocRates,
		toMoneyJSON(money.New(400000, money.UAH)), 4000,
		allocAllow{ReserveUAH: 4000, GoalsUAH: 4000}, money.UAH, npf)
	if !hasKind(free.Lines, "npf") {
		t.Fatal("без заборони рядка НПФ немає — тест перевіряв би не те")
	}

	got := allocatePlan(allocDoc(rows, nil), sug, allocRates,
		toMoneyJSON(money.New(400000, money.UAH)), 4000,
		allocAllow{ReserveUAH: 4000, GoalsUAH: 4000, Uses: "reserve,goals,invest"},
		money.UAH, npf)
	if hasKind(got.Lines, "npf") {
		t.Error("рядок НПФ лишився, хоч джерело його забороняє")
	}
	// Ті самі 4 000 ₴, лише всі в дозволений вид: заборона не має
	// перетворювати гроші на безіменний залишок.
	if spent := linesTotal(got.Lines); spent != 4000 {
		t.Errorf("у папери пішло %v, очікували всі 4000 — частка НПФ мусить перетекти", spent)
	}
	if got.RestUAH != 0 {
		t.Errorf("залишок %v, очікували 0", got.RestUAH)
	}
}

// Джерело, якому не можна в жоден інструмент, після подушки й цілей
// лишається залишком — але залишком НАЗВАНИМ. Мовчазна сума тут читалась
// би як загублена, а типова причина («цілей за видом не задано») повела б
// у налаштування, де все правильно.
func TestAllocateSavingsOnlyNamesItsReason(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(500000, money.UAH)), 5000,
		allocAllow{ReserveUAH: 5000, GoalsUAH: 5000, Uses: "reserve,goals"},
		money.UAH, nil)

	if len(got.Lines) != 0 {
		t.Errorf("рядки покупок є, хоч інструменти заборонені: %+v", got.Lines)
	}
	if got.RestUAH != 5000 {
		t.Errorf("залишок %v, очікували 5000", got.RestUAH)
	}
	if !strings.Contains(got.Note, "дозволено") {
		t.Errorf("причина %q не про дозвіл — вона веде не туди", got.Note)
	}
}

func hasKind(lines []allocLine, kind string) bool {
	for _, l := range lines {
		if l.Kind == kind {
			return true
		}
	}
	return false
}

func linesTotal(lines []allocLine) float64 {
	var sum float64
	for _, l := range lines {
		sum += l.TotalUAH
	}
	return sum
}

// --- поріг призначення ---

// npfSug — порада «внести в пенсійний». Ціни кроку в неї немає й бути не
// може: фонд приймає будь-яку суму, а поріг у ній — від людини (allocMinCutUAH).
func npfSug(name string) suggestion {
	return suggestion{
		Kind: "npf", Label: name, Currency: money.UAH,
		CostPerBond: toMoneyJSON(money.New(0, money.UAH)),
	}
}

var npfOne = map[string]int64{"Династія": 1}

// Внесок нижче порога рядком не стає, а гроші лишаються в залишку.
//
// Доки порога не було, застосунок радив віднести в пенсійний двадцять шість
// копійок — і в шістнадцяти ногах маршруту з сорока семи це була його ЄДИНА
// порада.
func TestAllocateNPFBelowFloorSkipped(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("npf", 100, 0)}, nil)
	got := allocatePlan(doc, []suggestion{npfSug("Династія")},
		allocRates, toMoneyJSON(money.New(400, money.UAH)), 4,
		allocAllow{ReserveUAH: 4, GoalsUAH: 4}, money.UAH, npfOne)

	if len(got.Lines) != 0 {
		t.Fatalf("рядок нижче порога: %+v", got.Lines)
	}
	if got.RestUAH != 4 {
		t.Errorf("залишок %.2f, чекали всі 4 — гроші не гинуть", got.RestUAH)
	}
	// Причина мусить назвати поріг і рахунок: «інструментів із відомою ціною
	// немає» тут було б неправдою про наявний пенсійний.
	if !strings.Contains(got.RestWhy, "Династія") ||
		!strings.Contains(got.RestWhy, uah(allocMinCutUAH)) {
		t.Errorf("причина залишку не називає порога й рахунку: %q", got.RestWhy)
	}
}

// Рівно поріг проходить: межа включна, інакше «менше за 10» і «10» читались
// би однаково.
func TestAllocateNPFAtFloorTaken(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("npf", 100, 0)}, nil)
	got := allocatePlan(doc, []suggestion{npfSug("Династія")},
		allocRates, toMoneyJSON(money.New(1000, money.UAH)), allocMinCutUAH,
		allocAllow{ReserveUAH: allocMinCutUAH, GoalsUAH: allocMinCutUAH},
		money.UAH, npfOne)

	if len(got.Lines) != 1 {
		t.Fatalf("рядків %d, чекали 1 — рівно поріг проходить: %+v", len(got.Lines), got)
	}
	if got.Lines[0].TotalUAH != allocMinCutUAH {
		t.Errorf("сума рядка %.2f, чекали %d", got.Lines[0].TotalUAH, allocMinCutUAH)
	}
}

// Вирізка подушки нижче порога не робиться, а гроші лишаються доступними
// паперам цієї ж ноги.
func TestAllocateReserveBelowFloorSkippedButMoneyStays(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)},
		&state.Reserve{FillNowUAH: 3, FillMonthUAH: 3, GapUAH: 50000})
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(340000, money.UAH)), 3400,
		allocAllow{ReserveUAH: 3400, GoalsUAH: 3400}, money.UAH, nil)

	if got.Reserve != nil {
		t.Fatalf("вирізка нижче порога: %+v", got.Reserve)
	}
	if got.AvailUAH != 3400 {
		t.Errorf("доступно %.2f, чекали всі 3400 — пропущена вирізка нікуди не поділась",
			got.AvailUAH)
	}
	// Причина саме порогова: політика й дозвіл тут ні при чому, і послати
	// людину в «Політику» означало б збрехати про місце поломки.
	if got.ReserveSkipWhy == "" {
		t.Fatal("зникла вирізка без причини читається як поломка")
	}
	if strings.Contains(got.ReserveSkipWhy, "політикою") ||
		strings.Contains(got.ReserveSkipWhy, "позначене") {
		t.Errorf("причина вказує не туди: %q", got.ReserveSkipWhy)
	}
}

// Виняток із порога: вирізка, якої досить, щоб розрив ЗНИК, робиться попри
// поріг. Інакше остання пʼятірка гривень до цілі не закрилась би ніколи.
func TestAllocateReserveBelowFloorClosesGap(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)},
		&state.Reserve{FillNowUAH: 4, FillMonthUAH: 4, GapUAH: 4})
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(340000, money.UAH)), 3400,
		allocAllow{ReserveUAH: 3400, GoalsUAH: 3400}, money.UAH, nil)

	if got.Reserve == nil || got.Reserve.AmountUAH != 4 {
		t.Fatalf("розрив не закрився: %+v", got.Reserve)
	}
}

// Те саме дзеркально для цілі накопичення.
func TestAllocateGoalBelowFloorClosesGap(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)}, nil)
	doc.Goals = []state.Goal{
		{ID: 1, Name: "майже зібрана", FillNowUAH: 4, FillMonthUAH: 4, GapUAH: 4},
		{ID: 2, Name: "далека", FillNowUAH: 6, FillMonthUAH: 6, GapUAH: 50000},
	}
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(340000, money.UAH)), 3400,
		allocAllow{ReserveUAH: 3400, GoalsUAH: 3400}, money.UAH, nil)

	if len(got.Goals) != 1 || got.Goals[0].ID != 1 {
		t.Fatalf("цілі взяли не те: %+v", got.Goals)
	}
	if got.GoalsSkipWhy == "" {
		t.Error("друга ціль мовчки не взяла своїх шести гривень")
	}
}

// Політика вріже частину, а решта не дотягує до порога — обидві причини
// мусять бути названі. Одна замість двох повела б у настройку, яка не
// пояснює всього.
func TestAllocateFloorReasonsDoNotCollide(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 0)},
		&state.Reserve{FillNowUAH: 3000, FillMonthUAH: 3000, GapUAH: 50000})
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(340000, money.UAH)), 3400,
		// Дозволено лише пʼять гривень: решту ріже політика, а й ці пʼять
		// не дотягують до порога.
		allocAllow{ReserveUAH: 5, GoalsUAH: 3400}, money.UAH, nil)

	if got.Reserve != nil {
		t.Fatalf("вирізка нижче порога: %+v", got.Reserve)
	}
	if !strings.Contains(got.ReserveSkipWhy, "політикою") {
		t.Errorf("причина мовчить про політику: %q", got.ReserveSkipWhy)
	}
	if !strings.Contains(got.ReserveSkipWhy, uah(allocMinCutUAH)) {
		t.Errorf("причина мовчить про поріг: %q", got.ReserveSkipWhy)
	}
}
