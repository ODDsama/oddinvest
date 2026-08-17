package domain

import (
	"math"
	"testing"
)

// dynastia — рахунок-взірець. ЧВОПА й ставки взяті правдоподібними, але
// жодне число тут не є твердженням про справжній фонд.
func dynastia() NPFAccount {
	return NPFAccount{
		ID: 1, Name: "Династія", Currency: "UAH",
		Nav: 3_472_156, NavDate: Date("2026-06-30"),
		ExpectedYieldBP: 1500, AccessDate: Date("2051-04-01"),
		IncomeTaxBP: 1380, CreditRateBP: 1800, ContribDay: 5,
	}
}

// TestNPFUnitsSurviveTheRoundTrip — дробові одиниці не губляться.
//
// Це і є причина власної таблиці: 1000 ₴ за ЧВОПА 3.472156 зараховують
// 288.005492 одиниці. У цілих сертифікатах fund_ops це або 288, або 289 —
// тобто до двох гривень туди-сюди на КОЖНОМУ внеску, і завжди в один бік.
func TestNPFUnitsSurviveTheRoundTrip(t *testing.T) {
	op := NPFOp{NPFID: 1, Date: Date("2026-07-05"), Units: 288_005_492, Amount: 100_000}
	// Виведена ЧВОПА мусить збігтися з тією, за якою рахували, ТОЧНО:
	// розбіжність означала б, що масштаб десь губить розряд.
	if nav := op.NavE6(); nav != 3_472_156 {
		t.Fatalf("ЧВОПА з операції = %d, очікували 3472156", nav)
	}
	p := NPFPosition{Units: op.Units, Nav: op.NavE6()}
	// Копійка втрати — це цілочисельне ділення, і вона мусить бути саме
	// однією: більше означало б, що масштаб замалий для шести знаків ЧВОПА.
	if got := p.Value(); got != 99_999 {
		t.Errorf("вартість позиції %d коп, а внесено 100000 — очікували 99999", got)
	}
	if u := p.UnitsMajor(); u < 288.0 || u > 288.01 {
		t.Errorf("одиниць %.6f, очікували ≈288.005492", u)
	}
}

// TestNPFNavTakesTheFresherOfTwoSources — ЧВОПА береться найсвіжіша з
// довідника й операцій.
//
// Обидва напрямки важливі. Забутий на пів року довідник не має занижувати
// позицію, коли внески вже принесли новішу ціну; але й оновлення з
// кабінету між внесками мусить діяти, інакше поле в UI було б декорацією.
func TestNPFNavTakesTheFresherOfTwoSources(t *testing.T) {
	a := dynastia()
	a.Nav, a.NavDate = 3_000_000, Date("2026-01-01")
	ops := []NPFOp{
		{NPFID: 1, Date: Date("2026-07-05"), Units: 288_005_492, Amount: 100_000},
	}
	p := NPFPositions([]NPFAccount{a}, ops)[1]
	if p.NavDate != Date("2026-07-05") {
		t.Errorf("операція свіжіша за довідник, а ЧВОПА лишилась від %s", p.NavDate)
	}

	// Тепер навпаки: довідник оновили руками ПІСЛЯ останнього внеску.
	a.Nav, a.NavDate = 3_600_000, Date("2026-08-01")
	p = NPFPositions([]NPFAccount{a}, ops)[1]
	if p.Nav != 3_600_000 {
		t.Errorf("ручне оновлення новіше за внесок, а взято %d", p.Nav)
	}
}

// TestNPFCostIsWhatIPaidNotWhatItIsWorth — собівартість не переоцінюється.
func TestNPFCostIsWhatIPaidNotWhatItIsWorth(t *testing.T) {
	a := dynastia()
	ops := []NPFOp{
		{NPFID: 1, Date: Date("2026-05-05"), Units: 300_000_000, Amount: 100_000},
		{NPFID: 1, Date: Date("2026-06-05"), Units: 288_005_492, Amount: 100_000},
	}
	p := NPFPositions([]NPFAccount{a}, ops)[1]
	if p.Cost != 200_000 {
		t.Fatalf("внесено 200000 коп, а собівартість %d", p.Cost)
	}
	if p.Gain() != p.Value()-200_000 {
		t.Errorf("приріст мусить бути вартість мінус внесене")
	}
}

