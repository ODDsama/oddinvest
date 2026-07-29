package api

import (
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
)

// TestLiquidityDoesNotCountMaturingDepositTwice — вклад, що гаситься
// всередині 90-денного вікна, не лічиться ще й «замкненим».
//
// Його тіло вже прийшло у in_90_uah потоками з календаря, тож додати його
// до locked_uah означало б показати ті самі гроші двічі: і як доступні
// незабаром, і як недоступні. На картці ліквідності це читається прямо
// протилежно тому, що є.
//
// Golden цього не стереже: у багатій фікстурі діючий вклад гаситься аж
// через 275 днів, тобто далеко за вікном, а другий закритий достроково —
// мутація «прибрати перевірку строку» golden не завалила.
func TestLiquidityDoesNotCountMaturingDepositTwice(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	d := func(off int) domain.Date { return domain.NewDate(now.AddDate(0, 0, off)) }

	deps := []domain.Deposit{
		// Гаситься за 60 днів — усередині вікна.
		{Bank: "ПУМБ", Currency: money.UAH, Principal: 5_000_00,
			OpenDate: d(-300), MaturityDate: d(60), RateBP: 1600},
		// Гаситься за 400 днів — далеко за вікном, справді замкнений.
		{Bank: "mono", Currency: money.UAH, Principal: 7_000_00,
			OpenDate: d(-100), MaturityDate: d(400), RateBP: 1600},
	}
	out := buildRisk(riskInput{
		TermDeposits: deps, Rates: fx.Rates{},
		AccountMinor: 1_000_00, Now: now, Today: today,
	})

	if got := out.Liquidity.LockedUAH; got != 7000 {
		t.Errorf("замкнено %v, очікували 7000: вклад, що гаситься за 60 днів, "+
			"уже стоїть у in_90 і замкненим бути не може", got)
	}
	if got := out.Liquidity.UnlockDate; got != string(d(400)) {
		t.Errorf("дата розблокування %q, очікували %q — найближчий СПРАВДІ замкнений вклад",
			got, d(400))
	}
}

// TestLiquidityWindowsAreCumulative — «за 90 днів» містить «за 30».
//
// Так на них і дивляться: скільки буде в розпорядженні на той момент,
// якщо нічого не купувати. Вікна, що не перекриваються, дали б криву, де
// доступних грошей через три місяці МЕНШЕ, ніж через місяць.
func TestLiquidityWindowsAreCumulative(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	d := func(off int) domain.Date { return domain.NewDate(now.AddDate(0, 0, off)) }

	cf := []domain.CashflowItem{
		{Date: d(10), ISIN: "UA1", Type: domain.PayCoupon, Amount: money.New(100_00, money.UAH)},
		{Date: d(70), ISIN: "UA1", Type: domain.PayCoupon, Amount: money.New(300_00, money.UAH)},
		{Date: d(200), ISIN: "UA1", Type: domain.PayCoupon, Amount: money.New(900_00, money.UAH)},
	}
	out := buildRisk(riskInput{
		Cashflow: cf, Rates: fx.Rates{},
		AccountMinor: 1_000_00, Now: now, Today: today,
	})

	if out.Liquidity.NowUAH != 1000 {
		t.Errorf("зараз %v, очікували 1000", out.Liquidity.NowUAH)
	}
	if out.Liquidity.In30UAH != 1100 {
		t.Errorf("за 30 днів %v, очікували 1100 (рахунок + купон на 10-й день)", out.Liquidity.In30UAH)
	}
	if out.Liquidity.In90UAH != 1400 {
		t.Errorf("за 90 днів %v, очікували 1400 — вікно НАКОПИЧУВАЛЬНЕ й містить перше",
			out.Liquidity.In90UAH)
	}
	if out.Liquidity.In90UAH < out.Liquidity.In30UAH {
		t.Error("за 90 днів доступно менше, ніж за 30 — вікна перестали перекриватись")
	}
}
