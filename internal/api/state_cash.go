// Гаманець портфеля — скільки грошей і де саме лежить, і з якого дня.
//
// Третя фаза розбиття buildState. Доти тут стояли ДВА акумулятори: bal
// (валюта → сума) і balBC (брокер×валюта → сума), і дев'ять місць
// оновлювали їх рядок у рядок:
//
//	bal[dep.Currency] -= dep.Principal
//	balBC[bc] -= dep.Principal
//
// Другий рядок у такій парі — не механізм, а звичка. Достатньо раз його
// не дописати, і «Разом» розійдеться з сумою по брокерах, причому мовчки:
// обидва числа лишаються правдоподібними, просто одне з них неправда.
//
// Тепер акумулятор один — розріз по (брокер × валюта), — а зведення по
// валютах із нього ВИВОДИТЬСЯ. Забути другий рядок більше нема де, бо
// другого рядка немає.
//
// ПОДІЇ, А НЕ ЛИШЕ СУМИ. Кожен рух приходить із датою й лишається в журналі
// пари: баланс — це Σ подій, а «з якого дня ці гроші лежать» — уцілілі
// надходження за правилом FIFO з domain.RemainingInflows (тим самим, що
// рахує «дохід без діла»). Історії балансів у застосунку немає (знімок несе
// один сумарний account_uah), і заводити її не треба: журнал подій уже все
// знає. Ціна — два підсумовані читання сховища (DepositsByBrokerCurrency,
// ConversionsNetByBroker) замінено списками з датами; суми ті самі.
//
// УВАГА: ті самі величини рахує ще cashEvents у cashflow.go — там та сама
// арифметика, але розкладена на окремі події. Дві реалізації мусять
// сходитись, і єдиний захист від їх розходження — тест
// TestCashflowStatementReconciles. Міняєш тут — дивись і туди.
package api

import (
	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// cashLedger — гроші на рахунках у МІНОРНИХ одиницях, нативно.
//
// Рахунки роздільні навмисно: гривня в mono не купить папір в inzhur, і
// помічник, який цього не бачить, радить покупку за гроші, яких у тому
// брокері немає.
type cashLedger struct {
	byBC   map[store.BrokerCur]int64
	events map[store.BrokerCur][]domain.CashEvent
}

func newCashLedger() *cashLedger {
	return &cashLedger{
		byBC:   map[store.BrokerCur]int64{},
		events: map[store.BrokerCur][]domain.CashEvent{},
	}
}

// add рухає рахунок брокера в цій валюті на minor (може бути від'ємним)
// датою on. Сума й подія пишуться разом — інакше баланс і вік знову стали б
// двома акумуляторами, які треба не забути оновити обидва.
func (c *cashLedger) add(broker, currency string, on domain.Date, minor int64) {
	k := store.BrokerCur{Broker: broker, Currency: currency}
	c.byBC[k] += minor
	c.events[k] = append(c.events[k], domain.CashEvent{Date: on, Amount: minor})
}

// lots — уцілілі надходження пари з датами, найстаріше першим.
func (c *cashLedger) lots(k store.BrokerCur) []domain.CashEvent {
	return domain.RemainingInflows(c.events[k])
}

// byCurrency — зведення по валютах, Σ по всіх брокерах.
//
// Порядок обходу мапи тут не має значення, і це не випадковість, на яку
// покладаються: суми цілочисельні, а додавання цілих асоціативне. Саме
// тому гаманець тримається в мінорних одиницях аж до самого кінця — з
// float64 такий обхід давав би різні останні біти від запуску до запуску
// (див. експозицію брокерів у state_builder.go).
func (c *cashLedger) byCurrency() map[string]int64 {
	out := make(map[string]int64, len(c.byBC))
	for k, v := range c.byBC {
		out[k.Currency] += v
	}
	return out
}

// byBroker — брокер → валюта → сума в МАЖОРНИХ одиницях, для UI і для
// перевірки «чи вистачає на папір». Порожня назва брокера показується як
// «—»: гроші без прив'язки — це теж місце, і мовчки зливати їх із чиїмось
// рахунком не можна.
func (c *cashLedger) byBroker() map[string]map[string]float64 {
	out := map[string]map[string]float64{}
	for k, m := range c.byBC {
		name := k.Broker
		if name == "" {
			name = noBrokerLabel
		}
		if out[name] == nil {
			out[name] = map[string]float64{}
		}
		out[name][k.Currency] = float64(m) / 100
	}
	return out
}
