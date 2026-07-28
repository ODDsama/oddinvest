package domain

import "testing"

// Фонд платить кожного 10 числа за попередній місяць; якщо 10-те випало на
// вихідний — наступного робочого дня.
func TestNextPayoutDateSkipsWeekend(t *testing.T) {
	cases := []struct {
		name string
		from Date
		want Date
	}{
		// 10 серпня 2026 — понеділок, робочий: без зсуву.
		{"будній день", "2026-08-01", "2026-08-10"},
		// Сам день виплати ще попереду сьогодні — це і є найближча дата.
		{"сьогодні день виплати", "2026-08-10", "2026-08-10"},
		// Минуло — беремо наступний місяць. 10 вересня 2026 — четвер.
		{"після виплати", "2026-08-11", "2026-09-10"},
		// 10 січня 2026 — субота, тож платять у понеділок 12-го.
		{"субота -> понеділок", "2026-01-05", "2026-01-12"},
		// 10 травня 2026 — неділя, тож платять у понеділок 11-го.
		{"неділя -> понеділок", "2026-05-01", "2026-05-11"},
		// Через межу року: після грудневої виплати наступна в січні.
		{"через новий рік", "2026-12-11", "2027-01-11"}, // 10.01.2027 — неділя
	}
	for _, c := range cases {
		got, ok := NextPayoutDate(10, c.from)
		if !ok {
			t.Errorf("%s: дата не порахувалась", c.name)
			continue
		}
		if got != c.want {
			t.Errorf("%s: від %s чекали %s, маємо %s", c.name, c.from, c.want, got)
		}
	}
}

func TestFundDividendFlows(t *testing.T) {
	// 1000 сертифікатів по 11.00 ₴ = 11 000 ₴; 9.6% річних -> 88 ₴ на місяць.
	p := &FundPosition{Fund: "Inzhur", Currency: "UAH", Qty: 1000,
		LastPrice: 110000, PayoutDay: 10}
	flows := FundDividendFlows(p, 9.6, 3, "2026-08-01")
	if len(flows) != 3 {
		t.Fatalf("очікували 3 виплати, маємо %d", len(flows))
	}
	// Дати йдуть підряд і не повторюються.
	want := []Date{"2026-08-10", "2026-09-10", "2026-10-12"} // 10.10.2026 — субота
	for i, f := range flows {
		if f.Date != want[i] {
			t.Errorf("виплата %d: чекали %s, маємо %s", i, want[i], f.Date)
		}
		if f.Amount.Amount() != 8800 {
			t.Errorf("виплата %d: сума %d, чекали 8800", i, f.Amount.Amount())
		}
		if !IsFundISIN(f.ISIN) {
			t.Errorf("потік фонду має бути впізнаваним: %q", f.ISIN)
		}
	}
}

func TestFundDividendFlowsSilentWithoutData(t *testing.T) {
	full := &FundPosition{Fund: "F", Currency: "UAH", Qty: 100, LastPrice: 110000, PayoutDay: 10}
	cases := map[string]*FundPosition{
		"без дня виплати":  {Fund: "F", Currency: "UAH", Qty: 100, LastPrice: 110000},
		"без сертифікатів": {Fund: "F", Currency: "UAH", PayoutDay: 10},
	}
	for name, p := range cases {
		if got := FundDividendFlows(p, 9.5, 6, "2026-08-01"); got != nil {
			t.Errorf("%s: мало бути порожньо, маємо %d", name, len(got))
		}
	}
	if got := FundDividendFlows(full, 0, 6, "2026-08-01"); got != nil {
		t.Errorf("без дохідності мало бути порожньо, маємо %d", len(got))
	}
	if got := FundDividendFlows(nil, 9.5, 6, "2026-08-01"); got != nil {
		t.Error("nil-позиція мала дати порожньо")
	}
}

func TestNextPayoutDateRejectsNonsense(t *testing.T) {
	for _, day := range []int{0, -1, 32} {
		if _, ok := NextPayoutDate(day, "2026-08-01"); ok {
			t.Errorf("день %d мав бути відхилений", day)
		}
	}
	if _, ok := NextPayoutDate(10, ""); ok {
		t.Error("порожня дата мала бути відхилена")
	}
}

// Заданий день виплати дає точний місячний ритм, тож ануалізація не
// покладається ні на медіану проміжків, ні на припущення «місяць = 30».
func TestPayoutDaySetsMonthlyRhythm(t *testing.T) {
	ops := []FundOp{
		{Date: "2026-06-04", Fund: "F", Kind: FundBuy, Qty: 1000, Amount: 1000000, Currency: "UAH"},
		{Date: "2026-07-10", Fund: "F", Kind: FundDividend, Amount: 1899, Tax: 266, Currency: "UAH"},
	}
	p := FundPositions(ops)["F"]
	guessed, ok := DividendYieldNet(ops, p, "2026-07-28")
	if !ok {
		t.Fatal("без дня виплати дохідність мала порахуватись")
	}
	p.PayoutDay = 10
	exact, ok := DividendYieldNet(ops, p, "2026-07-28")
	if !ok {
		t.Fatal("з днем виплати дохідність мала порахуватись")
	}
	// 365/12 = 30.42 довше за 30, тож річна сума трохи менша.
	if !(exact < guessed) {
		t.Errorf("точний ритм мав дати менше за припущені 30 днів: %.2f vs %.2f", exact, guessed)
	}
	if d := guessed - exact; d > 0.1 {
		t.Errorf("різниця завелика для самої лише зміни ритму: %.2f", d)
	}
}
