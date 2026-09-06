// Другий прохід розкладки: залишок, який не склався в цілий квиток свого
// виду, пропонується тим, хто ще недобирає до власної частки.
//
// Фікстури тут навмисне такі, щоб перший прохід ЛИШАВ хвіст: у решті
// тестів розкладки бюджети діляться націло, прохід не запускається, і
// перевіряти в них нема чого.

package api

import (
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
)

// npfSug і allocDoc живуть у handlers_allocate_test.go — тут вони лише
// читаються: другий екземпляр тієї самої поради розійшовся б із першим
// рівно тоді, коли в suggestion додасться поле.

// ГОЛОВНИЙ ВИПАДОК ФАЗИ, знятий із живого екрана: хвіст бюджету ОВДП, якого
// не вистачає на шостий папір, доїжджає в пенсійний — той нижче цілі й
// приймає будь-яку суму. Доти ці гроші просто лежали.
func TestAllocateTopUpTailGoesToNPF(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{
		kindRow("bonds", 90, 50000),
		kindRow("npf", 10, 0),
	}, nil)
	npfID := map[string]int64{"Династія": 7}
	got := allocatePlan(doc, []suggestion{
		bondSug("UA0001", 1000, money.UAH), npfSug("Династія"),
	}, allocRates, toMoneyJSON(money.New(134000, money.UAH)), 1340,
		allocAllow{ReserveUAH: 1340, GoalsUAH: 1340}, money.UAH, npfID)

	if got.RestUAH != 0 {
		t.Errorf("залишок %.2f, чекали 0: хвіст мав доїхати в пенсійний", got.RestUAH)
	}
	var npf *allocLine
	n := 0
	for i := range got.Lines {
		if got.Lines[i].Kind == "npf" {
			npf, n = &got.Lines[i], n+1
		}
	}
	if n != 1 {
		t.Fatalf("рядків НПФ %d, чекали рівно один — прохід мусить РОСТИТИ рядок, а не додавати другий", n)
	}
	// Сума розкладки дорівнює тому, що прийшло: гроші не гинуть ніде.
	spent := 0.0
	for _, l := range got.Lines {
		spent += l.TotalUAH
	}
	if d := spent + got.RestUAH - 1340; d > 0.01 || d < -0.01 {
		t.Errorf("розклали %.2f + залишок %.2f, а прийшло 1340", spent, got.RestUAH)
	}
	if npf.TotalUAH <= 264 {
		t.Errorf("внесок %.2f — прохід нічого не додав до бюджету виду", npf.TotalUAH)
	}
}

// ПРОХІД НЕ ПЕРЕСТРИБУЄ ЦІЛЬ. Це та властивість, яка дає право взагалі
// чіпати залишок: він уміє лише скоротити недобір і не вміє штовхнути
// вид понад його частку. НПФ тут найгостріший — allocOne віддає йому все,
// що дали, тож без обрізання недобором він з'їв би весь хвіст.
func TestAllocateTopUpNeverOvershoots(t *testing.T) {
	// НПФ уже майже на своїй частці: недобір копійчаний.
	doc := allocDoc([]state.RebalanceRow{
		kindRow("bonds", 90, 50000),
		kindRow("npf", 10, 10130),
	}, nil)
	got := allocatePlan(doc, []suggestion{
		bondSug("UA0001", 1000, money.UAH), npfSug("Династія"),
	}, allocRates, toMoneyJSON(money.New(134000, money.UAH)), 1340,
		allocAllow{ReserveUAH: 1340, GoalsUAH: 1340}, money.UAH,
		map[string]int64{"Династія": 7})

	for _, l := range got.Lines {
		if l.Kind != "npf" {
			continue
		}
		// Частка НПФ від бази «капітал + сума» — стеля, вище якої прохід
		// піднятись не має права. База: 100 000 + 1 340.
		if max := (100000 + 1340) * 0.10; l.TotalUAH+10130 > max+0.01 {
			t.Errorf("внесок %.2f підняв НПФ понад його частку (%.2f при стелі %.2f)",
				l.TotalUAH, l.TotalUAH+10130, max)
		}
	}
}

// ЗАБОРОНЕНИЙ ДОЗВОЛОМ ВИД ЗАЛИШКУ НЕ ДІСТАЄ — навіть коли недобір у нього
// найбільший і взяти він може будь-яку суму.
//
// Найважливіший тест пачки. Перший прохід гасить такий вид ціллю ще до
// поділу, тож у бюджетах його немає; кандидати ж другого проходу
// будуються окремо, і без власної перевірки він пролазив би туди чорним
// ходом — тобто зарплата, позначена «не в пенсійний», вносилась би в
// пенсійний.
func TestAllocateTopUpRespectsUses(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{
		kindRow("bonds", 90, 50000),
		kindRow("npf", 10, 0),
	}, nil)
	got := allocatePlan(doc, []suggestion{
		bondSug("UA0001", 1000, money.UAH), npfSug("Династія"),
	}, allocRates, toMoneyJSON(money.New(134000, money.UAH)), 1340,
		allocAllow{
			ReserveUAH: 1340, GoalsUAH: 1340,
			// Усе, крім пенсійного.
			Uses: domain.UsePlanReserve + "," + domain.UsePlanGoals + "," + domain.UsePlanInvest,
		}, money.UAH, map[string]int64{"Династія": 7})

	for _, l := range got.Lines {
		if l.Kind == "npf" {
			t.Fatalf("внесок у заборонений пенсійний: %+v", l)
		}
	}
	if got.RestUAH <= 0 {
		t.Error("залишок мав лишитись: єдиний приймач для нього закритий дозволом")
	}
}

