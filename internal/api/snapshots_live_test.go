package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

type liveSnapRow struct {
	Date        string   `json:"date"`
	Live        *bool    `json:"live"`
	ReserveUAH  float64  `json:"reserve_uah"`
	NominalUAH  float64  `json:"nominal_uah_eq"`
	ExternalUAH *float64 `json:"external_uah"`
}

func getSnaps(t *testing.T, url string) []liveSnapRow {
	t.Helper()
	var rows []liveSnapRow
	_, body := do(t, "GET", url, "")
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("snapshots: %v: %s", err, body)
	}
	return rows
}

// Крива «Як росте» мусить жити вдень, а не лише після ранкового знімка:
// з live=1 останній рядок — стан на зараз, і він ЗАМІНЮЄ сьогоднішній
// знімок, а не стає поруч. Без live відповідь — рівно таблиця знімків.
//
// І «внесено» на тій самій кривій — зовнішні гроші (externalMoves), а не
// собівартість зі знімка: переказ гаманець → резерв дає нуль, зняття з
// резерву — мінус, купівля паперу з рахунку — нічого.
func TestSnapshotsLiveRowAndExternalMoney(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seed(t, st)
	today := domain.NewDate(time.Now())
	d3, d2, d1 := today.AddDays(-3), today.AddDays(-2), today.AddDays(-1)

	for _, d := range []store.Deposit{
		{Date: d3, Amount: 10_000_00, Currency: "UAH", Broker: "mono"},
		// Перша нога переказу гаманець → резерв.
		{Date: d2, Amount: -5_000_00, Currency: "UAH", Broker: "mono"},
	} {
		if _, err := st.AddDeposit(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	for _, op := range []store.ReserveOp{
		{Date: d2, Amount: 5_000_00, Currency: "UAH", Place: "готівка"},
		// Зняття з резерву в життя — гроші вийшли назовні.
		{Date: d1, Amount: -3_000_00, Currency: "UAH", Place: "готівка"},
	} {
		if _, err := st.AddReserveOp(ctx, op); err != nil {
			t.Fatal(err)
		}
	}
	// Купівля з рахунку — переклад усередині капіталу, зовнішніх грошей не
	// міняє.
	if resp, b := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":2,"price_per_bond":"1000.00","buy_date":"`+string(d1)+`","channel":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, b)
	}
	for _, d := range []domain.Date{d3, d2, d1} {
		if err := st.SaveSnapshot(ctx, store.Snapshot{Date: d, NominalUAHEq: 1}); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Сьогоднішнього знімка ще немає — жива точка дописується.
	rows := getSnaps(t, srv.URL+"/api/snapshots?live=1")
	if len(rows) != 4 {
		t.Fatalf("чекали 3 знімки + живу точку, маємо %d: %+v", len(rows), rows)
	}
	last := rows[3]
	if last.Date != string(today) || last.Live == nil || !*last.Live {
		t.Fatalf("останній рядок мав бути живим за %s: %+v", today, last)
	}
	// Резерв живої точки — зі стану (5 000 − 3 000), а не з таблиці.
	if last.ReserveUAH != 2000 {
		t.Errorf("резерв живої точки = %.2f, чекали 2000 зі стану", last.ReserveUAH)
	}
	want := []float64{10000, 10000, 7000, 7000}
	for i, r := range rows {
		if r.ExternalUAH == nil || *r.ExternalUAH != want[i] {
			t.Errorf("%s: external_uah = %v, чекали %.0f", r.Date, r.ExternalUAH, want[i])
		}
		if i < 3 && r.Live != nil {
			t.Errorf("%s: записаний знімок позначено живим", r.Date)
		}
	}

	// 2. Сьогоднішній знімок є — жива точка його заміняє, а в БД він лишається.
	if err := st.SaveSnapshot(ctx, store.Snapshot{Date: today, NominalUAHEq: 1}); err != nil {
		t.Fatal(err)
	}
	rows = getSnaps(t, srv.URL+"/api/snapshots?live=1")
	if len(rows) != 4 || rows[3].Live == nil || rows[3].NominalUAH == 0.01 {
		t.Fatalf("жива точка мала замінити сьогоднішній знімок, а не стати поруч: %+v", rows)
	}

	// 3. Без live — лише записане, і сьогоднішній рядок той, що в БД.
	rows = getSnaps(t, srv.URL+"/api/snapshots")
	if len(rows) != 4 || rows[3].Live != nil || rows[3].NominalUAH != 0.01 {
		t.Fatalf("без live відповідь мала лишитись таблицею знімків: %+v", rows)
	}

	// 4. Вікно в минулому живої точки не отримує.
	rows = getSnaps(t, srv.URL+"/api/snapshots?live=1&to="+string(d1))
	if len(rows) != 3 || rows[2].Live != nil {
		t.Fatalf("вікно до вчора не мало отримати живу точку: %+v", rows)
	}
}
