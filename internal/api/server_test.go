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
	"strconv"
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

	// Поповнення на 100к (минулою датою) СПИСУЄ ще 100к з банку → −100к,
	// але спершу докладемо на рахунок, щоб бачити чистий ефект поповнення.
	if _, err := st.AddDeposit(context.Background(), store.Deposit{
		Date: "2026-01-11", Amount: 10000000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	// зараз: рахунок 100к (щойно долили), вклад замкнув перші 100к → 100к вільних
	if got := bankUAH(); got != 100000 {
		t.Fatalf("перед поповненням очікували 100000 вільних, маємо %v", got)
	}
	topupDate := string(domain.NewDate(time.Now()).AddDays(-5))
	if resp, b := do(t, "POST", srv.URL+"/api/term-deposits/1/topups",
		`{"date":"`+topupDate+`","amount":"100000.00"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("поповнення: %d %s", resp.StatusCode, b)
	}
	// поповнення замкнуло ще 100к → рахунок знову 0
	if got := bankUAH(); got != 0 {
		t.Errorf("після поповнення баланс ПУМБ має бути 0, маємо %v", got)
	}
	// deposits_uah = накопичене тіло 200к
	if _, b := do(t, "GET", srv.URL+"/api/summary", ""); !strings.Contains(b, `"deposits_uah":200000`) {
		t.Errorf("deposits_uah має бути 200000 після поповнення: %s", b)
	}
	// список вкладу віддає баланс 200к і одне поповнення
	if _, b := do(t, "GET", srv.URL+"/api/term-deposits", ""); !strings.Contains(b, `"topups"`) {
		t.Errorf("вклад має віддавати topups: %s", b)
	}
}

// Вклад входить у капітал, календар і драбину — не лише в баланс рахунку.
func TestTermDepositFlowsIntoAggregates(t *testing.T) {
	srv, st := testServer(t)
	if _, err := st.AddDeposit(context.Background(), store.Deposit{
		Date: "2026-01-10", Amount: 20000000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	open := domain.NewDate(time.Now()).AddDays(-30)
	mat := domain.NewDate(time.Now()).AddDays(335)
	body := `{"bank":"ПУМБ","currency":"UAH","principal":"100000.00","rate_pct":"16",` +
		`"open_date":"` + string(open) + `","maturity_date":"` + string(mat) + `","payout":"end"}`
	if resp, b := do(t, "POST", srv.URL+"/api/term-deposits", body); resp.StatusCode != http.StatusCreated {
		t.Fatalf("вклад: %d %s", resp.StatusCode, b)
	}

	// summary: капітал бачить вклад через deposits_uah.
	_, s := do(t, "GET", srv.URL+"/api/summary", "")
	if !strings.Contains(s, `"deposits_uah":100000`) {
		t.Errorf("summary не містить deposits_uah=100000: %s", s)
	}

	// календар: майбутні потоки вкладу — відсоток і повернення тіла — з
	// синтетичним ISIN deposit:N.
	_, c := do(t, "GET", srv.URL+"/api/calendar?from=1970-01-01", "")
	if !strings.Contains(c, `"deposit:1"`) {
		t.Errorf("календар не містить потоків вкладу: %s", c)
	}

	// драбина: тіло повертається року погашення (Nominal — у мінорних,
	// 100000.00 ₴ = 10000000).
	_, l := do(t, "GET", srv.URL+"/api/ladder", "")
	matYear := mat.Year()
	if !strings.Contains(l, `"Year":`+strconv.Itoa(matYear)) || !strings.Contains(l, `"Nominal":10000000`) {
		t.Errorf("драбина не містить тіла вкладу на %d: %s", matYear, l)
	}
}

// Помічник ранжує за РЕАЛЬНОЮ ДОХІДНІСТЮ, а не за ціною кроку.
//
// Це головна гарантія справедливості між інструментами: сертифікат фонду
// коштує копійки й тому «доступний завжди», але це не робить його
// найкращим. Вклад під 16% після податку б'є фонд під 8%, попри те, що
// поповнення вкладу коштує в тисячі разів дорожче.
func TestReinvestRanksYieldNotPrice(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	// Гроші в банку — щоб і поповнення вкладу, і сертифікат були по кишені.
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2026-01-10", Amount: 50000000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	// Вклад 100к під 16%, податок 19.5% → нетто-ставка 12.88%.
	open := domain.NewDate(time.Now()).AddDays(-10)
	mat := domain.NewDate(time.Now()).AddDays(355)
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 10000000, RateBP: 1600,
		OpenDate: open, MaturityDate: mat, Payout: domain.PayoutEnd, TaxBP: 1950,
		Replenishable: true, // інакше помічник поповнення не запропонує
	}); err != nil {
		t.Fatal(err)
	}
	// Фонд: сертифікат за 10 ₴ з дивідендною дохідністю ~8% нетто.
	// Купівля + дивіденд дають позицію з ціною й дохідністю.
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: domain.NewDate(time.Now()).AddDays(-40), Fund: "Inzhur", Kind: domain.FundBuy,
		Qty: 1000, Amount: 1000000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: domain.NewDate(time.Now()).AddDays(-10), Fund: "Inzhur", Kind: domain.FundDividend,
		Amount: 6800, Tax: 0, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	// Знецінення 0 — щоб порівнювати самі ставки без приведення.
	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"uah_devaluation_pct":"0","reinvest_rank":"rate"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("settings: %d %s", resp.StatusCode, b)
	}

	type row struct {
		Kind       string  `json:"kind"`
		Label      string  `json:"label"`
		RealPct    float64 `json:"real_pct"`
		CanBuy     bool    `json:"can_buy"`
		YieldBasis string  `json:"yield_basis"`
	}
	var rows []row
	_, body := do(t, "GET", srv.URL+"/api/reinvest", "")
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("reinvest: %v: %s", err, body)
	}

	byKind := map[string]row{}
	for _, r := range rows {
		if _, seen := byKind[r.Kind]; !seen {
			byKind[r.Kind] = r
		}
	}
	dep, okD := byKind["deposit"]
	fund, okF := byKind["fund"]
	if !okD || !okF {
		t.Fatalf("очікували пропозиції kind=deposit і kind=fund, маємо %+v", rows)
	}
	// Обидва по кишені (500к на рахунку).
	if !dep.CanBuy || !fund.CanBuy {
		t.Errorf("обидва мали бути доступні: вклад=%v фонд=%v", dep.CanBuy, fund.CanBuy)
	}
	// Вклад дохідніший — 12.88% проти ~8%.
	if !(dep.RealPct > fund.RealPct) {
		t.Errorf("вклад (%.2f%%) має бути дохідніший за фонд (%.2f%%)", dep.RealPct, fund.RealPct)
	}
	// І саме тому стоїть ВИЩЕ, попри те, що коштує 100к проти 10 ₴.
	depIdx, fundIdx := -1, -1
	for i, r := range rows {
		if r.Kind == "deposit" && depIdx < 0 {
			depIdx = i
		}
		if r.Kind == "fund" && fundIdx < 0 {
			fundIdx = i
		}
	}
	if depIdx > fundIdx {
		t.Errorf("дорожчий, але дохідніший вклад (#%d) має стояти вище за дешевший фонд (#%d)", depIdx, fundIdx)
	}
	// Природа дохідності названа чесно.
	if dep.YieldBasis == "" || fund.YieldBasis == "" {
		t.Errorf("yield_basis має бути заповнений: вклад=%q фонд=%q", dep.YieldBasis, fund.YieldBasis)
	}
}

// «Не перевкладено» бачить і вклади, не лише ОВДП: надійшлий відсоток
// лежить без діла так само, як купон, і зникає з плитки лише коли
// позначений «перевкладено».
func TestUninvestedCountsDepositInterest(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2026-01-10", Amount: 20000000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	// Вклад із ЩОМІСЯЧНОЮ виплатою, відкритий пів року тому: кілька
	// відсоткових виплат уже минули, отже надійшли.
	open := domain.NewDate(time.Now()).AddDays(-180)
	mat := domain.NewDate(time.Now()).AddDays(185)
	depID, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 10000000, RateBP: 1600,
		OpenDate: open, MaturityDate: mat, Payout: domain.PayoutMonthly, TaxBP: 1950,
	})
	if err != nil {
		t.Fatal(err)
	}

	uninvested := func() float64 {
		t.Helper()
		_, b := do(t, "GET", srv.URL+"/api/summary", "")
		var doc struct {
			UninvestedUAH float64 `json:"uninvested_uah"`
		}
		if err := json.Unmarshal([]byte(b), &doc); err != nil {
			t.Fatalf("summary: %v", err)
		}
		return doc.UninvestedUAH
	}

	// Минулі відсотки вкладу вже лежать на рахунку й не перевкладені.
	before := uninvested()
	if before <= 0 {
		t.Fatalf("відсотки вкладу мали потрапити в «не перевкладено», маємо %v", before)
	}

	// І зникають самі, коли гроші пішли в діло. Поповнюємо вклад на суму,
	// більшу за весь накопичений відсоток: жодного кліка, число падає в
	// нуль. Доти це вимагало позначити КОЖНУ виплату вручну.
	if _, err := st.AddDepositTopup(ctx, domain.DepositTopup{
		DepositID: depID, Date: domain.NewDate(time.Now()), Amount: 5000000,
	}); err != nil {
		t.Fatal(err)
	}
	if after := uninvested(); after != 0 {
		t.Errorf("поповнення на 50 000 ₴ мало з'їсти весь відсоток (%.2f), лишилось %.2f",
			before, after)
	}
}

// Стеля балансом. Черга сама по собі знає лише історію надходжень: якщо
// гроші зняли з рахунку, вона й далі рахувала б їх простоєм. Тож число
// не може перевищувати того, що реально лежить.
func TestUninvestedCappedByAccountBalance(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	// Вклад із щомісячною виплатою, відкритий пів року тому: кілька
	// відсоткових виплат уже надійшли й лежать на рахунку.
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2026-01-10", Amount: 20000000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 10000000, RateBP: 1600,
		OpenDate:     domain.NewDate(time.Now()).AddDays(-180),
		MaturityDate: domain.NewDate(time.Now()).AddDays(185),
		Payout:       domain.PayoutMonthly, TaxBP: 1950,
	}); err != nil {
		t.Fatal(err)
	}
	uninvested := func() float64 {
		t.Helper()
		_, b := do(t, "GET", srv.URL+"/api/summary", "")
		var doc struct {
			UninvestedUAH float64 `json:"uninvested_uah"`
			AccountUAH    float64 `json:"account_uah"`
		}
		if err := json.Unmarshal([]byte(b), &doc); err != nil {
			t.Fatalf("summary: %v", err)
		}
		// Інваріант: простій ніколи не більший за те, що лежить. Рахунок
		// у мінусі (зняли більше, ніж було) означає простій 0, а не
		// від'ємний — боргу «без діла» не буває.
		if cap := math.Max(0, doc.AccountUAH); doc.UninvestedUAH > cap+0.01 {
			t.Errorf("простій (%.2f) не може бути більший за рахунок (%.2f)",
				doc.UninvestedUAH, doc.AccountUAH)
		}
		return doc.UninvestedUAH
	}
	if uninvested() <= 0 {
		t.Fatal("минулі відсотки мали потрапити в простій")
	}
	// А тепер знімаємо з рахунку все: грошей немає — простою теж.
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: domain.NewDate(time.Now()), Amount: -20000000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	if got := uninvested(); got != 0 {
		t.Errorf("рахунок порожній, простій мав бути 0, маємо %.2f", got)
	}
}

// Помічник пропонує докласти ЛИШЕ в поповнюваний вклад: порада докласти
// у вклад, який поповнень не приймає, — порада, яку неможливо виконати.
func TestReinvestSuggestsOnlyReplenishableDeposits(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2026-01-10", Amount: 50000000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	open := domain.NewDate(time.Now()).AddDays(-10)
	mat := domain.NewDate(time.Now()).AddDays(355)
	base := domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 10000000, RateBP: 1600,
		OpenDate: open, MaturityDate: mat, Payout: domain.PayoutEnd, TaxBP: 1950,
	}
	if _, err := st.AddTermDeposit(ctx, base); err != nil { // НЕ поповнюваний
		t.Fatal(err)
	}

	kinds := func() []string {
		t.Helper()
		var rows []struct {
			Kind string `json:"kind"`
		}
		_, b := do(t, "GET", srv.URL+"/api/reinvest", "")
		if err := json.Unmarshal([]byte(b), &rows); err != nil {
			t.Fatalf("reinvest: %v: %s", err, b)
		}
		var out []string
		for _, r := range rows {
			out = append(out, r.Kind)
		}
		return out
	}

	for _, k := range kinds() {
		if k == "deposit" {
			t.Fatal("непоповнюваний вклад не має потрапляти в поради")
		}
	}

	// Той самий вклад, позначений поповнюваним, — уже потрапляє.
	repl := base
	repl.Replenishable = true
	if _, err := st.AddTermDeposit(ctx, repl); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, k := range kinds() {
		if k == "deposit" {
			found = true
		}
	}
	if !found {
		t.Error("поповнюваний вклад мав потрапити в поради")
	}
}

// seedRateHistory кладе дві точки курсу так, щоб між ними був рівно
// заданий річний темп. Backfill у тестах не потрібен — важлива не дорога
// до даних, а те, що застосунок їх читає.
func seedRateHistory(t *testing.T, st *store.Store, years int, oldE4, newE4 int64) {
	t.Helper()
	ctx := context.Background()
	old := domain.NewDate(time.Now().AddDate(-years, 0, 0))
	if err := st.SaveRate(ctx, "USD", oldE4, old); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRate(ctx, "USD", newE4, domain.NewDate(time.Now())); err != nil {
		t.Fatal(err)
	}
}

// Знецінення береться з реальних курсів, а не з припущеної шістки.
// Курси справжні (24.00 ₴/$ на початку 2016 проти 44.81 сьогодні), але
// рознесені рівно на 10 років, тому темп виходить 6.44%, а не 6.1% з
// живого вікна: там між точками 3854 дні, тут 3652. Точний перерахунок
// на справжніх датах перевіряє TestAnnualPctOnRealRates.
func TestDevaluationMeasuredFromRates(t *testing.T) {
	srv, st := testServer(t)
	seedRateHistory(t, st, 10, 240007, 448110)

	var got struct {
		EffectivePct float64 `json:"effective_pct"`
		Source       string  `json:"source"`
		Windows      []struct {
			Years int     `json:"years"`
			Pct   float64 `json:"pct"`
		} `json:"windows"`
	}
	_, body := do(t, "GET", srv.URL+"/api/devaluation", "")
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("devaluation: %v: %s", err, body)
	}
	if got.Source != "measured" {
		t.Errorf("джерело = %q, очікували measured: %s", got.Source, body)
	}
	if math.Abs(got.EffectivePct-6.44) > 0.1 {
		t.Errorf("виміряне знецінення = %.2f%%, очікували ≈6.44%%", got.EffectivePct)
	}
	if len(got.Windows) == 0 {
		t.Errorf("вікна мали порахуватись: %s", body)
	}

	// І воно справді доходить до дохідностей, а не лишається довідкою.
	if resp, b := do(t, "POST", srv.URL+"/api/term-deposits",
		`{"bank":"ПУМБ","currency":"UAH","principal":"100000","rate_pct":"16",
		  "open_date":"2026-07-01","maturity_date":"2027-07-01","payout":"end","tax_pct":"0"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("вклад: %d %s", resp.StatusCode, b)
	}
	var deps []struct {
		RealPct float64 `json:"real_pct"`
	}
	_, body = do(t, "GET", srv.URL+"/api/term-deposits", "")
	if err := json.Unmarshal([]byte(body), &deps); err != nil {
		t.Fatalf("term-deposits: %v: %s", err, body)
	}
	// 16% без податку при знеціненні 6.44% → (1.16/1.0644)-1 ≈ 8.98%.
	// Головне тут не сама цифра, а те, що вона порахована з КУРСІВ:
	// на припущеній шістці вийшло б 9.43%.
	if len(deps) != 1 || math.Abs(deps[0].RealPct-8.98) > 0.15 {
		t.Errorf("реальна ставка вкладу = %+v, очікували ≈8.98%%", deps)
	}
}

// Ручне значення важить більше за виміряне: людина може мати підстави, і
// відбирати в неї це право автоматика не повинна.
func TestDevaluationManualWins(t *testing.T) {
	srv, st := testServer(t)
	seedRateHistory(t, st, 10, 240007, 448110)
	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"uah_devaluation_pct":"15"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("settings: %d %s", resp.StatusCode, b)
	}
	var got struct {
		EffectivePct float64 `json:"effective_pct"`
		Source       string  `json:"source"`
	}
	_, body := do(t, "GET", srv.URL+"/api/devaluation", "")
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("devaluation: %v: %s", err, body)
	}
	if got.Source != "manual" || got.EffectivePct != 15 {
		t.Errorf("ручні 15%% мали перемогти виміряне: %+v", got)
	}

	// Порожнє значення повертає на виміряне — це і є «скинути».
	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"uah_devaluation_pct":""}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("settings: %d %s", resp.StatusCode, b)
	}
	_, body = do(t, "GET", srv.URL+"/api/devaluation", "")
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("devaluation: %v: %s", err, body)
	}
	if got.Source != "measured" {
		t.Errorf("після очищення мало повернутись на виміряне: %+v", got)
	}
}

