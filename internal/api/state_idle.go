package api

// Простій грошей: скільки з вільних грошей брокера можна вкласти ВЖЕ, з
// якого дня вони лежать і що це коштує.
//
// НАВІЩО. Вільні гроші входять у капітал (capital_uah) і не входять у
// жодну дохідність — картка «З чого складається дохідність» ставить проти
// них прочерк. Для застосунку, чия головна роль — реінвест-асистент, це
// дірка рівно посередині: ціни лежання грошей на рахунку не було видно
// ніде.
//
// ОЗНАЧЕННЯ, три частини, і кожна спирається на те, що вже є.
//
// Простій — по кожній парі (брокер × валюта) та частина балансу, на яку
// вже вистачає цілих квитків: floor(баланс / квиток) × квиток. Квиток —
// найдешевший папір у валюті або мінімум вкладу, те саме число, що стоїть
// під плиткою «Вільні гроші» (reinvest_min). Решта нижче квитка — не
// простій, а «збирається на квиток», і про неї каже savingTask. Пара, а не
// валюта: гривня в mono не купить папір в inzhur, і 600 + 600 у двох
// брокерів при квитку 1 000 не купують нічого.
//
// Сертифікат фонду квитком НЕ є, хоч «Що купити» його й пропонує: у REIT
// він коштує 11 ₴, і з ним простоєм ставала б будь-яка решта на рахунку —
// сигнал потонув би в копійках, а плитка поруч і далі казала б «поріг
// покупки 1 000». Квиток тут — рівно той поріг, що на плитці.
//
// Вік — правило FIFO з domain.IdleIncome, застосоване до ВСІХ подій
// гаманця пари (поповнення, конвертації, купони, дивіденди, відсотки, мінус
// лоти, купівлі фондів, внески, зняття), а не лише до доходу. Простій — це
// найстаріші з уцілілих надходжень до суми простою; since — дата
// найстарішого, age_days — зважений за сумою вік. Історії балансів для
// цього не треба: журнал подій знає більше, ніж знав би ряд знімків.
//
// Ціна — ставка НАЙКРАЩОЇ ДОСТУПНОЇ поради тієї ж валюти, яка вміщується в
// цього брокера, реальна (RealPct): те саме поле й те саме правило, що в
// annotateWaitCost для «ціни очікування» на порадах — одна міра на
// застосунок. cost_month_uah = простій × ставка / 12, cost_so_far_uah — від
// дати кожного надходження до сьогодні, ACT/365 як у domain.WaitCost.
// Ставка сьогоднішня, і підпис це каже («за сьогоднішньою порадою»).
//
// ЧОМУ ДВА КРОКИ. Суму й вік рахує buildState — вони з гаманця. Ціну
// приписує buildStateTasked: порад усередині buildState немає (довід у
// шапці state_tasks.go), тож поле там заповнене без ціни, і whatif/план це
// не хвилює — вони її не читають.

import (
	"math"
	"sort"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// idleFreeDays — скільки добових знімків поспіль без простою дають віху
// «Місяць без простою». Тридцять, як і серія внесків: місяць — природний
// цикл цього застосунку.
const idleFreeDays = 30

// buildIdle — простій по парах із журналу гаманця. nil, коли жодна пара не
// дотягує до квитка: тоді простою немає, і поле мовчить, а не показує нулі.
func buildIdle(cash *cashLedger, minByCur map[string]int64, rates fx.Rates, today domain.Date) *state.IdleCash {
	keys := make([]store.BrokerCur, 0, len(cash.byBC))
	for k := range cash.byBC {
		keys = append(keys, k)
	}
	// Порядок сталий: документ іде в MQTT і в золотий фікстур.
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Broker != keys[j].Broker {
			return keys[i].Broker < keys[j].Broker
		}
		return keys[i].Currency < keys[j].Currency
	})
	out := &state.IdleCash{}
	ageWeighted := 0.0
	for _, k := range keys {
		bal := cash.byBC[k]
		ticket := minByCur[k.Currency]
		if ticket <= 0 || bal < ticket {
			continue
		}
		investable := bal / ticket * ticket
		uahAmt, err := fx.ToUAH(money.New(investable, k.Currency), rates)
		if err != nil {
			continue
		}
		// Вік — найстаріші уцілілі надходження до суми простою. Уцілілих
		// завжди не менше за баланс (списання зʼїдає лише те, що було), тож
		// сума набирається завжди; майбутні дати (поповнення наперед) віку
		// не дають.
		var moneyDays float64
		var since domain.Date
		left := investable
		for _, lot := range cash.lots(k) {
			if left <= 0 {
				break
			}
			take := lot.Amount
			if take > left {
				take = left
			}
			if since == "" {
				since = lot.Date
			}
			if d := daysBetween(lot.Date, today); d > 0 {
				moneyDays += float64(take) * float64(d)
			}
			left -= take
		}
		broker := k.Broker
		if broker == "" {
			broker = noBrokerLabel
		}
		p := state.IdlePair{
			Broker: broker, Currency: k.Currency,
			Investable:    float64(investable) / 100,
			InvestableUAH: state.Major(uahAmt),
			Since:         string(since),
			AgeDays:       round2(moneyDays / float64(investable)),
		}
		if since != "" {
			if d := daysBetween(since, today); d > 0 {
				p.Days = d
			}
		}
		out.ByPair = append(out.ByPair, p)
		out.InvestableUAH += p.InvestableUAH
		ageWeighted += p.InvestableUAH * p.AgeDays
		if out.Since == "" || (p.Since != "" && p.Since < out.Since) {
			out.Since = p.Since
		}
		if p.Days > out.Days {
			out.Days = p.Days
		}
	}
	if len(out.ByPair) == 0 {
		return nil
	}
	out.InvestableUAH = round2(out.InvestableUAH)
	out.AgeDays = round2(ageWeighted / out.InvestableUAH)
	return out
}