// Залишок нижче порога руху лишається залишком. Поріг тут не окремий —
// це та сама allocStepUAH, якою міряється придатність кандидата, тож
// порада «віднеси в пенсійний три гривні» неможлива за побудовою.
func TestAllocateTopUpKeepsFloor(t *testing.T) {
	// Обидва види по 50%, обидва з нуля, 2 004 ₴. Бюджети виходять по
	// 1 002 ₴: ОВДП бере один папір і лишає 2 ₴, пенсійний забирає свій
	// бюджет цілком. Недобір у пенсійного при цьому величезний — тобто
	// приймач для цих 2 ₴ Є, і не бере він їх саме через поріг.
	doc := allocDoc([]state.RebalanceRow{
		kindRow("bonds", 50, 0),
		kindRow("npf", 50, 0),
	}, nil)
	got := allocatePlan(doc, []suggestion{
		bondSug("UA0001", 1000, money.UAH), npfSug("Династія"),
	}, allocRates, toMoneyJSON(money.New(200400, money.UAH)), 2004,
		allocAllow{ReserveUAH: 2004, GoalsUAH: 2004}, money.UAH,
		map[string]int64{"Династія": 7})

	if got.RestUAH < 0.005 || got.RestUAH >= allocMinCutUAH {
		t.Fatalf("залишок %.2f — фікстура мала дати хвіст МЕНШИЙ за поріг %.0f",
			got.RestUAH, float64(allocMinCutUAH))
	}
	for _, l := range got.Lines {
		if l.Kind != "npf" {
			continue
		}
		if l.TotalUAH > 1002.01 {
			t.Errorf("внесок %.2f: прохід протягнув у пенсійний хвіст нижче порога", l.TotalUAH)
		}
	}
	if got.RestWhy == "" {
		t.Error("залишок без причини читається як загублені гроші")
	}
}

// БОРГ ЗАЛИШКУ НЕ ДІСТАЄ, і після фази 45 це вже не про чергу приймачів, а
// про весь контур: дострокове погашення не забирає портфельних грошей
// ніде. Симетрія з подушкою й цілями здається очевидною — «борг під
// пʼятдесят відсотків дорожчий за будь-який вид», — тож саме сюди
// потягнеться наступний автор.
//
// Перевіряється це тепер сумою: усе, що не пішло в подушку й цілі, або
// куплене, або лишилось у залишку. Третього призначення немає, і поля під
// нього в allocPlan теж немає.
func TestAllocateTopUpLeavesDebtAlone(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 50000)}, nil)
	// Борг живий, дорогий і зі стелею — усе, що колись вмикало вирізку.
	doc.Debt = &state.DebtPlan{
		TotalUAH: 90000, TopRatePct: 49.8, TopName: "Холодильник",
		FillMonthUAH: 2000, FillNowUAH: 2000,
	}
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(134000, money.UAH)), 1340,
		allocAllow{ReserveUAH: 1340, GoalsUAH: 1340}, money.UAH, nil)

	spent := 0.0
	for _, l := range got.Lines {
		spent += l.TotalUAH
	}
	if d := spent + got.RestUAH - 1340; d > 0.01 || d < -0.01 {
		t.Errorf("куплено %.2f + залишок %.2f ≠ 1340: частина грошей пішла кудись ще",
			spent, got.RestUAH)
	}
	if got.RestUAH <= 0 {
		t.Error("залишок мав лишитись: інших приймачів у фікстурі немає")
	}
}

