package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/store"
)

func testServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	srv := httptest.NewServer(New(st, nil, log).Handler())
	t.Cleanup(srv.Close)
	return srv, st
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

func do(t *testing.T, method, url, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := new(strings.Builder)
	b := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(b)
		buf.Write(b[:n])
		if err != nil {
			break
		}
	}
	resp.Body.Close()
	return resp, buf.String()
}

func TestLotLifecycleAndSummary(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)

	resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"Дія"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання лота: %d %s", resp.StatusCode, body)
	}

	resp, body = do(t, "GET", srv.URL+"/api/summary", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("summary: %d %s", resp.StatusCode, body)
	}
	for _, want := range []string{`"schema":1`, `"invested_uah":4975`, `"next_payment"`, `"2026-09-16"`} {
		if !strings.Contains(body, want) {
			t.Errorf("summary не містить %s: %s", want, body)
		}
	}

	resp, body = do(t, "GET", srv.URL+"/api/positions", "")
	if !strings.Contains(body, `"qty":5`) || !strings.Contains(body, `"4975.00"`) {
		t.Errorf("positions: %s", body)
	}

	// продаж 2 паперів: перевіряємо валідацію і перерахунок
	resp, body = do(t, "POST", srv.URL+"/api/sales",
		`{"lot_id":1,"sale_date":"2026-08-01","qty":2,"clean_per_bond":"1001.00","accrued":"11.30","currency":"UAH"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("продаж: %d %s", resp.StatusCode, body)
	}
	resp, body = do(t, "GET", srv.URL+"/api/sales", "")
	// результат = (2×1001 + 11.30) − 2×995 = 23.30, купонів за володіння не було
	if !strings.Contains(body, `"23.30"`) {
		t.Errorf("realized result: %s", body)
	}

	// oversell після продажу
	resp, _ = do(t, "POST", srv.URL+"/api/sales",
		`{"lot_id":1,"sale_date":"2026-08-02","qty":4,"clean_per_bond":"1001.00","currency":"UAH"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("oversell мав повернути 400, маємо %d", resp.StatusCode)
	}

	// календар: виплати на залишок 3
	resp, body = do(t, "GET", srv.URL+"/api/calendar", "")
	if !strings.Contains(body, `"248.25"`) { // 3 × 82.75
		t.Errorf("календар після продажу: %s", body)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	srv, _ := testServer(t)
	resp, _ := do(t, "PUT", srv.URL+"/api/settings", `{"monthly_target_uah":"5000"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put settings: %d", resp.StatusCode)
	}
	_, body := do(t, "GET", srv.URL+"/api/settings", "")
	if !strings.Contains(body, `"monthly_target_uah":"5000"`) {
		t.Errorf("settings: %s", body)
	}
	resp, _ = do(t, "PUT", srv.URL+"/api/settings", `{"hacker_key":"1"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("невідомий ключ мав дати 400: %d", resp.StatusCode)
	}
}

func TestBondSearchAndAccrued(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	_, body := do(t, "GET", srv.URL+"/api/bonds/search?q=військ", "")
	if !strings.Contains(body, "UA4000227748") {
		t.Errorf("пошук: %s", body)
	}
	_, body = do(t, "GET", srv.URL+"/api/accrued/UA4000227748?on=2026-10-16", "")
	// період 2026-09-16..2027-03-17 (182 дні), 30 днів: 8275×30/182 = 1364.01 -> 13.64
	if !strings.Contains(body, `"13.64"`) {
		t.Errorf("НКД: %s", body)
	}
	_, body = do(t, "GET", srv.URL+"/api/bonds/UA0000000000", "")
	if !strings.Contains(body, "не знайдено") {
		t.Errorf("404: %s", body)
	}
}

// Віяло прогнозів: три сценарії відрізняються ДОПУЩЕННЯМИ, а не сумами,
// і всі три рахуються на одну дату — дедлайн. Тест ловить головне, що
// може зламатись при зміні формули: порядок сум і розкид ставок.
func TestForecastFanOrderedByAssumptions(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)

	deadline := time.Now().AddDate(3, 0, 0).Format("2006-01-02")
	if _, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"mono"}`); body == "" {
		t.Fatal("порожня відповідь на додавання лота")
	}
	if resp, body := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_target_uah":"5000","goal_amount_uah":"1000000","goal_date":"`+deadline+`"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put settings: %d %s", resp.StatusCode, body)
	}

	var got struct {
		Forecast struct {
			Date            string  `json:"date"`
			Months          int     `json:"months"`
			GoalAmount      float64 `json:"goal_amount"`
			RequiredMonthly float64 `json:"required_monthly"`
			Rows            []struct {
				Key            string  `json:"key"`
				Amount         float64 `json:"amount"`
				RatePct        float64 `json:"rate_pct"`
				ContribMonthly float64 `json:"contrib_monthly"`
				GoalPct        float64 `json:"goal_pct"`
			} `json:"rows"`
		} `json:"forecast"`
		Goals []any `json:"goals"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("summary не парситься: %v", err)
	}
	f := got.Forecast
	if len(got.Goals) != 0 {
		t.Errorf("старе поле goals має зникнути, маємо %d рядків", len(got.Goals))
	}
	if len(f.Rows) != 3 {
		t.Fatalf("очікували 3 сценарії, маємо %d: %s", len(f.Rows), body)
	}
	if f.Months < 35 || f.Months > 36 {
		t.Errorf("до дедлайну має бути ~36 місяців, маємо %d", f.Months)
	}
	if want := []string{"optimistic", "realistic", "pessimistic"}; f.Rows[0].Key != want[0] ||
		f.Rows[1].Key != want[1] || f.Rows[2].Key != want[2] {
		t.Errorf("порядок сценаріїв: %v", f.Rows)
	}
	// Суми строго спадають — інакше «оптимістично» нічого не означає.
	if !(f.Rows[0].Amount > f.Rows[1].Amount && f.Rows[1].Amount > f.Rows[2].Amount) {
		t.Errorf("суми мають спадати opt>real>pess: %v %v %v",
			f.Rows[0].Amount, f.Rows[1].Amount, f.Rows[2].Amount)
	}
	// Розходяться саме ставкою: ±3 п.п. навколо реалістичної.
	if d := f.Rows[0].RatePct - f.Rows[1].RatePct; d < 2.99 || d > 3.01 {
		t.Errorf("оптимістична ставка має бути +3 п.п., різниця %v", d)
	}
	if d := f.Rows[1].RatePct - f.Rows[2].RatePct; d < 2.99 || d > 3.01 {
		t.Errorf("песимістична ставка має бути -3 п.п., різниця %v", d)
	}
	// Внесок поки історії поповнень немає — однаковий в усіх трьох.
	for _, r := range f.Rows {
		if r.ContribMonthly != 5000 {
			t.Errorf("%s: внесок без історії має дорівнювати плану, маємо %v", r.Key, r.ContribMonthly)
		}
	}
	// Мільйона за 3 роки при 5 тис/міс не буде — має бути порада скільки треба.
	if f.RequiredMonthly <= 5000 {
		t.Errorf("required_monthly має перевищувати план, маємо %v", f.RequiredMonthly)
	}
	if f.Rows[1].GoalPct <= 0 || f.Rows[1].GoalPct >= 100 {
		t.Errorf("частка цілі має бути в (0,100), маємо %v", f.Rows[1].GoalPct)
	}
}