// Бенчмарк «а якби долари»: кожне поповнення переводиться в долари за
// курсом СВОГО дня. Саме це й робить порівняння чесним — інакше вийшло б,
// що ти купував валюту заднім числом за сьогоднішнім курсом.
func TestBenchmarkBuysAtEachDayRate(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	// Історія курсу: 25 ₴/$ торік, 50 ₴/$ сьогодні.
	if err := st.SaveRate(ctx, "USD", 250000, "2025-06-01"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRate(ctx, "USD", 500000, domain.NewDate(time.Now())); err != nil {
		t.Fatal(err)
	}
	// Два поповнення по 10 000 ₴: одне за курсом 25, друге за 50.
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2025-06-15", Amount: 1000000, Currency: "UAH", Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: domain.NewDate(time.Now()), Amount: 1000000, Currency: "UAH", Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}

	var b struct {
		PortfolioUAH float64 `json:"portfolio_uah"`
		BenchmarkUAH float64 `json:"benchmark_uah"`
		DiffUAH      float64 `json:"diff_uah"`
		USDBought    float64 `json:"usd_bought"`
		RateNow      float64 `json:"rate_now"`
	}
	_, body := do(t, "GET", srv.URL+"/api/benchmark", "")
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		t.Fatalf("benchmark: %v: %s", err, body)
	}
	// 10000/25 = 400 $, 10000/50 = 200 $, разом 600 $.
	if math.Abs(b.USDBought-600) > 0.01 {
		t.Errorf("куплено доларів = %.2f, очікували 600 (400 по 25 + 200 по 50)", b.USDBought)
	}
	// Сьогодні ті 600 $ коштують 30 000 ₴ — утричі більше за половину
	// внесеного, бо перша половина купувалась удвічі дешевше.
	if math.Abs(b.BenchmarkUAH-30000) > 0.01 {
		t.Errorf("бенчмарк = %.2f ₴, очікували 30 000", b.BenchmarkUAH)
	}
	// Портфель — самі гроші на рахунку (20 000 ₴), тож долари виграли.
	if math.Abs(b.PortfolioUAH-20000) > 0.01 {
		t.Errorf("портфель = %.2f ₴, очікували 20 000", b.PortfolioUAH)
	}
	if b.DiffUAH >= 0 {
		t.Errorf("гривня на рахунку мала програти долару, різниця %.2f", b.DiffUAH)
	}
}

