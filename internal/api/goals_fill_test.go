package api

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/state"
)

// --- стеля цілей: чиста арифметика ---

func goalRow(id int64, name string, gap, required, moved float64, due string) state.Goal {
	return state.Goal{
		ID: id, Name: name, Currency: money.UAH,
		GapUAH: gap, RequiredUAH: required, MovedUAH: moved, DueDate: due,
	}
}

func fillShare(pct float64) *state.SettingsDoc {
	return &state.SettingsDoc{GoalsFillSharePct: &pct}
}

// Доки потрібні темпи влазять у стелю, кожна ціль бере рівно СВОЄ.
//
// Це базовий випадок, і він мусить бути саме таким: стеля не розподіляє
// гроші порівну, вона лише обмежує. Розподіл порівну дав би цілі з
// близьким дедлайном стільки ж, скільки цілі з далеким, — тобто провалив
// би перший дедлайн заради другого.
func TestGoalsFillGivesEachItsOwnRate(t *testing.T) {
	goals := []state.Goal{
		goalRow(1, "Авто", 100_000, 5_000, 0, "2027-06-01"),
		goalRow(2, "Ремонт", 60_000, 3_000, 0, "2027-06-01"),
	}
	// План 100 000 × 20% = 20 000 стелі; потрібно 8 000 разом.
	state.GoalsFill(fillShare(20), goals, 100_000)

	if math.Abs(goals[0].FillNowUAH-5_000) > 0.01 || math.Abs(goals[1].FillNowUAH-3_000) > 0.01 {
		t.Errorf("частки поїхали: %.2f і %.2f, чекали 5 000 і 3 000",
			goals[0].FillNowUAH, goals[1].FillNowUAH)
	}
	for _, g := range goals {
		if g.ShortMonthUAH != 0 {
			t.Errorf("%s: нестача %.2f там, де стеля все покриває", g.Name, g.ShortMonthUAH)
		}
	}
}

// Коли стеля не покриває потрібного — цілі беруть ПО ЧЕРЗІ, а не пропорційно.
//
// Пропорція дала б кожній трохи менше, ніж треба, тобто гарантовано
// провалила б УСІ дедлайни одразу й не сказала б про це жодним числом.
// Черга рятує перший дедлайн і чесно називає, чого бракує решті.
func TestGoalsFillQueuesByPriorityAndNamesTheShortfall(t *testing.T) {
	goals := []state.Goal{
		goalRow(1, "Авто", 100_000, 5_000, 0, "2027-06-01"),
		goalRow(2, "Ремонт", 60_000, 3_000, 0, "2027-06-01"),
	}
	// План 30 000 × 20% = 6 000 стелі на потрібні 8 000.
	state.GoalsFill(fillShare(20), goals, 30_000)

	if math.Abs(goals[0].FillNowUAH-5_000) > 0.01 {
		t.Errorf("перша ціль дістала %.2f замість своїх 5 000 — стеля поділилась порівну",
			goals[0].FillNowUAH)
	}
	if math.Abs(goals[1].FillNowUAH-1_000) > 0.01 {
		t.Errorf("другій дісталось %.2f, а лишалось 1 000", goals[1].FillNowUAH)
	}
	if math.Abs(goals[1].ShortMonthUAH-2_000) > 0.01 {
		t.Errorf("нестача другої = %.2f, а це 3 000 − 1 000 = 2 000", goals[1].ShortMonthUAH)
	}
	if goals[0].ShortMonthUAH != 0 {
		t.Errorf("перша ціль дістала своє, нестачі бути не мусить: %.2f", goals[0].ShortMonthUAH)
	}
}

