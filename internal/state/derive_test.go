package state

import "testing"

func ptr(v float64) *float64 { return &v }

// Стеля подушки на час боргу: обрізає лише ВНИЗ, лише доки борг живий, і
// самогасне, коли борг закрився.
//
// Головне тут — не саме обрізання, а те, що воно не міняє поведінки, доки
// ключа немає: налаштування, яке мовчки просаджує головну ціль удвічі,
// було б найгіршим виглядом помилки.
func TestReserveTargetCappedWhileExpensiveDebt(t *testing.T) {
	set := &SettingsDoc{
		MonthlyExpensesUAH:  ptr(25000),
		ReserveTargetMonths: ptr(6),
	}
	const have = 80000

	// Ключа немає — борг нічого не міняє.
	full, gapFull := ReserveTarget(set, have, true)
	if full != 150000 || gapFull != 70000 {
		t.Fatalf("без ключа ціль %.2f / розрив %.2f, чекали 150000 / 70000", full, gapFull)
	}

	set.ReserveDebtMonths = ptr(3)
	capped, gapCapped := ReserveTarget(set, have, true)
	if capped != 75000 {
		t.Errorf("обрізана ціль %.2f, чекали 75000 (3 місяці × 25 000)", capped)
	}
	// Подушка вже більша за обрізану ціль — розриву немає, і це правильна
	// відповідь: доки живий дорогий борг, доливати в матрац нема чого.
	if gapCapped != 0 {
		t.Errorf("розрив при обрізаній цілі %.2f, чекали 0", gapCapped)
	}

	// Борг закрився — ціль повертається САМА, без жодної дії людини.
	if back, gapBack := ReserveTarget(set, have, false); back != 150000 || gapBack != 70000 {
		t.Errorf("після боргу ціль %.2f / розрив %.2f, чекали 150000 / 70000", back, gapBack)
	}

	// Стеля БІЛЬША за ціль нічого не робить: це стеля, а не друга ціль.
	set.ReserveDebtMonths = ptr(12)
	if v, _ := ReserveTarget(set, have, true); v != 150000 {
		t.Errorf("стеля 12 місяців підняла ціль до %.2f — вона мусить лише обрізати", v)
	}
}
