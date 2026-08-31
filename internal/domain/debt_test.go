package domain

import (
	"math"
	"testing"
)

// ГОЛОВНИЙ ТЕСТ ФАЗИ: «0% і комісія 1,99% на місяць» коштує близько 50%
// річних, а не 23,9%.
//
// Різниця не академічна. 23,9% — це 1,99 × 12, тобто число, яке виходить,
// якщо думати, що комісія береться від залишку. Вона береться від
// ПОЧАТКОВОЇ суми, а тіло щомісяця спадає, тож наприкінці строку ті самі
// 597 ₴ платяться за право користуватись трьома тисячами. Саме через це
// розстрочка стоїть у черзі погашення вище за все, що продає держава.
func TestDebtEffectiveRateFromSchedule(t *testing.T) {
	// Розстрочка ПУМБ «гроші частинами»: 30 000 на 9 місяців, 1,99%/міс.
	d := Debt{
		Kind: DebtInstallment, Currency: "UAH",
		Principal: 30_000_00, PaymentsTotal: 9,
		FirstPaymentDate: "2026-09-30", FeeMonthBp: 199,
	}
	rate, basis := DebtEffectiveRate(d, 0)
	if basis != DebtRateFromSchedule {
		t.Fatalf("основа ставки %q, чекали %q", basis, DebtRateFromSchedule)
	}
	// Виміряно: 49,82%. Місячна внутрішня ставка тут ≈3,42%, і за рік із
	// капіталізацією вона дає майже п'ятдесят — тобто рівно те, що ПУМБ
	// бере за власною карткою поза пільговим.
	if rate < 48 || rate > 52 {
		t.Errorf("ефективна ставка %.2f%%, чекали 48–52%%: саме тут ловиться "+
			"різниця між «1,99 × 12» і правдою", rate)
	}
	// І вона МУСИТЬ бути помітно вищою за наївні 23,88%.
	if rate < 23.88*1.5 {
		t.Errorf("ставка %.2f%% замало відрізняється від 1,99×12 = 23,88%% — "+
			"схоже, комісія порахована від залишку, а не від початкової суми", rate)
	}

	// Безкоштовна розстрочка мусить лишитись безкоштовною: інакше лавина
	// гнала б у неї гроші, яким там нема чого робити.
	free := d
	free.FeeMonthBp = 0
	if rate, _ := DebtEffectiveRate(free, 0); math.Abs(rate) > 0.01 {
		t.Errorf("розстрочка без комісії дала %.4f%%, чекали нуль", rate)
	}

	// Товарна ПУМБ: 0% перші три місяці, далі 3%. Ставка мусить лягти МІЖ
	// «3% з першого дня» і «0% назавжди» — одне число на обидва режими
	// помилялось би в один із боків.
	goods := Debt{
		Kind: DebtInstallment, Currency: "UAH",
		Principal: 12_000_00, PaymentsTotal: 12,
		FirstPaymentDate: "2026-10-05", FeeMonthBp: 300, FeeFreeMonths: 3,
	}
	always := goods
	always.FeeFreeMonths = 0
	withFree, _ := DebtEffectiveRate(goods, 0)
	withoutFree, _ := DebtEffectiveRate(always, 0)
	if !(withFree > 0 && withFree < withoutFree) {
		t.Errorf("пільгові місяці не вплинули: з ними %.2f%%, без них %.2f%%",
			withFree, withoutFree)
	}
}

// Картка: заявлена річна з місячною капіталізацією. Банк каже 47,88%
// річних, маючи на увазі 3,99% на місяць; за рік це 60,0%.
func TestCardRateCompounds(t *testing.T) {
	card := Debt{Kind: DebtCard, Currency: "UAH", StatementDay: 30, APRBp: 4788}
	rate, basis := DebtEffectiveRate(card, 10_000_00)
	if basis != DebtRateCompound {
		t.Fatalf("основа %q, чекали %q", basis, DebtRateCompound)
	}
	if rate < 59.5 || rate > 60.5 {
		t.Errorf("ставка картки %.2f%%, чекали ≈60%%", rate)
	}
	// Боргу немає — ставки теж немає. Нуль тут читався б як «безкоштовно».
	if _, basis := DebtEffectiveRate(card, 0); basis != DebtRateNone {
		t.Errorf("на нульовому боргу основа %q, чекали %q", basis, DebtRateNone)
	}
}

