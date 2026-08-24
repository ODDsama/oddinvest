package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/store"
)

// switchSeed — довідник із ДВОХ паперів.
//
// Двох, а не одного, і це не надмір: із єдиним папером найкраща
// альтернатива завжди дорівнює тому, що вже в портфелі, і гілка з
// реальним порогом не виконувалась би жодного разу. Другий папір із
// вищою ставкою і є тією альтернативою.
func switchSeed(t *testing.T, st *store.Store) {
	t.Helper()
	mine := domain.Bond{ISIN: "UA4000227748", Nominal: money.New(100_000, money.UAH),
		RateBP: 1000, Maturity: "2028-03-17", Descr: "мій папір"}
	better := domain.Bond{ISIN: "UA4000239040", Nominal: money.New(100_000, money.UAH),
		RateBP: 1800, Maturity: "2028-03-17", Descr: "те, що дають зараз"}
	secs := []nbu.Security{
		{Bond: mine, Payments: []domain.Payment{
			{ISIN: mine.ISIN, PayDate: "2027-03-17", Type: domain.PayCoupon, PerBond: money.New(10_000, money.UAH)},
			{ISIN: mine.ISIN, PayDate: "2028-03-17", Type: domain.PayCoupon, PerBond: money.New(10_000, money.UAH)},
			{ISIN: mine.ISIN, PayDate: "2028-03-17", Type: domain.PayRedemption, PerBond: money.New(100_000, money.UAH)},
		}},
		{Bond: better, Payments: []domain.Payment{
			{ISIN: better.ISIN, PayDate: "2027-03-17", Type: domain.PayCoupon, PerBond: money.New(18_000, money.UAH)},
			{ISIN: better.ISIN, PayDate: "2028-03-17", Type: domain.PayCoupon, PerBond: money.New(18_000, money.UAH)},
			{ISIN: better.ISIN, PayDate: "2028-03-17", Type: domain.PayRedemption, PerBond: money.New(100_000, money.UAH)},
		}},
	}
	if err := st.ReplaceDirectory(t.Context(), secs, time.Now()); err != nil {
		t.Fatal(err)
	}
}

type switchListOut struct {
	Alt *struct {
		Kind    string  `json:"kind"`
		ISIN    string  `json:"isin"`
		RealPct float64 `json:"real_pct"`
	} `json:"alt"`
	Rows []struct {
		ISIN         string    `json:"isin"`
		Qty          int64     `json:"qty"`
		CostPerBond  moneyJSON `json:"cost_per_bond"`
		Accrued      moneyJSON `json:"accrued"`
		BreakEven    moneyJSON `json:"break_even"`
		BreakEvenPct float64   `json:"break_even_pct"`
		HoldRealPct  float64   `json:"hold_real_pct"`
		Reason       string    `json:"reason"`
	} `json:"rows"`
}

func switchList(t *testing.T, url string) switchListOut {
	t.Helper()
	resp, body := do(t, "GET", url+"/api/switch", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/switch: %d %s", resp.StatusCode, body)
	}
	var out switchListOut
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("розбір відповіді: %v (%s)", err, body)
	}
	return out
}

// Порожній портфель — порожній перелік, а не помилка. Питання «чи
// перекладати» без паперів просто не має адресата.
func TestSwitchEmptyPortfolio(t *testing.T) {
	srv, st := testServer(t)
	switchSeed(t, st)
	if rows := switchList(t, srv.URL).Rows; len(rows) != 0 {
		t.Errorf("на порожньому портфелі мало бути нуль рядків, маємо %d", len(rows))
	}
}

