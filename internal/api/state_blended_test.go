package api

import (
	"math"
	"testing"
)

// Зведена дохідність бере ВСІ чотири види, а не дві половини.
//
// Це той самий портфель, на якому «Дохідність портфеля» доти показувала
// число, зроблене лише з ОВДП і фондів: вклади й НПФ мовчки випадали, хоч
// їхні ставки вже пораховані поруч.
func TestBlendYieldTakesAllFourKinds(t *testing.T) {
	nom, real, base, basis := blendYield([]yieldPart{
		{Pct: 10, Real: 5, Weight: 100, Basis: "до погашення"},
		{Pct: 20, Real: 15, Weight: 100, Basis: "до погашення"},
		{Pct: 30, Real: 25, Weight: 100, Basis: "до погашення"},
		{Pct: 40, Real: 35, Weight: 100, Basis: "до погашення"},
	})
	if nom != 25 || real != 20 {
		t.Errorf("очікували 25/20, маємо %v/%v", nom, real)
	}
	if base != 400 {
		t.Errorf("база мала бути 400, маємо %v", base)
	}
	if basis != "до погашення" {
		t.Errorf("основа одна на всіх — очікували «до погашення», маємо %q", basis)
	}
}

// Вага РІЗНА в кожного виду, і саме вона вирішує. Дрібний вид із гучним
// відсотком не має тягнути зведену на себе.
func TestBlendYieldWeighsByMoneyNotByCount(t *testing.T) {
	nom, _, base, _ := blendYield([]yieldPart{
		{Pct: 10, Real: 10, Weight: 900, Basis: "до погашення"},
		{Pct: 100, Real: 100, Weight: 100, Basis: "до погашення"},
	})
	// (10×900 + 100×100) / 1000 = 19, а не 55.
	if nom != 19 {
		t.Errorf("зважена мала дати 19, маємо %v", nom)
	}
	if base != 1000 {
		t.Errorf("база 1000, маємо %v", base)
	}
}

// Вид БЕЗ ваги не потрапляє ні в чисельник, ні в знаменник — і база це
// показує. Вклад без заданої ставки саме такий: нуль там означав би не
// «нульова дохідність», а «невідома».
func TestBlendYieldSkipsWeightlessKind(t *testing.T) {
	nom, _, base, basis := blendYield([]yieldPart{
		{Pct: 10, Real: 8, Weight: 500, Basis: "до погашення"},
		{Pct: 99, Real: 99, Weight: 0, Basis: "вигадана основа"},
	})
	if nom != 10 {
		t.Errorf("вид без ваги мав лишитись поза числом, маємо %v", nom)
	}
	if base != 500 {
		t.Errorf("база мала лишитись 500, маємо %v", base)
	}
	if basis != "до погашення" {
		t.Errorf("основа вида без ваги не мала потрапити в суміш, маємо %q", basis)
	}
}

// Порожній набір — не нуль, а мовчання: нуль читався б як «портфель не
// заробляє», а чесна відповідь тут «нема з чого рахувати».
func TestBlendYieldSilentWhenNothingToWeigh(t *testing.T) {
	nom, real, base, basis := blendYield(nil)
	if nom != 0 || real != 0 || base != 0 || basis != "" {
		t.Errorf("очікували повне мовчання, маємо %v/%v/%v/%q", nom, real, base, basis)
	}
}

// Суміш обіцянки з виміром називається вголос. ОВДП і вклад дають ставку,
// зафіксовану наперед; фонд — факт по прожитому, і зводити їх мовчки
// означало б видати суміш за однорідне число.
func TestBlendYieldNamesMixedBasis(t *testing.T) {
	_, _, _, basis := blendYield([]yieldPart{
		{Pct: 16, Real: 10, Weight: 1000, Basis: "до погашення"},
		{Pct: 12, Real: 6, Weight: 500, Basis: "дивіденди + зміна ціни"},
	})
	if basis != "різні основи" {
		t.Errorf("очікували «різні основи», маємо %q", basis)
	}
}

// Порожня основа доданка суміші не псує: вид, який не назвав своєї основи,
// не робить решту «різними».
func TestBlendYieldIgnoresEmptyBasis(t *testing.T) {
	_, _, _, basis := blendYield([]yieldPart{
		{Pct: 16, Real: 10, Weight: 1000, Basis: "до погашення"},
		{Pct: 12, Real: 6, Weight: 500, Basis: ""},
	})
	if basis != "до погашення" {
		t.Errorf("порожня основа мала промовчати, маємо %q", basis)
	}
}

// РЕГРЕСІЯ. Портфель лише з ОВДП і фондів мусить дати те саме число, що й
// доти, — інакше в історії сенсора Home Assistant «Дохідність портфеля»
// зʼявиться сходинка на порожньому місці.
func TestBlendYieldUnchangedForBondsAndFundsOnly(t *testing.T) {
	// Числа з бойових даних власника: ОВДП 16.26% на 22 000 ₴,
	// фонди 12.36% на 13 253.33 ₴ → 14.79%.
	nom, _, _, _ := blendYield([]yieldPart{
		{Pct: 16.26, Real: 10.16, Weight: 22000, Basis: "до погашення"},
		{Pct: 12.36, Real: 6.45, Weight: 13253.33, Basis: "різні основи"},
	})
	if math.Abs(nom-14.79) > 0.01 {
		t.Errorf("на портфелі без вкладів і НПФ число мало лишитись 14.79, маємо %v", nom)
	}
}
