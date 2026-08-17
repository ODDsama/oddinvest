package domain

import (
	"testing"

	money "github.com/Rhymond/go-money"
)

// Комісія входить у вартість — і це не дрібниця смаку: тим самим числом
// гаманець списує гроші з рахунку брокера, і тим самим форма покупки
// питає, чи їх вистачає. Забута комісія розвела б ці два числа мовчки.
func TestLotCost(t *testing.T) {
	lot := func(fee *money.Money) Lot {
		return Lot{Qty: 5, PricePerBond: money.New(995_00, money.UAH), Fee: fee}
	}
	cases := []struct {
		name string
		fee  *money.Money
		want int64
	}{
		{"без комісії", nil, 4975_00},
		{"нульова комісія", money.New(0, money.UAH), 4975_00},
		{"з комісією", money.New(25_00, money.UAH), 5000_00},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := LotCost(lot(c.fee))
			if err != nil {
				t.Fatal(err)
			}
			if got.Amount() != c.want {
				t.Errorf("вартість %d, чекали %d", got.Amount(), c.want)
			}
			if got.Currency().Code != money.UAH {
				t.Errorf("валюта %s, чекали UAH", got.Currency().Code)
			}
		})
	}
}

// Комісія в чужій валюті — помилка, а не мовчазне ігнорування: лот у
// гривні з комісією в доларах означає зіпсований запис, і списувати з
// рахунку «щось приблизне» гірше, ніж не списати нічого.
func TestLotCostRejectsMixedCurrency(t *testing.T) {
	_, err := LotCost(Lot{Qty: 1, PricePerBond: money.New(100_00, money.UAH),
		Fee: money.New(1_00, money.USD)})
	if err == nil {
		t.Fatal("комісія в іншій валюті мала дати помилку")
	}
}
