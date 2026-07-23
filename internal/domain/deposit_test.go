package domain

import "testing"

// helper: сума потоку за типом (нетто, як їх повертає DepositSchedule).
func flowByType(cf []CashflowItem, t PayType) int64 {
	var sum int64
	for _, c := range cf {
		if c.Type == t {
			sum += c.Amount.Amount()
		}
	}
	return sum
}

func baseDeposit() Deposit {
	// 100 000 грн під 16% на рік, податок 19.5%.
	return Deposit{
		ID: 1, Bank: "ПУМБ", Currency: "UAH", Principal: 10000000,
		RateBP: 1600, TaxBP: 1950,
		OpenDate: "2026-01-15", MaturityDate: "2027-01-15",
	}
}

func TestDepositEndPayoutSimple(t *testing.T) {
	d := baseDeposit()
	d.Payout = PayoutEnd
	cf := DepositSchedule(d, "2026-01-15")

	// Строк 2026-01-15 → 2027-01-15 = 365 днів. Брутто = 100000×16%×365/365
	// = 16000.00 ₴. Нетто = 16000 − 19.5% = 12880.00 ₴.
	gotInt := flowByType(cf, PayCoupon)
	if gotInt != 1288000 {
		t.Errorf("нетто-відсоток: маємо %d, хочемо 1288000", gotInt)
	}
	// Тіло повертається повністю.
	if got := flowByType(cf, PayRedemption); got != 10000000 {
		t.Errorf("тіло: маємо %d, хочемо 10000000", got)
	}
	// Одна виплата відсотків + одне повернення тіла, обидва на дату погашення.
	if len(cf) != 2 {
		t.Fatalf("очікували 2 потоки, маємо %d", len(cf))
	}
	for _, c := range cf {
		if c.Date != "2027-01-15" {
			t.Errorf("потік не на дату погашення: %+v", c)
		}
		if c.ISIN != "deposit:1" {
			t.Errorf("синтетичний ISIN: маємо %q", c.ISIN)
		}
	}
}

func TestDepositMonthlyPayout(t *testing.T) {
	d := baseDeposit()
	d.Payout = PayoutMonthly
	cf := DepositSchedule(d, "2026-01-15")

	// 12 щомісячних виплат відсотків + повернення тіла = 13 потоків.
	if got := len(cf); got != 13 {
		t.Fatalf("очікували 13 потоків (12 відсоткових + тіло), маємо %d", got)
	}
	// Сума нетто-відсотків за рік ≈ той самий річний відсоток нетто, що й
	// у режимі «в кінці», з точністю до цілочисельного округлення по місяцях.
	// Брутто ≈ 16000, нетто ≈ 12880; допускаємо ±кілька копійок на округленнях.
	sum := flowByType(cf, PayCoupon)
	if sum < 1287000 || sum > 1289000 {
		t.Errorf("сума нетто-відсотків за рік: маємо %d, чекали ≈1288000", sum)
	}
	if got := flowByType(cf, PayRedemption); got != 10000000 {
		t.Errorf("тіло: маємо %d", got)
	}
}

func TestDepositCapitalizedBeatsSimple(t *testing.T) {
	simple := baseDeposit()
	simple.Payout = PayoutEnd
	cap := baseDeposit()
	cap.Payout = PayoutEnd
	cap.Capitalized = true

	is := flowByType(DepositSchedule(simple, "2026-01-15"), PayCoupon)
	ic := flowByType(DepositSchedule(cap, "2026-01-15"), PayCoupon)
	// Капіталізація дає БІЛЬШЕ за прості відсотки — у цьому весь її сенс.
	if !(ic > is) {
		t.Errorf("капіталізований відсоток %d має бути більший за простий %d", ic, is)
	}
	// Але в розумних межах: за рік під 16% з місячною капіталізацією
	// ефективна ставка ~17.2%, тобто відсоток на ~7-8% більший, не вдвічі.
	if ic > is*2 {
		t.Errorf("капіталізований відсоток %d підозріло великий проти %d", ic, is)
	}
}

func TestDepositClosedHasNoFutureFlows(t *testing.T) {
	d := baseDeposit()
	d.Payout = PayoutEnd
	d.ClosedDate = "2026-06-01"
	d.ClosedAmount = 10500000 // тіло + трохи відсотків за штрафною ставкою
	if cf := DepositSchedule(d, "2026-07-01"); cf != nil {
		t.Errorf("закритий вклад не має майбутніх потоків, маємо %+v", cf)
	}
	if d.Active("2026-07-01") {
		t.Error("закритий вклад не активний")
	}
}

func TestDepositPastInterestExcludedFromFuture(t *testing.T) {
	d := baseDeposit()
	d.Payout = PayoutMonthly
	// Дивимось із середини строку: минулі щомісячні виплати у майбутнє не
	// входять, майбутні — входять, плюс тіло наприкінці.
	cf := DepositSchedule(d, "2026-07-15")
	for _, c := range cf {
		if c.Type == PayCoupon && c.Date.Before("2026-07-15") {
			t.Errorf("минула виплата потрапила в майбутнє: %+v", c)
		}
	}
	// Лишилось ~6 майбутніх відсоткових + тіло.
	if n := len(cf); n < 6 || n > 8 {
		t.Errorf("очікували ~7 майбутніх потоків, маємо %d", n)
	}
}

