package state

import (
	money "github.com/Rhymond/go-money"
	"testing"
)

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
	full, gapFull := ReserveTarget(set, have, true, 0, 0)
	if full != 150000 || gapFull != 70000 {
		t.Fatalf("без ключа ціль %.2f / розрив %.2f, чекали 150000 / 70000", full, gapFull)
	}

	set.ReserveDebtMonths = ptr(3)
	capped, gapCapped := ReserveTarget(set, have, true, 0, 0)
	if capped != 75000 {
		t.Errorf("обрізана ціль %.2f, чекали 75000 (3 місяці × 25 000)", capped)
	}
	// Подушка вже більша за обрізану ціль — розриву немає, і це правильна
	// відповідь: доки живий дорогий борг, доливати в матрац нема чого.
	if gapCapped != 0 {
		t.Errorf("розрив при обрізаній цілі %.2f, чекали 0", gapCapped)
	}

	// Борг закрився — ціль повертається САМА, без жодної дії людини.
	if back, gapBack := ReserveTarget(set, have, false, 0, 0); back != 150000 || gapBack != 70000 {
		t.Errorf("після боргу ціль %.2f / розрив %.2f, чекали 150000 / 70000", back, gapBack)
	}

	// Стеля БІЛЬША за ціль нічого не робить: це стеля, а не друга ціль.
	set.ReserveDebtMonths = ptr(12)
	if v, _ := ReserveTarget(set, have, true, 0, 0); v != 150000 {
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
	target, gap := ReserveTarget(set, have, true, 120000, 0)
	if target != 120000 {
		t.Errorf("ціль %.2f, чекали 120000: підлога мусить перебити стелю", target)
	}
	if gap != 40000 {
		t.Errorf("розрив %.2f, чекали 40000", gap)
	}

	// Підлога НИЖЧА за ціль не робить нічого: це підлога, а не друга ціль.
	if v, _ := ReserveTarget(set, have, false, 10000, 0); v != 150000 {
		t.Errorf("низька підлога опустила ціль до %.2f", v)
	}

	// Порожня ціль підлогою не піднімається: подушки, якої людина не
	// ставила, застосунок за неї не вигадує.
	if v, g := ReserveTarget(&SettingsDoc{}, have, false, 120000, 0); v != 0 || g != 0 {
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
		ReserveUAH: Major(30000, money.UAH),
	}
	if err := Derive(doc, DeriveInput{DebtCoverUAH: Major(74000, money.UAH)}); err != nil {
		t.Fatalf("Derive: %v", err)
	}
	r := doc.Reserve
	if r == nil {
		t.Fatal("картки резерву немає")
	}
	if r.DebtCoverUAH.Major() != 74000 || r.DebtCoverGapUAH.Major() != 44000 {
		t.Errorf("рубіж %.2f / бракує %.2f, чекали 74000 / 44000",
			r.DebtCoverUAH.Major(), r.DebtCoverGapUAH.Major())
	}

	// Подушка переросла борг — рубіж лишається названим, а «бракує» зникає:
	// «перекрито» це відповідь, а не мовчання.
	doc.ReserveUAH = Major(90000, money.UAH)
	if err := Derive(doc, DeriveInput{DebtCoverUAH: Major(74000, money.UAH)}); err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if doc.Reserve.DebtCoverUAH.Major() != 74000 || doc.Reserve.DebtCoverGapUAH.Major() != 0 {
		t.Errorf("після перекриття: рубіж %.2f / бракує %.2f",
			doc.Reserve.DebtCoverUAH.Major(), doc.Reserve.DebtCoverGapUAH.Major())
	}
}

// Позика в самого себе піднімає ціль РІВНО на нарахований відсоток — не на
// тіло. Тіло вже вирахуване з подушки самим зняттям, і додати його вдруге
// означало б вимагати повернути ті самі гроші двічі (0057).
func TestReserveTargetRisesByLoanInterestOnly(t *testing.T) {
	set := &SettingsDoc{
		MonthlyExpensesUAH:  ptr(25000),
		ReserveTargetMonths: ptr(6),
	}
	// Подушка була ПОВНА (150 000), потім із неї взяли 12 000.
	const have = 150000 - 12000

	base, baseGap := ReserveTarget(set, have, false, 0, 0)
	if base != 150000 || baseGap != 12000 {
		t.Fatalf("без позики ціль %.2f / розрив %.2f, чекали 150000 / 12000", base, baseGap)
	}
	// Ті самі гроші, але зняття оголошене позикою, і на ній наросло 186 ₴.
	withLoan, loanGap := ReserveTarget(set, have, false, 0, 186)
	if withLoan != 150186 {
		t.Errorf("ціль із позикою %.2f, чекали 150186 (150000 базова + 186 відсотка)", withLoan)
	}
	// І ось головне: на ПОВНІЙ подушці розрив дорівнює залишку боргу.
	if loanGap != 12186 {
		t.Errorf("розрив %.2f, чекали 12186 — тіло 12 000 плюс відсоток 186", loanGap)
	}
}

// Позика закрилась — надбавки немає, і ціль повертається до базової САМА,
// без жодного окремого механізму. Це і є те, чого просив власник:
// відсоток — тиск, поки борг живий, а не вічна надбавка до подушки.
func TestReserveTargetReturnsToBaseWhenLoanClosed(t *testing.T) {
	set := &SettingsDoc{
		MonthlyExpensesUAH:  ptr(25000),
		ReserveTargetMonths: ptr(6),
	}
	const have = 150186 // повернув тіло з відсотком — у подушці вийшов надлишок

	target, gap := ReserveTarget(set, have, false, 0, 0)
	if target != 150000 {
		t.Fatalf("після закриття позики ціль %.2f, чекали 150000", target)
	}
	// Перебір — не борг: розриву немає, і подушка просто більша за ціль.
	if gap != 0 {
		t.Errorf("розрив %.2f при подушці над ціллю, чекали 0", gap)
	}
}

// Надбавка додається ПІСЛЯ стелі й підлоги, а не сперечається з ними за
// одну ціль. Стеля відповідає на «якої величини потрібна подушка», позика —
// на «скільки я винен згори», і max() з'їв би друге питання цілком.
func TestReserveTargetLoanAddsOnTopOfCapAndFloor(t *testing.T) {
	set := &SettingsDoc{
		MonthlyExpensesUAH:  ptr(25000),
		ReserveTargetMonths: ptr(6),
		ReserveDebtMonths:   ptr(3),
	}
	if v, _ := ReserveTarget(set, 0, true, 0, 500); v != 75500 {
		t.Errorf("зі стелею ціль %.2f, чекали 75500 (3 × 25 000 + 500)", v)
	}
	if v, _ := ReserveTarget(set, 0, false, 200000, 500); v != 200500 {
		t.Errorf("з підлогою ціль %.2f, чекали 200500 (покриття 200 000 + 500)", v)
	}
}

// Порожня базова ціль надбавки НЕ отримує: вигадати подушку за людину, яка
// її не ставила, не можна навіть заради боргу. Сама позика від цього не
// зникає — вона показується власним блоком картки.
func TestReserveTargetLoanDoesNotInventTarget(t *testing.T) {
	if v, g := ReserveTarget(&SettingsDoc{}, 0, false, 0, 500); v != 0 || g != 0 {
		t.Errorf("без заданих витрат ціль %.2f / розрив %.2f, чекали нулі", v, g)
	}
}
