package domain

import "math"

// ProjectCapital симулює капітал портфеля помісячно, спираючись на РЕАЛЬНІ
// відомі потоки наявних паперів, а не на суху формулу складного відсотка:
//
//   - locked — номінал наявних паперів; валюється за номіналом і не росте
//     сам, лише генерує реальні купони (monthlyCoupon) у готівку, а при
//     погашенні (monthlyRedeem) повертає номінал у готівку;
//   - cash — готівка; НЕ приносить нічого, поки не реінвестована; щомісяця
//     поповнюється внеском, реальними купонами й погашеннями;
//   - invested — реінвестований пул (нові папери); росте під ставку
//     annualRatePct (купони припускаємо авто-реінвестованими всередині).
//
// Готівка реінвестується в invested щойно назбирується на найдешевший
// папір (threshold); залишок менший за папір лишається лежати як cash.
// Усі суми — у грн-еквіваленті (major). monthlyCoupon/monthlyRedeem —
// індекс = порядковий номер місяця від сьогодні (1..).
func ProjectCapital(cash0, nominal0, contribMonthly, threshold, annualRatePct float64,
	monthlyCoupon, monthlyRedeem map[int]float64, months int) float64 {

	rM := annualRatePct / 100 / 12
	cash := cash0
	invested := 0.0
	locked := nominal0
	for m := 1; m <= months; m++ {
		invested *= 1 + rM
		cash += contribMonthly + monthlyCoupon[m] + monthlyRedeem[m]
		locked -= monthlyRedeem[m]
		if locked < 0 {
			locked = 0
		}
		if threshold > 0 {
			if n := math.Floor(cash / threshold); n > 0 {
				invested += n * threshold
				cash -= n * threshold
			}
		} else {
			invested += cash
			cash = 0
		}
	}
	return invested + locked + cash
}

// RequiredMonthly — бісекцією підбирає внесок/міс, за якого симуляція сягає
// goal рівно на місяці months. Повертає 0, якщо months<=0.
func RequiredMonthly(cash0, nominal0, threshold, annualRatePct, goal float64,
	monthlyCoupon, monthlyRedeem map[int]float64, months int) float64 {

	if months <= 0 {
		return 0
	}
	lo, hi := 0.0, goal // внесок goal/міс завідомо перевищує ціль
	for i := 0; i < 60; i++ {
		mid := (lo + hi) / 2
		if ProjectCapital(cash0, nominal0, mid, threshold, annualRatePct, monthlyCoupon, monthlyRedeem, months) < goal {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}