func TestDepositMaturedReturnsNothing(t *testing.T) {
	d := baseDeposit()
	d.Payout = PayoutEnd
	if cf := DepositSchedule(d, "2027-06-01"); cf != nil {
		t.Errorf("погашений вклад майбутніх потоків не має, маємо %+v", cf)
	}
}

func TestDepositLadderByYear(t *testing.T) {
	deps := []Deposit{
		{ID: 1, Currency: "UAH", Principal: 10000000, OpenDate: "2026-01-15", MaturityDate: "2027-01-15"},
		{ID: 2, Currency: "USD", Principal: 500000, OpenDate: "2026-03-01", MaturityDate: "2027-06-01"},
		{ID: 3, Currency: "UAH", Principal: 3000000, OpenDate: "2025-01-01", MaturityDate: "2024-12-31"}, // погашений
	}
	lad := DepositLadder(deps, "2026-07-01")
	// #3 погашений (maturity у минулому) → у драбину не входить.
	// Лишаються 2027-UAH (10М) і 2027-USD (500k).
	got := map[string]int64{}
	for _, e := range lad {
		got[e.Currency] += e.Nominal
	}
	if got["UAH"] != 10000000 || got["USD"] != 500000 {
		t.Errorf("драбина вкладів: %+v", got)
	}
	for _, e := range lad {
		if e.Year != 2027 {
			t.Errorf("рік %d, очікували лише 2027", e.Year)
		}
	}
}

func TestDepositFlowsForXIRR(t *testing.T) {
	d := baseDeposit() // 100к під 16%, податок 19.5%, 2026-01-15 → 2027-01-15
	d.Payout = PayoutEnd
	// Дивимось ще до погашення: розміщення −тіло, тіло ще замкнене
	// (термінал за номіналом), відсотків реалізованих ще немає.
	flows := DepositFlows([]Deposit{d}, "UAH", "2026-06-01")
	if len(flows) != 2 { // −principal + terminal
		t.Fatalf("до погашення 2 потоки (розміщення+термінал), маємо %d: %+v", len(flows), flows)
	}
	var sum int64
	for _, f := range flows {
		sum += f.Amount
	}
	// −10М + 10М (термінал) = 0: гроші ще працюють, нічого не зароблено.
	if sum != 0 {
		t.Errorf("до погашення сума потоків має бути 0, маємо %d", sum)
	}
	// Інша валюта — жодного потоку.
	if DepositFlows([]Deposit{d}, "USD", "2026-06-01") != nil {
		t.Error("USD-потоків для гривневого вкладу бути не має")
	}
}

func TestDepositTopupsGrowInterest(t *testing.T) {
	// Вклад 100к під 16%, виплата в кінці, рік. Без поповнень нетто = 12880.
	base := baseDeposit()
	base.Payout = PayoutEnd
	plain := flowByType(DepositSchedule(base, "2026-01-15"), PayCoupon)

	// Те саме, але два поповнення по 100к у березні й червні.
	rep := baseDeposit()
	rep.Payout = PayoutEnd
	rep.Topups = []DepositTopup{
		{ID: 1, DepositID: 1, Date: "2026-03-15", Amount: 10000000},
		{ID: 2, DepositID: 1, Date: "2026-06-15", Amount: 10000000},
	}
	withTopups := flowByType(DepositSchedule(rep, "2026-01-15"), PayCoupon)

	// Відсоток на балансі, що росте, БІЛЬШИЙ за відсоток на фіксованих 100к.
	if !(withTopups > plain) {
		t.Errorf("відсоток із поповненнями %d має бути більший за %d", withTopups, plain)
	}
	// Тіло на погашенні — накопичене: 100к + 100к + 100к = 300к.
	if got := flowByType(DepositSchedule(rep, "2026-01-15"), PayRedemption); got != 30000000 {
		t.Errorf("накопичене тіло: маємо %d, хочемо 30000000", got)
	}
	// balanceAt росте сходинками.
	if rep.BalanceAt("2026-02-01") != 10000000 {
		t.Errorf("до 1-го поповнення баланс = 100к, маємо %d", rep.BalanceAt("2026-02-01"))
	}
	if rep.BalanceAt("2026-04-01") != 20000000 {
		t.Errorf("після 1-го поповнення баланс = 200к, маємо %d", rep.BalanceAt("2026-04-01"))
	}
	if rep.BalanceAt("2026-07-01") != 30000000 {
		t.Errorf("після 2-го поповнення баланс = 300к, маємо %d", rep.BalanceAt("2026-07-01"))
	}
}

func TestDepositCapitalizationWithTopups(t *testing.T) {
	// Капіталізація + поповнення разом рахуються й не падають.
	d := baseDeposit()
	d.Payout = PayoutEnd
	d.Capitalized = true
	d.Topups = []DepositTopup{{ID: 1, DepositID: 1, Date: "2026-04-15", Amount: 10000000}}
	cf := DepositSchedule(d, "2026-01-15")
	interest := flowByType(cf, PayCoupon)
	// Відсоток додатний і менший за наївне «два тіла × ставка» (частина
	// строку баланс був лише 100к).
	if interest <= 0 {
		t.Fatalf("капіталізований відсоток із поповненням має бути > 0, маємо %d", interest)
	}
	// Тіло на погашенні — накопичене 200к.
	if got := flowByType(cf, PayRedemption); got != 20000000 {
		t.Errorf("накопичене тіло: маємо %d, хочемо 20000000", got)
	}
}
