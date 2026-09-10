package api

import (
	"math"
	"testing"

	"github.com/ODDsama/oddinvest/internal/state"
	money "github.com/Rhymond/go-money"
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
			got := savingsRatePct(c.actual, &state.MonthPlan{GrossUAH: state.Major(c.gross, money.UAH)})
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
	if got := savingsRatePct(12_000, nil); got != 0 {
		t.Errorf("без плану доходу норма %.2f, мала мовчати", got)
	}
	if got := savingsRatePct(12_000, &state.MonthPlan{GrossUAH: state.Major(0, money.UAH)}); got != 0 {
		t.Errorf("при нульовому доході норма %.2f, мала мовчати", got)
	}
	// І навпаки: без темпу теж нема чого казати.
	if got := savingsRatePct(0, &state.MonthPlan{GrossUAH: state.Major(60_000, money.UAH)}); got != 0 {
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
