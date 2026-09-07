package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/store"
)

// universeBond — папір із простим графіком: один купон і погашення.
func universeBond(isin string, matures domain.Date) nbu.Security {
	return nbu.Security{
		Bond: domain.Bond{ISIN: isin, Nominal: money.New(100000, money.UAH),
			RateBP: 1600, Maturity: matures, Descr: "тест"},
		Payments: []domain.Payment{
			{ISIN: isin, PayDate: matures, Type: domain.PayCoupon, PerBond: money.New(8000, money.UAH)},
			{ISIN: isin, PayDate: matures, Type: domain.PayRedemption, PerBond: money.New(100000, money.UAH)},
		},
	}
}

type sugRow struct {
	Kind      string `json:"kind"`
	ISIN      string `json:"isin"`
	CostBasis string `json:"cost_basis"`
	ReadyOn   string `json:"ready_on"`
	ReadyNote string `json:"ready_note"`
	Maturity  string `json:"maturity"`
}

// reinvestBonds — поради-облігації через живий ендпойнт.
func reinvestBonds(t *testing.T, url string) []sugRow {
	t.Helper()
	resp, body := do(t, http.MethodGet, url+"/api/reinvest", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP %d: %s", resp.StatusCode, body)
	}
	var all []sugRow
	if err := json.Unmarshal([]byte(body), &all); err != nil {
		t.Fatal(err)
	}
	var out []sugRow
	for _, r := range all {
		if r.Kind == "bond" {
			out = append(out, r)
		}
	}
	return out
}

