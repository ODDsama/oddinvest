package domain

import "testing"

// Історія Inzhur REIT із реальної виписки, зведена до суті: конвертація
// закритих фондів принесла 1738 сертифікатів по 10.00, докупівля 44 по
// 10.10, потім усе продано, потім набрано заново.
func reitOps() []FundOp {
	return []FundOp{
		{Date: "2025-09-04", Fund: "Inzhur REIT", Kind: FundBuy, Qty: 1738, Amount: 1738000, Currency: "UAH"},
		{Date: "2025-09-10", Fund: "Inzhur REIT", Kind: FundBuy, Qty: 44, Amount: 44440, Currency: "UAH"},
		{Date: "2025-10-02", Fund: "Inzhur REIT", Kind: FundDividend, Amount: 14880, Tax: 2083, Currency: "UAH"},
		{Date: "2025-11-05", Fund: "Inzhur REIT", Kind: FundSell, Qty: 1782, Amount: 1798680, Tax: 3735, Currency: "UAH"},
		{Date: "2026-06-04", Fund: "Inzhur REIT", Kind: FundBuy, Qty: 11, Amount: 12078, Currency: "UAH"},
		{Date: "2026-06-05", Fund: "Inzhur REIT", Kind: FundBuy, Qty: 110, Amount: 120782, Currency: "UAH"},
		{Date: "2026-06-19", Fund: "Inzhur REIT", Kind: FundBuy, Qty: 90, Amount: 99970, Currency: "UAH"},
		{Date: "2026-07-01", Fund: "Inzhur REIT", Kind: FundBuy, Qty: 90, Amount: 100040, Currency: "UAH"},
		{Date: "2026-07-10", Fund: "Inzhur REIT", Kind: FundDividend, Amount: 1899, Tax: 266, Currency: "UAH"},
		{Date: "2026-07-16", Fund: "Inzhur REIT", Kind: FundBuy, Qty: 71, Amount: 79096, Currency: "UAH"},
		{Date: "2026-07-20", Fund: "Inzhur REIT", Kind: FundSell, Qty: 72, Amount: 79830, Tax: 178, Currency: "UAH"},
		{Date: "2026-07-21", Fund: "Inzhur REIT", Kind: FundBuy, Qty: 5, Amount: 5556, Currency: "UAH"},
	}
}

func TestFundPositionsFromRealHistory(t *testing.T) {
	p := FundPositions(reitOps())["Inzhur REIT"]
	if p == nil {
		t.Fatal("позиція не зібралась")
	}
	// 1738+44−1782+11+110+90+90+71−72+5
	if p.Qty != 305 {
		t.Errorf("залишок мав бути 305 сертифікатів, маємо %d", p.Qty)
	}
	// остання операція — купівля 5 за 55.56 -> 11.1120 ₴/серт.
	// Ціна в сотих мінорної одиниці, тож 11.1120 ₴ = 111120.
	if p.LastPrice != 111120 {
		t.Errorf("остання ціна мала бути 11.1120 ₴ (111120), маємо %d", p.LastPrice)
	}
	if p.LastPriceDate != "2026-07-21" {
		t.Errorf("дата останньої ціни: %s", p.LastPriceDate)
	}
	// 305 × 11.11 ≈ 3388 ₴
	if v := p.MarketValue(); v < 337000 || v > 340000 {
		t.Errorf("вартість позиції ≈3389 ₴, маємо %.2f", float64(v)/100)
	}
	if p.DividendsGross != 16779 || p.DividendsTax != 2349 {
		t.Errorf("дивіденди: брутто %d, податок %d", p.DividendsGross, p.DividendsTax)
	}
}

// Продаж має зменшувати собівартість пропорційно, а не обнуляти її й не
// лишати недоторканою: інакше після часткового продажу залишок виглядав
// би або безкоштовним, або дорожчим за сплачене.
func TestFundSellReducesCostProportionally(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-01-01", Fund: "F", Kind: FundBuy, Qty: 100, Amount: 100000, Currency: "UAH"},
		{Date: "2026-02-01", Fund: "F", Kind: FundSell, Qty: 40, Amount: 48000, Currency: "UAH"},
	}
	p := FundPositions(ops)["F"]
	if p.Qty != 60 {
		t.Errorf("залишок 60, маємо %d", p.Qty)
	}
	if p.CostBasis != 60000 {
		t.Errorf("собівартість залишку мала стати 600.00, маємо %.2f", float64(p.CostBasis)/100)
	}
	// продано за 480 те, що коштувало 400 -> результат +80
	if p.Realized != 8000 {
		t.Errorf("реалізований результат +80.00, маємо %.2f", float64(p.Realized)/100)
	}
}

// Дохідність рахується ПІСЛЯ податку: дивіденд фонду оподатковується, на
// відміну від купона ОВДП, і порівнювати їх до податку означало б давати
// фонду фору, якої в нього немає.
func TestDividendYieldIsAfterTax(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-01-01", Fund: "F", Kind: FundBuy, Qty: 1000, Amount: 1000000, Currency: "UAH"},
		{Date: "2026-04-01", Fund: "F", Kind: FundDividend, Amount: 50000, Tax: 7000, Currency: "UAH"},
		{Date: "2026-07-01", Fund: "F", Kind: FundDividend, Amount: 50000, Tax: 7000, Currency: "UAH"},
	}
	p := FundPositions(ops)["F"]
	y, ok := DividendYieldNet(ops, p, "2026-07-22")
	if !ok {
		t.Fatal("дохідність не порахувалась")
	}
	// (1000−140) чистими на 10 000 вкладених = 8.6%
	if y < 8.5 || y > 8.7 {
		t.Errorf("чиста дохідність ≈8.6%%, маємо %.2f%%", y)
	}
	// старі дивіденди поза вікном 365 днів не мають рахуватись
	old := append([]FundOp{{Date: "2024-01-01", Fund: "F", Kind: FundDividend,
		Amount: 900000, Tax: 0, Currency: "UAH"}}, ops...)
	if y2, _ := DividendYieldNet(old, FundPositions(old)["F"], "2026-07-22"); y2 != y {
		t.Errorf("дивіденд дворічної давнини потрапив у річну дохідність: %.2f vs %.2f", y2, y)
	}
}
