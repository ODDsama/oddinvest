package domain

import money "github.com/Rhymond/go-money"

// MonthInvested — сума покупок у гривневому вимірі за місяць (yyyy-mm).
// Валютні покупки конвертуються викликачем заздалегідь; тут очікуються
// суми в одній валюті (UAH-еквівалент).
func MonthInvested(items []*money.Money) (*money.Money, error) {
	if len(items) == 0 {
		return money.New(0, money.UAH), nil
	}
	return SumSameCurrency(items...)
}

// ProgressPct — прогрес до цілі у відсотках (0..∞), ціле число.
func ProgressPct(fact, target *money.Money) int {
	if target == nil || target.IsZero() {
		return 0
	}
	return int(fact.Amount() * 100 / target.Amount())
}