// buildIdleCost — ціна простою за порадами. Порада береться перша в
// рейтингу, яка по кишені, тієї ж валюти й уміщується в цього брокера
// (той самий відбір, що в annotateWaitCost). Немає такої — пара лишається
// без ціни: гроші лежать, але купити ними в цього брокера нічого, і
// вигадувати ставку не з чого. nil, коли ціни немає ні в однієї пари.
//
// Окремим полем документа (idle_cost), а не всередині idle: ціна — порада
// про сьогоднішній світ, і в гіпотетичному «після» кошика її бути не може
// (довід — у шапці state_tasks.go і в тесті TestWhatIfEmptyPlanMatchesSummary).
func buildIdleCost(idle *state.IdleCash, sug []suggestion) *state.IdleCost {
	if idle == nil {
		return nil
	}
	pick := func(broker, cur string) *suggestion {
		for i := range sug {
			s := &sug[i]
			if !s.CanBuy || s.Currency != cur || s.RealPct <= 0 {
				continue
			}
			for _, f := range s.Brokers {
				if f.Broker == broker {
					return s
				}
			}
		}
		return nil
	}
	out := &state.IdleCost{}
	rateW, base, topUAH := 0.0, 0.0, 0.0
	for _, p := range idle.ByPair {
		s := pick(p.Broker, p.Currency)
		if s == nil {
			continue
		}
		c := state.IdleCostPair{
			Broker: p.Broker, Currency: p.Currency,
			RatePct:      s.RealPct,
			RateLabel:    suggestName(s),
			CostMonthUAH: round2(p.InvestableUAH * s.RealPct / 100 / 12),
			CostSoFarUAH: round2(p.InvestableUAH * s.RealPct / 100 * p.AgeDays / 365),
		}
		out.ByPair = append(out.ByPair, c)
		out.CostMonthUAH += c.CostMonthUAH
		out.CostSoFarUAH += c.CostSoFarUAH
		rateW += p.InvestableUAH * s.RealPct
		base += p.InvestableUAH
		if p.InvestableUAH > topUAH {
			topUAH, out.RateLabel = p.InvestableUAH, c.RateLabel
		}
	}
	if len(out.ByPair) == 0 {
		return nil
	}
	out.CostMonthUAH = round2(out.CostMonthUAH)
	out.CostSoFarUAH = round2(out.CostSoFarUAH)
	out.RatePct = math.Round(rateW/base*100) / 100
	return out
}

// idleStreak — поточна серія добових знімків без простою й дата, коли
// серія вперше сягнула idleFreeDays. Знімки, що простою не знають
// (idle_uah < 0), серію обривають, а не продовжують: «не рахували» — не
// те саме, що «не було». idleNow — чи простій є СЬОГОДНІ за документом:
// знімок пишеться вранці, а гроші приходять удень, і серію, зірвану
// сьогодні, обривати треба сьогодні.
func idleStreak(snaps []store.Snapshot, idleNow bool) (current int, earnedOn string) {
	sorted := append([]store.Snapshot(nil), snaps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Date < sorted[j].Date })
	run := 0
	for _, sn := range sorted {
		if sn.IdleUAH == 0 {
			run++
			if run == idleFreeDays && earnedOn == "" {
				earnedOn = string(sn.Date)
			}
		} else {
			run = 0
		}
	}
	if idleNow {
		run = 0
	}
	return run, earnedOn
}
