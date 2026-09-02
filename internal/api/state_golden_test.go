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
	"strconv"
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
	// Курси. Дві крайні точки потрібні бенчмарку («а якби я просто тримав
	// долари»), проміжні — зведеному XIRR: він переводить КОЖЕН потік за
	// курсом його власної дати й мовчить цілком, якщо бодай одного курсу
	// немає. Без єрових точок до d(-110) єровий лот лишався б без курсу, і
	// total_return не зʼявився б у фікстурі взагалі — тобто ціла фаза не
	// перевірялась би. Дірка не вигадана: backfill тягне лише долар, євро
	// набирається добовою джобою з дня встановлення.
	for _, r := range []struct {
		cur  string
		e4   int64
		date domain.Date
	}{
		{money.USD, 380000, d(-700)}, {money.USD, 412000, d(-400)},
		{money.USD, 428000, d(-200)}, {money.USD, 441234, d(-1)},
		{money.EUR, 430000, d(-400)}, {money.EUR, 455000, d(-200)},
		{money.EUR, 480000, d(-1)},
	} {
		if err := st.SaveRate(ctx, r.cur, r.e4, r.date); err != nil {
			t.Fatal(err)
		}
	}

	// Помісячна історія — те, що в житті насипає беклог (десять років
	// долара) і добова джоба (євро з дня встановлення). Доти фікстура мала
	// чотири точки долара й три євро, і валютне вікно на ній не складалось
	// узагалі: domain.FXPlace вимагає щонайменше дванадцять точок, інакше
	// перцентиль виглядає точним, не бувши ним.
	//
	// Довжини різні НАВМИСНО. У долара десять років, тож усі три вікна
	// повні; у євро три, тож рядок «10 років» будується з тридцяти шести
	// точок — і саме поле points має це видати. Гілка не вигадана: беклог
	// тягне лише долар, і на живій базі євро рівно таке.
	for _, r := range []struct {
		cur    string
		months int
		fromE4 int64
		toE4   int64
	}{
		{money.USD, 120, 240_000, 441_234},
		{money.EUR, 36, 400_000, 480_000},
	} {
		for i := 1; i <= r.months; i++ {
			e4 := r.toE4 - (r.toE4-r.fromE4)*int64(i)/int64(r.months)
			if err := st.SaveRate(ctx, r.cur, e4, d(-30*i)); err != nil {
				t.Fatal(err)
			}
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
		// Позначки ціни (0034) — саме накопичувальному фонду, бо це його
		// єдиний канал доходу. Дві точки з розривом більше пів року: одна
		// ДО першої купівлі (опублікована фондом історія — рівно те, заради
		// чого таблиця й заведена), одна на сьогодні. Без них ані
		// price_marked, ані price_return_pct у фікстурі не з'являться, а
		// разом із ними не перевіряється й уся гілка витіснення: цей фонд
		// куплено ОДИН раз, тобто без позначок його дохідність не
		// вимірюється за побудовою.
		if f.Name == "Строковий фонд" {
			if _, err := st.AddFundPricePoints(ctx, f.ID, []domain.FundPrice{
				{Date: d(-400), Price: 8_500_000},
				{Date: d(0), Price: 10_600_000},
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Вклади: діючий поповнюваний і закритий достроково.
	id, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: money.UAH, Principal: 10000000, RateBP: 1600,
		OpenDate: d(-90), MaturityDate: d(275), Payout: domain.PayoutMonthly,
		// Відкличний — щоб «зламне» в ліквідності було не нулем. Пара з
		// безвідкличною сходинкою подушки нижче й перевіряє, що прапорець
		// розводить ці гроші по різних рядках картки.
		TaxBP: 1950, Replenishable: true, Revocable: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDepositTopup(ctx, domain.DepositTopup{
		DepositID: id, Date: d(-30), Amount: 2000000,
	}); err != nil {
		t.Fatal(err)
	}
	// Дві сходинки ПОДУШКИ, і навмисно різні за розривністю: сама пара й
	// перевіряє, що прапорець щось означає. Відкличний тримає доступ
	// (розмін), безвідкличний — ні, і на однакових сумах вони дають різні
	// стани драбини.
	//
	// Строки коротші за ціль подушки (6 місяців), тож у профілі доступу є і
	// покриті горизонти, і непокриті — інакше половина полів лишилась би
	// нулями, і сторож повноти був би задоволений документом, який нічого
	// не рахує.
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: money.UAH, Principal: 2000000, RateBP: 1500,
		OpenDate: d(-30), MaturityDate: d(60), Payout: domain.PayoutEnd,
		TaxBP: 1950, IsReserve: true, Revocable: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTermDeposit(ctx, domain.Deposit{
		Bank: "ПУМБ", Currency: money.UAH, Principal: 1500000, RateBP: 1550,
		OpenDate: d(-15), MaturityDate: d(105), Payout: domain.PayoutEnd,
		TaxBP: 1950, IsReserve: true,
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
		// Рух ПОТОЧНОГО місяця: без нього reserve.fill_moved_uah лишається
		// нулем, і гілка «стеля зменшується на вже відкладене» не
		// перевіряється взагалі — саме та, заради якої механізм і переробляли.
		{Date: d(-4), Amount: 100000, Currency: money.UAH, Place: "готівка"},
	} {
		if _, err := st.AddReserveOp(ctx, op); err != nil {
			t.Fatal(err)
		}
	}

	// Дві цілі накопичення, і вони РІЗНІ навмисно.
	//
	// «Авто» — у доларах із гривневими рухами й дедлайном: саме ця
	// комбінація вмикає все, що є в картці одразу — переведення за
	// сьогоднішнім курсом, fx_mixed, потрібний темп, фактичний темп і
	// прогноз «коли збереться». Гривнева ціль без дати перевіряла б рівно
	// нуль із цього.
	//
	// Дедлайн близький навмисно: за нинішнім темпом ціль до нього не
	// збереться, тож behind у фікстурі стоїть true. Далека дата лишила б це
	// поле false — тобто гілку «відстаю» ніхто б не побачив, а вона й є
	// відповіддю на головне питання цілі.
	//
	// Закриття «Ремонту» стоїть поза ПОТОЧНИМ місяцем свідомо: зняття на
	// всю суму в липні зсунуло б «внесено нетто» й перемалювало пів
	// золотого документа шумом, який до цілей стосунку не має.
	//
	// «Ремонт» — гривнева, без дедлайну й ЗАКРИТА: закрита ціль лишається в
	// списку (журнал під нею і є історія), тож поле done_date мусить бути в
	// фікстурі заповненим, а не лишитись гілкою, якої ніхто не бачив.
	car, err := st.AddGoal(ctx, store.Goal{
		Name: "Авто", TargetAmount: 2000000, Currency: money.USD,
		DueDate: d(300), Priority: 0, Place: "готівка",
	})
	if err != nil {
		t.Fatal(err)
	}
	repair, err := st.AddGoal(ctx, store.Goal{
		Name: "Ремонт", TargetAmount: 15000000, Currency: money.UAH,
		Priority: 1, Place: "картка", DoneDate: d(-40),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []store.GoalOp{
		{GoalID: car, Date: d(-120), Amount: 20000000, Currency: money.UAH, Place: "готівка"},
		// Рух у вікні темпу (183 дні) і в ПОТОЧНОМУ місяці: без нього
		// actual_* лишаються нулями, а «внесено нетто» не перевіряє гілку,
		// заради якої рухи цілей туди й додані.
		//
		// Сума мала навмисно — МЕНША за місячну стелю цілей. Інакше
		// відкладене цього місяця перекрило б стелю цілком, fill_now_uah
		// вийшов би нулем, і гілка «скільки ще лишилось відкласти» не
		// перевірялась би зовсім.
		{GoalID: car, Date: d(-3), Amount: 100000, Currency: money.UAH, Place: "готівка"},
		// Ціль закрита: зібрали й витратили. Залишок нульовий, тож у капітал
		// вона не входить — і саме це має бути видно у фікстурі.
		{GoalID: repair, Date: d(-200), Amount: 15000000, Currency: money.UAH, Place: "картка"},
		{GoalID: repair, Date: d(-40), Amount: -15000000, Currency: money.UAH, Place: "картка"},
	} {
		if _, err := st.AddGoalOp(ctx, op); err != nil {
			t.Fatal(err)
		}
	}

	// Борг (0045). Картка з пільговим циклом і розстрочка ВСЕРЕДИНІ неї:
	// саме ця пара робить живими всі гілки фази — обовʼязковий платіж
	// місяця, стелю подушки на час боргу й непільгову частину, яка одна
	// потрапляє в чергу погашення.
	//
	// Комісія 1,99% на місяць — справжня ставка ПУМБ, і на ній ефективна
	// виходить ≈50% річних, тобто вища за знецінення. Безкоштовна
	// розстрочка стелі подушки не вмикала б, і гілка лишилась би
	// неперевіреною.
	card, err := st.AddDebt(ctx, domain.Debt{
		Name: "ПУМБ ВсеМожу", Kind: domain.DebtCard, Currency: money.UAH,
		LimitAmount: 20000000, StatementDay: 30, APRBp: 4788, APROverdueBp: 6200,
		MinPaymentBp: 300, MinPaymentFloor: 10000, LateFee: 10000,
		OpenedDate: d(-800),
		// Ціль виходу з ліміту: без неї цілий блок debt.exit лишився б
		// нулем, а разом із ним і гілка, заради якої фаза й робиться.
		// Дата достатньо далека, щоб стеля витрат вийшла додатною —
		// інакше перевірявся б лише стан «не встигнути».
		ExitBy: d(300),
	})
	if err != nil {
		t.Fatal(err)
	}
	fridge, err := st.AddDebt(ctx, domain.Debt{
		Name: "Холодильник частинами", Kind: domain.DebtInstallment, Currency: money.UAH,
		CardID: card, Principal: 3000000, PaymentsTotal: 9,
		FirstPaymentDate: d(-60), FeeMonthBp: 199, OpenedDate: d(-90),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Звірка. Баланс ВІДʼЄМНИЙ, і виписка з нього ж: додатний баланс поруч
	// із живою випискою — стан, якого банки не роблять (платіж гасить
	// виписку й піднімає баланс однією дією), і фікстура не має його
	// закріплювати. Готівка з ліміту окремим числом: вона під відсотком з
	// першого дня й одна йде в чергу погашення.
	// ПЕРША зі ДВОХ звірок: без пари витрати не виміряти, а без виміру
	// половина блоку виходу з ліміту лишається нулями — разом із гілкою,
	// яка й відрізняє факт від заявленого наміру.
	if _, err := st.AddDebtMark(ctx, domain.DebtMark{
		DebtID: card, Date: d(-40), Balance: -3000000, Note: "місяць тому",
	}); err != nil {
		t.Fatal(err)
	}
	// Надходження МІЖ звірками: саме воно робить тотожність «внесено мінус
	// приріст балансу» розвʼязною.
	if _, err := st.AddDebtOp(ctx, domain.DebtOp{
		DebtID: card, Date: d(-20), Kind: domain.DebtOpPayment, Amount: 1200000,
		Note: "зарплата на картку",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDebtMark(ctx, domain.DebtMark{
		DebtID: card, Date: d(-5), Balance: -2000000,
		StatementDue: 1840000, NonGrace: 500000, Note: "звірка з додатком",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDebtOp(ctx, domain.DebtOp{
		DebtID: card, Date: d(-2), Kind: domain.DebtOpPayment, Amount: 300000,
		Note: "частина зарплати",
	}); err != nil {
		t.Fatal(err)
	}
	// Платіж за розстрочкою БІЛЬШИЙ за обовʼязковий: різниця і є
	// достроковим погашенням, і без цього рядка debt.paid_extra_uah
	// лишився б нулем — тобто гілка «стеля вже частково вибрана» не
	// перевірялась би зовсім.
	if _, err := st.AddDebtOp(ctx, domain.DebtOp{
		DebtID: fridge, Date: d(-2), Kind: domain.DebtOpPayment, Amount: 600000,
		Note: "закинув понад графік",
	}); err != nil {
		t.Fatal(err)
	}

	// Позначена як отримана майбутня виплата — гілка arrived поза датою.
	if err := st.SetPaymentStatus(ctx, uahBond.ISIN, d(150), "received"); err != nil {
		t.Fatal(err)
	}

	// Добовий знімок місячної давнини — заради capital_delta_30. Один, і
	// на 35-й день, а не на 30-й: дельта бере ОСТАННІЙ знімок на ≥30 днів
	// тому, і фікстура мусить показати саме цю гілку (пропущений день),
	// а не збіг дат. Числа менші за сьогоднішній капітал, щоб дельта
	// була додатною й ненульовою в кожному полі.
	if err := st.SaveSnapshot(ctx, store.Snapshot{
		Date: d(-35), InvestedUAH: 9_000_000, NominalUAHEq: 9_500_000,
		USDShareBP: 3000, UninvestedUAH: 100_000, MonthTargetUAH: 1_000_000,
		AccountUAH: 300_000, FundsUAH: 1_000_000, DepositsUAH: 2_000_000,
		FundsCostUAH: 950_000, ReserveUAH: 5_000_000, NPFUAH: 500_000,
		NPFCostUAH: 480_000, GoalsUAH: 1_000_000, NetWorthUAH: -2_000_000,
	}); err != nil {
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
		//
		// Витрати задані НОВОЮ парою (сума + валюта), а не спадковим
		// monthly_expenses_uah: саме через цю пару їх тепер вводять, і
		// фікстура мусить ганяти живий шлях, а не той, що лишився для
		// сумісності. Валюта гривнева свідомо — переведення в чужій валюті
		// зрушило б кожне число картки резерву й сховало б у шумі те, заради
		// чого золотий документ і є. Долар перевіряється окремим тестом
		// (TestExpensesInUSDConvertAtTodayRate).
		"monthly_expenses": "25000", "monthly_expenses_currency": "UAH",
		"reserve_target_months":  "6",
		"reserve_fill_share_pct": "20",
		// Стеля подушки на час боргу — 5 місяців проти шести звичайних.
		// Менша за ціль, щоб гілка reserve.debt_capped була живою, але
		// БІЛЬША за наявну подушку: інакше розрив закрився б цілком, і
		// разом із ним у нулі пішли б усі fill_* — тобто обрізання
		// перевірялось би ціною шести інших полів.
		"reserve_debt_months": "5",
		// Дострокове погашення й доля цілей на час боргу — обидва ключі
		// явні, бо дефолти в документі не видно (omitempty), і зламане
		// збереження пройшло б повз сторожа ненульових полів.
		// Стеля дострокового навмисно БІЛЬША за вже сплачене понад графік:
		// інакше fill_now_uah вийшов би нулем, і гілка «скільки з місячної
		// частки ще лишилось» не перевірялась би.
		"debt_fill_share_pct": "40", "debt_fill_from": "plan",
		"goals_while_debt": "keep",
		// Стеля цілей — навмисно МАЛА проти потрібного темпу «Авто»
		// (≈64 000 ₴/міс проти ≈2 100 ₴ стелі). Саме так у фікстурі
		// зʼявляється short_month_uah: «щоб устигнути, бракує стільки-то на
		// місяць». Велика стеля лишила б це поле нулем — тобто головна
		// відповідь механізму не перевірялась би.
		"goals_fill_share_pct": "20", "goals_fill_from": "any",
		// Не "any": дефолт у документі не видно (omitempty), і зламане
		// збереження цього ключа пройшло б повз сторожа ненульових полів.
		"reserve_fill_from": "plan",
		// Голова й стеля строку. Голова 2 з шести місяців лишає хвіст, який
		// є чим набирати, а стеля 6 дозволяє сходинку на весь хвіст — тобто
		// обидві гілки next_rung_months живі.
		// Голова 1 з шести місяців: журналу подушки на неї вистачає, тож
		// хвіст є чим набирати й next_rung_months живий. Стан «голова не
		// добрана» перевіряється окремим тестом — у golden він лишив би
		// половину драбини нулями.
		"reserve_liquid_months": "1", "reserve_max_term_months": "6",
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
	resp, b := do(t, "POST", srv+"/api/plan/flows",
		`{"name":"Зарплата","kind":"income","amount":"40000.00","cadence":"month","from_date":"`+
			string(d(-30))+`","invest_pct":"40"}`)
	if resp.StatusCode != 201 {
		t.Fatalf("потік плану: %d %s", resp.StatusCode, b)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(b), &created); err != nil || created.ID == 0 {
		t.Fatalf("id потоку: %v %s", err, b)
	}
	// ВИТРАТНИЙ потік — інакше month_plan.expense_uah лишився б нулем, а
	// нетто плану місяця дорівнювало б надходженням, тобто гілка «витрати
	// зменшують те, що можна закинути» не перевірялась би взагалі.
	if resp, b := do(t, "POST", srv+"/api/plan/flows",
		`{"name":"Оренда","kind":"expense","amount":"9000.00","cadence":"month","from_date":"`+
			string(d(-30))+`","invest_pct":"100"}`); resp.StatusCode != 201 {
		t.Fatalf("витратний потік: %d %s", resp.StatusCode, b)
	}
	// Відмітка надходження за ПОТОЧНИЙ місяць — сумою, відмінною від
	// планової: так перевіряється саме заміщення, а не збіг. Без неї
	// month_plan.received_uah і marked лишились би нулями.
	month := string(goldenNow.Format("2006-01"))
	if resp, b := do(t, "POST", srv+"/api/plan/receipts",
		`{"flow_id":`+strconv.FormatInt(created.ID, 10)+`,"month":"`+month+
			`","amount":"41500.00","currency":"UAH","note":"премія за квартал"}`); resp.StatusCode != 201 {
		t.Fatalf("відмітка надходження: %d %s", resp.StatusCode, b)
	}
	// Позапланове надходження — окрема гілка (у planMarks воно не входить),
	// і без нього month_plan.extra_uah не заповнюється нічим.
	if resp, b := do(t, "POST", srv+"/api/plan/receipts",
		`{"flow_id":0,"month":"`+month+
			`","name":"Продаж велосипеда","amount":"6000.00","currency":"UAH","invest_pct":"50"}`); resp.StatusCode != 201 {
		t.Fatalf("позапланове надходження: %d %s", resp.StatusCode, b)
	}
	if resp, b := do(t, "POST", srv+"/api/plan/actions",
		`{"date":"`+string(d(200))+`","type":"lock","amount":"50000.00","rate_pct":"20","months":24,"name":"MilTech"}`); resp.StatusCode != 201 {
		t.Fatalf("дія плану: %d %s", resp.StatusCode, b)
	}
}

// buildRichDoc піднімає сервер на багатій фікстурі й будує документ на
// ФІКСОВАНИЙ момент.
//
// buildStateTasked, а не голий buildState: саме цей документ віддає
// /api/summary і публікує MQTT, тож сітка мусить лежати під тим, що
// справді їде споживачам, — разом із чергою задач. Голий buildState лишив
// би tasks вічно порожніми, і TestDocFieldsPopulated довелось би вимикати
// винятком, тобто перестати перевіряти цілу фазу.
func buildRichDoc(t *testing.T) *state.Doc {
	t.Helper()
	srv, st := testServer(t)
	richPortfolio(t, srv.URL, st)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	doc, err := New(st, nil, log).buildStateTasked(context.Background(), goldenNow)
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
var allowedZero = map[string]string{
	// Нуль тут — не «фаза не заповнила», а ЗАКОНЧЕНИЙ СТАН: на цій фікстурі
	// за поточний місяць внесено 65 000 ₴ нетто при плані в 10 600 ₴, тобто
	// план місяця перевиконано, і закидати більше нема чого. Саме заради
	// цієї гілки (max(0, …) замість від'ємного числа) фікстура й лишається
	// такою: підняти план вище за внесене можна було б лише премією тисяч
	// на двісті, і тоді не перевірялась би вона.
	//
	// Виняток зникне сам, щойно фікстура зміниться в інший бік: зустрічна
	// перевірка нижче валить тест на винятку, який більше нічого не прикриває.
	"month_plan.left_uah": "план місяця перевиконано — внесено більше, ніж обіцяв план",
	// Ці два порожні ЧЕРЕЗ ОДИН ОДНОГО, і покрити обидва однією фікстурою
	// неможливо за побудовою: burn_why існує рівно тоді, коли виміру НЕ
	// відбулось, а фікстура має дві звірки й надходження між ними, тобто
	// вимір відбувся. Гілку «виміряти не вийшло» тримає окремий тест
	// TestCardBurnSilentWithoutSecondMark.
	"debt.exit.burn_why": "витрати виміряно — пояснювати, чому виміру немає, нема чого",
	// А це порожнє через ту саму пару: виміряні витрати малі, темп у ціль
	// укладається, і відставання немає. Гілку «не встигаєш» тримає
	// TestCardExitETAWhenSpendingExceedsIncome.
	"debt.exit.short_per_month_uah": "темп укладається в ціль виходу — бракувати нема чому",
	// Останній крок проходу ЗАВЖДИ лишає нуль — інакше це не вихід у нуль,
	// а просто дванадцять рядків. Тобто поле не може бути ненульовим у
	// КОЖНОМУ рядку за побудовою, і виняток тут не про фікстуру, а про
	// саму природу таблиці. Що борг меншає крок за кроком, тримає
	// TestDebtExitScheduleReachesZero.
	"debt.exit.schedule.left_uah": "останній крок проходу доводить борг до нуля — на те він і останній",
	// Подушка фікстури (83 649 ₴) більша за майбутні платежі за боргами
	// (32 512 ₴), тобто рубіж покриття вже взято, і нуль тут — ВІДПОВІДЬ
	// «перекрито», а не мовчання. Тримати фікстуру в стані «не перекрито»
	// довелося б утричі меншою подушкою, і тоді не перевірялась би ціла
	// гілка наповнення. Гілку «бракує» тримає TestReserveDebtCoverGap.
	"reserve.debt_cover_gap_uah": "подушка вже перекриває борг — бракувати нема чому",
	// Звірка фікстури — 10 липня, а карткова розстрочка списується пізніше:
	// до звірки з картки не пішло нічого, і нуль тут — відповідь, а не
	// мовчання. Сусідні paid_before_mark_uah і spend_before_mark_uah
	// заповнені тією самою звіркою, тобто саме відновлення боргу на
	// початок місяця золотий тест бачить; гілку «розстрочка до звірки»
	// тримає TestDebtExitStartsFromMonthStart через тотожність запасу.
	"debt.exit.installments_before_mark_uah": "розстрочки фікстури списуються після звірки — до неї нема чого віднімати",
}

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
