package domain

import (
	"testing"

	money "github.com/Rhymond/go-money"
)

func TestPositions(t *testing.T) {
	bonds := map[string]Bond{
		"UA1": {ISIN: "UA1", Nominal: uah(100000), Maturity: "2027-03-01"},
		"US1": {ISIN: "US1", Nominal: money.New(100000, money.USD), Maturity: "2028-01-15"},
	}
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-12-01", Type: PayCoupon, PerBond: uah(8000)},
		{ISIN: "UA1", PayDate: "2027-03-01", Type: PayRedemption, PerBond: uah(100000)},
	}
	lots := []Lot{
		{ID: 1, ISIN: "UA1", Qty: 5, PricePerBond: uah(99000), BuyDate: "2026-01-01"},
		{ID: 2, ISIN: "UA1", Qty: 5, PricePerBond: uah(97000), BuyDate: "2026-02-01"},
		{ID: 3, ISIN: "US1", Qty: 2, PricePerBond: money.New(99500, money.USD), BuyDate: "2026-03-01"},
	}
	sales := []Sale{{ID: 1, LotID: 2, SaleDate: "2026-06-01", Qty: 5, CleanPerBond: uah(98000), Accrued: uah(0)}}

	pos, err := Positions(bonds, pays, lots, sales, "2026-07-15")
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 2 {
		t.Fatalf("очікували 2 позиції, маємо %d", len(pos))
	}
	ua := pos[0]
	if ua.ISIN != "UA1" || ua.Qty != 5 {
		t.Fatalf("UA1: %+v", ua)
	}
	if ua.Invested.Amount() != 5*99000 { // лот 2 продано повністю
		t.Errorf("вартість входу залишку: %d", ua.Invested.Amount())
	}
	if ua.NextPayDate != "2026-12-01" || ua.NextPayAmt.Amount() != 40000 {
		t.Errorf("наступна виплата: %s %d", ua.NextPayDate, ua.NextPayAmt.Amount())
	}
	if ua.DaysToMat <= 0 {
		t.Errorf("днів до погашення має бути > 0: %d", ua.DaysToMat)
	}
}

// Папір, якого немає в кеші довідника, НЕ має валити весь портфель.
// Так буває, коли папір щойно розміщено, або коли лот прийшов із виписки
// раніше за оновлення НБУ. Раніше це давало помилку на весь /api/summary,
// тобто застосунок ставав непридатним через один невідомий ISIN.
func TestPositionsToleratesUnknownISIN(t *testing.T) {
	lots := []Lot{
		{ID: 1, ISIN: "UA9999999999", Qty: 2, PricePerBond: money.New(100000, money.UAH),
			BuyDate: "2026-07-20", Channel: "inzhur"},
	}
	pos, err := Positions(map[string]Bond{}, nil, lots, nil, "2026-07-22")
	if err != nil {
		t.Fatalf("невідомий папір не мав давати помилку: %v", err)
	}
	if len(pos) != 1 {
		t.Fatalf("позиція мала лишитись видимою, маємо %d", len(pos))
	}
	p := pos[0]
	if !p.Unknown {
		t.Error("позиція мала бути позначена як невідома")
	}
	if p.Qty != 2 || p.Invested.Amount() != 200000 {
		t.Errorf("кількість і вкладене відомі з лота: %+v", p)
	}
	// Номінал і виплати нам невідомі — вони мають бути НУЛЬОВІ, а не nil:
	// далі ці суми складаються й конвертуються, і nil там дав би паніку.
	if p.Nominal == nil || p.Nominal.Amount() != 0 {
		t.Errorf("номінал мав бути нульовим, маємо %v", p.Nominal)
	}
	if p.NextPayAmt == nil || p.NextPayAmt.Amount() != 0 {
		t.Errorf("наступна виплата мала бути нульовою, маємо %v", p.NextPayAmt)
	}
}