// TestNPFNavReturnNeedsTwoPointsAndSomeTime — зростання ЧВОПА не
// ануалізується з нічого.
//
// Два дні різниці, розтягнуті на рік, дають тризначні відсотки — число,
// яке виглядає як дохідність і нею не є.
func TestNPFNavReturnNeedsTwoPointsAndSomeTime(t *testing.T) {
	one := []NPFNav{{Date: Date("2026-01-01"), Nav: 3_000_000}}
	if _, ok := NPFNavReturn(one, Date("2026-08-17")); ok {
		t.Error("з однієї точки порахували дохідність")
	}
	short := []NPFNav{
		{Date: Date("2026-08-01"), Nav: 3_000_000},
		{Date: Date("2026-08-10"), Nav: 3_300_000},
	}
	if _, ok := NPFNavReturn(short, Date("2026-08-17")); ok {
		t.Error("дев'ять днів ануалізували в річну ставку")
	}
	// Рік рівно: 3.0 → 3.45 це +15%.
	year := []NPFNav{
		{Date: Date("2025-08-17"), Nav: 3_000_000},
		{Date: Date("2026-08-17"), Nav: 3_450_000},
	}
	r, ok := NPFNavReturn(year, Date("2026-08-17"))
	if !ok {
		t.Fatal("на рік даних дохідність не порахувалась")
	}
	if r < 14.9 || r > 15.1 {
		t.Errorf("3.0 → 3.45 за рік це ≈15%%, маємо %.2f", r)
	}
}

// TestNPFMeasuredDisplacesThePromise — факт витісняє обіцянку, і основа
// каже, що саме показано.
//
// Те саме правило, що у фондів (state_funds.go): доки міряти нема по чому,
// на екрані стоїть обіцянка — але вона мусить називатись обіцянкою.
func TestNPFMeasuredDisplacesThePromise(t *testing.T) {
	a := dynastia()
	r, basis := NPFOwnRatePct(a, nil, Date("2026-08-17"))
	if basis != "обіцяно фондом" {
		t.Errorf("без точок ЧВОПА основа має бути обіцянкою, маємо %q", basis)
	}
	if r != 15 {
		t.Errorf("обіцянка 15%%, маємо %.2f", r)
	}

	pts := []NPFNav{
		{Date: Date("2025-08-17"), Nav: 3_000_000},
		{Date: Date("2026-08-17"), Nav: 3_240_000}, // +8%
	}
	r, basis = NPFOwnRatePct(a, pts, Date("2026-08-17"))
	if basis != "зростання ЧВОПА" {
		t.Errorf("є що виміряти, а основа лишилась %q", basis)
	}
	if r < 7.9 || r > 8.1 {
		t.Errorf("виміряне ≈8%%, маємо %.2f — обіцянка не витіснилась", r)
	}
}

// TestNPFPromiseAsSimpleIsNotTheSameAsCompound — проста середньорічна не
// підставляється як складна.
func TestNPFPromiseAsSimpleIsNotTheSameAsCompound(t *testing.T) {
	a := dynastia()
	a.ExpectedYieldBP, a.YieldSimpleYears = 2500, 3
	r, _ := NPFOwnRatePct(a, nil, Date("2026-08-17"))
	// Проста 25% за три роки = ×1.75 = 20.5% складних, не 25%.
	if r > 21 {
		t.Errorf("проста 25%% за 3 роки це ≈20.5%% складних, маємо %.2f", r)
	}
}

