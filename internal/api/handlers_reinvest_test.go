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
	"github.com/ODDsama/oddinvest/internal/nbu"
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

// Довідник НБУ тримає майже дві сотні паперів і показує їх однаково
// купованими. Насправді на аукціонах за рік буває три десятки: решту
// взяти можна хіба на вторинному ринку й за ціною, якої застосунок не
// знає, — а ціна в рядку поради рахується саме за номіналом, тобто за
// первинним розміщенням.
//
// Несвіжий папір тут НАВМИСНО вигідніший за свіжий (16.55% проти 14%):
// без опускання він стояв би зверху, тож тест стереже саме його.
func TestReinvestDemotesPapersNotPlacedInAYear(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)
	const fresh, stale = "UA4000239016", "UA4000227748"
	bond := func(isin string, rateBP int64, coupon int64) nbu.Security {
		return nbu.Security{
			Bond: domain.Bond{ISIN: isin, Nominal: money.New(100000, money.UAH),
				RateBP: rateBP, Maturity: "2028-03-17"},
			Payments: []domain.Payment{
				{ISIN: isin, PayDate: "2028-03-17", Type: domain.PayCoupon, PerBond: money.New(coupon, money.UAH)},
				{ISIN: isin, PayDate: "2028-03-17", Type: domain.PayRedemption, PerBond: money.New(100000, money.UAH)},
			},
		}
	}
	if err := st.ReplaceDirectory(ctx, []nbu.Security{
		bond(fresh, 1400, 8000), bond(stale, 1655, 12000),
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRate(ctx, "USD", 441234, "2026-07-15"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: domain.NewDate(time.Now()), Amount: 500_000_00,
		Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	// Один розміщували вчора, другий — два роки тому.
	now := time.Now()
	if err := st.SaveAuctions(ctx, []nbu.Auction{
		{Date: domain.NewDate(now.AddDate(0, 0, -1)), ISIN: fresh, Num: "91",
			Currency: money.UAH, Bucket: "1.5y", IncomeBP: 1519, DaysToRepay: 580},
		{Date: domain.NewDate(now.AddDate(-2, 0, 0)), ISIN: stale, Num: "12",
			Currency: money.UAH, Bucket: "3y", IncomeBP: 1655, DaysToRepay: 1100},
	}); err != nil {
		t.Fatal(err)
	}

	_, body := do(t, "GET", srv.URL+"/api/reinvest", "")
	var got []struct {
		Kind           string  `json:"kind"`
		ISIN           string  `json:"isin"`
		Reason         string  `json:"reason"`
		LastAuction    string  `json:"last_auction"`
		LastAuctionPct float64 `json:"last_auction_pct"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	var bonds []int
	for i, g := range got {
		if g.Kind == "bond" {
			bonds = append(bonds, i)
		}
	}
	if len(bonds) != 2 {
		t.Fatalf("очікували дві облігації, маємо %d: %s", len(bonds), body)
	}
	first, second := got[bonds[0]], got[bonds[1]]
	if first.ISIN != fresh {
		t.Errorf("зверху мав бути свіжо розміщений %s, маємо %s (%s)", fresh, first.ISIN, body)
	}
	if second.ISIN != stale {
		t.Errorf("несвіжий %s мав опуститись, маємо %s", stale, second.ISIN)
	}
	// Свіжий каже, коли й під скільки; несвіжий — що його там не було.
	if first.LastAuction == "" || first.LastAuctionPct != 15.19 {
		t.Errorf("свіжий рядок мовчить про розміщення: %+v", first)
	}
	// Свіже розміщення попереджати нема про що: сам факт їде полями, а
	// проза лишається чистою.
	if strings.Contains(first.Reason, "вторинний ринок") {
		t.Errorf("свіжий рядок не мав попереджати: %q", first.Reason)
	}
	// Несвіжий, але ВІДОМИЙ папір каже саме «відтоді»: сказати про нього
	// «не розміщувався» було б неправдою — він існує, застаріла лише ціна,
	// і дата з рівнем поруч це показують.
	if !strings.Contains(second.Reason, "відтоді лише вторинний ринок") {
		t.Errorf("причина несвіжого: %q", second.Reason)
	}
	if second.LastAuction == "" || second.LastAuctionPct != 16.55 {
		t.Errorf("несвіжий рядок мав зберегти дату й рівень: %+v", second)
	}
	// Нічого не сховано: обидва лишаються в переліку.
	if len(bonds) != 2 {
		t.Error("порада зникла — ліміти й несвіжість опускають, а не ховають")
	}
}

// Поки історії аукціонів немає (свіжа інсталяція, бекфіл ще в фоні),
// про них не сказано ні слова. Написати «не розміщувався» там, де ми
// просто не дивились, означало б видати незнання за факт.
func TestReinvestSilentWithoutAuctionHistory(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if _, err := st.AddDeposit(context.Background(), store.Deposit{
		Date: domain.NewDate(time.Now()), Amount: 500_000_00,
		Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	_, body := do(t, "GET", srv.URL+"/api/reinvest", "")
	for _, word := range []string{"вторинний ринок", "останнє розміщення", "last_auction"} {
		if strings.Contains(body, word) {
			t.Errorf("без історії аукціонів згадки про них бути не мало (%q): %s", word, body)
		}
	}
}
