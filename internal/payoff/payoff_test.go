package payoff

import (
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
)

// payoffFixture — дорога велика розстрочка й дешева маленька. Саме на такій
// парі лавина й сніжок розходяться: сніжок спершу закриває дрібну, бо вона
// дрібна, і весь цей час платить комісію дорогій.
//
// Обидві — з договором, який комісії при достроковому СКАСОВУЄ. Без цього
// прапорця жодна стратегія не розійшлася б із мінімалками, бо дострокові
// гроші не доходили б нікуди (payoff.go), і три тести нижче міряли б
// порожнечу замість арифметики.
func payoffFixture() []Debt {
	return []Debt{
		{ID: 1, Name: "Дорога", Kind: domain.DebtInstallment, Rate: 49.8,
			Left: 30_000_00, perMonth: 3_333_33, feeMonth: 597_00,
			Prepayable: true, PrepayBasis: domain.DebtPrepayCancel},
		{ID: 2, Name: "Дешева", Kind: domain.DebtInstallment, Rate: 8.0,
			Left: 6_000_00, perMonth: 1_000_00, feeMonth: 30_00,
			Prepayable: true, PrepayBasis: domain.DebtPrepayCancel},
	}
}

// Лавина платить менше за сніжок — це арифметика, а не думка, і саме тому
// вона замовчування.
func TestPayoffAvalancheBeatsSnowballInMoney(t *testing.T) {
	debts := payoffFixture()
	const extra = 2_000_00

	av := Simulate(debts, Avalanche, extra)
	sn := Simulate(debts, Snowball, extra)
	min := Simulate(debts, Minimum, 0)

	if av.Cost >= sn.Cost {
		t.Errorf("лавина віддала банку %d, сніжок %d — лавина мусить платити менше",
			av.Cost, sn.Cost)
	}
	// Але сніжок закриває ПЕРШИЙ борг раніше — інакше його не було б за що
	// обирати, і показувати його як варіант було б нечесно.
	if sn.CloseAt[2] > av.CloseAt[2] {
		t.Errorf("сніжок закрив дрібний борг на місяці %d, лавина на %d",
			sn.CloseAt[2], av.CloseAt[2])
	}
	// «Лише мінімалки» — найдовше й найдорожче. Це лінійка, проти якої
	// міряються обидві стратегії.
	if min.Months <= av.Months || min.Cost <= av.Cost {
		t.Errorf("мінімалки: %d міс / %d ₴ проти лавини %d міс / %d ₴",
			min.Months, min.Cost, av.Months, av.Cost)
	}
}

// Прохід мусить закрити КОЖЕН борг і віддати рівно тіло плюс комісії.
func TestPayoffScheduleClosesEveryDebt(t *testing.T) {
	debts := payoffFixture()
	for _, strategy := range []string{Avalanche, Snowball, Minimum} {
		run := Simulate(debts, strategy, 2_000_00)
		if run.Unfunded {
			t.Fatalf("%s: борг не гаситься взагалі", strategy)
		}
		for _, d := range debts {
			if _, ok := run.CloseAt[d.ID]; !ok {
				t.Errorf("%s: борг %q не закрився за %d місяців", strategy, d.Name, run.Months)
			}
		}
		// Сплачене = тіло + те, що лишилось банку. Тотожність, без якої
		// підсумок не звести ні з чим.
		var body int64
		for _, d := range debts {
			body += d.Left
		}
		if run.Paid != body+run.Cost {
			t.Errorf("%s: сплачено %d ≠ тіло %d + ціна %d",
				strategy, run.Paid, body, run.Cost)
		}
	}
}

// Дострокове погашення розстрочки економить саме КОМІСІЇ майбутніх
// місяців — у цьому вся суть черги.
func TestPayoffExtraSavesFutureFees(t *testing.T) {
	debts := payoffFixture()
	none := Simulate(debts, Avalanche, 0)
	some := Simulate(debts, Avalanche, 5_000_00)
	if some.Cost >= none.Cost || some.Months >= none.Months {
		t.Errorf("додаткові 5 000/міс не дали нічого: %d міс / %d ₴ проти %d / %d",
			some.Months, some.Cost, none.Months, none.Cost)
	}
}