// TestNPFNavPointsMergeManualWithDerived — точки з внесків і заведені
// руками складаються, а не витісняють одні одних.
//
// Без цього track record фонду до першого внеску не побачити, а він і є
// єдиною підставою для обіцянки.
func TestNPFNavPointsMergeManualWithDerived(t *testing.T) {
	manual := []NPFNav{
		{NPFID: 1, Date: Date("2020-01-01"), Nav: 1_500_000},
		{NPFID: 1, Date: Date("2023-01-01"), Nav: 2_400_000},
		{NPFID: 2, Date: Date("2023-01-01"), Nav: 9_999_999}, // чужий рахунок
	}
	ops := []NPFOp{
		{NPFID: 1, Date: Date("2026-07-05"), Units: 288_005_492, Amount: 100_000},
	}
	pts := NPFNavPoints(manual, ops, 1)
	if len(pts) != 3 {
		t.Fatalf("очікували 3 точки (2 руками + 1 з внеску), маємо %d", len(pts))
	}
	if pts[0].Date != Date("2020-01-01") || pts[2].Date != Date("2026-07-05") {
		t.Errorf("точки не відсортовані за датою: %v", pts)
	}

	// Заведене руками важить більше за виведене: воно точне, а виведене
	// несе округлення суми внеску.
	manual = append(manual, NPFNav{NPFID: 1, Date: Date("2026-07-05"), Nav: 3_472_000})
	pts = NPFNavPoints(manual, ops, 1)
	for _, p := range pts {
		if p.Date == Date("2026-07-05") && p.Nav != 3_472_000 {
			t.Errorf("на спільну дату мала лишитись ручна точка, маємо %d", p.Nav)
		}
	}
}

// TestNPFContribReminderClearsItself — нагадування гасне саме, коли внесок
// зʼявляється.
//
// Це й є причина, чому стану «я вже бачив» ніде не тримається: питання
// ставиться до журналу, а журнал і є відповіддю.
func TestNPFContribReminderClearsItself(t *testing.T) {
	a := dynastia() // ContribDay = 5
	if NPFContribDue(a, nil, Date("2026-08-03")) {
		t.Error("третього числа при дні внеску 5 нагадувати ще рано")
	}
	if !NPFContribDue(a, nil, Date("2026-08-17")) {
		t.Error("сімнадцятого без внеску за серпень нагадування мусить горіти")
	}
	july := []NPFOp{{NPFID: 1, Date: Date("2026-07-05"), Units: 1, Amount: 100_000}}
	if !NPFContribDue(a, july, Date("2026-08-17")) {
		t.Error("внесок за ЛИПЕНЬ не має гасити нагадування за серпень")
	}
	august := append(july, NPFOp{NPFID: 1, Date: Date("2026-08-06"), Units: 1, Amount: 100_000})
	if NPFContribDue(a, august, Date("2026-08-17")) {
		t.Error("внесок за серпень мусить погасити нагадування")
	}
	a.ContribDay = 0
	if NPFContribDue(a, nil, Date("2026-08-17")) {
		t.Error("день внеску 0 означає «не нагадувати»")
	}
}

// TestNPFCreditCapsPerMonthNotPerYear — ліміт знижки місячний, і разовий
// великий внесок його не обходить.
//
// Рахувати від річної суми означало б завищити знижку рівно там, де
// внески нерівномірні, — а це звичайна річ.
func TestNPFCreditCapsPerMonthNotPerYear(t *testing.T) {
	a := dynastia()            // CreditRateBP = 1800
	capMonth := int64(466_000) // 4660 ₴ ліміт 2026
	lump := []NPFOp{{NPFID: 1, Date: Date("2026-01-15"), Units: 1, Amount: 6_000_000}}
	// 60000 ₴ одним платежем: у знижку йде лише ліміт ОДНОГО місяця.
	got := NPFCreditEstimate(a, lump, 2026, capMonth, 0)
	want := capMonth * 1800 / 10000
	if got != want {
		t.Errorf("разовий внесок: очікували %d (ліміт одного місяця × 18%%), маємо %d", want, got)
	}

	// Дванадцять внесків у межах ліміту — знижка з усієї суми.
	var spread []NPFOp
	for m := 1; m <= 12; m++ {
		d := Date("2026-01-05")
		spread = append(spread, NPFOp{NPFID: 1, Date: d.AddMonths(m - 1), Units: 1, Amount: 400_000})
	}
	got = NPFCreditEstimate(a, spread, 2026, capMonth, 0)
	if want := int64(12 * 400_000 * 1800 / 10000); got != want {
		t.Errorf("рівномірні внески: очікували %d, маємо %d", want, got)
	}
}

