package api

import (
	"math"
	"testing"

	money "github.com/Rhymond/go-money"
)

// TestBreakdownRealFXMatchesRealPct — головна перевірка всієї серії:
// розклад НІЧОГО не перерахував. RealFXPct мусить бути тим самим числом,
// яке застосунок показував як реальну дохідність, до останнього знака.
func TestBreakdownRealFXMatchesRealPct(t *testing.T) {
	rc := rateContext{deval: 6.1}
	cases := []struct {
		net float64
		cur string
	}{
		{0.1655, money.UAH},
		{0.045, money.USD},
		{0.032, money.EUR},
		{-0.02, money.UAH},
	}
	for _, c := range cases {
		want := round2(realYield(c.net, c.cur, rc.deval) * 100)
		got := rc.breakdown(c.net, c.net, c.cur, "тест").RealFXPct
		if got != want {
			t.Fatalf("%s %v: розклад дав %v замість %v", c.cur, c.net, got, want)
		}
	}
}

// TestBreakdownNamesTaxWithoutRecomputingIt — податок сюди приходить уже
// відрахованим (domain.NetRate у вкладів, domain.NetOfTax у фондів, нуль
// в ОВДП). Розклад його лише НАЗИВАЄ; друге означення податку тут було б
// тим самим, від чого застерігає розбір у handlers_whatif.go.
func TestBreakdownNamesTaxWithoutRecomputingIt(t *testing.T) {
	rc := rateContext{deval: 6.1}

	dep := rc.breakdown(0.16, 0.1232, money.UAH, "ставка вкладу") // 16% і 23% податку
	if dep.GrossPct != 16 || dep.NetPct != 12.32 {
		t.Fatalf("валова/чиста = %v/%v", dep.GrossPct, dep.NetPct)
	}
	if math.Abs(dep.TaxPct-3.68) > 0.01 {
		t.Fatalf("податок = %v в.п., хочемо 3.68", dep.TaxPct)
	}

	// ОВДП: податку немає взагалі, і рядок про нього не малюється.
	bond := rc.breakdown(0.1712, 0.1712, money.UAH, "до погашення")
	if bond.TaxPct != 0 {
		t.Fatalf("в ОВДП зʼявився податок %v", bond.TaxPct)
	}
}

// TestBreakdownSilentWithoutCPI — ряду цін може ще не бути (перший
// запуск, обірваний бекфіл). Тоді друга лінійка МОВЧИТЬ, а не показує
// нуль: нуль читався б як «інфляції немає».
func TestBreakdownSilentWithoutCPI(t *testing.T) {
	b := rateContext{deval: 6.1}.breakdown(0.16, 0.16, money.UAH, "тест")
	if b.InflationPct != nil || b.RealCPIPct != nil {
		t.Fatalf("без ряду ІСЦ зʼявились числа: %v / %v", b.InflationPct, b.RealCPIPct)
	}
	if b.RealFXPct == 0 {
		t.Fatal("перша лінійка мусить лишатись")
	}
}

// TestBreakdownCPIConvertsForeignBeforeDeflating — саме тут ІСЦ і робить
// те, заради чого заводився.
//
// realYield для валюти повертає ставку НЕТОРКАНОЮ: у моделі зашито, що
// долар купівельну спроможність тримає. Друга лінійка це припущення не
// повторює — вона переводить валютну ставку в гривневі терміни
// знеціненням (гроші прийдуть у долар, витрачати їх тут) і лише тоді
// дефлює цінами.
func TestBreakdownCPIConvertsForeignBeforeDeflating(t *testing.T) {
	rc := rateContext{deval: 6.1, cpi: 8.4, cpiOK: true}

	usd := rc.breakdown(0.045, 0.045, money.USD, "до погашення")
	if usd.RealFXPct != 4.5 {
		t.Fatalf("перша лінійка зрушила: %v", usd.RealFXPct)
	}
	if usd.RealCPIPct == nil {
		t.Fatal("друга лінійка мовчить при наявному ряді")
	}
	// (1.045 × 1.061) / 1.084 − 1 = 2.28%
	if math.Abs(*usd.RealCPIPct-2.28) > 0.02 {
		t.Fatalf("проти цін = %v%%, хочемо ≈2.28%%", *usd.RealCPIPct)
	}

	// Для гривні кроку конвертації немає — лише інший дефлятор.
	uah := rc.breakdown(0.16, 0.16, money.UAH, "ставка вкладу")
	want := round2(((1+0.16)/(1+8.4/100) - 1) * 100)
	if *uah.RealCPIPct != want {
		t.Fatalf("гривня проти цін = %v, хочемо %v", *uah.RealCPIPct, want)
	}
	// Дві лінійки НЕ складаються: кожна відраховує своє, і жодна не є
	// поправкою до іншої.
	if *uah.RealCPIPct == uah.RealFXPct {
		t.Fatal("обидві лінійки дали те саме число — дефлятор не змінився")
	}
}
