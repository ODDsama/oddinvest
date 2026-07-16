package domain

import "math"

import "testing"

func TestProjectCapitalContributionsAreAnnuity(t *testing.T) {
	// лише внески (cash0=0), миттєвий реінвест (threshold=0): симуляція =
	// класичний FV звичайного ануїтету C·((1+i)^n − 1)/i.
	C, rate := 100.0, 12.0
	months := 24
	got := ProjectCapital(0, 0, C, 0, rate, nil, nil, months)
	i := rate / 100 / 12
	g := math.Pow(1+i, float64(months))
	want := C * (g - 1) / i
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("ануїтет: got %.4f, want %.4f", got, want)
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

func TestRequiredMonthlyHitsGoal(t *testing.T) {
	req := RequiredMonthly(0, 0, 0, 12, 100000, nil, nil, 60)
	got := ProjectCapital(0, 0, req, 0, 12, nil, nil, 60)
	if math.Abs(got-100000) > 1.0 {
		t.Fatalf("required %.2f/міс -> %.2f, хочемо ~100000", req, got)
	}
}