// TestNPFCreditCannotExceedTaxActuallyWithheld — держава повертає
// сплачене, а не дарує.
func TestNPFCreditCannotExceedTaxActuallyWithheld(t *testing.T) {
	a := dynastia()
	ops := []NPFOp{{NPFID: 1, Date: Date("2026-03-05"), Units: 1, Amount: 400_000}}
	full := NPFCreditEstimate(a, ops, 2026, 466_000, 0)
	capped := NPFCreditEstimate(a, ops, 2026, 466_000, 1_000)
	if capped != 1_000 {
		t.Errorf("стеля ПДФО 1000 коп, а знижка %d", capped)
	}
	if full <= capped {
		t.Errorf("без стелі знижка мусить бути більшою: %d проти %d", full, capped)
	}
	a.CreditRateBP = 0
	if NPFCreditEstimate(a, ops, 2026, 466_000, 0) != 0 {
		t.Error("без ставки знижки оцінка мусить бути нульовою")
	}
}

// TestNPFAccessMonthsCountsToTheLock — місяці до доступу, і нуль там, де
// дати немає.
func TestNPFAccessMonthsCountsToTheLock(t *testing.T) {
	a := dynastia() // AccessDate 2051-04-01
	if m := NPFAccessMonths(a, Date("2026-04-01")); m != 300 {
		t.Errorf("з 2026-04 до 2051-04 це 300 місяців, маємо %d", m)
	}
	a.AccessDate = ""
	if m := NPFAccessMonths(a, Date("2026-08-17")); m != 0 {
		t.Errorf("без дати доступу очікували 0, маємо %d", m)
	}
	a.AccessDate = Date("2020-01-01")
	if m := NPFAccessMonths(a, Date("2026-08-17")); m != 0 {
		t.Errorf("минула дата доступу — це 0, маємо %d", m)
	}
}

// TestLockedAccumIsNotSpentByDrawdown — замкнена позиція не витрачається.
//
// Найдорожча помилка з усіх, які тут можливі: НПФ має відому вартість, тож
// декумуляція витратила б його мовчки й показала запас, якого немає. Число
// при цьому лишалось би правдоподібним — саме тому потрібен тест, а не
// уважність.
func TestLockedAccumIsNotSpentByDrawdown(t *testing.T) {
	free := Sleeve{Currency: "UAH", Rate0: 1,
		Accum: []Accum{{Value0: 20000, Cost0: 20000}}}
	locked := Sleeve{Currency: "UAH", Rate0: 1,
		Accum: []Accum{{Value0: 20000, Cost0: 20000, Locked: true}}}

	if m := DrawdownMonths([]Sleeve{free}, 0, 1000, 60); m != 21 {
		t.Fatalf("незамкнені 20000 під 1000/міс мали вичерпатись на 21-му, маємо %d", m)
	}
	// Замкнене не дає нічого, тобто знімати нема з чого з першого місяця.
	if m := DrawdownMonths([]Sleeve{locked}, 0, 1000, 60); m != 1 {
		t.Errorf("замкнений НПФ не можна витрачати — очікували вичерпання на 1-му, маємо %d", m)
	}
}

// TestLockedAccumStillGrowsAndCloses — замок забирає лише продаж.
//
// Рости, дожити до CloseM і заплатити податок з доходу замкнена позиція
// мусить як усі: інакше «Locked» тихо перетворився б на «не існує», і
// проєкція перестала б бачити пенсійні гроші взагалі.
func TestLockedAccumStillGrowsAndCloses(t *testing.T) {
	a := Accum{Value0: 10000, Cost0: 10000, RatePct: 12, CloseM: 24, TaxPct: 13.8, Locked: true}
	if v := AccumCloseValue(a); v <= 10000 {
		t.Errorf("замкнена позиція мусить зростати до закриття, маємо %.2f", v)
	}
	free := a
	free.Locked = false
	if AccumCloseValue(a) != AccumCloseValue(free) {
		t.Error("замок не має впливати на суму, яку позиція віддає на закритті")
	}
}

