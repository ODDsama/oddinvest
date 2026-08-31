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
	full, gapFull := ReserveTarget(set, have, true, 0)
	if full != 150000 || gapFull != 70000 {
		t.Fatalf("без ключа ціль %.2f / розрив %.2f, чекали 150000 / 70000", full, gapFull)
	}

	set.ReserveDebtMonths = ptr(3)
	capped, gapCapped := ReserveTarget(set, have, true, 0)
	if capped != 75000 {
		t.Errorf("обрізана ціль %.2f, чекали 75000 (3 місяці × 25 000)", capped)
	}
	// Подушка вже більша за обрізану ціль — розриву немає, і це правильна
	// відповідь: доки живий дорогий борг, доливати в матрац нема чого.
	if gapCapped != 0 {
		t.Errorf("розрив при обрізаній цілі %.2f, чекали 0", gapCapped)
	}

	// Борг закрився — ціль повертається САМА, без жодної дії людини.
	if back, gapBack := ReserveTarget(set, have, false, 0); back != 150000 || gapBack != 70000 {
		t.Errorf("після боргу ціль %.2f / розрив %.2f, чекали 150000 / 70000", back, gapBack)
	}

	// Стеля БІЛЬША за ціль нічого не робить: це стеля, а не друга ціль.
	set.ReserveDebtMonths = ptr(12)
	if v, _ := ReserveTarget(set, have, true, 0); v != 150000 {
		t.Errorf("стеля 12 місяців підняла ціль до %.2f — вона мусить лише обрізати", v)
	}
}

// Підлога цілі: борг, який не можна погасити достроково.
//
// Головне тут — що підлога СИЛЬНІША за стелю. Вони описують різні борги
// (стелю вмикає лише той, у який гроші можна подіти) і зустрічаються в
// одній цілі лише тоді, коли боргів два різних ґатунків. Порядок «стеля,
// потім підлога» дає єдину відповідь, яку можна захистити: подушка не
// опускається нижче за суму, якою цей борг доведеться закривати.
func TestReserveTargetFlooredByDebtCover(t *testing.T) {
	set := &SettingsDoc{
		MonthlyExpensesUAH:  ptr(25000),
		ReserveTargetMonths: ptr(6),
		ReserveDebtMonths:   ptr(3),
	}
	const have = 80000

	// Стеля обрізала ціль до 75 000, але закривати борг доведеться сумою
	// 120 000 — ціль не має права стояти нижче за неї.
	target, gap := ReserveTarget(set, have, true, 120000)
	if target != 120000 {
		t.Errorf("ціль %.2f, чекали 120000: підлога мусить перебити стелю", target)
	}
	if gap != 40000 {
		t.Errorf("розрив %.2f, чекали 40000", gap)
	}

	// Підлога НИЖЧА за ціль не робить нічого: це підлога, а не друга ціль.
	if v, _ := ReserveTarget(set, have, false, 10000); v != 150000 {
		t.Errorf("низька підлога опустила ціль до %.2f", v)
	}

	// Порожня ціль підлогою не піднімається: подушки, якої людина не
	// ставила, застосунок за неї не вигадує.
	if v, g := ReserveTarget(&SettingsDoc{}, have, false, 120000); v != 0 || g != 0 {
		t.Errorf("ціль з нічого: %.2f / %.2f", v, g)
	}
}

// Рубіж покриття показується окремо від цілі: він ближчий і відповідає на
// інше питання — чи є чим закрити кредити, коли дохід зникне.
func TestReserveDebtCoverGap(t *testing.T) {
	doc := &Doc{
		Settings: &SettingsDoc{
			MonthlyExpensesUAH:  ptr(25000),
			ReserveTargetMonths: ptr(6),
		},
		ReserveUAH: 30000,
	}
	if err := Derive(doc, DeriveInput{DebtCoverUAH: 74000}); err != nil {
		t.Fatalf("Derive: %v", err)
	}
	r := doc.Reserve
	if r == nil {
		t.Fatal("картки резерву немає")
	}
	if r.DebtCoverUAH != 74000 || r.DebtCoverGapUAH != 44000 {
		t.Errorf("рубіж %.2f / бракує %.2f, чекали 74000 / 44000",
			r.DebtCoverUAH, r.DebtCoverGapUAH)
	}

	// Подушка переросла борг — рубіж лишається названим, а «бракує» зникає:
	// «перекрито» це відповідь, а не мовчання.
	doc.ReserveUAH = 90000
	if err := Derive(doc, DeriveInput{DebtCoverUAH: 74000}); err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if doc.Reserve.DebtCoverUAH != 74000 || doc.Reserve.DebtCoverGapUAH != 0 {
		t.Errorf("після перекриття: рубіж %.2f / бракує %.2f",
			doc.Reserve.DebtCoverUAH, doc.Reserve.DebtCoverGapUAH)
	}
}