// Розрахункова дата — ЧИСЛО МІСЯЦЯ, і 30 у лютому означає останній день
// лютого, а не 2 березня.
func TestStatementCycleAnchorsOnDay(t *testing.T) {
	for _, c := range []struct {
		name             string
		day              int64
		from             Date
		wantClosed, want Date
	}{
		{"звичайний місяць", 30, "2026-09-14", "2026-08-30", "2026-09-30"},
		{"лютий обрізає 30 до 28", 30, "2026-02-14", "2026-01-30", "2026-02-28"},
		{"високосний лютий", 30, "2028-02-14", "2028-01-30", "2028-02-29"},
		{"перехід року", 10, "2026-12-15", "2026-12-10", "2027-01-10"},
		// Рівність читається як «платити сьогодні»: ціна помилки в цей бік —
		// зайвий день напруження, у другий — штраф і 62%.
		{"сьогодні і є дата", 20, "2026-09-20", "2026-08-20", "2026-09-20"},
		{"день після дати", 20, "2026-09-21", "2026-09-20", "2026-10-20"},
	} {
		closed, due := StatementCycle(c.day, c.from)
		if due != c.want || closed != c.wantClosed {
			t.Errorf("%s: цикл %s…%s, чекали %s…%s",
				c.name, closed, due, c.wantClosed, c.want)
		}
	}
	// Картці без розрахункової дати цикл не вигадується: показаний поріг,
	// якого банк не виставляв, гірший за жодного.
	if closed, due := StatementCycle(0, "2026-09-14"); closed != "" || due != "" {
		t.Errorf("без розрахункової дати вийшов цикл %s…%s", closed, due)
	}
}

// Сума графіка мусить дорівнювати тілу плюс усі комісії ДО КОПІЙКИ:
// остача від ділення йде в останній платіж, як у банку.
func TestInstallmentScheduleSumsExactly(t *testing.T) {
	d := Debt{
		Kind: DebtInstallment, Currency: "UAH",
		Principal: 30_000_00, PaymentsTotal: 9,
		FirstPaymentDate: "2026-09-30", FeeMonthBp: 199,
	}
	sched := InstallmentSchedule(d)
	if len(sched) != 9 {
		t.Fatalf("платежів %d, чекали 9", len(sched))
	}
	var principal, fee int64
	for _, p := range sched {
		principal += p.Principal
		fee += p.Fee
		if p.Amount != p.Principal+p.Fee {
			t.Errorf("платіж %d не сходиться: %d ≠ %d + %d", p.No, p.Amount, p.Principal, p.Fee)
		}
	}
	if principal != d.Principal {
		t.Errorf("тіло графіка %d, чекали %d", principal, d.Principal)
	}
	if want := int64(597_00) * 9; fee != want {
		t.Errorf("комісії разом %d, чекали %d", fee, want)
	}
	// Дати тримають число місяця першого платежу, з обрізанням у лютому.
	if sched[0].Date != "2026-09-30" || sched[4].Date != "2027-01-30" ||
		sched[5].Date != "2027-02-28" {
		t.Errorf("дати графіка поїхали: %s / %s / %s",
			sched[0].Date, sched[4].Date, sched[5].Date)
	}
}

// Пільгові місяці не платять комісії — і саме тому вони окрема колонка.
func TestInstallmentFeeFreeMonths(t *testing.T) {
	d := Debt{
		Kind: DebtInstallment, Currency: "UAH",
		Principal: 12_000_00, PaymentsTotal: 12,
		FirstPaymentDate: "2026-10-05", FeeMonthBp: 300, FeeFreeMonths: 3,
	}
	sched := InstallmentSchedule(d)
	for i, p := range sched {
		want := int64(360_00)
		if i < 3 {
			want = 0
		}
		if p.Fee != want {
			t.Errorf("платіж %d: комісія %d, чекали %d", p.No, p.Fee, want)
		}
	}
}