// Пільгова карусель у чергу погашення НЕ входить: оборот, який закривають
// вчасно, нічого не коштує, а гроші місяця вже описані витратами.
// У черзі опиняється лише готівка з ліміту — вона під відсотком з першого
// дня.
func TestPayoffGraceCarouselStaysOutOfQueue(t *testing.T) {
	card := domain.Debt{
		ID: 1, Kind: domain.DebtCard, Currency: "UAH", StatementDay: 30,
		LimitAmount: 200_000_00, APRBp: 4788, MinPaymentBp: 300,
	}
	today := domain.Date("2026-09-10")

	// Звичайний місяць: борг є, але весь він у пільговому.
	marks := []domain.DebtMark{{DebtID: 1, Date: "2026-09-01",
		Balance: -18_400_00, StatementDue: 18_400_00}}
	got := BuildDebts([]domain.Debt{card}, marks, nil, nil, today)
	if len(got) != 0 {
		t.Errorf("пільговий оборот потрапив у чергу: %+v", got)
	}

	// А готівка — потрапляє, і саме своєю сумою.
	marks[0].NonGrace = 5_000_00
	got = BuildDebts([]domain.Debt{card}, marks, nil, nil, today)
	if len(got) != 1 || got[0].Left != 5_000_00 {
		t.Fatalf("готівка з ліміту не стала боргом черги: %+v", got)
	}
	if got[0].RateBasis != domain.DebtRateCompound {
		t.Errorf("основа ставки картки %q", got[0].RateBasis)
	}
}

// Ціна ДВОХ помилок із карткою рахується окремо, бо помилки різні.
func TestPayoffGraceCostSplitsTwoMistakes(t *testing.T) {
	card := domain.Debt{
		ID: 1, Kind: domain.DebtCard, Currency: "UAH", StatementDay: 30,
		APRBp: 4788, APROverdueBp: 6200, MinPaymentBp: 300, LateFee: 100_00,
	}
	st := domain.CardState(card,
		[]domain.DebtMark{{DebtID: 1, Date: "2026-09-01",
			Balance: -20_000_00, StatementDue: 18_400_00}}, nil, nil, "2026-09-10")

	missFull, missMin := GraceCost(card, st)
	if missFull <= 0 || missMin <= 0 {
		t.Fatalf("ціни помилок: %d / %d", missFull, missMin)
	}
	// Без підвищеної ставки й штрафу друге число НЕ вигадується. Доти
	// підставлялась звичайна ставка, і два різні за ціною ризики виходили
	// однаковими — на екрані власника обидва показали 207,96 ₴.
	bare := card
	bare.APROverdueBp, bare.LateFee = 0, 0
	if full, min := GraceCost(bare, st); full <= 0 || min != 0 {
		t.Errorf("без підвищеної ставки: %d / %d, чекали друге число нулем", full, min)
	}
	// Пропустити мінімалку дорожче: підвищена ставка йде на ВЕСЬ борг, та
	// ще й штраф зверху.
	if missMin <= missFull {
		t.Errorf("пропустити мінімалку (%d) мусить коштувати більше, ніж не закрити виписку (%d)",
			missMin, missFull)
	}
}

