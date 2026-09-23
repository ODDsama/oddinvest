package domain

import (
	"math"
	"testing"
)

// runSteps — голий крок моделі (projState.step) без рукавів: те, що доти
// робила продакшн-обгортка ProjectCapital, прибрана як мертва. Живе в
// тестах, бо тут і лише тут потрібна однорукавна еталонна симуляція.
func runSteps(cash0, nominal0, contrib, threshold, ratePct float64,
	coupon, redeem map[int]float64, months int) float64 {
	st := projState{cash: cash0, locked: nominal0}
	rM := MonthlyRate(ratePct)
	for m := 1; m <= months; m++ {
		st.step(rM, contrib, threshold, coupon[m], redeem[m])
	}
	return st.total()
}

func TestStepContributionsAreAnnuity(t *testing.T) {
	// лише внески (cash0=0), миттєвий реінвест (threshold=0): симуляція =
	// класичний FV звичайного ануїтету C·((1+i)^n − 1)/i.
	C, rate := 100.0, 12.0
	months := 24
	got := runSteps(0, 0, C, 0, rate, nil, nil, months)
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

func TestStepRealCouponsFeedPool(t *testing.T) {
	// наявний папір номіналом 1000 гасне на місяці 6 (повертає номінал),
	// платить купон 50 на місяці 3. Внесків немає, ставка 0 → капітал
	// не має зникати: locked -> cash, підсумок лишається 1000.
	red := map[int]float64{6: 1000}
	cpn := map[int]float64{3: 50}
	got := runSteps(0, 1000, 0, 0, 0, cpn, red, 12)
	if math.Abs(got-1050) > 1e-9 {
		t.Fatalf("реальні потоки: got %.4f, want 1050 (1000 номінал + 50 купон)", got)
	}
}

func TestStepCashIdleUntilThreshold(t *testing.T) {
	// готівка 500 < найдешевший папір 1000, ставка 100%: поки не назбирано
	// на папір — не працює. За 1 міс без внесків капітал не росте.
	got := runSteps(500, 0, 0, 1000, 100, nil, nil, 1)
	if math.Abs(got-500) > 1e-9 {
		t.Fatalf("готівка нижче порогу має лежати: got %.4f, want 500", got)
	}
}