// «Вільно» — головне число картки, і воно рахується від ТРЬОХ речей:
// балансу, суми до сплати й найближчих частин розстрочок.
func TestCardFreeExcludesStatementAndInstallments(t *testing.T) {
	card := Debt{
		ID: 1, Kind: DebtCard, Currency: "UAH", StatementDay: 30,
		LimitAmount: 200_000_00, APRBp: 4788, MinPaymentBp: 300,
		MinPaymentFloor: 100_00,
	}
	inst := Debt{
		ID: 2, Kind: DebtInstallment, Currency: "UAH", CardID: 1,
		Principal: 30_000_00, PaymentsTotal: 9,
		FirstPaymentDate: "2026-09-30", FeeMonthBp: 199,
	}
	marks := []DebtMark{{
		DebtID: 1, Date: "2026-09-01",
		Balance: 40_000_00, StatementDue: 18_400_00,
	}}
	st := CardState(card, marks, nil, []Debt{inst}, "2026-09-10")

	if !st.Known || st.MarkAgeDays != 9 {
		t.Fatalf("вік звірки %d днів (known=%v)", st.MarkAgeDays, st.Known)
	}
	if st.DueDate != "2026-09-30" {
		t.Errorf("дата платежу %s, чекали 2026-09-30", st.DueDate)
	}
	// Частина розстрочки 30-го числа спишеться ДО дати платежу — отже, ці
	// гроші вже не вільні, хоч у суму цієї виписки й не входять.
	if st.InstallmentDue != 3_930_33 {
		t.Errorf("частина розстрочки %d, чекали 3 930,33", st.InstallmentDue)
	}
	if want := int64(40_000_00 - 18_400_00 - 3_930_33); st.Free != want {
		t.Errorf("вільно %d, чекали %d", st.Free, want)
	}
	// Плюс на картці ще не означає, що все це можна витратити: саме про це
	// й було питання власника.
	if st.Balance <= 0 || st.Free >= st.Balance {
		t.Errorf("вільно (%d) мусить бути меншим за баланс (%d)", st.Free, st.Balance)
	}
}

// Два пороги — два різні твердження. Мінімалка рятує від штрафу й 62%,
// повна сума — від відсотків узагалі, і зводити їх в одне не можна.
func TestCardTwoThresholdsDiffer(t *testing.T) {
	card := Debt{
		ID: 1, Kind: DebtCard, Currency: "UAH", StatementDay: 30,
		APRBp: 4788, MinPaymentBp: 300, MinPaymentFloor: 100_00,
	}
	marks := []DebtMark{{DebtID: 1, Date: "2026-09-01",
		Balance: -12_000_00, StatementDue: 18_400_00}}
	st := CardState(card, marks, nil, nil, "2026-09-10")

	if st.MinDue != 552_00 {
		t.Errorf("мінімалка %d, чекали 3%% від 18 400 = 552,00", st.MinDue)
	}
	if st.StatementDue != 18_400_00 {
		t.Errorf("повний поріг %d", st.StatementDue)
	}
	if st.MinDue >= st.StatementDue {
		t.Error("пороги злилися — саме цього не мусить статись")
	}

	// Підлога працює: 3% від дрібного боргу менші за неї.
	small := []DebtMark{{DebtID: 1, Date: "2026-09-01",
		Balance: -1_000_00, StatementDue: 1_000_00}}
	if st := CardState(card, small, nil, nil, "2026-09-10"); st.MinDue != 100_00 {
		t.Errorf("мінімалка при малому боргу %d, чекали підлогу 100,00", st.MinDue)
	}
	// Але мінімалка не буває більшою за весь борг: «внеси 100 при боргу
	// 40» читалось би як вимога переплатити.
	tiny := []DebtMark{{DebtID: 1, Date: "2026-09-01",
		Balance: -40_00, StatementDue: 40_00}}
	if st := CardState(card, tiny, nil, nil, "2026-09-10"); st.MinDue != 40_00 {
		t.Errorf("мінімалка при боргу 40,00 дорівнює %d", st.MinDue)
	}
}

