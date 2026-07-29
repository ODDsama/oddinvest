package api

import (
	"testing"

	money "github.com/Rhymond/go-money"
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
	c.add("mono", money.UAH, 500_000)
	c.add("mono", money.USD, 30_000)
	c.add("inzhur", money.UAH, 120_000)
	c.add("mono", money.UAH, -75_000) // те саме місце ще раз: має додатись
	c.add("", money.UAH, 9_900)       // гроші без брокера — теж гроші
	c.add("ПУМБ", money.UAH, -200_000)

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
	c.add("mono", money.UAH, 100_000)
	c.add("", money.UAH, 5_000)

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
