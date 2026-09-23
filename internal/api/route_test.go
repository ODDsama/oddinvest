package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/engine"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

// --- каркас ---

// --- головний інваріант ---

// --- стеля подушки ---

// --- накопичення ---

// --- перенос стану ---

// --- межі ---

// --- крізь HTTP ---

// Порожня база віддає 200 і порожній СПИСОК, а не null і не 500.
//
// null тут дорожчий за здається: у браузері він падає на legs.length
// усередині рендера, тобто сторінка гасне цілком, і причина в консолі
// виглядає як помилка розмітки, а не як порожній портфель.
func TestRouteEndpointEmptyDB(t *testing.T) {
	srv, _ := testServer(t)
	resp, body := do(t, "GET", srv.URL+"/api/route", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("маршрут: %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `"legs":[]`) {
		t.Errorf("порожній маршрут мусить нести legs:[], а несе: %s", body)
	}
	if !strings.Contains(body, `"note":`) {
		t.Errorf("порожня відповідь без причини читається як поломка: %s", body)
	}
}

// Живий портфель: купон із довідника НБУ доходить до маршруту й веде туди,
// куди веде політика.
//
// Тест наскрізний навмисно — між buildRoute і людиною стоять ще чотири
// кроки (BuildState, рейтинг, джерела, futureIncome), і кожен із них уміє
// віддати порожнечу, яку модульний тест не побачить.
func TestRouteEndpointSeesScheduledCoupon(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":50,"price_per_bond":"995.00",`+
			`"buy_date":"2026-07-01","channel":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, body)
	}

	_, body := do(t, "GET", srv.URL+"/api/route", "")
	var got engine.RouteDoc
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("маршрут не розбирається: %v — %s", err, body)
	}
	if len(got.Legs) == 0 {
		t.Fatalf("купон 2026-09-16 не дійшов до маршруту: %s", body)
	}
	prev := ""
	for i, leg := range got.Legs {
		if leg.Date < prev {
			t.Errorf("нога %d датована %s після %s — маршрут іде не в часі", i, leg.Date, prev)
		}
		prev = leg.Date
		if leg.Broker != "mono" {
			t.Errorf("нога %d у брокера %q, чекали mono — купон кредитує рахунок покупки",
				i, leg.Broker)
		}
		if leg.AmountUAH.Major() <= 0 {
			t.Errorf("нога %d на %.2f ₴ — надходження без грошей не буває", i, leg.AmountUAH.Major())
		}
	}
	if got.From == "" || got.To == "" {
		t.Errorf("маршрут без названих меж горизонту: %s", body)
	}
}

// У валюті звітності нога перекладається ЦІЛКОМ — разом із розкладкою,
// вкладеною в неї без імені (allocPlan).
//
// Живий випадок: «Надійде 101 $», а поруч «Разом 4 500 $» і «Куди піде
// 4 037 $ + 463 $» — гривні під знаком долара. Презентер пропускав
// неекспортовані поля, а вкладення приватного типу — саме таке поле, хоч
// json і піднімає його вміст до ноги. Кожна сума ноги звіряється з
// гривневою відповіддю через курс до цента; підпис фонду — проза, і вона
// теж має бути доларовою.
func TestRouteEndpointInReportCurrency(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st) // курс USD 44.1234
	ctx := context.Background()
	today := domain.NewDate(time.Now())
	if resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":50,"price_per_bond":"995.00",`+
			`"buy_date":"2026-07-01","channel":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, body)
	}
	// Фонд, що докуповує сертифікати сам: його нога несе підпис із сумами.
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: today.AddDays(-140), Fund: "Inzhur REIT", Kind: domain.FundBuy,
		Qty: 779, Amount: 866_575, Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	funds, err := st.ListFunds(ctx)
	if err != nil || len(funds) != 1 {
		t.Fatalf("довідник фондів: %v %+v", err, funds)
	}
	f := funds[0]
	f.ExpectedYieldBP, f.PayoutDay, f.Kind = 950, 10, store.FundReinvesting
	if err := st.RenameFund(ctx, f.ID, f); err != nil {
		t.Fatal(err)
	}

	route := func() engine.RouteDoc {
		resp, body := do(t, "GET", srv.URL+"/api/route", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("маршрут: %d %s", resp.StatusCode, body)
		}
		var got engine.RouteDoc
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("маршрут не розбирається: %v — %s", err, body)
		}
		return got
	}
	uah := route()
	if resp, body := do(t, "PUT", srv.URL+"/api/settings", `{"report_currency":"USD"}`); resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		t.Fatalf("settings: %d %s", resp.StatusCode, body)
	}
	usd := route()
	if len(usd.Legs) == 0 || len(usd.Legs) != len(uah.Legs) {
		t.Fatalf("ніг у доларах %d, у гривні %d", len(usd.Legs), len(uah.Legs))
	}

	const rate = 44.1234
	same := func(what string, got, want state.Money) {
		t.Helper()
		w := want.In("USD", rate).Minor()
		if g := got.Minor(); g < w-1 || g > w+1 {
			t.Errorf("%s: %d центів, чекали %d ±1 (з %v)", what, g, w, want)
		}
	}
	whyFund := ""
	for i := range usd.Legs {
		u, h := usd.Legs[i], uah.Legs[i]
		tag := "нога " + u.Date + " " + u.Label
		same(tag+" inflow_uah", u.InflowUAH, h.InflowUAH)
		same(tag+" amount_uah (вкладена розкладка)", u.AmountUAH, h.AmountUAH)
		same(tag+" rest_uah", u.RestUAH, h.RestUAH)
		same(tag+" avail_uah", u.AvailUAH, h.AvailUAH)
		if len(u.Lines) != len(h.Lines) {
			t.Fatalf("%s: рядків %d проти %d", tag, len(u.Lines), len(h.Lines))
		}
		for j := range u.Lines {
			same(tag+" lines[].total_uah", u.Lines[j].TotalUAH, h.Lines[j].TotalUAH)
		}
		if u.Reserve != nil && h.Reserve != nil {
			same(tag+" reserve.amount_uah", u.Reserve.AmountUAH, h.Reserve.AmountUAH)
		}
		if u.InflowWhy != "" {
			whyFund = u.InflowWhy
		}
	}
	if whyFund == "" {
		t.Fatal("нога фонду з докупівлею мала нести підпис")
	}
	if !strings.Contains(whyFund, "$") || strings.Contains(whyFund, "₴") {
		t.Errorf("підпис фонду не в доларах: %q", whyFund)
	}
	for i := range usd.Months {
		if i < len(uah.Months) {
			same("months[].plan_uah", usd.Months[i].PlanUAH, uah.Months[i].PlanUAH)
		}
	}
}

// --- основа надходження ---

// --- закріплення ---

// --- обраний папір ---

// Невідомий папір у ?pick= — 400 із причиною, а не тихий маршрут без ОВДП.
// Порожня база не має жодної поради, тож будь-який ISIN тут невідомий.
func TestRouteEndpointRejectsUnknownPick(t *testing.T) {
	srv, _ := testServer(t)
	resp, body := do(t, "GET",
		srv.URL+"/api/route?pick=2026-09-10%7Cmono%7CUAH%7CUA0000000000", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("невідомий папір у виборі: %d %s — чекали 400", resp.StatusCode, body)
	}
	if !strings.Contains(body, "UA0000000000") {
		t.Errorf("відмова мусить називати папір: %s", body)
	}
	resp, body = do(t, "GET", srv.URL+"/api/route?pick=abc", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("крива форма pick: %d %s — чекали 400", resp.StatusCode, body)
	}
}
