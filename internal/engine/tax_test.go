package engine

import (
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

// Дірка означає ПРОПУЩЕНУ виплату, а не місяць входу у фонд.
//
// Фонд платить за реєстром, складеним раніше за день виплати, тож той,
// хто зайшов усередині місяця, законно не отримує нічого. Перевірено на
// виписці: сертифікати куплені 4 червня 2026-го, червневої виплати немає,
// перша прийшла 10 липня — і перша версія цієї перевірки той червень
// позначила діркою.
//
// Тест кличе fundCoverage напряму: дати тут фіксовані, і крізь HTTP вони
// залежали б від того, яке сьогодні число.
func TestFundCoverageGapNeedsFullCycle(t *testing.T) {
	ops := []domain.FundOp{
		{Date: "2026-06-04", Fund: "Inzhur REIT", Kind: domain.FundBuy,
			Qty: 11, Amount: 12_078, Currency: money.UAH},
	}
	refs := []store.Fund{{Name: "Inzhur REIT", Currency: money.UAH, PayoutDay: 10}}

	_, gaps := fundCoverage(ops, refs, "2026-01-01", "2026-12-31", "2026-09-03")
	months := map[string]bool{}
	for _, g := range gaps {
		for _, m := range g.Months {
			months[m] = true
		}
	}
	if months["2026-06"] {
		t.Errorf("місяць входу не є діркою: %+v", gaps)
	}
	// А ось липень і далі — вже повний цикл: на 10 червня сертифікати вже
	// були, тож 10 липня виплата мала бути.
	if !months["2026-07"] {
		t.Errorf("липень мав бути діркою — виплати немає, а фонд протримали цикл: %+v", gaps)
	}
}
