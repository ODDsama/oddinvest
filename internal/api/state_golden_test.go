// Характеризаційний тест buildState — сітка під розбиття.
//
// buildState — це 1800 рядків в одній функції зі сотнею локальних
// змінних. Розносити її по фазах без такої сітки означає міняти проводку,
// вимкнувши світло: жоден наявний тест не перевіряє документ ЦІЛКОМ, вони
// дивляться по кілька полів кожен.
//
// Тут два різні сторожі, і потрібні обидва:
//
//   - golden звіряє документ БАЙТ У БАЙТ. Ловить «фаза почала рахувати
//     інакше» — будь-яку зміну числа, навіть в останньому знаку.
//   - TestDocFieldsPopulated вимагає, щоб КОЖНЕ поле було ненульовим.
//     Ловить «фаза тихо перестала заповнювати»: golden цього не побачить,
//     якщо фікстура й так давала там нуль.
//
// ПРАВИЛО НА ВСЮ СЕРІЮ РОЗБИТТЯ. Коміт-«перенесення» мусить лишати golden
// БЕЗ ЗМІН. Якщо diff зʼявився — це вже не перенесення, і його треба або
// відкотити, або назвати виправленням і пояснити кожне змінене поле.
// Перегенерувати можна так, але лише свідомо:
//
//	go test ./internal/api -run TestBuildStateGolden -update

package api

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

var updateGolden = flag.Bool("update", false, "перегенерувати golden документа стану")

// goldenNow — фіксований момент. Не time.Now(): golden мусить бути тим
// самим і завтра, інакше він перетвориться на генератор шуму, і його
// перестануть читати.
var goldenNow = time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)

func d(offset int) domain.Date { return domain.NewDate(goldenNow.AddDate(0, 0, offset)) }