// Зняття зменшує «куплені» долари так само, як і в житті: інакше
// бенчмарк рахував би гроші, яких ти вже не маєш.
func TestBenchmarkWithdrawalsReduceIt(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	if err := st.SaveRate(ctx, "USD", 400000, "2025-01-01"); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRate(ctx, "USD", 400000, domain.NewDate(time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2025-02-01", Amount: 4000000, Currency: "UAH", Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2025-03-01", Amount: -1000000, Currency: "UAH", Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	var b struct {
		USDBought float64 `json:"usd_bought"`
	}
	_, body := do(t, "GET", srv.URL+"/api/benchmark", "")
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		t.Fatalf("benchmark: %v: %s", err, body)
	}
	// (40000 − 10000) / 40 = 750 $.
	if math.Abs(b.USDBought-750) > 0.01 {
		t.Errorf("куплено доларів = %.2f, очікували 750", b.USDBought)
	}
}

// Ліквідність розкладає гроші за строками. Головна пастка тут — подвійний
// облік: вклад, що гаситься у вікні, приходить потоком, а вклад із
// дальшим строком стоїть у «замкнено», і жоден не має потрапити в обидва.
func TestLiquidityBuckets(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2026-01-10", Amount: 30000000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	// Вклад із погашенням через 200 днів — далеко за межами обох вікон.
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 10000000, RateBP: 1600,
		OpenDate:     domain.NewDate(time.Now()).AddDays(-10),
		MaturityDate: domain.NewDate(time.Now()).AddDays(200),
		Payout:       domain.PayoutEnd, TaxBP: 1950,
	}); err != nil {
		t.Fatal(err)
	}
	var sum struct {
		AccountUAH float64 `json:"account_uah"`
		Liquidity  *struct {
			NowUAH     float64 `json:"now_uah"`
			In30UAH    float64 `json:"in_30_uah"`
			In90UAH    float64 `json:"in_90_uah"`
			LockedUAH  float64 `json:"locked_uah"`
			UnlockDate string  `json:"unlock_date"`
		} `json:"liquidity"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	if sum.Liquidity == nil {
		t.Fatalf("блок ліквідності мав з'явитись: %s", body)
	}
	l := sum.Liquidity
	if l.NowUAH != sum.AccountUAH {
		t.Errorf("«зараз» (%.2f) має дорівнювати балансу рахунків (%.2f)", l.NowUAH, sum.AccountUAH)
	}
	// Виплата в кінці строку — у вікна не потрапляє нічого, тож обидва
	// дорівнюють поточному балансу.
	if l.In30UAH != l.NowUAH || l.In90UAH != l.NowUAH {
		t.Errorf("у вікна не мало потрапити нічого: зараз %.2f, 30 %.2f, 90 %.2f",
			l.NowUAH, l.In30UAH, l.In90UAH)
	}
	// Тіло — замкнене, і сказано, коли відкриється.
	if math.Abs(l.LockedUAH-100000) > 0.01 {
		t.Errorf("тіло вкладу мало бути замкнене: %.2f", l.LockedUAH)
	}
	if l.UnlockDate == "" {
		t.Error("дата відкриття мала бути названа")
	}
	// І воно НЕ входить у доступне — інакше та сама сотня стояла б двічі.
	if l.In90UAH >= l.NowUAH+100000 {
		t.Errorf("замкнене тіло потрапило і в доступне: 90 днів = %.2f", l.In90UAH)
	}
}

// Вклад, що гаситься В МЕЖАХ вікна, навпаки — доступний, і в «замкнено»
// його бути не має.
func TestLiquidityNearMaturityIsAvailable(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2026-01-10", Amount: 30000000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 10000000, RateBP: 1600,
		OpenDate:     domain.NewDate(time.Now()).AddDays(-300),
		MaturityDate: domain.NewDate(time.Now()).AddDays(45),
		Payout:       domain.PayoutEnd, TaxBP: 1950,
	}); err != nil {
		t.Fatal(err)
	}
	var sum struct {
		Liquidity *struct {
			NowUAH    float64 `json:"now_uah"`
			In30UAH   float64 `json:"in_30_uah"`
			In90UAH   float64 `json:"in_90_uah"`
			LockedUAH float64 `json:"locked_uah"`
		} `json:"liquidity"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	l := sum.Liquidity
	if l.LockedUAH != 0 {
		t.Errorf("вклад гаситься за 45 днів — замкненого бути не мало, маємо %.2f", l.LockedUAH)
	}
	if l.In30UAH != l.NowUAH {
		t.Errorf("за 30 днів іще нічого не гаситься: %.2f проти %.2f", l.In30UAH, l.NowUAH)
	}
	if l.In90UAH <= l.NowUAH+100000 {
		t.Errorf("за 90 днів мали додатись тіло й відсотки: %.2f", l.In90UAH)
	}
}