// Уже відкладене цього місяця ВІДНІМАЄТЬСЯ.
//
// Без цього порада висіла б незмінною, хай би скільки ти відкладав, — та
// сама вада, яку вже виправляли подушці. Перевіряється саме парою: стеля
// місяця лишається тією ж (вона про весь місяць, а не про залишок), а «ще
// лишилось» меншає рівно на покладене.
func TestGoalsFillSubtractsWhatIsAlreadyMoved(t *testing.T) {
	goals := []state.Goal{goalRow(1, "Авто", 100_000, 5_000, 2_000, "2027-06-01")}
	state.GoalsFill(fillShare(20), goals, 100_000)

	if math.Abs(goals[0].FillMonthUAH-5_000) > 0.01 {
		t.Errorf("стеля місяця = %.2f, чекали 5 000 (2 000 покладено + 3 000 лишилось)",
			goals[0].FillMonthUAH)
	}
	if math.Abs(goals[0].FillNowUAH-3_000) > 0.01 {
		t.Errorf("лишилось відкласти %.2f, а це 5 000 − 2 000 = 3 000", goals[0].FillNowUAH)
	}
}

// Ціль без дедлайну бере ВЕСЬ свій розрив, а не нуль.
//
// Потрібного темпу в неї немає — немає дати, — і обмежити її можна лише
// самою ціллю. Нуль означав би, що ціль без дати не наповнюється ніколи,
// тобто механізм для неї просто не працює.
func TestGoalWithoutDueDateTakesWholeGap(t *testing.T) {
	goals := []state.Goal{goalRow(1, "Будинок", 40_000, 0, 0, "")}
	state.GoalsFill(fillShare(50), goals, 200_000) // стеля 100 000 > розриву

	if math.Abs(goals[0].FillNowUAH-40_000) > 0.01 {
		t.Errorf("ціль без дати дістала %.2f замість усього розриву 40 000", goals[0].FillNowUAH)
	}
	if goals[0].ShortMonthUAH != 0 {
		t.Errorf("нестачі бути не мусить — стеля більша за розрив: %.2f", goals[0].ShortMonthUAH)
	}
}

// Зібрана й закрита цілі стелі не займають.
//
// Інакше вони з'їдали б чергу в тих, кому ще треба: місце в черзі — це не
// формальність, а гроші, які підуть комусь іншому.
func TestGoalsFillIgnoresDoneAndClosedGoals(t *testing.T) {
	done := goalRow(1, "Ремонт", 0, 0, 0, "")
	done.DoneDate = "2026-06-01"
	goals := []state.Goal{
		done,
		goalRow(2, "Зібрана", 0, 0, 0, "2027-06-01"),
		goalRow(3, "Авто", 100_000, 5_000, 0, "2027-06-01"),
	}
	state.GoalsFill(fillShare(20), goals, 30_000)

	if goals[0].FillNowUAH != 0 || goals[1].FillNowUAH != 0 {
		t.Errorf("закрита або зібрана ціль узяла своє: %.2f і %.2f",
			goals[0].FillNowUAH, goals[1].FillNowUAH)
	}
	if math.Abs(goals[2].FillNowUAH-5_000) > 0.01 {
		t.Errorf("живій цілі дісталось %.2f замість 5 000 — чергу зайняли закриті",
			goals[2].FillNowUAH)
	}
}

// Без стелі механізм МОВЧИТЬ.
//
// Порожнє налаштування означає «застосунок про цілі не заговорить», і той,
// хто про це не просив, не мусить побачити жодної зміни. Нулі в документі
// читались би як «механізм працює й радить нуль».
func TestGoalsFillSilentWithoutSetting(t *testing.T) {
	goals := []state.Goal{goalRow(1, "Авто", 100_000, 5_000, 0, "2027-06-01")}
	state.GoalsFill(nil, goals, 100_000)
	state.GoalsFill(&state.SettingsDoc{}, goals, 100_000)
	state.GoalsFill(fillShare(20), goals, 0) // плану доходу немає

	if goals[0].FillMonthUAH != 0 || goals[0].FillNowUAH != 0 {
		t.Errorf("механізм заговорив без стелі або без плану: %+v", goals[0])
	}
}

// --- черга в розкладці надходження ---

