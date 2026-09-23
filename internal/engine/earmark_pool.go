// Гроші погашених вкладів подушки й цілей — лишаються подушці й цілі.
//
// Рішення власника (2026-09-23). Доти тіло й відсотки такого вкладу, щойно
// надійшли, лягали на рахунок банку звичайною готівкою: подушка мовчки
// меншала на розмір вкладу, а розкладка пропонувала вкласти ці гроші в
// папери. Тобто гроші, які людина свідомо відклала на аварію чи на авто,
// самі перетікали в портфель у день погашення.
//
// ЧОМУ НЕ СИНТЕТИЧНИЙ РУХ У ЖУРНАЛІ ПОДУШКИ. reserve_ops і goal_ops — це
// «зовнішні гроші» (внесено своїх, XIRR, суперники, прогрес). Погашення
// вкладу — не новий внесок: гроші вже внесені, коли людина поповнила
// рахунок банку. Покласти їх у журнал означало б порахувати той самий
// внесок удруге.
//
// ТОМУ ПУЛ. Для кожної пари «призначення × банк × валюта» — окремий
// лічильник. Виплати (відсотки й тіло) earmarked-вкладів, що надійшли, йдуть
// у пул, а не на рахунок банку. Новий вклад того самого призначення в тому
// самому банку й валюті спершу бере гроші з пулу — так пролонгація подушки
// не вимагає жодного запису, крім самого нового вкладу, — і лише решту
// списує з рахунку. Залишок пулу на сьогодні — готівка подушки чи цілі
// «у банку»: доступна, як гроші з журналу, але не купівельна спроможність.
//
// Гаманець (state_builder.go) і рух грошей (CashEvents) беруть пул з однієї
// функції — інакше звірка гаманців розійшлася б рівно на пул.
package engine

import (
	"sort"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// earmarkKey — призначення (0 — подушка, інакше id цілі), банк і валюта.
type earmarkKey struct {
	goal int64
	bank string
	cur  string
}

func earmarkKeyOf(d domain.Deposit) earmarkKey {
	return earmarkKey{goal: d.GoalID, bank: d.Bank, cur: d.Currency}
}

// earmarkDraw — яке списання вкладу з рахунку: розміщення (topup 0) чи
// поповнення (id поповнення).
type earmarkDraw struct {
	dep, topup int64
}

type earmarkPools struct {
	// left — гроші пулу на сьогодні, мінорні одиниці валюти ключа.
	left map[earmarkKey]int64
	// drawn — скільки з розміщення чи поповнення взято з пулу, а не з
	// рахунку банку.
	drawn map[earmarkDraw]int64
	// lastIn — коли в пул востаннє надійшли гроші: від цієї дати задача
	// «перевклади або лиши» живе обмежений час (state_tasks.go).
	lastIn map[earmarkKey]domain.Date
}

// fromPool — частина списання, яку покрив пул.
func (p earmarkPools) fromPool(dep, topup int64) int64 { return p.drawn[earmarkDraw{dep, topup}] }

// buildEarmarkPools — пул за хронологією подій earmarked-вкладів.
//
// arrived — та сама умова «надійшло», що й у гаманці (дата минула чи
// позначка «Отримано»); дата надходження — ArrivalDate, як у гаманці.
// У один день спершу надходження, потім списання: вклад, погашений і
// перевкладений того самого дня, — це пролонгація, а не нова готівка.
func buildEarmarkPools(deposits []domain.Deposit, arrived func(string, domain.Date) bool,
	today domain.Date) earmarkPools {
	type event struct {
		on     domain.Date
		key    earmarkKey
		amount int64 // + у пул, − списання
		draw   earmarkDraw
	}
	var events []event
	for _, d := range deposits {
		if !d.Earmarked() {
			continue
		}
		k := earmarkKeyOf(d)
		if !d.OpenDate.After(today) {
			events = append(events, event{on: d.OpenDate, key: k, amount: -d.Principal,
				draw: earmarkDraw{dep: d.ID}})
		}
		for _, t := range d.Topups {
			if !t.Date.After(today) {
				events = append(events, event{on: t.Date, key: k, amount: -t.Amount,
					draw: earmarkDraw{dep: d.ID, topup: t.ID}})
			}
		}
		payout := func(cf domain.CashflowItem) {
			if arrived(cf.ISIN, cf.Date) {
				events = append(events, event{on: domain.ArrivalDate(cf.Date, today), key: k,
					amount: cf.Amount.Amount()})
			}
		}
		if d.ClosedDate != "" {
			for _, cf := range d.PaidBeforeClose() {
				payout(cf)
			}
			if !d.ClosedDate.After(today) {
				events = append(events, event{on: d.ClosedDate, key: k, amount: d.ClosedAmount})
			}
			continue
		}
		for _, cf := range domain.DepositSchedule(d, "1970-01-01") {
			payout(cf)
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].on != events[j].on {
			return events[i].on < events[j].on
		}
		return events[i].amount > events[j].amount // надходження раніше за списання
	})
	out := earmarkPools{left: map[earmarkKey]int64{}, drawn: map[earmarkDraw]int64{},
		lastIn: map[earmarkKey]domain.Date{}}
	for _, ev := range events {
		if ev.amount > 0 {
			out.left[ev.key] += ev.amount
			out.lastIn[ev.key] = ev.on
			continue
		}
		take := min(out.left[ev.key], -ev.amount)
		if take > 0 {
			out.left[ev.key] -= take
			out.drawn[ev.draw] += take
		}
	}
	return out
}