// seedPastBond кладе в довідник папір, купон і погашення якого вже
// МИНУЛИ. Фікстура seed() датована майбутнім, тож для всього, що питає
// «скільки я вже отримав», вона не годиться.
func seedPastBond(t *testing.T, st *store.Store, isin string, coupon, redeem domain.Date) {
	t.Helper()
	secs := []nbu.Security{{
		Bond: domain.Bond{ISIN: isin, Nominal: money.New(100000, money.UAH),
			RateBP: 1655, Maturity: redeem, Descr: "гривневі військові"},
		Payments: []domain.Payment{
			{ISIN: isin, PayDate: coupon, Type: domain.PayCoupon, PerBond: money.New(8275, money.UAH)},
			{ISIN: isin, PayDate: redeem, Type: domain.PayRedemption, PerBond: money.New(100000, money.UAH)},
		},
	}}
	if err := st.ReplaceDirectory(context.Background(), secs, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveRate(context.Background(), "USD", 441234, "2026-07-15"); err != nil {
		t.Fatal(err)
	}
}

// Податок по інструментах різний, і саме це має бути видно грошима:
// купон ОВДП звільнений, дивіденд фонду й відсотки вкладу — ні.
func TestTaxByInstrument(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	get := func() (rate float64, byKind map[string]float64, gross float64) {
		t.Helper()
		var d struct {
			GrossUAH float64 `json:"gross_uah"`
			RatePct  float64 `json:"rate_pct"`
			ByKind   []struct {
				Kind    string  `json:"kind"`
				RatePct float64 `json:"rate_pct"`
			} `json:"by_kind"`
		}
		_, b := do(t, "GET", srv.URL+"/api/tax", "")
		if err := json.Unmarshal([]byte(b), &d); err != nil {
			t.Fatalf("tax: %v: %s", err, b)
		}
		m := map[string]float64{}
		for _, l := range d.ByKind {
			m[l.Kind] = l.RatePct
		}
		return d.RatePct, m, d.GrossUAH
	}

	// Самі лише вклади: ефективна ставка дорівнює ставці податку вкладу.
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 10000000, RateBP: 1600,
		OpenDate:     domain.NewDate(time.Now()).AddDays(-180),
		MaturityDate: domain.NewDate(time.Now()).AddDays(185),
		Payout:       domain.PayoutMonthly, TaxBP: 1950,
	}); err != nil {
		t.Fatal(err)
	}
	rate, byKind, gross := get()
	if gross <= 0 {
		t.Fatal("відсотки вкладу мали потрапити в дохід")
	}
	if math.Abs(rate-19.5) > 0.1 {
		t.Errorf("портфель із самих вкладів мав дати ≈19.5%%, маємо %.2f%%", rate)
	}
	if math.Abs(byKind["deposit"]-19.5) > 0.1 {
		t.Errorf("рядок вкладів = %.2f%%", byKind["deposit"])
	}

	// Додаємо купони ОВДП — вони звільнені, тож ефективна ставка ПАДАЄ.
	past := domain.NewDate(time.Now()).AddDays(-90)
	seedPastBond(t, st, "UA4000227748", past, domain.NewDate(time.Now()).AddDays(275))
	if resp, b := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":50,"price_per_bond":"1000.00","buy_date":"`+
			string(domain.NewDate(time.Now()).AddDays(-120))+`","channel":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, b)
	}
	mixed, byKind2, _ := get()
	if byKind2["bond"] != 0 {
		t.Errorf("купон ОВДП звільнений, а рядок каже %.2f%%", byKind2["bond"])
	}
	if !(mixed < rate) {
		t.Errorf("звільнений дохід мав знизити ефективну ставку: було %.2f%%, стало %.2f%%",
			rate, mixed)
	}

	// І фонд — за ФАКТИЧНО утриманим, а не за зашитою ставкою.
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: domain.NewDate(time.Now()).AddDays(-10), Fund: "Inzhur", Kind: domain.FundDividend,
		Amount: 100000, Tax: 14000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	_, byKind3, _ := get()
	if math.Abs(byKind3["fund"]-14) > 0.1 {
		t.Errorf("фонд: утримано 14%% від 1000 ₴, а рядок каже %.2f%%", byKind3["fund"])
	}
}

// Погашення — це повернення власного тіла, а не дохід: якби воно сюди
// потрапляло, ефективна ставка розмивалась би до нуля щоразу, коли
// гаситься папір.
func TestTaxExcludesRedemptions(t *testing.T) {
	srv, st := testServer(t)
	// І купон, і ПОГАШЕННЯ вже минули — обидва в межах вікна, тож фільтр
	// має відсіяти саме друге, а не просто не дотягнутись до нього.
	seedPastBond(t, st, "UA4000227748",
		domain.NewDate(time.Now()).AddDays(-120), domain.NewDate(time.Now()).AddDays(-30))
	if resp, b := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":10,"price_per_bond":"1000.00","buy_date":"`+
			string(domain.NewDate(time.Now()).AddDays(-200))+`","channel":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, b)
	}
	var d struct {
		GrossUAH float64 `json:"gross_uah"`
	}
	_, b := do(t, "GET", srv.URL+"/api/tax?from=2000-01-01&to=2030-01-01", "")
	if err := json.Unmarshal([]byte(b), &d); err != nil {
		t.Fatalf("tax: %v: %s", err, b)
	}
	// 10 паперів × номінал 1000 = 10 000 ₴ погашення. Якби воно входило,
	// дохід перевищив би 10 000; самі купони — значно менші.
	if d.GrossUAH >= 10000 {
		t.Errorf("погашення потрапило в дохід: %.2f ₴", d.GrossUAH)
	}
	if d.GrossUAH <= 0 {
		t.Errorf("купони мали потрапити, маємо %.2f", d.GrossUAH)
	}
}

// Звіт про рух грошей мусить сходитись із рахунком. Він рахує ті самі
// величини, що й зведення, але подіями за період — і якщо дві
// реалізації розійдуться, помітити це можна лише тут.
func TestCashflowStatementReconciles(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seed(t, st)

	// Портфель із усіх трьох інструментів плюс свої гроші й конвертація.
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2026-01-10", Amount: 50000000, Currency: "UAH", Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"1000.00","fee":"25.00","buy_date":"2026-07-01","channel":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, b)
	}
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: "2026-06-01", Fund: "Inzhur", Kind: domain.FundBuy,
		Qty: 100, Amount: 100000, Currency: "UAH", Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	depID, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "mono", Currency: "UAH", Principal: 5000000, RateBP: 1600,
		OpenDate:     domain.NewDate(time.Now()).AddDays(-120),
		MaturityDate: domain.NewDate(time.Now()).AddDays(245),
		Payout:       domain.PayoutMonthly, TaxBP: 1950, Replenishable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDepositTopup(ctx, domain.DepositTopup{
		DepositID: depID, Date: domain.NewDate(time.Now()).AddDays(-30), Amount: 1000000,
	}); err != nil {
		t.Fatal(err)
	}

	// Період — від давнього минулого до сьогодні: тоді opening = 0, і
	// підсумок звіту має дорівнювати рахунку зі зведення.
	var st1 struct {
		OpeningUAH  float64 `json:"opening_uah"`
		IncomeUAH   float64 `json:"income_uah"`
		ContribUAH  float64 `json:"contributed_uah"`
		PurchaseUAH float64 `json:"purchased_uah"`
		ConvUAH     float64 `json:"conversions_uah"`
		ClosingUAH  float64 `json:"closing_uah"`
	}
	_, body := do(t, "GET", srv.URL+"/api/cashflow?from=2000-01-01", "")
	if err := json.Unmarshal([]byte(body), &st1); err != nil {
		t.Fatalf("cashflow: %v: %s", err, body)
	}
	var sum struct {
		AccountUAH float64 `json:"account_uah"`
	}
	_, body = do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	if math.Abs(st1.ClosingUAH-sum.AccountUAH) > 0.02 {
		t.Errorf("звіт дає %.2f, рахунок зі зведення %.2f — реалізації розійшлись",
			st1.ClosingUAH, sum.AccountUAH)
	}
	// І сама тотожність усередині звіту.
	want := st1.OpeningUAH + st1.IncomeUAH + st1.ContribUAH - st1.PurchaseUAH + st1.ConvUAH
	if math.Abs(st1.ClosingUAH-want) > 0.02 {
		t.Errorf("тотожність не сходиться: %.2f + %.2f + %.2f − %.2f + %.2f = %.2f, а закриття %.2f",
			st1.OpeningUAH, st1.IncomeUAH, st1.ContribUAH, st1.PurchaseUAH, st1.ConvUAH,
			want, st1.ClosingUAH)
	}
	if st1.OpeningUAH != 0 {
		t.Errorf("з 2000 року відкриття мало бути нульове, маємо %.2f", st1.OpeningUAH)
	}
	if st1.IncomeUAH <= 0 || st1.ContribUAH <= 0 || st1.PurchaseUAH <= 0 {
		t.Errorf("усі три категорії мали бути ненульові: %+v", st1)
	}
}

