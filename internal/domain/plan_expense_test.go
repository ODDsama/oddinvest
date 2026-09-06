package domain

import "testing"

const peToday = Date("2026-09-06")

func pe(due, paid string) PlanExpense {
	return PlanExpense{
		Name: "Котел", Amount: 30_000_00, Currency: "UAH",
		DueDate: Date(due), PaidFrom: PaidFromCard, PaidDate: Date(paid),
	}
}

// ГОЛОВНИЙ ТЕСТ ФАЗИ. Прострочена витрата мусить тиснути на ПОТОЧНИЙ
// місяць — саме цим вона й відрізняється від разового потоку, який у
// минулому місяці зникає нанівець (planFlowNative обриває `case "once"` за
// відʼємним зсувом). Якщо цей тест зелений на нулі, а не на −1 і не на
// власному місяці витрати, окрема таблиця 0056 виправдана.
func TestPlanExpenseOverdueStillPressesCurrentMonth(t *testing.T) {
	p := pe("2026-07-12", "") // два місяці тому, гроші не пішли
	if !p.Overdue(peToday) {
		t.Fatal("витрата з датою в минулому й без paid_date мусить бути простроченою")
	}
	if got := p.PressMonth(peToday); got != 0 {
		t.Errorf("прострочена тисне на місяць %d, а мала на поточний (0): "+
			"у своєму місяці змінити вже нічого не можна, а зникнувши, вона "+
			"перестала б вимагати грошей, яких далі вимагає", got)
	}
}

// Сплачена не тисне НІДЕ — і має власну гілку, а не нульову суму: нуль
// читався б як «витрати не сталося».
func TestPlanExpensePaidNeverPresses(t *testing.T) {
	p := pe("2026-07-12", "2026-07-14")
	if p.Overdue(peToday) {
		t.Error("сплачена не буває простроченою, хай яка стара її дата")
	}
	if got := p.PressMonth(peToday); got != -1 {
		t.Errorf("сплачена тисне на місяць %d, а мала не тиснути (-1)", got)
	}
	// Майбутня сплачена — те саме: платили наперед, і гроші вже пішли.
	if got := pe("2026-12-01", "2026-09-01").PressMonth(peToday); got != -1 {
		t.Errorf("сплачена наперед тисне на місяць %d, а мала не тиснути", got)
	}
}

func TestPlanExpenseFutureMonthOffset(t *testing.T) {
	// Зсув КАЛЕНДАРНИЙ, а не по днях: 30 вересня й 1 вересня — той самий
	// місяць, а 1 жовтня — наступний, хай між ними один день.
	for _, c := range []struct {
		due  string
		want int
	}{
		{"2026-09-30", 0},
		{"2026-10-01", 1},
		{"2026-11-15", 2},
		{"2027-09-06", 12},
	} {
		if got := pe(c.due, "").PressMonth(peToday); got != c.want {
			t.Errorf("%s: місяць %d, а мав бути %d", c.due, got, c.want)
		}
	}
}

// PressDate простроченої — СЬОГОДНІ, а не проґавлена дата. Від цього
// залежить фільтр місяця звірки картки: минула дата пройшла б його як
// «уже в балансі», хоча гроші не пішли.
func TestPlanExpensePressDateOfOverdueIsToday(t *testing.T) {
	if got := pe("2026-07-12", "").PressDate(peToday); got != peToday {
		t.Errorf("прострочена показує дату %s, а мала сьогоднішню %s", got, peToday)
	}
	if got := pe("2026-11-15", "").PressDate(peToday); got != "2026-11-15" {
		t.Errorf("майбутня показує дату %s, а мала свою власну", got)
	}
	// Сьогоднішня — ще не прострочена: гроші мають піти сьогодні, і день
	// іще не минув.
	if pe(string(peToday), "").Overdue(peToday) {
		t.Error("витрата з датою «сьогодні» ще не прострочена")
	}
}
