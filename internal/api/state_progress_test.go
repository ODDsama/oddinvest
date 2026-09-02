package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
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
		Level      int    `json:"level"`
		LevelOf    int    `json:"level_of"`
		NextKey    string `json:"next_key"`
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
			Left        string `json:"left"`
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

	// --- 8. «Лишилось» є рівно там, де є відстань ---------------------
	//
	// Правило двобічне навмисно. Порожнє «лишилось» у вимірної незібраної
	// віхи — це мовчання там, де відповідь відома; непорожнє в зібраної
	// чи невимірної — вигадана відстань. Обидва боки ловляться однією
	// перевіркою, бо межа тут одна.
	for _, m := range pr.Milestones {
		measurable := !m.Earned && m.ProgressPct >= 0
		if measurable && m.Left == "" {
			t.Errorf("віха %s незібрана й вимірна (%d%%), а «лишилось» порожнє",
				m.Key, m.ProgressPct)
		}
		if !measurable && m.Left != "" {
			t.Errorf("віха %s (earned=%v, %d%%) каже «%s» — відстані в неї немає",
				m.Key, m.Earned, m.ProgressPct, m.Left)
		}
	}

	// --- 9. Найближча віха — справді найближча ------------------------
	//
	// Не «якась незібрана»: якщо поруч стоїть незібрана вимірна з більшим
	// відсотком, то названо не ту, і герой «Шляху» веде людину не туди.
	if pr.NextKey == "" {
		for _, m := range pr.Milestones {
			if !m.Earned && m.ProgressPct >= 0 {
				t.Errorf("найближчої не названо, хоч %s стоїть на %d%%",
					m.Key, m.ProgressPct)
			}
		}
	} else {
		next, ok := byKey[pr.NextKey]
		if !ok {
			t.Fatalf("найближчою названо %s, а такої віхи в наборі немає", pr.NextKey)
		}
		if next.earned || next.pct < 0 {
			t.Errorf("найближчою названо %s: earned=%v, прогрес %d%%",
				pr.NextKey, next.earned, next.pct)
		}
		for _, m := range pr.Milestones {
			if !m.Earned && m.ProgressPct > next.pct {
				t.Errorf("найближчою названо %s (%d%%), а %s стоїть на %d%%",
					pr.NextKey, next.pct, m.Key, m.ProgressPct)
			}
		}
	}
}

// TestProgressPicksNearestMeasurable — правило вибору найближчої віхи
// поодинці: фільтр, максимум і розв'язання рівності.
//
// Окремо від TestProgressReconciles, бо на живому портфелі рівність
// відсотків може й не трапитись, а саме вона колись почне стрибати між
// двома віхами при кожному перезавантаженні.
func TestProgressPicksNearestMeasurable(t *testing.T) {
	got := pickNext([]milestone{
		// Зібрана й на ста відсотках — не кандидат узагалі.
		{Key: "done", Earned: true, ProgressPct: 100},
		// Невимірна: відстані немає, хоч віха й незібрана.
		{Key: "blind", ProgressPct: progressNoProgress},
		{Key: "far", ProgressPct: 10},
		// Двоє на однакових 75% — виграє оголошена раніше.
		{Key: "near", ProgressPct: 75},
		{Key: "near2", ProgressPct: 75},
	})
	if got != "near" {
		t.Errorf("найближчою мала бути «near», маємо «%s»", got)
	}

	if got := pickNext([]milestone{
		{Key: "done", Earned: true, ProgressPct: 100},
		{Key: "blind", ProgressPct: progressNoProgress},
	}); got != "" {
		t.Errorf("міряти нічим — найближчої немає, а названо «%s»", got)
	}
}