// TestNPFPayoutIsAStreamNotALump — виплата йде потоком, а не разово.
//
// Найважливіша правка стадії 4, і не косметична. За законом виплата на
// визначений строк іде мінімум десять років; разова модель вивалювала весь
// капітал у готівку одного місяця, далі реінвестувала його ринковою ставкою
// — і проєкція малювала капітал, якого не буде.
func TestNPFPayoutIsAStreamNotALump(t *testing.T) {
	lump := Accum{Value0: 120000, Cost0: 120000, CloseM: 1, Locked: true}
	stream := lump
	stream.PayoutM = 120 // десять років щомісяця

	// Разова: усе випадає першого ж місяця.
	s1 := Sleeve{Currency: "UAH", Rate0: 1, Accum: []Accum{lump}}
	st1 := s1.newState()
	if got := st1.grow(1); got != 120000 {
		t.Fatalf("разова виплата мала віддати 120000 одразу, маємо %.2f", got)
	}

	// Потоком: першого місяця лише одна частка, решта лишається в капіталі.
	s2 := Sleeve{Currency: "UAH", Rate0: 1, Accum: []Accum{stream}}
	st2 := s2.newState()
	first := st2.grow(1)
	if first != 1000 {
		t.Errorf("перша з 120 виплат мала бути 1000, маємо %.2f", first)
	}
	// Залишок мусить лишитись у капіталі, а не зникнути: інакше він просів
	// би на весь пенсійний баланс саме тоді, коли він найбільший.
	if left := st2.accumTotal(); left < 118000 || left > 119001 {
		t.Errorf("невиплачений залишок %.2f, очікували ≈119000", left)
	}

	// Сума всіх виплат мусить дорівнювати разовій — до копійки.
	total := first
	for m := 2; m <= 120; m++ {
		total += st2.grow(m)
	}
	if math.Abs(total-120000) > 0.01 {
		t.Errorf("Σ виплат %.2f замість 120000 — потік втратив або вигадав гроші", total)
	}
	// І після строку нічого більше не приходить.
	if extra := st2.grow(121); extra != 0 {
		t.Errorf("після строку виплати прийшло ще %.2f", extra)
	}
}

// TestNPFPayoutScheduleMatchesTheTotal — Σ подій календаря точно дорівнює
// сумі, яку віддає позиція.
//
// Календар і симуляція живляться тим самим числом (AccumCloseValue), і
// розійтись їм не дає ділення із залишком в останню виплату: без нього
// звірка падала б на копійки, і причину шукали б у податку.
func TestNPFPayoutScheduleMatchesTheTotal(t *testing.T) {
	a := dynastia()
	a.AccessDate = Date("2051-04-01")
	for _, tc := range []struct {
		freq string
		want int
	}{{"month", 120}, {"quarter", 40}, {"year", 10}} {
		a.PayoutYears, a.PayoutFreq = 10, tc.freq
		const total int64 = 1_000_003 // навмисно не ділиться рівно
		cf := NPFPayoutSchedule(a, total, Date("2099-01-01"))
		if len(cf) != tc.want {
			t.Errorf("%s: очікували %d виплат, маємо %d", tc.freq, tc.want, len(cf))
			continue
		}
		var sum int64
		for _, c := range cf {
			sum += c.Amount.Amount()
			if !IsNPFISIN(c.ISIN) {
				t.Errorf("%s: виплата під ключем %q — охорони її не впізнають", tc.freq, c.ISIN)
			}
		}
		if sum != total {
			t.Errorf("%s: Σ виплат %d замість %d", tc.freq, sum, total)
		}
		if cf[0].Date != a.AccessDate {
			t.Errorf("%s: перша виплата %s, а дата доступу %s", tc.freq, cf[0].Date, a.AccessDate)
		}
	}
}

// TestNPFPayoutScheduleRespectsHorizon — за обрієм виплат немає.
//
// Календар дивиться на рік, і виплата з 2051-го в нього потрапити не має:
// інакше «найближчі виплати» показували б подію за двадцять пʼять років.
func TestNPFPayoutScheduleRespectsHorizon(t *testing.T) {
	a := dynastia()
	a.AccessDate = Date("2051-04-01")
	a.PayoutYears, a.PayoutFreq = 10, "month"
	if cf := NPFPayoutSchedule(a, 1_000_000, Date("2027-01-01")); len(cf) != 0 {
		t.Errorf("у річному вікні пенсійних виплат бути не має, маємо %d", len(cf))
	}
	// А ось разова виплата в межах вікна — одна подія.
	a.PayoutYears = 0
	a.AccessDate = Date("2026-10-01")
	cf := NPFPayoutSchedule(a, 1_000_000, Date("2027-01-01"))
	if len(cf) != 1 || cf[0].Type != PayRedemption {
		t.Errorf("разова виплата мала дати одну подію-погашення, маємо %+v", cf)
	}
}
