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
				Key             string  `json:"key"`
				Amount          float64 `json:"amount"`
				RatePct         float64 `json:"rate_pct"`
				RateTerminalPct float64 `json:"rate_terminal_pct"`
				ContribMonthly  float64 `json:"contrib_monthly"`
				GoalPct         float64 `json:"goal_pct"`
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
	// Сьогоднішня ставка — факт, за нею можна купити просто зараз, тож
	// вона МАЄ бути однакова в усіх трьох сценаріях. Інакше «оптимістично»
	// означало б, що вже куплені папери магічно платять більше.
	for _, r := range f.Rows[1:] {
		if r.RatePct != f.Rows[0].RatePct {
			t.Errorf("%s: сьогоднішня ставка має збігатися з рештою (%v), маємо %v",
				r.Key, f.Rows[0].RatePct, r.RatePct)
		}
	}
	// Розходяться сценарії ДОВГОСТРОКОВОЮ ставкою: ±3 п.п. навколо неї.
	if d := f.Rows[0].RateTerminalPct - f.Rows[1].RateTerminalPct; d < 2.99 || d > 3.01 {
		t.Errorf("оптимістична довгострокова ставка має бути +3 п.п., різниця %v", d)
	}
	if d := f.Rows[1].RateTerminalPct - f.Rows[2].RateTerminalPct; d < 2.99 || d > 3.01 {
		t.Errorf("песимістична довгострокова ставка має бути -3 п.п., різниця %v", d)
	}
	// І довгострокова має бути НИЖЧОЮ за сьогоднішню: воєнні 16-17%
	// закладати на весь горизонт не можна навіть в оптимістичному.
	if f.Rows[0].RateTerminalPct >= f.Rows[0].RatePct {
		t.Errorf("навіть оптимістична довгострокова ставка не має перевищувати сьогоднішню %v, маємо %v",
			f.Rows[0].RatePct, f.Rows[0].RateTerminalPct)
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

// Таблиця проєкцій має порівнювати порівнюване: «внесено» і «за планом»
// в одній одиниці. Інакше приріст виходить від'ємним на коротких
// горизонтах при цілком здоровому портфелі.
func TestProjectionColumnsShareUnit(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if _, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"mono"}`); body == "" {
		t.Fatal("порожня відповідь на додавання лота")
	}
	if resp, body := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_target_uah":"5000","uah_devaluation_pct":"6"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put settings: %d %s", resp.StatusCode, body)
	}
	var got struct {
		Projection []struct {
			Years        int     `json:"years"`
			Contributed  float64 `json:"contributed"`
			WithReinvest float64 `json:"with_reinvest"`
		} `json:"projection"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("summary не парситься: %v", err)
	}
	if len(got.Projection) != 4 {
		t.Fatalf("очікували 4 горизонти, маємо %d", len(got.Projection))
	}
	for _, r := range got.Projection {
		if r.WithReinvest <= r.Contributed {
			t.Errorf("%d р.: 16.5%% проти 6%% знецінення мали дати приріст, маємо %.2f vs %.2f",
				r.Years, r.WithReinvest, r.Contributed)
		}
	}
}

