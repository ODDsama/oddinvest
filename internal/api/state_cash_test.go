package api

import (
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// TestCashLedgerCurrencyTotalsMatchBrokers — інваріант, заради якого
// другий акумулятор і прибрано: «Разом» по валюті дорівнює сумі по
// брокерах у цій валюті.
//
// Доти це трималось на тому, що дев'ять місць дописували два рядки
// замість одного. Тепер тримається на тому, що зведення ВИВОДИТЬСЯ, —
// але перевірити треба саме виведення, бо зіпсувати його можна й тут.
//
// Гроші без брокера сюди входять нарівні з рештою. У golden такого руху
// немає (усі поповнення фікстури мають брокера), тож без цього тесту
// «зведення тихо пропускає безіменного» проходило б непоміченим — саме
// це й показала мутаційна перевірка.
func TestCashLedgerCurrencyTotalsMatchBrokers(t *testing.T) {
	c := newCashLedger()
	c.add("mono", money.UAH, "2026-01-01", 500_000)
	c.add("mono", money.USD, "2026-01-02", 30_000)
	c.add("inzhur", money.UAH, "2026-01-03", 120_000)
	c.add("mono", money.UAH, "2026-01-04", -75_000) // те саме місце ще раз: має додатись
	c.add("", money.UAH, "2026-01-05", 9_900)       // гроші без брокера — теж гроші
	c.add("ПУМБ", money.UAH, "2026-01-06", -200_000)

	want := map[string]int64{
		money.UAH: 500_000 + 120_000 - 75_000 + 9_900 - 200_000,
		money.USD: 30_000,
	}
	got := c.byCurrency()
	if len(got) != len(want) {
		t.Fatalf("валют у зведенні %d, очікували %d: %v", len(got), len(want), got)
	}
	for cur, w := range want {
		if got[cur] != w {
			t.Errorf("%s: зведення %d, очікували %d", cur, got[cur], w)
		}
	}

	// Той самий інваріант, але порахований з іншого боку — щоб тест не
	// повторював арифметику, яку перевіряє.
	sum := map[string]int64{}
	for k, v := range c.byBC {
		sum[k.Currency] += v
	}
	for cur, v := range sum {
		if got[cur] != v {
			t.Errorf("%s: зведення %d розійшлось із сумою по брокерах %d", cur, got[cur], v)
		}
	}
}

// TestCashLedgerBrokerNamesUnnamedStaysApart — брокер без назви показується
// як «—» і НЕ зливається з чиїмось рахунком.
//
// Злити його з довільним брокером означало б показати гроші там, де їх
// немає, а помічник реінвесту саме за цим числом вирішує, чи вистачає на
// папір.
func TestCashLedgerBrokerNamesUnnamedStaysApart(t *testing.T) {
	c := newCashLedger()
	c.add("mono", money.UAH, "2026-01-01", 100_000)
	c.add("", money.UAH, "2026-01-01", 5_000)

	b := c.byBroker()
	if b["mono"][money.UAH] != 1000 {
		t.Errorf("mono: %v, очікували 1000.00", b["mono"][money.UAH])
	}
	if b["—"][money.UAH] != 50 {
		t.Errorf("безіменний: %v, очікували 50.00", b["—"][money.UAH])
	}
	if _, ok := b[""]; ok {
		t.Error("порожня назва брокера дійшла до UI як порожня")
	}
}

// TestCashLedgerLotsAreFIFOPerPair — журнал подій пари дає уцілілі
// надходження за правилом FIFO, і пари між собою не змішуються: купівля в
// mono не з'їдає поповнення в inzhur, хай яке воно старе.
func TestCashLedgerLotsAreFIFOPerPair(t *testing.T) {
	c := newCashLedger()
	c.add("inzhur", money.UAH, "2026-01-01", 100_000)
	c.add("mono", money.UAH, "2026-02-01", 300_000)
	c.add("mono", money.UAH, "2026-03-01", 200_000)
	c.add("mono", money.UAH, "2026-03-15", -350_000) // зʼїдає лютневе цілком і 50 000 березневого

	mono := c.lots(store.BrokerCur{Broker: "mono", Currency: money.UAH})
	if len(mono) != 1 || mono[0] != (domain.CashEvent{Date: "2026-03-01", Amount: 150_000}) {
		t.Fatalf("mono: лишки %v, чекали [{2026-03-01 150000}]", mono)
	}
	inz := c.lots(store.BrokerCur{Broker: "inzhur", Currency: money.UAH})
	if len(inz) != 1 || inz[0].Amount != 100_000 {
		t.Fatalf("inzhur: купівля в mono не мала чіпати цих грошей: %v", inz)
	}
	// Сума уцілілих ніколи не менша за баланс — інакше вік простою було б
	// нізвідки взяти.
	if got := c.byBC[store.BrokerCur{Broker: "mono", Currency: money.UAH}]; got != 150_000 {
		t.Fatalf("баланс mono %d, чекали 150000", got)
	}
}
