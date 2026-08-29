package api

import (
	"net/http"
	"testing"
)

// --- цілі накопичення в журналі рішень ---

// Відкладання на ціль теж стає рядком журналу.
//
// Довід той самий, що вивів туди подушку: гроші пішли ПОВЗ рейтинг, і без
// цього рядка журнал сліпий саме до найчастішого рішення живого портфеля —
// маршрут веде в подушку й цілі всі надходження року, а купівель за той рік
// може не бути жодної.
func TestDecisionRecordedOnGoalFill(t *testing.T) {
	srv, st := testServer(t)
	switchSeed(t, st)
	if resp, body := do(t, "POST", srv.URL+"/api/goals",
		`{"name":"Авто","amount":"20000","currency":"USD"}`,
	); resp.StatusCode != http.StatusCreated {
		t.Fatalf("ціль: %d %s", resp.StatusCode, body)
	}
	if resp, body := do(t, "POST", srv.URL+"/api/goal-ops",
		`{"goal_id":"1","date":"2026-07-01","amount":"12000.00","currency":"UAH","place":"сейф"}`,
	); resp.StatusCode != http.StatusCreated {
		t.Fatalf("рух у ціль: %d %s", resp.StatusCode, body)
	}
	out := decisions(t, srv.URL)
	if len(out.Rows) != 1 {
		t.Fatalf("очікували одне рішення, маємо %d", len(out.Rows))
	}
	r := out.Rows[0]
	// Ref — НАЗВА цілі, а не місце й не id: журнал читає людина, і «Авто»
	// каже про рішення все, тоді як «сейф» відповідає на інше питання.
	if r.Kind != decisionKindGoal || r.Ref != "Авто" {
		t.Errorf("рішення не про ціль або без її назви: %+v", r)
	}
	if r.Amount.Amount != "12000.00" {
		t.Errorf("сума %s, очікували 12000.00", r.Amount.Amount)
	}
	// Обіцянки в цілі немає, і нуль тут — точне твердження, а не «помічник
	// обіцяв 0%». Місця в рейтингу теж немає: вирізка береться ДО нього.
	if r.PromisedPct != 0 || r.RankPos != 0 {
		t.Errorf("цілі приписано обіцянку чи місце в рейтингу: %+v", r)
	}
	if r.TopLabel == "" {
		t.Error("не записано, від чого ці гроші відмовились — без цього рядок мовчить")
	}
}

// Зняття з цілі рядка НЕ дає.
//
// Довід той самий, що в подушки, лише привід протилежний: гроші звідти
// беруть тоді, коли річ КУПЛЕНО, тобто коли ціль досягнута. Записати таке
// рядком «відмовився від 9.4%» означало б назвати покупку авто втраченою
// вигодою.
func TestDecisionNotRecordedOnGoalWithdrawal(t *testing.T) {
	srv, st := testServer(t)
	switchSeed(t, st)
	if resp, body := do(t, "POST", srv.URL+"/api/goals",
		`{"name":"Авто","amount":"20000","currency":"UAH"}`,
	); resp.StatusCode != http.StatusCreated {
		t.Fatalf("ціль: %d %s", resp.StatusCode, body)
	}
	if resp, body := do(t, "POST", srv.URL+"/api/goal-ops",
		`{"goal_id":"1","date":"2026-07-01","amount":"-5000.00","currency":"UAH"}`,
	); resp.StatusCode != http.StatusCreated {
		t.Fatalf("зняття з цілі: %d %s", resp.StatusCode, body)
	}
	if out := decisions(t, srv.URL); len(out.Rows) != 0 {
		t.Errorf("зняття дало рядок журналу: %+v", out.Rows)
	}
}

// Цілі — ТРЕТІЙ знаменник, окремий і від покупок, і від подушки.
//
// Від покупок — бо верхнім рядком рейтингу ціль не стоїть ніколи, і в
// частці «взяв верхній» кожен її рух читався б як порушення дисципліни.
// Від подушки — бо доля різна: матрац тримають, ЩОБ НЕ витратити, а на
// авто збирають, ЩОБ витратити. Спільне число сховало б саме цю різницю.
func TestDecisionsSummaryKeepsGoalsApartFromReserve(t *testing.T) {
	got := summarizeDecisions([]decisionRow{
		{Kind: "bond", RankMode: "plan", RankPos: 1},
		{Kind: decisionKindReserve, TopLabel: "UA0001", ForgonePct: 9.4},
		{Kind: decisionKindGoal, TopLabel: "UA0001", ForgonePct: 8.0},
		{Kind: decisionKindGoal, TopLabel: "UA0001", ForgonePct: 6.0},
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