// Розстрочка, у якої банк бере комісії за ВЕСЬ строк, дострокових грошей
// не отримує: віддати ту саму суму раніше — не економія, а програш у часі.
//
// Тест міряє саме це: скільки б не кидати понад обовʼязкове, і місяці, і
// віддане банку лишаються тими самими. Доти прохід «економив» на такому
// боргу комісії, яких банк не скасовує, і сторінка обіцяла числа, яких у
// житті власника не існує.
func TestPayoffStickyFeeIgnoresExtra(t *testing.T) {
	today := domain.Date("2026-09-10")
	base := domain.Debt{ID: 1, Name: "mono готівка", Kind: domain.DebtInstallment,
		Currency: "UAH", Principal: 30_000_00, PaymentsTotal: 9,
		FirstPaymentDate: "2026-09-30", FeeMonthBp: 199}

	for _, c := range []struct {
		what string
		with func(domain.Debt) domain.Debt
	}{
		{"банк бере комісії за всі місяці", func(d domain.Debt) domain.Debt {
			d.FeeOnPrepay = domain.DebtFeeKeep
			return d
		}},
		{"договір не звірений", func(d domain.Debt) domain.Debt { return d }},
		{"комісії немає взагалі", func(d domain.Debt) domain.Debt {
			d.FeeMonthBp, d.FeeOnPrepay = 0, domain.DebtFeeCancel
			return d
		}},
	} {
		debts := BuildDebts([]domain.Debt{c.with(base)}, nil, nil, fx.Rates{}, today)

		zero := Simulate(debts, Avalanche, 0)
		much := Simulate(debts, Avalanche, 10_000_00)
		if zero.Months != much.Months || zero.Cost != much.Cost {
			t.Errorf("%s: 10 000/міс змінили план — %d міс / %d ₴ проти %d / %d",
				c.what, much.Months, much.Cost, zero.Months, zero.Cost)
		}
		// І три стратегії збігаються за побудовою: розподіляти нічого.
		sn := Simulate(debts, Snowball, 10_000_00)
		min := Simulate(debts, Minimum, 10_000_00)
		if sn.Cost != zero.Cost || min.Cost != zero.Cost {
			t.Errorf("%s: стратегії розійшлися — %d / %d / %d",
				c.what, zero.Cost, sn.Cost, min.Cost)
		}
	}
}

// Картка дострокові гроші отримує ЗАВЖДИ: відсоток нараховують на залишок,
// тож будь-який платіж зменшує наступне нарахування. Це друга половина
// того самого правила, і без неї перша читалась би як «борг чіпати не
// можна взагалі».
func TestPayoffCardTakesExtra(t *testing.T) {
	card := Debt{ID: 1, Name: "ПУМБ", Kind: domain.DebtCard,
		Left: 50_000_00, monthlyRate: 0.0399, minBp: 300, minFloor: 100_00,
		Prepayable: true, PrepayBasis: domain.DebtPrepayCard}

	slow := Simulate([]Debt{card}, Avalanche, 0)
	fast := Simulate([]Debt{card}, Avalanche, 10_000_00)
	if fast.Months >= slow.Months || fast.Cost >= slow.Cost {
		t.Errorf("картка не відреагувала на дострокові: %d міс / %d ₴ проти %d / %d",
			fast.Months, fast.Cost, slow.Months, slow.Cost)
	}
}

// Правило договору читається з самого боргу, а не проставляється рукою в
// проході: BuildDebts мусить донести його з domain до черги.
func TestBuildPayoffDebtsCarriesPrepayBasis(t *testing.T) {
	today := domain.Date("2026-09-10")
	keep := domain.Debt{ID: 1, Name: "mono", Kind: domain.DebtInstallment,
		Currency: "UAH", Principal: 30_000_00, PaymentsTotal: 9,
		FirstPaymentDate: "2026-09-30", FeeMonthBp: 199,
		FeeOnPrepay: domain.DebtFeeKeep}
	cancel := keep
	cancel.ID, cancel.FeeOnPrepay = 2, domain.DebtFeeCancel

	got := BuildDebts([]domain.Debt{keep, cancel}, nil, nil, fx.Rates{}, today)
	if len(got) != 2 {
		t.Fatalf("боргів у черзі %d, чекали 2", len(got))
	}
	for _, d := range got {
		want := d.ID == 2
		if d.Prepayable != want {
			t.Errorf("борг %d: prepayable=%v, чекали %v", d.ID, d.Prepayable, want)
		}
	}
	// І черга ставить придатний ПЕРШИМ, хай би яким був порядок у базі:
	// список на екрані малюється цим самим порядком.
	if order := Order(got, Avalanche); got[order[0]].ID != 2 {
		t.Errorf("першим у черзі борг %d, а не той, що приймає дострокові",
			got[order[0]].ID)
	}
}
