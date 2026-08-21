package state

import "testing"

// Драбина доступу до подушки.
//
// ГОЛОВНЕ, ЩО ТУТ ПЕРЕВІРЯЄТЬСЯ, — що станів ТРИ, а не два. Найлегша
// помилка в цьому механізмі не арифметична: вона в тому, щоб звести
// «доведеться розірвати вклад» і «грошей не буде ніяк» до одного «не
// гаразд». Обидва виглядають як неповне покриття, а означають протилежне,
// і різниця між ними — один прапорець у договорі.

// ladderFor — подушка на 12 місяців при витратах 50 000 ₴, голова 2.
func ladderFor(t *testing.T, liquid float64, deps ...ReserveDeposit) *Reserve {
	t.Helper()
	liquidMonths, maxTerm := 2.0, 12.0
	r := &Reserve{MonthlyExpensesUAH: 50_000, TargetMonths: 12}
	deriveReserveLadder(r, &SettingsDoc{
		ReserveLiquidMonths: &liquidMonths, ReserveMaxTermMonths: &maxTerm,
	}, DeriveInput{ReserveLiquidUAH: liquid, ReserveDeposits: deps})
	return r
}

// TestLadderRevocableTailIsTradeNotHole — один річний ВІДКЛИЧНИЙ вклад на
// хвіст покриває подушку, хоч сам гаситься лише в кінці.
//
// Це той самий випадок, який фіксоване правило «сходинка на кожен місяць»
// оголосило б порушенням, — і оголосило б хибно: тіло повернуть будь-коли,
// згорять лише відсотки.
func TestLadderRevocableTailIsTradeNotHole(t *testing.T) {
	r := ladderFor(t, 100_000, ReserveDeposit{Months: 12, AmountUAH: 500_000, Revocable: true})
	if r.LadderCoversMonths != 2 {
		t.Errorf("сама тягне %.0f міс., очікували 2 — далі рунга ще не погасилась",
			r.LadderCoversMonths)
	}
	if r.LadderReachMonths != 12 {
		t.Errorf("з розірванням дотягує %.0f міс., очікували 12 — тіло відкличного вкладу "+
			"доступне будь-коли", r.LadderReachMonths)
	}
	if r.LadderGapMonth != 0 || r.LadderGapUAH != 0 {
		t.Errorf("розмін показано дірою (місяць %.0f, %.2f ₴) — це не помилка, а ціна у відсотках",
			r.LadderGapMonth, r.LadderGapUAH)
	}
}

// TestLadderIrrevocableTailIsHole — той самий вклад на ті самі гроші, але
// БЕЗВІДКЛИЧНИЙ, лишає подушку недосяжною з третього місяця.
//
// Різниця з тестом вище — рівно один прапорець, і саме тому вони поруч:
// якщо колись їх зіллють в одну гілку, впаде рівно цей.
func TestLadderIrrevocableTailIsHole(t *testing.T) {
	r := ladderFor(t, 100_000, ReserveDeposit{Months: 12, AmountUAH: 500_000})
	if r.LadderReachMonths != 2 {
		t.Errorf("з розірванням дотягує %.0f міс. — безвідкличний вклад розірвати НЕ можна",
			r.LadderReachMonths)
	}
	if r.LadderGapMonth != 3 {
		t.Errorf("перша діра на %.0f-му місяці, очікували 3", r.LadderGapMonth)
	}
	// 3 × 50 000 = 150 000 витрачено, у руках 100 000.
	if r.LadderGapUAH != 50_000 {
		t.Errorf("бракує %.2f ₴, очікували 50 000", r.LadderGapUAH)
	}
}

// TestLadderHeadIsTheOnlyHardRule — недобрана голова гасить пораду про
// вклад повністю, хай би скільки грошей було в хвості.
//
// Порядок наповнення тут і живе: покласти правильну суму в неправильній
// формі гірше, ніж не покласти нічого.
func TestLadderHeadIsTheOnlyHardRule(t *testing.T) {
	full := ladderFor(t, 100_000, ReserveDeposit{Months: 6, AmountUAH: 200_000, Revocable: true})
	if full.NextRungMonths == 0 {
		t.Fatal("при добраній голові порада про сходинку мусить бути — інакше тест нижче нічого не доводить")
	}
	short := ladderFor(t, 99_999, ReserveDeposit{Months: 6, AmountUAH: 200_000, Revocable: true})
	if short.NextRungMonths != 0 {
		t.Errorf("голова недобрана, а застосунок радить сходинку на %.0f міс. — "+
			"вклад на цьому кроці погіршує доступ, а не покращує", short.NextRungMonths)
	}
	if short.LiquidTargetUAH != 100_000 {
		t.Errorf("вимога голови %.2f ₴, очікували 100 000 (2 міс. × 50 000)", short.LiquidTargetUAH)
	}
}

