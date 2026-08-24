package domain

import "testing"

func seq(n int, start, step int64) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = start + int64(i)*step
	}
	return out
}

// Вікно коротше за рік не існує зовсім: краще нічого, ніж перцентиль на
// трьох точках, який виглядає таким самим точним, як на ста двадцяти.
func TestFXPlaceRefusesShortWindow(t *testing.T) {
	if _, ok := FXPlace(seq(11, 400_000, 1_000), 405_000, 1); ok {
		t.Error("одинадцять точок не мали скласти вікно")
	}
	if _, ok := FXPlace(seq(12, 400_000, 1_000), 405_000, 1); !ok {
		t.Error("дванадцять точок — вікно вже є")
	}
}

func TestFXPlaceRefusesNonPositiveRate(t *testing.T) {
	if _, ok := FXPlace(seq(24, 400_000, 1_000), 0, 1); ok {
		t.Error("нульовий курс не має де стояти")
	}
}

// На монотонному ряді перцентиль читається очима: курс нижчий за все —
// нуль, вищий за все — сто, рівно посередині — половина.
func TestFXPlaceMonotonic(t *testing.T) {
	rates := seq(100, 400_000, 100) // 40.0000 … 49.9000
	cases := []struct {
		name string
		now  int64
		want float64
	}{
		{"нижче за все", 399_000, 0},
		{"вище за все", 500_000, 100},
		{"рівно перша точка", 400_000, 0.5},    // 0 нижчих + половина однієї рівної
		{"рівно остання точка", 409_900, 99.5}, // 99 нижчих + половина однієї рівної
	}
	for _, c := range cases {
		w, ok := FXPlace(rates, c.now, 3)
		if !ok {
			t.Fatalf("%s: вікно не склалось", c.name)
		}
		if w.Percentile != c.want {
			t.Errorf("%s: перцентиль %.2f, очікували %.2f", c.name, w.Percentile, c.want)
		}
	}
}

// Ряд з однакових курсів: «нижчих нуль» і «нижчих сто» однаково неправда,
// половина — рівно те, що про такий ряд можна сказати.
func TestFXPlaceTiesGiveHalf(t *testing.T) {
	flat := make([]int64, 50)
	for i := range flat {
		flat[i] = 420_000
	}
	w, ok := FXPlace(flat, 420_000, 10)
	if !ok {
		t.Fatal("вікно не склалось")
	}
	if w.Percentile != 50 {
		t.Errorf("перцентиль %.2f, очікували 50", w.Percentile)
	}
	if w.MedianE4 != 420_000 || w.MinE4 != 420_000 || w.MaxE4 != 420_000 {
		t.Errorf("рівний ряд мав дати однакові median/min/max, маємо %+v", w)
	}
}

// Медіана — СПРАВЖНЯ точка з історії, а не середнє двох сусідніх: для
// парної кількості беремо нижню, і 405_000 тут не має з'явитись нізвідки.
func TestFXPlaceMedianIsARealPoint(t *testing.T) {
	rates := []int64{400_000, 410_000}
	for i := 0; i < 11; i++ { // добиваємо до порога, не змінюючи країв
		rates = append(rates, 400_000)
	}
	w, ok := FXPlace(rates, 405_000, 1)
	if !ok {
		t.Fatal("вікно не склалось")
	}
	if w.MedianE4 != 400_000 {
		t.Errorf("медіана %d, очікували справжню точку 400000", w.MedianE4)
	}
}

// Недодатні точки не беруть участі ні в порядку, ні в кількості: нуль у
// ряді курсів — це відсутність даних, а не курс нуль.
func TestFXPlaceDropsNonPositivePoints(t *testing.T) {
	rates := append(seq(12, 400_000, 1_000), 0, -5)
	w, ok := FXPlace(rates, 405_000, 1)
	if !ok {
		t.Fatal("вікно не склалось")
	}
	if w.Points != 12 {
		t.Errorf("точок %d, очікували 12", w.Points)
	}
	if w.MinE4 != 400_000 {
		t.Errorf("мінімум %d — нуль проліз у ряд", w.MinE4)
	}
}
