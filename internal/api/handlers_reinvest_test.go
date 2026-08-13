package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// reinvestFund — заводить один накопичувальний фонд, дає довіднику
// правити його через mutate й повертає поради виду «fund».
func reinvestFund(t *testing.T, mutate func(*store.Fund)) []struct {
	Kind       string  `json:"kind"`
	Label      string  `json:"label"`
	Reason     string  `json:"reason"`
	NominalPct float64 `json:"nominal_pct"`
	YieldBasis string  `json:"yield_basis"`
} {
	t.Helper()
	ctx := context.Background()
	srv, st := testServer(t)
	seed(t, st)
	// Гроші на рахунку, інакше порад не буде взагалі.
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: domain.NewDate(time.Now()), Amount: 100_000_00,
		Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: domain.NewDate(time.Now().AddDate(0, 0, -30)), Fund: "MilTech",
		Kind: domain.FundBuy, Qty: 5, Amount: 500_000, Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	funds, err := st.ListFunds(ctx)
	if err != nil || len(funds) != 1 {
		t.Fatalf("очікували один фонд: %v %d", err, len(funds))
	}
	f := funds[0]
	f.ExpectedYieldBP, f.ExpectedYieldCur = 2500, money.UAH
	f.Kind, f.CloseDate = store.FundAccumulating, "2029-07-26"
	mutate(&f)
	if err := st.RenameFund(ctx, f.ID, f); err != nil {
		t.Fatal(err)
	}

	resp, body := do(t, "GET", srv.URL+"/api/reinvest", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET дав %d: %s", resp.StatusCode, body)
	}
	var all []struct {
		Kind       string  `json:"kind"`
		Label      string  `json:"label"`
		Reason     string  `json:"reason"`
		NominalPct float64 `json:"nominal_pct"`
		YieldBasis string  `json:"yield_basis"`
	}
	if err := json.Unmarshal([]byte(body), &all); err != nil {
		t.Fatalf("розбір: %v (%s)", err, body)
	}
	out := all[:0]
	for _, s := range all {
		if s.Kind == "fund" {
			out = append(out, s)
		}
	}
	return out
}

// TestReinvestSkipsFundAfterBuyUntil — фонд із закритим вікном залучення
// не потрапляє в поради, а рядок про строк не бреше.
//
// Inzhur MilTech приймає гроші до 31.12.2026 і після цього не приймає
// нікого. Порада купити його в 2027-му — це порада зробити неможливе, і
// вона гірша за відсутність поради.
func TestReinvestSkipsFundAfterBuyUntil(t *testing.T) {
	open := reinvestFund(t, func(f *store.Fund) { f.BuyUntil = "2099-12-31" })
	if len(open) != 1 {
		t.Fatalf("фонд із відкритим вікном мав потрапити в поради, маємо %d", len(open))
	}
	// Рядок про строк мусить бути правдивим: фонд із датою закриття не
	// «безстроковий». Саме це речення й було неправдою на екрані.
	if strings.Contains(open[0].Reason, "без строку й погашення") {
		t.Errorf("порада про строковий фонд каже «без строку й погашення»: %s", open[0].Reason)
	}
	if !strings.Contains(open[0].Reason, "2029-07-26") {
		t.Errorf("порада мовчить про дату закриття: %s", open[0].Reason)
	}
	if !strings.Contains(open[0].Reason, "2099-12-31") {
		t.Errorf("порада мовчить про останню дату купівлі: %s", open[0].Reason)
	}

	closed := reinvestFund(t, func(f *store.Fund) { f.BuyUntil = "2020-01-01" })
	if len(closed) != 0 {
		t.Errorf("фонд із закритим вікном лишився в порадах: %+v", closed)
	}
}

// TestReinvestComparesFundNetOfTax — обіцянка фонду йде в порівняння
// після податку, як і ставка вкладу поруч.
//
// Купон ОВДП від податку звільнений, ставка вкладу в цьому ж списку вже
// нетто («ставка вкладу після податку»), а обіцянка фонду йшла брутто —
// і в спільній таблиці фонд систематично виглядав кращим, ніж він є.
func TestReinvestComparesFundNetOfTax(t *testing.T) {
	gross := reinvestFund(t, func(f *store.Fund) { f.BuyUntil = "2099-12-31" })
	net := reinvestFund(t, func(f *store.Fund) {
		f.BuyUntil, f.IncomeTaxBP = "2099-12-31", 1400
	})
	if len(gross) != 1 || len(net) != 1 {
		t.Fatalf("очікували по одній пораді: %d / %d", len(gross), len(net))
	}
	if gross[0].NominalPct != 25 {
		t.Fatalf("без податку мало лишитись 25, маємо %v", gross[0].NominalPct)
	}
	if net[0].NominalPct >= gross[0].NominalPct {
		t.Errorf("з податком 14%% ставка %v не нижча за брутто %v",
			net[0].NominalPct, gross[0].NominalPct)
	}
	// Основа каже вголос, що число вже після податку — інакше поруч із
	// «до погашення» воно читалось би як така сама брутто-величина.
	if !strings.Contains(net[0].YieldBasis, "після податку") {
		t.Errorf("основа %q мовчить про податок", net[0].YieldBasis)
	}
}
