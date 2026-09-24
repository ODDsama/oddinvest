package domain

import "testing"

// Часткова доплата розстрочки зменшує ТІЛО й коротшає строк.
//
// Доти залишок будувався з самого договору: доплата лягала лише в
// «сплачено понад обовʼязкове» цього місяця, а залишок, план місяця,
// рубіж подушки й черга погашення до повного закриття бачили весь
// графік. Рішення власника (2026-09-24): доплата з'їдає ОСТАННІ внески
// графіка (платіж той самий, кінець раніше), а щомісячна комісія від
// початкової суми йде, доки тіло не закрите повністю. Після закриття —
// за договором: скасовує (cancel) — комісій більше немає; не скасовує чи
// не з'ясовано (0049) — банк бере їх за всі місяці.
func TestInstallmentPrepayShortensTerm(t *testing.T) {
	d := Debt{ID: 7, Kind: DebtInstallment, Currency: "UAH", Principal: 30_000_00,
		PaymentsTotal: 12, FirstPaymentDate: "2026-01-15", FeeMonthBp: 199, FeeOnPrepay: DebtFeeCancel}
	ops := []DebtOp{
		{DebtID: 7, Date: "2026-01-15", Kind: DebtOpPayment, Amount: 3_097_00},
		{DebtID: 7, Date: "2026-02-15", Kind: DebtOpPayment, Amount: 3_097_00},
		{DebtID: 7, Date: "2026-03-01", Kind: DebtOpPayment, Amount: 10_000_00}, // доплата
		{DebtID: 7, Date: "2026-03-15", Kind: DebtOpPayment, Amount: 3_097_00},
		{DebtID: 8, Date: "2026-03-01", Kind: DebtOpPayment, Amount: 99_000_00}, // чужий борг
	}
	today := Date("2026-03-20")
	if got := InstallmentPrepaid(d, ops, today); got != 10_000_00 {
		t.Fatalf("доплата %d, чекали 10 000,00", got)
	}

	left := func(d Debt) (principal, fees int64, n int, last Date) {
		for _, p := range InstallmentSchedule(d) {
			if p.Date.Before(today) {
				continue
			}
			principal += p.Principal
			fees += p.Fee
			n++
			last = p.Date
		}
		return
	}
	d.Prepaid = InstallmentPrepaid(d, ops, today)
	principal, fees, n, last := left(d)
	if principal != 12_500_00 {
		t.Errorf("тіла лишилось %d, чекали 12 500,00 (22 500 − 10 000)", principal)
	}
	if n != 5 || last != "2026-08-15" {
		t.Errorf("платежів %d до %s, чекали 5 до 2026-08-15 — на чотири місяці раніше", n, last)
	}
	if fees != 5*597_00 {
		t.Errorf("комісій %d, чекали 5 × 597 — щомісяця від початкової суми, доки тіло живе", fees)
	}

	// Договір не скасовує комісій: закриті місяці лишаються платежами
	// самої комісії.
	d.FeeOnPrepay = DebtFeeKeep
	principal, fees, n, _ = left(d)
	if principal != 12_500_00 || n != 9 || fees != 9*597_00 {
		t.Errorf("keep: тіло %d, платежів %d, комісій %d; чекали 12 500, 9, 9 × 597", principal, n, fees)
	}

	// Ставка — договору, а не залишку: доплата не робить уже взяті гроші
	// дешевшими.
	c := d
	c.Prepaid = 0
	r1, _ := DebtEffectiveRate(c, 0)
	r2, _ := DebtEffectiveRate(d, 0)
	if r1 != r2 {
		t.Errorf("ставка залежить від доплати: %.4f проти %.4f", r1, r2)
	}
}
