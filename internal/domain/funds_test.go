package domain

import (
	"fmt"
	"math"
	"testing"
)

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
	// Остання виплата 430 чистими, ритм 91 день (квітень→липень), вартість
	// 10 000: 430 × 365/91 ÷ 10 000 ≈ 17.25% річних. До податку вийшло б
	// 20.06% — саме цю фору податок і забирає.
	if y < 17.0 || y > 17.5 {
		t.Errorf("чиста дохідність ≈17.25%%, маємо %.2f%%", y)
	}

	// старі дивіденди поза вікном 365 днів не чіпають ні суму, ні ритм
	old := append([]FundOp{{Date: "2024-01-01", Fund: "F", Kind: FundDividend,
		Amount: 900000, Tax: 0, Currency: "UAH"}}, ops...)
	if y2, _ := DividendYieldNet(old, FundPositions(old)["F"], "2026-07-22"); y2 != y {
		t.Errorf("дивіденд дворічної давнини потрапив у річну дохідність: %.2f vs %.2f", y2, y)
	}
}

// Коротка історія більше не занижує дохідність: показник має міряти
// папір, а не те, скільки місяців він у тебе пролежав.
//
// Приклад із життя: після підчищення журналу в Inzhur REIT лишилась одна
// виплата, і стара формула («сума за 365 днів») дала 0.48% — не тому, що
// фонд погано платить, а тому, що інші одинадцять виплат просто ще не
// настали.
func TestDividendYieldDoesNotPunishShortHistory(t *testing.T) {
	// Той самий місячний фонд: 305 сертифікатів по 11.11, виплата 18.99
	// брутто / 2.66 податку.
	ops := []FundOp{
		{Date: "2026-06-04", Fund: "F", Kind: FundBuy, Qty: 305, Amount: 337787, Currency: "UAH"},
		{Date: "2026-07-10", Fund: "F", Kind: FundDividend, Amount: 1899, Tax: 266, Currency: "UAH"},
	}
	p := FundPositions(ops)["F"]
	y, ok := DividendYieldNet(ops, p, "2026-07-22")
	if !ok {
		t.Fatal("дохідність не порахувалась")
	}
	// 16.33 × 12 ≈ 196 на рік від 3378 вкладених ≈ 5.8%
	if y < 5.5 || y > 6.1 {
		t.Errorf("одна місячна виплата має давати ≈5.8%% річних, маємо %.2f%%", y)
	}

	// Дванадцять таких виплат за рік дають ту саму відповідь — показник
	// не залежить від довжини історії, лише від ритму й останньої суми.
	full := []FundOp{ops[0]}
	for m := 8; m <= 12; m++ {
		full = append(full, FundOp{Date: Date(fmt.Sprintf("2025-%02d-10", m)), Fund: "F",
			Kind: FundDividend, Amount: 1899, Tax: 266, Currency: "UAH"})
	}
	for m := 1; m <= 7; m++ {
		full = append(full, FundOp{Date: Date(fmt.Sprintf("2026-%02d-10", m)), Fund: "F",
			Kind: FundDividend, Amount: 1899, Tax: 266, Currency: "UAH"})
	}
	yFull, _ := DividendYieldNet(full, FundPositions(full)["F"], "2026-07-22")
	if math.Abs(yFull-y) > 0.6 {
		t.Errorf("рік історії дав %.2f%%, а один місяць %.2f%% — показник залежить від довжини історії", yFull, y)
	}
}

// Продано більше, ніж куплено — це не «позиція 0», а неповний журнал, і
// сказати про це має сам домен.
//
// Приклад узятий з життя: у Inzhur REIT виявилось продано 1854 сертифікати
// проти 421 купленого — решта надійшла обміном, якого імпорт не переніс.
// Застосунок показував правдоподібні 305 і мовчав, тож півтори тисячі
// сертифікатів «нізвідки» ніхто не помітив.
func TestSellingMoreThanBoughtIsReported(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-01-01", Fund: "F", Kind: FundBuy, Qty: 44, Amount: 44440, Currency: "UAH"},
		{Date: "2026-02-01", Fund: "F", Kind: FundSell, Qty: 1782, Amount: 1798680, Currency: "UAH"},
		{Date: "2026-03-01", Fund: "F", Kind: FundBuy, Qty: 305, Amount: 337787, Currency: "UAH"},
	}
	p := FundPositions(ops)["F"]
	if p.Qty != 305 {
		t.Errorf("залишок %d, хочемо 305", p.Qty)
	}
	if p.Short != 1738 {
		t.Errorf("нестача %d сертифікатів, хочемо 1738 (1782 продано проти 44 куплених)", p.Short)
	}
	if !p.Inconsistent() {
		t.Error("позиція з нестачею має вважатись суперечливою")
	}

	// Повний журнал не має нічого повідомляти.
	ok := []FundOp{
		{Date: "2026-01-01", Fund: "F", Kind: FundBuy, Qty: 100, Amount: 100000, Currency: "UAH"},
		{Date: "2026-02-01", Fund: "F", Kind: FundSell, Qty: 40, Amount: 44000, Currency: "UAH"},
	}
	if q := FundPositions(ok)["F"]; q.Short != 0 || q.Inconsistent() {
		t.Errorf("повний журнал позначено як суперечливий: Short=%d", q.Short)
	}
}
