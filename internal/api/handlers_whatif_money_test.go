// Гіпотеза приносить гроші, якими план оплачений.
//
// Спіймано власником на живому екрані: «6к у кошику, а капітал іде в
// мінус». Гіпотеза додавала саму витрату — лот, операцію фонду, внесок, —
// і касовий журнал чесно списував їхню вартість; поповнення, з якого це
// платиться, у неї не клали. Портфель «стане» виходив таким, у якому гроші
// зникли з рахунку, не давши нічого, крім паперу.
//
// Правило застосунку при цьому не змінилось: план купівель міряється
// ПЛАНОВИМИ грошима, а не сьогоднішнім залишком (шапка basketDoc).
// Бракувало другої половини тієї самої обіцянки.

package api

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// cashView — рахунки й капітал, більше нічого.
type cashView struct {
	CapitalUAH float64                       `json:"capital_uah"`
	AccountUAH float64                       `json:"account_uah"`
	NominalUAH float64                       `json:"nominal_uah_eq"`
	Brokers    map[string]map[string]float64 `json:"brokers"`
	Rebalance  []struct {
		Dimension  string  `json:"dimension"`
		Key        string  `json:"key"`
		CurrentPct float64 `json:"current_pct"`
		CurrentUAH float64 `json:"current_uah"`
	} `json:"rebalance"`
}

