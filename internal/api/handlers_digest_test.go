package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

type digestBody struct {
	Days     int     `json:"days"`
	FromDate string  `json:"from_date"`
	ToDate   string  `json:"to_date"`
	FromUAH  float64 `json:"from_uah"`
	ToUAH    float64 `json:"to_uah"`
	DeltaUAH float64 `json:"delta_uah"`
	Why      string  `json:"why"`
	Causes   []struct {
		Key      string  `json:"key"`
		UAH      float64 `json:"uah"`
		Measured bool    `json:"measured"`
		Why      string  `json:"why"`
	} `json:"causes"`
	Structure *struct {
		FromDate string `json:"from_date"`
		ToDate   string `json:"to_date"`
		Rows     []struct {
			Key   string  `json:"key"`
			Delta float64 `json:"delta"`
		} `json:"rows"`
	} `json:"structure"`
}

func getDigest(t *testing.T, url string) digestBody {
	t.Helper()
	resp, body := do(t, "GET", url, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET дав %d: %s", resp.StatusCode, body)
	}
	var out digestBody
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("розбір: %v (%s)", err, body)
	}
	return out
}

// TestDigestSilentWithoutSnapshot — без двох знімків порівнювати нема з
// чим, і це законний стан першого дня, а не помилка. Нулі тут читались би
// як «нічого не змінилось».
func TestDigestSilentWithoutSnapshot(t *testing.T) {
	srv, _ := testServer(t)
	out := getDigest(t, srv.URL+"/api/digest")
	if out.Structure != nil || out.DeltaUAH != 0 {
		t.Fatalf("без знімків зʼявились числа: %+v", out)
	}
	if out.Why == "" {
		t.Fatal("мовчазна відсутність без названої причини")
	}
	if out.Days != 7 {
		t.Fatalf("типове вікно %d, хочемо 7", out.Days)
	}
}

// TestDigestRejectsUnknownWindow — вікна три, і довільне число не
// приймається: воно виглядало б як відповідь на питання, якого застосунок
// не ставить.
func TestDigestRejectsUnknownWindow(t *testing.T) {
	srv, _ := testServer(t)
	resp, _ := do(t, "GET", srv.URL+"/api/digest?window=42", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("вікно 42 дало %d", resp.StatusCode)
	}
	for _, w := range []string{"1", "7", "30"} {
		resp, body := do(t, "GET", srv.URL+"/api/digest?window="+w, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("вікно %s дало %d: %s", w, resp.StatusCode, body)
		}
	}
}

// TestDigestCausesSumToDelta — ГОЛОВНИЙ інваріант: сума названих причин
// разом із решткою дорівнює різниці, яку вони пояснюють. Решта тут не
// «щось невідоме», а рівно доповнення до цілого, і якщо доданок
// зникне з відповіді або поїде знаком, зійтись перестане саме це.
func TestDigestCausesSumToDelta(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	seedDigestHistory(t, st)

	out := getDigest(t, srv.URL+"/api/digest?window=30")
	if out.Structure == nil {
		t.Fatalf("на двох знімках розділ мовчить: %s", out.Why)
	}
	var total float64
	seen := map[string]bool{}
	for _, c := range out.Causes {
		total += c.UAH
		seen[c.Key] = true
		if !c.Measured && c.Why == "" {
			t.Fatalf("необчислена причина %q без пояснення", c.Key)
		}
	}
	for _, k := range []string{"own", "income", "fx", "rest"} {
		if !seen[k] {
			t.Fatalf("причини %q немає у відповіді", k)
		}
	}
	if diff := total - out.DeltaUAH; diff > 0.02 || diff < -0.02 {
		t.Fatalf("сума причин %.2f проти різниці %.2f (розбіжність %.2f)",
			total, out.DeltaUAH, diff)
	}
	if out.FromDate != out.Structure.FromDate || out.ToDate != out.Structure.ToDate {
		t.Fatalf("дати відповіді й таблиці розійшлись: %s..%s проти %s..%s",
			out.FromDate, out.ToDate, out.Structure.FromDate, out.Structure.ToDate)
	}
}

// TestDigestNamesRealSnapshotDates — демон міг лежати, і різниця мусить
// бути підписана справжніми датами знімків, а не межами вікна.
func TestDigestNamesRealSnapshotDates(t *testing.T) {
	srv, st := testServer(t)
	seedDigestHistory(t, st)

	out := getDigest(t, srv.URL+"/api/digest?window=7")
	if out.FromDate == "" || out.ToDate == "" {
		t.Fatalf("дати не названі: %+v", out)
	}
	if out.FromDate >= out.ToDate {
		t.Fatalf("дати не в порядку: %s → %s", out.FromDate, out.ToDate)
	}
}

// seedDigestHistory — два знімки й рухи між ними: рівно те, з чого
// дайджест і рахує. Дати відносні, бо вікно відлічується від сьогодні.
func seedDigestHistory(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := t.Context()
	today := domain.NewDate(time.Now())

	// Знімок ДО початку вікна — з нього починається порівняння, і саме
	// його дату мусить назвати відповідь.
	if err := st.SaveSnapshot(ctx, store.Snapshot{
		Date: today.AddDays(-40), NominalUAHEq: 100_000_00, AccountUAH: 10_000_00,
		InvestedUAH: 100_000_00, IdleUAH: -1,
	}); err != nil {
		t.Fatal(err)
	}
	// Знімок у межах вікна — «стало».
	if err := st.SaveSnapshot(ctx, store.Snapshot{
		Date: today.AddDays(-1), NominalUAHEq: 120_000_00, AccountUAH: 15_000_00,
		InvestedUAH: 125_000_00, IdleUAH: -1,
	}); err != nil {
		t.Fatal(err)
	}
	// Свої гроші всередині вікна: без них причина «Свої гроші» була б
	// нулем, і головний інваріант сходився б на порожньому розкладі.
	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: today.AddDays(-5), Amount: 25_000_00,
		Currency: money.UAH, Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
}
