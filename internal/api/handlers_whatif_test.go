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
	"github.com/ODDsama/oddinvest/internal/store"
)

// whatIfServer — портфель, у якому вже щось є: без нього «до» і «після»
// однакові за побудовою, і тест не перевіряв би нічого.
func whatIfServer(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	srv, st := testServer(t)
	seed(t, st)
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: domain.NewDate(time.Now()), Amount: 100_000_00,
		Currency: money.UAH, Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddLot(ctx, domain.Lot{
		ISIN: "UA4000227748", Qty: 5, PricePerBond: money.New(99500, money.UAH),
		BuyDate: domain.NewDate(time.Now().AddDate(0, 0, -10)), Channel: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	return srv.URL
}

// Порожній план мусить дати документ, НЕВІДРІЗНИМИЙ від /api/summary.
//
// Це головна гарантія всього прийому: якщо вони збігаються, то й
// віднімання «після мінус до» на фронтенді законне — обидва числа
// народжені одним кодом, а не двома різними уявленнями про частки.
func TestWhatIfEmptyPlanMatchesSummary(t *testing.T) {
	url := whatIfServer(t)
	_, summary := do(t, "GET", url+"/api/summary", "")
	resp, body := do(t, "POST", url+"/api/whatif", `{"saved":false}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	var got struct {
		After json.RawMessage `json:"after"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	// generated_at рухається між двома запитами — прибираємо саме його,
	// а не порівнюємо «приблизно»: решта мусить збігатись до символу.
	//
	// tasks прибираємо з іншої причини, і вона змістовна. Черга задач — це
	// ПОРАДА про сьогоднішній світ, а не стан портфеля: /api/summary її
	// несе, «до/після» кошика — ні, і саме так і має бути. У гіпотетичному
	// «після» вона радила б купити те, що ти в ньому щойно купив, а рахувати
	// її означало б ганяти SearchBonds на п'ять тисяч паперів на кожен
	// перерахунок кошика. Тест же питає інше: чи не зрушив порожній кошик
	// ЧИСЛА.
	strip := func(s string) string {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		delete(m, "generated_at")
		delete(m, "tasks")
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if a, b := strip(summary), strip(string(got.After)); a != b {
		t.Errorf("порожній план змінив стан:\n/api/summary: %s\n/api/whatif:  %s", a, b)
	}
}

// Покупка рухає ті самі числа, що й справжня: частку валюти, капітал,
// драбину, готівку брокера. Жодне з них кошик не рахує сам.
func TestWhatIfMovesSharesAndCash(t *testing.T) {
	url := whatIfServer(t)
	var before, after struct {
		CapitalUAH float64                       `json:"capital_uah"`
		NominalUAH float64                       `json:"nominal_uah_eq"`
		Brokers    map[string]map[string]float64 `json:"brokers"`
		Ladder     []struct{ Year int }          `json:"ladder"`
	}
	_, b := do(t, "GET", url+"/api/summary", "")
	if err := json.Unmarshal([]byte(b), &before); err != nil {
		t.Fatal(err)
	}
	resp, body := do(t, "POST", url+"/api/whatif",
		`{"draft":[{"kind":"bond","ref":"UA4000227748","qty":3}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	var got struct {
		After  json.RawMessage `json:"after"`
		Basket struct {
			Lines []struct {
				Label   string `json:"label"`
				Qty     int64  `json:"qty"`
				Broker  string `json:"broker"`
				Assumed bool   `json:"broker_assumed"`
				Total   struct {
					Amount   string `json:"amount"`
					Currency string `json:"currency"`
				} `json:"total"`
			} `json:"lines"`
			Totals []struct {
				Amount string `json:"amount"`
			} `json:"totals"`
		} `json:"basket"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got.After, &after); err != nil {
		t.Fatal(err)
	}
	// Три папери по 1000 номіналу: номінал портфеля виріс рівно на 3000.
	if d := after.NominalUAH - before.NominalUAH; d != 3000 {
		t.Errorf("номінал зріс на %.2f, хочемо 3000", d)
	}
	// Гроші пішли з рахунку того брокера, у якого їх було найбільше.
	if after.Brokers["mono"]["UAH"] >= before.Brokers["mono"]["UAH"] {
		t.Errorf("готівка mono не зменшилась: було %.2f, стало %.2f",
			before.Brokers["mono"]["UAH"], after.Brokers["mono"]["UAH"])
	}
	if len(got.Basket.Lines) != 1 {
		t.Fatalf("рядків кошика %d", len(got.Basket.Lines))
	}
	ln := got.Basket.Lines[0]
	// Брокера не називали — застосунок обрав його сам і мусить це сказати.
	if ln.Broker != "mono" || !ln.Assumed {
		t.Errorf("брокер рядка: %+v", ln)
	}
	if ln.Qty != 3 || ln.Total.Currency != money.UAH {
		t.Errorf("рядок кошика: %+v", ln)
	}
}

// Нестача НЕ блокує: те саме правило, що й у лімітів концентрації —
// застосунок показує наслідки, а рішення за людиною. Гроші могли ще не
// прийти, і побачити картину наперед так само корисно.
func TestWhatIfReportsShortfallWithoutBlocking(t *testing.T) {
	url := whatIfServer(t)
	resp, body := do(t, "POST", url+"/api/whatif",
		`{"draft":[{"kind":"bond","ref":"UA4000227748","qty":500}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("нестача не мала блокувати: %d %s", resp.StatusCode, body)
	}
	var got struct {
		Basket struct {
			Totals []struct {
				Amount string `json:"amount"`
			} `json:"totals"`
			Shorts []struct {
				Broker string `json:"broker"`
				Short  struct {
					Amount string `json:"amount"`
				} `json:"short"`
			} `json:"shorts"`
		} `json:"basket"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Basket.Shorts) != 1 || got.Basket.Shorts[0].Broker != "mono" {
		t.Fatalf("нестача не названа: %s", body)
	}
	// Точна арифметика, а не «не нуль»: кошик і форма покупки рахують
	// нестачу одним shortfallMinor, і саме тут видно, що кошик справді
	// віднімає БАЛАНС, а не щось на нього схоже.
	//
	// Сама вартість 500 паперів залежить від НКД на сьогодні, тож у числі
	// її не фіксуємо — беремо з тієї ж відповіді. Баланс же відомий точно:
	// 100 000 ₴ поповнення мінус лот 5×995 у whatIfServer.
	if len(got.Basket.Totals) != 1 {
		t.Fatalf("чекали один підсумок валюти: %s", body)
	}
	const haveMinor = 95_025_00
	total, err := domain.ParseDecimalToMinor(got.Basket.Totals[0].Amount, money.UAH)
	if err != nil {
		t.Fatal(err)
	}
	want := toMoneyJSON(money.New(total-haveMinor, money.UAH)).Amount
	if got.Basket.Shorts[0].Short.Amount != want {
		t.Errorf("нестача %s, чекали %s (кошик %s мінус баланс 95025.00)",
			got.Basket.Shorts[0].Short.Amount, want, got.Basket.Totals[0].Amount)
	}
}

// Папір поза довідником — помилка з іменем, а не тиха покупка нізвідки.
func TestWhatIfRejectsUnknownBond(t *testing.T) {
	url := whatIfServer(t)
	resp, body := do(t, "POST", url+"/api/whatif",
		`{"draft":[{"kind":"bond","ref":"UA0000000000","qty":1}]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("хочемо 400, маємо %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "довідник") {
		t.Errorf("помилка мовчить про причину: %s", body)
	}
}
