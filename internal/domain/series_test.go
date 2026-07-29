package domain

import "testing"

// TestSeriesEndsWhereProjectSleevesDoes — остання точка кривої дорівнює
// тому, що на тому ж горизонті дає ProjectSleeves.
//
// Головний сторож: це дві реалізації однієї моделі — одна крокує в
// лок-степі й віддає зрізи, друга жене кожен рукав окремо до кінця.
// Розійтись їм не можна, бо на екрані вони стоять поруч: крива веде до
// числа, яке картка прогнозу називає словами.
func TestSeriesEndsWhereProjectSleevesDoes(t *testing.T) {
	sl := oneSleeve(50_000, 200_000, 14, 9_000)
	for _, months := range []int{1, 7, 12, 39, 120} {
		got := ProjectSleevesSeries(sl, 7, months, 3)
		if len(got) == 0 {
			t.Fatalf("%d міс: крива порожня", months)
		}
		last := got[len(got)-1]
		if last.Month != months {
			t.Errorf("%d міс: остання точка на місяці %d — кінець кривої мусить бути на горизонті",
				months, last.Month)
		}
		want := ProjectSleeves(sl, 7, months).TodayUAH
		if diff := last.UAH - want; diff > 0.005 || diff < -0.005 {
			t.Errorf("%d міс: крива дає %v, ProjectSleeves %v", months, last.UAH, want)
		}
	}
}

// TestSeriesStartsAtToday — перша точка це сьогоднішній капітал, без
// жодного місяця симуляції.
//
// Крива, що починається з першого кроку, візуально «стрибає» від нуля, і
// читач бачить приріст, якого не було.
func TestSeriesStartsAtToday(t *testing.T) {
	sl := oneSleeve(50_000, 200_000, 14, 9_000)
	got := ProjectSleevesSeries(sl, 7, 24, 3)
	if got[0].Month != 0 {
		t.Fatalf("перша точка на місяці %d, очікували 0", got[0].Month)
	}
	// Сьогодні капітал це просто готівка плюс замкнене, без дисконту.
	if want := 250_000.0; got[0].UAH != want {
		t.Errorf("старт %v, очікували %v", got[0].UAH, want)
	}
}

// TestSeriesRespectsStep — крок задає частоту точок, а кінець додається
// завжди.
//
// 39 місяців кроком 3 дають рівно 0,3,…,39; 40 кроком 3 дають ті самі
// плюс окрему точку 40, бо інакше кінець кривої не збігся б із прогнозом.
func TestSeriesRespectsStep(t *testing.T) {
	sl := oneSleeve(10_000, 0, 10, 1_000)
	exact := ProjectSleevesSeries(sl, 5, 39, 3)
	if n := len(exact); n != 14 {
		t.Errorf("39 міс кроком 3: точок %d, очікували 14", n)
	}
	odd := ProjectSleevesSeries(sl, 5, 40, 3)
	last, prev := odd[len(odd)-1], odd[len(odd)-2]
	if last.Month != 40 || prev.Month != 39 {
		t.Errorf("40 міс кроком 3: останні місяці %d і %d, очікували 39 і 40",
			prev.Month, last.Month)
	}
}

// TestSeriesIsMonotoneWhileContributing — поки вносиш і нічого не
// гаситься, капітал у сьогоднішніх гривнях не падає.
//
// Не самоочевидно: суми дисконтуються знеціненням, тож спадна крива тут
// цілком можлива арифметично — саме тому й перевіряємо, що при живому
// внеску вона не спадає.
func TestSeriesIsMonotoneWhileContributing(t *testing.T) {
	got := ProjectSleevesSeries(oneSleeve(0, 100_000, 16, 20_000), 7, 60, 1)
	for i := 1; i < len(got); i++ {
		if got[i].UAH < got[i-1].UAH {
			t.Fatalf("місяць %d: %v менше за попередній %v",
				got[i].Month, got[i].UAH, got[i-1].UAH)
		}
	}
}

// TestSeriesZeroHorizonIsEmpty — нульовий горизонт не дає точок.
// Порожня крива краща за одну точку «сьогодні»: малювати лінію з однієї
// точки нема сенсу, і UI має підставу її не показувати.
func TestSeriesZeroHorizonIsEmpty(t *testing.T) {
	if got := ProjectSleevesSeries(oneSleeve(1_000, 0, 10, 0), 7, 0, 3); got != nil {
		t.Errorf("нульовий горизонт дав %d точок", len(got))
	}
}