// TestProgressStreakMarksMatchStreak — смужка місяців і число серії
// мусять бути одним і тим самим, порахованим двічі.
//
// Це той самий клас захисту, що й TestProgressReconciles: два подання
// одного числа розійшлись би тихо — смужка лишилась би правдоподібною.
func TestProgressStreakMarksMatchStreak(t *testing.T) {
	snaps := []store.Snapshot{
		{Date: "2026-01-31", MonthTargetUAH: 1_000_000},
		// Лютого немає взагалі — знімків за нього не робилось.
		{Date: "2026-03-31", MonthTargetUAH: 1_000_000},
		{Date: "2026-04-30", MonthTargetUAH: 1_000_000},
		{Date: "2026-05-31", MonthTargetUAH: 1_000_000},
	}
	ev := []flowEvent{
		{Date: "2026-01-15", Kind: flowContribution, UAH: 1_200_000},
		{Date: "2026-02-15", Kind: flowContribution, UAH: 1_200_000},
		{Date: "2026-04-15", Kind: flowContribution, UAH: 1_200_000},
		{Date: "2026-05-15", Kind: flowContribution, UAH: 1_200_000},
	}
	got := buildStreak(snaps, ev, "2026-06-10")

	// Ряд суцільний: лютий у ньому Є, просто невідомий. Без нього
	// січень і березень стали б сусідами, і серія на смужці вийшла б
	// довшою за ту, яку рахує сам buildStreak.
	wantMonths := []string{"2026-01", "2026-02", "2026-03", "2026-04", "2026-05"}
	if len(got.Marks) != len(wantMonths) {
		t.Fatalf("у смужці %d місяців, а мало бути %d: %+v",
			len(got.Marks), len(wantMonths), got.Marks)
	}
	for i, w := range wantMonths {
		if got.Marks[i].Month != w {
			t.Fatalf("клітинка %d за %s, а мала бути за %s",
				i, got.Marks[i].Month, w)
		}
	}
	if got.Marks[1].Known {
		t.Error("лютий без знімка позначено як відомий")
	}
	if got.Marks[1].ContribUAH != 12000 {
		t.Errorf("внесок лютого %v: він відомий із руху грошей навіть без знімка",
			got.Marks[1].ContribUAH)
	}
	if !got.Marks[2].Known || got.Marks[2].Hit {
		t.Errorf("березень: ціль була, внеску не було — known=%v hit=%v",
			got.Marks[2].Known, got.Marks[2].Hit)
	}
	if got.Marks[0].TargetUAH != 10000 || got.Marks[0].ContribUAH != 12000 {
		t.Errorf("січень: %v із %v — мало бути 12000 із 10000",
			got.Marks[0].ContribUAH, got.Marks[0].TargetUAH)
	}

	// Серія, перерахована зі смужки, дорівнює заявленій.
	streak, best := 0, 0
	for _, mk := range got.Marks {
		if mk.Known && mk.Hit {
			streak++
			if streak > best {
				best = streak
			}
			continue
		}
		streak = 0
	}
	if streak != got.Months {
		t.Errorf("зі смужки серія %d, а заявлено %d", streak, got.Months)
	}
	if best != got.Best {
		t.Errorf("зі смужки найдовша серія %d, а заявлено %d", best, got.Best)
	}

	// Поточний місяць у смужку не входить: він ще не закінчився.
	for _, mk := range got.Marks {
		if mk.Month == "2026-06" {
			t.Error("поточний місяць потрапив у смужку")
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

// TestProgressLifeSkipsPrincipal — оплачені дні рахуються лише із
// ЗАРОБЛЕНОГО: погашення номіналу — flowIncome для виписки, але для
// прогресу воно Principal, і рахувати його означало б оплатити роки, яких
// портфель не заробляв. Внески й покупки не входять узагалі.
func TestProgressLifeSkipsPrincipal(t *testing.T) {
	ev := []flowEvent{
		{Date: "2026-03-01", Kind: flowContribution, UAH: 10_000_000},
		{Date: "2026-04-01", Kind: flowIncome, UAH: 500_000},                     // купон 5 000
		{Date: "2026-05-01", Kind: flowIncome, UAH: 20_000_000, Principal: true}, // погашення
		{Date: "2026-06-01", Kind: flowIncome, UAH: 700_000},                     // відсотки 7 000
		{Date: "2026-06-02", Kind: flowPurchase, UAH: -300_000},
	}
	// 30 000 ₴/міс → 1 000 ₴ на день; зароблено 12 000 → 12 днів.
	life := buildLife(ev, 30_000)
	if life == nil {
		t.Fatal("витрати задані — Life мав бути")
	}
	if life.IncomeUAH != 12_000 || life.PerDayUAH != 1_000 || life.Days != 12 {
		t.Errorf("Life = %+v, чекали 12 000 ₴ / 1 000 на день / 12 днів", *life)
	}
	if life.Since != "2026-04-01" {
		t.Errorf("Since = %q, чекали дату першого купона", life.Since)
	}
	// Поріг у 10 днів пройдено 1 червня (5 000 + 7 000 ≥ 10 000); погашення
	// в травні його НЕ пройшло, хоч і принесло 200 000.
	if got := lifeCrossedOn(ev, 1_000, 10); got != "2026-06-01" {
		t.Errorf("поріг 10 днів пройдено %q, чекали 2026-06-01", got)
	}
	if got := lifeCrossedOn(ev, 1_000, 30); got != "" {
		t.Errorf("поріг 30 днів не пройдено, а дата %q", got)
	}
	if buildLife(ev, 0) != nil {
		t.Error("без витрат Life мав мовчати")
	}
}

// TestProgressDebtMilestonesSilentWithoutDebt — на портфелі без боргу всі
// пʼять віх боргу невимірні, а не незібрані: «15 із 21» у людини, яка
// ніколи не була винна, читалось би як докір за те, чого не було.
func TestProgressDebtMilestonesSilentWithoutDebt(t *testing.T) {
	ms := debtMilestones(&state.Doc{}, &sources{}, nil, "2026-07-15")
	if len(ms) != 5 {
		t.Fatalf("віх боргу %d, чекали 5", len(ms))
	}
	for _, m := range ms {
		if m.Earned || m.ProgressPct != progressNoProgress || m.Left != "" {
			t.Errorf("%s без боргу мала мовчати: %+v", m.Key, m)
		}
	}
}

// TestProgressNetWorthDateSkipsUnknownZeros — нуль у старому знімку
// означає «тоді не рахували» (міграція 0048), а не «чистий капітал був
// нулем»: дата виходу з мінуса береться з першого ДОДАТНОГО після
// відʼємного, і нулі між ними не є ні тим, ні іншим.
func TestProgressNetWorthDateSkipsUnknownZeros(t *testing.T) {
	snaps := []store.Snapshot{
		{Date: "2026-03-01", NetWorthUAH: 0},
		{Date: "2026-04-01", NetWorthUAH: -10_000_00},
		{Date: "2026-05-01", NetWorthUAH: 0},
		{Date: "2026-06-01", NetWorthUAH: 5_000_00},
		{Date: "2026-07-01", NetWorthUAH: 6_000_00},
	}
	if got := netWorthPositiveOn(snaps); got != "2026-06-01" {
		t.Errorf("вихід із мінуса %q, чекали 2026-06-01", got)
	}
	// Мінусу не було — нема з чого виходити, дати немає.
	if got := netWorthPositiveOn(snaps[3:]); got != "" {
		t.Errorf("без мінуса в історії дата мала бути порожньою, а є %q", got)
	}
}

// TestProgressCardZeroDatedByCurrentRun — нуль на картці датований
// звіркою, що ПОЧАЛА нинішній невідʼємний відрізок, а не першою
// невідʼємною в історії: картка, що вийшла в плюс і знову провалилась,
// інакше отримала б дату з минулого життя.
func TestProgressCardZeroDatedByCurrentRun(t *testing.T) {
	card := domain.Debt{ID: 7, Kind: domain.DebtCard}
	marks := []domain.DebtMark{
		{DebtID: 7, Date: "2026-01-10", Balance: 100},
		{DebtID: 7, Date: "2026-02-10", Balance: -5_000},
		{DebtID: 7, Date: "2026-03-10", Balance: 200},
		{DebtID: 7, Date: "2026-04-10", Balance: 300},
		{DebtID: 8, Date: "2026-05-10", Balance: -1}, // чужа картка
	}
	if got := zeroRunStart(card, marks, "2026-07-15"); got != "2026-03-10" {
		t.Errorf("початок нинішнього плюса %q, чекали 2026-03-10", got)
	}
	marks = append(marks, domain.DebtMark{DebtID: 7, Date: "2026-05-01", Balance: -1})
	if got := zeroRunStart(card, marks, "2026-07-15"); got != "" {
		t.Errorf("остання звірка в мінусі — дати немає, а є %q", got)
	}
}

// TestProgressEtaNamesItsBasis — дата «за твоїм темпом» ніколи не стоїть
// без основи, і її немає у зібраних віх і там, де темпу не існує.
// Порогам капіталу темп дає ціль внесків: 30 000 ₴/міс — це 1 000 на
// день, і 100 000 з нуля — це 100 днів.
func TestProgressEtaNamesItsBasis(t *testing.T) {
	doc := &state.Doc{MonthTargetUAH: 30_000}
	ms := buildMilestones(doc, &sources{}, nil, nil, streakDoc{}, nil, nil, "2026-07-15")
	byKey := map[string]milestone{}
	for _, m := range ms {
		byKey[m.Key] = m
		if (m.EtaOn == "") != (m.EtaBasis == "") {
			t.Errorf("%s: дата й основа мають іти разом: %q / %q", m.Key, m.EtaOn, m.EtaBasis)
		}
		if m.Earned && m.EtaOn != "" {
			t.Errorf("%s: зібраній вісі дата не належить", m.Key)
		}
	}
	if got := byKey["first_100k"]; got.EtaOn != "2026-10-23" || got.EtaBasis != etaByTarget {
		t.Errorf("first_100k: %q / %q, чекали 2026-10-23 за ціллю внесків", got.EtaOn, got.EtaBasis)
	}
	// Частки й ліміти темпу не мають — дати немає.
	for _, k := range []string{"shares_aligned", "no_limit_breach", "four_kinds"} {
		if byKey[k].EtaOn != "" {
			t.Errorf("%s: темпу немає, а дата %q", k, byKey[k].EtaOn)
		}
	}
	// Без цілі внесків темпу немає й у порогів.
	ms = buildMilestones(&state.Doc{}, &sources{}, nil, nil, streakDoc{}, nil, nil, "2026-07-15")
	for _, m := range ms {
		if m.Key == "first_100k" && m.EtaOn != "" {
			t.Errorf("без цілі внесків дата мала мовчати, а є %q", m.EtaOn)
		}
	}
	// Горизонт: 80 років — не дата.
	if got := etaAfterDays("2026-07-15", 80*365); got != "" {
		t.Errorf("задалекий горизонт мав мовчати, а є %q", got)
	}
}
