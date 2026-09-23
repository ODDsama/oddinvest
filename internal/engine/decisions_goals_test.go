package engine

import (
	"testing"
)

// Цілі — ТРЕТІЙ знаменник, окремий і від покупок, і від подушки.
//
// Від покупок — бо верхнім рядком рейтингу ціль не стоїть ніколи, і в
// частці «взяв верхній» кожен її рух читався б як порушення дисципліни.
// Від подушки — бо доля різна: матрац тримають, ЩОБ НЕ витратити, а на
// авто збирають, ЩОБ витратити. Спільне число сховало б саме цю різницю.
func TestDecisionsSummaryKeepsGoalsApartFromReserve(t *testing.T) {
	got := SummarizeDecisions([]DecisionRow{
		{Kind: "bond", RankMode: "plan", RankPos: 1},
		{Kind: DecisionKindReserve, TopLabel: "UA0001", ForgonePct: 9.4},
		{Kind: DecisionKindGoal, TopLabel: "UA0001", ForgonePct: 8.0},
		{Kind: DecisionKindGoal, TopLabel: "UA0001", ForgonePct: 6.0},
	})
	if got.Count != 1 || got.Followed != 1 {
		t.Errorf("покупок %d, за верхнім %d — чекали 1/1: ні подушка, ні цілі сюди не входять",
			got.Count, got.Followed)
	}
	if got.ReserveCount != 1 {
		t.Errorf("рухів у подушку %d, чекали 1 — цілі до неї не долились", got.ReserveCount)
	}
	if got.GoalCount != 2 {
		t.Errorf("рухів у цілі %d, чекали 2", got.GoalCount)
	}
	if got.GoalForgonePctAvg != 7 {
		t.Errorf("доступне давало %.2f, чекали 7 ((8+6)/2)", got.GoalForgonePctAvg)
	}
	// Режими цілей не стосуються: рух повз рейтинг не залежить від того,
	// чим той упорядкований.
	for _, m := range got.ByMode {
		if m.Count != 1 {
			t.Errorf("режим %q дістав %d рішень, чекали 1", m.Mode, m.Count)
		}
	}
}
