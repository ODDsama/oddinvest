package api

import (
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/store"

	money "github.com/Rhymond/go-money"
)

// uahOnly — курси, яких вистачає гривні. Валютні перевірки тут не потрібні:
// FX перевіряється власними тестами, а питання цього файла — розподіл.
func uahOnly() fx.Rates { return nil }

func loanFixture(taken, rate int64, date string) store.ReserveLoan {
	return store.ReserveLoan{ID: 1, OpID: 1, RateBP: rate, TakenDate: domain.Date(date),
		TakenAmount: taken, TakenCurrency: money.UAH}
}

// Поповнення БЕЗ привʼязки гасить позику — і це не зручність. «Розкласти
// N ₴» і ноги «Маршруту грошей» постять у /api/reserve, нічого не знаючи
// про позики; без цього правила борг не гасився б ніколи.
func TestReserveLoanFreeTopUpRepaysIt(t *testing.T) {
	loans := []store.ReserveLoan{loanFixture(1_200_000, 1200, "2026-06-01")}
	ops := []store.ReserveOp{
		{ID: 1, Date: domain.Date("2026-06-01"), Amount: -1_200_000, Currency: money.UAH},
		{ID: 2, Date: domain.Date("2026-06-15"), Amount: 500_000, Currency: money.UAH},
	}
	got := reserveLoans(loans, ops, domain.Date("2026-07-01"), uahOnly())
	if len(got) != 1 {
		t.Fatalf("позик %d, чекали 1 (частково повернена)", len(got))
	}
	if got[0].OwedUAH >= 12000 {
		t.Errorf("залишок %.2f не зменшився на поповнення", got[0].OwedUAH)
	}
}

// Повне повернення закриває позику, і вона зникає з картки: та показує
// обіцянки, а не історію.
func TestReserveLoanFullTopUpClosesIt(t *testing.T) {
	loans := []store.ReserveLoan{loanFixture(1_200_000, 1200, "2026-06-01")}
	ops := []store.ReserveOp{
		{ID: 1, Date: domain.Date("2026-06-01"), Amount: -1_200_000, Currency: money.UAH},
		{ID: 2, Date: domain.Date("2026-06-15"), Amount: 5_000_000, Currency: money.UAH},
	}
	if got := reserveLoans(loans, ops, domain.Date("2026-07-01"), uahOnly()); len(got) != 0 {
		t.Fatalf("позика лишилась відкритою після поповнення вп'ятеро більшого: %+v", got)
	}
}

// Поповнення, СТАРІШЕ за позику, її не гасить — і не затуляє собою ті, що
// після неї. Саме на цьому проваленому випадку борг переставав танути
// взагалі: одна давня гривня в голові черги блокувала всі наступні.
func TestReserveLoanOlderTopUpDoesNotBlockQueue(t *testing.T) {
	loans := []store.ReserveLoan{loanFixture(1_200_000, 0, "2026-06-01")}
	ops := []store.ReserveOp{
		{ID: 9, Date: domain.Date("2026-01-01"), Amount: 3_000_000, Currency: money.UAH},
		{ID: 1, Date: domain.Date("2026-06-01"), Amount: -1_200_000, Currency: money.UAH},
		{ID: 2, Date: domain.Date("2026-06-15"), Amount: 400_000, Currency: money.UAH},
	}
	got := reserveLoans(loans, ops, domain.Date("2026-07-01"), uahOnly())
	if len(got) != 1 {
		t.Fatalf("позик %d, чекали 1", len(got))
	}
	if got[0].OwedUAH != 8000 {
		t.Errorf("залишок %.2f, чекали 8000: давнє поповнення не гасить, свіже гасить", got[0].OwedUAH)
	}
}

// Гривневе поповнення НЕ гасить доларову позику. Узяв 200 доларів — винен
// 200 доларів; крім того, суми в журналі мінорні й без валюти, тож 1 000
// копійок, застосовані до доларової позики, стали б десятьма доларами.
func TestReserveLoanCurrencyQueuesAreSeparate(t *testing.T) {
	loans := []store.ReserveLoan{{ID: 1, OpID: 1, RateBP: 0,
		TakenDate: domain.Date("2026-06-01"), TakenAmount: 20_000, TakenCurrency: money.USD}}
	ops := []store.ReserveOp{
		{ID: 1, Date: domain.Date("2026-06-01"), Amount: -20_000, Currency: money.USD},
		{ID: 2, Date: domain.Date("2026-06-15"), Amount: 5_000_000, Currency: money.UAH},
	}
	got := reserveLoans(loans, ops, domain.Date("2026-07-01"), fx.Rates{money.USD: 440000})
	if len(got) != 1 {
		t.Fatalf("гривневе поповнення закрило доларову позику: %+v", got)
	}
	if got[0].TakenNative != 200 || got[0].Currency != money.USD {
		t.Errorf("тіло %v %s, чекали 200 USD", got[0].TakenNative, got[0].Currency)
	}
}

