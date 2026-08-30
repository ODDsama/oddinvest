package domain

import (
	"math"
	"testing"
)

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
	flows := FundDividendFlows(p, 9.6, 3, "2026-08-01", false)
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
		if got := FundDividendFlows(p, 9.5, 6, "2026-08-01", false); got != nil {
			t.Errorf("%s: мало бути порожньо, маємо %d", name, len(got))
		}
	}
	if got := FundDividendFlows(full, 0, 6, "2026-08-01", false); got != nil {
		t.Errorf("без дохідності мало бути порожньо, маємо %d", len(got))
	}
	if got := FundDividendFlows(nil, 9.5, 6, "2026-08-01", false); got != nil {
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
	p := FundPositions(ops, nil)["F"]
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

// Числа тут — живі, з Inzhur REIT станом на 30.08.2026: 779 сертифікатів
// по 11.1242 ₴, обіцянка 9.5% річних. Саме на них застосунок показував
// 68,60 ₴ вільних грошей там, де приходить 1,85 ₴, і щомісяця радив
// віднести різницю в пенсійний.
func TestReinvestBuysWholeCertificatesAndLeavesTheRest(t *testing.T) {
	got := SplitFundDividend(6860, 111242)
	want := FundReinvestSplit{Gross: 6860, Units: 6, Spent: 6675, Cash: 185}
	if got != want {
		t.Errorf("розкол ренти: маємо %+v, чекали %+v", got, want)
	}
}

func TestReinvestSplitDegenerates(t *testing.T) {
	cases := []struct {
		name           string
		gross, priceE4 int64
		want           FundReinvestSplit
	}{
		{"ціни не знаємо — усе грошима", 6860, 0,
			FundReinvestSplit{Gross: 6860, Cash: 6860}},
		{"не стало й на один папір", 500, 111242,
			FundReinvestSplit{Gross: 500, Cash: 500}},
		// Рівно три папери по 10 ₴: решти не лишається, і такий місяць
		// маршрут ноги не породжує взагалі.
		{"поділилось без остачі", 3000, 100000,
			FundReinvestSplit{Gross: 3000, Units: 3, Spent: 3000, Cash: 0}},
		{"виплати немає", 0, 111242, FundReinvestSplit{}},
	}
	for _, c := range cases {
		if got := SplitFundDividend(c.gross, c.priceE4); got != c.want {
			t.Errorf("%s: маємо %+v, чекали %+v", c.name, got, c.want)
		}
	}
}

// Головна властивість розколу: жодна копійка не зникає й не народжується.
// Саме на ній тримається право показувати брутто в календарі, а решту — у
// маршруті: це два поля одного числа, а не два різні числа.
func TestReinvestSplitLosesNoKopiyka(t *testing.T) {
	for _, price := range []int64{1, 99, 111242, 1011356, 5_000_000} {
		for gross := int64(0); gross < 40_000; gross += 137 {
			s := SplitFundDividend(gross, price)
			if s.Spent+s.Cash != s.Gross {
				t.Fatalf("ціна %d, рента %d: %d+%d != %d",
					price, gross, s.Spent, s.Cash, s.Gross)
			}
			if s.Cash < 0 || s.Spent < 0 || s.Units < 0 {
				t.Fatalf("ціна %d, рента %d: відʼємне в %+v", price, gross, s)
			}
		}
	}
}

// Позиція росте сама, тож дванадцятий місяць не дорівнює першому. Без
// цього оцінка спиралась би на кількість, про яку ми вже знаємо, що вона
// застаріла.
func TestReinvestCompoundsPositionEachMonth(t *testing.T) {
	p := &FundPosition{Fund: "Inzhur REIT", Currency: "UAH", Qty: 779,
		LastPrice: 111242, PayoutDay: 10}
	flows := FundDividendFlows(p, 9.5, 12, "2026-09-01", true)
	if len(flows) != 12 {
		t.Fatalf("чекали 12 виплат, маємо %d", len(flows))
	}
	first, last := flows[0].Split, flows[11].Split
	if first.Gross != 6860 {
		t.Errorf("перша рента %d, чекали 6860", first.Gross)
	}
	if last.Gross <= first.Gross {
		t.Errorf("рента мала вирости: перша %d, дванадцята %d", first.Gross, last.Gross)
	}
	// Готівкою йде саме решта, а не вся рента.
	if flows[0].Amount.Amount() != first.Cash || first.Cash != 185 {
		t.Errorf("готівка першого місяця %d (Cash %d), чекали 185",
			flows[0].Amount.Amount(), first.Cash)
	}
	// Рента росте рівно на докуплені папери: дванадцятий місяць рахується
	// від кількості, у якій уже сидять одинадцять попередніх докупівель.
	var before int64
	for _, f := range flows[:11] {
		before += f.Split.Units
	}
	wantLast := int64(math.Round(float64((779+before)*p.LastPrice/100) * 9.5 / 100 / 12))
	if last.Gross != wantLast {
		t.Errorf("дванадцята рента %d, чекали %d (779+%d паперів)",
			last.Gross, wantLast, before)
	}
}

// Регресія: без реінвесту нічого не змінилось — дванадцять однакових сум,
// як було до розколу.
func TestDividendFlowsWithoutReinvestUnchanged(t *testing.T) {
	p := &FundPosition{Fund: "F", Currency: "UAH", Qty: 1000,
		LastPrice: 110000, PayoutDay: 10}
	flows := FundDividendFlows(p, 9.6, 6, "2026-08-01", false)
	for i, f := range flows {
		if f.Amount.Amount() != 8800 {
			t.Errorf("виплата %d: %d, чекали 8800", i, f.Amount.Amount())
		}
		if f.Split.Units != 0 || f.Split.Cash != f.Split.Gross {
			t.Errorf("виплата %d: розкол мав бути порожній, маємо %+v", i, f.Split)
		}
	}
}
