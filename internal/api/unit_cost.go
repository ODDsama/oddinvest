// Ціна ОДНОГО кроку покупки — в одному місці на весь застосунок.
//
// Крок різний за природою: папір купують штуками, сертифікат теж
// штуками, але за ринковою ціною, вклад поповнюють сумою відкриття.
// Спільне в них те, що це найменша неподільна покупка, і саме нею
// міряється «на скільки вистачить грошей».
//
// Чому не два обчислення. Помічник реінвесту показує ціну в пораді,
// кошик рахує з неї «що станеться, якщо куплю». Якби кожен рахував сам,
// той самий папір мав би на екрані дві ціни, і жодна не була б очевидно
// правильною — цей застосунок уже проходив таке з частками капіталу
// (див. state.Capital) і з арифметикою помічника (handlers_reinvest.go).

package api

import (
	"math"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// bondUnitCost — скільки сьогодні коштує один папір: номінал плюс НКД.
//
// Ціни в довіднику НБУ немає, тож «за номіналом» — чесне наближення, а
// не вимірювання. Принаймні НКД воно враховує, і той сплачується реально.
// Про те, чому ми НЕ виводимо ціну з аукціонного рівня, сказано в README
// («Чого тут свідомо немає»).
func bondUnitCost(b domain.Bond, pays []domain.Payment, today domain.Date) *money.Money {
	cost := b.Nominal
	acc, err := domain.EstimateAccrued(pays, b.ISIN, today)
	if err != nil || acc == nil {
		return cost
	}
	sum, err := cost.Add(acc)
	if err != nil {
		return cost
	}
	return sum
}

// fundUnitCost — один сертифікат за останньою відомою ціною. Нуль
// означає «ціни немає», і купувати за неї не можна нічого.
func fundUnitCost(lastPrice float64, currency string) *money.Money {
	if currency == "" {
		currency = money.UAH
	}
	minor := int64(math.Round(lastPrice * 100))
	if minor < 0 {
		minor = 0
	}
	return money.New(minor, currency)
}
