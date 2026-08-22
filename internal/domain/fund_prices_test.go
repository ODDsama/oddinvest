package domain

import "testing"

// Позначка, свіжіша за останню операцію, витісняє ціну виписки — і саме
// вона стає ринковою вартістю. Для накопичувального фонду це єдиний спосіб
// побачити дохід узагалі.
func TestFundPositionsMarkOverridesStalePrice(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-01-10", Fund: "F", Kind: FundBuy, Qty: 100, Amount: 100000, Currency: "UAH"},
	}
	marks := []FundPrice{{Fund: "F", Date: "2026-07-10", Price: 110000}}
	p := FundPositions(ops, marks)["F"]
	if p.LastPrice != 110000 || p.LastPriceDate != "2026-07-10" {
		t.Fatalf("ціна мала прийти з позначки, маємо %d від %s", p.LastPrice, p.LastPriceDate)
	}
	if !p.PriceMarked {
		t.Error("позначка свіжіша за операцію — PriceMarked мав стати істинним")
	}
	if p.MarketValue() != 110000 {
		t.Errorf("вартість мала вирости до 110000, маємо %d", p.MarketValue())
	}
}

// Позначка, ДАВНІША за останню операцію, ціну не чіпає: виписка знає
// свіжіше.
func TestFundPositionsMarkOlderThanOpIgnored(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-06-01", Fund: "F", Kind: FundBuy, Qty: 100, Amount: 100000, Currency: "UAH"},
	}
	marks := []FundPrice{{Fund: "F", Date: "2026-01-01", Price: 80000}}
	p := FundPositions(ops, marks)["F"]
	if p.LastPrice != 100000 || p.PriceMarked {
		t.Fatalf("стара позначка не мала витіснити ціну виписки: %d, marked=%v",
			p.LastPrice, p.PriceMarked)
	}
}

// На однаковій даті виграє позначка — вона точна, а виведена з операції
// несе округлення суми. Але незалежним джерелом вона не стає: нової дати
// вона не додала.
func TestFundPositionsMarkOnOpDateWinsButIsNotEvidence(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-06-01", Fund: "F", Kind: FundBuy, Qty: 3, Amount: 10000, Currency: "UAH"},
	}
	marks := []FundPrice{{Fund: "F", Date: "2026-06-01", Price: 333333}}
	p := FundPositions(ops, marks)["F"]
	if p.LastPrice != 333333 {
		t.Errorf("позначка на дату операції мала виграти, маємо %d", p.LastPrice)
	}
	if p.PriceMarked {
		t.Error("позначка в день операції лише повторює виписку — незалежним джерелом вона не є")
	}
}

// Позначка на фонд без операцій позиції не створює: нуль сертифікатів дає
// нуль вартості, а порожній рядок у картці був би позицією, якої немає.
func TestFundPositionsMarkWithoutOpsMakesNoPosition(t *testing.T) {
	if got := FundPositions(nil, []FundPrice{{Fund: "F", Date: "2026-06-01", Price: 1000}}); len(got) != 0 {
		t.Fatalf("позиції не мало зʼявитись, маємо %d", len(got))
	}
}

// ГОЛОВНЕ. Одна купівля й більше нічого — дохідність не вимірюється.
//
// Доти тут поверталось (0, true), і цей нуль витісняв обіцянку фонду: XIRR
// набору «−A у день купівлі, +A сьогодні» дорівнює нулю за побудовою, бо
// терміналом стоїть та сама ціна, за якою я купив.
func TestFundTotalReturnSilentWithoutEvidence(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-01-10", Fund: "F", Kind: FundBuy, Qty: 100, Amount: 100000, Currency: "UAH"},
	}
	if r, ok := FundTotalReturn(ops, nil, "F", "2026-07-10"); ok {
		t.Fatalf("міряти нема по чому, а функція відповіла %.2f", r)
	}
}

// Вклеєна історія ЦІЛКОМ ДО купівлі виміру не дає: дат багато, а термінал
// усе одно лишається ціною тієї самої купівлі. Саме через цей випадок
// охорона питає про виродження позиції, а не рахує точки ціни.
func TestFundTotalReturnSilentWhenHistoryEndsAtBuy(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-01-10", Fund: "F", Kind: FundBuy, Qty: 100, Amount: 100000, Currency: "UAH"},
	}
	marks := []FundPrice{
		{Fund: "F", Date: "2024-01-01", Price: 60000},
		{Fund: "F", Date: "2025-01-01", Price: 80000},
	}
	if r, ok := FundTotalReturn(ops, marks, "F", "2026-07-10"); ok {
		t.Fatalf("ціни після купівлі не знаємо — міряти нема по чому, а маємо %.2f", r)
	}
}

