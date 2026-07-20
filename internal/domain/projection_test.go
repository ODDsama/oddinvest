package domain

import "math"

import "testing"

func TestProjectCapitalContributionsAreAnnuity(t *testing.T) {
	// лише внески (cash0=0), миттєвий реінвест (threshold=0): симуляція =
	// класичний FV звичайного ануїтету C·((1+i)^n − 1)/i.
	C, rate := 100.0, 12.0
	months := 24
	got := ProjectCapital(0, 0, C, 0, rate, nil, nil, months)
	i := MonthlyRate(rate)
	g := math.Pow(1+i, float64(months))
	want := C * (g - 1) / i
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("ануїтет: got %.4f, want %.4f", got, want)
	}
}

// Місячна ставка має бути ЕКВІВАЛЕНТНОЮ: 12 капіталізацій дають рівно
// заявлену річну, а не більше (стара помилка r/12).
func TestMonthlyRateIsEquivalent(t *testing.T) {
	for _, annual := range []float64{15.78, 5, 40} {
		got := math.Pow(1+MonthlyRate(annual), 12) - 1
		if math.Abs(got-annual/100) > 1e-12 {
			t.Errorf("річна %.2f%%: 12 капіталізацій дали %.6f%%", annual, got*100)
		}
		if MonthlyRate(annual) >= annual/100/12 {
			t.Errorf("еквівалентна ставка має бути меншою за r/12 (%.6f)", MonthlyRate(annual))
		}
	}
}

func TestProjectCapitalRealCouponsFeedPool(t *testing.T) {
	// наявний папір номіналом 1000 гасне на місяці 6 (повертає номінал),
	// платить купон 50 на місяці 3. Внесків немає, ставка 0 → капітал
	// не має зникати: locked -> cash, підсумок лишається 1000.
	red := map[int]float64{6: 1000}
	cpn := map[int]float64{3: 50}
	got := ProjectCapital(0, 1000, 0, 0, 0, cpn, red, 12)
	if math.Abs(got-1050) > 1e-9 {
		t.Fatalf("реальні потоки: got %.4f, want 1050 (1000 номінал + 50 купон)", got)
	}
}

func TestProjectCapitalCashIdleUntilThreshold(t *testing.T) {
	// готівка 500 < найдешевший папір 1000, ставка 100%: поки не назбирано
	// на папір — не працює. За 1 міс без внесків капітал не росте.
	got := ProjectCapital(500, 0, 0, 1000, 100, nil, nil, 1)
	if math.Abs(got-500) > 1e-9 {
		t.Fatalf("готівка нижче порогу має лежати: got %.4f, want 500", got)
	}
}

func TestMonthsToReachMatchesProjection(t *testing.T) {
	// місяць, який повернув MonthsToReach, має бути ПЕРШИМ, де капітал
	// сягає цілі: на ньому вже досягнуто, на попередньому — ще ні.
	targets := []float64{50000, 100000}
	got := MonthsToReach(0, 0, 1000, 0, 12, nil, nil, targets, 600)
	for i, m := range got {
		if m <= 0 {
			t.Fatalf("ціль %.0f не досягнута: %d", targets[i], m)
		}
		at := ProjectCapital(0, 0, 1000, 0, 12, nil, nil, m)
		before := ProjectCapital(0, 0, 1000, 0, 12, nil, nil, m-1)
		if at < targets[i] {
			t.Errorf("міс %d: капітал %.2f < цілі %.0f", m, at, targets[i])
		}
		if before >= targets[i] {
			t.Errorf("міс %d не перший: попередній уже %.2f ≥ %.0f", m, before, targets[i])
		}
	}
}

func TestMonthsToReachAlreadyAndUnreachable(t *testing.T) {
	// ціль нижча за наявний капітал -> -1; недосяжна за горизонт -> 0
	got := MonthsToReach(10000, 0, 0, 0, 0, nil, nil, []float64{5000, 1e9}, 120)
	if got[0] != -1 {
		t.Errorf("вже досягнуту ціль очікували -1, маємо %d", got[0])
	}
	if got[1] != 0 {
		t.Errorf("недосяжну ціль очікували 0, маємо %d", got[1])
	}
}

func TestRequiredMonthlyHitsGoal(t *testing.T) {
	req := RequiredMonthly(0, 0, 0, 12, 100000, nil, nil, 60)
	got := ProjectCapital(0, 0, req, 0, 12, nil, nil, 60)
	if math.Abs(got-100000) > 1.0 {
		t.Fatalf("required %.2f/міс -> %.2f, хочемо ~100000", req, got)
	}
}
