package store

import (
	"context"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Видалення ноги конвертації забирає обидві ноги, а не падає на FK.
//
// Ноги показують одна на одну (LinkFundOps), зовнішній ключ увімкнено, і
// DELETE однієї доти падав «FOREIGN KEY constraint failed» — у UI це 500,
// і помилкову конвертацію не можна було прибрати зовсім. Рішення
// власника (2026-09-24): конвертація — одна подія, тож і зникає цілою.
func TestDeleteFundConversionLegRemovesBoth(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	sell, err := st.AddFundOp(ctx, domain.FundOp{Date: "2026-04-02", Fund: "Житній",
		Kind: domain.FundSell, Qty: 3, Amount: 919300, Currency: "UAH", Broker: "inzhur"})
	if err != nil {
		t.Fatal(err)
	}
	buy, err := st.AddFundOp(ctx, domain.FundOp{Date: "2026-04-02", Fund: "Ocean",
		Kind: domain.FundBuy, Qty: 2, Amount: 919300, Currency: "UAH", Broker: "inzhur"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkFundOps(ctx, sell, buy); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteFundOp(ctx, buy); err != nil {
		t.Fatalf("видалення ноги конвертації: %v", err)
	}
	ops, err := st.ListFundOps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("лишилось %d операцій, чекали 0 — конвертація зникає цілою", len(ops))
	}
}

// Зняття-позику з поверненнями можна видалити: повернення лишаються
// звичайними поповненнями подушки, без висячого loan_id.
func TestDeleteReserveLoanOpWithRepayments(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	take, err := st.AddReserveOp(ctx, ReserveOp{Date: "2026-02-01", Amount: -5_000_00, Currency: "UAH"})
	if err != nil {
		t.Fatal(err)
	}
	loan, err := st.AddReserveLoan(ctx, ReserveLoan{OpID: take, RateBP: 1200})
	if err != nil {
		t.Fatal(err)
	}
	back, err := st.AddReserveOp(ctx, ReserveOp{Date: "2026-03-01", Amount: 1_000_00, Currency: "UAH", LoanID: loan})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteReserveOp(ctx, take); err != nil {
		t.Fatalf("видалення зняття-позики: %v", err)
	}
	ops, err := st.ListReserveOps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].ID != back || ops[0].LoanID != 0 {
		t.Errorf("мало лишитись одне повернення без позики: %+v", ops)
	}
}