// Рухи після звірки зміщують БАЛАНС, а суму виписки зменшує лише платіж:
// покупка сьогодні потрапить у наступну виписку, а не в уже виставлену.
func TestCardOpsAfterMarkMoveBalanceNotStatement(t *testing.T) {
	card := Debt{
		ID: 1, Kind: DebtCard, Currency: "UAH", StatementDay: 30,
		LimitAmount: 100_000_00, APRBp: 4788, MinPaymentBp: 300,
	}
	marks := []DebtMark{{DebtID: 1, Date: "2026-09-01",
		Balance: 10_000_00, StatementDue: 8_000_00}}
	ops := []DebtOp{
		// Того самого дня, що й звірка, — уже В НІЙ. Інакше зарплата,
		// занесена в день звірки, порахувалась би двічі.
		{DebtID: 1, Date: "2026-09-01", Kind: DebtOpPayment, Amount: 5_000_00},
		{DebtID: 1, Date: "2026-09-03", Kind: DebtOpDraw, Amount: 3_000_00},
		{DebtID: 1, Date: "2026-09-05", Kind: DebtOpPayment, Amount: 2_000_00},
		{DebtID: 1, Date: "2026-09-07", Kind: DebtOpCash, Amount: 1_000_00},
		// Майбутнє не рахуємо: сьогодні його ще не сталося.
		{DebtID: 1, Date: "2026-09-20", Kind: DebtOpDraw, Amount: 50_000_00},
	}
	st := CardState(card, marks, ops, nil, "2026-09-10")

	if want := int64(10_000_00 - 3_000_00 + 2_000_00 - 1_000_00); st.Balance != want {
		t.Errorf("баланс %d, чекали %d", st.Balance, want)
	}
	if want := int64(8_000_00 - 2_000_00); st.StatementDue != want {
		t.Errorf("сума до сплати %d, чекали %d: покупка йде в НАСТУПНУ виписку", st.StatementDue, want)
	}
	// Готівка стає боргом під відсоток одразу, без пільгового.
	if st.NonGrace != 1_000_00 {
		t.Errorf("поза пільговим %d, чекали 1 000,00", st.NonGrace)
	}
}

// Мінімалка, менша за місячний відсоток, не гасить борг ніколи — і графік
// мусить це показувати, а не мовчати.
func TestCardScheduleShowsDebtThatOutgrowsMinimum(t *testing.T) {
	card := Debt{
		ID: 1, Kind: DebtCard, Currency: "UAH", StatementDay: 30,
		APRBp: 4788, MinPaymentBp: 300,
	}
	sched := DebtSchedule(card, 50_000_00, "2026-09-01", "2027-09-01")
	if len(sched) == 0 {
		t.Fatal("графіка картки немає")
	}
	// 3% мінімалки проти 3,99% відсотка на місяць: тіла в платежі немає.
	if sched[0].Principal != 0 {
		t.Errorf("перший платіж гасить тіло на %d, хоча відсоток більший за мінімалку",
			sched[0].Principal)
	}
	if sched[0].Fee <= sched[0].Amount {
		t.Errorf("відсоток %d мусить перевищувати платіж %d", sched[0].Fee, sched[0].Amount)
	}
	// Гасити нема чого — графіка теж немає.
	if got := DebtSchedule(card, 0, "2026-09-01", "2027-09-01"); got != nil {
		t.Errorf("на нульовому боргу графік із %d рядків", len(got))
	}
}

