// Гроші в JSON і довідкові підписи — спільні для розрахунку й обробників.
//
// Жили в api/httputil.go та handlers_*.go, але їх читає й розрахунок
// (MoneyJSON — у поле рядків розкладки й маршруту, plural — у пояснення
// задач), а розрахунок не має тягнути за собою HTTP-шар.

package engine

import (
	"fmt"
	"sort"

	"github.com/ODDsama/oddinvest/internal/domain"
	money "github.com/Rhymond/go-money"
)

type MoneyJSON struct {
	Amount   string `json:"amount"` // десятковий рядок "995.00"
	Currency string `json:"currency"`
}

func ToMoneyJSON(m *money.Money) MoneyJSON {
	if m == nil {
		return MoneyJSON{Amount: "0", Currency: money.UAH}
	}
	minor := m.Amount()
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	return MoneyJSON{
		Amount:   fmt.Sprintf("%s%d.%02d", sign, minor/100, minor%100),
		Currency: m.Currency().Code,
	}
}

func ParseMoney(amount, currency string) (*money.Money, error) {
	minor, err := domain.ParseDecimalToMinor(amount, currency)
	if err != nil {
		return nil, err
	}
	return money.New(minor, currency), nil
}

// plural — українське відмінювання для довідкових підписів.
func Plural(n int, one, few, many string) string {
	d, h := n%10, n%100
	switch {
	case d == 1 && h != 11:
		return one
	case d >= 2 && d <= 4 && (h < 10 || h >= 20):
		return few
	default:
		return many
	}
}

// Порядок у відповіді детермінований навмисно: інакше два однакові
// запити давали б різний JSON (мапи в Go обходяться випадково), і будь-яке
// порівняння відповідей — очима чи тестом — перетворилось би на гадання.
func sortMoneyJSON(m []MoneyJSON) {
	sort.Slice(m, func(i, j int) bool { return m[i].Currency < m[j].Currency })
}
