package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

type decisionsOut struct {
	Rows []struct {
		Kind        string    `json:"kind"`
		Ref         string    `json:"ref"`
		Amount      moneyJSON `json:"amount"`
		RankMode    string    `json:"rank_mode"`
		PromisedPct float64   `json:"promised_pct"`
		RankPos     int       `json:"rank_pos"`
		TopLabel    string    `json:"top_label"`
		VsTopPP     float64   `json:"vs_top_pp"`
		ActualPct   float64   `json:"actual_pct"`
		DriftPP     float64   `json:"drift_pp"`
		Basis       string    `json:"basis"`
	} `json:"rows"`
	Summary *struct {
		Count      int     `json:"count"`
		Followed   int     `json:"followed"`
		VsTopPPAvg float64 `json:"vs_top_pp_avg"`
		Measured   int     `json:"measured"`
		ByMode     []struct {
			Mode  string `json:"mode"`
			Count int    `json:"count"`
		} `json:"by_mode"`
	} `json:"summary"`
	MinRows int `json:"min_rows"`
}

func decisions(t *testing.T, url string) decisionsOut {
	t.Helper()
	resp, body := do(t, "GET", url+"/api/decisions", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/decisions: %d %s", resp.StatusCode, body)
	}
	var out decisionsOut
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("розбір відповіді: %v (%s)", err, body)
	}
	return out
}

// Порожній журнал — порожній перелік і мовчазне зведення, а не помилка.
func TestDecisionsEmpty(t *testing.T) {
	srv, st := testServer(t)
	switchSeed(t, st)
	out := decisions(t, srv.URL)
	if len(out.Rows) != 0 {
		t.Errorf("рядків мало бути нуль, маємо %d", len(out.Rows))
	}
	if out.Summary != nil {
		t.Error("зведення на порожньому журналі не мало бути")
	}
	if out.MinRows != decisionsMinRows {
		t.Errorf("поріг %d, очікували %d — UI не має вписувати його в себе",
			out.MinRows, decisionsMinRows)
	}
}

// Купівля паперу, який стоїть у рейтингу, фіксується сама — без окремої
// дії й без кнопки.
func TestDecisionRecordedOnLotPurchase(t *testing.T) {
	srv, st := testServer(t)
	switchSeed(t, st)
	if resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000239040","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"Дія"}`,
	); resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання лота: %d %s", resp.StatusCode, body)
	}
	out := decisions(t, srv.URL)
	if len(out.Rows) != 1 {
		t.Fatalf("очікували одне рішення, маємо %d", len(out.Rows))
	}
	r := out.Rows[0]
	if r.Kind != "bond" || r.Ref != "UA4000239040" {
		t.Errorf("рішення не про той папір: %+v", r)
	}
	if r.PromisedPct == 0 {
		t.Error("обіцянка не записалась — знімок рейтингу порожній")
	}
	if r.RankMode == "" {
		t.Error("режим рейтингу не записався: без нього порівняти режими неможливо")
	}
	// Сума — вартість лота, а не кількість і не ціна за папір: питання
	// журналу «скільки грошей пішло за цим рішенням».
	if r.Amount.Amount != "4975.00" {
		t.Errorf("сума %s, очікували 4975.00 (5 × 995)", r.Amount.Amount)
	}
	// Дохідніший із двох паперів стоїть верхнім, тож альтернативи немає.
	if r.RankPos != 1 {
		t.Errorf("позиція в рейтингу %d, очікували першу", r.RankPos)
	}
	if r.TopLabel != "" || r.VsTopPP != 0 {
		t.Errorf("верхній рядок сам собі альтернативою не буває: %+v", r)
	}
}

// Купівля не з верхнього рядка фіксує, чим саме знехтувано й на скільки
// п.п. це різнить дохідність. Саме заради цієї пари журнал і заведено.
func TestDecisionRecordsWhatStoodOnTop(t *testing.T) {
	srv, st := testServer(t)
	switchSeed(t, st)
	if resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"Дія"}`,
	); resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання лота: %d %s", resp.StatusCode, body)
	}
	r := decisions(t, srv.URL).Rows[0]
	if r.RankPos < 2 {
		t.Fatalf("менш дохідний папір мав стояти нижче першого, маємо %d", r.RankPos)
	}
	if r.TopLabel != "UA4000239040" {
		t.Errorf("верхнім мав стояти дохідніший папір, маємо %q", r.TopLabel)
	}
	// Знак: обране менш дохідне за верхній рядок, тож різниця відʼємна.
	// Додатна тут теж законна (у режимі «plan» верхнім буває менш дохідний
	// рядок, що зрушує портфель до цілі), але не в цій фікстурі — тут
	// рейтинг веде саме дохідність.
	if r.VsTopPP >= 0 {
		t.Errorf("різниця з верхнім рядком %.2f — очікували відʼємну", r.VsTopPP)
	}
}

