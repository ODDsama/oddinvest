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

// TestLiquiditySplitsLockedFromBreakable — розривність вкладу розводить
// гроші по різних рядках картки ліквідності.
//
// Доти застосунок вважав безвідкличним КОЖЕН строковий вклад, і це було
// чесно: розрізнити їх було нічим, а за ЦКУ саме це й замовчування. Тепер
// прапорець є, і зсипати обидва в одне число означало б казати «цього не
// дістати» про гроші, які дістати можна, заплативши відсотками.
func TestLiquiditySplitsLockedFromBreakable(t *testing.T) {
	today := domain.NewDate(time.Now())
	far := today.AddMonths(9) // свідомо за межами вікна 90 днів
	in := riskInput{
		Now:   time.Now(),
		Rates: fx.Rates{money.UAH: 100},
		TermDeposits: []domain.Deposit{
			{Bank: "a", Currency: money.UAH, Principal: 100_000_00, OpenDate: today,
				MaturityDate: far, Payout: domain.PayoutEnd},
			{Bank: "b", Currency: money.UAH, Principal: 300_000_00, OpenDate: today,
				MaturityDate: far, Payout: domain.PayoutEnd, Revocable: true},
		},
	}
	l := buildRisk(in).Liquidity
	if l == nil {
		t.Fatal("картки ліквідності немає")
	}
	if l.LockedUAH != 100_000 {
		t.Errorf("замкнено %.2f ₴, очікували 100 000 — це безвідкличний вклад", l.LockedUAH)
	}
	if l.BreakableUAH != 300_000 {
		t.Errorf("зламне %.2f ₴, очікували 300 000 — договір дозволяє забрати достроково",
			l.BreakableUAH)
	}
	// Ні те, ні те не є вільними грошима: додати зламне в «зараз» означало
	// б зробити подушку купівельною спроможністю.
	if l.NowUAH != 0 {
		t.Errorf("«зараз» %.2f ₴ — вклади не є готівкою, хай би якими розривними були", l.NowUAH)
	}
}

// TestLiquidityAvailableNowCountsReserveAndGoals — подушка й цілі входять
// у «під рукою» і у вікна, але НЕ в now_uah.
//
// Доти головним числом картки стояв самий рахунок брокера, і на портфелі,
// де подушка лежить готівкою, вона казала «доступно 9,87 ₴» людині з
// десятьма тисячами в сейфі. Питання картки — коли гроші стають
// ДОСТУПНІ; подушку й ціль діставати нізвідки не треба.
//
// Друга половина тесту стереже інваріант, через який це не можна було
// зробити доливанням у now_uah: на рівності now_uah == account_uah
// тримається звірка звіту про рух коштів.
func TestLiquidityAvailableNowCountsReserveAndGoals(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	d := func(off int) domain.Date { return domain.NewDate(now.AddDate(0, 0, off)) }

	out := buildRisk(riskInput{
		Rates:        fx.Rates{},
		AccountMinor: 1_000_00,
		ReserveUAH:   10_000,
		GoalsUAH:     3_000,
		Cashflow: []domain.CashflowItem{
			{Date: d(10), ISIN: "UA1", Type: domain.PayCoupon, Amount: money.New(100_00, money.UAH)},
		},
		Now: now, Today: today,
	})
	l := out.Liquidity

	if l.NowUAH != 1000 {
		t.Errorf("«на рахунках» %.2f, очікували 1000: подушка й цілі сюди не входять — "+
			"на now_uah == account_uah стоїть звірка звіту про рух коштів", l.NowUAH)
	}
	if l.AvailableNowUAH != 14000 {
		t.Errorf("«під рукою» %.2f, очікували 14000 = рахунок 1000 + подушка 10000 + цілі 3000",
			l.AvailableNowUAH)
	}
	if l.In30UAH != 14100 || l.In90UAH != 14100 {
		t.Errorf("вікна %.2f / %.2f, очікували 14100: вони рахуються від «під рукою», "+
			"а не від рахунку", l.In30UAH, l.In90UAH)
	}
	if l.ReserveUAH != 10000 || l.GoalsUAH != 3000 {
		t.Errorf("доданки мають лишитись видимими окремо: подушка %.2f, цілі %.2f",
			l.ReserveUAH, l.GoalsUAH)
	}
}

// TestLiquidityMarksReserveRungs — резервний вклад видно окремо всередині
// замкненого.
//
// Він лишається в спільному числі (для питання «коли звільниться» вклад є
// вклад), але без підпису картка мовчала про те, що частина замкненого —
// власна подушка, яка ще не дозріла, а не портфель.
func TestLiquidityMarksReserveRungs(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	today := domain.NewDate(now)
	d := func(off int) domain.Date { return domain.NewDate(now.AddDate(0, 0, off)) }

	deps := []domain.Deposit{
		{Bank: "ПУМБ", Currency: money.UAH, Principal: 7_000_00,
			OpenDate: d(-100), MaturityDate: d(400), Payout: domain.PayoutEnd},
		{Bank: "mono", Currency: money.UAH, Principal: 5_000_00, IsReserve: true,
			OpenDate: d(-100), MaturityDate: d(400), Payout: domain.PayoutEnd},
		{Bank: "sense", Currency: money.UAH, Principal: 2_000_00, IsReserve: true, Revocable: true,
			OpenDate: d(-100), MaturityDate: d(400), Payout: domain.PayoutEnd},
	}
	l := buildRisk(riskInput{
		TermDeposits: deps, Rates: fx.Rates{}, Now: now, Today: today,
	}).Liquidity

	if l.LockedUAH != 12000 {
		t.Errorf("замкнено %.2f, очікували 12000: резервна рунга лишається в спільному числі",
			l.LockedUAH)
	}
	if l.LockedReserveUAH != 5000 {
		t.Errorf("з них подушка %.2f, очікували 5000", l.LockedReserveUAH)
	}
	if l.BreakableUAH != 2000 || l.BreakableReserveUAH != 2000 {
		t.Errorf("зламне %.2f, з них подушка %.2f — очікували 2000 і 2000",
			l.BreakableUAH, l.BreakableReserveUAH)
	}
	if l.AvailableNowUAH != 0 {
		t.Errorf("«під рукою» %.2f: тіло вкладу, хай і резервного, у руках не лежить",
			l.AvailableNowUAH)
	}
}
