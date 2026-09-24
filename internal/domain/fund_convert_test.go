package domain

import "testing"

// Конвертація фонду переносить собівартість, а не обнуляє прибуток.
//
// Продаж-нога пари знімала зі старого фонду собівартість проданої частки,
// а купівля-нога клала в новий РИНКОВУ суму. Прибуток, що наріс у старому
// фонді (258,95 ₴ на бойових даних), зникав з усіх показників: у новому
// фонді він ставав собівартістю, а realized на парі не пишеться. Рішення
// власника (2026-09-24): собівартість переходить у новий фонд, прибуток
// лишається нереалізованим — і видимим.
func TestFundConversionCarriesCost(t *testing.T) {
	ops := []FundOp{
		{ID: 1, Date: "2025-01-10", Fund: "Житній", Kind: FundBuy, Qty: 10, Amount: 1_000_00, Currency: "UAH"},
		// Купівля-нога записана раніше за продаж-ногу — порядок у журналі
		// не гарантований, тож і правило від нього не залежить.
		{ID: 3, Date: "2025-09-01", Fund: "Ocean", Kind: FundBuy, Qty: 5, Amount: 1_258_95, Currency: "UAH", PairID: 2},
		{ID: 2, Date: "2025-09-01", Fund: "Житній", Kind: FundSell, Qty: 10, Amount: 1_258_95, Currency: "UAH", PairID: 3},
		{ID: 4, Date: "2026-03-01", Fund: "Ocean", Kind: FundSell, Qty: 5, Amount: 1_400_00, Tax: 92_00, Currency: "UAH"},
	}
	pos := FundPositions(ops[:3], nil)
	if got := pos["Ocean"].CostBasis; got != 1_000_00 {
		t.Errorf("собівартість Ocean %d, чекали 1 000,00 — успадковану від Житнього", got)
	}
	if got := pos["Житній"].CostBasis + pos["Житній"].Realized + pos["Ocean"].Realized; got != 0 {
		t.Errorf("на конвертації не мало бути ні залишку собівартості, ні realized: %d", got)
	}
	// Податкова база наступного продажу — від успадкованої собівартості.
	sales := FundSales(ops)
	if len(sales) != 1 || sales[0].Gain != 400_00 {
		t.Errorf("продаж Ocean: %+v, чекали один рядок з прибутком 400,00 (1 400 − 1 000)", sales)
	}
}

// Нові гроші в конвертації — лише доплата понад виручку продаж-ноги.
func TestFundBuyNewCountsOnlyTopUp(t *testing.T) {
	ops := []FundOp{
		{ID: 2, Kind: FundSell, Amount: 9_193_00, Tax: 0, Currency: "UAH", PairID: 3},
		{ID: 3, Kind: FundBuy, Amount: 9_500_00, Currency: "UAH", PairID: 2},
		{ID: 4, Kind: FundBuy, Amount: 1_000_00, Currency: "UAH"},
	}
	if got := FundBuyNew(ops[1], ops); got != 307_00 {
		t.Errorf("купівля-нога: нових %d, чекали 307,00 доплати", got)
	}
	if got := FundBuyNew(ops[2], ops); got != 1_000_00 {
		t.Errorf("звичайна купівля: нових %d, чекали всю суму", got)
	}
}
