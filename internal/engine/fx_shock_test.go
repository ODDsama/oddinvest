package engine

import (
	"testing"

	money "github.com/Rhymond/go-money"
)

// Порожня гіпотеза курсів не вмикає прийом взагалі — і саме це найлегше
// зламати, забувши поле в empty().
func TestFXShockEmptyRatesIsNotAHypothesis(t *testing.T) {
	if !(Hypothetical{}).empty() {
		t.Fatal("порожня гіпотеза мусить лишатись порожньою")
	}
	if (Hypothetical{rates: map[string]int64{money.USD: 400_000}}).empty() {
		t.Error("гіпотеза з курсами вважається порожньою — прийом буде мовчки пропущено")
	}
}