// Розрізане навпіл вікно теж має сходитись: відкриття другого періоду
// дорівнює закриттю першого. Інакше «за місяць» показувало б числа, які
// не стикуються між собою.
func TestCashflowPeriodsChain(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2026-03-10", Amount: 30000000, Currency: "UAH", Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2026-06-15", Amount: 20000000, Currency: "UAH", Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	get := func(from, to string) (open, close float64) {
		t.Helper()
		var d struct {
			OpeningUAH float64 `json:"opening_uah"`
			ClosingUAH float64 `json:"closing_uah"`
		}
		_, b := do(t, "GET", srv.URL+"/api/cashflow?from="+from+"&to="+to, "")
		if err := json.Unmarshal([]byte(b), &d); err != nil {
			t.Fatalf("cashflow: %v: %s", err, b)
		}
		return d.OpeningUAH, d.ClosingUAH
	}
	_, firstClose := get("2000-01-01", "2026-05-31")
	secondOpen, _ := get("2026-06-01", "2026-12-31")
	if math.Abs(firstClose-secondOpen) > 0.02 {
		t.Errorf("періоди не стикуються: закриття %.2f, наступне відкриття %.2f",
			firstClose, secondOpen)
	}
	if firstClose != 300000 {
		t.Errorf("до червня внесено 300 000 ₴, маємо %.2f", firstClose)
	}
}

// Курс має бути в зведенні завжди, а не лише коли задано ціль. Доти він
// потрапляв туди контрабандою — усередині блоку прогнозу, як
// forecast.rate0_usd, — тож питання «скільки це в доларах» не мало
// відповіді, поки не заповниш ціль і дедлайн, хоча одне з одним не
// пов'язане ніяк.
func TestSummaryCarriesRatesWithoutGoal(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st) // seed кладе курс USD, але жодної цілі не задає
	var sum struct {
		Rates    map[string]float64 `json:"rates"`
		Forecast *struct{}          `json:"forecast"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	if sum.Forecast != nil {
		t.Fatalf("фікстура мала бути без цілі — інакше тест нічого не доводить: %s", body)
	}
	if math.Abs(sum.Rates["USD"]-44.1234) > 0.0001 {
		t.Errorf("курс USD = %v, очікували 44.1234: %s", sum.Rates["USD"], body)
	}
}

// Номінальна й реальна — не два незалежні числа, а одне через поправку.
// Якщо вони перестануть сходитись, екран покаже пару, яка суперечить
// сама собі, і жоден інший тест цього не помітить.
func TestNominalAndRealAgree(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	seedRateHistory(t, st, 10, 240007, 448110) // ≈6.44%/рік
	if resp, b := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"1000.00","buy_date":"2026-07-01","channel":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv.URL+"/api/term-deposits",
		`{"bank":"ПУМБ","currency":"UAH","principal":"100000","rate_pct":"16",
		  "open_date":"2026-07-01","maturity_date":"2027-07-01","payout":"end","tax_pct":"19.5"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("вклад: %d %s", resp.StatusCode, b)
	}

	deval := 6.44
	agree := func(what string, nominal, real float64) {
		t.Helper()
		want := ((1+nominal/100)/(1+deval/100) - 1) * 100
		if math.Abs(real-want) > 0.05 {
			t.Errorf("%s: номінальна %.2f%% при знеціненні %.2f%% мала дати %.2f%% реальних, маємо %.2f%%",
				what, nominal, deval, want, real)
		}
	}

	// Позиція ОВДП.
	var pos []struct {
		YTMPct  float64 `json:"ytm_pct"`
		RealPct float64 `json:"real_pct"`
	}
	_, body := do(t, "GET", srv.URL+"/api/positions", "")
	if err := json.Unmarshal([]byte(body), &pos); err != nil {
		t.Fatalf("positions: %v: %s", err, body)
	}
	if len(pos) != 1 {
		t.Fatalf("очікували одну позицію: %s", body)
	}
	agree("позиція ОВДП", pos[0].YTMPct, pos[0].RealPct)

	// Вклад: тут номінальна — саме NetPct (після податку), а не ставка з
	// договору, інакше поправок було б дві одразу.
	var deps []struct {
		RatePct float64 `json:"rate_pct"`
		NetPct  float64 `json:"net_pct"`
		RealPct float64 `json:"real_pct"`
	}
	_, body = do(t, "GET", srv.URL+"/api/term-deposits", "")
	if err := json.Unmarshal([]byte(body), &deps); err != nil {
		t.Fatalf("term-deposits: %v: %s", err, body)
	}
	if len(deps) != 1 {
		t.Fatalf("очікували один вклад: %s", body)
	}
	if deps[0].NetPct >= deps[0].RatePct {
		t.Errorf("ставка після податку (%.2f) мала бути нижча за договірну (%.2f)",
			deps[0].NetPct, deps[0].RatePct)
	}
	agree("вклад", deps[0].NetPct, deps[0].RealPct)

	// Зведена по портфелю — теж парою.
	var sum struct {
		PortfolioYield     map[string]float64 `json:"portfolio_yield"`
		PortfolioYieldReal map[string]float64 `json:"portfolio_yield_real"`
	}
	_, body = do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	if len(sum.PortfolioYieldReal) == 0 {
		t.Fatalf("реальна зведена мала з'явитись: %s", body)
	}
	agree("зведена ОВДП ₴", sum.PortfolioYield["UAH"], sum.PortfolioYieldReal["UAH"])
}

// Валютну дохідність знецінення гривні не торкається: долар купівельну
// спроможність тримає сам, і ділити його на гривневе знецінення означало
// б порахувати поправку двічі.
func TestForeignYieldIsAlreadyReal(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seedRateHistory(t, st, 10, 240007, 448110)
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: "USD", Principal: 100000, RateBP: 400,
		OpenDate:     domain.NewDate(time.Now()).AddDays(-10),
		MaturityDate: domain.NewDate(time.Now()).AddDays(355),
		Payout:       domain.PayoutEnd, TaxBP: 0,
	}); err != nil {
		t.Fatal(err)
	}
	var deps []struct {
		NetPct  float64 `json:"net_pct"`
		RealPct float64 `json:"real_pct"`
	}
	_, body := do(t, "GET", srv.URL+"/api/term-deposits", "")
	if err := json.Unmarshal([]byte(body), &deps); err != nil {
		t.Fatalf("term-deposits: %v: %s", err, body)
	}
	if len(deps) != 1 || math.Abs(deps[0].RealPct-deps[0].NetPct) > 0.01 {
		t.Errorf("доларовий вклад: реальна (%+v) мала дорівнювати номінальній", deps)
	}
}

// Уламок вікна — не вимірювання. Три роки історії дали б 10.2%/рік
// замість 6.4% з десятирічного, і видавати це за «виміряне знецінення»
// означало б показати наслідок обірваного backfill як факт про гривню.
func TestDevaluationIgnoresTooShortWindow(t *testing.T) {
	srv, st := testServer(t)
	seedRateHistory(t, st, 3, 276913, 448110) // 2020 → сьогодні
	var got struct {
		EffectivePct float64 `json:"effective_pct"`
		Source       string  `json:"source"`
		Windows      []struct {
			Years int     `json:"years"`
			Pct   float64 `json:"pct"`
		} `json:"windows"`
	}
	_, body := do(t, "GET", srv.URL+"/api/devaluation", "")
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("devaluation: %v: %s", err, body)
	}
	if got.Source != "default" || got.EffectivePct != 6 {
		t.Errorf("трирічної історії мало не вистачити: %+v", got)
	}
	// Але саме вікно показати варто — щоб було видно, що дані є, просто
	// їх замало.
	if len(got.Windows) == 0 {
		t.Error("наявні вікна мали лишитись видимими навіть коли їм не вірять")
	}
}

// Без історії курсу — чесний відступ на припущення, а не нуль чи помилка.
func TestDevaluationFallsBackWithoutHistory(t *testing.T) {
	srv, _ := testServer(t)
	var got struct {
		EffectivePct float64 `json:"effective_pct"`
		Source       string  `json:"source"`
		Note         string  `json:"note"`
	}
	_, body := do(t, "GET", srv.URL+"/api/devaluation", "")
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("devaluation: %v: %s", err, body)
	}
	if got.Source != "default" || got.EffectivePct != 6 {
		t.Errorf("без даних мала бути припущена шістка: %+v", got)
	}
	if got.Note == "" {
		t.Error("відсутність історії мала бути названа словами")
	}
}

