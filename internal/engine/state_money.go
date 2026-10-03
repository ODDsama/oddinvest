// Зовнішні гроші портфеля — одне означення на всіх, хто про них питає.
//
// Питання «які гроші зайшли ЗЗОВНІ, а які просто переклались усередині»
// стоїть у кількох місцях: «внесено за місяць» і фактичний темп
// (state_month.go), рух капіталу за 30 днів (state_delta.go), бенчмарк
// «Усі гроші» (rivals.go), крива «Внесено» (snapshots.go, plan_timeline.go).
// Доти склад журналів був переписаний у кожному з них руками — і
// розійшовся в трьох напрямках одночасно.
//
// СКЛАД — РУХ НА МЕЖІ ІНСТРУМЕНТА + РЕЗЕРВ + ЦІЛІ.
//
// Рахунків застосунок більше не веде (ревізія 2026-10-03), тож межею
// портфеля став сам інструмент: покупка — гроші зайшли, виплата чи вихід —
// вийшли (state_flows.go, де й довід, чому XIRR від цього не змінюється).
// Поза інструментами в капіталі (state.Capital) лишаються рівно два
// кошики — резерв і цілі, і їхні журнали — це гроші, які людина відклала
// або забрала сама.
//
// Тут лише СКЛАД і порядок, без вікна, без курсу: що з цими рухами робити —
// питання кожного читача окремо. Темп бере нето за 183 дні, «внесено» — за
// календарний місяць, дельта — від дати знімка.
package engine

import "github.com/ODDsama/oddinvest/internal/domain"

// moneyMove — один рух зовнішніх грошей: + зайшли, − вийшли.
//
// Нативна валюта, мінорні одиниці: переводить у гривню читач, і кожен своїм
// курсом (темп і дельта — сьогоднішнім, бенчмарк — курсом дня руху).
type moneyMove struct {
	Date     domain.Date
	Amount   int64
	Currency string
}

// flowInputsOf — вхід журналу інструментів із уже прочитаних джерел.
func flowInputsOf(src *sources, today domain.Date) flowInputs {
	arrived := domain.Arrived(src.statuses, today)
	return flowInputs{
		lots: src.lots, sales: src.sales, pays: src.pays, arrived: arrived,
		fundOps: src.fundOps, npfAccounts: src.npfAccounts, npfOps: src.npfOps,
		deposits: src.termDeposits,
		pools:    buildEarmarkPools(src.termDeposits, arrived, today),
		today:    today,
	}
}

// instrumentMoves — рух на межі інструмента як зовнішні гроші портфеля:
// той самий журнал, що в state_flows.go, із протилежним знаком.
func instrumentMoves(flows []instrFlow) []moneyMove {
	out := make([]moneyMove, 0, len(flows))
	for _, f := range flows {
		out = append(out, moneyMove{Date: f.Date, Amount: -f.Amount, Currency: f.Currency})
	}
	return out
}

// externalMoves — усі рухи зовнішніх грошей портфеля: інструменти, резерв,
// цілі. Порядок сталий заради тестів, які звіряють списки; жоден читач на
// нього не спирається.
func externalMoves(src *sources, today domain.Date) ([]moneyMove, error) {
	flows, err := instrumentFlows(flowInputsOf(src, today))
	if err != nil {
		return nil, err
	}
	return externalMovesFrom(flows, src), nil
}

// externalMovesFrom — те саме з уже зібраного журналу інструментів: збирач
// стану будує журнал раз і віддає його кільком фазам.
func externalMovesFrom(flows []instrFlow, src *sources) []moneyMove {
	out := instrumentMoves(flows)
	for _, op := range src.reserveOps {
		out = append(out, moneyMove{Date: op.Date, Amount: op.Amount, Currency: op.Currency})
	}
	for _, op := range src.goalOps {
		out = append(out, moneyMove{Date: op.Date, Amount: op.Amount, Currency: op.Currency})
	}
	return out
}
