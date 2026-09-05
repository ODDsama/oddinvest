package domain

import (
	"math"
	"testing"
)

// ryad2024 — СПРАВЖНІЙ ряд НБУ за грудень 2023 — грудень 2024, знятий із
// живого API. Вигаданих чисел тут немає навмисно: ланцюг перевіряється
// проти опублікованого річного темпу, і з рівних синтетичних відсотків
// така перевірка сходилась би завжди, тобто не перевіряла б нічого.
func ryad2024() []CPIPoint {
	return []CPIPoint{
		{Period: "2023-12", MoMBP: 70, YoYBP: 510},
		{Period: "2024-01", MoMBP: 40, YoYBP: 470},
		{Period: "2024-02", MoMBP: 30, YoYBP: 430},
		{Period: "2024-03", MoMBP: 50, YoYBP: 320},
		{Period: "2024-04", MoMBP: 20, YoYBP: 320},
		{Period: "2024-05", MoMBP: 60, YoYBP: 330},
		{Period: "2024-06", MoMBP: 220, YoYBP: 480},
		{Period: "2024-07", MoMBP: 0, YoYBP: 540},
		{Period: "2024-08", MoMBP: 60, YoYBP: 750},
		{Period: "2024-09", MoMBP: 150, YoYBP: 860},
		{Period: "2024-10", MoMBP: 180, YoYBP: 970},
		{Period: "2024-11", MoMBP: 190, YoYBP: 1120},
		{Period: "2024-12", MoMBP: 140, YoYBP: 1200},
	}
}

// TestCPIChainMatchesPublishedYoY — головна перевірка ряду: перемножені
// місячні зміни мусять збігтися з опублікованим річним темпом.
//
// Це друга, НЕЗАЛЕЖНА сітка під зсувом місяця (див. шапку nbu.Inflation):
// якщо ряд поїде на місяць, ланцюг рахуватиметься по чужих числах і
// розійдеться з опублікованим р/р одразу.
func TestCPIChainMatchesPublishedYoY(t *testing.T) {
	pts := ryad2024()
	levels := CPIChain(pts)

	drift, ok := CPIYoYDrift(pts, levels, "2024-12")
	if !ok {
		t.Fatal("розбіжність не порахувалась там, де є обидві точки")
	}
	if math.Abs(drift) > 0.1 {
		t.Fatalf("ланцюг розійшовся з опублікованим р/р на %.2f в.п.", drift)
	}
}

// TestCPIYoYDriftCatchesMissingMonth — дірка в ряду не оголошується
// помилкою при побудові, і саме тому мусить бути видною тут.
func TestCPIYoYDriftCatchesMissingMonth(t *testing.T) {
	full := ryad2024()
	holed := make([]CPIPoint, 0, len(full)-1)
	for _, p := range full {
		if p.Period == "2024-06" { // місяць із найбільшою зміною, +2.2%
			continue
		}
		holed = append(holed, p)
	}

	drift, ok := CPIYoYDrift(holed, CPIChain(holed), "2024-12")
	if !ok {
		t.Fatal("розбіжність не порахувалась")
	}
	if math.Abs(drift) < 2 {
		t.Fatalf("зникнення місяця з +2.2%% лишилось непоміченим: %.2f в.п.", drift)
	}
}

func TestCPIChainBaseIsFirstPoint(t *testing.T) {
	levels := CPIChain(ryad2024())
	if len(levels) != 13 {
		t.Fatalf("рівнів %d при 13 точках", len(levels))
	}
	if levels[0].Level != 1 {
		t.Fatalf("база не 1.0, а %v", levels[0].Level)
	}
	if levels[0].Period != "2023-12" {
		t.Fatalf("база не на першій точці, а на %q", levels[0].Period)
	}
	if CPIChain(nil) != nil {
		t.Fatal("порожній ряд дав рівні")
	}
}

func TestCPIAnnualPctOverAYear(t *testing.T) {
	levels := CPIChain(ryad2024())
	got, ok := CPIAnnualPct(levels, "2023-12", "2024-12")
	if !ok {
		t.Fatal("річний темп не порахувався на повному році")
	}
	if math.Abs(got-12.0) > 0.3 {
		t.Fatalf("річний темп %.2f%% при опублікованих 12.0%%", got)
	}
}

// TestCPIAnnualPctRefusesShortAndUnknown — та сама відмова, що в
// AnnualPct: нема з чого рахувати — нема числа, а не нуль.
//
// Мінімальне ВІКНО ВИМІРЮВАННЯ тут свідомо не задається: поріг «менше
// місяця» належить самій AnnualPct, а те, що річний темп із одного місяця
// — лотерея, вирішує викликач (у знецінення це devalMinDays, у картки
// інфляції — вікна 1/3/5/10 років). Другий поріг тут завів би третє
// уявлення про те саме.
func TestCPIAnnualPctRefusesShortAndUnknown(t *testing.T) {
	levels := CPIChain(ryad2024())
	if _, ok := CPIAnnualPct(levels, "2020-01", "2024-12"); ok {
		t.Fatal("темп порахувався від місяця, якого в ряду немає")
	}
	if _, ok := CPIAnnualPct(levels, "2024-12", "2024-12"); ok {
		t.Fatal("темп порахувався на нульовому проміжку")
	}
}

func TestCPIProjectGoalTargetGrows(t *testing.T) {
	// 100 000 ₴ через десять років за 8%/рік.
	got := CPIProject(100000, 8, 120)
	want := 100000 * math.Pow(1.08, 10)
	if math.Abs(got-want) > 0.01 {
		t.Fatalf("проєкція %.2f при очікуваних %.2f", got, want)
	}
	if CPIProject(100000, 8, 0) != 100000 {
		t.Fatal("нульовий горизонт зрушив суму")
	}
	if CPIProject(0, 8, 120) != 0 {
		t.Fatal("нуль виріс")
	}
}

func TestCPIPlaceRefusesShortWindow(t *testing.T) {
	if _, ok := CPIPlace([]int64{500, 600, 700}, 550, 1); ok {
		t.Fatal("вікно з трьох точок мусить мовчати")
	}
}

// TestCPIPlaceCountsTiesAsHalf — те саме правило, що у FXPlace, і воно
// тут навіть важливіше: опублікована інфляція має один знак після коми,
// тож однакові значення в ряду звичайна річ.
func TestCPIPlaceTiesGiveHalf(t *testing.T) {
	same := make([]int64, 12)
	for i := range same {
		same[i] = 800
	}
	w, ok := CPIPlace(same, 800, 1)
	if !ok {
		t.Fatal("вікно на дванадцяти точках мусить бути")
	}
	if w.Percentile != 50 {
		t.Fatalf("перцентиль на однаковому ряду = %v, хочемо 50", w.Percentile)
	}
	if w.MedianBP != 800 || w.MinBP != 800 || w.MaxBP != 800 {
		t.Fatalf("межі ряду поїхали: %+v", w)
	}
}

// TestCPIPlaceKeepsDeflation — від'ємний місяць лишається в ряду.
func TestCPIPlaceKeepsDeflation(t *testing.T) {
	vals := []int64{-50, 0, 100, 200, 300, 400, 500, 600, 700, 800, 900, 2660}
	w, ok := CPIPlace(vals, 500, 3)
	if !ok {
		t.Fatal("вікно мусить бути")
	}
	if w.MinBP != -50 {
		t.Fatalf("дефляційний місяць випав із ряду: min=%d", w.MinBP)
	}
	if w.Points != 12 {
		t.Fatalf("точок %d", w.Points)
	}
}
