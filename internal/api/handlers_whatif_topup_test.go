// Чим добрати: решта грошей місяця по видах — і головне, ЧОГО в ній не
// пропонують.
//
// Тести тут стережуть рівно одне віднімання (addTopup у
// handlers_whatif.go) і одну властивість складу (рядок готівки в
// state_rebalance.go). Обидва відповідають на те саме питання власника:
// «план купівель не має просто змінювати свою частку, він має
// відштовхуватись від загальної картини».

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// topupView — рівно ті поля відповіді, на які дивляться тести нижче.
// Повний allocPlan тут не потрібен: його арифметику вже стережуть тести
// розкладки, а сюди він приїжджає тією самою функцією.
type topupView struct {
	PlanUAH float64 `json:"topup_plan_uah"`
	LeftUAH float64 `json:"topup_left_uah"`
	Topup   *struct {
		AmountUAH float64 `json:"amount_uah"`
		Lines     []struct {
			Kind     string  `json:"kind"`
			Ref      string  `json:"ref"`
			TotalUAH float64 `json:"total_uah"`
		} `json:"lines"`
	} `json:"topup"`
}

func topupOf(t *testing.T, body string) topupView {
	t.Helper()
	var got topupView
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// seedMonthPlan — джерело доходу, без якого month_plan відсутній, а
// добирати нема з чого за побудовою.
//
// СУМА ТУТ ЗАВЕЛИКА НАВМИСНО. planServer кладе мільйон гривень
// поповненням СЬОГОДНІШНІМ днем, а left_uah — це «план місяця мінус уже
// внесене» (state_month.go), тож зі звичайною зарплатою залишок чесно
// дорівнює нулю, і тести міряли б порожнечу. Абсолютні числа тут ролі не
// грають: усе нижче міряє ВІДНОШЕННЯ — на скільки решта зменшилась і чи
// зменшилась узагалі.
func seedMonthPlan(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.AddPlanFlow(context.Background(), store.PlanFlow{
		Name: "зарплата", Kind: "income", Amount: 2_000_000_00, Currency: money.UAH,
		Cadence: "month", FromDate: domain.NewDate(time.Now().AddDate(-1, 0, 0)),
		InvestBP: 10000,
	}); err != nil {
		t.Fatal(err)
	}
}

// СУМА ЧАСТОК ЗА ВИДОМ ДАЄ РІВНО СОТНЮ — і це стало перевірною
// властивістю лише з появою рядка готівки.
//
// Шапка state_rebalance.go обіцяла її й доти, але обіцяла про ЦІЛІ:
// «сума цілей за видом мусить давати 100». Про ФАКТ сказати було нічого —
// невкладені гроші сиділи в знаменнику без власного рядка, і всі види
// разом стояли нижче сотні рівно на них. Саме ця діра й читалась у картці
// наслідків як «покупка рухає лише власну частку».
func TestKindSharesSumToHundredWithCash(t *testing.T) {
	url, _ := planServer(t)
	_, summary := do(t, "GET", url+"/api/summary", "")
	var doc struct {
		Rebalance []struct {
			Dimension  string  `json:"dimension"`
			Key        string  `json:"key"`
			CurrentPct float64 `json:"current_pct"`
		} `json:"rebalance"`
	}
	if err := json.Unmarshal([]byte(summary), &doc); err != nil {
		t.Fatal(err)
	}
	sum, cash := 0.0, false
	for _, r := range doc.Rebalance {
		if r.Dimension != "kind" {
			continue
		}
		sum += r.CurrentPct
		if r.Key == "cash" {
			cash = true
		}
	}
	if !cash {
		t.Fatal("рядка готівки немає — сума часток за видом не має чим зійтись")
	}
	// Півдесятої відсотка: кожен рядок округлений до двох знаків, і п'ять
	// округлень не зобов'язані скластись у рівно 100.
	if sum < 99.5 || sum > 100.5 {
		t.Errorf("частки за видом дають %.2f%% замість 100", sum)
	}
}

// ГОЛОВНИЙ ТЕСТ ЦІЄЇ ПАЧКИ: добір не пропонує того, що вже заплановане.
//
// Саме тут ламалась стара кнопка «Розкласти» на «Що купити»: вона ходила
// в POST /api/allocate, а той будує стан БЕЗ plan_buys — тобто радив
// докупити рівно те, що в плані стоїть. Ціна помилки — подвійний рахунок
// на кожен рядок плану.
func TestTopupSubtractsPlannedBuys(t *testing.T) {
	url, st := planServer(t)
	seedMonthPlan(t, st)

	_, empty := whatIf(t, url, `{}`)
	base := topupOf(t, empty)
	if base.Topup == nil {
		t.Fatal("порожній план: добирати мало бути з чого — перевір month_plan")
	}
	if base.PlanUAH != base.LeftUAH {
		t.Errorf("без рядків плану решта мусить дорівнювати обіцяному: %.2f проти %.2f",
			base.LeftUAH, base.PlanUAH)
	}

	// Рядок «зараз»: майбутній цих грошей не витрачає й відніматись не
	// має (та сама межа, що ділить портфельні числа картки наслідків).
	if _, err := st.AddPlanBuy(context.Background(), store.PlanBuy{
		Kind: store.BuyBond, Ref: "UA4000227748", Qty: 3, Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	_, withPlan := whatIf(t, url, `{}`)
	got := topupOf(t, withPlan)
	if got.Topup == nil {
		t.Fatal("три папери не могли забрати весь місяць")
	}
	if got.PlanUAH != base.PlanUAH {
		t.Errorf("план купівель не має рухати саме обіцяне місяцем: %.2f → %.2f",
			base.PlanUAH, got.PlanUAH)
	}
	spent := base.LeftUAH - got.LeftUAH
	if spent <= 0 {
		t.Fatalf("решта не зменшилась: %.2f → %.2f", base.LeftUAH, got.LeftUAH)
	}
	// Сума розкладки — це і є решта: інакше картка показала б одне число,
	// а розклала б інше.
	if d := got.Topup.AmountUAH - got.LeftUAH; d > 0.01 || d < -0.01 {
		t.Errorf("розкладають %.2f, а решта %.2f", got.Topup.AmountUAH, got.LeftUAH)
	}
}

// Майбутній рядок цих грошей не витрачає — він у наступному місяці.
func TestTopupIgnoresFutureRows(t *testing.T) {
	url, st := planServer(t)
	seedMonthPlan(t, st)
	_, empty := whatIf(t, url, `{}`)
	base := topupOf(t, empty)

	if _, err := st.AddPlanBuy(context.Background(), store.PlanBuy{
		Kind: store.BuyBond, Ref: "UA4000227748", Qty: 3, Broker: "mono",
		BuyDate: domain.Date(time.Now().AddDate(0, 2, 0).Format("2006-01-02")),
	}); err != nil {
		t.Fatal(err)
	}
	_, withFuture := whatIf(t, url, `{}`)
	got := topupOf(t, withFuture)
	if d := got.LeftUAH - base.LeftUAH; d > 0.01 || d < -0.01 {
		t.Errorf("рядок на позаминулий місяць з'їв гроші цього: %.2f → %.2f",
			base.LeftUAH, got.LeftUAH)
	}
}

// План більший за те, що обіцяє місяць, — добирати нема з чого, і картки
// не буде зовсім. Нуля тут не буває навмисно: порожня розкладка нуля
// читалась би як поломка.
func TestTopupAbsentWhenPlanEatsTheMonth(t *testing.T) {
	url, st := planServer(t)
	seedMonthPlan(t, st)
	_, empty := whatIf(t, url, `{}`)
	base := topupOf(t, empty)
	if base.Topup == nil {
		t.Fatal("порожній план: добирати мало бути з чого")
	}
	// Вкладом, а не паперами: у нього сума задається прямо, тож «трохи
	// більше за решту» виражається числом, а не діленням на ціну квитка,
	// яка залежить від НКД і може змінитись разом із довідником.
	if _, err := st.AddPlanBuy(context.Background(), store.PlanBuy{
		Kind: store.BuyDeposit, Ref: "mono", Currency: money.UAH,
		Amount: int64(base.LeftUAH*100) + 1_000_00, RateBP: 1400, Months: 12,
	}); err != nil {
		t.Fatal(err)
	}
	code, body := whatIf(t, url, `{}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	got := topupOf(t, body)
	if got.Topup != nil {
		t.Errorf("план з'їв місяць, а картка все одно радить на %.2f", got.Topup.AmountUAH)
	}
	if got.LeftUAH != 0 {
		t.Errorf("решта мусить бути нулем, а не від'ємною: %.2f", got.LeftUAH)
	}
}

// Плану доходу немає — добирати нема з чого, і мовчання тут єдина чесна
// відповідь. Той самий стан, що його розрізняє monthHeadHTML: «плану
// немає» і «план закритий» — різні речі, і жодна з них не нуль.
func TestTopupAbsentWithoutIncomePlan(t *testing.T) {
	url, _ := planServer(t)
	code, body := whatIf(t, url, `{}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if got := topupOf(t, body); got.Topup != nil {
		t.Errorf("без плану доходу картка радить на %.2f", got.Topup.AmountUAH)
	}
}
