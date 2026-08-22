package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// totalOf — зведений результат зі зведення, розібраний у щось перевірюване.
type totalOf struct {
	XIRRPct   *float64 `json:"xirr_pct"`
	GainUAH   float64  `json:"gain_uah"`
	GainPct   float64  `json:"gain_pct"`
	MoneyDays float64  `json:"money_days"`
	MinDays   int      `json:"min_days"`
	FXLag     int      `json:"fx_max_lag_days"`
}

func summaryTotal(t *testing.T, srv string) (totalOf, map[string]float64, bool) {
	t.Helper()
	var sum struct {
		Total *totalOf           `json:"total_return"`
		XIRR  map[string]float64 `json:"xirr"`
	}
	_, body := do(t, "GET", srv+"/api/summary", "")
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, body)
	}
	if sum.Total == nil {
		return totalOf{}, sum.XIRR, false
	}
	return *sum.Total, sum.XIRR, true
}

// ГОЛОВНИЙ ІНВАРІАНТ. Портфель лише з гривні мусить дати зведений XIRR,
// рівний гривневому — до символу.
//
// Саме він робить нове число перевірюваним, а не просто правдоподібним:
// гривневі потоки через asOfRates.uah не конвертуються взагалі, тож
// будь-яка розбіжність тут — помилка згортання, а не курсу.
func TestTotalReturnEqualsUAHWhenPortfolioIsUAHOnly(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	today := domain.NewDate(time.Now())

	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: "UAH", Principal: 10_000_000, RateBP: 1600,
		OpenDate: today.AddDays(-200), MaturityDate: today.AddDays(165),
		Payout: domain.PayoutMonthly, TaxBP: 1950,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: today.AddDays(-150), Fund: "Inzhur REIT", Kind: domain.FundBuy,
		Qty: 100, Amount: 100000, Currency: "UAH",
	}); err != nil {
		t.Fatal(err)
	}

	total, xirr, ok := summaryTotal(t, srv.URL)
	if !ok {
		t.Fatal("зведене число мало зʼявитись на гривневому портфелі")
	}
	if total.XIRRPct == nil {
		t.Fatalf("річна ставка мала порахуватись: %+v", total)
	}
	uah, has := xirr["UAH"]
	if !has {
		t.Fatal("гривнева плитка мала бути")
	}
	if *total.XIRRPct != uah {
		t.Errorf("зведений XIRR %v мав дорівнювати гривневому %v", *total.XIRRPct, uah)
	}
}

// Курсу на дату потоку немає — число мовчить ЦІЛКОМ, а валютні плитки
// лишаються на місці.
//
// Це не педантизм, а захист від дуже приємного числа: asOfRates.uah на
// відсутній курс повертає нуль, тобто купівля перетворилась би на нуль, а
// термінальна вартість (сьогодні, курс є завжди) лишилась би — і
// дохідність полетіла б у стелю.
func TestTotalReturnSilentWhenRateMissingOnFlowDate(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	today := domain.NewDate(time.Now())

	// Курс є ЛИШЕ на сьогодні; доларові операції — двісті днів тому.
	if err := st.SaveRate(ctx, "USD", 440000, today); err != nil {
		t.Fatal(err)
	}
	for _, op := range []domain.FundOp{
		{Date: today.AddDays(-200), Fund: "Долар-фонд", Kind: domain.FundBuy,
			Qty: 10, Amount: 100000, Currency: "USD"},
		{Date: today.AddDays(-100), Fund: "Долар-фонд", Kind: domain.FundBuy,
			Qty: 1, Amount: 12000, Currency: "USD"},
	} {
		if _, err := st.AddFundOp(ctx, op); err != nil {
			t.Fatal(err)
		}
	}

	_, xirr, ok := summaryTotal(t, srv.URL)
	if ok {
		t.Error("без курсу на дату купівлі зведене число мало промовчати")
	}
	if _, has := xirr["USD"]; !has {
		t.Error("мовчання зведеного не мало гасити доларову плитку")
	}
}