// Папір, якого в рейтингу не було, рішенням не стає: записати його з
// нульовою обіцянкою означало б сказати «помічник обіцяв 0%», хоч він не
// обіцяв нічого.
func TestDecisionSkippedWhenNotSuggested(t *testing.T) {
	srv, _ := testServer(t) // довідник порожній — рейтингу немає взагалі
	// currency доводиться назвати явно: паперу немає в довіднику, і
	// вивести валюту нема звідки. Це і є той самий випадок «купив те,
	// чого помічник не пропонував», який тут і перевіряється.
	if resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000239040","qty":1,"price_per_bond":"995.00","currency":"UAH","buy_date":"2026-07-01","channel":"Дія"}`,
	); resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання лота: %d %s", resp.StatusCode, body)
	}
	if rows := decisions(t, srv.URL).Rows; len(rows) != 0 {
		t.Errorf("рішень мало бути нуль, маємо %d: %+v", len(rows), rows)
	}
}

// Знімок беремо ДО запису. Друга купівля того самого паперу мусить бачити
// рейтинг, у якому першої вже враховано, — але жодна з них не має бачити
// СЕБЕ. Перевіряємо це найпростішим спостережуваним наслідком: обидві
// купівлі записались, і в обох обіцянка ненульова.
func TestDecisionSnapshotTakenBeforeWrite(t *testing.T) {
	srv, st := testServer(t)
	switchSeed(t, st)
	for i := 0; i < 2; i++ {
		if resp, body := do(t, "POST", srv.URL+"/api/lots",
			`{"isin":"UA4000239040","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"Дія"}`,
		); resp.StatusCode != http.StatusCreated {
			t.Fatalf("купівля %d: %d %s", i+1, resp.StatusCode, body)
		}
	}
	rows := decisions(t, srv.URL).Rows
	if len(rows) != 2 {
		t.Fatalf("очікували два рішення, маємо %d", len(rows))
	}
	for i, r := range rows {
		if r.PromisedPct == 0 {
			t.Errorf("рішення %d без обіцянки: знімок узявся після запису", i+1)
		}
	}
}

