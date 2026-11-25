package store

import (
	"context"
	"testing"
)

// Зіставлення брокера з джерелом цін переживає бекап і відновлення.
func TestBackupKeepsBrokerQuoteSource(t *testing.T) {
	src := openTest(t)
	ctx := context.Background()
	id, err := src.AddBroker(ctx, "mono")
	if err != nil {
		t.Fatal(err)
	}
	if err := src.SetBrokerQuoteSource(ctx, id, "mono-bank"); err != nil {
		t.Fatal(err)
	}
	if err := src.SetBrokerEntry(ctx, id, BrokerEntryStatement); err != nil {
		t.Fatal(err)
	}
	dump, err := src.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dst := openTest(t)
	if err := dst.ImportAll(ctx, dump); err != nil {
		t.Fatal(err)
	}
	brokers, err := dst.ListBrokers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(brokers) != 1 || brokers[0].QuoteSource != "mono-bank" {
		t.Errorf("після відновлення %+v, чекали mono ↔ mono-bank", brokers)
	}
	// Те саме для входу операцій (0072): позначка «виписка» мусить пережити
	// відновлення, інакше воно відкрило б ручний запис на такого брокера.
	if len(brokers) != 1 || brokers[0].Entry != BrokerEntryStatement {
		t.Errorf("після відновлення %+v, чекали entry=%q", brokers, BrokerEntryStatement)
	}
}