func cashOf(t *testing.T, body string) cashView {
	t.Helper()
	var got cashView
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// ГОЛОВНЕ ТВЕРДЖЕННЯ ФАЗИ: готівка після плану дорівнює готівці до нього.
//
// Не «майже» і не «в межах округлення»: поповнення дзеркалить списання тим
// самим числом у тій самій валюті на тому самому рахунку, тож різниця має
// бути рівно нулем.
func TestWhatIfPlanBringsItsOwnMoney(t *testing.T) {
	url, st := planServer(t)
	if _, err := st.AddPlanBuy(t.Context(), store.PlanBuy{
		Kind: store.BuyBond, Ref: "UA4000227748", Qty: 3, Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	_, summary := do(t, "GET", url+"/api/summary", "")
	before := cashOf(t, summary)

	code, body := whatIf(t, url, `{}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	after := cashOf(t, afterOf(t, body))

	if d := after.AccountUAH - before.AccountUAH; d != 0 {
		t.Errorf("готівка змінилась на %.2f, хочемо 0: гіпотеза мала принести те, що витратила", d)
	}
	if d := after.Brokers["mono"]["UAH"] - before.Brokers["mono"]["UAH"]; d != 0 {
		t.Errorf("гривня в mono змінилась на %.2f, хочемо 0", d)
	}
}

// КАПІТАЛ РОСТЕ, А НЕ ПАДАЄ — і росте менше за суму плану рівно на
// сплачений НКД.
//
// Це те саме число, яке власник побачив на екрані: план на 6 445 ₴, а
// капітал 77 314 → 76 913. Різниця там розкладалась без залишку на НКД
// пʼяти паперів і переоцінку сертифіката.
func TestWhatIfCapitalGrowsByNominalNotByPrice(t *testing.T) {
	url, st := planServer(t)
	if _, err := st.AddPlanBuy(t.Context(), store.PlanBuy{
		Kind: store.BuyBond, Ref: "UA4000227748", Qty: 3, Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	_, summary := do(t, "GET", url+"/api/summary", "")
	before := cashOf(t, summary)

	code, body := whatIf(t, url, `{}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var got struct {
		After  json.RawMessage `json:"after"`
		Basket basketDoc       `json:"basket"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	after := cashOf(t, string(got.After))

	grew := after.CapitalUAH - before.CapitalUAH
	if grew <= 0 {
		t.Fatalf("капітал зрушив на %.2f — він мусить РОСТИ: гроші плану тепер у гіпотезі", grew)
	}
	// Приріст — це номінал куплених паперів.
	if d := after.NominalUAH - before.NominalUAH; math.Abs(grew-d) > 0.01 {
		t.Errorf("капітал зріс на %.2f, номінал — на %.2f: мали збігтись", grew, d)
	}
	// А різниця з ціною покупки — сплачений НКД, і вона ДОДАТНА: без неї
	// фікстура нічого не стереже.
	spent, err := domain.ParseDecimalToMinor(got.Basket.Totals[0].Amount, money.UAH)
	if err != nil {
		t.Fatal(err)
	}
	accrued := float64(spent)/100 - grew
	if accrued <= 0 {
		t.Errorf("заплачено %.2f, капітал зріс на %.2f — НКД не видно, фікстура порожня",
			float64(spent)/100, grew)
	}
}

// МАЙБУТНІЙ РЯДОК ГРОШЕЙ НЕ ПРИНОСИТЬ.
//
// Він і не витрачає їх сьогодні: папір стає замком, фонд — накопиченням,
// внесок — потоком. Дати йому поповнення означало б підняти СЬОГОДНІШНІЙ
// капітал за покупку, якої ще немає.
func TestWhatIfFutureRowBringsNoMoney(t *testing.T) {
	url, st := planServer(t)
	_, summary := do(t, "GET", url+"/api/summary", "")
	before := cashOf(t, summary)

	if _, err := st.AddPlanBuy(t.Context(), store.PlanBuy{
		Kind: store.BuyBond, Ref: "UA4000227748", Qty: 3, Broker: "mono",
		BuyDate: domain.NewDate(time.Now().AddDate(0, 2, 0)),
	}); err != nil {
		t.Fatal(err)
	}
	code, body := whatIf(t, url, `{}`)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	after := cashOf(t, afterOf(t, body))

	if d := after.AccountUAH - before.AccountUAH; d != 0 {
		t.Errorf("готівка змінилась на %.2f: майбутній рядок не має приносити грошей", d)
	}
	if d := after.CapitalUAH - before.CapitalUAH; d != 0 {
		t.Errorf("капітал змінився на %.2f: майбутній рядок сьогоднішнього портфеля не рухає", d)
	}
}

// РЯДОК ГОТІВКИ Є Й ПРИ ВІДʼЄМНОМУ ЗАЛИШКУ, і сума часток за видом
// дорівнює сотні.
//
// Відʼємна готівка — законний стан: касовий журнал не має підлоги, тож
// зняття понад залишок дає її одразу. Доти рядок при цьому ЗНИКАВ, а
// знаменник відʼємну готівку враховував і далі — видимі види разом давали
// понад сотню, і пояснити це було нічим.
func TestKindSharesSumToHundredOnNegativeCash(t *testing.T) {
	url, st := planServer(t)
	// Знімаємо з рахунку більше, ніж на ньому лежить, — але не стільки,
	// щоб у мінус пішов і сам ЗНАМЕННИК: при kindMajor ≤ 0 рядків за
	// видом немає взагалі, і тест перевіряв би порожнечу. Фікстура
	// planServer кладе мільйон і витрачає 4 975 на папери, тож 996 000
	// лишають рахунок близько −975 при номіналі 5 000.
	if _, err := st.AddDeposit(t.Context(), store.Deposit{
		Date: domain.NewDate(time.Now()), Amount: -996_000_00,
		Currency: money.UAH, Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}
	_, summary := do(t, "GET", url+"/api/summary", "")
	doc := cashOf(t, summary)

	if doc.AccountUAH >= 0 {
		t.Fatalf("рахунок %.2f — фікстура мала загнати його в мінус", doc.AccountUAH)
	}
	sum, cash := 0.0, false
	for _, r := range doc.Rebalance {
		if r.Dimension != "kind" {
			continue
		}
		sum += r.CurrentPct
		if r.Key == "cash" {
			cash = true
			if r.CurrentUAH >= 0 {
				t.Errorf("рядок готівки показує %.2f при відʼємному рахунку", r.CurrentUAH)
			}
		}
	}
	if !cash {
		t.Fatal("рядка готівки немає — саме тоді, коли її найважче помітити")
	}
	if sum < 99.5 || sum > 100.5 {
		t.Errorf("частки за видом дають %.2f%% замість 100", sum)
	}
}