// Результат у гривнях чесний із першого дня, бо не ануалізований, — тож
// обʼєкт є навіть тоді, коли річної ставки ще немає. Без цього на екрані
// знову зʼявився б прочерк, який колись уже закривав Realized.
func TestTotalReturnGivesGainBeforeAnnualising(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	today := domain.NewDate(time.Now())

	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: today.AddDays(-3), Fund: "Свіжий", Kind: domain.FundBuy,
		Qty: 100, Amount: 100000, Currency: "UAH",
	}); err != nil {
		t.Fatal(err)
	}
	total, _, ok := summaryTotal(t, srv.URL)
	if !ok {
		t.Fatal("обʼєкт мав бути навіть на триденному портфелі")
	}
	if total.XIRRPct != nil {
		t.Errorf("ануалізувати три дні не можна, маємо %v", *total.XIRRPct)
	}
	if total.MinDays != 30 || total.MoneyDays > 5 {
		t.Errorf("підпис мав назвати поріг і вік грошей: %+v", total)
	}
}

// Курс береться НА ДАТУ ПОТОКУ, а не сьогоднішній. Дві бази з однаковими
// операціями й різною історією курсів мусять дати різні числа.
func TestTotalReturnUsesRateOfFlowDate(t *testing.T) {
	run := func(t *testing.T, buyRate int64) float64 {
		t.Helper()
		srv, st := testServer(t)
		ctx := context.Background()
		today := domain.NewDate(time.Now())
		for _, r := range []struct {
			d domain.Date
			v int64
		}{{today.AddDays(-300), buyRate}, {today, 440000}} {
			if err := st.SaveRate(ctx, "USD", r.v, r.d); err != nil {
				t.Fatal(err)
			}
		}
		for _, op := range []domain.FundOp{
			{Date: today.AddDays(-300), Fund: "Ф", Kind: domain.FundBuy,
				Qty: 100, Amount: 100000, Currency: "USD"},
			{Date: today.AddDays(-100), Fund: "Ф", Kind: domain.FundBuy,
				Qty: 1, Amount: 1100, Currency: "USD"},
		} {
			if _, err := st.AddFundOp(ctx, op); err != nil {
				t.Fatal(err)
			}
		}
		total, _, ok := summaryTotal(t, srv.URL)
		if !ok || total.XIRRPct == nil {
			t.Fatalf("число мало порахуватись: %+v", total)
		}
		return *total.XIRRPct
	}
	// Розкид навмисно помірний: купівля по 27 при сьогоднішніх 44 дала б
	// 104% річних, а смуга правдоподібності (±100%) таке ховає як шум
	// ануалізації — і тест перевіряв би не курс, а смугу.
	cheap := run(t, 350000) // купували по 35
	dear := run(t, 430000)  // купували по 43
	if math.Abs(cheap-dear) < 1 {
		t.Errorf("курс на дату купівлі мав змінити результат: %v проти %v", cheap, dear)
	}
	// Купівля за дешевим доларом означає більший гривневий приріст.
	if cheap <= dear {
		t.Errorf("дешевша купівля мала дати вищу гривневу дохідність: %v проти %v", cheap, dear)
	}
}

// Ручка /api/xirr віддає зведене поруч із валютними.
func TestXIRREndpointCarriesTotal(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	today := domain.NewDate(time.Now())
	if _, err := st.AddFundOp(ctx, domain.FundOp{
		Date: today.AddDays(-200), Fund: "Ф", Kind: domain.FundBuy,
		Qty: 100, Amount: 100000, Currency: "UAH",
	}); err != nil {
		t.Fatal(err)
	}
	resp, body := do(t, "GET", srv.URL+"/api/xirr", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("xirr: %d %s", resp.StatusCode, body)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("xirr: %v: %s", err, body)
	}
	if _, has := got["total"]; !has {
		t.Errorf("ручка мала нести зведене число: %s", body)
	}
}
