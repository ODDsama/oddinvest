package engine

import (
	"math"
	"testing"

	"github.com/ODDsama/oddinvest/internal/state"
	money "github.com/Rhymond/go-money"
)

func goalRow(id int64, name string, gap, required, moved float64, due string) state.Goal {
	return state.Goal{
		ID: id, Name: name, Currency: money.UAH,
		GapUAH: state.Major(gap, money.UAH), RequiredUAH: state.Major(required, money.UAH), MovedUAH: state.Major(moved, money.UAH), DueDate: due,
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
	state.GoalsFill(fillShare(20), goals, 100_000, false)

	if math.Abs(goals[0].FillNowUAH.Major()-5_000) > 0.01 || math.Abs(goals[1].FillNowUAH.Major()-3_000) > 0.01 {
		t.Errorf("частки поїхали: %.2f і %.2f, чекали 5 000 і 3 000",
			goals[0].FillNowUAH.Major(), goals[1].FillNowUAH.Major())
	}
	for _, g := range goals {
		if g.ShortMonthUAH.Major() != 0 {
			t.Errorf("%s: нестача %.2f там, де стеля все покриває", g.Name, g.ShortMonthUAH.Major())
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
	state.GoalsFill(fillShare(20), goals, 30_000, false)

	if math.Abs(goals[0].FillNowUAH.Major()-5_000) > 0.01 {
		t.Errorf("перша ціль дістала %.2f замість своїх 5 000 — стеля поділилась порівну",
			goals[0].FillNowUAH.Major())
	}
	if math.Abs(goals[1].FillNowUAH.Major()-1_000) > 0.01 {
		t.Errorf("другій дісталось %.2f, а лишалось 1 000", goals[1].FillNowUAH.Major())
	}
	if math.Abs(goals[1].ShortMonthUAH.Major()-2_000) > 0.01 {
		t.Errorf("нестача другої = %.2f, а це 3 000 − 1 000 = 2 000", goals[1].ShortMonthUAH.Major())
	}
	if goals[0].ShortMonthUAH.Major() != 0 {
		t.Errorf("перша ціль дістала своє, нестачі бути не мусить: %.2f", goals[0].ShortMonthUAH.Major())
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
	state.GoalsFill(fillShare(20), goals, 100_000, false)

	if math.Abs(goals[0].FillMonthUAH.Major()-5_000) > 0.01 {
		t.Errorf("стеля місяця = %.2f, чекали 5 000 (2 000 покладено + 3 000 лишилось)",
			goals[0].FillMonthUAH.Major())
	}
	if math.Abs(goals[0].FillNowUAH.Major()-3_000) > 0.01 {
		t.Errorf("лишилось відкласти %.2f, а це 5 000 − 2 000 = 3 000", goals[0].FillNowUAH.Major())
	}
}

// Ціль без дедлайну бере ВЕСЬ свій розрив, а не нуль.
//
// Потрібного темпу в неї немає — немає дати, — і обмежити її можна лише
// самою ціллю. Нуль означав би, що ціль без дати не наповнюється ніколи,
// тобто механізм для неї просто не працює.
func TestGoalWithoutDueDateTakesWholeGap(t *testing.T) {
	goals := []state.Goal{goalRow(1, "Будинок", 40_000, 0, 0, "")}
	state.GoalsFill(fillShare(50), goals, 200_000, false) // стеля 100 000 > розриву

	if math.Abs(goals[0].FillNowUAH.Major()-40_000) > 0.01 {
		t.Errorf("ціль без дати дістала %.2f замість усього розриву 40 000", goals[0].FillNowUAH.Major())
	}
	if goals[0].ShortMonthUAH.Major() != 0 {
		t.Errorf("нестачі бути не мусить — стеля більша за розрив: %.2f", goals[0].ShortMonthUAH.Major())
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
	state.GoalsFill(fillShare(20), goals, 30_000, false)

	if goals[0].FillNowUAH.Major() != 0 || goals[1].FillNowUAH.Major() != 0 {
		t.Errorf("закрита або зібрана ціль узяла своє: %.2f і %.2f",
			goals[0].FillNowUAH.Major(), goals[1].FillNowUAH.Major())
	}
	if math.Abs(goals[2].FillNowUAH.Major()-5_000) > 0.01 {
		t.Errorf("живій цілі дісталось %.2f замість 5 000 — чергу зайняли закриті",
			goals[2].FillNowUAH.Major())
	}
}

// Без стелі механізм МОВЧИТЬ.
//
// Порожнє налаштування означає «застосунок про цілі не заговорить», і той,
// хто про це не просив, не мусить побачити жодної зміни. Нулі в документі
// читались би як «механізм працює й радить нуль».
func TestGoalsFillSilentWithoutSetting(t *testing.T) {
	goals := []state.Goal{goalRow(1, "Авто", 100_000, 5_000, 0, "2027-06-01")}
	state.GoalsFill(nil, goals, 100_000, false)
	state.GoalsFill(&state.SettingsDoc{}, goals, 100_000, false)
	state.GoalsFill(fillShare(20), goals, 0, false) // плану доходу немає

	if goals[0].FillMonthUAH.Major() != 0 || goals[0].FillNowUAH.Major() != 0 {
		t.Errorf("механізм заговорив без стелі або без плану: %+v", goals[0])
	}
}

// Пауза цілей на час боргу: ключ goals_while_debt, який до цієї фази не
// читав НІХТО.
//
// Замовчування «keep» — мовчазна зупинка накопичення була б найгіршим
// виглядом помилки; «pause» вибирають свідомо.
func TestGoalsPausedWhileExiting(t *testing.T) {
	set := fillShare(20)

	goals := []state.Goal{goalRow(1, "Авто", 500_000, 0, 0, "")}
	state.GoalsFill(set, goals, 100_000, true)
	if goals[0].FillNowUAH.Major() <= 0 {
		t.Fatalf("без ключа борг зупинив цілі: %+v", goals[0])
	}

	set.GoalsWhileDebt = "pause"
	goals = []state.Goal{goalRow(1, "Авто", 500_000, 0, 0, "")}
	state.GoalsFill(set, goals, 100_000, true)
	if goals[0].FillMonthUAH.Major() != 0 || goals[0].FillNowUAH.Major() != 0 {
		t.Errorf("пауза не спрацювала: %+v", goals[0])
	}

	// Боргу немає — пауза не діє, хай би що стояло в ключі.
	goals = []state.Goal{goalRow(1, "Авто", 500_000, 0, 0, "")}
	state.GoalsFill(set, goals, 100_000, false)
	if goals[0].FillNowUAH.Major() <= 0 {
		t.Errorf("пауза діє без боргу: %+v", goals[0])
	}
}