// Помічник має порівнювати валюти ЧЕСНО. Сира купонна ставка цього не
// вміє: гривневі 16% завжди били доларові 4%, хоча при знеціненні
// гривні реальна дохідність може бути на боці долара.
func TestReinvestRanksByRealReturn(t *testing.T) {
	srv, st := testServer(t)

	uahHigh := nbu.Security{
		Bond: domain.Bond{ISIN: "UA0000000001", Nominal: money.New(100000, money.UAH),
			RateBP: 1600, Maturity: "2029-07-15", Descr: "гривневий 16%"},
		Payments: []domain.Payment{
			{ISIN: "UA0000000001", PayDate: "2027-07-15", Type: domain.PayCoupon, PerBond: money.New(16000, money.UAH)},
			{ISIN: "UA0000000001", PayDate: "2028-07-15", Type: domain.PayCoupon, PerBond: money.New(16000, money.UAH)},
			{ISIN: "UA0000000001", PayDate: "2029-07-15", Type: domain.PayCoupon, PerBond: money.New(16000, money.UAH)},
			{ISIN: "UA0000000001", PayDate: "2029-07-15", Type: domain.PayRedemption, PerBond: money.New(100000, money.UAH)},
		},
	}
	usdLow := nbu.Security{
		Bond: domain.Bond{ISIN: "UA0000000002", Nominal: money.New(100000, money.USD),
			RateBP: 400, Maturity: "2029-07-15", Descr: "доларовий 4%"},
		Payments: []domain.Payment{
			{ISIN: "UA0000000002", PayDate: "2027-07-15", Type: domain.PayCoupon, PerBond: money.New(4000, money.USD)},
			{ISIN: "UA0000000002", PayDate: "2028-07-15", Type: domain.PayCoupon, PerBond: money.New(4000, money.USD)},
			{ISIN: "UA0000000002", PayDate: "2029-07-15", Type: domain.PayCoupon, PerBond: money.New(4000, money.USD)},
			{ISIN: "UA0000000002", PayDate: "2029-07-15", Type: domain.PayRedemption, PerBond: money.New(100000, money.USD)},
		},
	}
	if err := st.ReplaceDirectory(context.Background(), []nbu.Security{uahHigh, usdLow}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRate(context.Background(), "USD", 4200000, "2026-07-15"); err != nil {
		t.Fatal(err)
	}

	type row struct {
		ISIN     string  `json:"isin"`
		Currency string  `json:"currency"`
		YTMPct   float64 `json:"ytm_pct"`
		RealPct  float64 `json:"real_pct"`
	}
	rank := func(devalPct string) map[string]row {
		t.Helper()
		if resp, body := do(t, "PUT", srv.URL+"/api/settings",
			`{"uah_devaluation_pct":"`+devalPct+`","reinvest_rank":"rate"}`); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("put settings: %d %s", resp.StatusCode, body)
		}
		var rows []row
		_, body := do(t, "GET", srv.URL+"/api/reinvest", "")
		if err := json.Unmarshal([]byte(body), &rows); err != nil {
			t.Fatalf("reinvest не парситься: %v — %s", err, body)
		}
		if len(rows) != 2 {
			t.Fatalf("очікували 2 папери, маємо %d: %s", len(rows), body)
		}
		out := map[string]row{}
		for _, r := range rows {
			out[r.Currency] = r
		}
		out["_first"] = rows[0]
		return out
	}

	// Долар знеціненням не зачеплений — його реальна дорівнює YTM.
	low := rank("0")
	if u := low["USD"]; math.Abs(u.RealPct-u.YTMPct) > 0.01 {
		t.Errorf("доларова реальна має дорівнювати YTM: %v vs %v", u.RealPct, u.YTMPct)
	}
	// Гривнева реальна має бути НИЖЧОЮ за YTM, щойно знецінення ненульове.
	mid := rank("6")
	if h := mid["UAH"]; !(h.RealPct < h.YTMPct) {
		t.Errorf("гривнева реальна мала просісти нижче YTM: %v vs %v", h.RealPct, h.YTMPct)
	}
	// Без знецінення виграє гривня, за високого — долар. Саме цього
	// перевороту стара сортировка за rate_bp не вміла в принципі.
	if low["_first"].Currency != money.UAH {
		t.Errorf("без знецінення першим мав бути гривневий, маємо %s", low["_first"].Currency)
	}
	if high := rank("15"); high["_first"].Currency != money.USD {
		t.Errorf("за знецінення 15%% першим мав стати доларовий, маємо %s", high["_first"].Currency)
	}
}