// ДОЗВІЛ МІСЯЦЯ ТРИМАЄ І ДРУГИЙ ПРОХІД. Це латка живого дефекту, не нова
// властивість.
//
// Прохід має право обійти ТЕМП (goals_fill_share_pct): гроші, яким інакше
// нема куди подітись, краще віддати цілі, що не встигає до дати. Але темп і
// ДОЗВІЛ місяця (PlanGoalsUAH — «скільки з доходу взагалі можна вести в
// цілі») були перемножені в FillMonthUAH одним числом, тож обходячи стелю,
// прохід обходив і дозвіл. Ціль діставала більше, ніж місяць їй дає.
//
// Саме через цей витік у чергу не пускали подушку — а цілі пропустили.
func TestAllocateTopUpGoalRespectsMonthAllowance(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 50000)}, nil)
	doc.Goals = []state.Goal{{
		ID: 1, Name: "Авто", Currency: money.UAH,
		GapUAH: 500_000, DueDate: "2027-06-01",
		// Не встигає — отже кандидат першого ярусу.
		ShortMonthUAH: 30_000,
		// А місяць дозволяє їй лише тисячу.
		FillFromUAH: 1000,
	}}
	// Папір НЕДОСЯЖНИЙ навмисно: інакше бюджет ОВДП зʼїв би 1 000 ₴, у
	// залишок пішло б 340, і тест не дійшов би до дозволу взагалі —
	// перевіряв би те, що менше за нього самого.
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 5000, money.UAH)},
		allocRates, toMoneyJSON(money.New(134000, money.UAH)), 1340,
		allocAllow{ReserveUAH: 1340, GoalsUAH: 1340}, money.UAH, nil)

	if got.GoalsUAH > 1000.01 {
		t.Errorf("ціль узяла %.2f при дозволі місяця 1000 — прохід обійшов не лише темп",
			got.GoalsUAH)
	}
}

// Дзеркально: дозвіл нульовий — місяць увесь позначено «не в цілі», і прохід
// не дає нічого, хоч ціль і не встигає. Нуль тут означає заборону, а не
// «без обмежень», і саме тому поле не має omitempty-семантики «немає».
func TestAllocateTopUpGoalSilentWithoutAllowance(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 50000)}, nil)
	doc.Goals = []state.Goal{{
		ID: 1, Name: "Авто", Currency: money.UAH,
		GapUAH: 500_000, DueDate: "2027-06-01", ShortMonthUAH: 30_000,
	}}
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(134000, money.UAH)), 1340,
		allocAllow{ReserveUAH: 1340, GoalsUAH: 1340}, money.UAH, nil)

	if got.GoalsUAH > 0.005 {
		t.Errorf("ціль узяла %.2f без дозволу місяця", got.GoalsUAH)
	}
}

// Подушка тепер У ЧЕРЗІ — термінальним приймачем, — і теж під дозволом.
func TestAllocateTopUpReserveTakesTailWithinAllowance(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 50000)},
		// Стелю темпу вже вибрано (FillNowUAH = 0), розрив великий, дозвіл
		// місяця — 200 ₴. Прохід має взяти рівно 200, а не весь хвіст.
		&state.Reserve{FillNowUAH: 0, FillMonthUAH: 0, GapUAH: 90000, FillFromUAH: 200})
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(134000, money.UAH)), 1340,
		allocAllow{ReserveUAH: 1340, GoalsUAH: 1340}, money.UAH, nil)

	if got.Reserve == nil {
		t.Fatal("подушка не взяла нічого, хоч розрив живий і дозвіл є")
	}
	if got.Reserve.AmountUAH > 200.01 {
		t.Errorf("подушка взяла %.2f при дозволі місяця 200", got.Reserve.AmountUAH)
	}
}

// Без дозволу місяця подушка мовчить — той самий випадок, що
// TestRouteMonthCeilingBindsCouponToo, лише в другому проході.
func TestAllocateTopUpReserveSilentWithoutAllowance(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{kindRow("bonds", 100, 50000)},
		&state.Reserve{FillNowUAH: 0, FillMonthUAH: 0, GapUAH: 90000})
	got := allocatePlan(doc, []suggestion{bondSug("UA0001", 1000, money.UAH)},
		allocRates, toMoneyJSON(money.New(134000, money.UAH)), 1340,
		allocAllow{ReserveUAH: 1340, GoalsUAH: 1340}, money.UAH, nil)

	if got.Reserve != nil {
		t.Errorf("подушка взяла %+v без дозволу місяця", got.Reserve)
	}
}

// Вибір паперу діє й у другому проході. Інакше тому, хто обрав дорогий
// папір, мовчки купили б рейтинговий із хвоста — рівно те, від чого людина
// втекла, обираючи.
func TestAllocateTopUpHonoursPick(t *testing.T) {
	doc := allocDoc([]state.RebalanceRow{
		kindRow("bonds", 90, 50000),
		kindRow("npf", 10, 0),
	}, nil)
	got := allocatePlan(doc, []suggestion{
		bondSug("UA0001", 1000, money.UAH),
		bondSug("UA0002", 5000, money.UAH),
		npfSug("Династія"),
	}, allocRates, toMoneyJSON(money.New(134000, money.UAH)), 1340,
		allocAllow{ReserveUAH: 1340, GoalsUAH: 1340, PickISIN: "UA0002"},
		money.UAH, map[string]int64{"Династія": 7})

	for _, l := range got.Lines {
		if l.Kind == "bond" && l.Ref != "UA0002" {
			t.Errorf("куплено не обраний папір: %+v", l)
		}
	}
}