// Та сама позиція з позначкою ціни — дохідність зʼявляється.
func TestFundTotalReturnSpeaksAfterMark(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-01-10", Fund: "F", Kind: FundBuy, Qty: 100, Amount: 100000, Currency: "UAH"},
	}
	marks := []FundPrice{{Fund: "F", Date: "2026-07-10", Price: 110000}}
	r, ok := FundTotalReturn(ops, marks, "F", "2026-07-10")
	if !ok {
		t.Fatal("з позначкою дохідність мала порахуватись")
	}
	// +10% за 181 день ануалізуються приблизно у 21%.
	if r < 15 || r > 25 {
		t.Errorf("очікували близько 21%% річних, маємо %.2f", r)
	}
}

// Дивіденд після купівлі — доказ не гірший за позначку: гроші реально
// прийшли. Охорона гілки REIT.
func TestFundTotalReturnSpeaksOnDividendAfterBuy(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-01-10", Fund: "F", Kind: FundBuy, Qty: 100, Amount: 100000, Currency: "UAH"},
		{Date: "2026-06-10", Fund: "F", Kind: FundDividend, Amount: 5000, Tax: 700, Currency: "UAH"},
	}
	if _, ok := FundTotalReturn(ops, nil, "F", "2026-07-10"); !ok {
		t.Fatal("дивіденд після купівлі — це вимір, а не тавтологія")
	}
}

// Дві купівлі за різними цінами теж міряють зміну ціни — без жодної
// позначки. Сітка під TestFundTotalReturnSeesPriceGrowth у server_test.go.
func TestFundTotalReturnSpeaksOnSecondBuy(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-01-10", Fund: "F", Kind: FundBuy, Qty: 100, Amount: 100000, Currency: "UAH"},
		{Date: "2026-07-09", Fund: "F", Kind: FundBuy, Qty: 1, Amount: 1100, Currency: "UAH"},
	}
	if _, ok := FundTotalReturn(ops, nil, "F", "2026-07-10"); !ok {
		t.Fatal("друга купівля принесла нову ціну — вимір є")
	}
}

// Зростання самої ціни: менше двох точок або коротший за пів року відрізок
// — не число.
func TestFundPriceReturnNeedsTwoPointsAndHalfYear(t *testing.T) {
	one := []FundPrice{{Fund: "F", Date: "2026-01-01", Price: 100000}}
	if _, ok := FundPriceReturn(one, "2026-07-10"); ok {
		t.Error("одна точка кривої не задає")
	}
	near := []FundPrice{
		{Fund: "F", Date: "2026-05-01", Price: 100000},
		{Fund: "F", Date: "2026-07-01", Price: 110000},
	}
	if _, ok := FundPriceReturn(near, "2026-07-10"); ok {
		t.Error("два місяці ануалізувати не можна — вийдуть тризначні відсотки")
	}
	year := []FundPrice{
		{Fund: "F", Date: "2025-07-01", Price: 100000},
		{Fund: "F", Date: "2026-07-01", Price: 125000},
	}
	r, ok := FundPriceReturn(year, "2026-07-10")
	if !ok {
		t.Fatal("рік між точками — цілком достатньо")
	}
	if r < 24 || r > 26 {
		t.Errorf("+25%% за рік мали дати близько 25%%, маємо %.2f", r)
	}
}

// Точки ціни зводяться з двох джерел, і позначка перекриває виведену з
// операції на ту саму дату.
func TestFundPricePointsMergeSources(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-01-10", Fund: "F", Kind: FundBuy, Qty: 100, Amount: 100000, Currency: "UAH"},
		{Date: "2026-04-10", Fund: "F", Kind: FundDividend, Amount: 500, Currency: "UAH"},
	}
	marks := []FundPrice{
		{Fund: "F", Date: "2026-01-10", Price: 100500},
		{Fund: "F", Date: "2026-07-10", Price: 110000},
	}
	pts := FundPricePoints(marks, ops, "F")
	if len(pts) != 2 {
		t.Fatalf("дивіденд ціни не несе, тож точок мало бути дві: %+v", pts)
	}
	if pts[0].Price != 100500 {
		t.Errorf("позначка мала перекрити ціну операції, маємо %d", pts[0].Price)
	}
}

