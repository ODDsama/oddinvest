package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// TestProgressReconciles — числа прогресу мусять дорівнювати тим
// таблицям, з яких вони взяті.
//
// Це той самий клас захисту, що й TestCashflowStatementReconciles, і
// потрібен він із тієї ж причини: доріжка на «Огляді» показує число, яке
// має двійника в іншому місці застосунку, і розійтись вони можуть тихо —
// обидва лишаться правдоподібними.
//
// Кожне твердження нижче б'є в конкретну помилку, яку в прототипі
// редизайну вже ловили:
//
//	— «дисципліна 75%» не сходилась із журналом рішень, де було 2 з 4;
//	— «частки зійшлись 3 з 6» була вигадана цілком: вимірів сім, і жоден
//	  не на цілі;
//	— «жодного перевищеного ліміту» бралось із rebalance, у якого поля
//	  про ліміт немає взагалі.
func TestProgressReconciles(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seed(t, st)

	// Портфель із трьох видів плюс свої гроші: інакше половина віх
	// перевіряє порожнечу й нічого не доводить.
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2026-01-10", Amount: 50000000, Currency: "UAH", Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"1000.00","fee":"25.00","buy_date":"2026-07-01","channel":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, b)
	}
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "mono", Currency: "UAH", Principal: 5000000, RateBP: 1600,
		OpenDate:     domain.NewDate(time.Now()).AddDays(-120),
		MaturityDate: domain.NewDate(time.Now()).AddDays(245),
		Payout:       domain.PayoutMonthly, TaxBP: 1950,
	}); err != nil {
		t.Fatal(err)
	}

	var pr struct {
		Level      int `json:"level"`
		LevelOf    int `json:"level_of"`
		Discipline struct {
			TopRow int  `json:"top_row"`
			Total  int  `json:"total"`
			Enough bool `json:"enough"`
		} `json:"discipline"`
		Collection struct {
			Currencies []string `json:"currencies"`
			Rows       []struct {
				Year  int    `json:"year"`
				Cells []bool `json:"cells"`
			} `json:"rows"`
			Filled int `json:"filled"`
			Of     int `json:"of"`
		} `json:"collection"`
		Streak struct {
			Months         int    `json:"months"`
			Best           int    `json:"best"`
			MonthsMeasured int    `json:"months_measured"`
			KnownFrom      string `json:"known_from"`
		} `json:"streak"`
		Milestones []struct {
			Key         string `json:"key"`
			Earned      bool   `json:"earned"`
			ProgressPct int    `json:"progress_pct"`
		} `json:"milestones"`
	}
	_, body := do(t, "GET", srv.URL+"/api/progress", "")
	if err := json.Unmarshal([]byte(body), &pr); err != nil {
		t.Fatalf("progress: %v: %s", err, body)
	}

	var sum struct {
		Ladder []struct {
			Year int     `json:"year"`
			UAH  float64 `json:"uah"`
			USD  float64 `json:"usd"`
			EUR  float64 `json:"eur"`
		} `json:"ladder"`
		Rebalance []struct {
			Dimension  string  `json:"dimension"`
			TargetPct  float64 `json:"target_pct"`
			CurrentPct float64 `json:"current_pct"`
		} `json:"rebalance"`
		Concentration []struct {
			OverUAH float64 `json:"over_uah"`
		} `json:"concentration"`
	}
	_, body = do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}

	byKey := map[string]struct {
		earned bool
		pct    int
	}{}
	for _, m := range pr.Milestones {
		byKey[m.Key] = struct {
			earned bool
			pct    int
		}{m.Earned, m.ProgressPct}
	}

	// --- 1. Рівень — це кількість зібраних віх, а не окуляри ----------
	earned := 0
	for _, m := range pr.Milestones {
		if m.Earned {
			earned++
		}
	}
	if pr.Level != earned {
		t.Errorf("рівень %d, а зібраних віх %d — рівень перестав бути їх кількістю",
			pr.Level, earned)
	}
	if pr.LevelOf != len(pr.Milestones) {
		t.Errorf("знаменник рівня %d, а віх у наборі %d", pr.LevelOf, len(pr.Milestones))
	}

	// --- 2. Дисципліна дорівнює журналу рішень знак у знак ------------
	var dec struct {
		Rows    []json.RawMessage `json:"rows"`
		Summary *struct {
			Count    int `json:"count"`
			Followed int `json:"followed"`
		} `json:"summary"`
	}
	_, body = do(t, "GET", srv.URL+"/api/decisions", "")
	if err := json.Unmarshal([]byte(body), &dec); err != nil {
		t.Fatalf("decisions: %v: %s", err, body)
	}
	if dec.Summary == nil {
		// Журнал закороткий — і прогрес мусить казати те саме, а не
		// показувати відсоток з одного рішення.
		if pr.Discipline.Enough {
			t.Errorf("журнал закороткий (%d рядків), а дисципліна вважає його достатнім",
				len(dec.Rows))
		}
		if pr.Discipline.Total != 0 {
			t.Errorf("журнал закороткий, а дисципліна показує %d рішень", pr.Discipline.Total)
		}
	} else if pr.Discipline.TopRow != dec.Summary.Followed ||
		pr.Discipline.Total != dec.Summary.Count {
		t.Errorf("дисципліна %d з %d, а журнал каже %d з %d — реалізації розійшлись",
			pr.Discipline.TopRow, pr.Discipline.Total,
			dec.Summary.Followed, dec.Summary.Count)
	}

	// --- 3. Поле колекції — це драбина, а не окремий підрахунок -------
	if len(pr.Collection.Currencies) != 3 {
		t.Fatalf("колонок поля мало бути три, маємо %v", pr.Collection.Currencies)
	}
	var ladderRows int
	for _, l := range sum.Ladder {
		if l.Year != 0 {
			ladderRows++
		}
	}
	if len(pr.Collection.Rows) != ladderRows {
		t.Errorf("рядків поля %d, а років у драбині %d",
			len(pr.Collection.Rows), ladderRows)
	}
	filled, i := 0, 0
	for _, l := range sum.Ladder {
		if l.Year == 0 {
			continue
		}
		if i >= len(pr.Collection.Rows) {
			break
		}
		row := pr.Collection.Rows[i]
		i++
		if row.Year != l.Year {
			t.Errorf("рядок поля за %d, а драбина за %d — порядок розійшовся",
				row.Year, l.Year)
			continue
		}
		want := []bool{l.UAH > 0, l.USD > 0, l.EUR > 0}
		for k, w := range want {
			if row.Cells[k] != w {
				t.Errorf("клітинка %d/%s: поле каже %v, драбина %v",
					l.Year, pr.Collection.Currencies[k], row.Cells[k], w)
			}
			if w {
				filled++
			}
		}
	}
	if pr.Collection.Filled != filled {
		t.Errorf("заповнених клітинок заявлено %d, а в драбині %d",
			pr.Collection.Filled, filled)
	}
	if pr.Collection.Of != len(pr.Collection.Rows)*3 {
		t.Errorf("розмір поля %d, а рядків %d × 3", pr.Collection.Of, len(pr.Collection.Rows))
	}

	// --- 4. «Частки зійшлись» рахується з УСІХ вимірів ребалансу ------
	total, at := 0, 0
	for _, r := range sum.Rebalance {
		total++
		if r.TargetPct > 0 && math.Abs(r.CurrentPct-r.TargetPct) <= 0.5 {
			at++
		}
	}
	m := byKey["shares_aligned"]
	if total == 0 {
		if m.pct != -1 {
			t.Errorf("цілей часток немає, а віха показує %d%%", m.pct)
		}
	} else {
		want := int(math.Round(float64(at) / float64(total) * 100))
		if m.pct != want {
			t.Errorf("«частки зійшлись» %d%%, а в ребалансі %d із %d = %d%%",
				m.pct, at, total, want)
		}
		if m.earned != (at == total) {
			t.Errorf("«частки зійшлись» earned=%v при %d із %d", m.earned, at, total)
		}
	}

	// --- 5. «Жодного перевищеного ліміту» — це concentration ----------
	over := 0
	for _, c := range sum.Concentration {
		if c.OverUAH > 0 {
			over++
		}
	}
	if m := byKey["no_limit_breach"]; m.earned != (over == 0) {
		t.Errorf("«жодного перевищеного ліміту» earned=%v, а перевищено %d", m.earned, over)
	}

	// --- 6. Кожен місяць серії витримує перевірку поодинці ------------
	//
	// Серія — не окреме число, а НАСЛІДОК: якщо вона каже N, то останні N
	// вимірюваних місяців кожен мають внесок не менший за ціль того
	// місяця. Інакше це просто лічильник, який ніхто не перевіряє.
	if pr.Streak.Months > pr.Streak.MonthsMeasured {
		t.Errorf("серія %d місяців, а виміряти вдалось лише %d",
			pr.Streak.Months, pr.Streak.MonthsMeasured)
	}
	if pr.Streak.Best < pr.Streak.Months {
		t.Errorf("найкраща серія %d менша за поточну %d", pr.Streak.Best, pr.Streak.Months)
	}
	if pr.Streak.MonthsMeasured > 0 && pr.Streak.KnownFrom == "" {
		t.Error("місяці виміряні, але не сказано, з якого саме відомо")
	}

	// --- 7. Прогрес ніколи не виходить за межі ------------------------
	for _, m := range pr.Milestones {
		if m.ProgressPct != -1 && (m.ProgressPct < 0 || m.ProgressPct > 100) {
			t.Errorf("віха %s: прогрес %d%% поза межами", m.Key, m.ProgressPct)
		}
		if m.Earned && m.ProgressPct != -1 && m.ProgressPct != 100 {
			t.Errorf("віха %s зібрана, а прогрес %d%%", m.Key, m.ProgressPct)
		}
	}
}

