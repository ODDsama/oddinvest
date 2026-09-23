package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

// testStore — сховище в тимчасовому каталозі, закривається разом із тестом.
// Двійник api.testServer без HTTP: тестам розрахунку сервер не потрібен.
func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func seed(t *testing.T, st *store.Store) {
	t.Helper()
	secs := []nbu.Security{{
		Bond: domain.Bond{ISIN: "UA4000227748", Nominal: money.New(100000, money.UAH),
			RateBP: 1655, Maturity: "2027-03-17", Descr: "гривневі військові"},
		Payments: []domain.Payment{
			{ISIN: "UA4000227748", PayDate: "2026-09-16", Type: domain.PayCoupon, PerBond: money.New(8275, money.UAH)},
			{ISIN: "UA4000227748", PayDate: "2027-03-17", Type: domain.PayCoupon, PerBond: money.New(8275, money.UAH)},
			{ISIN: "UA4000227748", PayDate: "2027-03-17", Type: domain.PayRedemption, PerBond: money.New(100000, money.UAH)},
		},
	}}
	if err := st.ReplaceDirectory(context.Background(), secs, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRate(context.Background(), "USD", 441234, "2026-07-15"); err != nil {
		t.Fatal(err)
	}
}
