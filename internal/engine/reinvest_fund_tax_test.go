package engine

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/state"
)

// Податок на дивіденди фонду береться ОДИН раз. YieldNetPct — виміряна
// дохідність уже ПІСЛЯ податку (FundPositionRow), а ранжування ще й
// проганяло її через NetOfTax: 10% нетто ставали 8,05% «після податку»,
// і фонд опускався в рейтингу на податок, якого вдруге ніхто не бере.
// Обіцяна ставка (ExpectedPct) — брутто, їй податок потрібен.
func TestFundSuggestionTaxedOnce(t *testing.T) {
	e := New(testStore(t), testLogger())
	doc := &state.Doc{Funds: []state.FundPositionRow{
		{Fund: "REIT", Currency: "UAH", LastPrice: 1000, YieldNetPct: 10, IncomeTaxPct: 19.5},
		{Fund: "Обіцяє", Currency: "UAH", LastPrice: 1000, ExpectedPct: 10, IncomeTaxPct: 19.5},
	}}
	sug, err := e.ReinvestSuggestions(context.Background(), time.Now(), doc)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, s := range sug {
		if s.Kind == "fund" {
			got[s.Label] = s.NominalPct
		}
	}
	if math.Abs(got["REIT"]-10) > 0.005 {
		t.Errorf("виміряні 10%% нетто стали %.2f%% — податок узято вдруге", got["REIT"])
	}
	if got["Обіцяє"] >= 10 || got["Обіцяє"] <= 0 {
		t.Errorf("обіцяні 10%% брутто мали стати меншими після податку, маємо %.2f%%", got["Обіцяє"])
	}
}
