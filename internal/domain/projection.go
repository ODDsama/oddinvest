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

	st := projState{cash: cash0, locked: nominal0}
	rM := MonthlyRate(annualRatePct)
	for m := 1; m <= months; m++ {
		st.step(rM, contribMonthly, threshold, monthlyCoupon[m], monthlyRedeem[m])
	}
	return st.total()
}

// MonthlyRate — еквівалентна місячна ставка: (1+r)^(1/12)−1.
// Свідомо НЕ r/12: за щомісячної капіталізації r/12 дає фактичну річну
// вищу за заявлену (15.78% перетворювалось на 16.97%), і проєкція тихо
// завищувала результат — на 10 роках це ~6%.
func MonthlyRate(annualPct float64) float64 {
	if annualPct <= -100 {
		return 0
	}
	return math.Pow(1+annualPct/100, 1.0/12) - 1
}

// projState — стан симуляції. Винесено окремо, щоб ProjectCapital і
// MonthsToReach крокували ідентично й не розїхались із часом.
type projState struct{ cash, invested, locked float64 }

func (p *projState) total() float64 { return p.invested + p.locked + p.cash }

func (p *projState) step(rMonthly, contrib, threshold, coupon, redeem float64) {
	p.invested *= 1 + rMonthly
	p.cash += contrib + coupon + redeem
	p.locked -= redeem
	if p.locked < 0 {
		p.locked = 0
	}
	if threshold > 0 {
		if n := math.Floor(p.cash / threshold); n > 0 {
			p.invested += n * threshold
			p.cash -= n * threshold
		}
	} else {
		p.invested += p.cash
		p.cash = 0
	}
}

// MonthsToReach — за скільки місяців капітал сягне кожної з цілей за
// поточним темпом. Одна симуляція на всі цілі одразу.
// Повертає: -1 = ціль уже досягнута, 0 = не досягається за maxMonths,
// >0 = номер місяця.
func MonthsToReach(cash0, nominal0, contribMonthly, threshold, annualRatePct float64,
	monthlyCoupon, monthlyRedeem map[int]float64, targets []float64, maxMonths int) []int {

	res := make([]int, len(targets))
	st := projState{cash: cash0, locked: nominal0}
	for i, t := range targets {
		if t > 0 && st.total() >= t {
			res[i] = -1
		}
	}
	rM := MonthlyRate(annualRatePct)
	for m := 1; m <= maxMonths; m++ {
		st.step(rM, contribMonthly, threshold, monthlyCoupon[m], monthlyRedeem[m])
		tot := st.total()
		for i, t := range targets {
			if res[i] == 0 && t > 0 && tot >= t {
				res[i] = m
			}
		}
	}
	return res
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
