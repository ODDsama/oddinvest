package domain

import "math"

// Валютні рукави: симуляція, у якій гривня, долар і євро поводяться
// по-різному.
//
// Раніше все зводилось у гривню за сьогоднішнім курсом ОДИН раз, і далі
// курс вважався вічним. Наслідок: гривневий папір під 16% завжди бив
// доларовий під 4%, бо єдине, чим вони відрізнялись, — цифра купона.
// Насправді 4% у доларі при девальвації 6%/рік ≈ 10% у гривні, тож
// модель систематично недооцінювала валютну частину портфеля.
//
// Тепер кожна валюта симулюється ОКРЕМО і в НАТИВНІЙ валюті — свій
// календар виплат, своя дохідність, свій поріг докупівлі. Девальвація
// входить у двох місцях: гривневі поповнення валютних рукавів щомісяця
// купують менше валюти, а гривневий рукав наприкінці переводиться в
// сьогоднішню купівельну спроможність.

// Sleeve — один валютний рукав. Усі суми, крім ContribUAH, — у нативній
// валюті рукава (major).
type Sleeve struct {
	Currency  string
	Cash0     float64         // готівка на рахунку
	Nominal0  float64         // номінал наявних паперів
	RatePct   float64         // річна дохідність реінвесту в цій валюті
	Threshold float64         // ціна найдешевшого паперу (0 = докупівля без порога)
	Coupon    map[int]float64 // реальні купони по місяцях (індекс 1..)
	Redeem    map[int]float64 // реальні погашення по місяцях
	// ContribUAH — скільки ГРИВЕНЬ на місяць спрямовується в цей рукав.
	// Саме гривень: заробіток гривневий, і саме він конвертується.
	ContribUAH float64
	// Rate0 — сьогоднішній курс, ₴ за одиницю валюти. Для UAH = 1.
	Rate0 float64
	// RateTerminalPct — довгострокова ставка, до якої сповзає RatePct за
	// GlideYears років. GlideYears = 0 вимикає спуск і лишає ставку вічною.
	//
	// Без цього модель припускала, що папери, куплені через десять років,
	// матимуть сьогоднішній воєнний купон. Гривневі 16-17% — це наслідок
	// війни та інфляції, а не норма; закладати їх на весь горизонт означає
	// малювати капітал, якого не буде. RatePct лишається фактом (за такою
	// ставкою можна купити сьогодні), а припущенням стає саме RateTerminalPct.
	RateTerminalPct float64
	GlideYears      float64
}

// rateAt — ставка реінвесту на місяці m: лінійний спуск від сьогоднішньої
// до довгострокової за GlideYears років, далі рівно довгострокова.
func (s Sleeve) rateAt(m int) float64 {
	if s.GlideYears <= 0 {
		return s.RatePct
	}
	w := 1 - float64(m)/(12*s.GlideYears)
	if w < 0 {
		w = 0
	}
	return s.RateTerminalPct + (s.RatePct-s.RateTerminalPct)*w
}

// SleeveResult — підсумок симуляції у двох одиницях.
type SleeveResult struct {
	// TodayUAH — капітал у гривні СЬОГОДНІШНЬОЇ купівельної спроможності.
	// Це та цифра, яку можна порівнювати з ціллю, заданою сьогодні.
	TodayUAH float64
	// NominalUAH — те саме в гривні МАЙБУТНЬОГО дня, тобто скільки
	// намальовано буде на рахунку. Більше за TodayUAH рівно настільки,
	// наскільки знецінилась гривня.
	NominalUAH float64
	// ByCurrency — підсумок кожного рукава в його нативній валюті.
	ByCurrency map[string]float64
}

// InTodayUSD — той самий капітал у сьогоднішніх доларах. rate0USD — курс
// ₴ за долар на сьогодні; 0 = курсу немає, повертаємо 0.
func (r SleeveResult) InTodayUSD(rate0USD float64) float64 {
	if rate0USD <= 0 {
		return 0
	}
	return r.TodayUAH / rate0USD
}

// ProjectSleeves жене всі рукави months місяців і зводить підсумок.
// devalPct — річне знецінення гривні до твердої валюти, %.
func ProjectSleeves(sleeves []Sleeve, devalPct float64, months int) SleeveResult {
	dM := MonthlyRate(devalPct)
	out := SleeveResult{ByCurrency: map[string]float64{}}
	for _, s := range sleeves {
		st := projState{cash: s.Cash0, locked: s.Nominal0}
		for m := 1; m <= months; m++ {
			st.step(MonthlyRate(s.rateAt(m)), s.contribAt(m, dM), s.Threshold, s.Coupon[m], s.Redeem[m])
		}
		total := st.total()
		out.ByCurrency[s.Currency] += total
		today, nominal := s.toUAH(total, dM, months)
		out.TodayUAH += today
		out.NominalUAH += nominal
	}
	return out
}