// Профілі, які ще не пройшли міграцію 0008, не мають лишитись без цілі:
// ціль підхоплюється зі старого поля «оптимістична».
func TestForecastFallsBackToLegacyGoalFields(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	deadline := time.Now().AddDate(2, 0, 0).Format("2006-01-02")
	if resp, body := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_target_uah":"1000","goal_optimistic_uah":"750000","goal_date":"`+deadline+`"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put settings: %d %s", resp.StatusCode, body)
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if !strings.Contains(body, `"goal_amount":750000`) {
		t.Errorf("ціль зі старого поля не підхопилась: %s", body)
	}
}

// Девальвація має доходити до відповіді, а не лишатись у домені:
// реальна сума менша за номінальну, і що вищий очікуваний темп
// знецінення — то менше капіталу в сьогоднішніх грошах.
func TestForecastReflectsDevaluation(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	deadline := time.Now().AddDate(5, 0, 0).Format("2006-01-02")
	if _, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"mono"}`); body == "" {
		t.Fatal("порожня відповідь на додавання лота")
	}

	realistic := func(devalPct string) (real, nominal, deval float64) {
		t.Helper()
		if resp, body := do(t, "PUT", srv.URL+"/api/settings",
			`{"monthly_target_uah":"5000","goal_amount_uah":"1000000","goal_date":"`+deadline+
				`","uah_devaluation_pct":"`+devalPct+`"}`); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("put settings: %d %s", resp.StatusCode, body)
		}
		var got struct {
			Forecast struct {
				Rate0USD float64 `json:"rate0_usd"`
				Rows     []struct {
					Key            string  `json:"key"`
					Amount         float64 `json:"amount"`
					AmountNominal  float64 `json:"amount_nominal"`
					DevaluationPct float64 `json:"devaluation_pct"`
				} `json:"rows"`
			} `json:"forecast"`
		}
		_, body := do(t, "GET", srv.URL+"/api/summary", "")
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("summary не парситься: %v", err)
		}
		for _, r := range got.Forecast.Rows {
			if r.Key == "realistic" {
				return r.Amount, r.AmountNominal, r.DevaluationPct
			}
		}
		t.Fatalf("реалістичного сценарію немає: %s", body)
		return 0, 0, 0
	}

	real6, nominal6, deval6 := realistic("6")
	if deval6 != 6 {
		t.Errorf("реалістичний сценарій мав узяти задане знецінення 6%%, маємо %v", deval6)
	}
	if !(nominal6 > real6) {
		t.Errorf("номінальна сума має перевищувати реальну: %v vs %v", nominal6, real6)
	}
	real15, _, deval15 := realistic("15")
	if deval15 != 15 {
		t.Errorf("знецінення не підхопилось із налаштувань: %v", deval15)
	}
	if !(real15 < real6) {
		t.Errorf("за вищого знецінення реальний капітал мав бути меншим: %v vs %v", real15, real6)
	}

	// Нульове знецінення = стара поведінка: реальне збігається з номінальним.
	real0, nominal0, _ := realistic("0")
	if math.Abs(real0-nominal0) > 0.01 {
		t.Errorf("без знецінення реальне й номінальне мають збігатись: %v vs %v", real0, nominal0)
	}
}