// TestLadderNextRungTakesFarthestGap — сходинки набираються з ДАЛЬНЬОГО
// кінця, а не з найближчого.
//
// Ближні місяці тримає готівка голови; гроші, які не знадобляться півроку,
// мусять півроку й заробляти. Найближчий непокритий місяць дав би зворотний
// порядок — короткі вклади під найдорожчі за строком гроші.
func TestLadderNextRungTakesFarthestGap(t *testing.T) {
	r := ladderFor(t, 100_000)
	if r.NextRungMonths != 12 {
		t.Errorf("наступна сходинка на %.0f міс., очікували 12 — це найдальший непокритий місяць",
			r.NextRungMonths)
	}
	// Стеля строку обрізає результат, і саме тому картка окремо каже, що
	// робити, коли банк такого строку не пропонує.
	liquidMonths, maxTerm := 2.0, 6.0
	capped := &Reserve{MonthlyExpensesUAH: 50_000, TargetMonths: 12}
	deriveReserveLadder(capped, &SettingsDoc{
		ReserveLiquidMonths: &liquidMonths, ReserveMaxTermMonths: &maxTerm,
	}, DeriveInput{ReserveLiquidUAH: 100_000})
	if capped.NextRungMonths != 6 {
		t.Errorf("зі стелею 6 сходинка на %.0f міс. — стеля мусить обрізати", capped.NextRungMonths)
	}
}

// TestLadderBuildingIsNotAHole — драбина, що розгортається, і драбина з
// дірою розрізняються, хоч покриття в обох неповне.
//
// Це та пара станів, яку найлегше показати однаковим червоним, і саме тому
// пара LadderRungs/LadderRungsTarget існує окремо від LadderGapMonth.
func TestLadderBuildingIsNotAHole(t *testing.T) {
	building := ladderFor(t, 100_000,
		ReserveDeposit{Months: 11, AmountUAH: 250_000, Revocable: true},
		ReserveDeposit{Months: 12, AmountUAH: 250_000, Revocable: true})
	if building.LadderGapMonth != 0 {
		t.Errorf("розгортання показано дірою на %.0f-му місяці", building.LadderGapMonth)
	}
	if building.LadderRungs != 2 || building.LadderRungsTarget != 10 {
		t.Errorf("сходинок %d із %d, очікували 2 з 10 (хвіст 12−2, обрізаний стелею 12)",
			building.LadderRungs, building.LadderRungsTarget)
	}
}

// TestLadderSilentWithoutExpenses — без місячних витрат мовчить усе.
//
// Нулі в документі читались би як «механізм працює і дає нуль», а
// насправді питання не має відповіді: ділити нема на що.
func TestLadderSilentWithoutExpenses(t *testing.T) {
	r := &Reserve{TargetMonths: 12}
	liquidMonths, maxTerm := 2.0, 12.0
	deriveReserveLadder(r, &SettingsDoc{
		ReserveLiquidMonths: &liquidMonths, ReserveMaxTermMonths: &maxTerm,
	}, DeriveInput{ReserveLiquidUAH: 100_000,
		ReserveDeposits: []ReserveDeposit{{Months: 6, AmountUAH: 200_000}}})
	if r.Ladder != nil || r.LiquidUAH != 0 || r.LadderRungs != 0 || r.NextRungMonths != 0 {
		t.Errorf("без витрат драбина заговорила: %+v", r)
	}
}

// TestLadderCoversStopsAtFirstGap — покриття рахується до ПЕРШОГО недобору,
// а не до останнього.
//
// Драбина з дірою посередині — не драбина. Рахуючи далі, застосунок назвав
// би її справною через місяць після того, як вона перестала бути такою.
func TestLadderCoversStopsAtFirstGap(t *testing.T) {
	// Голова 100 000 тягне 2 місяці. Третій порожній. На 4-му приходить
	// велика сходинка, і накопиченого знову вистачає — але 3-й місяць уже
	// прожити не було чим.
	r := ladderFor(t, 100_000, ReserveDeposit{Months: 4, AmountUAH: 500_000})
	if r.LadderCoversMonths != 2 {
		t.Errorf("сама тягне %.0f міс., очікували 2 — далі діра на третьому",
			r.LadderCoversMonths)
	}
}
