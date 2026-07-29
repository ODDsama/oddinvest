// Гаманець портфеля — скільки грошей і де саме лежить.
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
// УВАГА: ті самі величини рахує ще cashEvents у cashflow.go — там та сама
// арифметика, але розкладена на окремі події. Дві реалізації мусять
// сходитись, і єдиний захист від їх розходження — тест
// TestCashflowStatementReconciles. Міняєш тут — дивись і туди.
package api

import "github.com/ODDsama/oddinvest/internal/store"

// cashLedger — гроші на рахунках у МІНОРНИХ одиницях, нативно.
//
// Рахунки роздільні навмисно: гривня в mono не купить папір в inzhur, і
// помічник, який цього не бачить, радить покупку за гроші, яких у тому
// брокері немає.
type cashLedger struct {
	byBC map[store.BrokerCur]int64
}

func newCashLedger() *cashLedger {
	return &cashLedger{byBC: map[store.BrokerCur]int64{}}
}

// add рухає рахунок брокера в цій валюті на minor (може бути від'ємним).
func (c *cashLedger) add(broker, currency string, minor int64) {
	c.byBC[store.BrokerCur{Broker: broker, Currency: currency}] += minor
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
			name = "—"
		}
		if out[name] == nil {
			out[name] = map[string]float64{}
		}
		out[name][k.Currency] = float64(m) / 100
	}
	return out
}
