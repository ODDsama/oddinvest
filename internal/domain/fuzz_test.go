package domain

import (
	"fmt"
	"math"
	"testing"
)

// Гроші з рядка — із форм і виписок. Властивість: розпізнане число,
// записане назад десятковим рядком, розпізнається в те саме (політика
// заокруглення одна — RatToInt64HalfEven) і нічого не губить. Seed-и
// ганяються кожним `go test`; довгий пошук — вручну:
//
//	go test ./internal/domain -run '^$' -fuzz FuzzParseDecimal -fuzztime 60s
func FuzzParseDecimal(f *testing.F) {
	for _, s := range []string{"16.5", "1000", "-0.005", "1e3", "0.125", "999999999999.99", "abc", "",
		"1 234,56", "\u221240\u00a0000,00", "1,2.3", ","} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, err := ParseDecimalToMinor(s, "UAH")
		if err != nil {
			return
		}
		sign, a := "", v
		if a < 0 {
			if a == math.MinInt64 {
				return
			}
			sign, a = "-", -a
		}
		back := fmt.Sprintf("%s%d.%02d", sign, a/100, a%100)
		v2, err := ParseDecimalToMinor(back, "UAH")
		if err != nil || v2 != v {
			t.Fatalf("%q → %d → %q → %d (%v)", s, v, back, v2, err)
		}
	})
}

// XIRR на довільних потоках: або помилка, або скінченне число — жодної
// паніки й жодного NaN, який далі поповз би в документ стану.
func FuzzXIRR(f *testing.F) {
	f.Add(int64(-100000), int64(0), int64(110000), int64(365))
	f.Add(int64(-1), int64(0), int64(1), int64(1))
	f.Add(int64(0), int64(0), int64(0), int64(0))
	f.Fuzz(func(t *testing.T, a1, d1, a2, d2 int64) {
		base := Date("2025-01-01")
		clamp := func(d int64) int {
			if d < 0 {
				d = -d
			}
			return int(d % 20000)
		}
		flows := []Flow{
			{Date: base.AddDays(clamp(d1)), Amount: a1},
			{Date: base.AddDays(clamp(d2)), Amount: a2},
		}
		r, err := XIRR(flows)
		if err == nil && (math.IsNaN(r) || math.IsInf(r, 0)) {
			t.Fatalf("XIRR(%+v) = %v без помилки", flows, r)
		}
	})
}
