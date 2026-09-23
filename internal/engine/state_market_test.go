package engine

import (
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// Стартова ставка прогнозу — зі свіжого аукціону: «1y», інакше строк,
// найближчий до року; короткі й несвіжі розміщення не беруться, а валюта
// без аукціону лишається запасним шляхам.
func TestAuctionRateByCurPicksFreshYearTenor(t *testing.T) {
	today := domain.Date("2026-09-23")
	pts := []store.AuctionPoint{
		{Date: "2026-09-16", Currency: "UAH", Bucket: "6m", Days: 182, IncomeBP: 1500},
		{Date: "2026-09-16", Currency: "UAH", Bucket: "1y", Days: 360, IncomeBP: 1610},
		{Date: "2026-09-09", Currency: "UAH", Bucket: "2y", Days: 720, IncomeBP: 1650},
		// Долара «1y» немає — найближчий до року строк, а не найновіший.
		{Date: "2026-08-20", Currency: "USD", Bucket: "1.5y", Days: 540, IncomeBP: 420},
		{Date: "2026-09-10", Currency: "USD", Bucket: "3y", Days: 1080, IncomeBP: 460},
		{Date: "2026-09-10", Currency: "USD", Bucket: "3m", Days: 90, IncomeBP: 380},
		// Євро — лише застаріле: у мапі його бути не мусить.
		{Date: "2025-06-01", Currency: "EUR", Bucket: "1y", Days: 365, IncomeBP: 320},
	}
	got := auctionRateByCur(pts, today)
	if r := got["UAH"]; r.Pct != 16.1 || r.Date != "2026-09-16" {
		t.Errorf("UAH: %+v, чекали 1y 16,1 %% від 16.09", r)
	}
	if r := got["USD"]; r.Pct != 4.2 || r.Date != "2026-08-20" {
		t.Errorf("USD: %+v, чекали 1.5y 4,2 %% (найближче до року)", r)
	}
	if _, ok := got["EUR"]; ok {
		t.Error("застарілий аукціон EUR не мав потрапити в стартову ставку")
	}

	// Джерело доходить до рядка прогнозу тим самим вибором.
	f := sleeveFactory{in: projectionInput{
		MarketRateByCur: got,
		YieldByCur:      map[string]float64{"UAH": 19, "EUR": 3.1},
		AvgRateByCur:    map[string]float64{"GBP": 5},
	}}
	for cur, want := range map[string]string{
		"UAH": rateFromAuction, "EUR": rateFromPortfolio, "GBP": rateFromDirectory,
	} {
		if _, src, _ := f.startRate(cur); src != want {
			t.Errorf("%s: джерело %q, чекали %q", cur, src, want)
		}
	}
}
