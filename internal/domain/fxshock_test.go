package domain

import (
	"fmt"
	"math"
	"testing"
)

// months будує рівний місячний ряд від 2020-01 із заданими курсами.
func months(rates ...int64) []FXPoint {
	out := make([]FXPoint, 0, len(rates))
	y, m := 2020, 1
	for _, r := range rates {
		out = append(out, FXPoint{Date: Date(fmt.Sprintf("%04d-%02d-01", y, m)), RateE4: r})
		if m++; m > 12 {
			y, m = y+1, 1
		}
	}
	return out
}

// rising — n місяців рівного зростання, без жодного стрибка.
func rising(n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = 100_000 + int64(i)*100
	}
	return out
}

// Густота ряду не має ставати властивістю гривні: усередині місяця може
// лежати скільки завгодно добових точок, а місячним рухом лишається рух
// між ПЕРШИМИ числами. Інакше свіжа частина історії, яку джоба питає
// щодня, виглядала б стрибкішою за давню рівно через це.
func TestMonthlyFXCollapsesDailyPoints(t *testing.T) {
	in := []FXPoint{
		{Date: "2022-01-01", RateE4: 280_000},
		{Date: "2022-01-17", RateE4: 285_000},
		{Date: "2022-01-31", RateE4: 999_000}, // сплеск усередині місяця
		{Date: "2022-02-01", RateE4: 290_000},
	}
	got := MonthlyFX(in)
	if len(got) != 2 {
		t.Fatalf("двомісячний ряд мав дати дві точки, дав %d", len(got))
	}
	if got[0].Date != "2022-01-01" || got[0].RateE4 != 280_000 {
		t.Errorf("перша точка місяця не найраніша: %+v", got[0])
	}
	if got[1].Date != "2022-02-01" {
		t.Errorf("другий місяць загубився: %+v", got[1])
	}
}

// Той самий сплеск не має ставати «найгіршим місячним рухом»: він стався
// всередині місяця, а на місячній сітці його не видно. Твердження про
// гранулярність тримається тестом, а не лише абзацом у шапці.
func TestWorstFXMoveIsMeasuredOnMonthlyGrid(t *testing.T) {
	in := months(rising(13)...)
	in = append(in, FXPoint{Date: "2020-03-20", RateE4: 500_000})

	mv, ok := WorstFXMove(in, 1)
	if !ok {
		t.Fatal("рівний ряд на тринадцять місяців мусив дати вікно")
	}
	if mv.Pct > 1.5 {
		t.Errorf("сплеск усередині місяця протік у місячний рух: %+v", mv)
	}
}

// Пара мусить стояти РІВНО за months місяців. Пропущений місяць не
// підміняється сусіднім: назвати тримісячним рухом те, що сталось за
// чотири, — помилка тиха й правдоподібна.
func TestWorstFXMoveNeedsExactWindow(t *testing.T) {
	// Тридцять місяців рівного зростання, у яких 2021-01 стрибає вгору,
	// — саме він був би найгіршим вікном. Вирізаємо його: жодна пара з
	// ним більше не складається, а решта вікон лишається.
	rates := rising(30)
	rates[12] = 300_000
	full := months(rates...)

	var cut []FXPoint
	for _, p := range full {
		if monthKey(p.Date) == "2021-01" {
			continue
		}
		cut = append(cut, p)
	}
	if _, ok := FXMoveOver(cut, "2021-01-01", "2021-02-01"); ok {
		t.Error("вирізаний місяць не мав знайтись")
	}
	mv, ok := WorstFXMove(cut, 12)
	if !ok {
		t.Fatal("решта ряду ще дає дванадцять вікон")
	}
	if monthKey(mv.From) == "2021-01" || monthKey(mv.To) == "2021-01" {
		t.Errorf("вікно спирається на місяць, якого немає: %+v", mv)
	}
	if mv.Pct > 15 {
		t.Errorf("стрибок вирізаного місяця протік у відповідь: %+v", mv)
	}
}

// Максимум із трьох кандидатів — не максимум, а монетка, яка виглядає
// такою ж точною, як максимум зі ста.
func TestWorstFXMoveSilentOnThinHistory(t *testing.T) {
	if _, ok := WorstFXMove(months(rising(4)...), 1); ok {
		t.Error("три вікна не мали скласти «найгірше»")
	}
	if _, ok := WorstFXMove(months(rising(13)...), 1); !ok {
		t.Error("дванадцять вікон — найгірше вже є")
	}
}

// Ряд, у якому гривня лише міцніла, мовчить: дзеркального боку фіча не
// показує, а видати від'ємний рух за «найгірший» означало б відповісти
// на питання, якого не ставили.
func TestWorstFXMoveSilentWithoutUpwardMove(t *testing.T) {
	down := make([]int64, 13)
	for i := range down {
		down[i] = 120_000 - int64(i)*1_000
	}
	if _, ok := WorstFXMove(months(down...), 1); ok {
		t.Error("ряд без руху вгору не мав дати шоку")
	}
}

// Повернені дати — точки, які СПРАВДІ є у вході, а не межі запитаного
// вікна: число, що стоїть поруч із фактичними курсами, саме мусить бути
// фактом.
func TestFXMoveOverNamesRealPoints(t *testing.T) {
	in := []FXPoint{
		{Date: "2021-09-03", RateE4: 266_000},
		{Date: "2022-09-05", RateE4: 365_686},
	}
	mv, ok := FXMoveOver(in, "2021-09-20", "2022-09-28")
	if !ok {
		t.Fatal("обидва місяці є в ряду")
	}
	if mv.From != "2021-09-03" || mv.To != "2022-09-05" {
		t.Errorf("повернуто не справжні точки: %+v", mv)
	}
	if mv.Months != 12 {
		t.Errorf("вікно на дванадцять місяців, а не %d", mv.Months)
	}
	want := (365_686.0/266_000.0 - 1) * 100
	if math.Abs(mv.Pct-want) > 1e-9 {
		t.Errorf("відсоток %v, чекали %v", mv.Pct, want)
	}
}

// Співрух міряється за ТИМИ САМИМИ датами, і коли точки на них немає —
// валюта не шокується зовсім, а не підставляє найближчу.
func TestFXMoveOverSilentWhenMonthMissing(t *testing.T) {
	in := []FXPoint{{Date: "2021-09-01", RateE4: 266_000}}
	if _, ok := FXMoveOver(in, "2021-09-01", "2022-09-01"); ok {
		t.Error("другого місяця в ряду немає — мовчимо")
	}
}

// Зсув по ключу, а не календарний: 31 січня плюс місяць у Go дає
// 3 березня, і вікно «один місяць» мовчки стало б довшим.
func TestShiftMonthIsCalendarSafe(t *testing.T) {
	for _, c := range []struct {
		key  string
		n    int
		want string
	}{
		{"2022-01", 1, "2022-02"},
		{"2022-12", 1, "2023-01"},
		{"2022-01", 12, "2023-01"},
		{"2022-03", -3, "2021-12"},
	} {
		if got := shiftMonth(c.key, c.n); got != c.want {
			t.Errorf("shiftMonth(%q,%d) = %q, чекали %q", c.key, c.n, got, c.want)
		}
	}
}
