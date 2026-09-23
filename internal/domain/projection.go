package domain

import "math"

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

// projState — стан симуляції одного рукава, помісячно, на РЕАЛЬНИХ
// відомих потоках наявних паперів, а не на сухій формулі складного
// відсотка:
//
//   - locked — номінал наявних паперів; валюється за номіналом і не росте
//     сам, лише генерує реальні купони в готівку, а при погашенні повертає
//     номінал у готівку;
//   - cash — готівка; НЕ приносить нічого, поки не реінвестована; щомісяця
//     поповнюється внеском, реальними купонами й погашеннями;
//   - invested — реінвестований пул (нові папери); росте під ставку
//     (купони припускаємо авто-реінвестованими всередині).
//
// Готівка реінвестується в invested щойно назбирується на найдешевший
// папір (threshold); залишок менший за папір лишається лежати як cash.
//
// Доти поруч жили ProjectCapital, MonthsToReach і RequiredMonthly —
// однорукавні обгортки над тим самим кроком. Застосунок давно рахує
// рукавами (sleeves.go: ProjectSleeves, MonthsToReachSleeves,
// RequiredMonthlySleeves), а обгортки кликали лише тести, тож їх прибрано:
// мертва паралельна реалізація читається як зразок і тиражується. Сам
// крок перевіряють тести projection_test.go через тестовий runSteps.
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
				locked: a.Locked, contrib: a.ContribByMonth,
				payoutM: a.PayoutM,
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
// ліквідного в locked ДО звичайного кроку. Spend (планована купівля
// накопичувального фонду) знімає їх так само, але в locked не кладе —
// його друга половина чекає в Accum.ContribByMonth і додасться в grow()
// нижче, у тому ж місяці.
//
// Обидва списують ДО кроку з того самого доводу: до місяця m готівку вже
// могло змести в invested порогом (step нижче), тож брати треба спершу
// звідти, потім із cash. Якщо не вистачає жодного — cash іде в мінус:
// застосунок показує наслідок гіпотези, а не блокує її, як і кошик
// покупки (handlers_whatif.go).
func (p *projState) stepSleeve(s Sleeve, m int, contrib float64) {
	if amt := s.Lock[m]; amt > 0 {
		p.debit(amt)
		p.locked += amt
	}
	if amt := s.Spend[m]; amt > 0 {
		// locked НЕ росте: гроші пішли в позицію, яка росте сама.
		p.debit(amt)
	}
	fromFunds := p.grow(m) + p.pay()
	p.step(MonthlyRate(s.rateAt(m)), contrib, s.Threshold, s.Coupon[m]+fromFunds, s.Redeem[m])
}

// debit — зняти суму з ліквідного боку: спершу з invested, потім із cash.
//
// Спільна для Lock і Spend навмисно. Порядок «спершу invested» тут не
// косметика (див. stepSleeve), і дві копії цього правила розійшлися б
// саме тоді, коли одну з них хтось поправить.
func (p *projState) debit(amt float64) {
	fromInvested := math.Min(amt, p.invested)
	p.invested -= fromInvested
	p.cash -= amt - fromInvested
}
