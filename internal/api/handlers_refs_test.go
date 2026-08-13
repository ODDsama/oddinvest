package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"

	money "github.com/Rhymond/go-money"
)

// TestFundCatalogPutCarriesEveryField — усе, що надіслали в PUT, видно в
// наступному GET.
//
// Сторож стоїть проти конкретної поразки, а не проти уявної: у
// /api/fund-catalog обіцянка приймається як expected_yield_pct РЯДКОМ, а
// не як expected_yield_bp числом, — і запит із «правильною» на вигляд
// назвою поля проходив із 204 No Content, нічого не змінивши. Мовчазний
// no-op на ендпойнті, який відповідає «успіх», знайти можна лише
// порівнявши те, що поклали, з тим, що потім віддали.
//
// Обіг іде через HTTP навмисно: розбіжність живе саме між JSON-тегом
// запиту й полем store.Fund, тобто там, куди виклик методу не зазирає.
func TestFundCatalogPutCarriesEveryField(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	// Окремого «створити фонд» немає — його заводить перша операція.
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: "2026-08-13", Fund: "Inzhur MilTech", Kind: domain.FundBuy,
		Qty: 5, Amount: 500000, Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	funds, err := st.ListFunds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(funds) != 1 {
		t.Fatalf("операція мала завести один фонд, маємо %d", len(funds))
	}

	body := `{"name":"Inzhur MilTech","currency":"UAH","expected_yield_pct":"25",
		"expected_yield_currency":"UAH","payout_day":0,"kind":"accum",
		"close_date":"2029-07-26","buy_until":"2026-12-31",
		"income_tax_pct":"14","yield_simple_years":3}`
	resp, out := do(t, "PUT", fmt.Sprintf("%s/api/fund-catalog/%d", srv.URL, funds[0].ID), body)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT дав %d: %s", resp.StatusCode, out)
	}

	resp, out = do(t, "GET", srv.URL+"/api/fund-catalog", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET дав %d: %s", resp.StatusCode, out)
	}
	var got []store.Fund
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("розбір відповіді: %v (%s)", err, out)
	}
	if len(got) != 1 {
		t.Fatalf("очікували один фонд, маємо %d", len(got))
	}
	f := got[0]
	for _, c := range []struct {
		field string
		got   any
		want  any
	}{
		{"kind", f.Kind, store.FundAccumulating},
		{"close_date", f.CloseDate, "2029-07-26"},
		{"buy_until", f.BuyUntil, "2026-12-31"},
		{"income_tax_bp", f.IncomeTaxBP, int64(1400)},
		{"yield_simple_years", f.YieldSimpleYears, int64(3)},
		{"expected_yield_bp", f.ExpectedYieldBP, int64(2500)},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, поклали %v", c.field, c.got, c.want)
		}
	}
}

// TestFundCatalogRejectsBadTerm — вид і дати перевіряються, а не лягають
// у базу як завгодно.
//
// Помилковий вид фонду мовчки означав би «розподільний», тобто фонд,
// який нічого не платить, у моделі почав би платити. Дата в чужому
// форматі зламалась би не тут, а через три фази, у проєкції.
func TestFundCatalogRejectsBadTerm(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: "2026-08-13", Fund: "Inzhur MilTech", Kind: domain.FundBuy,
		Qty: 5, Amount: 500000, Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	funds, err := st.ListFunds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	url := fmt.Sprintf("%s/api/fund-catalog/%d", srv.URL, funds[0].ID)
	// Тіло без зіпсутого поля мусить проходити — інакше кожен рядок нижче
	// віддавав би 400 і без жодної перевірки, і тест зеленів би з хибної
	// причини. Саме так він і зеленів, доки мутація не прибрала перевірку
	// виду й нічого не змінилось.
	ok := `{"name":"Inzhur MilTech","currency":"UAH","expected_yield_pct":"25"`
	resp, out := do(t, "PUT", url, ok+`}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("здорове тіло дало %d: %s — решта перевірок нічого не варта",
			resp.StatusCode, out)
	}
	for _, c := range []struct{ what, body string }{
		{"невідомий вид", ok + `,"kind":"growth"}`},
		{"дата закриття", ok + `,"close_date":"26.07.2029"}`},
		{"остання купівля", ok + `,"buy_until":"грудень"}`},
		{"строк простої", ok + `,"yield_simple_years":99}`},
		{"податок понад 100%", ok + `,"income_tax_pct":"140"}`},
	} {
		resp, out := do(t, "PUT", url, c.body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: очікували 400, маємо %d (%s)", c.what, resp.StatusCode, out)
		}
	}
}

// TestFundCatalogEmptyPercentMeansUnset — порожня обіцянка це «не
// задано», а не помилка.
//
// Відсотки їздять рядком саме заради цієї різниці, і в довіднику так і
// написано в коментарі, — а код кликав parsePercentBP беззастережно й
// падав на порожньому рядку. Помітно це лише на фонді БЕЗ обіцянки, тобто
// на щойно заведеному операцією: у двох наявних вона задана, і PUT ходив.
func TestFundCatalogEmptyPercentMeansUnset(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: "2026-08-13", Fund: "Новий фонд", Kind: domain.FundBuy,
		Qty: 1, Amount: 100000, Currency: money.UAH, Broker: "inzhur",
	}); err != nil {
		t.Fatal(err)
	}
	funds, err := st.ListFunds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	url := fmt.Sprintf("%s/api/fund-catalog/%d", srv.URL, funds[0].ID)
	resp, out := do(t, "PUT", url,
		`{"name":"Фонд без обіцянки","currency":"UAH","expected_yield_pct":"","income_tax_pct":""}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("перейменування фонду без обіцянки дало %d: %s", resp.StatusCode, out)
	}
	got, err := st.ListFunds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Name != "Фонд без обіцянки" {
		t.Errorf("назва %q — перейменування не доїхало", got[0].Name)
	}
	if got[0].ExpectedYieldBP != 0 || got[0].IncomeTaxBP != 0 {
		t.Errorf("порожні відсотки дали %d/%d, очікували нулі",
			got[0].ExpectedYieldBP, got[0].IncomeTaxBP)
	}
}