// Відʼємний баланс НЕ подвоює суму до сплати.
//
// Спіймано на бойових даних, не тестом: борг 182 317, виписка 180 260 —
// і «вільно» виходило −362 577, бо той самий борг стояв у формулі двічі.
// Банк так не рахує: платіж гасить виписку й піднімає баланс однією дією,
// тож додатний баланс і жива виписка разом не існують.
func TestCardFreeDoesNotDoubleCountNegativeBalance(t *testing.T) {
	card := Debt{
		ID: 1, Kind: DebtCard, Currency: "UAH", StatementDay: 30,
		LimitAmount: 182_317_45, APRBp: 4788, MinPaymentBp: 300,
	}
	// Ліміт вибраний до дна, уся сума до сплати — пільгова.
	marks := []DebtMark{{
		DebtID: 1, Date: "2026-09-01",
		Balance: -182_317_45, StatementDue: 180_259_85,
	}}
	st := CardState(card, marks, nil, nil, "2026-09-10")

	if st.Free != -180_259_85 {
		t.Errorf("вільно %d, чекали −180 259,85 — рівно стільки треба принести", st.Free)
	}
	if st.Debt != 182_317_45 {
		t.Errorf("борг %d", st.Debt)
	}
	// Використано весь ліміт — і це видно.
	if st.UsedPct < 99.9 {
		t.Errorf("використано %.2f%% ліміту", st.UsedPct)
	}
	// Пільговий оборот у чергу погашення не входить: нараховувати ще нема
	// на що.
	if st.NonGrace != 0 {
		t.Errorf("поза пільговим %d, чекали нуль", st.NonGrace)
	}

	// А коли зарплата покрила виписку, вільним стає рівно її залишок.
	ops := []DebtOp{{DebtID: 1, Date: "2026-09-05", Kind: DebtOpPayment, Amount: 185_000_00}}
	st = CardState(card, marks, ops, nil, "2026-09-10")
	if st.StatementDue != 0 {
		t.Errorf("після платежу до сплати лишилось %d", st.StatementDue)
	}
	if want := int64(185_000_00 - 182_317_45); st.Free != want {
		t.Errorf("вільно %d, чекали %d", st.Free, want)
	}
}

// ГОЛОВНЕ ЧИСЛО ФАЗИ: скільки можна витрачати на місяць, щоб вийти з
// ліміту до названої дати. Числа справжні — з бойової бази власника.
func TestCardExitSpendCap(t *testing.T) {
	in := CardExitInput{
		DebtUAH:   182_317_45, // ліміт вибраний до дна
		GrossUAH:  222_800_00, // 2 300 $ + 2 500 $ + 9 000 ₴
		InvestUAH: 41_500_00,  // те, що явно виводиться в інструменти
		SpendUAH:  66_826_00,  // заявлені 1 500 $
		Today:     "2026-09-01",
		ExitBy:    "2026-10-31", // ≈2 місяці
	}
	got := CardExit(in)
	if !got.Known || !got.Feasible {
		t.Fatalf("вихід оголошено неможливим: %+v", got)
	}
	// На картці лишається 181 300; треба звільняти ≈91 159 → витрачати
	// можна ≈90 100. Допуск на округлення місяців.
	if got.SpendCap < 88_000_00 || got.SpendCap > 93_000_00 {
		t.Errorf("стеля витрат %d, чекали ≈90 100 ₴", got.SpendCap)
	}
	if got.NeedPerMonth < 89_000_00 || got.NeedPerMonth > 94_000_00 {
		t.Errorf("треба звільняти %d, чекали ≈91 159 ₴", got.NeedPerMonth)
	}
	// Заявлені витрати в стелю вкладаються — відставання немає.
	if got.ShortPerMonth != 0 {
		t.Errorf("бракує %d, хоча витрати нижчі за стелю", got.ShortPerMonth)
	}
	// Докинути інвестиційну частку — стеля росте рівно на неї.
	if want := got.SpendCap + in.InvestUAH; got.WithInvestSpendCap != want {
		t.Errorf("зі скинутою інвестчасткою стеля %d, чекали %d",
			got.WithInvestSpendCap, want)
	}

	// За один місяць не виходить навіть при нульових витратах — і це
	// окреме твердження, а не «мало».
	in.ExitBy = "2026-09-30"
	if got := CardExit(in); got.Feasible || got.SpendCap > 0 {
		t.Errorf("за місяць оголошено можливим: стеля %d", got.SpendCap)
	}
}

