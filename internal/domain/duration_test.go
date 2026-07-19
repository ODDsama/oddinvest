package domain

import (
	"math"
	"testing"
)

// Для безкупонного паперу дюрація Маколея = строк до погашення.
func TestDurationZeroCoupon(t *testing.T) {
	mac, mod, pv := Duration([]CashPoint{{Years: 5, Amount: 1000}}, 0.15)
	if math.Abs(mac-5) > 1e-9 {
		t.Errorf("Маколей безкупонного = строк 5, маємо %.6f", mac)
	}
	if math.Abs(mod-5/1.15) > 1e-9 {
		t.Errorf("модифікована = D/(1+y), маємо %.6f", mod)
	}
	if want := 1000 / math.Pow(1.15, 5); math.Abs(pv-want) > 1e-9 {
		t.Errorf("PV %.4f, хочемо %.4f", pv, want)
	}
}

// Купонний папір: дюрація СТРОГО менша за строк до погашення,
// бо частину грошей отримуємо раніше.
func TestDurationCouponLessThanMaturity(t *testing.T) {
	pts := []CashPoint{
		{Years: 1, Amount: 150}, {Years: 2, Amount: 150},
		{Years: 3, Amount: 150}, {Years: 3, Amount: 1000},
	}
	mac, mod, pv := Duration(pts, 0.15)
	if mac <= 0 || mac >= 3 {
		t.Fatalf("дюрація купонного має бути в (0,3), маємо %.4f", mac)
	}
	if mod >= mac {
		t.Errorf("модифікована має бути менша за Маколея: %.4f vs %.4f", mod, mac)
	}
	// папір за номіналом зі ставкою = купону: PV ≈ 1000
	if math.Abs(pv-1000) > 0.01 {
		t.Errorf("PV папера за номіналом ≈1000, маємо %.4f", pv)
	}
}

func TestDurationEmpty(t *testing.T) {
	if mac, mod, pv := Duration(nil, 0.15); mac != 0 || mod != 0 || pv != 0 {
		t.Errorf("порожні потоки -> нулі, маємо %v %v %v", mac, mod, pv)
	}
}

func TestPriceChangePct(t *testing.T) {
	// D_mod=3, ставка +2 п.п. -> ціна ≈ −6%
	if got := PriceChangePct(3, 2); math.Abs(got+6) > 1e-9 {
		t.Errorf("зміна ціни %.4f, хочемо -6", got)
	}
	// падіння ставки -> зростання ціни
	if got := PriceChangePct(3, -1); math.Abs(got-3) > 1e-9 {
		t.Errorf("зміна ціни %.4f, хочемо +3", got)
	}
}
