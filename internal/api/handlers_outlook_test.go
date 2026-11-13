package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ODDsama/oddinvest/internal/engine"
)

type outlookResp struct {
	Pace struct {
		Monthly float64 `json:"monthly"`
		Bars    []struct {
			Month  string  `json:"month"`
			Amount float64 `json:"amount"`
		} `json:"bars"`
	} `json:"pace"`
	Points []struct {
		Month   int     `json:"month"`
		Amount  float64 `json:"amount"`
		Nominal float64 `json:"nominal"`
	} `json:"points"`
	Why string `json:"why"`
}

func getOutlook(t *testing.T, url string) outlookResp {
	t.Helper()
	resp, body := do(t, "GET", url+"/api/outlook", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/outlook: %d %s", resp.StatusCode, body)
	}
	var out outlookResp
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Маршрут наскрізь на справжньому сховищі: лот, куплений десять днів тому,
// — це зовнішні гроші, тож темп є, і прогноз мусить бути повний.
//
// Друга половина — валюта звітності. Суми перекладаються, а «на рахунку
// буде» обнуляється: майбутня гривня за сьогоднішнім курсом — число, якого
// не існує, і показати його доларом означало б його вигадати.
func TestOutlookEndpoint(t *testing.T) {
	_, _, srv := fxShockServer(t)

	uah := getOutlook(t, srv.URL)
	if uah.Why != "" || uah.Pace.Monthly <= 0 {
		t.Fatalf("темпу немає, хоч лот куплено щойно: monthly=%v why=%q", uah.Pace.Monthly, uah.Why)
	}
	if len(uah.Points) != engine.OutlookMonths+1 {
		t.Fatalf("точок %d", len(uah.Points))
	}
	if len(uah.Pace.Bars) != 12 {
		t.Fatalf("стовпчиків темпу %d", len(uah.Pace.Bars))
	}
	if last := uah.Pace.Bars[len(uah.Pace.Bars)-1]; last.Amount <= 0 && uah.Pace.Bars[len(uah.Pace.Bars)-2].Amount <= 0 {
		t.Errorf("купівлі десятиденної давнини немає ні в цьому, ні в минулому місяці: %+v", uah.Pace.Bars)
	}
	p := uah.Points[120]
	if p.Amount <= 0 || p.Nominal < p.Amount {
		t.Fatalf("десятий рік у гривні: сума %v, номінал %v", p.Amount, p.Nominal)
	}

	// Обсяг: «усі гроші» — законний параметр, чужий — 400, а не тихий спад
	// на типове.
	if resp, body := do(t, "GET", srv.URL+"/api/outlook?scope=all", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("scope=all: %d %s", resp.StatusCode, body)
	}
	if resp, _ := do(t, "GET", srv.URL+"/api/outlook?scope=everything", ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("чужий scope: %d, а має бути 400", resp.StatusCode)
	}

	if resp, body := do(t, "PUT", srv.URL+"/api/settings", `{"report_currency":"USD"}`); resp.StatusCode >= 300 {
		t.Fatalf("PUT settings: %d %s", resp.StatusCode, body)
	}
	usd := getOutlook(t, srv.URL)
	q := usd.Points[120]
	if q.Amount <= 0 || q.Amount >= p.Amount {
		t.Errorf("сума в доларах %v проти %v у гривні — не перекладено", q.Amount, p.Amount)
	}
	if q.Nominal != 0 {
		t.Errorf("номінал у доларовому документі мав обнулитись, а стоїть %v", q.Nominal)
	}
}
