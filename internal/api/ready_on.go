// «Коли вистачить»: дата, на яку рахунок покриє цю покупку.
//
// Доти помічник відповідав двійково — can_buy або ні, — і рядок, на який
// сьогодні не стає, просто опускався нижче. Питання, яке при цьому
// лишалось без відповіді, і є головним питанням реінвесту: чекати три
// тижні до купона чи взяти те, на що стане вже сьогодні. Дата й ціна
// очікування (domain.WaitCost) роблять із нього вибір із двох названих
// сторін.
//
// РАХУНКИ РОЗДІЛЬНІ. Дата рахується по парах (брокер × валюта), а не по
// валюті: гривня в mono не купить папір в inzhur, і купон, що прийде на
// чужий рахунок, цієї покупки не наблизить. Дата рядка — найраніша серед
// брокерів, і брокер названий поруч.
//
// ЩО ВХОДИТЬ У ДАТУ. Лише те, що портфель уже винен сам собі: купони й
// погашення ОВДП, відсотки й тіло вкладів. Три речі свідомо не входять, і
// в кожної своя причина.
//
//	Планові надходження (plan_flows) — зарплата це намір, який застосунок
//	не може перевірити, і дата повзла б від кожної правки плану. Питання
//	«а якщо докласти» вже має свою відповідь — план купівель і
//	POST /api/whatif.
//
//	Оцінені дивіденди фондів — вони саме оцінені (state_schedule.go рахує
//	їх зі ставки, а не із зобовʼязання, і позначає ключем fund:<назва>).
//	У календарі оцінка стоїть підписаною й читається як оцінка; дата ж —
//	одне число, у якому припущення стало б невидимим.
//
//	Виплати НПФ — гроші звідти не приходять до пенсійного віку, і
//	підмішувати їх у «коли зможу купити папір» означало б рахувати
//	покупку за гроші, яких не буде.
//
// ДРУГЕ «КОЛИ» В ЗАСТОСУНКУ — і воно лишається. savingTask
// (state_tasks.go) уже каже «за твоїм темпом це ≈ N днів», рахуючи з
// МІСЯЧНОЇ ЦІЛІ ВНЕСКІВ. Це інша міра того самого питання: там гроші, які
// ти плануєш доносити, тут — які портфель платить сам. Зводити їх в одне
// число не можна (одне спирається на намір, друге на зобовʼязання), тому
// обидва називають свою основу прямо: «за твоїм темпом» проти «з
// надходжень портфеля». Без цих слів два різні числа на двох екранах
// читались би як розбіжність.
//
// ЩО НЕ ЗМІНЮЄТЬСЯ. Порядок рядків. Ланцюг компаратора (Locked → CanBuy →
// ліміт/транзит → planScore → stale) — це політика, а дата — факт поруч
// із нею. Пустити дату в сортування означало б тихо завести нове правило
// під виглядом показу.
//
// ЦІНА ЦЬОГО ФАЙЛУ — один зайвий прохід по джерелах на /api/reinvest:
// привʼязати виплату до брокера можна лише через лоти, а state.Doc
// брокера у виплатах не несе (і не має нести — той документ іде в MQTT).
// Тому анотація живе в обробнику, а не в reinvestSuggestions: та сама
// збірка порад працює ще й усередині buildState (черга задач), і другий
// loadSources подорожчав би кожен /api/summary заради поля, якого черга
// не показує.
package api