// Подушка забирає своє ПЕРШОЮ, цілі — з того, що лишилось.
//
// Порядок не стилістичний: аварія не має дати й може статись завтра, а річ,
// на яку збирають, дату має. Пустити цілі поперед подушки означало б
// платити за передбачуване з грошей, відкладених на непередбачуване.
func TestAllocateReserveBeforeGoals(t *testing.T) {
	srv, _ := testServer(t)

	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_expenses":"10000","monthly_expenses_currency":"UAH","reserve_target_months":"6",
		  "reserve_fill_share_pct":"30","goals_fill_share_pct":"30","target_bonds_pct":"100"}`); resp.StatusCode >= 300 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, b)
	}
	// План доходу — без нього стелі нема від чого рахувати.
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"50000.00","cadence":"month",
		  "from_date":"2026-01-01","invest_pct":"100"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("потік: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/goals",
		`{"name":"Авто","amount":"500000","currency":"UAH"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("ціль: %d %s", resp.StatusCode, b)
	}

	var plan struct {
		Reserve *struct {
			AmountUAH float64 `json:"amount_uah"`
		} `json:"reserve"`
		Goals []struct {
			Name      string  `json:"name"`
			AmountUAH float64 `json:"amount_uah"`
		} `json:"goals"`
		GoalsUAH float64 `json:"goals_uah"`
		AvailUAH float64 `json:"avail_uah"`
		Note     string  `json:"note"`
	}
	_, body := do(t, "POST", srv.URL+"/api/allocate", `{"amount":"20000"}`)
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		t.Fatalf("allocate: %v: %s", err, body)
	}

	// Стеля подушки — 30% від 50 000 = 15 000; стеля цілей — стільки ж, але
	// різати їй уже нема з чого: подушка забрала 15 000 із 20 000, лишилось
	// 5 000, і саме вони йдуть у ціль.
	if plan.Reserve == nil || math.Abs(plan.Reserve.AmountUAH-15_000) > 0.01 {
		t.Fatalf("подушка взяла %+v, чекали 15 000", plan.Reserve)
	}
	if len(plan.Goals) != 1 || math.Abs(plan.Goals[0].AmountUAH-5_000) > 0.01 {
		t.Fatalf("цілі взяли %+v, чекали 5 000 залишку", plan.Goals)
	}
	if plan.Goals[0].Name != "Авто" {
		t.Errorf("вирізка не названа поіменно: %q", plan.Goals[0].Name)
	}
	// Усе розібрано — і нота мусить назвати ОБОХ, а не саму подушку.
	if plan.AvailUAH > 0.01 {
		t.Errorf("до паперів дійшло %.2f, а мало нічого", plan.AvailUAH)
	}
	if plan.Note == "" || !strings.Contains(plan.Note, "цілі") {
		t.Errorf("нота не називає цілей: %q", plan.Note)
	}
}

// Політика «звідки наповнювати» в цілей СВОЯ.
//
// Подушку багато хто свідомо наповнює лише зарплатою, а цілі — усім, що
// прийде. Спільний ключ віддав би обом найсуворішу з двох політик, а
// зникла вирізка без пояснення читається як поломка — тому перевіряється
// ще й причина.
func TestGoalsFillFromIsIndependentOfReserve(t *testing.T) {
	srv, _ := testServer(t)

	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"goals_fill_share_pct":"50","goals_fill_from":"plan","target_bonds_pct":"100"}`); resp.StatusCode >= 300 {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"50000.00","cadence":"month",
		  "from_date":"2026-01-01","invest_pct":"100"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("потік: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/goals",
		`{"name":"Авто","amount":"500000","currency":"UAH"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("ціль: %d %s", resp.StatusCode, b)
	}

	var plan struct {
		GoalsUAH     float64 `json:"goals_uah"`
		GoalsSkipWhy string  `json:"goals_skip_why"`
	}
	// Купон — це дохід ПОРТФЕЛЯ, а політика каже «лише плановий».
	_, body := do(t, "POST", srv.URL+"/api/allocate",
		`{"amount":"20000","source":"portfolio"}`)
	if err := json.Unmarshal([]byte(body), &plan); err != nil {
		t.Fatalf("allocate: %v: %s", err, body)
	}
	if plan.GoalsUAH > 0.01 {
		t.Errorf("цілі взяли %.2f з купона при політиці «лише планові»", plan.GoalsUAH)
	}
	if plan.GoalsSkipWhy == "" {
		t.Error("вирізка зникла мовчки — це читається як поломка, а не як рішення")
	}
}
