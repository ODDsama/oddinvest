package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
	money "github.com/Rhymond/go-money"
)

func yearOf(t *testing.T, srv string, year string) yearResp {
	t.Helper()
	url := srv + "/api/year"
	if year != "" {
		url += "?year=" + year
	}
	resp, body := do(t, "GET", url, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: %d %s", url, resp.StatusCode, body)
	}
	var got yearResp
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return got
}

// Рік і дванадцять його місяців — ті самі гроші, і дні хітмапу в сумі
// дають ті самі статті: хітмап — це виписка, розкладена по днях, а не
// другий рахунок.
//
// Доти рік звірявся зі звітом про рух рахунку (/api/cashflow); рахунків
// більше немає (ревізія 2026-10-03), і другим обчисленням того самого
// стала сума місячних підсумків — «Місяць» і «Рік» читають той самий
// журнал межі інструмента, і розійтись їм нема на чому, доки стоїть тест.
func TestYearMoneyAgreesWithMonthsAndDays(t *testing.T) {
	srv, st := testServer(t)
	seedPeriodMonth(t, st)

	got := yearOf(t, srv.URL, "2026")
	m := got.Money

	var mIncome, mContrib, mPurchase, mOwn float64
	for mo := 1; mo <= 12; mo++ {
		pm := periodOf(t, srv.URL, fmt.Sprintf("2026-%02d", mo)).Money
		mIncome += pm.IncomeUAH.Major()
		mContrib += pm.ContribUAH.Major()
		mPurchase += pm.PurchaseUAH.Major()
		mOwn += pm.OwnUAH.Major()
	}
	if domain.Round2(mIncome) != m.IncomeUAH.Major() || domain.Round2(mContrib) != m.ContribUAH.Major() ||
		domain.Round2(mPurchase) != m.PurchaseUAH.Major() || domain.Round2(mOwn) != m.OwnUAH.Major() {
		t.Errorf("рік %+v розійшовся з сумою місяців (дохід %v, внесено %v, покупки %v, своїх %v)",
			m, mIncome, mContrib, mPurchase, mOwn)
	}
	// Нетто: внесено = покупки мінус виплати.
	if domain.Round2(m.PurchaseUAH.Major()-m.IncomeUAH.Major()) != m.ContribUAH.Major() {
		t.Errorf("внесено %v ≠ покупки %v − дохід %v",
			m.ContribUAH.Major(), m.PurchaseUAH.Major(), m.IncomeUAH.Major())
	}
	var contrib, income, purchase float64
	for _, d := range got.Days {
		contrib += d.OutsideUAH.Major()
		income += d.IncomeUAH.Major()
		purchase -= d.PurchaseUAH.Major()
		if d.Lvl < 1 || d.Lvl > 4 {
			t.Errorf("%s: рівень %d поза 1..4", d.Date, d.Lvl)
		}
	}
	// Рух дня поза інструментами (outside_uah) — лише подушка й цілі: рух на межі
	// інструмента день уже несе покупкою й доходом, і другий раз у внесок
	// його не кладуть. Тож дні сходяться з роком так: внески днів — це
	// outside_uah, а покупки мінус дохід плюс вони — own_uah.
	if domain.Round2(contrib) != m.OutsideUAH.Major() || domain.Round2(income) != m.IncomeUAH.Major() ||
		domain.Round2(purchase) != m.PurchaseUAH.Major() {
		t.Errorf("дні (%v/%v/%v) не сходяться зі статтями %+v", contrib, income, purchase, m)
	}
	if domain.Round2(purchase-income+contrib) != m.OwnUAH.Major() {
		t.Errorf("дні дають своїх %v, а рік — %v", domain.Round2(purchase-income+contrib), m.OwnUAH.Major())
	}
	if got.EarnedUAH.Major()+got.PrincipalUAH.Major() != m.IncomeUAH.Major() {
		t.Errorf("зароблене %v + тіло %v ≠ дохід %v", got.EarnedUAH.Major(), got.PrincipalUAH.Major(), m.IncomeUAH.Major())
	}
	if len(got.Years) == 0 || got.Years[0] < 2026 {
		t.Errorf("роки %v мають починатись із поточного", got.Years)
	}
}

// Рік без жодної події не падає й не вигадує чисел; неправильний рік —
// 400, а не порожній рік.
func TestYearEmptyAndBadInput(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)
	got := yearOf(t, srv.URL, "2019")
	if got.Money.OwnUAH.Major() != 0 || len(got.Days) != 0 || got.BestMonth != nil {
		t.Errorf("порожній рік мав бути порожнім: %+v", got)
	}
	if got.Partial {
		t.Error("2019 давно закрився — не partial")
	}
	if resp, _ := do(t, "GET", srv.URL+"/api/year?year=abc", ""); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("рік «abc» мав дати 400, дав %d", resp.StatusCode)
	}
}

// Квартилі, а не частка від максимуму: один великий внесок не робить
// решту року блідою.
func TestYearHeatLevelsByQuartile(t *testing.T) {
	byDay := map[string]*yearDay{}
	for i, v := range []float64{10, 20, 30, 40, 1_000_000} {
		d := &yearDay{Date: "2026-01-0" + string(rune('1'+i)), OutsideUAH: state.Major(v, money.UAH)}
		byDay[d.Date] = d
	}
	days := heatDays(byDay)
	if len(days) != 5 {
		t.Fatalf("днів %d, чекали 5", len(days))
	}
	if days[0].Lvl != 1 || days[4].Lvl != 4 || days[2].Lvl == 1 {
		t.Errorf("рівні %v: перший 1, останній 4, середній не 1", days)
	}
}
