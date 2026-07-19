package domain

import "math"

// CashPoint — майбутній потік для оцінки процентного ризику:
// Years — скільки років від дати оцінки, Amount — сума (major, своя валюта).
type CashPoint struct {
	Years  float64
	Amount float64
}

// Duration рахує дюрацію Маколея, модифіковану дюрацію і приведену
// вартість (PV) набору потоків за річною ставкою y (частка: 0.157 = 15.7%).
//
// Дюрація Маколея — середньозважений (за приведеною вартістю) строк
// надходження грошей. Модифікована дюрація показує чутливість ціни до
// ставки: ΔP/P ≈ −D_mod × Δy. Для ОВДП це головна міра ризику: чим
// довша драбина, тим болючіше б'є зростання ставок.
//
// Повертає нулі, якщо потоків немає або PV≤0.
func Duration(points []CashPoint, y float64) (macaulay, modified, pv float64) {
	if y <= -1 {
		return 0, 0, 0
	}
	var weighted float64
	for _, p := range points {
		df := math.Pow(1+y, p.Years)
		if df <= 0 {
			continue
		}
		v := p.Amount / df
		pv += v
		weighted += p.Years * v
	}
	if pv <= 0 {
		return 0, 0, 0
	}
	macaulay = weighted / pv
	modified = macaulay / (1 + y)
	return macaulay, modified, pv
}

// PriceChangePct — очікувана зміна вартості портфеля у відсотках при зміні
// ставки на deltaPP процентних пунктів: ΔP/P ≈ −D_mod × Δy.
func PriceChangePct(modified, deltaPP float64) float64 {
	return -modified * deltaPP
}
