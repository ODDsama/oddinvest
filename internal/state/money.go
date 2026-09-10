package state

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Money — сума в копійках плюс валюта. На дроті — число, як і доти.
//
// Доти документ стану й відповіді обробників тримали гроші голим float64,
// і гривня в них малась на увазі. Триста сорок п'ять полів, з яких сто
// шістдесят вісім носили суфікс _uah, три десятки гривневих без суфікса й
// два десятки натуральних (собівартість фонду в доларах, ціль у євро) —
// і відрізнити одні від інших можна було лише пам'яттю. Двісті двадцять
// два розсипані `float64(x) / 100` по сорока трьох файлах — та сама
// причина: конвертувати з копійок доводилось у кожному місці окремо, бо
// самого поняття «гроші» на цій межі не було. Домен і сховище давно
// працюють на go-money (копійки + код валюти), тобто патерн Фаулера в
// проєкті є — він просто обривався на порозі документа.
//
// Цей тип доносить його до порога. Сума лишається цілою в копійках до
// самого MarshalJSON, валюта їде разом із числом, а складати долар із
// гривнею тип не дає. У JSON іде те саме число з двома знаками, що й
// доти: контракт із Home Assistant і веб-застосунком не міняється, golden
// документа лишається байт у байт. Валюта на дроті — на рівні документа
// (поле currency), а не при кожному числі: інакше довелося б переписати
// кожного читача заради інформації, яка для всіх сум документа одна.
//
// Нульове значення — «0, валюта ще не названа». Воно навмисно складається
// з будь-чим: акумулятор `var total Money` мусить приймати перший доданок
// у його валюті, не змушуючи називати її наперед. Скрізь, де валюта
// невідома й сума не нуль, — це гривня (Currency повертає UAH).
type Money struct {
	minor int64
	cur   string
}

// UAH — сума в гривні з копійок.
func UAH(minor int64) Money { return Money{minor: minor, cur: money.UAH} }

// Minor — сума з копійок у названій валюті.
func Minor(minor int64, cur string) Money { return Money{minor: minor, cur: cur} }

// Of — з домену. nil читається як нуль без валюти.
func Of(m *money.Money) Money {
	if m == nil {
		return Money{}
	}
	return Money{minor: m.Amount(), cur: m.Currency().Code}
}

// Major — з мажорних одиниць (12345.67) у копійки.
//
// Приймає float, бо частина документа рахується у float (відсоток від
// суми, зважене середнє) — і це єдине місце, де такий результат стає
// грішми. Рівно тут і заокруглюється: 0.29·100 у float дорівнює
// 28.999999999999996, і `int64(v*100)` давав би 28 копійок — саме так
// добовий знімок доти обрізав копійку на кожній колонці.
//
// math.Round, а не half-to-even, як у domain.RatToInt64HalfEven: та
// політика — для точних раціональних, а тут float, у якому «рівно
// половина» майже ніколи не є половиною. Це та сама дія, що робив round2
// на кожному полі доти, — і саме тому golden документа лишається байт у
// байт, а не зсувається на копійку там, де float випадково дав .5.
func Major(v float64, cur string) Money {
	return Money{minor: int64(math.Round(v * 100)), cur: cur}
}

// Major — у мажорних одиницях, для арифметики, яка ще не стала грішми.
func (m Money) Major() float64 { return float64(m.minor) / 100 }

// Minor — копійки.
func (m Money) Minor() int64 { return m.minor }

// Currency — код валюти; неназвана — гривня.
func (m Money) Currency() string {
	if m.cur == "" {
		return money.UAH
	}
	return m.cur
}

// IsZero — нульова сума. Саме цей метод читає encoding/json для тега
// omitzero, тож нуль у названій валюті випадає з JSON так само, як доти
// випадав нульовий float під omitempty.
func (m Money) IsZero() bool { return m.minor == 0 }

// Money — назад у домен.
func (m Money) Money() *money.Money { return money.New(m.minor, m.Currency()) }