// Налаштування приймало будь-що: "abc" лягало в базу, а на читанні
// мовчки підмінялось дефолтом — поле виглядало збереженим і поводилось
// як незбережене.
func TestSettingsRejectGarbage(t *testing.T) {
	srv, _ := testServer(t)
	for _, bad := range []string{
		`{"uah_devaluation_pct":"abc"}`,
		`{"uah_devaluation_pct":"-5"}`,
		`{"terminal_rate_pct":"багато"}`,
	} {
		if resp, b := do(t, "PUT", srv.URL+"/api/settings", bad); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s мало бути відхилено, маємо %d %s", bad, resp.StatusCode, b)
		}
	}
	// А правильне — приймається.
	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"uah_devaluation_pct":"7.5"}`); resp.StatusCode != http.StatusNoContent {
		t.Errorf("валідне значення мало пройти: %d %s", resp.StatusCode, b)
	}
}

// Драбина мусить СКЛАДАТИ облігації й вклади, коли вони гасяться в
// одному році й одній валюті, а не показувати останнє записане. Рядок
// року складався присвоєнням, тож із двох джерел виживало одне — і
// таблиця розходилась зі стовпчиками над нею, які завжди сумували.
func TestLadderAddsBondsAndDepositsInSameYear(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seed(t, st) // папір із погашенням 2027-03-17, номінал 1000 ₴
	if resp, b := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":2,"price_per_bond":"1000.00","buy_date":"2026-07-01","channel":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, b)
	}
	// Вклад на 50 000 ₴, що гаситься того ж 2027 року.
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 5000000, RateBP: 1600,
		OpenDate: domain.NewDate(time.Now()).AddDays(-10), MaturityDate: "2027-09-01",
		Payout: domain.PayoutEnd, TaxBP: 1950,
	}); err != nil {
		t.Fatal(err)
	}

	var sum struct {
		Ladder []struct {
			Year int     `json:"year"`
			UAH  float64 `json:"uah"`
		} `json:"ladder"`
		LadderUAH []struct {
			Year int     `json:"year"`
			UAH  float64 `json:"uah"`
		} `json:"ladder_uah"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	var row, bar float64
	for _, r := range sum.Ladder {
		if r.Year == 2027 {
			row = r.UAH
		}
	}
	for _, r := range sum.LadderUAH {
		if r.Year == 2027 {
			bar = r.UAH
		}
	}
	const want float64 = 2000 + 50000 // два папери по 1000 ₴ + тіло вкладу
	if row != want {
		t.Errorf("рядок драбини за 2027 = %.2f, очікували %.2f (папери + вклад)", row, want)
	}
	// Стовпчик над таблицею завжди сумував — вони зобов'язані збігтись.
	if row != bar {
		t.Errorf("таблиця (%.2f) розійшлась зі стовпчиком (%.2f) за той самий рік", row, bar)
	}
}

// Відновлення з бекапу мусить працювати з вкладами. Доти term_deposits
// не було в списку таблиць, які restore очищає, а term_deposits.broker_id
// посилається на brokers — тож DELETE FROM brokers упирався у FK, і
// відновлення падало цілком у будь-кого, хто має бодай один вклад.
// Помітно це стало б у найгірший момент: коли відновлюватись уже треба.
func TestRestoreWorksWithDeposits(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	dep, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 10000000, RateBP: 1600,
		OpenDate:     domain.NewDate(time.Now()).AddDays(-30),
		MaturityDate: domain.NewDate(time.Now()).AddDays(335),
		Payout:       domain.PayoutEnd, TaxBP: 1950, Replenishable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDepositTopup(ctx, domain.DepositTopup{
		DepositID: dep, Date: domain.NewDate(time.Now()).AddDays(-5), Amount: 10000000,
	}); err != nil {
		t.Fatal(err)
	}

	_, dump := do(t, "GET", srv.URL+"/api/backup", "")
	if resp, b := do(t, "POST", srv.URL+"/api/restore", dump); resp.StatusCode >= 300 {
		t.Fatalf("відновлення з вкладом мало пройти: %d %s", resp.StatusCode, b)
	}

	// Рівно один вклад, а не два: restore ЗАМІНЮЄ дані, а не доливає.
	var deps []struct {
		Bank    string `json:"bank"`
		Balance struct {
			Amount string `json:"amount"`
		} `json:"balance"`
		Topups []struct{} `json:"topups"`
	}
	_, body := do(t, "GET", srv.URL+"/api/term-deposits", "")
	if err := json.Unmarshal([]byte(body), &deps); err != nil {
		t.Fatalf("term-deposits: %v: %s", err, body)
	}
	if len(deps) != 1 {
		t.Fatalf("після відновлення мав лишитись один вклад, маємо %d: %s", len(deps), body)
	}
	if len(deps[0].Topups) != 1 {
		t.Errorf("поповнення мало відновитись рівно одне, маємо %d", len(deps[0].Topups))
	}
	if deps[0].Bank != "ПУМБ" {
		t.Errorf("банк загубився: %q", deps[0].Bank)
	}
}

// Знімок мусить нести ВЕСЬ капітал, а не лише облігаційну його частину:
// доти вклади в історію не писались узагалі, і крива «Як росте»
// показувала портфель без них.
func TestSnapshotCarriesEveryInstrument(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seed(t, st)
	if resp, b := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"1000.00","buy_date":"2026-07-01","channel":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, b)
	}
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 10000000, RateBP: 1600,
		OpenDate:     domain.NewDate(time.Now()).AddDays(-10),
		MaturityDate: domain.NewDate(time.Now()).AddDays(355),
		Payout:       domain.PayoutEnd, TaxBP: 1950,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: domain.NewDate(time.Now()).AddDays(-40), Fund: "Inzhur", Kind: domain.FundBuy,
		Qty: 1000, Amount: 1000000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}

	// Пишемо знімок так само, як добова джоба: через зведення.
	var doc struct {
		NominalUAHEq float64 `json:"nominal_uah_eq"`
		FundsUAH     float64 `json:"funds_uah"`
		DepositsUAH  float64 `json:"deposits_uah"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	if doc.DepositsUAH <= 0 || doc.FundsUAH <= 0 {
		t.Fatalf("зведення мало бачити і фонди, і вклади: %+v", doc)
	}
	if err := st.SaveSnapshot(ctx, store.Snapshot{
		Date:         domain.NewDate(time.Now()),
		NominalUAHEq: int64(doc.NominalUAHEq * 100),
		FundsUAH:     int64(doc.FundsUAH * 100),
		DepositsUAH:  int64(doc.DepositsUAH * 100),
	}); err != nil {
		t.Fatal(err)
	}

	var snaps []struct {
		FundsUAH    float64 `json:"funds_uah"`
		DepositsUAH float64 `json:"deposits_uah"`
	}
	_, body = do(t, "GET", srv.URL+"/api/snapshots", "")
	if err := json.Unmarshal([]byte(body), &snaps); err != nil {
		t.Fatalf("snapshots: %v: %s", err, body)
	}
	if len(snaps) != 1 {
		t.Fatalf("очікували один знімок, маємо %d: %s", len(snaps), body)
	}
	if snaps[0].DepositsUAH != doc.DepositsUAH {
		t.Errorf("вклади в знімку = %.2f, а в портфелі %.2f", snaps[0].DepositsUAH, doc.DepositsUAH)
	}

	// І переживають бекап: інакше відновлення тихо стерло б історію.
	_, dump := do(t, "GET", srv.URL+"/api/backup", "")
	if resp, b := do(t, "POST", srv.URL+"/api/restore", dump); resp.StatusCode >= 300 {
		t.Fatalf("restore: %d %s", resp.StatusCode, b)
	}
	_, body = do(t, "GET", srv.URL+"/api/snapshots", "")
	if err := json.Unmarshal([]byte(body), &snaps); err != nil {
		t.Fatalf("snapshots після restore: %v: %s", err, body)
	}
	if len(snaps) != 1 || snaps[0].DepositsUAH != doc.DepositsUAH || snaps[0].FundsUAH != doc.FundsUAH {
		t.Errorf("бекап загубив склад знімка: %+v (чекали вклади %.2f, фонди %.2f)",
			snaps, doc.DepositsUAH, doc.FundsUAH)
	}
}

// Вклад не має вторинного ринку, тож і цінового ризику в нього немає:
// сума погашення записана в договорі. Доти його потоки потрапляли в ті
// самі сценарії, що й облігаційні, і портфель із самого вкладу на 100 000
// показував приведену вартість 125 054 — тобто більше, ніж у ньому є.
func TestDepositHasNoPriceRisk(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 10000000, RateBP: 1600,
		OpenDate:     domain.NewDate(time.Now()).AddDays(-10),
		MaturityDate: domain.NewDate(time.Now()).AddDays(700),
		Payout:       domain.PayoutEnd, TaxBP: 1950,
	}); err != nil {
		t.Fatal(err)
	}
	var sum struct {
		RateRisk *struct {
			DurationYears   float64 `json:"duration_years"`
			PVUAH           float64 `json:"pv_uah"`
			ReinvestYears   float64 `json:"reinvest_years"`
			ReturningUAH    float64 `json:"returning_uah"`
			ReinvestSoonUAH float64 `json:"reinvest_soon_uah"`
		} `json:"rate_risk"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	if sum.RateRisk == nil {
		t.Fatalf("блок ризику мав лишитись — строк перевкладення є й без облігацій: %s", body)
	}
	rr := sum.RateRisk
	// Ціновий ризик — порожній: переоцінювати нічого.
	if rr.DurationYears != 0 || rr.PVUAH != 0 {
		t.Errorf("вклад не має цінового ризику, а маємо дюрацію %.2f і PV %.2f",
			rr.DurationYears, rr.PVUAH)
	}
	// Строк перевкладення — навпаки, є: вклад гаситься.
	if rr.ReinvestYears <= 0 {
		t.Errorf("строк перевкладення мав порахуватись, маємо %.2f", rr.ReinvestYears)
	}
	// Повернеться тіло з відсотками, тобто більше за тіло.
	if !(rr.ReturningUAH > 100000) {
		t.Errorf("повернутись мало більше за тіло 100000, маємо %.2f", rr.ReturningUAH)
	}
	// Погашення через 700 днів — у найближчі 12 місяців не повертається
	// нічого (виплата в кінці строку).
	if rr.ReinvestSoonUAH != 0 {
		t.Errorf("за 12 міс. не мало повернутись нічого, маємо %.2f", rr.ReinvestSoonUAH)
	}
}

