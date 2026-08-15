// Розгортання плану (фаза 9) у те, що вже вміє sleeveFactory:
// помісячний гривневий вектор внеску (ContribByMonth) і купонні/погашальні
// записи замків (Lock). Сирі рядки — store.PlanFlow/store.PlanAction —
// нічого не рахують самі (state_plan.go про це не забуває навіть у назві:
// «сирі факти» лишаються фактами до цього файлу), а тут вони стають
// числами, прив'язаними до місяця симуляції.
package api

import (
	"math"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/store"

	money "github.com/Rhymond/go-money"
)

// monthOffsetRaw — місяць дати d відносно today, БЕЗ обмеження знизу.
// <1 означає «вже минуло чи сьогодні».
func monthOffsetRaw(today, d domain.Date) int {
	return (d.Year()-today.Year())*12 + int(d.Month()) - int(today.Month())
}

// monthOffset — те саме, але не менше 1: дата в минулому чи сьогодні
// означає «вже діє», а не «ніколи». Той самий компроміс, що й у
// сирих потоках Cashflow (sleeveFactory, збірка Coupon/Redeem): подія,
// запланована заднім числом, усе одно мусить зʼявитись у моделі, а не
// зникнути.
func monthOffset(today, d domain.Date) int {
	if mi := monthOffsetRaw(today, d); mi >= 1 {
		return mi
	}
	return 1
}

// planFlowMonthlyUAH — скільки потік f спрямовує в план на місяці m (1..),
// у гривневому НОМІНАЛІ місяця m. Додатне — дохід, від'ємне — витрата,
// нуль — потік цього місяця мовчить (ще не почався, вже закінчився, не
// той місяць періодичності).
//
// Валюта потоку конвертується в гривню за СЬОГОДНІШНІМ курсом — модель
// не вгадує майбутній, той самий компроміс, що й у решти проєкції.
func planFlowMonthlyUAH(f store.PlanFlow, today domain.Date, rates fx.Rates, m int) float64 {
	raw := monthOffsetRaw(today, f.FromDate)
	var start int
	switch f.Cadence {
	case "once":
		// Разова подія в минулому на майбутню проєкцію не впливає —
		// на відміну від регулярного потоку, у неї немає «наступного
		// разу», де можна було б надолужити.
		if raw < 1 {
			return 0
		}
		start = raw
	default:
		start = raw
		if start < 1 {
			start = 1
		}
	}
	if m < start {
		return 0
	}
	if f.UntilDate != "" {
		end := monthOffsetRaw(today, f.UntilDate)
		if end < 1 {
			end = 1
		}
		if m > end {
			return 0
		}
	}
	step := 1
	switch f.Cadence {
	case "once":
		if m != start {
			return 0
		}
	case "month":
		step = 1
	case "quarter":
		step = 3
	case "year":
		step = 12
	default:
		return 0
	}
	if f.Cadence != "once" && (m-start)%step != 0 {
		return 0
	}

	amt := float64(f.Amount) / 100
	// Індексація — лише для регулярних потоків: разова виплата не має
	// «наступного разу», де зростання мало б сенс.
	if f.GrowthBP != 0 && f.Cadence != "once" {
		if years := (m - start) / 12; years > 0 {
			amt *= math.Pow(1+float64(f.GrowthBP)/10000, float64(years))
		}
	}

	uah := amt
	if f.Currency != money.UAH {
		u, err := fx.ToUAH(money.New(int64(math.Round(amt*100)), f.Currency), rates)
		if err != nil {
			return 0 // курсу немає — цей місяць пропускаємо чесно, а не падаємо
		}
		uah = float64(u.Amount()) / 100
	}
	uah *= float64(f.InvestBP) / 10000
	if f.Kind == "expense" {
		uah = -uah
	}
	return uah
}

// planLockFlows розкладає дію lock у місяць переходу m0, суму (нативна
// валюта) і купонні/погашальні записи для НЕЇ Ж — ті самі мапи, якими
// вже живуть облігації й вклади (Sleeve.Coupon/Redeem), нового поля під
// це не знадобилось.
//
// months == 0 («безстроково», накопичувальний фонд): купон платиться
// щомісяця до кінця горизонту, тіло не повертається взагалі — спрощення,
// яке приймає компроміс на користь простоти («два рядки в
// projState.step», а не нова гілка симуляції поруч з Accum/Dist).
func planLockFlows(a store.PlanAction, today domain.Date, horizon int) (m0 int, amount float64, coupon, redeem map[int]float64) {
	m0 = monthOffset(today, a.Date)
	amount = float64(a.Amount) / 100
	coupon, redeem = map[int]float64{}, map[int]float64{}
	if amount <= 0 || m0 > horizon {
		return m0, amount, coupon, redeem
	}
	end := horizon
	if a.Months > 0 {
		if e := m0 + a.Months; e < end {
			end = e
		}
	}
	if monthly := amount * (float64(a.RateBP) / 10000) / 12; monthly > 0 {
		for m := m0 + 1; m <= end; m++ {
			coupon[m] += monthly
		}
	}
	if a.Months > 0 && m0+a.Months <= horizon {
		redeem[m0+a.Months] += amount
	}
	return m0, amount, coupon, redeem
}

// shareFromUSDEUR — повна валютна карта часток із двох заданих (USD, EUR)
// і гривневого залишку. Спільна для базових часток (з налаштувань) і для
// кожної точки зламу set_shares — щоб «решта йде в гривню» рахувалась
// рівно один раз.
func shareFromUSDEUR(usd, eur float64) map[string]float64 {
	out := map[string]float64{money.USD: usd, money.EUR: eur}
	if rest := 1 - usd - eur; rest > 0 {
		out[money.UAH] = rest
	} else {
		out[money.UAH] = 0
	}
	return out
}