// Термінальний потік XIRR іде за позначкою — той самий інваріант, заради
// якого мінялась сигнатура: вартість у картці й термінал у дохідності
// зобовʼязані бути одним числом.
func TestFundFlowsOneTerminalFollowsMark(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-01-10", Fund: "F", Kind: FundBuy, Qty: 100, Amount: 100000, Currency: "UAH"},
	}
	marks := []FundPrice{{Fund: "F", Date: "2026-07-10", Price: 110000}}
	flows := FundFlowsOne(ops, marks, "F", "2026-07-10")
	if len(flows) != 2 {
		t.Fatalf("очікували купівлю й термінал, маємо %+v", flows)
	}
	if flows[1].Amount != 110000 {
		t.Errorf("термінал мав іти за позначкою, маємо %d", flows[1].Amount)
	}
}

// Свіжість ціни. Дірка, знайдена вживу: ReturnMeasurable дивиться, чи
// розійшлась вартість із собівартістю, а розійшлась вона НАЗАВЖДИ після
// першої ж позначки — тож піврічної давнини ціна проходить охорону так
// само, як учорашня.
func TestFundPositionPriceStale(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-01-10", Fund: "F", Kind: FundBuy, Qty: 100, Amount: 100000, Currency: "UAH"},
	}
	fresh := FundPositions(ops, []FundPrice{{Fund: "F", Date: "2026-07-01", Price: 110000}})["F"]
	if fresh.PriceStale("2026-07-10") {
		t.Error("позначці девʼять днів — застарілою вона бути не може")
	}
	if !fresh.PriceStale("2026-09-10") {
		t.Error("за два з половиною місяці позначка мала застаріти")
	}
	// Рівно на порозі ще не застаріла: поріг — «більше ніж», а не «від».
	// 2026-07-01 + 45 днів = 2026-08-15.
	if fresh.PriceStale("2026-08-15") {
		t.Error("рівно на порозі ціна ще не застаріла")
	}
	if !fresh.PriceStale("2026-08-16") {
		t.Error("на день за поріг — уже застаріла")
	}
}

// Порожня позиція мовчить: у нуля сертифікатів ціна ні на що не впливає, і
// нагадувати про неї означало б просити роботу задарма.
func TestFundPositionPriceStaleSilentOnEmptyPosition(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-01-10", Fund: "F", Kind: FundBuy, Qty: 100, Amount: 100000, Currency: "UAH"},
		{Date: "2026-01-11", Fund: "F", Kind: FundSell, Qty: 100, Amount: 100000, Currency: "UAH"},
	}
	if FundPositions(ops, nil)["F"].PriceStale("2027-01-01") {
		t.Error("порожня позиція про застарілу ціну не нагадує")
	}
}

// Межа 30 днів — та, що здивувала вживу.
//
// МілТех мав позначку ціни, вартість розійшлась із собівартістю, тобто
// ReturnMeasurable уже істинна — а на екрані стояла обіцянка. Тримав її
// ІНШИЙ, давніший поріг: 0.7% за девʼять днів в ануалізації дали б 32%,
// тобто арифметику ділення на малий строк. Тест закріплює саме межу, бо
// перемикання на ній виглядає як поломка, якщо не знати, що воно за
// правилом.
func TestFundTotalReturnWaitsForThirtyDays(t *testing.T) {
	const buy = Date("2026-01-10")
	ops := []FundOp{
		{Date: buy, Fund: "F", Kind: FundBuy, Qty: 100, Amount: 100000, Currency: "UAH"},
	}
	marks := []FundPrice{{Fund: "F", Date: "2026-01-11", Price: 100700}}
	// 29 днів — гроші ще не попрацювали, число мовчить.
	if _, ok := FundTotalReturn(ops, marks, "F", "2026-02-08"); ok {
		t.Error("на 29-й день ануалізувати ще нема чого")
	}
	// 31 — уже говорить.
	if _, ok := FundTotalReturn(ops, marks, "F", "2026-02-10"); !ok {
		t.Error("на 31-й день число мало зʼявитись")
	}
}
