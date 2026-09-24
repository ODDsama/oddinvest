package engine

import (
	"math"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/store"
)

// TestSavingsRateIsCheckableByDivision — норма заощаджень мусить точно
// відтворюватись діленням двох чисел, які стоять поруч у тому самому
// документі.
//
// Головна властивість цієї плитки: вона нічого не додає до знання, крім
// частки, — обидві половини вже опубліковані. Якщо колись знаменник
// поїде на інше число (скажімо, на нетто замість валового), інваріант
// упаде тут, а не на екрані.
func TestSavingsRateIsCheckableByDivision(t *testing.T) {
	cases := []struct {
		name    string
		actual  float64
		gross   float64
		wantPct float64
	}{
		{"звичайний місяць", 12_000, 60_000, 20},
		// Понад сотню — законний стан, а не помилка: відкласти більше, ніж
		// заробив цього місяця, можна (продав щось, дістав із-під матраца,
		// прийшов бонус повз план). Стелі тут немає навмисно.
		{"відклав більше, ніж заробив", 76_790.80, 47_500, 161.66},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := savingsRatePct(c.actual, c.gross)
			if math.Abs(got-c.wantPct) > 0.01 {
				t.Errorf("норма %.2f%%, чекали %.2f%%", got, c.wantPct)
			}
			// Допуск — рівно ціна показу. Норма округлена до сотих
			// відсотка, тож зворотне множення повертає темп із похибкою до
			// половини останнього знака: 0.005% від доходу. На 47 500 ₴ це
			// 2.4 ₴, і вимагати тут копійки означало б вимагати від
			// відсотка більше знаків, ніж людині варто показувати.
			tol := c.gross*0.005/100 + 0.01
			if back := got / 100 * c.gross; math.Abs(back-c.actual) > tol {
				t.Errorf("норма × дохід = %.2f, а темп %.2f (допуск %.2f) — "+
					"число не відтворюється діленням", back, c.actual, tol)
			}
		})
	}
}

// TestSavingsRateStaysSilentWithoutIncome — без плану доходу числа немає.
//
// Нуль тут читався б як «нічого не відкладаю», а це зовсім інше
// твердження: ділити просто нема на що. Той самий поділ між «невідомо» й
// «нуль», на якому стоїть половина полів цього документа.
func TestSavingsRateStaysSilentWithoutIncome(t *testing.T) {
	if got := savingsRatePct(12_000, 0); got != 0 {
		t.Errorf("без плану доходу норма %.2f, мала мовчати", got)
	}
	if got := savingsRatePct(12_000, 0); got != 0 {
		t.Errorf("при нульовому доході норма %.2f, мала мовчати", got)
	}
	// І навпаки: без темпу теж нема чого казати.
	if got := savingsRatePct(0, 60_000); got != 0 {
		t.Errorf("без темпу норма %.2f, мала мовчати", got)
	}
}

// TestNiceStepReadsAsAnAction — крок внеску округлений до числа, яким
// людина справді мислить рішення.
//
// «Відкладай на 987.43 ₴ більше» формально точніше, але такого рішення
// ніхто не ухвалює. Точність тут і не втрачається: крок узятий з голови
// (десята від темпу), і вдавати, що в ньому значущі копійки, було б
// гірше за округлення.
func TestNiceStepReadsAsAnAction(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{0, 0},
		{99, 0},     // нижче сотні крок нічого не зрушить — рядка не буде
		{120, 100},  // 1.2 × 100 -> 100
		{487, 500},  // 4.87 × 100 -> 500
		{987, 1000}, // 9.87 × 100 -> 1000
		{1_200, 1_000},
		{4_870, 5_000},
		{76_790, 100_000},
	}
	for _, c := range cases {
		if got := niceStep(c.in); got != c.want {
			t.Errorf("niceStep(%.0f) = %.0f, чекали %.0f", c.in, got, c.want)
		}
	}
}

// Знаменник норми — у ТОМУ Ж вікні, що й чисельник: середній валовий дохід
// за ActualMonths місяців, а не дохід поточного. Доти премія цього місяця
// (або її відсутність) хитала норму так, ніби змінилась дисципліна.
func TestSavingsBaseAveragesSameWindow(t *testing.T) {
	today := domain.Date("2026-09-24")
	src := &sources{
		planFlows: []store.PlanFlow{
			{ID: 1, Name: "зарплата", Kind: "income", Amount: 6_000_000, Currency: "UAH",
				Cadence: "month", FromDate: "2025-01-10", InvestBP: 10000},
			{ID: 2, Name: "премія", Kind: "income", Amount: 6_000_000, Currency: "UAH",
				Cadence: "once", FromDate: "2026-09-05", InvestBP: 10000},
		},
		deposits: []store.Deposit{
			{Date: "2026-07-16", Amount: 3_000_000, Currency: "UAH"},
			{Date: "2026-08-15", Amount: 3_000_000, Currency: "UAH"},
			{Date: "2026-09-14", Amount: 3_000_000, Currency: "UAH"},
		},
	}
	mth, err := buildMonth(src, domain.Holdings{}, fx.Rates{}, today.Time(), today, 0)
	if err != nil {
		t.Fatal(err)
	}
	if mth.ActualMonths != 3 {
		t.Fatalf("вікно %d місяців, фікстура розрахована на 3", mth.ActualMonths)
	}
	// Вересень 120 000 (зарплата + премія), серпень і липень по 60 000.
	if math.Abs(mth.GrossAvgUAH-80_000) > 0.01 {
		t.Errorf("середній валовий %.2f, чекали 80 000 — (120 000 + 60 000 + 60 000) / 3", mth.GrossAvgUAH)
	}
}
