package engine

import (
	"testing"

	"github.com/ODDsama/oddinvest/internal/store"
)

// TestStreakMarksMatchStreak — смужка місяців і число серії
// мусять бути одним і тим самим, порахованим двічі.
//
// Це той самий клас захисту, що й TestProgressReconciles: два подання
// одного числа розійшлись би тихо — смужка лишилась би правдоподібною.
func TestStreakMarksMatchStreak(t *testing.T) {
	snaps := []store.Snapshot{
		{Date: "2026-01-31", MonthTargetUAH: 1_000_000},
		// Лютого немає взагалі — знімків за нього не робилось.
		{Date: "2026-03-31", MonthTargetUAH: 1_000_000},
		{Date: "2026-04-30", MonthTargetUAH: 1_000_000},
		{Date: "2026-05-31", MonthTargetUAH: 1_000_000},
	}
	ev := []FlowEvent{
		{Date: "2026-01-15", Kind: FlowPurchase, UAH: -1_200_000},
		{Date: "2026-02-15", Kind: FlowPurchase, UAH: -1_200_000},
		{Date: "2026-04-15", Kind: FlowPurchase, UAH: -1_200_000},
		{Date: "2026-05-15", Kind: FlowPurchase, UAH: -1_200_000},
	}
	got := BuildStreak(snaps, ev, "2026-06-10")

	// Ряд суцільний: лютий у ньому Є, просто невідомий. Без нього
	// січень і березень стали б сусідами, і серія на смужці вийшла б
	// довшою за ту, яку рахує сам BuildStreak.
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
	if got.Marks[1].ContribUAH.Major() != 12000 {
		t.Errorf("внесок лютого %v: він відомий із руху грошей навіть без знімка",
			got.Marks[1].ContribUAH.Major())
	}
	if !got.Marks[2].Known || got.Marks[2].Hit {
		t.Errorf("березень: ціль була, внеску не було — known=%v hit=%v",
			got.Marks[2].Known, got.Marks[2].Hit)
	}
	if got.Marks[0].TargetUAH.Major() != 10000 || got.Marks[0].ContribUAH.Major() != 12000 {
		t.Errorf("січень: %v із %v — мало бути 12000 із 10000",
			got.Marks[0].ContribUAH.Major(), got.Marks[0].TargetUAH.Major())
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

// TestStreakUsesTargetOfItsMonth — ціль минулого місяця береться
// зі знімка ТОГО місяця, а не з сьогоднішніх налаштувань.
//
// Без цього зміна цілі переписувала б минуле: підняв ціль удвічі — і
// заднім числом «зривався» пів року, хоч тоді все було виконано.
func TestStreakUsesTargetOfItsMonth(t *testing.T) {
	snaps := []store.Snapshot{
		// Січень: ціль 10 000 ₴ (у копійках), внесено 12 000 — виконано.
		{Date: "2026-01-31", MonthTargetUAH: 1_000_000},
		// Лютий: ціль піднялась до 50 000, внесено ті самі 12 000 — ні.
		{Date: "2026-02-28", MonthTargetUAH: 5_000_000},
		// Березень: ціль знову 10 000 — виконано.
		{Date: "2026-03-31", MonthTargetUAH: 1_000_000},
	}
	ev := []FlowEvent{
		{Date: "2026-01-15", Kind: FlowPurchase, UAH: -1_200_000},
		{Date: "2026-02-15", Kind: FlowPurchase, UAH: -1_200_000},
		{Date: "2026-03-15", Kind: FlowPurchase, UAH: -1_200_000},
	}
	got := BuildStreak(snaps, ev, "2026-04-10")

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

// TestStreakSkipsUnknownMonths — місяць без знімка обриває
// ЗНАННЯ, а не зараховується й не карається.
//
// Це головна різниця між «ти зривався» і «застосунок тоді не дивився», і
// сплутати їх означає докоряти за власну сліпоту.
func TestStreakSkipsUnknownMonths(t *testing.T) {
	snaps := []store.Snapshot{
		{Date: "2026-01-31", MonthTargetUAH: 1_000_000},
		// Лютого немає взагалі — знімків за нього не робилось.
		{Date: "2026-03-31", MonthTargetUAH: 1_000_000},
		{Date: "2026-04-30", MonthTargetUAH: 1_000_000},
	}
	ev := []FlowEvent{
		{Date: "2026-01-15", Kind: FlowPurchase, UAH: -1_200_000},
		{Date: "2026-03-15", Kind: FlowPurchase, UAH: -1_200_000},
		{Date: "2026-04-15", Kind: FlowPurchase, UAH: -1_200_000},
	}
	got := BuildStreak(snaps, ev, "2026-05-10")

	// Січень виконано, лютий невідомий, березень і квітень виконані:
	// серія — два, а не три (діра обірвала) і не нуль (докору немає).
	if got.Months != 2 {
		t.Errorf("серія мала бути 2 (березень і квітень), маємо %d", got.Months)
	}
	if got.BrokenOn != "" {
		t.Errorf("пропущений місяць — не зрив плану, а він записаний як «%s»", got.BrokenOn)
	}
}

// TestStreakCountsOnlyContributions — серія міряє ТВІЙ внесок, а не
// будь-які покупки.
//
// Покупка, оплачена купоном чи погашенням, теж велика, і зарахувати її в
// план означало б святкувати те, що сталося саме собою. Внесок — лише
// те, що понад виплату: тут 50 100 покупок мінус 50 000 купона = 1 000.
func TestStreakCountsOnlyContributions(t *testing.T) {
	snaps := []store.Snapshot{{Date: "2026-01-31", MonthTargetUAH: 1_000_000}}
	ev := []FlowEvent{
		{Date: "2026-01-15", Kind: FlowIncome, UAH: 5_000_000},
		{Date: "2026-01-20", Kind: FlowPurchase, UAH: -5_100_000},
	}
	got := BuildStreak(snaps, ev, "2026-02-10")
	if got.Months != 0 {
		t.Errorf("внесено 1 000 ₴ з 10 000 — місяць не виконано, а серія %d", got.Months)
	}
}
