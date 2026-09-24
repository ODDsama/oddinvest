package domain

import (
	"math/big"
	"testing"
)

func TestParseDecimalToMinor(t *testing.T) {
	cases := []struct {
		in   string
		code string
		want int64
	}{
		{"1000", "UAH", 100000},
		{"16.5", "UAH", 1650},
		{"16.55", "UAH", 1655},
		{"0.01", "UAH", 1},
		{"1057.9", "UAH", 105790},
		{"41.9", "USD", 4190},
		// банківське заокруглення третього знака
		{"0.005", "UAH", 0},  // 0.5 коп -> до парного 0
		{"0.015", "UAH", 2},  // 1.5 коп -> до парного 2
		{"0.025", "UAH", 2},  // 2.5 коп -> до парного 2
		{"0.0251", "UAH", 3}, // > половини -> вгору
	}
	for _, c := range cases {
		got, err := ParseDecimalToMinor(c.in, c.code)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseDecimalToMinor(%s,%s) = %d, хочемо %d", c.in, c.code, got, c.want)
		}
	}
}

func TestParseDecimalToMinorErrors(t *testing.T) {
	if _, err := ParseDecimalToMinor("abc", "UAH"); err == nil {
		t.Error("очікували помилку на нечисловому вводі")
	}
	if _, err := ParseDecimalToMinor("10", "XXX"); err == nil {
		t.Error("очікували помилку на невідомій валюті")
	}
}

func TestRatToInt64HalfEvenNegative(t *testing.T) {
	cases := []struct {
		num, den int64
		want     int64
	}{
		{-5, 2, -2},   // -2.5 -> до парного -2
		{-7, 2, -4},   // -3.5 -> до парного -4
		{-51, 10, -5}, // -5.1 -> -5
		{-59, 10, -6}, // -5.9 -> -6
	}
	for _, c := range cases {
		got, err := RatToInt64HalfEven(big.NewRat(c.num, c.den))
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("HalfEven(%d/%d) = %d, хочемо %d", c.num, c.den, got, c.want)
		}
	}
}

// Число, як його пише людина в Україні, приймається.
//
// Десятковий роздільник на українській клавіатурі телефона — кома, а сам
// застосунок показує суми з пробілом тисяч («40 000,00») і знаком «−».
// Скопійоване з екрана число доти відхилялось як «невалідне», і кожна
// форма мала б нормалізувати його сама (звірка так і робила). Тепер —
// тут, один раз для всіх.
func TestParseDecimalAcceptsLocalFormat(t *testing.T) {
	cases := map[string]int64{
		"1234,56":       123456,
		"40 000,00":     4000000,
		"40\u00a0000,5": 4000050,
		"40\u202f000":   4000000,
		"\u221212,30":   -1230,
		" 16.5 ":        1650,
	}
	for in, want := range cases {
		got, err := ParseDecimalToMinor(in, "UAH")
		if err != nil || got != want {
			t.Errorf("%q → %d (%v), чекали %d", in, got, err, want)
		}
	}
	// Кома й крапка разом — неоднозначно («1,234.5» чи «1.234,5»?), тож
	// відмова, а не вгадування.
	for _, bad := range []string{"1,234.50", "1.234,50", "1,2,3"} {
		if _, err := ParseDecimalToMinor(bad, "UAH"); err == nil {
			t.Errorf("%q мало бути відхилене", bad)
		}
	}
}