// Облігація дає обидва ризики: і ціновий, і строк перевкладення. Купон
// цього року — це вже гроші, які треба буде перевкласти.
func TestBondHasBothRisks(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	if resp, b := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":10,"price_per_bond":"1000.00","buy_date":"2026-07-01","channel":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, b)
	}
	var sum struct {
		RateRisk *struct {
			DurationYears   float64 `json:"duration_years"`
			PVUAH           float64 `json:"pv_uah"`
			ReinvestYears   float64 `json:"reinvest_years"`
			ReinvestSoonUAH float64 `json:"reinvest_soon_uah"`
		} `json:"rate_risk"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	if sum.RateRisk == nil {
		t.Fatalf("облігація мала дати ризик: %s", body)
	}
	if sum.RateRisk.DurationYears <= 0 || sum.RateRisk.PVUAH <= 0 {
		t.Errorf("ціновий ризик мав порахуватись: дюрація %.2f, PV %.2f",
			sum.RateRisk.DurationYears, sum.RateRisk.PVUAH)
	}
	if sum.RateRisk.ReinvestYears <= 0 {
		t.Errorf("строк перевкладення мав порахуватись, маємо %.2f", sum.RateRisk.ReinvestYears)
	}
	// Купон 2026-09-16 — у межах року, тож щось перевкладати доведеться.
	if sum.RateRisk.ReinvestSoonUAH <= 0 {
		t.Errorf("купон найближчого року мав потрапити в «за 12 міс.», маємо %.2f",
			sum.RateRisk.ReinvestSoonUAH)
	}
}

// Дохідність позиції рахується за ТВОЄЮ собівартістю, а не за ціною
// довідника: питання «скільки заробляю я», а не «скільки платить папір».
// І приводиться до сьогоднішньої гривні тією ж формулою, що й поради.
func TestPositionsCarryRealYield(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)

	// Номінал 1000, купон 16.55%, куплено з дисконтом за 900. Дисконт —
	// теж дохід, тож YTM має вийти помітно вище купонної ставки.
	if resp, b := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"900.00","buy_date":"2026-07-01","channel":"mono"}`); resp.StatusCode != http.StatusCreated {
		t.Fatalf("лот: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"uah_devaluation_pct":"6"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("settings: %d %s", resp.StatusCode, b)
	}

	var rows []struct {
		ISIN       string  `json:"isin"`
		YTMPct     float64 `json:"ytm_pct"`
		RealPct    float64 `json:"real_pct"`
		YieldBasis string  `json:"yield_basis"`
	}
	_, body := do(t, "GET", srv.URL+"/api/positions", "")
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("positions: %v: %s", err, body)
	}
	if len(rows) != 1 {
		t.Fatalf("очікували одну позицію, маємо %d: %s", len(rows), body)
	}
	p := rows[0]
	if p.YTMPct <= 16.55 {
		t.Errorf("YTM %.2f%% мав бути вище купона 16.55%% — дисконт не врахований", p.YTMPct)
	}
	// Масштаб пінимо зведеною цифрою: папір один, тож дохідність позиції
	// зобов'язана збігтися з портфельною, яку показують плитки. Без цього
	// тест не відрізнив би відсотки від частки — а WeightedYTM віддає
	// відсотки, тоді як YTM поруч віддає частку.
	var sum struct {
		PortfolioYield map[string]float64 `json:"portfolio_yield"`
	}
	_, sbody := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(sbody), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, sbody)
	}
	if math.Abs(p.YTMPct-sum.PortfolioYield[money.UAH]) > 0.01 {
		t.Errorf("YTM позиції %.2f%% розійшовся зі зведеним %.2f%% — інший масштаб",
			p.YTMPct, sum.PortfolioYield[money.UAH])
	}
	if p.YieldBasis != "до погашення" {
		t.Errorf("yield_basis = %q, очікували «до погашення»", p.YieldBasis)
	}
	// Реальна — та сама дохідність, поділена на знецінення 6%.
	want := ((1+p.YTMPct/100)/1.06 - 1) * 100
	if math.Abs(p.RealPct-want) > 0.05 {
		t.Errorf("real_pct = %.2f, очікували %.2f (YTM %.2f при знеціненні 6%%)",
			p.RealPct, want, p.YTMPct)
	}
}

