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
