package store

import (
	"context"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Правка ноги конвертації не рве пару.
//
// PUT замінює операцію цілком, а форма правки про пару не знає й
// pair_id не шле. Доти UpdateFundOp писав його з тіла — тобто NULL, — і
// виправлена одруківка в податку перетворювала конвертацію на справжній
// продаж: з'являвся реалізований прибуток, податок і інший XIRR. Пару
// ставить лише LinkFundOps; правка її не чіпає.
func TestUpdateFundOpKeepsPair(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	sell, err := st.AddFundOp(ctx, domain.FundOp{Date: "2026-04-02", Fund: "Inzhur Житній",
		Kind: domain.FundSell, Qty: 3, Amount: 919300, Currency: "UAH", Broker: "inzhur"})
	if err != nil {
		t.Fatal(err)
	}
	buy, err := st.AddFundOp(ctx, domain.FundOp{Date: "2026-04-02", Fund: "Inzhur Ocean",
		Kind: domain.FundBuy, Qty: 2, Amount: 919300, Currency: "UAH", Broker: "inzhur"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.LinkFundOps(ctx, sell, buy); err != nil {
		t.Fatal(err)
	}
	// Правка з форми: pair_id у тілі немає.
	if err := st.UpdateFundOp(ctx, domain.FundOp{ID: buy, Date: "2026-04-02", Fund: "Inzhur Ocean",
		Kind: domain.FundBuy, Qty: 2, Amount: 919300, Tax: 100, Currency: "UAH", Broker: "inzhur",
		Note: "виправив"}); err != nil {
		t.Fatal(err)
	}
	ops, err := st.ListFundOps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pairs := map[int64]int64{}
	for _, o := range ops {
		pairs[o.ID] = o.PairID
	}
	if pairs[buy] != sell || pairs[sell] != buy {
		t.Errorf("пара мала лишитись %d↔%d, а стало %v", sell, buy, pairs)
	}
}
