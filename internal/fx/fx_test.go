package fx

import (
	"testing"

	money "github.com/Rhymond/go-money"
)

func TestToUAH(t *testing.T) {
	rates := Rates{"USD": 441234} // 44.1234
	// $1000.00 -> 44123.40 грн
	got, err := ToUAH(money.New(100000, money.USD), rates)
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount() != 4412340 || got.Currency().Code != money.UAH {
		t.Errorf("ToUAH = %d %s", got.Amount(), got.Currency().Code)
	}
}

func TestToUAHPassthrough(t *testing.T) {
	got, err := ToUAH(money.New(12345, money.UAH), nil)
	if err != nil || got.Amount() != 12345 {
		t.Errorf("UAH має проходити без змін: %v %v", got, err)
	}
}

func TestToUAHMissingRate(t *testing.T) {
	if _, err := ToUAH(money.New(100, money.EUR), Rates{"USD": 441234}); err == nil {
		t.Error("очікували помилку без курсу EUR")
	}
}

func TestToUAHBankersRounding(t *testing.T) {
	// 1 цент × 44.1250 = 0.441250 грн = 44.125 коп -> half-even -> 44 коп
	got, err := ToUAH(money.New(1, money.USD), Rates{"USD": 441250})
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount() != 44 {
		t.Errorf("банківське заокруглення: %d, хочемо 44", got.Amount())
	}
}

func TestParseRateE4(t *testing.T) {
	cases := map[string]int64{"44.1234": 441234, "44": 440000, "41.99995": 420000, "41.99994": 419999}
	for in, want := range cases {
		got, err := ParseRateE4(in)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("ParseRateE4(%s) = %d, хочемо %d", in, got, want)
		}
	}
}

// FromUAH — точний зворотний бік ToUAH, а не «поділити на курс».
func TestFromUAH(t *testing.T) {
	r := Rates{"USD": 441234, "EUR": 480000}

	// Гривня лишається гривнею без жодних дій.
	got, err := FromUAH(money.New(12345, money.UAH), money.UAH, r)
	if err != nil || got.Amount() != 12345 {
		t.Fatalf("UAH мала пройти як є: %v %v", got, err)
	}

	// 44123.40 ₴ = рівно $1000.00.
	got, err = FromUAH(money.New(4412340, money.UAH), money.USD, r)
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount() != 100000 || got.Currency().Code != money.USD {
		t.Errorf("44123.40 ₴ = %v, чекали $1000.00", got.Display())
	}

	// Кругообіг: у гривню й назад дає ту саму суму.
	orig := money.New(250000, money.EUR) // €2500
	uah, err := ToUAH(orig, r)
	if err != nil {
		t.Fatal(err)
	}
	back, err := FromUAH(uah, money.EUR, r)
	if err != nil {
		t.Fatal(err)
	}
	if back.Amount() != orig.Amount() {
		t.Errorf("туди-назад дало %d замість %d", back.Amount(), orig.Amount())
	}

	// Заокруглення банківське, а не «відкинути дробову»: саме цим
	// відрізнявся ручний цілочисельний поділ у бенчмарку.
	got, err = FromUAH(money.New(100, money.UAH), money.USD, r) // 1.00 ₴
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount() != 2 { // 1/44.1234 = 0.02266… → 2 центи
		t.Errorf("1.00 ₴ = %d центів, чекали 2", got.Amount())
	}

	if _, err := FromUAH(money.New(100, money.UAH), "GBP", r); err == nil {
		t.Error("без курсу мала бути помилка")
	}
	if _, err := FromUAH(money.New(100, money.USD), money.EUR, r); err == nil {
		t.Error("FromUAH мала відмовитись від негривневої суми")
	}
}

// RateMajor віддає курс, а не конвертує. Гривня — одиниця.
func TestRateMajor(t *testing.T) {
	r := Rates{"USD": 441234}
	if v, ok := RateMajor(money.UAH, r); !ok || v != 1 {
		t.Errorf("UAH = %v (%v), чекали 1", v, ok)
	}
	if v, ok := RateMajor(money.USD, r); !ok || v != 44.1234 {
		t.Errorf("USD = %v (%v), чекали 44.1234", v, ok)
	}
	if _, ok := RateMajor("GBP", r); ok {
		t.Error("для невідомої валюти мало бути ok=false")
	}
}