// universeSetup — база з грошима й довідником на N паперів.
func universeSetup(t *testing.T, secs []nbu.Security) (string, *store.Store) {
	t.Helper()
	srv, st := testServer(t)
	ctx := context.Background()
	if err := st.ReplaceDirectory(ctx, secs, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: domain.NewDate(time.Now()), Amount: 100_000_00,
		Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	return srv.URL, st
}

// ПОМІЧНИК БАЧИТЬ УВЕСЬ ДОВІДНИК, А НЕ ПЕРШІ 50.
//
// Доти `reinvestSuggestions` кликав `SearchBonds` із лімітом 5000 — намір
// «усі», — а сховище мовчки затискало його до 50 і віддавало ORDER BY
// maturity, тобто рівно 50 НАЙКОРОТШИХ. На бойовому це ховало 136 паперів
// зі 186: помічник ніколи не бачив нічого з погашенням після березня 2028.
func TestReinvestSeesWholeDirectory(t *testing.T) {
	base := domain.NewDate(time.Now())
	var secs []nbu.Security
	for i := 0; i < 70; i++ {
		// Строки зростають, тож при обрізанні до 50 зникнуть саме останні.
		secs = append(secs, universeBond(fmt.Sprintf("UA400000%04d", i), base.AddDays(200+i*30)))
	}
	url, _ := universeSetup(t, secs)
	got := reinvestBonds(t, url)
	if len(got) != 70 {
		t.Fatalf("порад %d, чекали всі 70 — схоже, повернулось затискання до 50", len(got))
	}
}

// ПАПІР, ЯКИЙ ОТ-ОТ ПОГАСИТЬСЯ, НЕ Є ПОРАДОЮ.
//
// Річна дохідність на такому залишку — не вимір: на живих даних одна
// гривня ціни рухала її на 4,4 в.п. Плюс брокери знімають такі папери з
// продажу, і власник побачив у пораді папір, якого вже не було ні в
// Приват24, ні в Inzhur.
func TestReinvestSkipsNearMaturity(t *testing.T) {
	base := domain.NewDate(time.Now())
	url, _ := universeSetup(t, []nbu.Security{
		universeBond("UA0000000009", base.AddDays(9)),             // як у живому випадку
		universeBond("UA0000000029", base.AddDays(minTermDays-1)), // рівно під порогом
		universeBond("UA0000000030", base.AddDays(minTermDays)),   // рівно на порозі — лишається
		universeBond("UA0000000400", base.AddDays(400)),           // звичайний
	})
	got := reinvestBonds(t, url)
	seen := map[string]bool{}
	for _, r := range got {
		seen[r.ISIN] = true
	}
	for _, isin := range []string{"UA0000000009", "UA0000000029"} {
		if seen[isin] {
			t.Errorf("%s гаситься раніше за поріг, а лишився в порадах", isin)
		}
	}
	for _, isin := range []string{"UA0000000030", "UA0000000400"} {
		if !seen[isin] {
			t.Errorf("%s мав лишитись у порадах", isin)
		}
	}
}

// ДОСТУПНІСТЬ ХОВАЄ ЛИШЕ ТЕ, ПРО ЩО МИ ПИТАЛИ.
//
// Це головне застереження всієї роботи. Ціна від свого брокера — доказ
// доступності, але її ВІДСУТНІСТЬ означає «не продають» тільки тоді, коли
// обхід питав про всі папери. Без знака повного обходу відсутність
// означає «не питали», і ховати за нею не можна — інакше зі списку зникло
// б півтори сотні паперів за неправдивою підставою.
func TestReinvestHidesUnpricedOnlyAfterFullSweep(t *testing.T) {
	base := domain.NewDate(time.Now())
	ctx := context.Background()
	url, st := universeSetup(t, []nbu.Security{
		universeBond("UA0000000111", base.AddDays(400)), // буде з ціною
		universeBond("UA0000000222", base.AddDays(500)), // без ціни
	})
	id, err := st.AddBroker(ctx, "inzhur")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetBrokerQuoteSource(ctx, id, "Inzhur"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveQuotes(ctx, []store.Quote{{
		ISIN: "UA0000000111", Source: "Inzhur", Date: base,
		PriceMinor: 101000, Currency: money.UAH, Origin: store.QuoteOriginFinomo,
	}}); err != nil {
		t.Fatal(err)
	}

	// Знака повного обходу ще немає — ховати не можна.
	if got := reinvestBonds(t, url); len(got) != 2 {
		t.Fatalf("без знака повного обходу мали лишитись обидва папери, маємо %d: %+v", len(got), got)
	}

	// Знак поставлено — тепер відсутність ціни щось означає.
	if err := st.SetAppState(ctx, store.QuotesSweptAtKey,
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	got := reinvestBonds(t, url)
	if len(got) != 1 || got[0].ISIN != "UA0000000111" {
		t.Fatalf("після повного обходу мав лишитись лише папір із ціною: %+v", got)
	}
	if got[0].CostBasis != CostBasisMarket {
		t.Errorf("підстава %q, чекали ринкову", got[0].CostBasis)
	}

	// Протухлий знак прирівнюється до відсутнього: обхід, старший за
	// поріг, не може підтверджувати доступність.
	if err := st.SetAppState(ctx, store.QuotesSweptAtKey,
		time.Now().AddDate(0, 0, -(quoteFreshDays+1)).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if got := reinvestBonds(t, url); len(got) != 2 {
		t.Errorf("зі протухлим знаком ховати не можна, маємо %d рядків", len(got))
	}
}

// ПЕРЕЛІК НА ОБХІД НЕ ЗАЛЕЖИТЬ ВІД ПОРАД — інакше фільтр замикає сам себе:
// схований папір не потрапляє в обхід, без обходу не дістає ціни й не
// з'являється вже ніколи.
func TestQuoteISINsIndependentOfSuggestions(t *testing.T) {
	base := domain.NewDate(time.Now())
	ctx := context.Background()
	srv, st := testServer(t)
	_ = srv
	if err := st.ReplaceDirectory(ctx, []nbu.Security{
		universeBond("UA0000000111", base.AddDays(400)),
		universeBond("UA0000000222", base.AddDays(500)),
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Знак повного обходу стоїть, цін немає в жодного — тобто в порадах
	// не лишилось би нічого.
	if err := st.SetAppState(ctx, store.QuotesSweptAtKey,
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	s := New(st, nil, testLogger())
	isins, err := s.quoteISINs(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(isins) != 2 {
		t.Fatalf("на обхід мали піти обидва папери, маємо %v — фільтр замкнув сам себе", isins)
	}
}
