package api

import (
	"math"
	"testing"

	money "github.com/Rhymond/go-money"
)

// TestRealNominalRoundTrip — той самий тест, який коментар при
// nominalYield обіцяв, а репозиторій не мав: пару realYield/nominalYield
// не стерегло ніщо.
//
// Ціна розходження конкретна. nominalYield годує поріг перекладання
// (handlers_switch.go): реальна ставка альтернативи вертається в
// номінальну, щоб дисконтувати графік виплат у валюті паперу. Помилка на
// цьому кроці не падає й не виглядає помилкою — вона просто зсуває
// поріг, тобто радить продати папір, який продавати не треба.
func TestRealNominalRoundTrip(t *testing.T) {
	rates := []float64{-0.05, 0, 0.0123, 0.045, 0.1655, 0.31, 1.0}
	devals := []float64{0, 0.5, 6.1, 12.4, 34.0}
	curs := []string{money.UAH, money.USD, money.EUR}

	for _, cur := range curs {
		for _, d := range devals {
			for _, y := range rates {
				got := nominalYield(realYield(y, cur, d), cur, d)
				if math.Abs(got-y) > 1e-12 {
					t.Fatalf("%s, знецінення %.1f%%: %v -> %v (розбіжність %.3g)",
						cur, d, y, got, got-y)
				}
			}
		}
	}
}

// TestRealYieldTouchesOnlyHryvnia — друга половина тієї самої пари, і
// водночас найважливіше припущення всієї моделі, записане вголос: у
// валюті дохідність не дефлюється взагалі, бо застосунок вважає, що
// долар купівельну спроможність тримає.
//
// Саме це припущення й перевіряє ІСЦ (domain/cpi.go), тому воно мусить
// бути під тестом: якщо воно колись зміниться, це має статись свідомо, а
// не як побічний наслідок правки формули.
func TestRealYieldTouchesOnlyHryvnia(t *testing.T) {
	const y, deval = 0.045, 12.0

	if got := realYield(y, money.USD, deval); got != y {
		t.Fatalf("долар продефльовано: %v", got)
	}
	if got := realYield(y, money.EUR, deval); got != y {
		t.Fatalf("євро продефльовано: %v", got)
	}
	uah := realYield(y, money.UAH, deval)
	if uah >= y {
		t.Fatalf("гривню не продефльовано: %v", uah)
	}
	want := (1+y)/(1+deval/100) - 1
	if math.Abs(uah-want) > 1e-12 {
		t.Fatalf("гривня продефльована не за формулою: %v проти %v", uah, want)
	}
}