// Комісія теж з'їдає дохідність, тож вона входить у собівартість: два
// однакові папери за однакову ціну дають РІЗНУ дохідність, якщо за один
// заплачено брокеру.
func TestPositionYieldCountsFee(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	mk := func(isin string) nbu.Security {
		return nbu.Security{
			Bond: domain.Bond{ISIN: isin, Nominal: money.New(100000, money.UAH),
				RateBP: 1655, Maturity: "2027-03-17", Descr: "гривневі військові"},
			Payments: []domain.Payment{
				{ISIN: isin, PayDate: "2026-09-16", Type: domain.PayCoupon, PerBond: money.New(8275, money.UAH)},
				{ISIN: isin, PayDate: "2027-03-17", Type: domain.PayCoupon, PerBond: money.New(8275, money.UAH)},
				{ISIN: isin, PayDate: "2027-03-17", Type: domain.PayRedemption, PerBond: money.New(100000, money.UAH)},
			},
		}
	}
	if err := st.ReplaceDirectory(ctx,
		[]nbu.Security{mk("UA4000227748"), mk("UA4000227755")}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{
		`{"isin":"UA4000227748","qty":10,"price_per_bond":"1000.00","buy_date":"2026-07-01","channel":"mono"}`,
		`{"isin":"UA4000227755","qty":10,"price_per_bond":"1000.00","fee":"500.00","buy_date":"2026-07-01","channel":"mono"}`,
	} {
		if resp, b := do(t, "POST", srv.URL+"/api/lots", l); resp.StatusCode != http.StatusCreated {
			t.Fatalf("лот: %d %s", resp.StatusCode, b)
		}
	}

	var rows []struct {
		ISIN   string  `json:"isin"`
		YTMPct float64 `json:"ytm_pct"`
	}
	_, body := do(t, "GET", srv.URL+"/api/positions", "")
	if err := json.Unmarshal([]byte(body), &rows); err != nil {
		t.Fatalf("positions: %v: %s", err, body)
	}
	ytm := map[string]float64{}
	for _, r := range rows {
		ytm[r.ISIN] = r.YTMPct
	}
	free, paid := ytm["UA4000227748"], ytm["UA4000227755"]
	if free == 0 || paid == 0 {
		t.Fatalf("обидві позиції мали дати дохідність: %s", body)
	}
	if !(paid < free) {
		t.Errorf("папір із комісією (%.2f%%) мав дати меншу дохідність, ніж без неї (%.2f%%)",
			paid, free)
	}
}

// Портфель і помічник говорять одним числом: скільки вклад чи фонд дає
// ЗАРАЗ і скільки дасть, якщо докласти, — це та сама реальна дохідність.
// Якби бази розійшлися, порада суперечила б тому, що видно в позиціях.
func TestPortfolioAndAdviceAgreeOnRealYield(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: "2026-01-10", Amount: 50000000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 10000000, RateBP: 1600,
		OpenDate:     domain.NewDate(time.Now()).AddDays(-10),
		MaturityDate: domain.NewDate(time.Now()).AddDays(355),
		Payout:       domain.PayoutEnd, TaxBP: 1950, Replenishable: true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, op := range []domain.FundOp{
		{Date: domain.NewDate(time.Now()).AddDays(-40), Fund: "Inzhur", Kind: domain.FundBuy,
			Qty: 1000, Amount: 1000000, Currency: "UAH", Broker: "ПУМБ"},
		{Date: domain.NewDate(time.Now()).AddDays(-10), Fund: "Inzhur", Kind: domain.FundDividend,
			Amount: 6800, Currency: "UAH", Broker: "ПУМБ"},
	} {
		if _, err := st.AddFundOp(ctx, op); err != nil {
			t.Fatal(err)
		}
	}
	// Знецінення НЕ нульове — саме приведення тут і перевіряємо.
	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"uah_devaluation_pct":"6"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("settings: %d %s", resp.StatusCode, b)
	}

	var deps []struct {
		RealPct    float64 `json:"real_pct"`
		YieldBasis string  `json:"yield_basis"`
	}
	_, body := do(t, "GET", srv.URL+"/api/term-deposits", "")
	if err := json.Unmarshal([]byte(body), &deps); err != nil {
		t.Fatalf("term-deposits: %v: %s", err, body)
	}
	var sum struct {
		Funds []struct {
			TotalPct   float64 `json:"total_pct"`
			RealPct    float64 `json:"real_pct"`
			YieldBasis string  `json:"yield_basis"`
		} `json:"funds"`
	}
	_, body = do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	var adv []struct {
		Kind    string  `json:"kind"`
		RealPct float64 `json:"real_pct"`
	}
	_, body = do(t, "GET", srv.URL+"/api/reinvest", "")
	if err := json.Unmarshal([]byte(body), &adv); err != nil {
		t.Fatalf("reinvest: %v: %s", err, body)
	}
	byKind := map[string]float64{}
	for _, a := range adv {
		if _, seen := byKind[a.Kind]; !seen {
			byKind[a.Kind] = a.RealPct
		}
	}

	if len(deps) != 1 || len(sum.Funds) != 1 {
		t.Fatalf("очікували один вклад і один фонд, маємо %d і %d", len(deps), len(sum.Funds))
	}
	if deps[0].RealPct <= 0 || sum.Funds[0].RealPct <= 0 {
		t.Fatalf("реальна дохідність має бути заповнена: вклад=%.2f фонд=%.2f",
			deps[0].RealPct, sum.Funds[0].RealPct)
	}
	if deps[0].YieldBasis != "ставка вкладу" {
		t.Errorf("основа дохідності вкладу = %q", deps[0].YieldBasis)
	}
	// Вклад — те саме питання з обох боків: ставка зафіксована договором,
	// і «скільки він дає» не залежить від того, дивишся ти на нього в
	// портфелі чи думаєш його поповнити. Розійтись тут числа не мають.
	if math.Abs(deps[0].RealPct-byKind["deposit"]) > 0.01 {
		t.Errorf("вклад: у портфелі %.2f%%, у пораді %.2f%% — бази розійшлись",
			deps[0].RealPct, byKind["deposit"])
	}

	// А от фонд — питання РІЗНІ, і однакове число тут було б неправдою.
	// У портфелі «скільки я на ньому заробив» — факт, дивіденди разом зі
	// зміною ціни. У пораді «скільки він дасть, якщо докласти» — самі
	// дивіденди: минуле подорожчання сертифіката ніхто не обіцяв, і
	// видавати його за очікувану дохідність означало б радити купувати
	// те, що вже виросло, саме тому, що воно вже виросло.
	if sum.Funds[0].YieldBasis != "дивіденди + зміна ціни" {
		t.Errorf("у портфелі фонд має рахуватись повністю, основа = %q", sum.Funds[0].YieldBasis)
	}
	if sum.Funds[0].TotalPct <= 0 {
		t.Errorf("повна дохідність фонду мала порахуватись, маємо %.2f", sum.Funds[0].TotalPct)
	}
	if byKind["fund"] <= 0 {
		t.Errorf("порада мала лишитись на дивідендах, маємо %.2f", byKind["fund"])
	}
	// Фікстура підібрана так, що вони справді різні: єдиний дивіденд
	// ануалізується як місячний (8.27%), а XIRR бачить фактичні 40 днів
	// тримання (6.4%). Якби числа збіглися, тест мовчав би про підміну
	// однієї бази іншою.
	if math.Abs(sum.Funds[0].RealPct-byKind["fund"]) < 0.01 {
		t.Errorf("портфель і порада мали дати РІЗНІ числа для фонду, обидва %.2f",
			sum.Funds[0].RealPct)
	}
}

// Повна дохідність фонду бачить подорожчання сертифіката, а не самі
// дивіденди. Фонд, який не платить нічого, але виріс у ціні, доти мав
// дохідність «—», хоч гроші на ньому заробились.
func TestFundTotalReturnSeesPriceGrowth(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	// Куплено 100 сертифікатів по 10 ₴, за 60 днів ціна дійшла до 11 ₴.
	// Другу купівлю робимо дрібною — вона потрібна лише щоб принести
	// нову ціну, бо ціна береться з останньої операції.
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: domain.NewDate(time.Now()).AddDays(-60), Fund: "Inzhur", Kind: domain.FundBuy,
		Qty: 100, Amount: 100000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: domain.NewDate(time.Now()).AddDays(-1), Fund: "Inzhur", Kind: domain.FundBuy,
		Qty: 1, Amount: 1100, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	if resp, b := do(t, "PUT", srv.URL+"/api/settings",
		`{"uah_devaluation_pct":"0"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("settings: %d %s", resp.StatusCode, b)
	}

	var sum struct {
		Funds []struct {
			YieldNetPct float64 `json:"yield_net_pct"`
			TotalPct    float64 `json:"total_pct"`
			RealPct     float64 `json:"real_pct"`
			YieldBasis  string  `json:"yield_basis"`
		} `json:"funds"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	if len(sum.Funds) != 1 {
		t.Fatalf("очікували один фонд: %s", body)
	}
	f := sum.Funds[0]
	// Дивідендів не було жодних — стара формула мовчала б.
	if f.YieldNetPct != 0 {
		t.Errorf("дивідендів не було, а yield_net_pct = %.2f", f.YieldNetPct)
	}
	// А повна дохідність бачить +10% за два місяці.
	if f.TotalPct <= 0 {
		t.Fatalf("подорожчання на 10%% мало дати додатну дохідність, маємо %.2f: %s", f.TotalPct, body)
	}
	if f.YieldBasis != "дивіденди + зміна ціни" {
		t.Errorf("основа = %q", f.YieldBasis)
	}
	// Знецінення нульове, тож реальна дорівнює номінальній.
	if math.Abs(f.RealPct-f.TotalPct) > 0.02 {
		t.Errorf("при нульовому знеціненні real (%.2f) мала дорівнювати total (%.2f)",
			f.RealPct, f.TotalPct)
	}
}

// Ануалізація на коротких строках — це не дохідність, а ділення на малий
// строк: три дні тримання з приростом 0.4% дають 60% річних. Такому
// числу краще не бути взагалі.
func TestFundTotalReturnStaysQuietOnShortHistory(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: domain.NewDate(time.Now()).AddDays(-3), Fund: "Inzhur", Kind: domain.FundBuy,
		Qty: 100, Amount: 100000, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: domain.NewDate(time.Now()), Fund: "Inzhur", Kind: domain.FundBuy,
		Qty: 1, Amount: 1040, Currency: "UAH", Broker: "ПУМБ",
	}); err != nil {
		t.Fatal(err)
	}
	var sum struct {
		Funds []struct {
			TotalPct float64 `json:"total_pct"`
		} `json:"funds"`
	}
	_, body := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	if len(sum.Funds) != 1 {
		t.Fatalf("очікували один фонд: %s", body)
	}
	if sum.Funds[0].TotalPct != 0 {
		t.Errorf("на трьох днях історії дохідності бути не мало, маємо %.2f%%", sum.Funds[0].TotalPct)
	}
}