// Коли витрати зʼїдають увесь дохід, дати виходу НЕМАЄ — і це чесніше за
// «через шістсот місяців».
func TestCardExitETAWhenSpendingExceedsIncome(t *testing.T) {
	in := CardExitInput{
		DebtUAH: 182_317_45, GrossUAH: 222_800_00, InvestUAH: 41_500_00,
		SpendUAH: 181_300_00, // рівно те, що лишається на картці
		Today:    "2026-09-01", ExitBy: "2026-12-31",
	}
	got := CardExit(in)
	if got.ETADate != "" {
		t.Errorf("дата виходу %s при нульовому профіциті", got.ETADate)
	}
	// Але стеля існує й каже, наскільки треба врізатись.
	if got.ShortPerMonth <= 0 {
		t.Error("не сказано, наскільки витрати перевищують стелю")
	}
	// А з інвестиційною часткою профіцит зʼявляється — і дата теж.
	if got.WithInvestETADate == "" {
		t.Error("з докинутою інвестчасткою дати виходу теж немає")
	}
}

// Витрати міряються ЗМІНОЮ БАЛАНСУ між звірками, а записані покупки в цій
// формулі скорочуються: вони вже сидять у виміряному балансі.
func TestCardBurnFromTwoMarks(t *testing.T) {
	card := Debt{ID: 1, Kind: DebtCard, Currency: "UAH", StatementDay: 30, APRBp: 4788}
	marks := []DebtMark{
		{DebtID: 1, Date: "2026-08-01", Balance: -100_000_00},
		{DebtID: 1, Date: "2026-08-31", Balance: -150_000_00},
	}
	ops := []DebtOp{
		{DebtID: 1, Date: "2026-08-05", Kind: DebtOpPayment, Amount: 180_000_00},
		// Покупка записана — і НЕ мусить змінити результат: вона вже в
		// балансі другої звірки.
		{DebtID: 1, Date: "2026-08-10", Kind: DebtOpDraw, Amount: 30_000_00},
		// Рух того самого дня, що й перша звірка, уже в ній.
		{DebtID: 1, Date: "2026-08-01", Kind: DebtOpPayment, Amount: 9_999_00},
	}
	got := CardBurnFrom(card, marks, ops, "2026-09-01")
	if !got.Known {
		t.Fatalf("вимір не відбувся: %q", got.Why)
	}
	// 180 000 внесено, баланс просів на 50 000 → витрачено 230 000.
	if got.Spent != 230_000_00 {
		t.Errorf("витрачено %d, чекали 230 000,00", got.Spent)
	}
	if got.Days != 30 || got.PerMonth < 229_000_00 || got.PerMonth > 234_000_00 {
		t.Errorf("за місяць %d за %d днів", got.PerMonth, got.Days)
	}
}

// Мовчить, коли міряти нема чим, і КАЖЕ, чого бракує.
func TestCardBurnSilentWithoutSecondMark(t *testing.T) {
	card := Debt{ID: 1, Kind: DebtCard, Currency: "UAH", StatementDay: 30}
	one := []DebtMark{{DebtID: 1, Date: "2026-08-31", Balance: -100_000_00}}
	got := CardBurnFrom(card, one, nil, "2026-09-01")
	if got.Known || got.Why == "" {
		t.Errorf("на одній звірці: known=%v why=%q", got.Known, got.Why)
	}

	// Баланс зріс, а надходжень не записано — це прогалина в журналі, а не
	// відʼємні витрати.
	marks := []DebtMark{
		{DebtID: 1, Date: "2026-08-01", Balance: -100_000_00},
		{DebtID: 1, Date: "2026-08-31", Balance: -10_000_00},
	}
	got = CardBurnFrom(card, marks, nil, "2026-09-01")
	if got.Known || got.Why == "" {
		t.Errorf("баланс зріс без надходжень: known=%v why=%q", got.Known, got.Why)
	}
}