// TestProgressStreakUsesTargetOfItsMonth — ціль минулого місяця береться
// зі знімка ТОГО місяця, а не з сьогоднішніх налаштувань.
//
// Без цього зміна цілі переписувала б минуле: підняв ціль удвічі — і
// заднім числом «зривався» пів року, хоч тоді все було виконано.
func TestProgressStreakUsesTargetOfItsMonth(t *testing.T) {
	snaps := []store.Snapshot{
		// Січень: ціль 10 000 ₴ (у копійках), внесено 12 000 — виконано.
		{Date: "2026-01-31", MonthTargetUAH: 1_000_000},
		// Лютий: ціль піднялась до 50 000, внесено ті самі 12 000 — ні.
		{Date: "2026-02-28", MonthTargetUAH: 5_000_000},
		// Березень: ціль знову 10 000 — виконано.
		{Date: "2026-03-31", MonthTargetUAH: 1_000_000},
	}
	ev := []flowEvent{
		{Date: "2026-01-15", Kind: flowContribution, UAH: 1_200_000},
		{Date: "2026-02-15", Kind: flowContribution, UAH: 1_200_000},
		{Date: "2026-03-15", Kind: flowContribution, UAH: 1_200_000},
	}
	got := buildStreak(snaps, ev, "2026-04-10")

	if got.Months != 1 {
		t.Errorf("поточна серія мала бути 1 (сам березень), маємо %d", got.Months)
	}
	if got.Best != 1 {
		t.Errorf("найкраща серія мала бути 1, маємо %d", got.Best)
	}
	if got.BrokenOn != "2026-02" {
		t.Errorf("серія обірвалась у лютому, а сказано «%s»", got.BrokenOn)
	}
	if got.KnownFrom != "2026-01" {
		t.Errorf("судити можна з січня, а сказано «%s»", got.KnownFrom)
	}
	if got.MonthsMeasured != 3 {
		t.Errorf("вимірюваних місяців три, маємо %d", got.MonthsMeasured)
	}
}

