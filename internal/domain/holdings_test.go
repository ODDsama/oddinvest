package domain

import (
	"testing"

	money "github.com/Rhymond/go-money"
)

// TestHoldingsRemainingMatchesRemainingQtyNow — головний сторож фази.
//
// Holdings рахує залишок одним проходом по продажах замість виклику
// RemainingQtyNow на кожен лот. Уся ставка перенесення в тому, що ці два
// способи дають ОДНЕ І ТЕ САМЕ число, і перевіряти це треба прямо, а не
// сподіватись, що golden впіймає. Golden побачить лише ті лоти, які є у
// фікстурі; тут — і продаж частинами, і продаж понад куплене, і лот без
// жодного продажу, і продаж чужого лота.
func TestHoldingsRemainingMatchesRemainingQtyNow(t *testing.T) {
	lots := []Lot{
		{ID: 1, ISIN: "UA1", Qty: 10, PricePerBond: uah(99000), BuyDate: "2026-01-01"},
		{ID: 2, ISIN: "UA1", Qty: 5, PricePerBond: uah(97000), BuyDate: "2026-02-01"},
		{ID: 3, ISIN: "UA1", Qty: 7, PricePerBond: uah(98000), BuyDate: "2026-03-01"},
		{ID: 4, ISIN: "UA1", Qty: 3, PricePerBond: uah(98000), BuyDate: "2026-04-01"},
	}
	sales := []Sale{
		{ID: 1, LotID: 1, SaleDate: "2026-05-01", Qty: 3, CleanPerBond: uah(99500)},
		{ID: 2, LotID: 1, SaleDate: "2026-06-01", Qty: 4, CleanPerBond: uah(99500)},  // двома заходами
		{ID: 3, LotID: 2, SaleDate: "2026-06-01", Qty: 5, CleanPerBond: uah(99500)},  // рівно все
		{ID: 4, LotID: 3, SaleDate: "2026-06-02", Qty: 9, CleanPerBond: uah(99500)},  // БІЛЬШЕ, ніж куплено
		{ID: 5, LotID: 99, SaleDate: "2026-06-03", Qty: 5, CleanPerBond: uah(99500)}, // лота немає
	}

	h := NewHoldings(lots, sales, map[string]Bond{}, nil, nil, "2026-07-15")
	if len(h.Lots) != len(lots) {
		t.Fatalf("Holdings загубив лоти: %d проти %d", len(h.Lots), len(lots))
	}
	for i, got := range h.Lots {
		want := RemainingQtyNow(lots[i], sales)
		if got.Remaining != want {
			t.Errorf("лот %d: залишок %d, а RemainingQtyNow каже %d",
				got.ID, got.Remaining, want)
		}
	}
	// Явно, щоб зламаний RemainingQtyNow не «підтвердив» зламаний Holdings.
	for _, c := range []struct {
		id   int64
		want int64
	}{{1, 3}, {2, 0}, {3, 0}, {4, 3}} {
		for _, got := range h.Lots {
			if got.ID == c.id && got.Remaining != c.want {
				t.Errorf("лот %d: залишок %d, очікували %d", c.id, got.Remaining, c.want)
			}
		}
	}
}

// TestHoldingsMaturedKeepsEmptyDateSemantics — порожня дата погашення
// вважається «уже погашено», і це навмисно.
//
// Date.Before — порівняння рядків, тож "" «раніше» за будь-яку дату.
// Викликачі номіналу й експозиції брокера поводились саме так, і
// перенесення не мало права це змінити. Positions трактує порожню дату
// інакше, і та розбіжність жива — але зводити її треба окремим комітом.
func TestHoldingsMaturedKeepsEmptyDateSemantics(t *testing.T) {
	bonds := map[string]Bond{
		"UA1": {ISIN: "UA1", Nominal: uah(100000), Maturity: "2027-03-01"},
		"UA2": {ISIN: "UA2", Nominal: uah(100000), Maturity: "2026-01-10"},
		"UA3": {ISIN: "UA3", Nominal: uah(100000)}, // строку немає
	}
	lots := []Lot{
		{ID: 1, ISIN: "UA1", Qty: 1, PricePerBond: uah(99000), BuyDate: "2026-01-01"},
		{ID: 2, ISIN: "UA2", Qty: 1, PricePerBond: uah(99000), BuyDate: "2026-01-01"},
		{ID: 3, ISIN: "UA3", Qty: 1, PricePerBond: uah(99000), BuyDate: "2026-01-01"},
		{ID: 4, ISIN: "XX9", Qty: 1, PricePerBond: uah(99000), BuyDate: "2026-01-01"}, // не в довіднику
	}
	h := NewHoldings(lots, nil, bonds, nil, nil, "2026-07-15")

	want := map[int64]struct{ known, matured, held bool }{
		1: {known: true, matured: false, held: true},
		2: {known: true, matured: true, held: false},
		3: {known: true, matured: true, held: false}, // порожня дата = погашено
		4: {known: false, matured: false, held: false},
	}
	for _, l := range h.Lots {
		w := want[l.ID]
		if l.Known != w.known || l.Matured != w.matured || l.Held() != w.held {
			t.Errorf("лот %d: known=%v matured=%v held=%v; очікували %v/%v/%v",
				l.ID, l.Known, l.Matured, l.Held(), w.known, w.matured, w.held)
		}
	}
}

// TestHoldingsFundsAreValuesInStableOrder — фонди йдуть значеннями,
// відсортовані за назвою, а PayoutDay проставляє лише конструктор.
//
// Доти FundPositions віддавала мапу вказівників, і будівник дописував у
// неї день виплати. Це не шкодило рівно тому, що друге зведення будувало
// свіжу мапу; щойно зведення стало одне, така мутація поповзла б усюди.
func TestHoldingsFundsAreValuesInStableOrder(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-01-10", Fund: "Ямбол", Kind: FundBuy, Qty: 10, Amount: 10000, Currency: money.UAH},
		{Date: "2026-01-11", Fund: "Альфа", Kind: FundBuy, Qty: 5, Amount: 5000, Currency: money.UAH},
		{Date: "2026-02-01", Fund: "Бета", Kind: FundBuy, Qty: 2, Amount: 4000, Currency: money.UAH},
	}
	h := NewHoldings(nil, nil, map[string]Bond{}, ops,
		map[string]int64{"Альфа": 10, "Ямбол": 25}, "2026-07-15")

	var names []string
	for _, f := range h.Funds {
		names = append(names, f.Fund)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Fatalf("порядок фондів нестабільний: %v", names)
		}
	}
	byName := map[string]int64{}
	for _, f := range h.Funds {
		byName[f.Fund] = f.PayoutDay
	}
	if byName["Альфа"] != 10 || byName["Ямбол"] != 25 {
		t.Errorf("день виплати не з довідника: %v", byName)
	}
	if byName["Бета"] != 0 {
		t.Errorf("фонду без запису в довіднику приписали день %d", byName["Бета"])
	}
	// Тут стояла ще перевірка «правка копії не змінює Holdings». Її
	// прибрано навмисно: доки FundHolding тримає FundPosition ЗНАЧЕННЯМ,
	// вона не може впасти в принципі, і мутаційна перевірка це показала —
	// зламане поле вона пропустила. Тест, який не вміє червоніти, дає
	// хибну певність, а не захист. Спільний стан тепер стережуть тип
	// (значення, не вказівник) і make sources-boundary, який не пускає
	// друге зведення фондів у buildState.
}
