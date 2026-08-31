package api

import (
	"encoding/json"
	"net/http"
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
func payoffFixture() []payoffDebt {
	return []payoffDebt{
		{ID: 1, Name: "Дорога", Kind: domain.DebtInstallment, Rate: 49.8,
			Left: 30_000_00, perMonth: 3_333_33, feeMonth: 597_00,
			prepayable: true, prepayBasis: domain.DebtPrepayCancel},
		{ID: 2, Name: "Дешева", Kind: domain.DebtInstallment, Rate: 8.0,
			Left: 6_000_00, perMonth: 1_000_00, feeMonth: 30_00,
			prepayable: true, prepayBasis: domain.DebtPrepayCancel},
	}
}

// Лавина платить менше за сніжок — це арифметика, а не думка, і саме тому
// вона замовчування.
func TestPayoffAvalancheBeatsSnowballInMoney(t *testing.T) {
	debts := payoffFixture()
	const extra = 2_000_00

	av := runPayoff(debts, payoffAvalanche, extra)
	sn := runPayoff(debts, payoffSnowball, extra)
	min := runPayoff(debts, payoffMinimum, 0)

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
	for _, strategy := range []string{payoffAvalanche, payoffSnowball, payoffMinimum} {
		run := runPayoff(debts, strategy, 2_000_00)
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
	none := runPayoff(debts, payoffAvalanche, 0)
	some := runPayoff(debts, payoffAvalanche, 5_000_00)
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
	got := buildPayoffDebts([]domain.Debt{card}, marks, nil, nil, today)
	if len(got) != 0 {
		t.Errorf("пільговий оборот потрапив у чергу: %+v", got)
	}

	// А готівка — потрапляє, і саме своєю сумою.
	marks[0].NonGrace = 5_000_00
	got = buildPayoffDebts([]domain.Debt{card}, marks, nil, nil, today)
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

	missFull, missMin := payoffGraceCost(card, st)
	if missFull <= 0 || missMin <= 0 {
		t.Fatalf("ціни помилок: %d / %d", missFull, missMin)
	}
	// Без підвищеної ставки й штрафу друге число НЕ вигадується. Доти
	// підставлялась звичайна ставка, і два різні за ціною ризики виходили
	// однаковими — на екрані власника обидва показали 207,96 ₴.
	bare := card
	bare.APROverdueBp, bare.LateFee = 0, 0
	if full, min := payoffGraceCost(bare, st); full <= 0 || min != 0 {
		t.Errorf("без підвищеної ставки: %d / %d, чекали друге число нулем", full, min)
	}
	// Пропустити мінімалку дорожче: підвищена ставка йде на ВЕСЬ борг, та
	// ще й штраф зверху.
	if missMin <= missFull {
		t.Errorf("пропустити мінімалку (%d) мусить коштувати більше, ніж не закрити виписку (%d)",
			missMin, missFull)
	}
}

// Наскрізь через HTTP: три стратегії, чутливість і пільговий блок.
func TestPayoffEndpoint(t *testing.T) {
	srv, _ := testServer(t)
	card := addDebt(t, srv.URL, `{"name":"ПУМБ","kind":"card","currency":"UAH",
		"limit":"200000","statement_day":"30","apr_pct":"47.88","apr_overdue_pct":"62",
		"min_payment_pct":"3","late_fee":"100"}`)
	addDebt(t, srv.URL, `{"name":"Холодильник","kind":"installment","currency":"UAH",
		"card_id":"`+did(card)+`","principal":"30000","payments_total":"9",
		"first_payment_date":"2026-09-30","fee_month_pct":"1.99"}`)
	if resp, out := do(t, "POST", srv.URL+"/api/debt-marks",
		`{"debt_id":"`+did(card)+`","balance":"5000","statement_due":"12000","non_grace":"4000"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("звірка: %d %s", resp.StatusCode, out)
	}

	resp, out := do(t, "GET", srv.URL+"/api/payoff?extra=3000", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/payoff: %d %s", resp.StatusCode, out)
	}
	var got payoffResp
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if got.Strategy != payoffAvalanche {
		t.Errorf("замовчування %q, чекали лавину", got.Strategy)
	}
	if len(got.Debts) != 2 {
		t.Fatalf("боргів у черзі %d, чекали 2 (розстрочка + готівка картки): %s",
			len(got.Debts), out)
	}
	// Порядок рядків — це черга погашення: першою стоїть найдорожча.
	// Готівка з ліміту під 60% обходить розстрочку під ~50%.
	if got.Debts[0].Rate <= got.Debts[1].Rate {
		t.Errorf("черга не за ставкою: %.2f%% перед %.2f%%",
			got.Debts[0].Rate, got.Debts[1].Rate)
	}
	var inst payoffDebtJSON
	for _, d := range got.Debts {
		if d.Kind == domain.DebtInstallment {
			inst = d
		}
	}
	// Розстрочка з комісією 1,99% коштує ~50% річних, а не 23,88% — це
	// головна знахідка всієї фази.
	if inst.Rate < 45 || inst.Rate > 55 {
		t.Errorf("ставка розстрочки %.2f%%, чекали ~50%%: %+v", inst.Rate, got.Debts)
	}
	if inst.Basis != domain.DebtRateFromSchedule {
		t.Errorf("основа %q, чекали виведену з графіка", inst.Basis)
	}
	// Реальна ставка мусить бути НИЖЧОЮ за номінальну рівно на знецінення.
	if inst.RealPct >= inst.Rate {
		t.Errorf("реальна %.2f не менша за номінальну %.2f", inst.RealPct, inst.Rate)
	}
	if len(got.Compare) != 3 {
		t.Errorf("порівняння стратегій: %d рядків", len(got.Compare))
	}
	if got.Plan.FreeDate == "" || got.Plan.Months == 0 {
		t.Errorf("дати свободи немає: %+v", got.Plan)
	}
	if len(got.Sensitivity) == 0 {
		t.Error("чутливості немає — саме вона відповідає «а якщо ще тисяча»")
	}
	if len(got.Grace) != 1 {
		t.Fatalf("пільгового блоку немає: %s", out)
	}
	g := got.Grace[0]
	if !g.Known || g.DueDate == "" {
		t.Errorf("пільговий блок без дати або без звірки: %+v", g)
	}
	// «Вільно» = 5 000 − 12 000 − частина розстрочки: відʼємне, і саме це
	// і є та пастка, через яку безкоштовний оборот стає боргом.
	if g.Free.Amount[0] != '-' {
		t.Errorf("вільно %s — чекали відʼємне при боргу більшому за баланс", g.Free.Amount)
	}
	if g.MissMinCost.Amount == g.MissFullCost.Amount {
		t.Error("ціни двох помилок злилися")
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
		debts := buildPayoffDebts([]domain.Debt{c.with(base)}, nil, nil, fx.Rates{}, today)

		zero := runPayoff(debts, payoffAvalanche, 0)
		much := runPayoff(debts, payoffAvalanche, 10_000_00)
		if zero.Months != much.Months || zero.Cost != much.Cost {
			t.Errorf("%s: 10 000/міс змінили план — %d міс / %d ₴ проти %d / %d",
				c.what, much.Months, much.Cost, zero.Months, zero.Cost)
		}
		// І три стратегії збігаються за побудовою: розподіляти нічого.
		sn := runPayoff(debts, payoffSnowball, 10_000_00)
		min := runPayoff(debts, payoffMinimum, 10_000_00)
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
	card := payoffDebt{ID: 1, Name: "ПУМБ", Kind: domain.DebtCard,
		Left: 50_000_00, monthlyRate: 0.0399, minBp: 300, minFloor: 100_00,
		prepayable: true, prepayBasis: domain.DebtPrepayCard}

	slow := runPayoff([]payoffDebt{card}, payoffAvalanche, 0)
	fast := runPayoff([]payoffDebt{card}, payoffAvalanche, 10_000_00)
	if fast.Months >= slow.Months || fast.Cost >= slow.Cost {
		t.Errorf("картка не відреагувала на дострокові: %d міс / %d ₴ проти %d / %d",
			fast.Months, fast.Cost, slow.Months, slow.Cost)
	}
}

// Правило договору читається з самого боргу, а не проставляється рукою в
// проході: buildPayoffDebts мусить донести його з domain до черги.
func TestBuildPayoffDebtsCarriesPrepayBasis(t *testing.T) {
	today := domain.Date("2026-09-10")
	keep := domain.Debt{ID: 1, Name: "mono", Kind: domain.DebtInstallment,
		Currency: "UAH", Principal: 30_000_00, PaymentsTotal: 9,
		FirstPaymentDate: "2026-09-30", FeeMonthBp: 199,
		FeeOnPrepay: domain.DebtFeeKeep}
	cancel := keep
	cancel.ID, cancel.FeeOnPrepay = 2, domain.DebtFeeCancel

	got := buildPayoffDebts([]domain.Debt{keep, cancel}, nil, nil, fx.Rates{}, today)
	if len(got) != 2 {
		t.Fatalf("боргів у черзі %d, чекали 2", len(got))
	}
	for _, d := range got {
		want := d.ID == 2
		if d.prepayable != want {
			t.Errorf("борг %d: prepayable=%v, чекали %v", d.ID, d.prepayable, want)
		}
	}
	// І черга ставить придатний ПЕРШИМ, хай би яким був порядок у базі:
	// список на екрані малюється цим самим порядком.
	if order := payoffOrder(got, payoffAvalanche); got[order[0]].ID != 2 {
		t.Errorf("першим у черзі борг %d, а не той, що приймає дострокові",
			got[order[0]].ID)
	}
}
