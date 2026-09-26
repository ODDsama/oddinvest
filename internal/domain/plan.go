package domain

import money "github.com/Rhymond/go-money"

// ProgressPct — прогрес до цілі у відсотках (0..∞), ціле число.
// Незаданий факт — це нуль прогресу, а не паніка: поле опційне.
func ProgressPct(fact, target *money.Money) int {
	if fact == nil || target == nil || target.IsZero() {
		return 0
	}
	return int(fact.Amount() * 100 / target.Amount())
}
