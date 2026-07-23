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

// importSince ставить водяний знак імпорту: усе, що старше за цю дату,
// виписка не розглядає взагалі. Тестам він потрібен явно — фікстури
// датовані фіксованим липнем 2026, а справжній знак ставиться на день
// запуску, тож без цього тести залежали б від сьогоднішнього числа.
func importSince(t *testing.T, st *store.Store, date string) {
	t.Helper()
	if err := st.SetSetting(context.Background(), "import_since", date); err != nil {
		t.Fatal(err)
	}
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
	resp, _ := do(t, "PUT", srv.URL+"/api/settings", `{"uah_devaluation_pct":"6"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put settings: %d", resp.StatusCode)
	}
	_, body := do(t, "GET", srv.URL+"/api/settings", "")
	if !strings.Contains(body, `"uah_devaluation_pct":"6"`) {
		t.Errorf("settings: %s", body)
	}
	// ручний місячний план прибрано — ключ більше не приймається
	if resp, _ := do(t, "PUT", srv.URL+"/api/settings", `{"monthly_target_uah":"5000"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("прибраний ключ мав дати 400: %d", resp.StatusCode)
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
		`{"goal_amount_uah":"1000000","goal_date":"`+deadline+`"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put settings: %d %s", resp.StatusCode, body)
	}

	var got struct {
		Forecast struct {
			Date            string  `json:"date"`
			Months          int     `json:"months"`
			GoalAmount      float64 `json:"goal_amount"`
			ContribPlan     float64 `json:"contrib_plan"`
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
	// План виводиться з цілі, тож усі ринкові сценарії беруть його.
	if f.ContribPlan <= 0 {
		t.Fatalf("план мав вивестись із цілі, маємо %v", f.ContribPlan)
	}
	for _, r := range f.Rows {
		if r.ContribMonthly != f.ContribPlan {
			t.Errorf("%s: ринковий сценарій має брати похідний план %v, маємо %v",
				r.Key, f.ContribPlan, r.ContribMonthly)
		}
	}
	// І головний наслідок похідного плану: реалістичний сценарій за
	// побудовою впирається рівно в ціль.
	if f.Rows[1].GoalPct < 99 || f.Rows[1].GoalPct > 101 {
		t.Errorf("реалістичний сценарій мав зійтись на 100%% цілі, маємо %v", f.Rows[1].GoalPct)
	}
	// А окремої поради «треба більше» вже немає: план і є та порада.
	if f.RequiredMonthly != 0 {
		t.Errorf("поле required_monthly мало зникнути, маємо %v", f.RequiredMonthly)
	}
}

// Профілі, які ще не пройшли міграцію 0008, не мають лишитись без цілі:
// ціль підхоплюється зі старого поля «оптимістична».
func TestForecastFallsBackToLegacyGoalFields(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	deadline := time.Now().AddDate(2, 0, 0).Format("2006-01-02")
	if resp, body := do(t, "PUT", srv.URL+"/api/settings",
		`{"goal_optimistic_uah":"750000","goal_date":"`+deadline+`"}`); resp.StatusCode != http.StatusNoContent {
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

	realistic := func(devalPct string) (real, nominal, deval, plan float64) {
		t.Helper()
		if resp, body := do(t, "PUT", srv.URL+"/api/settings",
			`{"goal_amount_uah":"1000000","goal_date":"`+deadline+
				`","uah_devaluation_pct":"`+devalPct+`"}`); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("put settings: %d %s", resp.StatusCode, body)
		}
		var got struct {
			Forecast struct {
				Rate0USD    float64 `json:"rate0_usd"`
				ContribPlan float64 `json:"contrib_plan"`
				Rows        []struct {
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
				return r.Amount, r.AmountNominal, r.DevaluationPct, got.Forecast.ContribPlan
			}
		}
		t.Fatalf("реалістичного сценарію немає: %s", body)
		return 0, 0, 0, 0
	}

	real6, nominal6, deval6, plan6 := realistic("6")
	if deval6 != 6 {
		t.Errorf("реалістичний сценарій мав узяти задане знецінення 6%%, маємо %v", deval6)
	}
	if !(nominal6 > real6) {
		t.Errorf("номінальна сума має перевищувати реальну: %v vs %v", nominal6, real6)
	}
	// План виводиться з цілі, тож за вищого знецінення адаптується ВНЕСОК,
	// а не підсумок: сума лишається на цілі, просто доходити до неї
	// доводиться більшими внесками.
	_, _, deval15, plan15 := realistic("15")
	if deval15 != 15 {
		t.Errorf("знецінення не підхопилось із налаштувань: %v", deval15)
	}
	if !(plan15 > plan6) {
		t.Errorf("за вищого знецінення потрібний внесок мав зрости: %v vs %v", plan15, plan6)
	}

	// Нульове знецінення = стара поведінка: реальне збігається з номінальним.
	real0, nominal0, _, _ := realistic("0")
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
		`{"goal_amount_uah":"500000","goal_date":"`+time.Now().AddDate(5,0,0).Format("2006-01-02")+`","uah_devaluation_pct":"6"}`); resp.StatusCode != http.StatusNoContent {
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
		`{"goal_amount_uah":"500000","goal_date":"`+deadline+`"}`); resp.StatusCode != http.StatusNoContent {
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
	if act.ContribMonthly <= 0 {
		t.Errorf("фактичний темп мав бути додатним, маємо %v", act.ContribMonthly)
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
	// і головне: три ринкові сценарії тепер НЕ чіпають внесок — усі три
	// беруть той самий похідний план
	plan := r["realistic"].ContribMonthly
	if plan <= 0 {
		t.Fatalf("план мав вивестись із цілі, маємо %v", plan)
	}
	for _, k := range []string{"optimistic", "pessimistic"} {
		if r[k].ContribMonthly != plan {
			t.Errorf("%s: ринковий сценарій має брати той самий план %v, маємо %v",
				k, plan, r[k].ContribMonthly)
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

	// Зняття входять у темп із мінусом: капітал вони зменшують так само,
	// як поповнення збільшують. Особливо це важить для переказів між
	// брокерами — окремої сутності переказу немає, тож він записується як
	// зняття + поповнення, і без нетто завищував би темп на свою суму.
	got, _ = pace(t, map[int]string{30: "5000.00", 0: "-1000.00"})
	if got < 1900 || got > 2150 {
		t.Errorf("темп мав бути від НЕТТО 4000 (~2015), маємо %.0f", got)
	}
	// переказ між брокерами не додає нових грошей — темп не має зрости
	transfer, _ := pace(t, map[int]string{30: "5000.00", 1: "-2000.00", 0: "2000.00"})
	plain, _ := pace(t, map[int]string{30: "5000.00"})
	if diff := transfer - plain; diff < -1 || diff > 1 {
		t.Errorf("переказ між брокерами не мав змінити темп: %.2f vs %.2f", transfer, plain)
	}
}

// Платіж під ціль один, але ринок вирішує, наскільки він посильний:
// за гіршого ринку той самий результат коштує більшого внеску. Саме це
// має розкидати віяло — а не суму на дедлайн, яка за побудовою всюди
// однакова, щойно внесок підбирається під ціль.
func TestForecastSpreadsRequiredPayment(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	deadline := time.Now().AddDate(3, 0, 0).Format("2006-01-02")
	if resp, body := do(t, "PUT", srv.URL+"/api/settings",
		`{"goal_amount_uah":"1000000","goal_date":"`+deadline+`","uah_devaluation_pct":"6"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put settings: %d %s", resp.StatusCode, body)
	}
	var got struct {
		Forecast struct {
			ContribPlan float64 `json:"contrib_plan"`
			Rows        []struct {
				Key             string  `json:"key"`
				RequiredMonthly float64 `json:"required_monthly"`
			} `json:"rows"`
		} `json:"forecast"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("summary не парситься: %v", err)
	}
	req := map[string]float64{}
	for _, r := range got.Forecast.Rows {
		req[r.Key] = r.RequiredMonthly
	}
	for _, k := range []string{"optimistic", "realistic", "pessimistic"} {
		if req[k] <= 0 {
			t.Fatalf("%s: потрібний внесок мав порахуватись, маємо %v", k, req[k])
		}
	}
	// За кращого ринку ціль коштує ДЕШЕВШЕ, за гіршого — дорожче.
	if !(req["optimistic"] < req["realistic"] && req["realistic"] < req["pessimistic"]) {
		t.Errorf("потрібний внесок мав зростати від оптимістичного до песимістичного: %v %v %v",
			req["optimistic"], req["realistic"], req["pessimistic"])
	}
	// Реалістичний потрібний внесок — це і є план.
	if math.Abs(req["realistic"]-got.Forecast.ContribPlan) > 1 {
		t.Errorf("реалістичний внесок мав збігтись із планом: %v vs %v",
			req["realistic"], got.Forecast.ContribPlan)
	}
}

// Сертифікати фондів проходять увесь шлях: журнал операцій -> позиція ->
// стан. Головне, що перевіряємо, — дохідність рахується ПІСЛЯ податку:
// купон ОВДП від нього звільнений, дивіденд фонду ні, і до податку ці
// числа непорівнянні.
func TestFundOpsFlowToState(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)

	// та сама історія, що у виписці Inzhur: набір позиції, дивіденд, продаж
	add := func(body string) {
		t.Helper()
		if resp, b := do(t, "POST", srv.URL+"/api/funds", body); resp.StatusCode != http.StatusCreated {
			t.Fatalf("операція фонду: %d %s", resp.StatusCode, b)
		}
	}
	add(`{"date":"2026-06-05","fund":"Inzhur REIT","kind":"buy","qty":110,"amount":"1207.82","broker":"inzhur"}`)
	add(`{"date":"2026-07-01","fund":"Inzhur REIT","kind":"buy","qty":90,"amount":"1000.40","broker":"inzhur"}`)
	add(`{"date":"2026-07-10","fund":"Inzhur REIT","kind":"dividend","amount":"18.99","tax":"2.66","broker":"inzhur"}`)
	add(`{"date":"2026-07-20","fund":"Inzhur REIT","kind":"sell","qty":72,"amount":"798.30","tax":"1.78","broker":"inzhur"}`)

	var got struct {
		FundsUAH float64 `json:"funds_uah"`
		Funds    []struct {
			Fund         string  `json:"fund"`
			Qty          int64   `json:"qty"`
			LastPrice    float64 `json:"last_price"`
			MarketValue  float64 `json:"market_value"`
			DividendsNet float64 `json:"dividends_net"`
			DividendsTax float64 `json:"dividends_tax"`
			YieldNetPct  float64 `json:"yield_net_pct"`
		} `json:"funds"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("summary не парситься: %v", err)
	}
	if len(got.Funds) != 1 {
		t.Fatalf("очікували одну позицію, маємо %d: %s", len(got.Funds), body)
	}
	f := got.Funds[0]
	if f.Qty != 128 { // 110 + 90 − 72
		t.Errorf("залишок 128 сертифікатів, маємо %d", f.Qty)
	}
	// остання операція — продаж 72 за 798.30 -> 11.0875 ₴
	if math.Abs(f.LastPrice-11.0875) > 0.0001 {
		t.Errorf("остання ціна 11.0875, маємо %v", f.LastPrice)
	}
	if math.Abs(f.MarketValue-128*11.0875) > 0.05 {
		t.Errorf("вартість позиції ≈%.2f, маємо %v", 128*11.0875, f.MarketValue)
	}
	// дивіденд ЧИСТИЙ: 18.99 − 2.66
	if math.Abs(f.DividendsNet-16.33) > 0.01 || math.Abs(f.DividendsTax-2.66) > 0.01 {
		t.Errorf("дивіденди мали бути 16.33 чистими при податку 2.66, маємо %v / %v",
			f.DividendsNet, f.DividendsTax)
	}
	if f.YieldNetPct <= 0 {
		t.Errorf("чиста дохідність мала порахуватись, маємо %v", f.YieldNetPct)
	}
	if math.Abs(got.FundsUAH-f.MarketValue) > 0.01 {
		t.Errorf("funds_uah має дорівнювати сумі позицій: %v vs %v", got.FundsUAH, f.MarketValue)
	}

	// валідація: невідомий тип і нульова кількість
	if resp, _ := do(t, "POST", srv.URL+"/api/funds",
		`{"fund":"X","kind":"transfer","qty":1,"amount":"10"}`); resp.StatusCode != http.StatusBadRequest {
		t.Error("невідомий kind мав дати 400")
	}
	if resp, _ := do(t, "POST", srv.URL+"/api/funds",
		`{"fund":"X","kind":"buy","qty":0,"amount":"10"}`); resp.StatusCode != http.StatusBadRequest {
		t.Error("нульова кількість мала дати 400")
	}
	// дивіденд без кількості — навпаки, коректний
	if resp, b := do(t, "POST", srv.URL+"/api/funds",
		`{"fund":"X","kind":"dividend","amount":"5.00","tax":"0.70"}`); resp.StatusCode != http.StatusCreated {
		t.Errorf("дивіденд без кількості мав пройти: %d %s", resp.StatusCode, b)
	}
}

// Імпорт виписки. Найважливіше тут — ідемпотентність: щомісячний файл
// містить і старі рядки, тож без дедуплікації другий імпорт подвоїв би
// і позицію, і баланс.
func TestImportInzhurIsIdempotent(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	importSince(t, st, "2020-01-01")

	xlsx := buildXLSX(t, [][]string{
		{"Дата", "Тип операції", "Вид цінного паперу", "Дебет", "Кредит"},
		{"46224.65980324074", "Купівля 5 сертифікатів", "Inzhur REIT", "", "55.56"},
		{"46213.00001157408", "Сплата податку", "Inzhur REIT", "", "2.66"},
		{"46213", "Нарахування дивідендів", "Inzhur REIT", "18.99"},
		{"46221.975694444445", "Поповнення брокерського рахунку", "-", "300"},
		{"46223.376238425924", "Купівля 1 облігації", "ОВДП UA4000237416", "", "1032.46"},
	})

	post := func(dry bool) map[string]any {
		t.Helper()
		url := srv.URL + "/api/import/inzhur"
		if dry {
			url += "?dry=1"
		}
		body, ct := multipartFile(t, "file", "statement.xlsx", xlsx)
		req, _ := http.NewRequest("POST", url, body)
		req.Header.Set("Content-Type", ct)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("імпорт %d: %v", resp.StatusCode, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("імпорт: %d %v", resp.StatusCode, out)
		}
		return out
	}

	// Ручний рух тієї ж суми має бути НАЗВАНИЙ конфліктом: поки обліку
	// фондів не було, купівлі сертифікатів доводилось записувати як
	// зняття, і тепер така пара стала б подвійним рахунком.
	if resp, b := do(t, "POST", srv.URL+"/api/deposits",
		`{"amount":"-55.56","currency":"UAH","date":"2026-07-21","broker":"inzhur"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("ручне зняття: %d %s", resp.StatusCode, b)
	}
	{
		var found string
		for _, r := range post(true)["rows"].([]any) {
			m := r.(map[string]any)
			if c, _ := m["conflict"].(string); c != "" {
				found = c
			}
		}
		if found == "" {
			t.Error("імпорт мав попередити про ручний рух тієї ж суми")
		}
	}

	// Прогін без запису нічого не змінює.
	preview := post(true)
	// купівля сертифікатів, дивіденд, поповнення, купівля облігації
	if preview["new"].(float64) != 4 {
		t.Errorf("превʼю мало знайти 4 нові операції, маємо %v", preview["new"])
	}
	if preview["imported"].(float64) != 0 {
		t.Errorf("режим превʼю не мав нічого записати, маємо %v", preview["imported"])
	}
	if _, body := do(t, "GET", srv.URL+"/api/funds", ""); !strings.Contains(body, "[]") {
		t.Errorf("після превʼю журнал мав лишитись порожнім: %s", body)
	}

	first := post(false)
	if first["imported"].(float64) != 4 {
		t.Errorf("перший імпорт мав записати 4 операції, маємо %v", first["imported"])
	}
	if sk, _ := first["skipped"].([]any); len(sk) != 0 {
		t.Errorf("у цій виписці пропускати нічого, маємо %v", sk)
	}
	// Облігація має стати лотом із ціною, виведеною із суми.
	_, lotsBody := do(t, "GET", srv.URL+"/api/lots", "")
	if !strings.Contains(lotsBody, "UA4000237416") || !strings.Contains(lotsBody, "1032.46") {
		t.Errorf("облігація мала стати лотом: %s", lotsBody)
	}

	second := post(false)
	if second["imported"].(float64) != 0 {
		t.Errorf("повторний імпорт не мав записати нічого, маємо %v", second["imported"])
	}

	// Позиція після двох імпортів — така сама, як після одного.
	var got struct {
		Funds []struct {
			Qty          int64   `json:"qty"`
			DividendsNet float64 `json:"dividends_net"`
		} `json:"funds"`
		Accounts map[string]float64 `json:"accounts"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Funds) != 1 || got.Funds[0].Qty != 5 {
		t.Errorf("після двох імпортів мало лишитись 5 сертифікатів: %+v", got.Funds)
	}
	// дивіденд 18.99 із податком 2.66 -> 16.33 чистими, один раз
	if len(got.Funds) == 1 && math.Abs(got.Funds[0].DividendsNet-16.33) > 0.01 {
		t.Errorf("дивіденд мав врахуватись один раз чистим: %v", got.Funds[0].DividendsNet)
	}
	// Гаманець має рухатись від УСІХ операцій фонду, а не лише від
	// поповнень: 300 (поповнення) − 55.56 (купівля) + 16.33 (дивіденд
	// чистими) = 260.77. І кожна з них — рівно один раз.
	// 300 (поповнення) − 55.56 (сертифікати) + 16.33 (дивіденд чистими)
	// − 1032.46 (облігація з виписки)
	// − 55.56 (той самий ручний рух, який імпорт і назвав конфліктом:
	// він лишається в базі, доки користувач його не прибере)
	if v := got.Accounts["UAH"]; math.Abs(v-(205.21-1032.46)) > 0.01 {
		t.Errorf("баланс: очікували %.2f, маємо %v", 205.21-1032.46, v)
	}
}

// Облігація, уже внесена вручну, не має задвоїтись імпортом: ключ —
// папір, дата й кількість, а не ціна, бо ручний запис міг бути округлений
// інакше й від того не став іншою купівлею.
func TestImportSkipsAlreadyEnteredBond(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	importSince(t, st, "2020-01-01")
	if resp, b := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":1,"price_per_bond":"1032.40","buy_date":"2026-07-20","channel":"inzhur"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("ручний лот: %d %s", resp.StatusCode, b)
	}
	xlsx := buildXLSX(t, [][]string{
		{"Дата", "Тип операції", "Вид цінного паперу", "Дебет", "Кредит"},
		{"46223.376238425924", "Купівля 1 облігації", "ОВДП UA4000227748", "", "1032.46"},
	})
	body, ct := multipartFile(t, "file", "s.xlsx", xlsx)
	req, _ := http.NewRequest("POST", srv.URL+"/api/import/inzhur", body)
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Imported int `json:"imported"`
		Rows     []struct {
			Exists bool `json:"exists"`
		} `json:"rows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Imported != 0 || len(out.Rows) != 1 || !out.Rows[0].Exists {
		t.Errorf("вже внесена облігація мала позначитись «вже є»: %+v", out)
	}
	_, lots := do(t, "GET", srv.URL+"/api/lots", "")
	if strings.Count(lots, "UA4000227748") != 1 {
		t.Errorf("лот задвоївся: %s", lots)
	}
}

// Ручний рух міг бути записаний іншою датою й на іншому брокері — саме
// так виглядає «вирівнювання балансу» постфактум. Детектор має ловити і
// такий випадок, інакше він мовчить рівно там, де потрібен найбільше.
func TestImportConflictAcrossDatesAndBrokers(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	importSince(t, st, "2020-01-01")

	// продаж 72 сертифікатів стався 20-го на inzhur…
	xlsx := buildXLSX(t, [][]string{
		{"Дата", "Тип операції", "Вид цінного паперу", "Дебет", "Кредит"},
		{"46223.368946759256", "Продаж 72 сертифікатів", "Inzhur REIT", "798.3"},
	})
	// …а записаний вручну 22-го й на mono
	if resp, b := do(t, "POST", srv.URL+"/api/deposits",
		`{"amount":"798.30","currency":"UAH","date":"2026-07-22","broker":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("ручний рух: %d %s", resp.StatusCode, b)
	}

	body, ct := multipartFile(t, "file", "s.xlsx", xlsx)
	req, _ := http.NewRequest("POST", srv.URL+"/api/import/inzhur?dry=1", body)
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Rows []struct {
			Kind     string `json:"kind"`
			Conflict string `json:"conflict"`
		} `json:"rows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 {
		t.Fatalf("очікували один рядок, маємо %+v", out.Rows)
	}
	if out.Rows[0].Conflict == "" {
		t.Errorf("продаж 20-го й ручний рух 22-го — це та сама сума, мав бути конфлікт")
	}
}

// Поповнив і того ж дня купив на ту саму суму — це нормальний рух
// грошей, а не подвоєння. Детектор має мовчати: хибні тривоги псують
// довіру до нього саме тоді, коли трапиться справжня.
func TestImportNoConflictWhenDepositFundsPurchase(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	importSince(t, st, "2020-01-01")
	// поповнення +8051.74 того ж дня, що й купівля на 8051.74
	if resp, b := do(t, "POST", srv.URL+"/api/deposits",
		`{"amount":"8051.74","currency":"UAH","date":"2024-04-02","broker":"inzhur"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("поповнення: %d %s", resp.StatusCode, b)
	}
	xlsx := buildXLSX(t, [][]string{
		{"Дата", "Тип операції", "Вид цінного паперу", "Дебет", "Кредит"},
		{"45384.5", "Купівля 2 сертифікатів", "Inzhur Ocean", "", "8051.74"},
	})
	body, ct := multipartFile(t, "file", "s.xlsx", xlsx)
	req, _ := http.NewRequest("POST", srv.URL+"/api/import/inzhur?dry=1", body)
	req.Header.Set("Content-Type", ct)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Rows []struct{ Conflict string `json:"conflict"` } `json:"rows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 {
		t.Fatalf("очікували один рядок: %+v", out.Rows)
	}
	if out.Rows[0].Conflict != "" {
		t.Errorf("поповнення НА купівлю — не подвоєння, конфлікту бути не мало: %s", out.Rows[0].Conflict)
	}
}

// Водяний знак: усе, що старше за дату останнього імпорту, ігнорується
// незалежно від вмісту. Виписка щомісяця приносить повну історію, і
// покладатись лише на дедуплікацію не можна — після ручного підчищення
// журналу старі рядки почали проситися назад.
//
// Дата — це ДЕНЬ ЗАПУСКУ імпорту, а не найпізніший рядок у файлі: файл
// може закінчуватись раніше, ніж його завантажили.
func TestImportIgnoresRowsOlderThanWatermark(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)

	// 46213 = 2026-07-10, 46224 = 2026-07-21.
	xlsx := buildXLSX(t, [][]string{
		{"Дата", "Тип операції", "Вид цінного паперу", "Дебет", "Кредит"},
		{"46213", "Нарахування дивідендів", "Inzhur REIT", "18.99"},
		{"46224.65980324074", "Купівля 5 сертифікатів", "Inzhur REIT", "", "55.56"},
	})
	post := func(dry bool) map[string]any {
		t.Helper()
		url := srv.URL + "/api/import/inzhur"
		if dry {
			url += "?dry=1"
		}
		body, ct := multipartFile(t, "file", "statement.xlsx", xlsx)
		req, _ := http.NewRequest("POST", url, body)
		req.Header.Set("Content-Type", ct)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// Знак посеред файлу: дивіденд 10 липня старший, купівля 21-го — ні.
	importSince(t, st, "2026-07-15")
	prev := post(true)
	if n, _ := prev["before"].(float64); n != 1 {
		t.Errorf("відсіяно як старі %v рядків, хочемо 1", prev["before"])
	}
	if rows, _ := prev["rows"].([]any); len(rows) != 1 {
		t.Errorf("до розгляду взято %d рядків, хочемо 1", len(rows))
	}
	// Перегляд не має рухати знак: інакше кнопка «Переглянути» тихо
	// з'їдала б період, і справжній імпорт уже нічого не побачив би.
	if got, _ := st.GetSetting(context.Background(), "import_since"); got != "2026-07-15" {
		t.Errorf("перегляд зсунув знак на %q", got)
	}

	// Справжній імпорт рухає знак на СЬОГОДНІ, а не на 2026-07-21.
	post(false)
	today := string(domain.NewDate(time.Now()))
	got, _ := st.GetSetting(context.Background(), "import_since")
	if got != today {
		t.Errorf("після імпорту знак %q, хочемо день запуску %q", got, today)
	}

	// Повторний імпорт того самого файлу вже не бачить нічого.
	again := post(true)
	if rows, _ := again["rows"].([]any); len(rows) != 0 {
		t.Errorf("після зсуву знака файл дав %d рядків, хочемо 0", len(rows))
	}
}

// Купон, датований СЬОГОДНІ, сам на рахунок не лягає — і це навмисно:
// графік НБУ каже, коли виплата ПОВИННА прийти, а не коли прийшла.
// Позначка «отримано» в календарі і є способом сказати «вже прийшли»,
// після чого сума лягає на рахунок ТОГО брокера, через якого куплено
// папір, пропорційно кількості паперів у нього.
func TestTodayCouponCreditsBrokerOnlyWhenMarked(t *testing.T) {
	srv, st := testServer(t)
	today := domain.NewDate(time.Now())
	const isin = "UA4000239016"
	if err := st.ReplaceDirectory(context.Background(), []nbu.Security{{
		Bond: domain.Bond{ISIN: isin, Nominal: money.New(100000, money.UAH),
			RateBP: 1500, Maturity: "2028-01-01", Descr: "тестові"},
		Payments: []domain.Payment{{ISIN: isin, PayDate: today,
			Type: domain.PayCoupon, PerBond: money.New(7575, money.UAH)}},
	}}, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Куплено вчора: на дату виплати папери вже у власності. Два папери
	// в mono, один в inzhur — щоб побачити не лише «гроші прийшли», а й
	// що вони прийшли КОЖНОМУ своєму брокеру за його кількістю.
	buy := string(today.AddDays(-1))
	for _, lot := range []string{
		`{"isin":"` + isin + `","qty":2,"price_per_bond":"1000.00","buy_date":"` + buy + `","channel":"mono"}`,
		`{"isin":"` + isin + `","qty":1,"price_per_bond":"1000.00","buy_date":"` + buy + `","channel":"inzhur"}`,
	} {
		if resp, body := do(t, "POST", srv.URL+"/api/lots", lot); resp.StatusCode != http.StatusCreated {
			t.Fatalf("лот: %d %s", resp.StatusCode, body)
		}
	}

	balances := func() map[string]map[string]float64 {
		t.Helper()
		_, body := do(t, "GET", srv.URL+"/api/summary", "")
		var doc struct {
			Brokers map[string]map[string]float64 `json:"brokers"`
		}
		if err := json.Unmarshal([]byte(body), &doc); err != nil {
			t.Fatalf("summary: %v: %s", err, body)
		}
		return doc.Brokers
	}

	// До позначки видно лише витрати на купівлю: купон сьогоднішній.
	before := balances()
	if got := before["mono"]["UAH"]; got != -2000 {
		t.Fatalf("до позначки mono має бути -2000, маємо %v", got)
	}
	if got := before["inzhur"]["UAH"]; got != -1000 {
		t.Fatalf("до позначки inzhur має бути -1000, маємо %v", got)
	}

	if resp, body := do(t, "POST", srv.URL+"/api/payments/status",
		`{"isin":"`+isin+`","pay_date":"`+string(today)+`","status":"received"}`,
	); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("позначка: %d %s", resp.StatusCode, body)
	}

	// mono: 2 × 75.75 = 151.50, inzhur: 1 × 75.75 = 75.75
	after := balances()
	if got := after["mono"]["UAH"]; got != -1848.5 {
		t.Errorf("після позначки mono має бути -1848.5, маємо %v", got)
	}
	if got := after["inzhur"]["UAH"]; got != -924.25 {
		t.Errorf("після позначки inzhur має бути -924.25, маємо %v", got)
	}

	// Скасування позначки повертає баланс РІВНО до того, що був до неї:
	// раз позначка рухає гроші, помилковий клік має бути оборотним.
	if resp, body := do(t, "POST", srv.URL+"/api/payments/status",
		`{"isin":"`+isin+`","pay_date":"`+string(today)+`","status":"none"}`,
	); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("скасування: %d %s", resp.StatusCode, body)
	}
	cleared := balances()
	if got := cleared["mono"]["UAH"]; got != -2000 {
		t.Errorf("після скасування mono має бути -2000, маємо %v", got)
	}
	if got := cleared["inzhur"]["UAH"]; got != -1000 {
		t.Errorf("після скасування inzhur має бути -1000, маємо %v", got)
	}
}

// Розміщення вкладу СПИСУЄ тіло з рахунку банку, а позначена як отримана
// виплата відсотків його кредитує — той самий arrived(), що й для купонів.
func TestTermDepositMovesAccount(t *testing.T) {
	srv, st := testServer(t)
	// Поповнюємо банк, щоб було з чого класти вклад.
	if _, err := st.AddDeposit(context.Background(), store.Deposit{
		Date: "2026-01-10", Amount: 10000000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	// 100 000 ₴ під 16%, виплата в кінці, відкрито в минулому.
	open := domain.NewDate(time.Now()).AddDays(-40)
	mat := domain.NewDate(time.Now()).AddDays(325)
	body := `{"bank":"ПУМБ","currency":"UAH","principal":"100000.00","rate_pct":"16",` +
		`"open_date":"` + string(open) + `","maturity_date":"` + string(mat) + `","payout":"end"}`
	if resp, b := do(t, "POST", srv.URL+"/api/term-deposits", body); resp.StatusCode != http.StatusCreated {
		t.Fatalf("створення вкладу: %d %s", resp.StatusCode, b)
	}

	bankUAH := func() float64 {
		t.Helper()
		_, b := do(t, "GET", srv.URL+"/api/summary", "")
		var doc struct {
			Brokers map[string]map[string]float64 `json:"brokers"`
		}
		if err := json.Unmarshal([]byte(b), &doc); err != nil {
			t.Fatalf("summary: %v", err)
		}
		return doc.Brokers["ПУМБ"]["UAH"]
	}

	// Поповнили 100 000, вклад замкнув 100 000 → на рахунку 0.
	if got := bankUAH(); got != 0 {
		t.Errorf("після розміщення баланс ПУМБ має бути 0, маємо %v", got)
	}

	// Список вкладів віддає створений.
	if _, b := do(t, "GET", srv.URL+"/api/term-deposits", ""); !strings.Contains(b, `"rate_pct":16`) {
		t.Errorf("список вкладів: %s", b)
	}
}
