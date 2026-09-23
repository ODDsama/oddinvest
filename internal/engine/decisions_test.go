package engine

import (
	"testing"
)

// Подушка не входить у знаменник дисципліни.
//
// «Слідую помічнику» означає «взяв те, що стояло верхнім»; подушка верхнім
// не стоїть НІКОЛИ. Потрапивши в Count, кожен її рух тягнув би Followed
// донизу й перетворив би метрику дисципліни на метрику «як часто я
// поповнюю резерв».
func TestDecisionsSummaryKeepsReserveApart(t *testing.T) {
	got := SummarizeDecisions([]DecisionRow{
		{Kind: "bond", RankMode: "plan", RankPos: 1},
		{Kind: "bond", RankMode: "plan", RankPos: 1},
		{Kind: DecisionKindReserve, TopLabel: "UA0001", ForgonePct: 9.4},
		{Kind: DecisionKindReserve, TopLabel: "UA0001", ForgonePct: 8.6},
	})
	if got.Count != 2 || got.Followed != 2 {
		t.Errorf("покупок %d, за верхнім %d — чекали 2/2: подушка сюди не входить",
			got.Count, got.Followed)
	}
	if got.ReserveCount != 2 {
		t.Errorf("рухів у подушку %d, чекали 2", got.ReserveCount)
	}
	if got.ReserveForgonePctAvg != 9 {
		t.Errorf("доступне давало %.2f, чекали 9 ((9.4+8.6)/2)", got.ReserveForgonePctAvg)
	}
	// Режими подушки не стосуються: рух у матрац не залежить від того, чим
	// упорядкований рейтинг.
	for _, m := range got.ByMode {
		if m.Count != 2 {
			t.Errorf("режим %q дістав %d рішень, чекали 2", m.Mode, m.Count)
		}
	}
}
