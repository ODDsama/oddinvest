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
//
// accum і dist — позиції фондів (accum.go, dist.go). Порожні для всього,
// крім рукавів із фондами, і тоді стан поводиться точно як раніше.
type projState struct {
	cash, invested, locked float64
	accum                  []accumState
	dist                   []distState
}

func (p *projState) total() float64 {
	return p.invested + p.locked + p.cash + p.accumTotal() + p.distTotal()
}

// incomeMonthly — потік, який портфель ВІДДАЄ на місяці m, у нативній
// валюті рукава: те, що можна забирати, не проїдаючи тіло.
//
// Одна функція на обидві картки навмисно — «скільки буде приносити» в
// проєкції й «коли дохід покриє життя» в незалежності. Доти формула
// стояла двома копіями за сто рядків одна від одної, і кожна нова
// сутність капіталу мусила бути дописана в обидві.
//
// Складники міряні кожен своєю ставкою, і це головне:
//
//   - invested — реінвестований пул, тобто нові папери: ставка рукава;
//   - locked — номінал ОВДП і тіло вкладів: та сама ставка, бо саме їхній
//     купон її і задає;
//   - dist — розподільні фонди: ВЛАСНА дивідендна, а не облігаційна;
//   - accum — накопичувальні: нуль, вони не платять нічого;
//   - cash — нуль: готівка, що не дотягла до найдешевшого паперу, не
//     приносить, і вважати її дохідною означало б обіцяти потік із
//     грошей, які лежать.
func (p *projState) incomeMonthly(s Sleeve, m int) float64 {
	return (p.invested+p.locked)*MonthlyRate(s.rateAt(m)) + p.pay()
}

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

// newState — стартовий стан рукава.
//
// Один конструктор на всі шість симуляцій навмисно. Доти кожна збирала
// projState літералом у себе, і нове поле довелось би дописувати в шість
// місць — забуте в одному дало б симуляцію, що тихо розходиться з
// рештою, причому не помилкою, а іншою відповіддю на те саме питання.
func (s Sleeve) newState() projState {
	var st projState
	st.cash, st.locked = s.Cash0, s.Nominal0
	if len(s.Accum) > 0 {
		st.accum = make([]accumState, 0, len(s.Accum))
		for _, a := range s.Accum {
			st.accum = append(st.accum, accumState{
				value: a.Value0, cost: a.Cost0, rM: MonthlyRate(a.RatePct),
				closeM: a.CloseM, taxPct: a.TaxPct, exitTaxPct: a.ExitTaxPct,
			})
		}
	}
	if len(s.Dist) > 0 {
		st.dist = make([]distState, 0, len(s.Dist))
		for _, d := range s.Dist {
			st.dist = append(st.dist, distState{
				value: d.Value, cost: d.Cost, rM: MonthlyRate(d.RatePct),
				exitTaxPct: d.ExitTaxPct,
			})
		}
	}
	return st
}

// stepSleeve — крок рукава на місяці m із заданим внеском.
//
// Гроші фондів приходять ТИМ САМИМ входом, що й купон: далі вони лежать
// готівкою, доки не назбирається на найдешевший папір. Два джерела:
// дивіденди розподільних позицій (щомісяця, весь горизонт) і разова сума
// накопичувальної, яка цього місяця закрилась. Остання далі працює за
// ставкою рукава, а не за фондовою — фонду на той момент немає.
//
// Внесок передається зовні, а не береться з рукава, бо декумуляція
// крокує тими самими правилами, але без внесків.
//
// Lock (планова дія «замкнути суму на строк») переносить гроші з
// ліквідного в locked ДО звичайного кроку: до місяця m готівку вже могло
// змести в invested порогом (step нижче), тож замок бере спершу звідти,
// потім із cash. Якщо не вистачає жодного — cash іде в мінус: застосунок
// показує наслідок гіпотези, а не блокує її, як і кошик покупки
// (handlers_whatif.go).
func (p *projState) stepSleeve(s Sleeve, m int, contrib float64) {
	if amt := s.Lock[m]; amt > 0 {
		fromInvested := math.Min(amt, p.invested)
		p.invested -= fromInvested
		p.cash -= amt - fromInvested
		p.locked += amt
	}
	fromFunds := p.grow(m) + p.pay()
	p.step(MonthlyRate(s.rateAt(m)), contrib, s.Threshold, s.Coupon[m]+fromFunds, s.Redeem[m])
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