// same — валюта другого доданка, або паніка, якщо валюти різні.
//
// Паніка, а не помилка: скласти долар із гривнею — помилка програміста, і
// вона мусить бути видна на першому ж тесті, а не дійти до документа
// правдоподібним числом. Нуль без валюти складається з будь-чим (див.
// шапку типу).
func (m Money) same(o Money, op string) string {
	switch {
	case m.cur == "" || m.minor == 0 && o.cur != "":
		return o.cur
	case o.cur == "" || o.minor == 0:
		return m.cur
	case m.cur != o.cur:
		panic(fmt.Sprintf("state.Money: %s %s і %s", op, m.cur, o.cur))
	}
	return m.cur
}

// Add — сума. Різні валюти — паніка.
func (m Money) Add(o Money) Money {
	return Money{minor: m.minor + o.minor, cur: m.same(o, "додати")}
}

// Sub — різниця. Різні валюти — паніка.
func (m Money) Sub(o Money) Money {
	return Money{minor: m.minor - o.minor, cur: m.same(o, "відняти")}
}

// Neg — зі зміненим знаком.
func (m Money) Neg() Money { return Money{minor: -m.minor, cur: m.cur} }

// Mul — помножити на число, до копійки (та сама політика, що в Major).
func (m Money) Mul(f float64) Money {
	return Money{minor: int64(math.Round(float64(m.minor) * f)), cur: m.cur}
}

// In — та сама сума в іншій валюті за курсом perUnit: скільки ЦІЄЇ валюти
// коштує одиниця code. Округлення до копійки — те саме, що в Major.
//
// Це не арифметика домену (там обмін іде через fx.ToUAH/FromUAH у
// копійках ×10⁴), а шар презентації: документ уже порахований у гривні,
// і питання лише в тому, якою одиницею його показати.
func (m Money) In(code string, perUnit float64) Money {
	return Money{minor: int64(math.Round(float64(m.minor) / perUnit)), cur: code}
}

// Cmp — −1, 0, +1. Різні валюти — паніка.
func (m Money) Cmp(o Money) int {
	m.same(o, "порівняти")
	switch {
	case m.minor < o.minor:
		return -1
	case m.minor > o.minor:
		return 1
	}
	return 0
}

// String — для тестів і логів: «12345.67 UAH».
func (m Money) String() string {
	return strconv.FormatFloat(m.Major(), 'f', 2, 64) + " " + m.Currency()
}

// MarshalJSON — число в мажорних одиницях, тим самим текстом, що
// encoding/json дає для float64: 100 → 100, 12345.67 → 12345.67.
//
// Формат 'f' і найкоротший запис — рівно те, що робить стандартний
// кодувальник для float у діапазоні грошей (він переходить на 'e' лише за
// 1e21, а сум таких немає). Це й тримає контракт незмінним.
func (m Money) MarshalJSON() ([]byte, error) {
	return strconv.AppendFloat(nil, m.Major(), 'f', -1, 64), nil
}

// UnmarshalJSON — з числа (книжкова валюта, тобто гривня) або з об'єкта
// {amount, currency}, як у списках операцій.
//
// Потрібен тестам і фікстурам: сам документ ніхто не читає назад у Go, але
// сотня тестів розбирає відповідь у state.Doc, щоб дістати одне поле.
func (m *Money) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null":
		*m = Money{}
		return nil
	case strings.HasPrefix(s, "{"):
		var o struct {
			Amount   json.Number `json:"amount"`
			Currency string      `json:"currency"`
		}
		if err := json.Unmarshal(b, &o); err != nil {
			return err
		}
		cur := o.Currency
		if cur == "" {
			cur = money.UAH
		}
		minor, err := domain.ParseDecimalToMinor(o.Amount.String(), cur)
		if err != nil {
			return err
		}
		*m = Money{minor: minor, cur: cur}
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("state.Money: не число: %s", s)
	}
	*m = Major(f, "")
	return nil
}
