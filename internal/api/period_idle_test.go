package api

import (
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/engine"
)

// Виручка продажу — не «дохід без діла».
//
// Продаж паперу на 50 000 без нових покупок того місяця доти давав у
// «Підсумку місяця» 50 000 доходу без діла: плюсовий рядок кошика покупок
// перевертався в покупку з мінусом, а IdleIncome читав її як надходження.
func TestPeriodIdleIgnoresSaleProceeds(t *testing.T) {
	rows := []engine.FlowEvent{
		{Date: "2026-08-05", Kind: engine.FlowIncome, UAH: 1_000_00},    // купон
		{Date: "2026-08-20", Kind: engine.FlowPurchase, UAH: 50_000_00}, // продаж
		{Date: "2026-08-25", Kind: engine.FlowPurchase, UAH: -400_00},   // покупка
	}
	income, buys := idleInputs(rows)
	if got := domain.IdleIncome(income, buys); got != 600_00 {
		t.Errorf("дохід без діла %d, чекали 600,00 (купон 1 000 − покупка 400)", got)
	}
}

// Конвертація фонду — продаж і купівля одного дня — не з'їдає доходу без
// діла: переставлені між фондами гроші вже були вкладені.
func TestPeriodIdleNetsFundConversion(t *testing.T) {
	rows := []engine.FlowEvent{
		{Date: "2026-08-05", Kind: engine.FlowIncome, UAH: 1_000_00},
		{Date: "2026-08-20", Kind: engine.FlowPurchase, UAH: 9_193_00},  // продаж-нога
		{Date: "2026-08-20", Kind: engine.FlowPurchase, UAH: -9_193_00}, // купівля-нога
	}
	income, buys := idleInputs(rows)
	if got := domain.IdleIncome(income, buys); got != 1_000_00 {
		t.Errorf("дохід без діла %d, чекали 1 000,00 — конвертація грошей не витрачає", got)
	}
}
