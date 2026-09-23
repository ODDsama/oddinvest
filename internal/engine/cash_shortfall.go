// Чи вистачить грошей на цю операцію — і скільки бракує, якщо ні.
//
// Одне питання — одна відповідь. Доти нестачу рахував лише кошик
// (api/handlers_whatif.go), просто в циклі; щойно те саме питання постало на
// формі покупки, копія цього виразу опинилась би в JS. Чим це
// закінчується, розказано в шапці api/handlers_whatif.go: двічі в цьому
// застосунку друга копія арифметики давала два різні числа на одному
// екрані.
//
// Тому рахує сервер, а фронтенд отримує ГОТОВИЙ рядок суми і передає його
// далі дослівно — не множачи й не віднімаючи нічого сам.
//
// Ендпоїнти /check приймають ТЕ САМЕ тіло, що й відповідний запис, і
// розбирають його ТИМИ САМИМИ функціями (lotFromReq, termDepositFromReq,
// topupFromReq). Це і є гарантія, що перевірили саме те, що потім
// запишуть: інакше «перевірка» лишалась би схожою на правду, а схожа на
// правду відповідь про гроші гірша за жодної.
//
// Записом тут не пахне: /check відповідає на питання, а рішення — за
// людиною. Те саме правило, що й у кошика (api/handlers_whatif.go:85-88):
// нестача нічого не блокує, гроші могли ще не прийти.
package engine

import (
	"math"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
)

// BrokerBalanceMinor — баланс рахунку broker×currency у МІНОРНИХ одиницях.
//
// state.Doc тримає гаманець мажорними float64 (так його бачить UI і так
// описує contract/oddinvest-state.schema.json), тож зворотний переклад
// неминучий. Саме math.Round, а не int64(v*100+0.5): другий вираз
// обрізає до нуля, тобто на ВІД'ЄМНОМУ балансі дає −99999 замість
// −100000 — на копійку менше боргу. А від'ємний баланс тут не крайній
// випадок, а рівно той стан, під який цей файл і пишеться: після
// «поповнити рівно на нестачу» рахунок мусить стати 0, а не −0.01.
func BrokerBalanceMinor(doc *state.Doc, broker, currency string) int64 {
	byCur, ok := doc.Brokers[broker]
	if !ok {
		return 0
	}
	return int64(math.Round(byCur[currency].Major() * 100))
}

// ShortfallMinor — скільки НЕ ВИСТАЧАЄ рахунку broker×currency, щоб
// витратити want. Нуль означає «вистачає».
func ShortfallMinor(doc *state.Doc, broker, currency string, want int64) int64 {
	have := BrokerBalanceMinor(doc, broker, currency)
	if want <= have {
		return 0
	}
	return want - have
}

// CashDebit — що саме списується з рахунку і звідки. Переклад операції
// (лот, вклад, поповнення вкладу) в один спільний знаменник.
type CashDebit struct {
	Broker   string
	Currency string
	Amount   int64 // мінорні, > 0
}

// LotDebit — списання під купівлю лота. Вартість рахує domain.LotCost —
// та сама функція, якою потім віднімає гроші гаманець
// (state_builder.go). Доки формула одна, «перевірили» й «списали»
// розійтись не можуть.
func LotDebit(l domain.Lot) (CashDebit, error) {
	cost, err := domain.LotCost(l)
	if err != nil {
		return CashDebit{}, err
	}
	return CashDebit{Broker: l.Channel, Currency: cost.Currency().Code,
		Amount: cost.Amount()}, nil
}