import (
	"context"
	"sort"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// noBrokerLabel — як показується рахунок без брокера. Те саме «—», що й у
// гаманці (state_cash.go): два різні позначення одного рахунку розвели б
// баланс із датою.
const noBrokerLabel = "—"

// readyFlow — одне майбутнє надходження на конкретний рахунок.
type readyFlow struct {
	Date   domain.Date
	Amount int64 // мінорні, у валюті рахунку
	Label  string
}

// readyEvent — те саме назовні: з чого саме склалася сума.
type readyEvent struct {
	Date   string    `json:"date"`
	Label  string    `json:"label"`
	Amount moneyJSON `json:"amount"`
}

// incomeAhead — майбутні надходження в розрізі (брокер × валюта).
type incomeAhead map[store.BrokerCur][]readyFlow

// futureIncome — що ще прийде на рахунки і на які саме.
//
// Розкладка по брокерах дзеркалить гаманець (state_builder.go): купон
// кредитує рахунок того брокера, де куплено папір, відсотки вкладу —
// рахунок його банку. Порожня назва стає «—» тим самим правилом, що й у
// cashLedger.byBroker: гроші без привʼязки — це теж місце, і мовчки
// зливати їх із чиїмось рахунком не можна.
//
// Виплати рахує domain.FuturePayments — та сама функція, якою збирається
// календар і зведення. Розділені по брокерах лоти дають розділені потоки:
// сума по всіх брокерах дорівнює загальному розкладу, бо HolderQty
// лінійна за лотами (на це є тест).
//
// arrived фільтрує те, що гаманець УЖЕ порахував балансом. Без цього
// виплата, датована сьогодні й позначена «отримано», лічилась би двічі —
// один раз у балансі, другий як майбутнє надходження.
func (s *Server) futureIncome(src *sources, today domain.Date) (incomeAhead, error) {
	arrived := domain.Arrived(src.statuses, today)
	out := incomeAhead{}
	add := func(broker, currency string, f readyFlow) {
		if broker == "" {
			broker = noBrokerLabel
		}
		k := store.BrokerCur{Broker: broker, Currency: currency}
		out[k] = append(out[k], f)
	}

	byChannel := map[string][]domain.Lot{}
	for _, l := range src.lots {
		byChannel[l.Channel] = append(byChannel[l.Channel], l)
	}
	for channel, lots := range byChannel {
		cfs, err := domain.FuturePayments(src.pays, lots, src.sales, today)
		if err != nil {
			return nil, err
		}
		for _, cf := range cfs {
			if arrived(cf.ISIN, cf.Date) {
				continue
			}
			add(channel, cf.Amount.Currency().Code, readyFlow{
				Date: cf.Date, Amount: cf.Amount.Amount(), Label: cf.ISIN,
			})
		}
	}

	for _, dep := range src.termDeposits {
		// Назву банку в підписі видно саме тут, і це навмисне розходження зі
		// state.payLabel, де стоїть просто «вклад»: там перелік вкладів у
		// функцію не передається, а тут він і є входом.
		label := "вклад"
		if dep.Bank != "" {
			label += " " + dep.Bank
		}
		for _, cf := range domain.DepositSchedule(dep, today) {
			if arrived(cf.ISIN, cf.Date) {
				continue
			}
			add(dep.Bank, cf.Amount.Currency().Code, readyFlow{
				Date: cf.Date, Amount: cf.Amount.Amount(), Label: label,
			})
		}
	}

	for k := range out {
		flows := out[k]
		sort.Slice(flows, func(i, j int) bool {
			if flows[i].Date != flows[j].Date {
				return flows[i].Date < flows[j].Date
			}
			return flows[i].Label < flows[j].Label
		})
		out[k] = coalesceSameDay(flows)
	}
	return out, nil
}

// coalesceSameDay зводить виплати одного паперу одного дня в одну подію.
//
// Купон і погашення приходять окремими рядками розкладу — типи різні, і в
// календарі це правильно. Але на рахунок вони лягають одним приходом того
// самого дня, і в складі суми «UA4000235865 817,50 ₴ + UA4000235865
// 10 000,00 ₴» назва повторюється двічі там, де сталася одна подія.
// Знайдено на живих даних НБУ, а не тестом.
//
// Зведення нічого не міняє в даті: доданки того самого дня в будь-якому
// разі накопичуються разом.
func coalesceSameDay(flows []readyFlow) []readyFlow {
	out := flows[:0:0]
	for _, f := range flows {
		if n := len(out); n > 0 && out[n-1].Date == f.Date && out[n-1].Label == f.Label {
			out[n-1].Amount += f.Amount
			continue
		}
		out = append(out, f)
	}
	return out
}

// readiness — коли й де набереться потрібна сума.
type readiness struct {
	Date   domain.Date
	Broker string
	Via    []readyFlow
}

// readyFor — перший день, коли якийсь із рахунків покриє costMinor.
//
// Обходимо кожного брокера окремо: баланс сьогодні плюс його власні
// надходження по датах. Найраніша дата серед брокерів і є відповіддю; при
// однакових датах виграє менша назва — щоб два запуски на тих самих даних
// не давали різних брокерів (мапа в Go обходиться в довільному порядку).
func (inc incomeAhead) readyFor(doc *state.Doc, currency string, costMinor int64) (readiness, bool) {
	if costMinor <= 0 {
		return readiness{}, false
	}
	brokers := map[string]bool{}
	for name := range doc.Brokers {
		brokers[name] = true
	}
	for k := range inc {
		if k.Currency == currency {
			brokers[k.Broker] = true
		}
	}
	names := make([]string, 0, len(brokers))
	for name := range brokers {
		names = append(names, name)
	}
	sort.Strings(names)

	var best readiness
	found := false
	for _, name := range names {
		bal := brokerBalanceMinor(doc, name, currency)
		var via []readyFlow
		for _, f := range inc[store.BrokerCur{Broker: name, Currency: currency}] {
			bal += f.Amount
			via = append(via, f)
			if bal < costMinor {
				continue
			}
			if !found || f.Date.Before(best.Date) {
				best = readiness{Date: f.Date, Broker: name, Via: via}
				found = true
			}
			break
		}
	}
	return best, found
}

// annotateReady дописує до порад дату доступності й ціну очікування.
//
// Мовчить там, де відповіді немає: рядок, на який стає вже сьогодні, дати
// не отримує (він і так зверху), а рядок, на який із відомих надходжень не
// набереться, отримує названу причину замість порожнечі.
func (s *Server) annotateReady(ctx context.Context, today domain.Date,
	doc *state.Doc, sug []suggestion) error {
	src, err := s.loadSources(ctx, today)
	if err != nil {
		return err
	}
	inc, err := s.futureIncome(src, today)
	if err != nil {
		return err
	}
	for i := range sug {
		if sug[i].CanBuy {
			continue
		}
		cost, cerr := parseMoney(sug[i].CostPerBond.Amount, sug[i].CostPerBond.Currency)
		if cerr != nil || cost.Amount() <= 0 {
			continue
		}
		r, ok := inc.readyFor(doc, sug[i].Currency, cost.Amount())
		if !ok {
			sug[i].ReadyNote = "з відомих надходжень портфеля не набереться"
			continue
		}
		sug[i].ReadyOn = string(r.Date)
		sug[i].ReadyBroker = r.Broker
		sug[i].ReadyDays = domain.DaysBetween(today, r.Date)
		sug[i].ReadyVia = make([]readyEvent, 0, len(r.Via))
		for _, f := range r.Via {
			sug[i].ReadyVia = append(sug[i].ReadyVia, readyEvent{
				Date: string(f.Date), Label: f.Label,
				Amount: toMoneyJSON(money.New(f.Amount, sug[i].Currency)),
			})
		}
		annotateWaitCost(&sug[i], sug, doc)
	}
	return nil
}

// annotateWaitCost — скільки коштує це очікування, міряне альтернативою.
//
// Альтернатива береться НЕ найдохідніша взагалі, а та, яку справді можна
// виконати замість очікування: та сама валюта і той самий рахунок, на
// якому ми чекаємо. Найдохідніший рядок в іншого брокера — не вибір, а
// сусідній рядок таблиці, і міряти ним втрату означало б порахувати
// втраченим те, чого не було.
//
// Працюють не всі гроші рахунку, а стільки, скільки складається в цілу
// кількість кроків альтернативи: решта однаково лежала б без діла, і
// зараховувати їй дохід було б тим самим вигаданим числом, від якого
// застосунок відмовляється всюди.
func annotateWaitCost(row *suggestion, all []suggestion, doc *state.Doc) {
	if row.ReadyDays <= 0 {
		return
	}
	bal := brokerBalanceMinor(doc, row.ReadyBroker, row.Currency)
	if bal <= 0 {
		return
	}
	var alt *suggestion
	var altCost int64
	for i := range all {
		a := &all[i]
		if !a.CanBuy || a.Currency != row.Currency || a.Label == row.Label {
			continue
		}
		fitsHere := false
		for _, f := range a.Brokers {
			if f.Broker == row.ReadyBroker {
				fitsHere = true
				break
			}
		}
		if !fitsHere {
			continue
		}
		c, cerr := parseMoney(a.CostPerBond.Amount, a.CostPerBond.Currency)
		if cerr != nil || c.Amount() <= 0 || c.Amount() > bal {
			continue
		}
		if alt == nil || a.RealPct > alt.RealPct {
			alt, altCost = a, c.Amount()
		}
	}
	if alt == nil {
		return
	}
	working := bal / altCost * altCost
	cost := domain.WaitCost(working, alt.RealPct, row.ReadyDays)
	if cost <= 0 {
		return
	}
	m := toMoneyJSON(money.New(cost, row.Currency))
	row.WaitCost = &m
	row.WaitAlt = alt.Label
}
