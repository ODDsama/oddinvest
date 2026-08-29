package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// goalsDoc — зріз документа, потрібний тестам цілей.
type goalsDoc struct {
	CapitalUAH   float64 `json:"capital_uah"`
	NominalUAHEq float64 `json:"nominal_uah_eq"`
	AccountUAH   float64 `json:"account_uah"`
	FundsUAH     float64 `json:"funds_uah"`
	DepositsUAH  float64 `json:"deposits_uah"`
	ReserveUAH   float64 `json:"reserve_uah"`
	GoalsUAH     float64 `json:"goals_uah"`
	NPFUAH       float64 `json:"npf_uah"`
	Liquidity    *struct {
		NowUAH   float64 `json:"now_uah"`
		GoalsUAH float64 `json:"goals_uah"`
	} `json:"liquidity"`
	Goals []struct {
		ID              int64              `json:"id"`
		Name            string             `json:"name"`
		Currency        string             `json:"currency"`
		TargetNative    float64            `json:"target_native"`
		CollectedNative float64            `json:"collected_native"`
		GapNative       float64            `json:"gap_native"`
		TargetUAH       float64            `json:"target_uah"`
		CollectedUAH    float64            `json:"collected_uah"`
		GapUAH          float64            `json:"gap_uah"`
		DonePct         float64            `json:"done_pct"`
		ByCurrency      map[string]float64 `json:"by_currency"`
		FXMixed         bool               `json:"fx_mixed"`
		DueDate         string             `json:"due_date"`
		DoneDate        string             `json:"done_date"`
		RequiredUAH     float64            `json:"required_uah"`
		ActualUAH       float64            `json:"actual_uah"`
		ETADate         string             `json:"eta_date"`
		Behind          bool               `json:"behind"`
	} `json:"goals"`
}

