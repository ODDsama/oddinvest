package store

import (
	"context"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Відновлення не зносить фонд, який ще щось тримає.
//
// pruneOrphanFundsIn прибирав БУДЬ-ЯКИЙ фонд без операцій — разом із
// позначками ціни, що вводяться руками. Фонд, позначки якого завели
// наперед (крива, ціна для конвертації), або який стоїть у плані купівель
// сусіднього портфеля, зникав від відновлення зовсім іншого портфеля.
func TestRestoreKeepsFundsWithMarks(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	op, err := st.AddFundOp(ctx, domain.FundOp{Date: "2026-01-10", Fund: "Inzhur Ocean",
		Kind: domain.FundBuy, Qty: 1, Amount: 100000, Currency: "UAH", Broker: "inzhur"})
	if err != nil {
		t.Fatal(err)
	}
	funds, _ := st.ListFunds(ctx)
	var fundID int64
	for _, f := range funds {
		if f.Name == "Inzhur Ocean" {
			fundID = f.ID
		}
	}
	if err := st.DeleteFundOp(ctx, op); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFundPricePoints(ctx, fundID, []domain.FundPrice{{Date: "2026-02-01", Price: 1_000_000}}); err != nil {
		t.Fatal(err)
	}
	empty := &Backup{Schema: BackupSchema, App: "oddinvest", Migration: "9999"}
	if err := st.ImportAll(ctx, empty); err != nil {
		t.Fatal(err)
	}
	marks, err := st.ListFundPrices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(marks) != 1 {
		t.Errorf("позначка ціни фонду без операцій зникла від відновлення: %+v", marks)
	}
}
