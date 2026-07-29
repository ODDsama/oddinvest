package api

import (
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/store"
)

// TestMonthReserveMoveNetsToZero — переміщення гаманець → матрац не є
// внеском, хоч і записане двома окремими рухами.
//
// Це той самий інваріант, який описує коментар у buildMonth, і його треба
// перевіряти прямо. Golden цього не робить: рухи резерву в багатій
// фікстурі стоять у травні-червні, а документ будується на 15 липня, тож
// гілка резерву в місячному циклі там не виконується взагалі — мутація
// «прибрати резерв із внесків» golden не завалила.
//
// Ціна помилки тут висока в обидва боки. Порахувати лише першу ногу —
// показати втрату капіталу, якої не було. Не рахувати другу — показати
// внесок там, де гроші просто переклали з кишені в кишеню.
func TestMonthReserveMoveNetsToZero(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	d := func(off int) domain.Date { return domain.NewDate(now.AddDate(0, 0, off)) }

	src := &sources{
		deposits: []store.Deposit{
			// Гроші пішли з рахунку брокера в матрац.
			{Date: d(-3), Amount: -100_000, Currency: money.UAH, Broker: "mono"},
		},
		reserveOps: []store.ReserveOp{
			// Та сама сума прийшла в матрац — друга нога переміщення.
			{Date: d(-3), Amount: 100_000, Currency: money.UAH, Place: "готівка"},
		},
	}
	out, err := buildMonth(src, domain.Holdings{}, fx.Rates{}, now, today)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.DepositedUAH.Amount(); got != 0 {
		t.Errorf("переміщення в резерв дало внесок %d, а мусить дати 0 — нових грошей не з'явилось", got)
	}
	// Зняття при цьому чесне: гроші справді пішли з рахунку брокера, і
	// картка «знято цього місяця» має це показати.
	if got := out.WithdrawnUAH.Amount(); got != 100_000 {
		t.Errorf("знято %d, очікували 100000", got)
	}
}

// TestMonthExternalReserveIsContribution — гроші, відкладені в матрац
// ЗЗОВНІ (на рахунок брокера вони не заходили), це справжній внесок.
//
// Дзеркало попереднього тесту: разом вони фіксують, що резерв рахується
// в тому самому нетто, що й поповнення, а не окремим правилом.
func TestMonthExternalReserveIsContribution(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)

	src := &sources{
		reserveOps: []store.ReserveOp{
			{Date: domain.NewDate(now.AddDate(0, 0, -2)), Amount: 50_000,
				Currency: money.UAH, Place: "сейф"},
		},
	}
	out, err := buildMonth(src, domain.Holdings{}, fx.Rates{}, now, today)
	if err != nil {
		t.Fatal(err)
	}
	if got := out.DepositedUAH.Amount(); got != 50_000 {
		t.Errorf("внесено %d, очікували 50000 — відкладене зовні теж внесок", got)
	}
	if got := out.WithdrawnUAH.Amount(); got != 0 {
		t.Errorf("знято %d, а знять не було", got)
	}
}
