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

// spendServer — портфель, гроші й КАРТКА з датою виходу з ліміту.
//
// Картка тут не декорація: без ExitBy і без боргу режим виходу з ліміту
// не будується взагалі (state_debts.go), а саме він і є тим дротом між
// двома контурами, заради якого фіча існує.
func spendServer(t *testing.T) (*Server, *store.Store, *httptest.Server, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	seed(t, st)

	if _, err := st.AddDeposit(ctx, store.Deposit{
		Date: domain.NewDate(time.Now().AddDate(0, 0, -30)), Amount: 200_000_00,
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

	cardID, err := st.AddDebt(ctx, domain.Debt{
		Name: "ПУМБ", Kind: domain.DebtCard, Currency: money.UAH,
		LimitAmount: 200_000_00, StatementDay: 10, APRBp: 4200,
		MinPaymentBp: 500, MinPaymentFloor: 100_00,
		ExitBy: domain.Date(fmt.Sprintf("%d-12-31", time.Now().Year()+1)),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Звірка МІСЯЦЬ ТОМУ: сьогоднішня проковтнула б покупку сьогоднішнім
	// днем (довід — у spendOnCard), і тест міряв би не те.
	if _, err := st.AddDebtMark(ctx, domain.DebtMark{
		DebtID: cardID, Date: domain.NewDate(time.Now().AddDate(0, 0, -30)),
		Balance: -50_000_00,
	}); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	s := New(st, nil, log)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return s, st, srv, cardID
}

type spendResp struct {
	After json.RawMessage `json:"after"`
	Cost  struct {
		Alternative *struct {
			Kind    string  `json:"kind"`
			Label   string  `json:"label"`
			RealPct float64 `json:"real_pct"`
			YearUAH float64 `json:"year_uah"`
		} `json:"alternative"`
		AlternativeWhy string `json:"alternative_why"`
		Credit         *struct {
			Basis    string  `json:"basis"`
			APRPct   float64 `json:"apr_pct"`
			ExtraUAH float64 `json:"extra_uah"`
			Prepay   string  `json:"prepay"`
		} `json:"credit"`
	} `json:"cost"`
}

func postSpend(t *testing.T, url, body string) (int, spendResp, string) {
	t.Helper()
	resp, raw := do(t, "POST", url+"/api/spend", body)
	var got spendResp
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("%v: %s", err, raw)
		}
	}
	return resp.StatusCode, got, raw
}

// ГОЛОВНИЙ ТЕСТ ПРИЙОМУ. Порожня витрата мусить дати документ,
// невідрізнимий від /api/summary — саме це робить законним віднімання
// «після мінус до» на фронтенді. Близнюк TestWhatIfEmptyPlanMatchesSummary.
func TestSpendEmptyMatchesSummary(t *testing.T) {
	_, _, srv, _ := spendServer(t)
	_, summary := do(t, "GET", srv.URL+"/api/summary", "")
	code, got, raw := postSpend(t, srv.URL, `{"amount":"0"}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, raw)
	}
	if a, b := stripDoc(t, []byte(summary)), stripDoc(t, got.After); a != b {
		t.Errorf("порожня витрата змінила стан:\n/api/summary: %s\n/api/spend:   %s", a, b)
	}
}

// Порожня гіпотеза витрати не вмикає прийом — те саме, що найлегше
// зламати, забувши поле в empty().
func TestSpendEmptyHypothesisStaysEmpty(t *testing.T) {
	if (hypothetical{cash: []store.Deposit{{Amount: -1}}}).empty() {
		t.Error("витрата вважається порожньою гіпотезою — прийом буде мовчки пропущено")
	}
	if (hypothetical{debts: []domain.Debt{{ID: -1}}}).empty() {
		t.Error("гіпотетичний борг вважається порожнім")
	}
	if (hypothetical{debtOps: []domain.DebtOp{{ID: -1}}}).empty() {
		t.Error("гіпотетичний рух боргу вважається порожнім")
	}
}

// Питання нічого не записує.
func TestSpendWritesNothing(t *testing.T) {
	_, st, srv, cardID := spendServer(t)
	ctx := context.Background()
	count := func() (int, int, int) {
		d, err := st.ListDeposits(ctx)
		if err != nil {
			t.Fatal(err)
		}
		dd, err := st.ListDebts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ops, err := st.ListDebtOps(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return len(d), len(dd), len(ops)
	}
	a1, b1, c1 := count()
	postSpend(t, srv.URL, `{"amount":"30000","pay":"cash","broker":"mono"}`)
	postSpend(t, srv.URL, fmt.Sprintf(`{"amount":"30000","pay":"card","card_id":%d}`, cardID))
	postSpend(t, srv.URL, `{"amount":"30000","pay":"installment","installment":
		{"name":"3D","kind":"installment","payments_total":"10","fee_month_pct":"1.99"}}`)
	a2, b2, c2 := count()
	if a1 != a2 || b1 != b2 || c1 != c2 {
		t.Errorf("превʼю щось записало: рухи %d→%d, борги %d→%d, операції %d→%d",
			a1, a2, b1, b2, c1, c2)
	}
}

// Готівкова витрата зменшує гроші брокера й капітал рівно на суму — і НЕ
// чіпає резерву: «матрац» навмисно поза купівельною спроможністю.
func TestSpendFromCashMovesBrokerNotCushion(t *testing.T) {
	_, _, srv, _ := spendServer(t)
	var before struct {
		CapitalUAH float64                       `json:"capital_uah"`
		ReserveUAH float64                       `json:"reserve_uah"`
		Brokers    map[string]map[string]float64 `json:"brokers"`
	}
	_, raw := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(raw), &before); err != nil {
		t.Fatal(err)
	}
	_, got, _ := postSpend(t, srv.URL, `{"amount":"30000","pay":"cash","broker":"mono"}`)
	var after struct {
		CapitalUAH float64                       `json:"capital_uah"`
		ReserveUAH float64                       `json:"reserve_uah"`
		Brokers    map[string]map[string]float64 `json:"brokers"`
	}
	if err := json.Unmarshal(got.After, &after); err != nil {
		t.Fatal(err)
	}
	if d := before.CapitalUAH - after.CapitalUAH; d < 29999 || d > 30001 {
		t.Errorf("капітал зрушив на %v, а витрата 30000", d)
	}
	if before.ReserveUAH != after.ReserveUAH {
		t.Errorf("витрата залізла в резерв: %v → %v", before.ReserveUAH, after.ReserveUAH)
	}
	if a, b := before.Brokers["mono"]["UAH"], after.Brokers["mono"]["UAH"]; a-b < 29999 || a-b > 30001 {
		t.Errorf("рахунок mono зрушив на %v", a-b)
	}
}

// ДРІТ МІЖ ДВОМА КОНТУРАМИ — сенс усієї фічі: покупка на картку рухає
// дату виходу з ліміту й НЕ чіпає рахунків.
func TestSpendOnCardMovesExitDate(t *testing.T) {
	_, _, srv, cardID := spendServer(t)
	// TotalUAH тут навмисно НЕ перевіряється: у документі це борг ПІД
	// СТАВКОЮ, а пільговий оборот картки в нього не входить (шапка
	// DebtPlan). Покупка в грейсі його й не мусить зрушити — вона рухає
	// те, скільки принести до розрахункової дати, і чого це коштує виходу
	// з ліміту.
	type exitDoc struct {
		Debt struct {
			Cards []struct {
				Name          string  `json:"name"`
				DebtUAH       float64 `json:"debt_uah"`
				BringByDueUAH float64 `json:"bring_by_due_uah"`
				FreeUAH       float64 `json:"free_uah"`
			} `json:"cards"`
			Exit *struct {
				NeedPerMonthUAH float64 `json:"need_per_month_uah"`
				SpendCapUAH     float64 `json:"spend_cap_uah"`
			} `json:"exit"`
		} `json:"debt"`
		Brokers map[string]map[string]float64 `json:"brokers"`
	}
	var before exitDoc
	_, raw := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(raw), &before); err != nil {
		t.Fatal(err)
	}
	if before.Debt.Exit == nil {
		t.Fatalf("режим виходу з ліміту не побудувався — тест міряв би не те: %s", raw)
	}

	code, got, body := postSpend(t, srv.URL,
		fmt.Sprintf(`{"amount":"30000","pay":"card","card_id":%d}`, cardID))
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var after exitDoc
	if err := json.Unmarshal(got.After, &after); err != nil {
		t.Fatal(err)
	}
	if after.Debt.Exit == nil {
		t.Fatal("після покупки режим виходу зник")
	}
	// FreeUAH тут теж не перевіряється: це ВЛАСНІ гроші на картці, а не
	// залишок ліміту (domain/debt.go), і при боргу він нуль з обох боків.
	card := func(d exitDoc) float64 {
		for _, c := range d.Debt.Cards {
			if c.Name == "ПУМБ" {
				return c.DebtUAH
			}
		}
		t.Fatal("картки ПУМБ немає в документі")
		return 0
	}
	if debtA, debtB := card(before), card(after); debtB <= debtA {
		t.Errorf("борг картки не зріс: %v → %v", debtA, debtB)
	}
	if after.Debt.Exit.NeedPerMonthUAH <= before.Debt.Exit.NeedPerMonthUAH {
		t.Errorf("вихід із ліміту не подорожчав: %v → %v",
			before.Debt.Exit.NeedPerMonthUAH, after.Debt.Exit.NeedPerMonthUAH)
	}
	if before.Brokers["mono"]["UAH"] != after.Brokers["mono"]["UAH"] {
		t.Error("покупка на картку зачепила рахунок — гроші з нього не йшли")
	}
}

// Майбутня витрата не рухає сьогоднішніх грошей, але рухає місячну ціль:
// вона живе в плані, а не в гаманці.
func TestSpendFutureCashDoesNotMoveToday(t *testing.T) {
	_, _, srv, _ := spendServer(t)
	var before struct {
		CapitalUAH     float64 `json:"capital_uah"`
		MonthTargetUAH float64 `json:"month_target_uah"`
	}
	_, raw := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(raw), &before); err != nil {
		t.Fatal(err)
	}
	when := time.Now().AddDate(0, 3, 0).Format("2006-01-02")
	code, got, body := postSpend(t, srv.URL,
		fmt.Sprintf(`{"amount":"30000","pay":"cash","broker":"mono","date":%q}`, when))
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var after struct {
		CapitalUAH float64 `json:"capital_uah"`
	}
	if err := json.Unmarshal(got.After, &after); err != nil {
		t.Fatal(err)
	}
	if before.CapitalUAH != after.CapitalUAH {
		t.Errorf("майбутня витрата зрушила сьогоднішній капітал: %v → %v",
			before.CapitalUAH, after.CapitalUAH)
	}
}

// Минула дата — це операція, а не питання.
func TestSpendRejectsPastDate(t *testing.T) {
	_, _, srv, _ := spendServer(t)
	when := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	code, _, body := postSpend(t, srv.URL,
		fmt.Sprintf(`{"amount":"30000","pay":"cash","date":%q}`, when))
	if code != http.StatusBadRequest {
		t.Errorf("минула дата дала %d: %s", code, body)
	}
}

// Дата не пізніша за останню звірку була б МОВЧКИ ПРОКОВТНУТА картковим
// контуром (CardState). Мовчазна неправильність гірша за 400.
func TestSpendRejectsBeforeCardMark(t *testing.T) {
	_, st, srv, cardID := spendServer(t)
	// Звірка сьогоднішнім днем: тепер покупка сьогодні в неї «вже входить».
	if _, err := st.AddDebtMark(context.Background(), domain.DebtMark{
		DebtID: cardID, Date: domain.NewDate(time.Now()), Balance: -60_000_00,
	}); err != nil {
		t.Fatal(err)
	}
	code, _, body := postSpend(t, srv.URL,
		fmt.Sprintf(`{"amount":"30000","pay":"card","card_id":%d}`, cardID))
	if code != http.StatusBadRequest {
		t.Errorf("покупка, яку звірка проковтне, дала %d: %s", code, body)
	}
}

// Майбутня покупка на картку структурно невидима картковому контуру.
func TestSpendRejectsFutureCardCharge(t *testing.T) {
	_, _, srv, cardID := spendServer(t)
	when := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	code, _, body := postSpend(t, srv.URL,
		fmt.Sprintf(`{"amount":"30000","pay":"card","card_id":%d,"date":%q}`, cardID, when))
	if code != http.StatusBadRequest {
		t.Errorf("майбутня покупка на картку дала %d: %s", code, body)
	}
}

// Ставка розстрочки дорівнює тій, що рахує domain. Другого APR немає.
func TestSpendInstallmentAPRMatchesDomain(t *testing.T) {
	_, _, srv, _ := spendServer(t)
	code, got, body := postSpend(t, srv.URL, `{"amount":"30000","pay":"installment","note":"3D-принтер",
		"installment":{"name":"3D-принтер","kind":"installment","payments_total":"10","fee_month_pct":"1.99"}}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if got.Cost.Credit == nil {
		t.Fatalf("ціни кредиту немає: %s", body)
	}
	want := domain.Debt{
		Name: "3D-принтер", Kind: domain.DebtInstallment, Currency: money.UAH,
		Principal: 30_000_00, PaymentsTotal: 10, FeeMonthBp: 199,
		FirstPaymentDate: domain.NewDate(time.Now()).AddMonths(1),
		OpenedDate:       domain.NewDate(time.Now()),
	}
	apr, basis := domain.DebtEffectiveRate(want, 0)
	if got.Cost.Credit.Basis != basis {
		t.Errorf("основа %q, а domain каже %q", got.Cost.Credit.Basis, basis)
	}
	if d := got.Cost.Credit.APRPct - round2(apr); d > 0.01 || d < -0.01 {
		t.Errorf("APR %v, а domain дає %v", got.Cost.Credit.APRPct, round2(apr))
	}
	if got.Cost.Credit.ExtraUAH <= 0 {
		t.Error("комісія за десять місяців під 1.99% не може бути нульовою")
	}
}

// Альтернатива — ПЕРШИЙ доступний рядок того самого рейтингу, що й у
// «Що купити». Другого впорядкування не заводиться.
func TestSpendOpportunityIsTopAffordableRow(t *testing.T) {
	_, _, srv, _ := spendServer(t)
	_, raw := do(t, "GET", srv.URL+"/api/reinvest", "")
	var sugg []struct {
		Label   string  `json:"label"`
		CanBuy  bool    `json:"can_buy"`
		RealPct float64 `json:"real_pct"`
	}
	if err := json.Unmarshal([]byte(raw), &sugg); err != nil {
		t.Fatal(err)
	}
	var top string
	var topReal float64
	for _, g := range sugg {
		if g.CanBuy {
			top, topReal = g.Label, g.RealPct
			break
		}
	}

	_, got, body := postSpend(t, srv.URL, `{"amount":"30000","pay":"cash","broker":"mono"}`)
	if top == "" {
		if got.Cost.Alternative != nil || got.Cost.AlternativeWhy == "" {
			t.Errorf("доступного рядка немає — мала бути названа причина: %s", body)
		}
		return
	}
	if got.Cost.Alternative == nil {
		t.Fatalf("доступний рядок є (%s), а альтернативи немає: %s", top, body)
	}
	if got.Cost.Alternative.Label != top {
		t.Errorf("альтернатива %q, а верхній доступний рядок %q", got.Cost.Alternative.Label, top)
	}
	if want := round2(30000 * topReal / 100); got.Cost.Alternative.YearUAH != want {
		t.Errorf("за рік %v, а 30000 під %v%% дає %v", got.Cost.Alternative.YearUAH, topReal, want)
	}
}

// АЛЬТЕРНАТИВА НЕ МАЄ ЗАМИКАТИСЬ НА САМІЙ ВИТРАТІ.
//
// Спіймано на живому запуску: доки альтернатива міряли над станом ПІСЛЯ
// витрати, помічник бачив у ньому щойно взяту розстрочку під 50% і чесно
// ставив найкращим рядком «погасити цей борг». Питання «від чого ці гроші
// відмовляються» діставало відповідь «від того, щоб скасувати самих
// себе». Те саме сталось би з карткою.
func TestSpendAlternativeIgnoresTheSpendItself(t *testing.T) {
	_, _, srv, cardID := spendServer(t)

	_, got, body := postSpend(t, srv.URL, `{"amount":"30000","pay":"installment","note":"3D-принтер",
		"installment":{"name":"3D-принтер","kind":"installment","payments_total":"10","fee_month_pct":"1.99"}}`)
	if got.Cost.Alternative != nil && got.Cost.Alternative.Kind == "debt" &&
		got.Cost.Alternative.Label == "3D-принтер" {
		t.Errorf("альтернативою став сам щойно взятий борг: %s", body)
	}

	_, onCard, cardBody := postSpend(t, srv.URL,
		fmt.Sprintf(`{"amount":"30000","pay":"card","card_id":%d}`, cardID))
	if onCard.Cost.Alternative != nil && onCard.Cost.Alternative.Kind == "debt" {
		// Погасити ПУМБ може бути законною порадою й без цієї покупки —
		// перевіряємо саме те, що вона не ПІДНЯЛАСЬ через неї.
		_, plain, _ := postSpend(t, srv.URL, `{"amount":"30000","pay":"cash","broker":"mono"}`)
		if plain.Cost.Alternative == nil || plain.Cost.Alternative.Label != onCard.Cost.Alternative.Label {
			t.Errorf("покупка на картку змінила альтернативу: %s", cardBody)
		}
	}
}

// Паритет із записом: превʼю не приймає того, що відхилить POST /api/debts.
func TestSpendRejectsWhatDebtWriteRejects(t *testing.T) {
	_, _, srv, _ := spendServer(t)
	// Від'ємна комісія — рівно те, що відхиляє debtFromReq.
	code, _, body := postSpend(t, srv.URL, `{"amount":"30000","pay":"installment",
		"installment":{"name":"x","kind":"installment","payments_total":"10","fee_month_pct":"-1"}}`)
	if code != http.StatusBadRequest {
		t.Errorf("превʼю прийняло те, що запис відхиляє: %d %s", code, body)
	}
}

// Робить відмову README «Автоматичних місячних витрат» механічною:
// одна названа покупка не має ставати вартістю місяця життя.
func TestSpendDoesNotChangeMonthlyExpenses(t *testing.T) {
	_, st, srv, _ := spendServer(t)
	ctx := context.Background()
	for k, v := range map[string]string{
		"monthly_expenses":      "25000",
		"reserve_target_months": "6",
	} {
		if err := st.SetSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	var before struct {
		Reserve struct {
			MonthlyExpensesUAH float64 `json:"monthly_expenses_uah"`
			TargetUAH          float64 `json:"target_uah"`
		} `json:"reserve"`
	}
	_, raw := do(t, "GET", srv.URL+"/api/summary", "")
	if err := json.Unmarshal([]byte(raw), &before); err != nil {
		t.Fatal(err)
	}
	_, got, _ := postSpend(t, srv.URL, `{"amount":"30000","pay":"cash","broker":"mono"}`)
	var after struct {
		Reserve struct {
			MonthlyExpensesUAH float64 `json:"monthly_expenses_uah"`
			TargetUAH          float64 `json:"target_uah"`
		} `json:"reserve"`
	}
	if err := json.Unmarshal(got.After, &after); err != nil {
		t.Fatal(err)
	}
	if before.Reserve.MonthlyExpensesUAH != after.Reserve.MonthlyExpensesUAH ||
		before.Reserve.TargetUAH != after.Reserve.TargetUAH {
		t.Errorf("одна покупка переписала вартість місяця життя: %v/%v → %v/%v",
			before.Reserve.MonthlyExpensesUAH, before.Reserve.TargetUAH,
			after.Reserve.MonthlyExpensesUAH, after.Reserve.TargetUAH)
	}
}
