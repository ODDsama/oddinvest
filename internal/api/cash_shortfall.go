// Чи вистачить грошей на цю операцію — і скільки бракує, якщо ні.
//
// Одне питання — одна відповідь. Доти нестачу рахував лише кошик
// (handlers_whatif.go), просто в циклі; щойно те саме питання постало на
// формі покупки, копія цього виразу опинилась би в JS. Чим це
// закінчується, розказано в шапці handlers_whatif.go: двічі в цьому
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
// людиною. Те саме правило, що й у кошика (handlers_whatif.go:85-88):
// нестача нічого не блокує, гроші могли ще не прийти.
package api

import (
	"math"
	"net/http"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
)

// brokerBalanceMinor — баланс рахунку broker×currency у МІНОРНИХ одиницях.
//
// state.Doc тримає гаманець мажорними float64 (так його бачить UI і так
// описує contract/oddinvest-state.schema.json), тож зворотний переклад
// неминучий. Саме math.Round, а не int64(v*100+0.5): другий вираз
// обрізає до нуля, тобто на ВІД'ЄМНОМУ балансі дає −99999 замість
// −100000 — на копійку менше боргу. А від'ємний баланс тут не крайній
// випадок, а рівно той стан, під який цей файл і пишеться: після
// «поповнити рівно на нестачу» рахунок мусить стати 0, а не −0.01.
func brokerBalanceMinor(doc *state.Doc, broker, currency string) int64 {
	byCur, ok := doc.Brokers[broker]
	if !ok {
		return 0
	}
	return int64(math.Round(byCur[currency] * 100))
}

// shortfallMinor — скільки НЕ ВИСТАЧАЄ рахунку broker×currency, щоб
// витратити want. Нуль означає «вистачає».
func shortfallMinor(doc *state.Doc, broker, currency string, want int64) int64 {
	have := brokerBalanceMinor(doc, broker, currency)
	if want <= have {
		return 0
	}
	return want - have
}

// cashDebit — що саме списується з рахунку і звідки. Переклад операції
// (лот, вклад, поповнення вкладу) в один спільний знаменник.
type cashDebit struct {
	Broker   string
	Currency string
	Amount   int64 // мінорні, > 0
}

// lotDebit — списання під купівлю лота. Вартість рахує domain.LotCost —
// та сама функція, якою потім віднімає гроші гаманець
// (state_builder.go). Доки формула одна, «перевірили» й «списали»
// розійтись не можуть.
func lotDebit(l domain.Lot) (cashDebit, error) {
	cost, err := domain.LotCost(l)
	if err != nil {
		return cashDebit{}, err
	}
	return cashDebit{Broker: l.Channel, Currency: cost.Currency().Code,
		Amount: cost.Amount()}, nil
}

// cashCheckResp — відповідь усіх трьох /check.
//
// Broker повертає сервер, хоч у двох випадках його ж і прислали: для
// поповнення вкладу банк відомий лише серверу (він у самому вкладі), і
// фронтенд не мусить збирати тіло автопоповнення з двох джерел. Значення
// СИРЕ, зокрема порожнє — показувати порожнє як «—» можна, а класти «—» у
// тіло поповнення не можна: store.AddDeposit заводить брокера за назвою
// (store/refs.go:25), і в довіднику з'явився б брокер на ім'я «—».
type cashCheckResp struct {
	Broker string    `json:"broker"`
	Cost   moneyJSON `json:"cost"`
	Have   moneyJSON `json:"have"` // може бути від'ємним
	Short  moneyJSON `json:"short"`
	Enough bool      `json:"enough"`
}

// writeCashCheck — спільне тіло трьох хендлерів /check. Нічого не пише.
//
// Баланс береться на СЬОГОДНІ — те саме число, яке людина бачить на
// екрані. Для операції, датованої майбутнім, це може виявитись не тим
// балансом, який буде на її дату; свідомо не ускладнюємо, бо це та сама
// позиція, що вже записана в handlers_whatif.go:85-88 — застосунок
// показує наслідки, рішення за людиною.
func (s *Server) writeCashCheck(w http.ResponseWriter, r *http.Request, d cashDebit) {
	doc, err := s.buildState(r.Context(), time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	short := shortfallMinor(doc, d.Broker, d.Currency, d.Amount)
	writeJSON(w, http.StatusOK, cashCheckResp{
		Broker: d.Broker,
		Cost:   toMoneyJSON(money.New(d.Amount, d.Currency)),
		Have:   toMoneyJSON(money.New(brokerBalanceMinor(doc, d.Broker, d.Currency), d.Currency)),
		Short:  toMoneyJSON(money.New(short, d.Currency)),
		Enough: short == 0,
	})
}