func goalsSummary(t *testing.T, url string) goalsDoc {
	t.Helper()
	var d goalsDoc
	_, body := do(t, "GET", url+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	return d
}

// Капітал складається зі своїх частин ДО КОПІЙКИ, і цілі — одна з них.
//
// Це головний інваріант усієї сутності. Гроші під ціллю існують: вони не
// зникають із капіталу від того, що їх не збираються вкладати. Але поза
// капіталом їх теж не лишиш — тоді відкладання на авто виглядало б як
// втрата, і кожна крива, кожен «Підсумок місяця» й кожна валютна частка
// поїхали б разом із ним.
func TestGoalsAddUpToCapital(t *testing.T) {
	srv, _ := testServer(t)

	if resp, b := do(t, "POST", srv.URL+"/api/goals",
		`{"name":"Авто","amount":"20000","currency":"UAH"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("ціль: %d %s", resp.StatusCode, b)
	}
	for _, body := range []string{
		`{"goal_id":"1","amount":"12000","currency":"UAH","place":"готівка"}`,
		`{"goal_id":"1","amount":"3000","currency":"UAH","place":"сейф"}`,
		// Зняття: журнал нетто, і мінус мусить зменшити суму, а не додатись.
		`{"goal_id":"1","amount":"-2000","currency":"UAH","place":"готівка"}`,
	} {
		if resp, b := do(t, "POST", srv.URL+"/api/goal-ops", body); resp.StatusCode != http.StatusCreated {
			t.Fatalf("рух цілі: %d %s", resp.StatusCode, b)
		}
	}

	d := goalsSummary(t, srv.URL)
	if math.Abs(d.GoalsUAH-13_000) > 0.01 {
		t.Errorf("goals_uah = %.2f, а 12 000 + 3 000 − 2 000 = 13 000", d.GoalsUAH)
	}
	parts := d.NominalUAHEq + d.AccountUAH + d.FundsUAH + d.DepositsUAH +
		d.ReserveUAH + d.GoalsUAH + d.NPFUAH
	if math.Abs(d.CapitalUAH-parts) > 0.01 {
		t.Errorf("capital_uah = %.2f, а сума частин %.2f — цілі випали з капіталу",
			d.CapitalUAH, parts)
	}
	// У ліквідності цілі стоять ОКРЕМИМ рядком: у now_uah їм не місце
	// (на рівності now_uah == account_uah тримається звірка зі звітом про
	// рух коштів), а в locked_uah вони брехали б — ламати нічого не треба.
	if d.Liquidity == nil || math.Abs(d.Liquidity.GoalsUAH-13_000) > 0.01 {
		t.Errorf("liquidity.goals_uah не показує цілей: %+v", d.Liquidity)
	}
	if d.Liquidity != nil && d.Liquidity.NowUAH != d.AccountUAH {
		t.Errorf("now_uah = %.2f, account_uah = %.2f — цілі протекли в доступні гроші",
			d.Liquidity.NowUAH, d.AccountUAH)
	}
}

// Ціль у доларах, гроші в гривні: зібране міряється СЬОГОДНІШНІМ курсом.
//
// І головне — курс рухає розрив БЕЗ ЖОДНОГО РУХУ В ЖУРНАЛІ. Це не артефакт
// розрахунку, а те, що з доларовою ціллю й гривневими заощадженнями
// відбувається насправді: девальвація відкидає тебе назад, і застосунок
// мусить це показати, а не малювати прогрес, якого немає.
func TestGoalInUSDMeasuredAtTodayRate(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	today := domain.NewDate(time.Now())

	if err := st.SaveRate(ctx, "USD", 40_0000, today); err != nil {
		t.Fatal(err)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/goals",
		`{"name":"Авто","amount":"20000","currency":"USD"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("ціль: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/goal-ops",
		`{"goal_id":"1","amount":"200000","currency":"UAH","place":"готівка"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("рух: %d %s", resp.StatusCode, b)
	}

	d := goalsSummary(t, srv.URL)
	if len(d.Goals) != 1 {
		t.Fatalf("цілей у документі: %d", len(d.Goals))
	}
	g := d.Goals[0]
	if g.Currency != "USD" || math.Abs(g.TargetNative-20_000) > 0.01 {
		t.Fatalf("ціль поїхала: %+v", g)
	}
	// 200 000 ₴ ÷ 40 = $5 000; лишається $15 000.
	if math.Abs(g.CollectedNative-5_000) > 0.01 || math.Abs(g.GapNative-15_000) > 0.01 {
		t.Errorf("зібрано %.2f $, розрив %.2f $ — чекали 5 000 і 15 000",
			g.CollectedNative, g.GapNative)
	}
	// Відсоток рахується в НАТИВНІЙ валюті: у гривневій він ріс би сам від
	// девальвації — та сама сума в шухляді, більше гривень, «ближче до
	// цілі», при тому що авто подорожчало рівно на стільки ж.
	if math.Abs(g.DonePct-25) > 0.01 {
		t.Errorf("done_pct = %.2f, а $5 000 з $20 000 це 25%%", g.DonePct)
	}
	// Гроші лежать не в тій валюті, у якій названа ціль, — сказано прямо.
	if !g.FXMixed || g.ByCurrency["UAH"] != 200_000 {
		t.Errorf("fx_mixed=%v, by_currency=%+v — валютний розрив не названий",
			g.FXMixed, g.ByCurrency)
	}
	// Гривневі числа — та сама ціль, переведена сьогоднішнім курсом.
	if math.Abs(g.TargetUAH-800_000) > 0.01 || math.Abs(g.CollectedUAH-200_000) > 0.01 {
		t.Errorf("гривневі числа поїхали: ціль %.2f, зібрано %.2f", g.TargetUAH, g.CollectedUAH)
	}

	// Гривня просіла — розрив у доларах виріс, хоч у журналі нічого не було.
	if err := st.SaveRate(ctx, "USD", 50_0000, today); err != nil {
		t.Fatal(err)
	}
	d = goalsSummary(t, srv.URL)
	g = d.Goals[0]
	if math.Abs(g.CollectedNative-4_000) > 0.01 || math.Abs(g.GapNative-16_000) > 0.01 {
		t.Errorf("після девальвації зібрано %.2f $, розрив %.2f $ — чекали 4 000 і 16 000",
			g.CollectedNative, g.GapNative)
	}
	// А гривневе «зібрано» не зрушило: гривень у шухляді стільки ж.
	if math.Abs(g.CollectedUAH-200_000) > 0.01 {
		t.Errorf("гривневе зібрано поїхало на %.2f — а гривень стільки ж", g.CollectedUAH)
	}
}

// Ціль без дедлайну не має ні потрібного темпу, ні відставання.
//
// Це законний стан, а не брак даних: «збираю на будинок, коли збереться —
// тоді й куплю». Питання «чи встигаю» такій цілі не ставлять, і вигадана
// відповідь на нього була б гіршою за її відсутність.
func TestGoalWithoutDueDateHasNoPace(t *testing.T) {
	srv, _ := testServer(t)
	if resp, b := do(t, "POST", srv.URL+"/api/goals",
		`{"name":"Будинок","amount":"1000000","currency":"UAH"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("ціль: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/goal-ops",
		`{"goal_id":"1","amount":"100000","currency":"UAH"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("рух: %d %s", resp.StatusCode, b)
	}
	g := goalsSummary(t, srv.URL).Goals[0]
	if g.RequiredUAH != 0 || g.Behind {
		t.Errorf("ціль без дати дістала темп: required=%.2f behind=%v", g.RequiredUAH, g.Behind)
	}
	// А от «коли збереться» рахується й без дедлайну: це не обіцянка
	// встигнути, а наслідок нинішнього темпу.
	if g.ETADate == "" || g.ActualUAH <= 0 {
		t.Errorf("прогноз за темпом мав бути: eta=%q actual=%.2f", g.ETADate, g.ActualUAH)
	}
}

// Закрита ціль мовчить про розрив.
//
// Розрив рахується від НИНІШНЬОГО залишку, а в закритої цілі він нульовий —
// гроші пішли на річ, заради якої їх і збирали. Без цього правила куплене
// авто показувало б «зібрано 0%, бракує 20 000»: рівно протилежне тому, що
// сталось.
func TestDoneGoalReportsNoGap(t *testing.T) {
	srv, _ := testServer(t)
	today := string(domain.NewDate(time.Now()))

	if resp, b := do(t, "POST", srv.URL+"/api/goals",
		`{"name":"Ремонт","amount":"50000","currency":"UAH"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("ціль: %d %s", resp.StatusCode, b)
	}
	for _, body := range []string{
		`{"goal_id":"1","amount":"50000","currency":"UAH"}`,
		`{"goal_id":"1","amount":"-50000","currency":"UAH"}`,
	} {
		if resp, b := do(t, "POST", srv.URL+"/api/goal-ops", body); resp.StatusCode != http.StatusCreated {
			t.Fatalf("рух: %d %s", resp.StatusCode, b)
		}
	}
	if resp, b := do(t, "PUT", srv.URL+"/api/goals/1",
		`{"name":"Ремонт","amount":"50000","currency":"UAH","done_date":"`+today+`"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("закриття цілі: %d %s", resp.StatusCode, b)
	}

	d := goalsSummary(t, srv.URL)
	if len(d.Goals) != 1 {
		t.Fatalf("закрита ціль зникла зі списку — а журнал під нею і є історія: %+v", d.Goals)
	}
	g := d.Goals[0]
	if g.DoneDate != today {
		t.Errorf("дата закриття не збереглась: %q", g.DoneDate)
	}
	if g.GapNative != 0 || g.GapUAH != 0 || g.Behind {
		t.Errorf("закрита ціль показує розрив: gap=%.2f/%.2f behind=%v",
			g.GapNative, g.GapUAH, g.Behind)
	}
	// Гроші під нею вже витрачені — у капіталі їх немає.
	if math.Abs(d.GoalsUAH) > 0.01 {
		t.Errorf("goals_uah = %.2f — гроші закритої цілі лишились у капіталі", d.GoalsUAH)
	}
}

// Ціль ЗАБИРАЄ гроші з купівельної спроможності, лишаючись у капіталі.
//
// Дві половини одного твердження, і перевіряти їх треба разом: капітал
// росте (гроші є), а база, з якої помічник розкладає надходження, — ні
// (вкладати їх не збираються). Без другої половини реінвест запропонував би
// купити папір за гроші, відкладені на авто.
func TestGoalsStayOutOfBuyingPower(t *testing.T) {
	srv, _ := testServer(t)

	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"target_bonds_pct":"100"}`); resp.StatusCode >= 300 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/goals",
		`{"name":"Авто","amount":"500000","currency":"UAH"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("ціль: %d %s", resp.StatusCode, b)
	}

	before := goalsSummary(t, srv.URL)
	if resp, b := do(t, "POST", srv.URL+"/api/goal-ops",
		`{"goal_id":"1","amount":"100000","currency":"UAH"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("рух: %d %s", resp.StatusCode, b)
	}
	after := goalsSummary(t, srv.URL)

	if math.Abs((after.CapitalUAH-before.CapitalUAH)-100_000) > 0.01 {
		t.Errorf("капітал зріс на %.2f замість 100 000 — відкладене виглядає як втрата",
			after.CapitalUAH-before.CapitalUAH)
	}
	// Розкладка ділить суму по видах від бази «капітал без подушки й без
	// цілей». Якби цілі лишились у базі, той самий надхід розклався б інакше.
	var plan struct {
		AvailUAH float64 `json:"avail_uah"`
		Lines    []struct {
			TotalUAH float64 `json:"total_uah"`
		} `json:"lines"`
	}
	_, body := do(t, "POST", srv.URL+"/api/allocate", `{"amount":"10000"}`)
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		t.Fatalf("allocate: %v: %s", err, body)
	}
	if math.Abs(plan.AvailUAH-10_000) > 0.01 {
		t.Errorf("до розкладки дійшло %.2f з 10 000 — цілі відкусили від надходження",
			plan.AvailUAH)
	}
}