// richPortfolio — портфель, де є ВСЕ, що вміє застосунок.
//
// Кожен елемент тут стоїть заради конкретної гілки коду; прибереш —
// і відповідна фаза перестане перевірятись, лишившись у golden нулями.
func richPortfolio(t *testing.T, srv string, st *store.Store) {
	t.Helper()
	ctx := context.Background()

	// Довідник: гривневий папір, доларовий, євровий і ПОГАШЕНИЙ.
	uahBond := domain.Bond{ISIN: "UA4000227748", Nominal: money.New(100000, money.UAH),
		RateBP: 1655, Maturity: d(600), Descr: "гривневі військові"}
	usdBond := domain.Bond{ISIN: "UA4000230114", Nominal: money.New(100000, money.USD),
		RateBP: 425, Maturity: d(800), Descr: "валютні"}
	eurBond := domain.Bond{ISIN: "UA4000239065", Nominal: money.New(100000, money.EUR),
		RateBP: 300, Maturity: d(900), Descr: "єврові"}
	oldBond := domain.Bond{ISIN: "UA4000000001", Nominal: money.New(100000, money.UAH),
		RateBP: 1600, Maturity: d(-30), Descr: "уже погашений"}
	secs := []nbu.Security{
		{Bond: uahBond, Payments: []domain.Payment{
			{ISIN: uahBond.ISIN, PayDate: d(-40), Type: domain.PayCoupon, PerBond: money.New(8275, money.UAH)},
			{ISIN: uahBond.ISIN, PayDate: d(150), Type: domain.PayCoupon, PerBond: money.New(8275, money.UAH)},
			{ISIN: uahBond.ISIN, PayDate: d(600), Type: domain.PayRedemption, PerBond: money.New(100000, money.UAH)},
		}},
		{Bond: usdBond, Payments: []domain.Payment{
			{ISIN: usdBond.ISIN, PayDate: d(200), Type: domain.PayCoupon, PerBond: money.New(2125, money.USD)},
			{ISIN: usdBond.ISIN, PayDate: d(800), Type: domain.PayRedemption, PerBond: money.New(100000, money.USD)},
		}},
		{Bond: eurBond, Payments: []domain.Payment{
			{ISIN: eurBond.ISIN, PayDate: d(300), Type: domain.PayCoupon, PerBond: money.New(1500, money.EUR)},
			{ISIN: eurBond.ISIN, PayDate: d(900), Type: domain.PayRedemption, PerBond: money.New(100000, money.EUR)},
		}},
		{Bond: oldBond, Payments: []domain.Payment{
			{ISIN: oldBond.ISIN, PayDate: d(-30), Type: domain.PayRedemption, PerBond: money.New(100000, money.UAH)},
		}},
	}
	if err := st.ReplaceDirectory(ctx, secs, goldenNow); err != nil {
		t.Fatal(err)
	}
	// Курси на дві дати: свіжий і дворічної давнини — щоб бенчмарк мав із
	// чим рахувати «а якби я просто тримав долари».
	for _, r := range []struct {
		cur  string
		e4   int64
		date domain.Date
	}{
		{money.USD, 380000, d(-700)}, {money.USD, 441234, d(-1)},
		{money.EUR, 480000, d(-1)},
	} {
		if err := st.SaveRate(ctx, r.cur, r.e4, r.date); err != nil {
			t.Fatal(err)
		}
	}

	// Аукціони: три строки в гривні й один у євро. Один із гривневих —
	// торішній, щоб на фікстурі був і свіжий рівень, і застарілий: саме
	// на цій різниці тримається «несвіжий папір нижче» в помічнику.
	if err := st.SaveAuctions(ctx, []nbu.Auction{
		{Date: d(-3), ISIN: uahBond.ISIN, Num: "91", Currency: money.UAH,
			Bucket: "1.5y", IncomeBP: 1565, DaysToRepay: 600},
		{Date: d(-10), ISIN: "UA4000239107", Num: "92", Currency: money.UAH,
			Bucket: "2y", IncomeBP: 1610, DaysToRepay: 910},
		{Date: d(-400), ISIN: "UA4000000001", Num: "12", Currency: money.UAH,
			Bucket: "3y", IncomeBP: 1780, DaysToRepay: 1100},
		{Date: d(-40), ISIN: eurBond.ISIN, Num: "93", Currency: money.EUR,
			Bucket: "2.5y", IncomeBP: 320, DaysToRepay: 900},
	}); err != nil {
		t.Fatal(err)
	}

	// Гроші на ДВА брокери й у трьох валютах.
	for _, dep := range []store.Deposit{
		{Date: d(-200), Amount: 50000000, Currency: money.UAH, Broker: "mono"},
		{Date: d(-150), Amount: 300000, Currency: money.USD, Broker: "mono"},
		{Date: d(-150), Amount: 200000, Currency: money.EUR, Broker: "inzhur"},
		{Date: d(-100), Amount: 10000000, Currency: money.UAH, Broker: "inzhur"},
		{Date: d(-20), Amount: -2000000, Currency: money.UAH, Broker: "mono"}, // зняття
		// Рухи ПОТОЧНОГО місяця. Без них уся фаза «місяць» (внесено,
		// знято, вкладено, прогрес плану) лишається нулями: решта дат тут
		// із запасом раніша за goldenNow. Внесено більше, ніж знято, бо
		// month_deposited_uah — нетто.
		{Date: d(-6), Amount: 8000000, Currency: money.UAH, Broker: "mono"},
		{Date: d(-3), Amount: -1500000, Currency: money.UAH, Broker: "mono"},
	} {
		if _, err := st.AddDeposit(ctx, dep); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.AddConversion(ctx, store.Conversion{
		Date: d(-90), FromCurrency: money.UAH, FromAmount: 4400000,
		ToCurrency: money.USD, ToAmount: 100000, Broker: "mono",
	}); err != nil {
		t.Fatal(err)
	}

	// Лоти: у двох брокерах, у трьох валютах, плюс погашений.
	for _, body := range []string{
		`{"isin":"UA4000227748","qty":10,"price_per_bond":"1000.00","fee":"25.00","buy_date":"` + string(d(-180)) + `","channel":"mono"}`,
		`{"isin":"UA4000230114","qty":3,"price_per_bond":"1000.00","buy_date":"` + string(d(-120)) + `","channel":"mono"}`,
		`{"isin":"UA4000239065","qty":2,"price_per_bond":"1000.00","buy_date":"` + string(d(-110)) + `","channel":"inzhur"}`,
		`{"isin":"UA4000000001","qty":4,"price_per_bond":"1000.00","buy_date":"` + string(d(-300)) + `","channel":"mono"}`,
		// Покупка ПОТОЧНОГО місяця — інакше month_invested_uah нуль.
		`{"isin":"UA4000227748","qty":5,"price_per_bond":"1002.00","fee":"12.50","buy_date":"` + string(d(-7)) + `","channel":"inzhur"}`,
	} {
		if resp, b := do(t, "POST", srv+"/api/lots", body); resp.StatusCode != 201 {
			t.Fatalf("лот: %d %s", resp.StatusCode, b)
		}
	}
	// ЧАСТКОВИЙ продаж першого лота — щоб RemainingQtyNow не збігався з Qty.
	if resp, b := do(t, "POST", srv+"/api/sales",
		`{"lot_id":1,"qty":3,"sale_date":"`+string(d(-60))+
			`","clean_per_bond":"1010.00","accrued":"12.00","currency":"UAH"}`); resp.StatusCode != 201 {
		t.Fatalf("продаж: %d %s", resp.StatusCode, b)
	}

	// Фонди: один живий розподільний, один накопичувальний зі строком,
	// один із «короткою» дірою в журналі.
	for _, op := range []domain.FundOp{
		{Date: d(-140), Fund: "Inzhur REIT", Kind: domain.FundBuy, Qty: 500, Amount: 500000, Currency: money.UAH, Broker: "inzhur"},
		{Date: d(-40), Fund: "Inzhur REIT", Kind: domain.FundDividend, Amount: 12000, Tax: 1680, Currency: money.UAH, Broker: "inzhur"},
		{Date: d(-20), Fund: "Строковий фонд", Kind: domain.FundBuy, Qty: 5, Amount: 500000, Currency: money.UAH, Broker: "inzhur"},
		{Date: d(-70), Fund: "Short Fund", Kind: domain.FundSell, Qty: 10, Amount: 20000, Currency: money.UAH, Broker: "mono"},
	} {
		if _, err := st.AddFundOp(ctx, op); err != nil {
			t.Fatal(err)
		}
	}
	// Довідник фондів: обіцяна дохідність у ЧУЖІЙ валюті (сертифікат
	// гривневий, обіцянка доларова) і день виплати. Без цього три поля
	// рядка фонду лишаються нулями, і фаза, яка їх заповнює, не
	// перевіряється зовсім.
	funds, err := st.ListFunds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range funds {
		switch f.Name {
		case "Inzhur REIT":
			f.ExpectedYieldBP, f.ExpectedYieldCur, f.PayoutDay = 950, money.USD, 10
		case "Строковий фонд":
			// Накопичувальний зі строком: обіцянка задана ПРОСТОЮ
			// середньорічною за три роки, закриття — рівно через 30
			// місяців від сьогодні, податок на дохід 14%. Без такого
			// фонду у фікстурі накопичувальна гілка не перевіряється
			// зовсім: усі поля строку лишаються нулями, і TestDocFields
			// цього не бачить.
			f.Kind = store.FundAccumulating
			f.ExpectedYieldBP, f.ExpectedYieldCur = 2500, money.UAH
			f.YieldSimpleYears, f.IncomeTaxBP, f.ExitTaxBP = 3, 1400, 2300
			f.CloseDate = string(d(0).AddMonths(30))
			f.BuyUntil = string(d(0).AddMonths(4))
		default:
			continue
		}
		if err := st.RenameFund(ctx, f.ID, f); err != nil {
			t.Fatal(err)
		}
	}

	// Вклади: діючий поповнюваний і закритий достроково.
	id, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: money.UAH, Principal: 10000000, RateBP: 1600,
		OpenDate: d(-90), MaturityDate: d(275), Payout: domain.PayoutMonthly,
		TaxBP: 1950, Replenishable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDepositTopup(ctx, domain.DepositTopup{
		DepositID: id, Date: d(-30), Amount: 2000000,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "mono", Currency: money.USD, Principal: 100000, RateBP: 400,
		OpenDate: d(-200), MaturityDate: d(160), Payout: domain.PayoutEnd,
		TaxBP: 1950, ClosedDate: d(-10), ClosedAmount: 101200,
	}); err != nil {
		t.Fatal(err)
	}

	// Резерв у ДВОХ місцях і двох валютах.
	for _, op := range []store.ReserveOp{
		{Date: d(-50), Amount: 3000000, Currency: money.UAH, Place: "готівка"},
		{Date: d(-45), Amount: 40000, Currency: money.USD, Place: "сейф"},
	} {
		if _, err := st.AddReserveOp(ctx, op); err != nil {
			t.Fatal(err)
		}
	}

	// Позначена як отримана майбутня виплата — гілка arrived поза датою.
	if err := st.SetPaymentStatus(ctx, uahBond.ISIN, d(150), "received"); err != nil {
		t.Fatal(err)
	}

	// УСІ налаштування. uah_devaluation_pct обовʼязково явним: інакше
	// знецінення міряється з історії курсів ВІД time.Now(), і golden
	// поповз би сам собою від зміни дати.
	for k, v := range map[string]string{
		"usd_target_share_pct": "25", "eur_target_share_pct": "10",
		"goal_amount_uah": "4000000", "goal_date": string(d(1200)),
		"uah_devaluation_pct": "7", "terminal_rate_pct": "11", "rate_glide_years": "5",
		"reinvest_rank":   "plan",
		"deposit_min_usd": "100", "deposit_min_eur": "100", "deposit_min_uah": "5000",
		"deposit_rate_usd_pct": "1.7", "deposit_rate_eur_pct": "0.7", "deposit_rate_uah_pct": "16",
		// Резерв. Стеля поповнення задана навмисно: без неї гілка
		// «скільки з вільних грошей відкласти» не рахувалась би зовсім, а
		// нулі fill_* у документі не відрізнити від фази, яка перестала їх
		// заповнювати. Ціль у 6 місяців витрат (150 000 ₴) при меншому
		// резерві лишає розрив, тож рахується саме гілка «стеля обмежує».
		"monthly_expenses_uah": "25000", "reserve_target_months": "6",
		"reserve_fill_share_pct": "20",
		// Припущення прогнозу. Ціль доходу НЕ дорівнює витратам навмисно:
		// інакше спад «порожньо = витрати» був би невідрізнимий від
		// заданого значення, і зламаний спад пройшов би повз тест.
		"income_target_uah": "30000", "withdraw_monthly_uah": "20000",
		"rate_spread_pp": "3", "deval_spread_pp": "4",
		"target_bonds_pct": "45", "target_funds_pct": "20", "target_deposits_pct": "15",
		"target_npf_pct": "10",
		// Знижка: ПДФО за рік і є перемикачем, тож без нього credit_est_uah
		// лишився б нулем, і гілка оцінки не перевірялась би зовсім.
		"npf_credit_pdfo_year_uah": "40000", "npf_credit_cap_month_uah": "4660",
		"limit_isin_pct": "20", "limit_broker_pct": "50", "limit_year_pct": "40",
		"goal_pessimistic_uah": "200000", "goal_realistic_uah": "500000",
		"goal_optimistic_uah": "1000000",
		"import_since":        string(d(-5)),
		// Пишеться фоновою задачею оновлення НБУ, якої в тесті немає.
		// Фіксований момент, а не goldenNow: у документ воно потрапляє
		// рядком як є, і будь-яка похідна від часу дата зробила б golden
		// нестабільним.
		"nbu_refreshed_at": "2026-07-15T06:00:00Z",
	} {
		if err := st.SetSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}

	// НПФ: один рахунок із заповненим УСІМ — обіцянка, дата доступу, податок
	// на виплату, ставка знижки й день внеску. Порожнє поле тут означає
	// неперевірену гілку, і TestDocFieldsPopulated саме за це й валить.
	//
	// contrib_day = 5 при goldenNow 15 липня: внесок за липень навмисно НЕ
	// заводиться, тож npf_contrib_due виходить true. Для bool це єдиний
	// спосіб бути «ненульовим», а нагадування — те, заради чого НПФ узагалі
	// зʼявляється в HA окремою сутністю.
	npfID, err := st.AddNPFAccount(ctx, domain.NPFAccount{
		Name: "Династія", Administrator: "ЦПО", Currency: money.UAH,
		Nav: 3_472_156, NavDate: d(-15),
		ExpectedYieldBP: 1500, YieldSimpleYears: 3,
		AccessDate: d(9000), IncomeTaxBP: 1380,
		CreditRateBP: 1800, ContribDay: 5, Note: "ICU",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Два внески за РІЗНОЮ ЧВОПА: один не дає ні кривої, ні різниці між
	// вартістю й собівартістю, тобто gain_uah лишився б нулем.
	for _, op := range []domain.NPFOp{
		{NPFID: npfID, Date: d(-70), Units: 300_000_000, Amount: 100_000, Broker: "ПУМБ"},
		{NPFID: npfID, Date: d(-40), Units: 288_005_492, Amount: 100_000, Broker: "ПУМБ"},
	} {
		if _, err := st.AddNPFOp(ctx, op); err != nil {
			t.Fatal(err)
		}
	}
	// Історія ЧВОПА понад рік: без двох точок і 180 днів між ними
	// nav_return_pct не рахується, і yield_basis лишався б обіцянкою — тобто
	// гілка витіснення факту не перевірялась би.
	if _, err := st.AddNPFNavPoints(ctx, npfID, []domain.NPFNav{
		{Date: d(-500), Nav: 2_800_000},
		{Date: d(-15), Nav: 3_472_156},
	}); err != nil {
		t.Fatal(err)
	}

	// План (фаза 9): одне джерело доходу й одна дія — інакше
	// plan_provides_uah, ContribByMonth і Lock лишаються неперевіреними
	// нулями на цій фікстурі, а TestDocFieldsPopulated про це мовчить,
	// бо *план* технічно заповнений (лише не впливає на суму).
	if resp, b := do(t, "POST", srv+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"40000.00","cadence":"month","from_date":"`+
			string(d(-30))+`","invest_pct":"40"}`); resp.StatusCode != 201 {
		t.Fatalf("потік плану: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv+"/api/plan/actions",
		`{"date":"`+string(d(200))+`","type":"lock","amount":"50000.00","rate_pct":"20","months":24,"name":"MilTech"}`); resp.StatusCode != 201 {
		t.Fatalf("дія плану: %d %s", resp.StatusCode, b)
	}
}

// buildRichDoc піднімає сервер на багатій фікстурі й будує документ на
// ФІКСОВАНИЙ момент.
func buildRichDoc(t *testing.T) *state.Doc {
	t.Helper()
	srv, st := testServer(t)
	richPortfolio(t, srv.URL, st)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	doc, err := New(st, nil, log).buildState(context.Background(), goldenNow)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestBuildStateGolden(t *testing.T) {
	doc := buildRichDoc(t)
	got, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", "state_rich.json")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("golden перегенеровано — переконайся, що diff саме той, який ти мав на увазі")
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("немає golden (%v). Створи: go test ./internal/api -run TestBuildStateGolden -update", err)
	}
	if string(got) != string(want) {
		t.Errorf("документ стану змінився.\n\nЯкщо це ПЕРЕНЕСЕННЯ коду — його не мало бути.\n"+
			"Якщо ВИПРАВЛЕННЯ — перегенеруй і назви кожне змінене поле в повідомленні коміту:\n"+
			"  go test ./internal/api -run TestBuildStateGolden -update\n\n%s",
			firstDiff(string(want), string(got)))
	}
}

// TestBuildStateIsDeterministic — той самий портфель мусить давати той
// самий документ БАЙТ У БАЙТ, скільки разів його не будуй.
//
// Це не паранойя, а перше, що впіймала ця сітка. Експозиція брокера
// збиралась обходом мапи валют, а додавання float64 не асоціативне: сума
// різнилась у останніх бітах щоразу. Поки жодне число не лежало рівно на
// пів копійки, цього не було видно; over_uah у концентрації ліг — і почав
// показувати то 215026.58, то 215026.59 на незмінних даних.
//
// Без цього сторожа golden не сторож, а генератор випадкових падінь: він
// червонів би раз на пʼять прогонів, і його перше, що зробили б, — вимкнули.
func TestBuildStateIsDeterministic(t *testing.T) {
	// З відступами, а не компактно: інакше firstDiff показує весь документ
	// одним рядком, і від нього немає користі саме тоді, коли він потрібен.
	first, err := json.MarshalIndent(buildRichDoc(t), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// Порядок обходу мап Go рандомізує на КОЖНОМУ проході, тож повтори
	// тут — не марна робота: одного порівняння замало, щоб відрізнити
	// сталу суму від такої, що збіглась випадково.
	for i := 1; i < 20; i++ {
		got, err := json.MarshalIndent(buildRichDoc(t), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(first) {
			t.Fatalf("прохід %d дав інший документ на тих самих даних — десь float-сума "+
				"збирається обходом мапи.\n%s", i+1, firstDiff(string(first), string(got)))
		}
	}
}

// firstDiff — перший рядок розбіжності з контекстом. Повний diff на
// шістсот рядків JSON читати неможливо, а перше розходження майже завжди
// і є причина.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var lw, lg string
		if i < len(w) {
			lw = w[i]
		}
		if i < len(g) {
			lg = g[i]
		}
		if lw != lg {
			from := i - 3
			if from < 0 {
				from = 0
			}
			var b strings.Builder
			b.WriteString("перша розбіжність, рядок " + itoa(i+1) + ":\n")
			for j := from; j < i; j++ {
				b.WriteString("  " + w[j] + "\n")
			}
			b.WriteString("- було:  " + lw + "\n")
			b.WriteString("+ стало: " + lg + "\n")
			return b.String()
		}
	}
	return "(порядкової розбіжності немає — різниця лише в кінцевому символі)"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// allowedZero — поля, які на цій фікстурі законно порожні. Ключ — шлях,
// значення — ЧОМУ нуль тут чесний.
//
// Зараз мапа порожня, і це не недогляд: фікстуру добирали доти, доки
// кожне поле документа не заповнилось по-справжньому. Виняток тут — це
// завжди визнання, що якусь гілку ми не перевіряємо; додавати його можна,
// але з поясненням, і TestDocFieldsPopulated окремо стежить, щоб виняток,
// який більше нічого не прикриває, не залишався лежати.
var allowedZero = map[string]string{}

// TestDocFieldsPopulated — кожне поле документа мусить бути ненульовим.
//
// Це відповідь на «що впіймає фазу, яка тихо перестала щось заповнювати».
// Golden цього не бачить: якщо поле й до того було нулем, його зникнення
// нічого в JSON не змінить (усюди omitempty). А в Home Assistant сутність
// просто піде в нуль, і помітять це через місяць.
func TestDocFieldsPopulated(t *testing.T) {
	doc := buildRichDoc(t)
	empty := walkDoc(reflect.ValueOf(doc), "")

	paths := make([]string, 0, len(empty))
	for p := range empty {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if _, ok := allowedZero[p]; ok {
			continue
		}
		t.Errorf("поле %s — %s. Якщо це законно на цій фікстурі, додай його в "+
			"allowedZero З ПОЯСНЕННЯМ; якщо ні — фаза перестала його заповнювати",
			p, empty[p])
	}

	// Зустрічна перевірка: виняток, який більше нічого не прикриває, — це
	// сміття, і саме так список винятків перестає щось означати. Або поле
	// вже заповнюється (тоді виняток геть), або шлях перейменували (тоді
	// сторож мовчки перестав сторожити те, що мав).
	stale := make([]string, 0, len(allowedZero))
	for p := range allowedZero {
		if _, ok := empty[p]; !ok {
			stale = append(stale, p)
		}
	}
	sort.Strings(stale)
	for _, p := range stale {
		t.Errorf("allowedZero[%q] зайвий: поле заповнене (або шлях перейменували). Прибери його", p)
	}
}

// walkDoc повертає мапу «шлях поля → чому воно вважається порожнім».
// Мапа, а не t.Errorf одразу, бо для зрізів результати рядків треба
// перетнути, перш ніж когось звинувачувати.
func walkDoc(v reflect.Value, path string) map[string]string {
	empty := map[string]string{}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			empty[path] = "nil"
			return empty
		}
		return walkDoc(v.Elem(), path)
	case reflect.Struct:
		typ := v.Type()
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				name = f.Name
			}
			sub := name
			if path != "" {
				sub = path + "." + name
			}
			for k, why := range walkDoc(v.Field(i), sub) {
				empty[k] = why
			}
		}
	case reflect.Slice, reflect.Map:
		if v.Len() == 0 {
			empty[path] = "порожній"
			return empty
		}
		if v.Kind() != reflect.Slice || v.Index(0).Kind() != reflect.Struct {
			return empty
		}
		// Поле зрізу вважається заповненим, якщо воно ненульове ХОЧА Б
		// В ОДНОМУ рядку — тобто перетин «порожніх» по всіх рядках.
		//
		// Ні перший рядок, ні всі рядки поодинці не годяться. Рядки
		// навмисно різні: ladder.usd порожній у гривневому році,
		// concentration.label — у рядку брокера, funds.short — у здорового
		// фонду. Вимагати скрізь означало б список винятків завдовжки з сам
		// документ; дивитись лише на нульовий — не помітити, що фаза
		// перестала заповнювати поле в усіх інших рядках.
		empty = walkDoc(v.Index(0), path)
		for i := 1; i < v.Len() && len(empty) > 0; i++ {
			row := walkDoc(v.Index(i), path)
			for k := range empty {
				if _, alsoEmpty := row[k]; !alsoEmpty {
					delete(empty, k)
				}
			}
		}
	default:
		if v.IsZero() {
			empty[path] = "нуль"
		}
	}
	return empty
}