// Правка лота має зберігати id, бо продажі посилаються на нього через
// lot_id. «Видалити й створити заново» осиротило б історію продажів —
// саме тому PUT, а не пара DELETE+POST.
func TestUpdateLotKeepsSalesLinked(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)

	if resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, body)
	}
	if resp, body := do(t, "POST", srv.URL+"/api/sales",
		`{"lot_id":1,"sale_date":"2026-08-01","qty":2,"clean_per_bond":"1001.00","currency":"UAH"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("продаж: %d %s", resp.StatusCode, body)
	}

	// правимо ціну й канал — продаж має лишитись прив'язаним
	if resp, body := do(t, "PUT", srv.URL+"/api/lots/1",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"990.00","buy_date":"2026-07-01","channel":"inzhur"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("правка лота: %d %s", resp.StatusCode, body)
	}
	_, body := do(t, "GET", srv.URL+"/api/lots", "")
	if !strings.Contains(body, `"990.00"`) || !strings.Contains(body, "inzhur") {
		t.Errorf("правка не застосувалась: %s", body)
	}
	_, body = do(t, "GET", srv.URL+"/api/sales", "")
	if !strings.Contains(body, `"lot_id":1`) {
		t.Errorf("продаж відв'язався від лота: %s", body)
	}
	// результат продажу мав перерахуватись під нову ціну: 2×1001 − 2×990 = 22.00
	if !strings.Contains(body, `"22.00"`) {
		t.Errorf("результат продажу не перерахувався під нову ціну: %s", body)
	}

	// не даємо зменшити кількість нижче проданої
	if resp, body := do(t, "PUT", srv.URL+"/api/lots/1",
		`{"isin":"UA4000227748","qty":1,"price_per_bond":"990.00","buy_date":"2026-07-01","channel":"inzhur"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("кількість нижче проданої мала дати 400, маємо %d %s", resp.StatusCode, body)
	}
	// і не мовчимо про неіснуючий id
	if resp, _ := do(t, "PUT", srv.URL+"/api/lots/999",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"990.00","buy_date":"2026-07-01"}`); resp.StatusCode == http.StatusNoContent {
		t.Error("правка неіснуючого лота мала дати помилку")
	}
}

// Поповнення й конвертації правляться так само, і баланс має поїхати за
// правкою — інакше звірка з реальним рахунком безглузда.
func TestUpdateDepositAndConversion(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)

	do(t, "POST", srv.URL+"/api/deposits", `{"amount":"5000.00","currency":"UAH","date":"2026-07-01","broker":"mono"}`)
	if resp, body := do(t, "PUT", srv.URL+"/api/deposits/1",
		`{"amount":"5161.60","currency":"UAH","date":"2026-07-01","broker":"mono","note":"звірка"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("правка поповнення: %d %s", resp.StatusCode, body)
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if !strings.Contains(body, `5161.6`) {
		t.Errorf("баланс не поїхав за правкою: %s", body)
	}

	do(t, "POST", srv.URL+"/api/conversions",
		`{"from_currency":"UAH","from_amount":"4200.00","to_currency":"USD","to_amount":"100.00","broker":"mono"}`)
	if resp, body := do(t, "PUT", srv.URL+"/api/conversions/1",
		`{"from_currency":"UAH","from_amount":"4300.00","to_currency":"USD","to_amount":"100.00","broker":"mono"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("правка конвертації: %d %s", resp.StatusCode, body)
	}
	_, body = do(t, "GET", srv.URL+"/api/conversions", "")
	if !strings.Contains(body, `"4300.00"`) {
		t.Errorf("конвертація не оновилась: %s", body)
	}
	// однакові валюти лишаються забороненими і на правці
	if resp, _ := do(t, "PUT", srv.URL+"/api/conversions/1",
		`{"from_currency":"UAH","from_amount":"100.00","to_currency":"UAH","to_amount":"100.00"}`); resp.StatusCode != http.StatusBadRequest {
		t.Error("однакові валюти мали дати 400 і на PUT")
	}
}

// Четвертий рядок віяла — про ТЕБЕ, а не про ринок: плановий внесок
// замінено фактичним темпом за тих самих ринкових допущень, що й
// реалістичний. Тож різниця між ними — рівно твоя поведінка.
func TestForecastActualPaceRow(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	deadline := time.Now().AddDate(3, 0, 0).Format("2006-01-02")

	type row struct {
		Key            string  `json:"key"`
		Amount         float64 `json:"amount"`
		ContribMonthly float64 `json:"contrib_monthly"`
		RatePct        float64 `json:"rate_pct"`
		DevaluationPct float64 `json:"devaluation_pct"`
	}
	rows := func() map[string]row {
		t.Helper()
		var got struct {
			Forecast struct {
				Rows []row `json:"rows"`
			} `json:"forecast"`
		}
		_, body := do(t, "GET", srv.URL+"/api/summary", "")
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("summary не парситься: %v", err)
		}
		out := map[string]row{}
		for _, r := range got.Forecast.Rows {
			out[r.Key] = r
		}
		return out
	}

	if resp, body := do(t, "PUT", srv.URL+"/api/settings",
		`{"monthly_target_uah":"5000","goal_amount_uah":"500000","goal_date":"`+deadline+`"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put settings: %d %s", resp.StatusCode, body)
	}
	// поки історії поповнень немає — рядка теж немає
	if _, ok := rows()["actual"]; ok {
		t.Error("без історії поповнень рядок «за фактом» не має з'являтись")
	}

	// два поповнення з розривом >60 днів: темп ≈ 3000/міс, нижчий за план
	first := time.Now().AddDate(0, 0, -90).Format("2006-01-02")
	for _, d := range []string{first, time.Now().Format("2006-01-02")} {
		if resp, body := do(t, "POST", srv.URL+"/api/deposits",
			`{"amount":"4500.00","currency":"UAH","date":"`+d+`","broker":"mono"}`); resp.StatusCode != http.StatusCreated {
			t.Fatalf("поповнення: %d %s", resp.StatusCode, body)
		}
	}

	r := rows()
	act, ok := r["actual"]
	if !ok {
		t.Fatal("рядок «за фактом» мав з'явитись після 60 днів історії")
	}
	if act.ContribMonthly >= 5000 || act.ContribMonthly <= 0 {
		t.Errorf("фактичний темп мав бути нижчим за план 5000, маємо %v", act.ContribMonthly)
	}
	// ринкові допущення — ті самі, що в реалістичному
	real := r["realistic"]
	if act.RatePct != real.RatePct || act.DevaluationPct != real.DevaluationPct {
		t.Errorf("фактичний рядок має брати ринок реалістичного: ставка %v vs %v, знецінення %v vs %v",
			act.RatePct, real.RatePct, act.DevaluationPct, real.DevaluationPct)
	}
	// менший внесок за тих самих допущень — менший капітал
	if act.Amount >= real.Amount {
		t.Errorf("за нижчого темпу капітал мав бути меншим: %v vs %v", act.Amount, real.Amount)
	}
	// і головне: три ринкові сценарії тепер НЕ чіпають внесок
	for _, k := range []string{"optimistic", "realistic", "pessimistic"} {
		if r[k].ContribMonthly != 5000 {
			t.Errorf("%s: ринковий сценарій має брати плановий внесок 5000, маємо %v",
				k, r[k].ContribMonthly)
		}
	}
}

// Фактичний темп має ПОВЕРТАТИ те, що людина реально відкладає. Стара
// формула ділила на проміжок «перше поповнення … сьогодні» і завищувала
// в півтора раза: поповнення фінансують періоди, а не проміжок між
// собою — три щомісячні внески покривають три місяці, а проміжок лише
// два. Плюс вона потребувала 60 днів історії, бо на старті ділення на
// частку місяця давало сотні тисяч.
func TestActualPaceEstimator(t *testing.T) {
	pace := func(t *testing.T, deposits map[int]string) (float64, int) {
		t.Helper()
		srv, st := testServer(t)
		seed(t, st)
		for daysAgo, amount := range deposits {
			d := time.Now().AddDate(0, 0, -daysAgo).Format("2006-01-02")
			if resp, body := do(t, "POST", srv.URL+"/api/deposits",
				`{"amount":"`+amount+`","currency":"UAH","date":"`+d+`","broker":"mono"}`); resp.StatusCode != http.StatusCreated {
				t.Fatalf("поповнення: %d %s", resp.StatusCode, body)
			}
		}
		var got struct {
			ActualMonthly float64 `json:"actual_monthly_uah"`
			ActualMonths  int     `json:"actual_months"`
		}
		_, body := do(t, "GET", srv.URL+"/api/summary", "")
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("summary не парситься: %v", err)
		}
		return got.ActualMonthly, got.ActualMonths
	}

	// три щомісячні внески по 5000 -> темп має бути ~5000, а не ~7600
	got, months := pace(t, map[int]string{60: "5000.00", 30: "5000.00", 0: "5000.00"})
	if got < 4800 || got > 5200 {
		t.Errorf("три внески по 5000/міс мали дати ~5000 ₴/міс, маємо %.0f", got)
	}
	if months != 3 {
		t.Errorf("історія мала бути 3 міс, маємо %d", months)
	}

	// одне поповнення сьогодні: показуємо одразу і без вибуху
	got, months = pace(t, map[int]string{0: "5000.00"})
	if got < 4900 || got > 5100 {
		t.Errorf("одне поповнення 5000 мало дати ~5000 ₴/міс, маємо %.0f", got)
	}
	if months != 1 {
		t.Errorf("історія мала бути 1 міс, маємо %d", months)
	}

	// давнє одиничне поповнення — темп низький, і це правда
	got, _ = pace(t, map[int]string{365: "5000.00"})
	if got > 500 {
		t.Errorf("одне поповнення рік тому мало дати низький темп, маємо %.0f", got)
	}

	// зняття не рахуються як внески
	got, _ = pace(t, map[int]string{30: "5000.00", 0: "-1000.00"})
	if got < 2300 || got > 2700 {
		t.Errorf("зняття мало лишитись поза темпом (~2500), маємо %.0f", got)
	}
}
