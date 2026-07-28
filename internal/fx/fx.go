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

// FromUAH — зворотний бік ToUAH: скільки це у валюті cur.
//
// Потрібна, бо гривню ділили назад на курс float64-ом просто в місцях
// виклику — повз єдину точку конвертації й повз банківське заокруглення.
// Через це, зокрема, deficit_uah і deficit_native у картці ребалансу не
// були точно взаємними.
func FromUAH(m *money.Money, cur string, rates Rates) (*money.Money, error) {
	if cur == money.UAH {
		return m, nil
	}
	if code := m.Currency().Code; code != money.UAH {
		return nil, fmt.Errorf("FromUAH чекає гривню, отримала %s", code)
	}
	rateE4, ok := rates[cur]
	if !ok || rateE4 <= 0 {
		return nil, fmt.Errorf("немає курсу для %s", cur)
	}
	r := new(big.Rat).SetInt64(m.Amount())
	r.Mul(r, big.NewRat(RateScale, rateE4))
	minor, err := domain.RatToInt64HalfEven(r)
	if err != nil {
		return nil, err
	}
	return money.New(minor, cur), nil
}

// RateMajor — курс як звичайне число (₴ за одиницю валюти).
//
// Для випадків, де потрібен САМЕ курс, а не конвертація: підпис «по
// 44.12/$», поле rates документа. Щоб ділити на RateScale не доводилось
// самотужки в шести місцях — саме так масштаб ×10⁴ і розповзався за межі
// цього пакета.
func RateMajor(cur string, rates Rates) (float64, bool) {
	if cur == money.UAH {
		return 1, true
	}
	e4, ok := rates[cur]
	if !ok || e4 <= 0 {
		return 0, false
	}
	return float64(e4) / RateScale, true
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