// Явна привʼязка чужою валютою мовчки не спрацьовує: сума пішла б у
// залишок як своя й збрехала б у стільки разів, у скільки різняться курси.
// Рядок при цьому не зникає — він лишається звичайним поповненням.
func TestReserveLoanForeignRepayBindingIsIgnored(t *testing.T) {
	loans := []store.ReserveLoan{{ID: 1, OpID: 1, RateBP: 0,
		TakenDate: domain.Date("2026-06-01"), TakenAmount: 20_000, TakenCurrency: money.USD}}
	ops := []store.ReserveOp{
		{ID: 1, Date: domain.Date("2026-06-01"), Amount: -20_000, Currency: money.USD},
		{ID: 2, Date: domain.Date("2026-06-15"), Amount: 20_000, Currency: money.UAH, LoanID: 1},
	}
	got := reserveLoans(loans, ops, domain.Date("2026-07-01"), fx.Rates{money.USD: 440000})
	if len(got) != 1 {
		t.Fatalf("гривневе повернення закрило доларову позику: %+v", got)
	}
}

// Одне поповнення закриває дві позики: залишок переходить наступній, а не
// згорає разом із першою.
func TestReserveLoanOneTopUpClosesTwoLoans(t *testing.T) {
	loans := []store.ReserveLoan{
		{ID: 1, OpID: 1, RateBP: 0, TakenDate: domain.Date("2026-06-01"),
			TakenAmount: 100_000, TakenCurrency: money.UAH},
		{ID: 2, OpID: 2, RateBP: 0, TakenDate: domain.Date("2026-06-02"),
			TakenAmount: 100_000, TakenCurrency: money.UAH},
	}
	ops := []store.ReserveOp{
		{ID: 1, Date: domain.Date("2026-06-01"), Amount: -100_000, Currency: money.UAH},
		{ID: 2, Date: domain.Date("2026-06-02"), Amount: -100_000, Currency: money.UAH},
		{ID: 3, Date: domain.Date("2026-06-10"), Amount: 200_000, Currency: money.UAH},
	}
	if got := reserveLoans(loans, ops, domain.Date("2026-07-01"), uahOnly()); len(got) != 0 {
		t.Fatalf("одне поповнення мало закрити обидві позики, лишилось: %+v", got)
	}
}

// Прострочена — лише позика з дедлайном. Без дати людина собі нічого не
// обіцяла, і фарбувати її червоним означало б вимагати того, чого не було.
func TestReserveLoanOverdueNeedsDueDate(t *testing.T) {
	withDue := []store.ReserveLoan{loanFixture(1_200_000, 0, "2026-06-01")}
	withDue[0].DueDate = "2026-06-20"
	ops := []store.ReserveOp{{ID: 1, Date: domain.Date("2026-06-01"), Amount: -1_200_000, Currency: money.UAH}}

	got := reserveLoans(withDue, ops, domain.Date("2026-07-01"), uahOnly())
	if len(got) != 1 || !got[0].Overdue {
		t.Fatalf("позика з минулим дедлайном не позначена простроченою: %+v", got)
	}
	noDue := []store.ReserveLoan{loanFixture(1_200_000, 0, "2026-06-01")}
	got = reserveLoans(noDue, ops, domain.Date("2026-07-01"), uahOnly())
	if len(got) != 1 || got[0].Overdue {
		t.Fatalf("позика без дедлайну позначена простроченою: %+v", got)
	}
}

// Надбавка до цілі — це САМЕ ВІДСОТОК, а не залишок: тіло вже вирахуване
// з подушки самим зняттям. Цей тест і є той запобіжник від подвійного
// рахунку, заради якого фіча не стала третім debts.kind.
func TestReserveOwedInterestIsInterestNotBody(t *testing.T) {
	loans := []store.ReserveLoan{loanFixture(1_200_000, 1200, "2026-06-01")}
	ops := []store.ReserveOp{{ID: 1, Date: domain.Date("2026-06-01"), Amount: -1_200_000, Currency: money.UAH}}
	got := reserveLoans(loans, ops, domain.Date("2026-07-01"), uahOnly())
	markup := reserveOwedInterestUAH(got)
	if markup <= 0 || markup >= 1000 {
		t.Fatalf("надбавка %.2f — вона мусить бути відсотком за 30 днів, а не тілом 12 000", markup)
	}
	if got[0].OwedUAH-got[0].InterestUAH != 12000 {
		t.Errorf("залишок мінус відсоток = %.2f, чекали тіло 12 000",
			got[0].OwedUAH-got[0].InterestUAH)
	}
}