// Головне: поріг існує, він додатний і стоїть поруч із собівартістю —
// саме їх порівняння й відповідає на питання «продавати чи ні».
func TestSwitchThresholdForHeldPaper(t *testing.T) {
	srv, st := testServer(t)
	switchSeed(t, st)
	if resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"Дія"}`,
	); resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання лота: %d %s", resp.StatusCode, body)
	}

	out := switchList(t, srv.URL)
	if out.Alt == nil {
		t.Fatal("альтернативи немає — порівнювати не було з чим")
	}
	if len(out.Rows) != 1 {
		t.Fatalf("очікували один рядок, маємо %d", len(out.Rows))
	}
	r := out.Rows[0]
	if r.Reason != "" {
		t.Fatalf("рядок мовчить: %s", r.Reason)
	}
	if r.ISIN != "UA4000227748" || r.Qty != 5 {
		t.Errorf("рядок не про той папір: %+v", r)
	}
	if r.CostPerBond.Amount != "995.00" {
		t.Errorf("собівартість %s, очікували 995.00", r.CostPerBond.Amount)
	}
	be := parseAmount(t, r.BreakEven.Amount)
	if be <= 0 {
		t.Errorf("поріг %.2f — має бути додатним", be)
	}
	// Папір під 10% проти альтернативи під 18%: тримати його гірше, ніж
	// перекластися, тож віддати його варто вже нижче за номінал.
	if be >= 1000 {
		t.Errorf("поріг %.2f не нижчий за номінал, хоч альтернатива дохідніша", be)
	}
	if r.BreakEvenPct <= 0 || r.BreakEvenPct >= 100 {
		t.Errorf("поріг у %% номіналу = %.2f, очікували (0, 100)", r.BreakEvenPct)
	}
	if r.HoldRealPct == 0 {
		t.Error("дохідність утримання не порахувалась")
	}
}

// Поріг і вердикт мусять сходитись і НА РІВНІ HTTP, а не лише в домені:
// між ними стоїть переклад «реальна ↔ номінальна», і саме там колись
// зникло б знецінення.
func TestSwitchVerdictAgreesWithThreshold(t *testing.T) {
	srv, st := testServer(t)
	switchSeed(t, st)
	if resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"Дія"}`,
	); resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання лота: %d %s", resp.StatusCode, body)
	}
	be := switchList(t, srv.URL).Rows[0].BreakEven.Amount

	at := switchVerdict(t, srv.URL, "UA4000227748", be)
	if at.Worth {
		t.Error("рівно за порогом перекладання не мало бути вигідним")
	}
	if d := at.AltRealPct - at.HoldRealPct; d > 0.02 || d < -0.02 {
		t.Errorf("за порогом дохідності мали зрівнятись: утримання %.2f, альтернатива %.2f",
			at.HoldRealPct, at.AltRealPct)
	}

	above := switchVerdict(t, srv.URL, "UA4000227748", bump(t, be, 50))
	if !above.Worth {
		t.Error("вище за поріг перекладання мало бути вигідним")
	}
	if above.EdgePP <= 0 {
		t.Errorf("виграш у п.п. %.2f — очікували додатний", above.EdgePP)
	}
	// Виграш на позицію — це виграш на папір, помножений на кількість.
	// Множення робить сервер: у браузері воно було б другою копією
	// арифметики, а тут — перевіркою, що кількість узагалі врахована.
	per, total := parseAmount(t, above.GainPerBond.Amount), parseAmount(t, above.GainTotal.Amount)
	if diff := total - per*5; diff > 0.01 || diff < -0.01 {
		t.Errorf("виграш на позицію %.2f ≠ %.2f × 5", total, per)
	}

	below := switchVerdict(t, srv.URL, "UA4000227748", bump(t, be, -50))
	if below.Worth {
		t.Error("нижче за поріг перекладання не мало бути вигідним")
	}
}

// Найкраще доступне — те саме, що вже лежить: поріг тут вироджується в
// «продай і купи те саме», і замість числа має стояти пояснення.
func TestSwitchSaysNothingBetterThanWhatYouHold(t *testing.T) {
	srv, st := testServer(t)
	switchSeed(t, st)
	if resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000239040","qty":5,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"Дія"}`,
	); resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання лота: %d %s", resp.StatusCode, body)
	}
	out := switchList(t, srv.URL)
	if out.Alt == nil || out.Alt.ISIN != "UA4000239040" {
		t.Fatalf("найкращим мав бути дохідніший папір, маємо %+v", out.Alt)
	}
	r := out.Rows[0]
	if !strings.Contains(r.Reason, "цей самий папір") {
		t.Errorf("очікували пояснення про той самий папір, маємо %q", r.Reason)
	}
	// Поле лишається порожнім, а не нульовим: нуль тут прочитався б як
	// «поріг нуль», тобто «віддавай задарма».
	if r.BreakEven.Amount != "" {
		t.Errorf("порога тут бути не мало, маємо %q", r.BreakEven.Amount)
	}
}

// Дві помилки, які легко переплутати між собою, і плутати їх не можна:
// паперу немає в ДОВІДНИКУ — це 404, паперу немає в ПОРТФЕЛІ — це 409.
func TestSwitchVerdictRejectsUnknownAndUnheld(t *testing.T) {
	srv, st := testServer(t)
	switchSeed(t, st)
	if resp, body := do(t, "POST", srv.URL+"/api/lots",
		`{"isin":"UA4000227748","qty":1,"price_per_bond":"995.00","buy_date":"2026-07-01","channel":"Дія"}`,
	); resp.StatusCode != http.StatusCreated {
		t.Fatalf("додавання лота: %d %s", resp.StatusCode, body)
	}
	for _, c := range []struct {
		name, isin string
		want       int
	}{
		{"немає в довіднику", "UA0000000000", http.StatusNotFound},
		{"немає в портфелі", "UA4000239040", http.StatusConflict},
	} {
		resp, body := do(t, "POST", srv.URL+"/api/switch",
			`{"isin":"`+c.isin+`","clean":"1000.00"}`)
		if resp.StatusCode != c.want {
			t.Errorf("%s: %d, очікували %d (%s)", c.name, resp.StatusCode, c.want, body)
		}
	}
}

func switchVerdict(t *testing.T, url, isin, clean string) switchVerdictOut {
	t.Helper()
	resp, body := do(t, "POST", url+"/api/switch",
		`{"isin":"`+isin+`","clean":"`+clean+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/switch: %d %s", resp.StatusCode, body)
	}
	var out switchVerdictOut
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("розбір відповіді: %v (%s)", err, body)
	}
	return out
}

func parseAmount(t *testing.T, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("сума %q не розбирається: %v", s, err)
	}
	return v
}

// bump — та сама сума, зсунута на кілька гривень. Рядком, бо саме рядок
// приймає ендпойнт, і гнати число через float означало б перевіряти
// заразом і власне заокруглення.
func bump(t *testing.T, amount string, delta int64) string {
	t.Helper()
	minor, err := domain.ParseDecimalToMinor(amount, money.UAH)
	if err != nil {
		t.Fatal(err)
	}
	return toMoneyJSON(money.New(minor+delta*100, money.UAH)).Amount
}
