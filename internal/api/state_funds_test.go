package api

import (
	"math"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// TestFundsPromiseYieldPairsWithItself — коли основа рядка «обіцяно
// фондом», номінальна й реальна беруться З ТІЄЇ САМОЇ обіцянки.
//
// Це той баг, який видно було на екрані: плитка показувала 9.5%
// реальних і 2.8% номінальних для того самого фонду. Реальна приходила з
// доларової обіцянки (до неї гривневий штраф не застосовується), а
// номінальна відкочувалась на виміряні гривневі дивіденди. Два числа з
// різних джерел стояли поруч як «реальна / номінальна», хоч друге не є
// першим до поправки, — і виходило неможливе.
func TestFundsPromiseYieldPairsWithItself(t *testing.T) {
	today := domain.Date("2026-07-15")
	ops := []domain.FundOp{
		// Купівля місяць тому: історії замало, щоб ануалізувати повну
		// дохідність, тож спрацює гілка обіцянки — рівно як на живих
		// даних, де total_pct порожній.
		{Date: "2026-06-20", Fund: "Inzhur", Kind: domain.FundBuy,
			Qty: 100, Amount: 100_000, Currency: money.UAH},
		{Date: "2026-07-05", Fund: "Inzhur", Kind: domain.FundDividend,
			Amount: 2_300, Currency: money.UAH},
	}
	src := &sources{fundOps: ops, fundRefs: map[string]store.Fund{
		// Обіцянка 9.5% У ДОЛАРІ на гривневому сертифікаті — саме той
		// випадок, що дав розбіжність на живих даних.
		"Inzhur": {Name: "Inzhur", Currency: money.UAH,
			ExpectedYieldBP: 950, ExpectedYieldCur: money.USD},
	}}
	hold := domain.NewHoldings(nil, nil, nil, ops, nil, nil, today)

	out := buildFunds(src, hold, fx.Rates{}, 7 /* знецінення, % */, today)
	if len(out.Rows) != 1 || out.Rows[0].YieldBasis != "обіцяно фондом" {
		t.Fatalf("очікували один рядок з основою «обіцяно фондом», маємо %+v", out.Rows)
	}
	if out.YieldRealPct > out.YieldPct {
		t.Errorf("реальна %v ВИЩА за номінальну %v — це неможливо для пари з одного джерела",
			out.YieldRealPct, out.YieldPct)
	}
	if out.YieldPct != 9.5 || out.YieldRealPct != 9.5 {
		t.Errorf("номінальна %v, реальна %v; очікували 9.5 обидві: обіцянка доларова, "+
			"а до валютної ставки гривневе знецінення не застосовується — так само, "+
			"як у доларових ОВДП", out.YieldPct, out.YieldRealPct)
	}
	// Основа зведення мусить дійти до документа, інакше плитка знову
	// зашиватиме її рядком і назве ту, якої в числі немає.
	if out.Basis != "обіцяно фондом" {
		t.Errorf("основа зведення %q, очікували «обіцяно фондом»", out.Basis)
	}
}

// TestFundsSimplePromiseBecomesCompoundEverywhere — обіцянка, задана
// ПРОСТОЮ, доходить до всіх трьох чисел рядка вже складною.
//
// Одиниця має бути одна на весь рядок. Доти переведення стояло лише в
// expected_pct, а real_pct і вага зведення рахувались наново з довідника,
// тобто з сирих 25% — і в одному рядку опинялись 20.51% номінальних
// поруч із реальними, порахованими з 25%. Та сама пара з різних джерел,
// від якої стереже сусідній тест, лише сховані на рівень глибше.
func TestFundsSimplePromiseBecomesCompoundEverywhere(t *testing.T) {
	today := domain.Date("2026-07-15")
	ops := []domain.FundOp{
		{Date: "2026-06-20", Fund: "MilTech", Kind: domain.FundBuy,
			Qty: 5, Amount: 500_000, Currency: money.UAH},
	}
	src := &sources{fundOps: ops, fundRefs: map[string]store.Fund{
		"MilTech": {Name: "MilTech", Currency: money.UAH,
			ExpectedYieldBP: 2500, ExpectedYieldCur: money.UAH,
			YieldSimpleYears: 3, Kind: store.FundAccumulating},
	}}
	hold := domain.NewHoldings(nil, nil, nil, ops, nil, nil, today)
	out := buildFunds(src, hold, fx.Rates{}, 0 /* без знецінення */, today)

	if len(out.Rows) != 1 {
		t.Fatalf("очікували один рядок, маємо %d", len(out.Rows))
	}
	row := out.Rows[0]
	const want = 20.51 // (1 + 0.25×3)^(1/3) − 1
	if math.Abs(row.ExpectedPct-want) > 0.01 {
		t.Errorf("expected_pct %v, очікували %v складних", row.ExpectedPct, want)
	}
	// Обіцянка «як її назвав фонд» лишається поруч — інакше різницю між
	// вписаними 25 і показаними 20.51 нічим не пояснити.
	if row.ExpectedSimplePct != 25 || row.ExpectedSimpleYears != 3 {
		t.Errorf("проста обіцянка в рядку: %v за %v р.; очікували 25 за 3",
			row.ExpectedSimplePct, row.ExpectedSimpleYears)
	}
	// Без знецінення реальна дорівнює номінальній — і тій самій, що в
	// expected_pct. Якщо десь лишилось сире 25, це видно тут.
	if math.Abs(row.RealPct-want) > 0.01 {
		t.Errorf("real_pct %v, а номінальна в тому ж рядку %v — рахувались із різних чисел",
			row.RealPct, row.ExpectedPct)
	}
	if math.Abs(out.YieldPct-want) > 0.01 {
		t.Errorf("зведена дохідність %v, очікували %v: вага мусить брати ту саму обіцянку",
			out.YieldPct, want)
	}
}

// TestFundsAccumulatingLeavesLockedCapital — накопичувальний фонд не
// потрапляє в замкнений капітал проєкції, а розподільний потрапляє.
//
// Це вся суть поділу: у замкненому позиція лежить цеглиною, бо воно не
// росте, а весь дохід накопичувального саме в зростанні.
func TestFundsAccumulatingLeavesLockedCapital(t *testing.T) {
	today := domain.Date("2026-07-15")
	ops := []domain.FundOp{
		{Date: "2026-06-20", Fund: "MilTech", Kind: domain.FundBuy,
			Qty: 5, Amount: 500_000, Currency: money.UAH},
	}
	ref := store.Fund{Name: "MilTech", Currency: money.UAH,
		ExpectedYieldBP: 2500, ExpectedYieldCur: money.UAH,
		CloseDate: "2029-07-26", IncomeTaxBP: 1400, ExitTaxBP: 2300}
	hold := domain.NewHoldings(nil, nil, nil, ops, nil, nil, today)

	ref.Kind = store.FundAccumulating
	acc := buildFunds(&sources{fundOps: ops,
		fundRefs: map[string]store.Fund{"MilTech": ref}}, hold, fx.Rates{}, 0, today)
	if len(acc.Dist) != 0 {
		t.Errorf("накопичувальний потрапив у розподільний кошик: %+v", acc.Dist)
	}
	if len(acc.Accum[money.UAH]) != 1 {
		t.Fatalf("накопичувальний не потрапив у власний кошик: %+v", acc.Accum)
	}
	a := acc.Accum[money.UAH][0]
	if a.Value0 != 5000 || a.Cost0 != 5000 {
		t.Errorf("вартість/собівартість %v/%v, очікували 5000/5000", a.Value0, a.Cost0)
	}
	if a.TaxPct != 14 {
		t.Errorf("податок при закритті %v, очікували 14", a.TaxPct)
	}
	// Дострокова ставка — окреме число, і губиться воно тихо: у багатій
	// фікстурі сертифікатів на 10 000 при знятті 20 000/міс, тож golden
	// нуль замість 23% не показує взагалі.
	if a.ExitTaxPct != 23 {
		t.Errorf("податок при виході %v, очікували 23", a.ExitTaxPct)
	}
	// 2026-07 → 2029-07 це 36 місяців. День місяця не враховується — так
	// само, як для купонів, інакше гроші фонду лягли б у сусідній крок.
	if a.CloseM != 36 {
		t.Errorf("місяць закриття %d, очікували 36", a.CloseM)
	}

	ref.Kind = store.FundDistributing
	dis := buildFunds(&sources{fundOps: ops,
		fundRefs: map[string]store.Fund{"MilTech": ref}}, hold, fx.Rates{}, 0, today)
	if len(dis.Accum) != 0 {
		t.Errorf("розподільний потрапив у кошик зростання: %+v", dis.Accum)
	}
	if len(dis.Dist[money.UAH]) != 1 {
		t.Fatalf("розподільний не потрапив у власний кошик: %+v", dis.Dist)
	}
	d := dis.Dist[money.UAH][0]
	if d.Value != 5000 || d.Cost != 5000 {
		t.Errorf("вартість/собівартість %v/%v, очікували 5000/5000", d.Value, d.Cost)
	}
	// Собівартість і ставка виходу потрібні декумуляції: без них продаж
	// сертифікатів виглядає безподатковим. Губляться вони тихо — golden
	// на таку різницю не реагує.
	if d.ExitTaxPct != 23 {
		t.Errorf("податок при виході %v, очікували 23", d.ExitTaxPct)
	}
	// Ставка виплат — обіцянка фонду, та сама, з якої календар оцінює
	// найближчі дивіденди. Два різні числа на той самий потік означали б,
	// що картка виплат і крива капіталу говорять про різні фонди.
	if d.RatePct != 25 {
		t.Errorf("ставка виплат %v, очікували 25 (обіцянка фонду)", d.RatePct)
	}
}

// TestForeignPromiseConvertsForGrowthNotForPayouts — валютна поправка
// застосовується до ЗРОСТАННЯ і не застосовується до ВИПЛАТ.
//
// Одна обіцянка, два різні сенси. Гривневий сертифікат, чия ціна йде за
// курсом НБУ, обіцяє 9.5% у доларі. Для накопичувального це опис того, як
// росте вартість, — і гривнева вартість справді росте на знецінення
// понад обіцянку. Для розподільного та сама обіцянка описує виплати, а
// цінова частина сидить у ціні сертифіката й готівкою не приходить.
//
// Переплутати легко, і я переплутав: спершу поправка стояла в спільній
// функції й текла в обидва кошики. На живих даних це видно — виміряна
// дивідендна тієї самої позиції близько 8%, тобто поруч із самою
// обіцянкою (9.5%), а не з переведеною (17.2%).
func TestForeignPromiseConvertsForGrowthNotForPayouts(t *testing.T) {
	today := domain.Date("2026-07-15")
	ops := []domain.FundOp{
		{Date: "2026-06-20", Fund: "REIT", Kind: domain.FundBuy,
			Qty: 100, Amount: 100_000, Currency: money.UAH},
	}
	ref := store.Fund{Name: "REIT", Currency: money.UAH,
		ExpectedYieldBP: 950, ExpectedYieldCur: money.USD}
	hold := domain.NewHoldings(nil, nil, nil, ops, nil, nil, today)
	const deval = 7.0

	ref.Kind = store.FundDistributing
	dis := buildFunds(&sources{fundOps: ops,
		fundRefs: map[string]store.Fund{"REIT": ref}}, hold, fx.Rates{}, deval, today)
	if got := dis.Dist[money.UAH][0].RatePct; math.Abs(got-9.5) > 0.01 {
		t.Errorf("ставка виплат %v, очікували 9.5 — обіцянку як дану, без валютної поправки", got)
	}

	ref.Kind = store.FundAccumulating
	acc := buildFunds(&sources{fundOps: ops,
		fundRefs: map[string]store.Fund{"REIT": ref}}, hold, fx.Rates{}, deval, today)
	want := ((1+0.095)*(1+0.07) - 1) * 100 // 17.165
	if got := acc.Accum[money.UAH][0].RatePct; math.Abs(got-want) > 0.01 {
		t.Errorf("ставка зростання %v, очікували %.3f — доларова обіцянка в гривневій вартості",
			got, want)
	}
}

// TestProjectionDropsEstimatedFundFlows — оцінені дивіденди фондів не
// потрапляють у купони рукава.
//
// Це сторож проти ПОДВІЙНОГО РАХУНКУ. Календар оцінює виплати фонду на рік
// уперед, і ці ж потоки доти лягали в купони симуляції. Тепер потік фонду
// народжується в кошику Dist і живе весь горизонт — лишити поруч ще й
// оцінку означало б порахувати перший рік двічі.
//
// Купон облігації в тому ж наборі мусить пройти: якби фільтр викидав усе
// підряд, тест зеленів би з хибної причини.
func TestProjectionDropsEstimatedFundFlows(t *testing.T) {
	today := domain.Date("2026-07-15")
	in := projectionInput{
		Settings:     &state.SettingsDoc{},
		CashByCur:    map[string]int64{},
		NominalByCur: map[string]int64{},
		YieldByCur:   map[string]float64{money.UAH: 16},
		Rates:        fx.Rates{},
		Today:        today,
		Cashflow: []domain.CashflowItem{
			{Date: "2026-08-10", ISIN: "UA4000227748", Type: domain.PayCoupon,
				Amount: money.New(80_00, money.UAH)},
			{Date: "2026-08-10", ISIN: domain.FundISINPrefix + "Inzhur REIT",
				Type: domain.PayCoupon, Amount: money.New(60_00, money.UAH)},
		},
	}
	f := newSleeveFactory(in)
	if got := f.coupon[money.UAH][1]; got != 80 {
		t.Errorf("купон першого місяця %v, очікували 80: облігаційний лишається, "+
			"оцінка фонду відсіюється", got)
	}
}

// TestFundsMixedBasesSayMixed — коли фонди міряні по-різному, зведення
// не приписує собі основу найбільшого з них.
func TestFundsMixedBasesSayMixed(t *testing.T) {
	today := domain.Date("2026-07-15")
	ops := []domain.FundOp{
		// Довга історія — буде виміряна повна дохідність.
		{Date: "2025-01-10", Fund: "Старий", Kind: domain.FundBuy,
			Qty: 100, Amount: 100_000, Currency: money.UAH},
		{Date: "2025-07-10", Fund: "Старий", Kind: domain.FundDividend,
			Amount: 5_000, Currency: money.UAH},
		// Щойно куплений — лишиться обіцянка.
		{Date: "2026-06-20", Fund: "Новий", Kind: domain.FundBuy,
			Qty: 50, Amount: 50_000, Currency: money.UAH},
	}
	src := &sources{fundOps: ops, fundRefs: map[string]store.Fund{
		"Новий": {Name: "Новий", Currency: money.UAH, ExpectedYieldBP: 900},
	}}
	hold := domain.NewHoldings(nil, nil, nil, ops, nil, nil, today)

	out := buildFunds(src, hold, fx.Rates{}, 7, today)
	if out.Basis != "різні основи" {
		t.Errorf("основа зведення %q; фонди міряні по-різному, і назвати одну з них "+
			"спільною означало б видати частину за ціле", out.Basis)
	}
}

// TestFundsYieldIsWeightedByMarketValue — зведена дохідність фондів важить
// ринковою вартістю, а не рахує просте середнє.
//
// Golden цього не стереже: у багатій фікстурі лише ОДИН фонд має
// ненульову вартість, а на одному фонді зважене й просте середнє
// збігаються. Мутаційна перевірка це й показала — підміна ваг на одиниці
// golden не завалила. Тож перевірка тут, на двох фондах різного розміру.
//
// Питання не косметичне: дрібний фонд із гучним відсотком інакше тягнув
// би плитку на себе, і вона суперечила б таблиці під собою.
func TestFundsYieldIsWeightedByMarketValue(t *testing.T) {
	today := domain.Date("2026-07-15")
	ops := []domain.FundOp{
		// Великий фонд: 100 сертифікатів по 10.00 ₴ = 1 000 ₴ вартості.
		{Date: "2025-01-10", Fund: "Великий", Kind: domain.FundBuy,
			Qty: 100, Amount: 100_000, Currency: money.UAH},
		{Date: "2025-07-10", Fund: "Великий", Kind: domain.FundDividend,
			Amount: 5_000, Currency: money.UAH},
		// Дрібний: 10 сертифікатів по тій самій ціні = 100 ₴ вартості,
		// але дивіденд удвічі більший за розміром позиції.
		{Date: "2025-01-11", Fund: "Дрібний", Kind: domain.FundBuy,
			Qty: 10, Amount: 10_000, Currency: money.UAH},
		{Date: "2025-07-11", Fund: "Дрібний", Kind: domain.FundDividend,
			Amount: 4_000, Currency: money.UAH},
	}
	src := &sources{fundOps: ops, fundRefs: map[string]store.Fund{}}
	hold := domain.NewHoldings(nil, nil, nil, ops, nil, nil, today)

	out := buildFunds(src, hold, fx.Rates{}, 0, today)
	if len(out.Rows) != 2 {
		t.Fatalf("очікували 2 рядки фондів, маємо %d", len(out.Rows))
	}

	nom := map[string]float64{}
	mv := map[string]float64{}
	for _, r := range out.Rows {
		n := r.TotalPct
		if n == 0 {
			n = r.YieldNetPct
		}
		nom[r.Fund] = n
		mv[r.Fund] = r.MarketValue
	}
	if mv["Великий"] <= mv["Дрібний"] {
		t.Fatalf("фікстура зіпсована: великий фонд не більший (%v проти %v)",
			mv["Великий"], mv["Дрібний"])
	}
	if nom["Великий"] == nom["Дрібний"] {
		t.Fatalf("фікстура зіпсована: дохідності однакові (%v), зважування не видно",
			nom["Великий"])
	}

	// Дві незалежні властивості будь-якого середньозваженого. Порівнювати
	// з переписаною тут же формулою було б безглуздо — такий тест повторює
	// код, а не перевіряє його.
	//
	// Перша: результат лежить МІЖ крайніми значеннями. Ловить зіпсовані
	// ваги, від яких число вилітає за межі обох фондів.
	lo, hi := math.Min(nom["Великий"], nom["Дрібний"]), math.Max(nom["Великий"], nom["Дрібний"])
	if out.YieldPct < lo || out.YieldPct > hi {
		t.Errorf("зведена дохідність %v поза межами [%v; %v] — це вже не середнє",
			out.YieldPct, lo, hi)
	}
	// Друга: воно ближче до ВЕЛИКОГО фонду, ніж просте середнє. Ловить
	// втрату самих ваг, від якої число лишається в межах, але з'їжджає в
	// середину.
	mean := (nom["Великий"] + nom["Дрібний"]) / 2
	if math.Abs(out.YieldPct-nom["Великий"]) >= math.Abs(mean-nom["Великий"]) {
		t.Errorf("зведена дохідність %v не ближча до великого фонду (%v), ніж просте середнє (%v) — ваги не працюють",
			out.YieldPct, nom["Великий"], mean)
	}
}

// ГОЛОВНИЙ ТЕСТ ЦІЄЇ ЗМІНИ: накопичувальний фонд, куплений один раз і
// пів року тому, показує ОБІЦЯНКУ, а не нуль.
//
// Доти тут стояла гілка «дивіденди + зміна ціни» з total_pct = 0 і
// відʼємною реальною: FundTotalReturn повертав нуль, бо термінальна
// вартість дорівнювала собівартості за побудовою, і цей нуль витісняв
// обіцянку 25%. Тобто застосунок називав збитковим папір, ціни якого він
// просто не знає.
func TestFundPromiseSurvivesWithoutPriceEvidence(t *testing.T) {
	today := domain.Date("2026-07-15")
	ops := []domain.FundOp{
		{Date: "2026-01-10", Fund: "MilTech", Kind: domain.FundBuy,
			Qty: 5, Amount: 500_000, Currency: money.UAH},
	}
	src := &sources{fundOps: ops, fundRefs: map[string]store.Fund{
		"MilTech": {Name: "MilTech", Currency: money.UAH,
			ExpectedYieldBP: 2500, ExpectedYieldCur: money.UAH,
			YieldSimpleYears: 3, Kind: store.FundAccumulating},
	}}
	hold := domain.NewHoldings(nil, nil, nil, ops, nil, nil, today)
	out := buildFunds(src, hold, fx.Rates{}, 0 /* без знецінення */, today)

	row := out.Rows[0]
	if row.YieldBasis != "обіцяно фондом" {
		t.Fatalf("міряти нема по чому — основа мала лишитись обіцянкою, маємо %q", row.YieldBasis)
	}
	if row.TotalPct != 0 {
		t.Errorf("повної дохідності тут бути не може, маємо %v", row.TotalPct)
	}
	if row.RealPct <= 0 {
		t.Errorf("реальна мала прийти з обіцянки й бути додатною, маємо %v", row.RealPct)
	}
	if row.PriceMarked {
		t.Error("позначок не було — price_marked мав лишитись хибним")
	}
}

// Та сама позиція з позначкою ціни: вимір зʼявився й ВИТІСНИВ обіцянку, а
// ринкова вартість піднялась над собівартістю.
func TestFundMeasuredDisplacesPromiseAfterMark(t *testing.T) {
	today := domain.Date("2026-07-15")
	ops := []domain.FundOp{
		{Date: "2026-01-10", Fund: "MilTech", Kind: domain.FundBuy,
			Qty: 5, Amount: 500_000, Currency: money.UAH},
	}
	// 1000.00 ₴ за сертифікат на купівлі → 1120.00 ₴ сьогодні.
	marks := []domain.FundPrice{{Fund: "MilTech", Date: today, Price: 11_200_000}}
	src := &sources{fundOps: ops, fundPrices: marks,
		fundRefs: map[string]store.Fund{
			"MilTech": {Name: "MilTech", Currency: money.UAH,
				ExpectedYieldBP: 2500, ExpectedYieldCur: money.UAH,
				YieldSimpleYears: 3, Kind: store.FundAccumulating},
		}}
	hold := domain.NewHoldings(nil, nil, nil, ops, marks, nil, today)
	out := buildFunds(src, hold, fx.Rates{}, 0 /* без знецінення */, today)

	row := out.Rows[0]
	if row.YieldBasis != "дивіденди + зміна ціни" {
		t.Fatalf("з позначкою вимір мав витіснити обіцянку, маємо %q", row.YieldBasis)
	}
	if row.TotalPct <= 0 {
		t.Errorf("повна дохідність мала стати додатною, маємо %v", row.TotalPct)
	}
	if !row.PriceMarked {
		t.Error("price_marked мав сказати, що ціна прийшла з позначки")
	}
	if row.MarketValue <= row.CostBasis {
		t.Errorf("вартість %v мала піднятись над собівартістю %v",
			row.MarketValue, row.CostBasis)
	}
	// Обіцянка при цьому НЕ зникає з рядка: без неї звірка припущення була
	// б неможлива саме тоді, коли вона нарешті стала можливою.
	if row.ExpectedPct <= 0 {
		t.Error("обіцянка мала лишитись у рядку поруч із виміряним")
	}
}

// Зростання самої ціни — окреме число, і воно НЕ чіпає основу рядка.
// Витіснення в yield_basis робить лише повна дохідність; четверте значення
// зробило б основу залежною від того, який вимір випадково дозрів першим.
func TestFundPriceReturnStandsApartFromBasis(t *testing.T) {
	today := domain.Date("2026-07-15")
	ops := []domain.FundOp{
		{Date: "2026-07-01", Fund: "MilTech", Kind: domain.FundBuy,
			Qty: 5, Amount: 500_000, Currency: money.UAH},
	}
	// Опублікована фондом історія до першої купівлі — рік розриву.
	marks := []domain.FundPrice{
		{Fund: "MilTech", Date: "2025-07-01", Price: 8_000_000},
		{Fund: "MilTech", Date: "2026-07-01", Price: 10_000_000},
	}
	src := &sources{fundOps: ops, fundPrices: marks,
		fundRefs: map[string]store.Fund{
			"MilTech": {Name: "MilTech", Currency: money.UAH,
				ExpectedYieldBP: 2500, ExpectedYieldCur: money.UAH,
				YieldSimpleYears: 3, Kind: store.FundAccumulating},
		}}
	hold := domain.NewHoldings(nil, nil, nil, ops, marks, nil, today)
	out := buildFunds(src, hold, fx.Rates{}, 0 /* без знецінення */, today)

	row := out.Rows[0]
	if math.Abs(row.PriceReturnPct-25) > 0.5 {
		t.Errorf("+25%% за рік — очікували близько 25, маємо %v", row.PriceReturnPct)
	}
	// Гроші працюють два тижні: повної дохідності тут бути не може, і
	// основа лишається обіцянкою — попри те, що зростання ціни вже виміряне.
	if row.YieldBasis != "обіцяно фондом" {
		t.Errorf("зростання ціни основу не міняє, маємо %q", row.YieldBasis)
	}
}
