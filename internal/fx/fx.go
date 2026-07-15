// Package fx — єдина точка конвертації валют у проєкті.
// Курси зберігаються цілими ×10⁴ (44.1234 -> 441234), математика —
// через big.Rat із банківським заокругленням. Конвертовані значення
// призначені лише для відображення й агрегатів (грн-еквівалент);
// у БД суми завжди лежать у валюті операції.
package fx

import (
	"fmt"
	"math/big"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

const RateScale = 10000 // 4 знаки курсу НБУ

// Rates — курси до гривні, ×10⁴. Ключ — код валюти ("USD").
type Rates map[string]int64

// ToUAH конвертує суму у гривню за курсом ×10⁴.
// UAH повертається як є.
func ToUAH(m *money.Money, rates Rates) (*money.Money, error) {
	code := m.Currency().Code
	if code == money.UAH {
		return m, nil
	}
	rateE4, ok := rates[code]
	if !ok || rateE4 <= 0 {
		return nil, fmt.Errorf("немає курсу для %s", code)
	}
	r := new(big.Rat).SetInt64(m.Amount())
	r.Mul(r, big.NewRat(rateE4, RateScale))
	minor, err := domain.RatToInt64HalfEven(r)
	if err != nil {
		return nil, err
	}
	return money.New(minor, money.UAH), nil
}

// ParseRateE4 — парсинг десяткового курсу ("44.1234") у ціле ×10⁴
// без float64, із банківським заокругленням зайвих знаків.
func ParseRateE4(s string) (int64, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, fmt.Errorf("невалідний курс %q", s)
	}
	r.Mul(r, new(big.Rat).SetInt64(RateScale))
	return domain.RatToInt64HalfEven(r)
}
