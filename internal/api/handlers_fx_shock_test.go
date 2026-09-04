package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// firstOfMonth — перше число місяця, зсунутого на back назад від сьогодні.
func firstOfMonth(back int) domain.Date {
	t := time.Now().AddDate(0, -back, 0)
	return domain.Date(fmt.Sprintf("%04d-%02d-01", t.Year(), int(t.Month())))
}

// fxShockServer — сервер, портфель і ІСТОРІЯ КУРСІВ, якої вистачає на
// вікна. Без історії шок мовчить за побудовою, і тест не перевіряв би
// нічого, крім порожнього стану.
//
// Тридцять місяців: рівний рівень, а на пʼятнадцятому місяці — стрибок
// на 50%. Саме він і мусить знайтись найгіршим у будь-якому вікні.
func fxShockServer(t *testing.T) (*Server, *store.Store, *httptest.Server) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	seed(t, st)

	for i := 30; i >= 1; i-- {
		usd, eur := int64(400_000), int64(430_000)
		if i <= 15 {
			usd, eur = 600_000, 516_000
		}
		if err := st.SaveRate(ctx, money.USD, usd, firstOfMonth(i)); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveRate(ctx, money.EUR, eur, firstOfMonth(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: domain.NewDate(time.Now()), Amount: 100_000_00,
		Currency: money.USD, Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddLot(ctx, domain.Lot{
		ISIN: "UA4000227748", Qty: 5, PricePerBond: money.New(99500, money.UAH),
		BuyDate: domain.NewDate(time.Now().AddDate(0, 0, -10)), Channel: "mono",
	}); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	s := New(st, nil, log)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return s, st, srv
}

// stripDoc прибирає з документа те, що законно рухається між двома
// збірками. Той самий набір і той самий довід, що при whatif.
func stripDoc(t *testing.T, b []byte) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "generated_at")
	delete(m, "tasks")
	delete(m, "idle_cost")
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// ГОЛОВНИЙ ТЕСТ ПРИЙОМУ. Підміна ТИМИ САМИМИ курсами мусить дати
// документ, невідрізнимий від звичайного.
//
// Якщо вони збігаються — значить, шок не має власної арифметики, і
// віднімання «після мінус до» на фронтенді законне: обидва числа
// народжені одним кодом. Близнюк TestWhatIfEmptyPlanMatchesSummary, і
// тримає він рівно те саме.
func TestFXShockSameRatesMatchSummary(t *testing.T) {
	s, _, _ := fxShockServer(t)
	ctx, now := context.Background(), time.Now()

	rates, err := s.rates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := s.buildState(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	same, err := s.buildStateWith(ctx, now, hypothetical{rates: rates})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(plain)
	b, _ := json.Marshal(same)
	if x, y := stripDoc(t, a), stripDoc(t, b); x != y {
		t.Errorf("підміна тими самими курсами зрушила стан:\nбез неї: %s\nз нею:   %s", x, y)
	}
}

// Порожня гіпотеза курсів не вмикає прийом взагалі — і саме це найлегше
// зламати, забувши поле в empty().
func TestFXShockEmptyRatesIsNotAHypothesis(t *testing.T) {
	if !(hypothetical{}).empty() {
		t.Fatal("порожня гіпотеза мусить лишатись порожньою")
	}
	if (hypothetical{rates: map[string]int64{money.USD: 400_000}}).empty() {
		t.Error("гіпотеза з курсами вважається порожньою — прийом буде мовчки пропущено")
	}
}

func getShock(t *testing.T, url string, window int) (int, string) {
	t.Helper()
	resp, body := do(t, "GET", fmt.Sprintf("%s/api/fx-shock?window=%d", url, window), "")
	return resp.StatusCode, body
}

type shockResp struct {
	Granularity string `json:"granularity"`
	Windows     []int  `json:"windows"`
	Measured    struct {
		Anchor string `json:"anchor"`
		Months int    `json:"months"`
	} `json:"measured"`
	Episode *struct {
		WindowMonths int    `json:"window_months"`
		From         string `json:"from"`
		To           string `json:"to"`
		Moves        []struct {
			Currency string  `json:"currency"`
			From     string  `json:"from"`
			To       string  `json:"to"`
			MovePct  float64 `json:"move_pct"`
			RateNow  float64 `json:"rate_now"`
			RateThen float64 `json:"rate_then"`
			Why      string  `json:"why"`
		} `json:"moves"`
	} `json:"episode"`
	After json.RawMessage `json:"after"`
	Why   string          `json:"why"`
}

// Дати епізоду — справжні точки з fx_rates, а курс «стане» дорівнює
// сьогоднішньому, помноженому на виміряний рух. Нічого зашитого й
// нічого інтерпольованого.
func TestFXShockNamesRealDates(t *testing.T) {
	_, _, srv := fxShockServer(t)
	code, body := getShock(t, srv.URL, 1)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var got shockResp
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Granularity != "month" {
		t.Errorf("одиниця мусить бути названа місяцем, а не %q", got.Granularity)
	}
	if got.Episode == nil {
		t.Fatalf("епізод мав знайтись: %s", body)
	}
	if got.Episode.From != string(firstOfMonth(16)) || got.Episode.To != string(firstOfMonth(15)) {
		t.Errorf("вікно не там, де стрибок: %s → %s", got.Episode.From, got.Episode.To)
	}
	for _, mv := range got.Episode.Moves {
		if mv.Why != "" {
			t.Errorf("%s не зрушено: %s", mv.Currency, mv.Why)
			continue
		}
		want := mv.RateNow * (1 + mv.MovePct/100)
		if diff := mv.RateThen - want; diff > 0.01 || diff < -0.01 {
			t.Errorf("%s: «стане» %v, а рух %v від %v дає %v",
				mv.Currency, mv.RateThen, mv.MovePct, mv.RateNow, want)
		}
	}
	if len(got.After) == 0 || string(got.After) == "null" {
		t.Error("епізод є, а документа «стане» немає")
	}
}

// Сплеск усередині місяця не стає місячним рухом: історія міряється по
// місячній сітці, і саме це фіча каже вголос.
func TestFXShockIsMeasuredMonthly(t *testing.T) {
	_, st, srv := fxShockServer(t)
	ctx := context.Background()
	// Добова точка всередині найсвіжішого місяця, утричі вища за все.
	spike := domain.Date(fmt.Sprintf("%s-17", string(firstOfMonth(1))[:7]))
	if err := st.SaveRate(ctx, money.USD, 1_800_000, spike); err != nil {
		t.Fatal(err)
	}
	_, body := getShock(t, srv.URL, 1)
	var got shockResp
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Episode == nil {
		t.Fatalf("епізод мав знайтись: %s", body)
	}
	for _, mv := range got.Episode.Moves {
		if mv.Currency == money.USD && mv.MovePct > 60 {
			t.Errorf("сплеск усередині місяця протік у місячний рух: %v%%", mv.MovePct)
		}
	}
}

// Історії замало — показуємо причину, а не нулі.
func TestFXShockSilentOnThinHistory(t *testing.T) {
	srv, _ := testServer(t) // seed кладе рівно одну точку курсу
	code, body := getShock(t, srv.URL, 12)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var got shockResp
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Episode != nil {
		t.Error("на одній точці епізоду бути не може")
	}
	if got.Why == "" {
		t.Error("мовчання без названої причини — саме те, від чого фіча застерігає")
	}
	if len(got.Windows) != 0 {
		t.Errorf("вибирати нема з чого, а запропоновано %v", got.Windows)
	}
	if string(got.After) != "null" && len(got.After) != 0 {
		t.Error("епізоду немає — документа «стане» бути не повинно")
	}
}

// Вікно, якого немає в переліку, не підміняється найближчим.
func TestFXShockRejectsUnknownWindow(t *testing.T) {
	_, _, srv := fxShockServer(t)
	resp, _ := do(t, "GET", srv.URL+"/api/fx-shock?window=7", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("невідоме вікно мало впасти, а дало %d", resp.StatusCode)
	}
}

// Рухається РІВЕНЬ, а не річний темп: знецінення лишається десятирічним,
// і прогноз під шоком рахується тим самим припущенням.
func TestFXShockDoesNotMoveDeval(t *testing.T) {
	s, st, srv := fxShockServer(t)
	ctx := context.Background()
	// Віяло сценаріїв існує лише тоді, коли є ціль із датою: без
	// дедлайну немає й самих сценаріїв, а знецінення живе саме в них.
	for k, v := range map[string]string{
		"goal_amount_uah": "1000000",
		"goal_date":       string(domain.NewDate(time.Now().AddDate(5, 0, 0))),
	} {
		if err := st.SetSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}

	before, err := s.buildState(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, body := getShock(t, srv.URL, 12)
	var got shockResp
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Episode == nil {
		t.Fatalf("епізод мав знайтись: %s", body)
	}
	if before.Forecast == nil || len(before.Forecast.Rows) == 0 {
		t.Fatal("віяло сценаріїв порожнє — порівнювати нема чого")
	}
	var after struct {
		Forecast struct {
			Rows []struct {
				DevaluationPct float64 `json:"devaluation_pct"`
			} `json:"rows"`
		} `json:"forecast"`
	}
	if err := json.Unmarshal(got.After, &after); err != nil {
		t.Fatal(err)
	}
	if len(before.Forecast.Rows) != len(after.Forecast.Rows) {
		t.Fatalf("віяло змінило довжину: %d → %d",
			len(before.Forecast.Rows), len(after.Forecast.Rows))
	}
	for i := range after.Forecast.Rows {
		if before.Forecast.Rows[i].DevaluationPct != after.Forecast.Rows[i].DevaluationPct {
			t.Errorf("знецінення зрушилось: %v → %v",
				before.Forecast.Rows[i].DevaluationPct, after.Forecast.Rows[i].DevaluationPct)
		}
	}
}

// Питання нічого не записує: курси в базі лишаються ті самі.
func TestFXShockWritesNothing(t *testing.T) {
	_, st, srv := fxShockServer(t)
	ctx := context.Background()

	beforeUSD, err := st.LatestRate(ctx, money.USD)
	if err != nil {
		t.Fatal(err)
	}
	beforeMonths, err := st.RateMonthCount(ctx, money.USD)
	if err != nil {
		t.Fatal(err)
	}
	if _, body := getShock(t, srv.URL, 12); body == "" {
		t.Fatal("порожня відповідь")
	}
	afterUSD, err := st.LatestRate(ctx, money.USD)
	if err != nil {
		t.Fatal(err)
	}
	afterMonths, err := st.RateMonthCount(ctx, money.USD)
	if err != nil {
		t.Fatal(err)
	}
	if beforeUSD != afterUSD || beforeMonths != afterMonths {
		t.Errorf("шок переписав історію: курс %d→%d, місяців %d→%d",
			beforeUSD, afterUSD, beforeMonths, afterMonths)
	}
}

// Витрати, названі у валюті, мусять перекластись УЖЕ за новим курсом —
// інакше достатність подушки мовчки міряється за старим. Рядок
// resolveExpensesUAH після підміни забути найлегше, і тримає його саме
// цей тест.
func TestFXShockReExpressesForeignExpenses(t *testing.T) {
	s, st, srv := fxShockServer(t)
	ctx := context.Background()
	for k, v := range map[string]string{
		"monthly_expenses":          "1000",
		"monthly_expenses_currency": money.USD,
		"reserve_target_months":     "6",
	} {
		if err := st.SetSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.buildState(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if before.Reserve == nil || before.Reserve.MonthlyExpensesUAH <= 0 {
		t.Fatalf("витрати не переклались і без шоку: %+v", before.Reserve)
	}

	_, body := getShock(t, srv.URL, 12)
	var got shockResp
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Episode == nil {
		t.Fatalf("епізод мав знайтись: %s", body)
	}
	var after struct {
		Reserve struct {
			MonthlyExpensesUAH float64 `json:"monthly_expenses_uah"`
		} `json:"reserve"`
	}
	if err := json.Unmarshal(got.After, &after); err != nil {
		t.Fatal(err)
	}
	if after.Reserve.MonthlyExpensesUAH <= before.Reserve.MonthlyExpensesUAH {
		t.Errorf("долар подорожчав, а місяць життя в гривні — ні: %v → %v",
			before.Reserve.MonthlyExpensesUAH, after.Reserve.MonthlyExpensesUAH)
	}
}
