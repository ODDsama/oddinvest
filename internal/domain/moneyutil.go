package domain

import (
	"fmt"
	"math"
	"math/big"
	"strings"
	"unicode"

	money "github.com/Rhymond/go-money"
)

// ParseDecimalToMinor конвертує десятковий рядок ("16.5", "1000")
// у мінорні одиниці валюти (копійки/центи) без проходу через float64.
// Зайві знаки після коми заокруглюються за банківським правилом.
func ParseDecimalToMinor(s string, code string) (int64, error) {
	cur := money.GetCurrency(code)
	if cur == nil {
		return 0, fmt.Errorf("невідома валюта %q", code)
	}
	return ParseDecimalToScale(s, cur.Fraction)
}

// ParseDecimalToScale — те саме, але масштаб задається прямо, а не
// виводиться з валюти.
//
// Потрібне там, де одиниця не грошова: ЧВОПА й одиниці пенсійних активів
// живуть на ×10⁶, тобто на шести знаках проти двох у копійки (див.
// domain/npf.go). Друга реалізація парсера тут була б третім місцем, де
// вирішується, як заокруглювати десятковий дріб, — а політика заокруглення
// в проєкті ОДНА, і саме тому обидві функції зводяться до
// RatToInt64HalfEven.
func ParseDecimalToScale(s string, decimals int) (int64, error) {
	if decimals < 0 {
		return 0, fmt.Errorf("масштаб не може бути відʼємним: %d", decimals)
	}
	norm, err := normalizeDecimal(s)
	if err != nil {
		return 0, err
	}
	r, ok := new(big.Rat).SetString(norm)
	if !ok {
		return 0, fmt.Errorf("невалідне десяткове число %q", s)
	}
	pow := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	r.Mul(r, pow)
	return RatToInt64HalfEven(r)
}

// normalizeDecimal — число так, як його пише людина в Україні, у
// вигляд, який розуміє big.Rat.
//
// Десятковий роздільник на українській клавіатурі — кома, а сам
// застосунок показує суми з пробілами тисяч (toLocaleString("uk") ставить
// нерозривний, U+00A0 чи U+202F) і знаком «−» (U+2212). Скопійоване з
// екрана число доти відхилялось, і кожна форма мала нормалізувати його
// сама — звірка так і робила, решта ні. Тут, один раз для всіх входів.
//
// Кома РАЗОМ із крапкою — відмова, а не вгадування: «1,234.5» і
// «1.234,5» означають різне залежно від того, хто писав.
func normalizeDecimal(s string) (string, error) {
	out := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return -1
		case r == '−': // «−», яким format.js пише від'ємні суми
			return '-'
		}
		return r
	}, s)
	if strings.Contains(out, ",") {
		if strings.Contains(out, ".") {
			return "", fmt.Errorf("неоднозначне число %q: і кома, і крапка", s)
		}
		out = strings.Replace(out, ",", ".", 1)
	}
	return out, nil
}

// RatToInt64HalfEven — заокруглення big.Rat до цілого за правилом
// half-to-even (банківське). Єдина політика заокруглення в проєкті.
func RatToInt64HalfEven(r *big.Rat) (int64, error) {
	num, den := r.Num(), r.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	if rem.Sign() == 0 {
		if !q.IsInt64() {
			return 0, fmt.Errorf("переповнення int64: %s", r.RatString())
		}
		return q.Int64(), nil
	}
	twice := new(big.Int).Abs(rem)
	twice.Lsh(twice, 1)
	cmp := twice.Cmp(den)
	roundAway := cmp > 0
	if cmp == 0 { // рівно половина -> до парного
		roundAway = q.Bit(0) == 1
	}
	if roundAway {
		if r.Sign() >= 0 {
			q.Add(q, big.NewInt(1))
		} else {
			q.Sub(q, big.NewInt(1))
		}
	}
	if !q.IsInt64() {
		return 0, fmt.Errorf("переповнення int64: %s", r.RatString())
	}
	return q.Int64(), nil
}

// Apportion — частина total, що припадає на part паперів із whole,
// із банківським заокругленням (та сама політика, що й усюди в проєкті).
// Порожній/нульовий total, whole<=0 чи part<=0 -> нуль у валюті total.
func Apportion(total *money.Money, part, whole int64) (*money.Money, error) {
	code := money.UAH
	if total != nil {
		code = total.Currency().Code
	}
	if total == nil || total.IsZero() || whole <= 0 || part <= 0 {
		return money.New(0, code), nil
	}
	r := new(big.Rat).SetFrac(
		new(big.Int).Mul(big.NewInt(total.Amount()), big.NewInt(part)),
		big.NewInt(whole),
	)
	minor, err := RatToInt64HalfEven(r)
	if err != nil {
		return nil, err
	}
	return money.New(minor, code), nil
}

// SumSameCurrency додає суми, вимагаючи єдину валюту.
func SumSameCurrency(items ...*money.Money) (*money.Money, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("порожній список сум")
	}
	acc := items[0]
	var err error
	for _, m := range items[1:] {
		acc, err = acc.Add(m)
		if err != nil {
			return nil, err
		}
	}
	return acc, nil
}

// Round2 — округлення до 2 знаків для довідкових (не облікових) чисел:
// відсотків, ставок, мажорних сум у документі.
func Round2(v float64) float64 { return math.Round(v*100) / 100 }
