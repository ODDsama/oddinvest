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

// planFlowNative — скільки потік f спрямовує в план на місяці m (1..), у
// ВЛАСНІЙ валюті потоку. Додатне — дохід, від'ємне — витрата, нуль — потік
// цього місяця мовчить (ще не почався, вже закінчився, не той місяць
// періодичності). Частка в портфель уже застосована.
//
// Це єдине означення періодичності й індексації в застосунку: гривневий
// вигляд (planFlowMonthlyUAH), колонка «дає ₴/міс» і профіль надходжень —
// усі три стоять на ньому, тож розійтись їм нема на чому.
func planFlowNative(f store.PlanFlow, today domain.Date, m int) float64 {
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
		// Тут НЕ можна обрізати знизу одиницею, як для дати початку.
		//
		// Місяць 1 — це НАСТУПНИЙ місяць, а не поточний: профіль підписує
		// точки як today.AddMonths(m), починаючи з m=1, і разова подія,
		// датована сьогоднішнім числом, свідомо не рахується (гілка
		// "once" вище). Тобто потік, що закінчується цього місяця чи
		// раніше (end <= 0), у вікно проєкції не потрапляє взагалі.
		//
		// Доти обрізання `if end < 1 { end = 1 }` разом із перевіркою лише
		// `m > end` давало хибну умову на першому місяці — і джерело,
		// закрите хоч торік, платило ще один раз: зайвий місяць у
		// проєкції й зайва 1/12 у колонці «дає ₴/міс».
		//
		// Для дати ПОЧАТКУ те саме обрізання лишається правильним і має
		// протилежний сенс: потік, заведений заднім числом, уже діє, тож
		// у вікно входить з першого ж місяця (див. monthOffset).
		if end < 1 {
			return 0
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

	amt *= float64(f.InvestBP) / 10000
	if f.Kind == "expense" {
		amt = -amt
	}
	return amt
}

// planFlowMonthlyUAH — те саме, але переведене в гривню за СЬОГОДНІШНІМ
// курсом.
//
// ВАЖЛИВО, куди це годиться, а куди ні. Сьогоднішній курс — чесне число
// для «скільки план дає ЗАРАЗ» (плитка розділу, колонка таблиці, профіль
// надходжень): усі вони описують сьогодні. Але для симуляції на роки
// вперед воно хибне, і хибне тихо: гривневий номінал валютного потоку в
// моделі мусить рости разом із курсом, інакше рукав ділить його на курс,
// що росте (sleeves.go:contribAt), і $500 на 120-му місяці перетворюються
// приблизно на $275. Тому в проєкцію валютні потоки йдуть НЕ через цю
// функцію, а нативними — див. newSleeveFactory.
func planFlowMonthlyUAH(f store.PlanFlow, today domain.Date, rates fx.Rates, m int) float64 {
	amt := planFlowNative(f, today, m)
	if amt == 0 || f.Currency == money.UAH {
		return amt
	}
	// Знак переживає конвертацію окремо: money.New від'ємної суми через
	// fx.ToUAH проходить, але покладатись на це не варто — беремо модуль і
	// повертаємо знак назад.
	sign := 1.0
	if amt < 0 {
		sign, amt = -1, -amt
	}
	u, err := fx.ToUAH(money.New(int64(math.Round(amt*100)), f.Currency), rates)
	if err != nil {
		return 0 // курсу немає — цей місяць пропускаємо чесно, а не падаємо
	}
	return sign * float64(u.Amount()) / 100
}

// planProvidesMonths — вікно, за яким усереднюється «скільки план дає».
// Разова стаття (премія, ремонт) не має смикати число вгору-вниз щомісяця,
// тож дивимось на рік уперед.
//
// Константа, а не літерал у двох файлах: на цьому числі стоїть ТОТОЖНІСТЬ
// — сума колонки «дає ₴/міс» по рядках мусить збігатися з плиткою «План
// дає». Обидва боки міряються тим самим вікном, і розійтись їм можна
// рівно одним способом — якщо число буде записане двічі.
const planProvidesMonths = 12

// planFlowProvidesUAH — скільки потік дає В СЕРЕДНЬОМУ за найближчі months
// місяців, ₴/міс за сьогоднішнім курсом. Те саме означення, що й у
// PlanProvidesUAH, тільки по одному потоку: суми там і тут просто міняються
// місцями, тож тотожність структурна, а не збіг.
//
// Наслідки, які варто розуміти, читаючи колонку: разова стаття у вікні дає
// суму/12, поза вікном — 0; квартальний потік із початком зараз влучає в 4
// місяці з 12, тобто суму/3; потік, що починається пізніше ніж за рік,
// показує 0 — і це те саме, що вже показує число вгорі.
func planFlowProvidesUAH(f store.PlanFlow, today domain.Date, rates fx.Rates, months int) float64 {
	if months <= 0 {
		return 0
	}
	var sum float64
	for m := 1; m <= months; m++ {
		sum += planFlowMonthlyUAH(f, today, rates, m)
	}
	return sum / float64(months)
}

// planFlowGrossUAH — те саме до застосування «частки в портфель».
//
// Рахується тією ж функцією на копії потоку зі 100%: invest_bp
// застосовується останнім скаляром (planFlowNative), тож множення
// комутує й нової арифметики тут не з'являється.
func planFlowGrossUAH(f store.PlanFlow, today domain.Date, rates fx.Rates, months int) float64 {
	f.InvestBP = 10000
	return planFlowProvidesUAH(f, today, rates, months)
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