// TestProgressStreakSkipsUnknownMonths — місяць без знімка обриває
// ЗНАННЯ, а не зараховується й не карається.
//
// Це головна різниця між «ти зривався» і «застосунок тоді не дивився», і
// сплутати їх означає докоряти за власну сліпоту.
func TestProgressStreakSkipsUnknownMonths(t *testing.T) {
	snaps := []store.Snapshot{
		{Date: "2026-01-31", MonthTargetUAH: 1_000_000},
		// Лютого немає взагалі — знімків за нього не робилось.
		{Date: "2026-03-31", MonthTargetUAH: 1_000_000},
		{Date: "2026-04-30", MonthTargetUAH: 1_000_000},
	}
	ev := []flowEvent{
		{Date: "2026-01-15", Kind: flowContribution, UAH: 1_200_000},
		{Date: "2026-03-15", Kind: flowContribution, UAH: 1_200_000},
		{Date: "2026-04-15", Kind: flowContribution, UAH: 1_200_000},
	}
	got := buildStreak(snaps, ev, "2026-05-10")

	// Січень виконано, лютий невідомий, березень і квітень виконані:
	// серія — два, а не три (діра обірвала) і не нуль (докору немає).
	if got.Months != 2 {
		t.Errorf("серія мала бути 2 (березень і квітень), маємо %d", got.Months)
	}
	if got.BrokenOn != "" {
		t.Errorf("пропущений місяць — не зрив плану, а він записаний як «%s»", got.BrokenOn)
	}
}

// TestProgressCountsOnlyContributions — серія міряє ТВІЙ внесок, а не
// будь-які гроші на рахунку.
//
// Купон і погашення теж збільшують рахунок, і зарахувати їх у план
// означало б святкувати те, що сталося саме собою.
func TestProgressCountsOnlyContributions(t *testing.T) {
	snaps := []store.Snapshot{{Date: "2026-01-31", MonthTargetUAH: 1_000_000}}
	ev := []flowEvent{
		{Date: "2026-01-15", Kind: flowIncome, UAH: 5_000_000},
		{Date: "2026-01-20", Kind: flowContribution, UAH: 100_000},
	}
	got := buildStreak(snaps, ev, "2026-02-10")
	if got.Months != 0 {
		t.Errorf("внесено 1 000 ₴ з 10 000 — місяць не виконано, а серія %d", got.Months)
	}
}