// Журнал переживає бекап і відновлення. Це не формальність: рейтинг
// учорашнього дня не відтворити НІЯК, тож restore без цієї таблиці стер би
// єдину копію.
func TestDecisionsSurviveBackupRestore(t *testing.T) {
	srv, st := testServer(t)
	switchSeed(t, st)
	if resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"Дія"}`,
	); resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання лота: %d %s", resp.StatusCode, body)
	}
	before := decisions(t, srv.URL).Rows
	if len(before) != 1 {
		t.Fatalf("очікували одне рішення, маємо %d", len(before))
	}

	resp, dump := do(t, "GET", srv.URL+"/api/backup", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("бекап: %d %s", resp.StatusCode, dump)
	}
	if resp, body := do(t, "POST", srv.URL+"/api/restore", dump); resp.StatusCode != http.StatusOK {
		t.Fatalf("відновлення: %d %s", resp.StatusCode, body)
	}
	after := decisions(t, srv.URL).Rows
	if len(after) != 1 {
		t.Fatalf("після відновлення рішень %d, очікували 1", len(after))
	}
	if after[0].Ref != before[0].Ref || after[0].VsTopPP != before[0].VsTopPP {
		t.Errorf("рішення змінилось після відновлення: було %+v, стало %+v",
			before[0], after[0])
	}
}

// --- подушка ---

// Рух у подушку теж стає рядком журналу, і несе те, від чого гроші
// відмовились.
//
// Доти журнал був сліпий саме до цього рішення: знімок шукав куплене В
// рейтингу, а подушка в ньому не стоїть — вирізка на неї береться ДО
// ранжування. На живому портфелі це найчастіше рішення взагалі.
func TestDecisionRecordedOnReserveFill(t *testing.T) {
	srv, st := testServer(t)
	switchSeed(t, st)
	if resp, body := do(t, "POST", srv.URL+"/api/reserve",
		`{"date":"2026-07-01","amount":"12000.00","currency":"UAH","place":"готівка"}`,
	); resp.StatusCode != http.StatusCreated {
		t.Fatalf("рух у резерв: %d %s", resp.StatusCode, body)
	}
	out := decisions(t, srv.URL)
	if len(out.Rows) != 1 {
		t.Fatalf("очікували одне рішення, маємо %d", len(out.Rows))
	}
	r := out.Rows[0]
	if r.Kind != decisionKindReserve || r.Ref != "готівка" {
		t.Errorf("рішення не про подушку: %+v", r)
	}
	if r.Amount.Amount != "12000.00" {
		t.Errorf("сума %s, очікували 12000.00", r.Amount.Amount)
	}
	// Обіцянки в подушки немає, і нуль тут — точне твердження, а не
	// «помічник обіцяв 0%». Місця в рейтингу теж немає.
	if r.PromisedPct != 0 || r.RankPos != 0 {
		t.Errorf("подушці приписано обіцянку чи місце в рейтингу: %+v", r)
	}
	if r.TopLabel == "" {
		t.Error("не записано, від чого ці гроші відмовились — без цього рядок мовчить")
	}
}

// Зняття з подушки рядка НЕ дає: гроші звідти беруть тоді, коли сталось
// те, заради чого її й тримали, і назвати аварію вибором не можна.
func TestDecisionNotRecordedOnReserveWithdrawal(t *testing.T) {
	srv, st := testServer(t)
	switchSeed(t, st)
	if resp, body := do(t, "POST", srv.URL+"/api/reserve",
		`{"date":"2026-07-01","amount":"-5000.00","currency":"UAH","place":"готівка"}`,
	); resp.StatusCode != http.StatusCreated {
		t.Fatalf("зняття з резерву: %d %s", resp.StatusCode, body)
	}
	if out := decisions(t, srv.URL); len(out.Rows) != 0 {
		t.Errorf("зняття дало рядок журналу: %+v", out.Rows)
	}
}

// Подушка не входить у знаменник дисципліни.
//
// «Слідую помічнику» означає «взяв те, що стояло верхнім»; подушка верхнім
// не стоїть НІКОЛИ. Потрапивши в Count, кожен її рух тягнув би Followed
// донизу й перетворив би метрику дисципліни на метрику «як часто я
// поповнюю резерв».
func TestDecisionsSummaryKeepsReserveApart(t *testing.T) {
	got := summarizeDecisions([]decisionRow{
		{Kind: "bond", RankMode: "plan", RankPos: 1},
		{Kind: "bond", RankMode: "plan", RankPos: 1},
		{Kind: decisionKindReserve, TopLabel: "UA0001", ForgonePct: 9.4},
		{Kind: decisionKindReserve, TopLabel: "UA0001", ForgonePct: 8.6},
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