// contribAt — внесок у НАТИВНІЙ валюті на місяці m. Для гривні це просто
// план; для валютних рукавів гривневий внесок щомісяця купує менше
// валюти, бо курс росте.
func (s Sleeve) contribAt(m int, dM float64) float64 {
	if s.isUAH() {
		return s.ContribUAH
	}
	if s.Rate0 <= 0 {
		return 0
	}
	return s.ContribUAH / (s.Rate0 * math.Pow(1+dM, float64(m)))
}

// toUAH переводить підсумок рукава в сьогоднішні й номінальні гривні.
func (s Sleeve) toUAH(total, dM float64, months int) (today, nominal float64) {
	infl := math.Pow(1+dM, float64(months))
	if s.isUAH() {
		// Гривня номінально лишається собою, але купує менше.
		return total / infl, total
	}
	// Валюта тримає купівельну спроможність, зате в гривні дорожчає.
	return total * s.Rate0, total * s.Rate0 * infl
}

func (s Sleeve) isUAH() bool { return s.Currency == "UAH" || s.Currency == "" }

// RealContributed — скільки коштують у СЬОГОДНІШНІХ грошах стартовий
// капітал плюс номінальні внески, які просто відкладали й не вкладали.
//
// Потрібна, щоб порівняння «вклав vs просто відкладав» лишалось у одній
// одиниці. Внесок на місяці m — це номінальні гривні того місяця, тож
// сьогодні вони коштують менше рівно на знецінення за m місяців.
func RealContributed(start, contribMonthly, devalPct float64, months int) float64 {
	dM := MonthlyRate(devalPct)
	out := start
	for m := 1; m <= months; m++ {
		out += contribMonthly / math.Pow(1+dM, float64(m))
	}
	return out
}

// MonthsToReachSleeves — за скільки місяців капітал у СЬОГОДНІШНІХ
// гривнях сягне цілі. -1 = вже досягнуто, 0 = не досягається за maxMonths.
//
// Рукави крокують у лок-степі, бо ціль порівнюється із загальною сумою,
// а не з окремою валютою.
func MonthsToReachSleeves(sleeves []Sleeve, devalPct, target float64, maxMonths int) int {
	if target <= 0 {
		return 0
	}
	dM := MonthlyRate(devalPct)
	sts := make([]projState, len(sleeves))
	for i, s := range sleeves {
		sts[i] = projState{cash: s.Cash0, locked: s.Nominal0}
	}
	totalToday := func(m int) float64 {
		sum := 0.0
		for i, s := range sleeves {
			today, _ := s.toUAH(sts[i].total(), dM, m)
			sum += today
		}
		return sum
	}
	if totalToday(0) >= target {
		return -1
	}
	for m := 1; m <= maxMonths; m++ {
		for i, s := range sleeves {
			sts[i].step(MonthlyRate(s.rateAt(m)), s.contribAt(m, dM), s.Threshold, s.Coupon[m], s.Redeem[m])
		}
		if totalToday(m) >= target {
			return m
		}
	}
	return 0
}

// RequiredMonthlySleeves — бісекцією підбирає СУМАРНИЙ гривневий внесок,
// за якого капітал у сьогоднішніх гривнях сягає цілі рівно на місяці
// months. Внесок розкладається по рукавах у тих самих пропорціях, що й
// зараз, — щоб відповідь була про суму, а не про зміну стратегії.
func RequiredMonthlySleeves(sleeves []Sleeve, devalPct, goal float64, months int) float64 {
	if months <= 0 || goal <= 0 {
		return 0
	}
	base := 0.0
	for _, s := range sleeves {
		base += s.ContribUAH
	}
	shares := make([]float64, len(sleeves))
	for i, s := range sleeves {
		if base > 0 {
			shares[i] = s.ContribUAH / base
		} else if i == 0 {
			shares[i] = 1 // внеску ще немає — умовно ллємо в перший рукав
		}
	}
	withContrib := func(total float64) []Sleeve {
		out := make([]Sleeve, len(sleeves))
		copy(out, sleeves)
		for i := range out {
			out[i].ContribUAH = total * shares[i]
		}
		return out
	}
	lo, hi := 0.0, goal // внесок goal/міс завідомо перевищує ціль
	for i := 0; i < 60; i++ {
		mid := (lo + hi) / 2
		if ProjectSleeves(withContrib(mid), devalPct, months).TodayUAH < goal {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}
