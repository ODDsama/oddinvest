package state

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
)

// sampleDoc — документ у тому вигляді, у якому його заповнює будівник:
// прямі поля кладуться в Doc, похідні добудовує Derive.
//
// Доти тут був один літерал Input на пʼятдесят полів, і він працював ще й
// як чек-лист: видно було, що подано все. Тепер прямі поля й входи
// похідних розділені, і саме це розділення й перевіряється.
//
// ІНВАРІАНТ, який дорожчий за все інше в цьому файлі: тут мусить бути
// КОЖНЕ поле, яке читає інтеграція Home Assistant.
//
// Причина механічна. З цього документа генерується contract/fixtures/
// basic.json, і ця фікстура — ЄДИНЕ, проти чого тестується репозиторій
// ha-oddinvest: його pytest бачить лише її. Поле, якого тут немає,
// приїжджає в парсер інтеграції неперевіреним — не «перевіреним слабко»,
// а взагалі. Тож коли в HA додається читання нового поля, першим кроком
// воно додається сюди.
func sampleDoc(t *testing.T) (*Doc, DeriveInput) {
	t.Helper()
	monthDep := money.New(450_000, money.UAH)
	monthTarget := money.New(500_000, money.UAH)
	settings := func() *SettingsDoc {
		tgt, u := 5000.0, 50.0
		// Витрати й ціль резерву задані, щоб deriveReserve добудував
		// картку цілком: без них він віддає саму лише суму, і поля
		// months/target_uah/gap_uah у фікстурі не зʼявились би.
		exp, months := 30_000.0, 3.0
		// Ціль НПФ і обидва числа знижки — щоб фікстура показувала їх
		// заповненими: ПДФО за рік і є перемикачем знижки, тож без нього
		// npf_credit_* у фікстурі не зʼявились би взагалі.
		npfTgt, pdfo, capMonth := 10.0, 40_000.0, 4_660.0
		// Голова подушки й стеля строку — щоб драбина доступу зʼявилась у
		// фікстурі заповненою. Без них deriveReserveLadder віддає профіль
		// без вимоги до голови, і половина полів лишилась би нулями.
		liquidM, maxTerm := 1.0, 6.0
		return &SettingsDoc{
			MonthlyTargetUAH: &tgt, USDTargetSharePct: &u,
			// Витрати ТРЬОМА полями, бо їх у документі три: введена сума,
			// її валюта і виведене гривневе число. Тут вони збігаються —
			// пакет state курсів не має й перекласти нічого не може
			// (переклад робить resolveExpensesUAH у будівнику), — але
			// фікстура мусить показати кожне поле заповненим: саме проти
			// неї тестується інтеграція.
			MonthlyExpenses: &exp, MonthlyExpensesCurrency: "UAH",
			MonthlyExpensesUAH: &exp, ReserveTargetMonths: &months,
			ReserveLiquidMonths:  &liquidM,
			ReserveMaxTermMonths: &maxTerm,
			TargetNPFPct:         &npfTgt,
			NPFCreditPDFOYearUAH: &pdfo,
			NPFCreditCapMonthUAH: &capMonth,
		}
	}()
	doc := &Doc{
		MonthInvestedUAH:  Major(money.New(450_000, money.UAH)),
		MonthDepositedUAH: Major(monthDep),
		MonthTargetUAH:    Major(monthTarget),
		UninvestedUAH:     Major(money.New(0, money.UAH)),
		Settings:          settings,
		XIRRPct:           map[string]float64{"UAH": 16.51, "USD": 3.22},
		// Гривня має і річну ставку, і результат за фактом; долар —
		// лише факт, бо його гроші ще молодші за поріг. Саме ця пара і є
		// суть контракту: realized є завжди, xirr — не завжди.
		Realized: map[string]RealizedRow{
			"UAH": {Gain: 1240.55, GainPct: 3.31, MoneyDays: 74.2, MinDays: 30},
			"USD": {Gain: -12.40, GainPct: -0.62, MoneyDays: 18.5, MinDays: 30},
		},
		// Дві валюти, щоб у фікстурі було видно, що строки не змішуються
		// в одну криву: 16% гривні й 3% долара — це різні шкали.
		//
		// VsPortfolioPP тут проставлено руками: різницю рахує buildMarket
		// в internal/api, а цей пакет її лише несе. Знаки різні навмисно —
		// ринок буває і вищим за портфель, і нижчим, і споживач мусить
		// побачити обидва випадки, а не лише приємний.
		MarketYield: []MarketYieldRow{
			{Currency: "UAH", Bucket: "1.5y", Pct: 15.65, Date: "2026-07-14",
				ISIN: "UA4000239040", VsPortfolioPP: 0.72},
			{Currency: "USD", Bucket: "2y", Pct: 3.15, Date: "2026-05-05",
				ISIN: "UA4000239032", VsPortfolioPP: -0.4},
		},
		// Валютне вікно. Три рядки на одну валюту, бо саме так їх бачить
		// споживач, і саме на трьох видно те, заради чого вікон три:
		// перцентиль за рік і за десять різний, і різниця змістовна.
		//
		// VsMedianNative тут теж проставлено руками (як VsPortfolioPP
		// вище): різницю рахує buildFXWindow в internal/api. Знак
		// відʼємний — «сьогодні дорожче за звичне», тобто той випадок,
		// заради якого поле й існує; у рядку без валютної цілі його немає
		// зовсім, і третій рядок показує саме це.
		FXWindow: []FXWindowRow{
			{Currency: "USD", Years: 1, Points: 12, Percentile: 91.67,
				NowRate: 44.1234, MedianRate: 43.2, MinRate: 41.8, MaxRate: 44.5,
				VsMedianNative: -42.19},
			{Currency: "USD", Years: 3, Points: 36, Percentile: 78.5,
				NowRate: 44.1234, MedianRate: 40.15, MinRate: 36.57, MaxRate: 44.5,
				VsMedianNative: -178.4},
			{Currency: "USD", Years: 10, Points: 120, Percentile: 96.25,
				NowRate: 44.1234, MedianRate: 27.9, MinRate: 24.6, MaxRate: 44.5},
		},
		// Нижче — поля, які читає інтеграція. Числа тут не мусять бути
		// звʼязними з портфелем вище (Derive їх не рахує й не звіряє), але
		// мусять бути НЕНУЛЬОВИМИ: нуль у фікстурі не відрізнити від
		// відсутнього поля, і тест «старий сервіс не надсилає» на тому боці
		// перестав би щось означати.
		ReserveUAH: 60_000,
		// GoalsUAH — сума під цілями. Мусить збігатися з in.Goals нижче й з
		// Capital.GoalsUAH: фікстура, у якій ці три числа розходяться,
		// описувала б неможливий портфель.
		GoalsUAH: 30_000,
		// НПФ. npf_uah читає інтеграція (він іде в атрибути капіталу й у
		// резервну суму capital()), npf_contrib_due — новий binary_sensor,
		// тож обидва мусять бути тут і обидва ненульовими. Для bool це
		// означає саме true: false у фікстурі не відрізнити від
		// відсутнього поля.
		NPFUAH:        45_000,
		NPFCostUAH:    40_000,
		NPFContribDue: true,
		NPF: []NPFPositionRow{{
			Name: "Династія", Currency: money.UAH,
			Units: 12_960.55, Nav: 3.472156, NavDate: "2026-06-30",
			CostUAH: 40_000, ValueUAH: 45_000, GainUAH: 5_000,
			// Обидві дохідності заповнені разом навмисно: пара «обіцяли /
			// фактично» і є головним, що показує картка, а yield_basis каже,
			// котре з двох потрапило в real_pct.
			NavReturnPct: 12.40, ExpectedPct: 15, RealPct: 4.85,
			YieldBasis: "зростання ЧВОПА",
			AccessDate: "2051-04-01", ContribDay: 5, ContribDue: true,
			CreditEstUAH: 7_200, Administrator: "ЦПО",
		}},
		IncomeMonthlyNow:    1_240.50,
		AccruedUAH:          812.33,
		PortfolioYieldPct:   14.93,
		FundsYieldPct:       11.20,
		BlendedYieldPct:     14.10,
		BlendedYieldRealPct: 6.85,
		// Три види з чотирьох: НПФ у фікстурі навмисно немає ключа —
		// саме так документ і виглядає, коли виду в портфелі нема.
		KindYieldRealPct: map[string]float64{
			"bonds": 7.10, "funds": 5.62, "deposits": 5.50,
		},
		// Дві задачі з різних ярусів: споживач фікстури мусить побачити і
		// те, що черга впорядкована (now перед watch), і те, що kind буває
		// порожнім — задача про довідник НБУ не про інструмент.
		Tasks: []Task{
			{
				ID: "reserve-fill", Sev: "now", Rank: 10, Kind: "reserve",
				Title:     "Спершу поповнити резерв — 12 400,00 ₴",
				Why:       "Стеля, яку ти сам поставив: до цілі ще 47 600,00 ₴, решта грошей лишається на папери.",
				Action:    "fill-reserve",
				AmountUAH: 12_400,
			},
			{
				ID: "nbu", Sev: "watch", Rank: 20,
				Title: "Довідник НБУ не оновлювався 5 днів",
				Why:   "Ставки й графіки виплат можуть бути несвіжі.",
			},
		},
		NBURefreshedAt: "2026-07-15T06:10:00Z",
		Liquidity: &Liquidity{
			NowUAH: 1_500, In30UAH: 5_637.50, In90UAH: 9_775,
			ReserveUAH: 60_000, GoalsUAH: 30_000,
			LockedUAH: 120_000, UnlockDate: "2027-03-17",
			// Зламне — ОКРЕМО від замкненого, і навмисно не нуль: це різні
			// твердження, а не відтінки одного, і фікстура мусить показати
			// обидва. 40 000 у відкличному вкладі — тіло повернуть, відсотки
			// згорять; 120 000 замкнено намертво.
			BreakableUAH: 40_000,
			// Замкнене в НПФ — частина LockedUAH, а не додаток до нього, і
			// саме тому менша за нього: 120 000 замкнено всього, з них
			// 45 000 у пенсійному, решта у вкладі. Рівність двох чисел
			// приховала б підполе, а сума понад LockedUAH описувала б
			// неможливий портфель.
			LockedNPFUAH: 45_000,
		},
		Independence: &Independence{
			TargetUAH: 30_000, IncomeNowUAH: 1_240.50, TargetFrom: "expenses",
			PlanMonths: 214, PlanDate: "2044-05-15",
			ActualMonths: 262, ActualDate: "2048-05-15",
		},
		// Два рядки з різними вимірами й лише ОДИН із перевищенням:
		// споживачеві треба вміти відрізнити «ліміт заданий і дотриманий»
		// від «перевищено», а з однорідного переліку це не видно.
		Concentration: []ConcentrationRow{
			{Dimension: "isin", Key: "UA4000230114", AmountUAH: 88_246.80,
				SharePct: 34.2, LimitPct: 25, OverUAH: 23_746.80,
				Label: "валютні військові"},
			{Dimension: "year", Key: "2027", AmountUAH: 138_246.80,
				SharePct: 100, LimitPct: 100},
		},
	}
	in := DeriveInput{
		Now: time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC),
		Positions: []domain.Position{
			{ISIN: "UA4000227748", Currency: "UAH", Qty: 50,
				Invested: money.New(4_950_000, money.UAH), Nominal: money.New(5_000_000, money.UAH),
				Maturity: "2027-03-17"},
			{ISIN: "UA4000230114", Currency: "USD", Qty: 2,
				Invested: money.New(199_000, money.USD), Nominal: money.New(200_000, money.USD),
				Maturity: "2027-09-17"},
		},
		// Перший рядок — ВКЛАД, і він тут не для повноти переліку.
		//
		// Вклад ходить у розкладі під синтетичним ключем "deposit:<id>", і
		// саме він може опинитися найближчою виплатою. Доти схема вимагала
		// від next_payment.isin патерн UA[0-9A-Z]{10}, тобто документ у
		// цьому випадку не проходив власної схеми — і жодна фікстура цього
		// не показувала, бо вкладів у них не було. Тепер показує.
		//
		// Заразом це єдине місце, де в фікстурі непорожній label.
		Cashflow: []domain.CashflowItem{
			{Date: "2026-07-18", ISIN: "deposit:1", Type: domain.PayCoupon, Amount: money.New(30_000, money.UAH)},
			{Date: "2026-07-20", ISIN: "UA4000227748", Type: domain.PayCoupon, Amount: money.New(413_750, money.UAH)},
			{Date: "2026-09-16", ISIN: "UA4000227748", Type: domain.PayCoupon, Amount: money.New(413_750, money.UAH)},
			{Date: "2027-03-17", ISIN: "UA4000227748", Type: domain.PayRedemption, Amount: money.New(5_000_000, money.UAH)},
		},
		Ladder: []domain.LadderEntry{
			{Year: 2027, Currency: "UAH", Nominal: 5_000_000},
			{Year: 2027, Currency: "USD", Nominal: 200_000},
		},
		Rates: fx.Rates{"USD": 441234},
		// Capital подається ГОТОВИМ, як і в живому будівнику: цей пакет
		// його більше не збирає. Числа мусять відповідати Positions вище —
		// 50 000 ₴ номіналу гривневих плюс $2 000 × 44.1234 = 88 246.80 ₴.
		//
		// Резерв тут ОБОВʼЯЗКОВО той самий, що й doc.ReserveUAH: капітал за
		// визначенням містить його (див. Capital.TotalUAH), і фікстура, у
		// якій резерв є, а в капіталі його немає, описувала б неможливий
		// портфель. ReserveByCur порожній — матрац гривневий, валютної
		// експозиції не додає.
		// НПФ тут ОБОВʼЯЗКОВО той самий, що й doc.NPFUAH, і з тієї ж
		// причини, що резерв: капітал за визначенням його містить
		// (Capital.TotalUAH). NPFByCur гривневий, тобто валютної експозиції
		// не додає — але сам по собі НПФ у чисельник часток входить, на
		// відміну від сертифікатів.
		Capital: Capital{
			BondsUAH:   50_000 + 88_246.80,
			ReserveUAH: 60_000,
			GoalsUAH:   30_000,
			NPFUAH:     45_000,
			BondsByCur: map[string]float64{money.USD: 88_246.80},
			// Цілі гривневі, як і резерв: валютної експозиції не додають.
			GoalsByCur: map[string]float64{money.UAH: 30_000},
		},
		// Драбина доступу. ReserveLiquidUAH тут МЕНШИЙ за doc.ReserveUAH
		// навмисно: різниця й є резервний вклад, і фікстура, у якій вони
		// рівні, показувала б подушку без драбини — тобто саме той стан,
		// який ці поля й додані розрізняти.
		ReserveLiquidUAH: 35_000,
		ReserveDeposits: []ReserveDeposit{
			// Відкличний: у профілі він дає РОЗМІН, а не діру, і без нього
			// reachable_uah у фікстурі дорівнював би available_uah скрізь —
			// тобто друге число ніколи не перевірялось би.
			{Months: 2, AmountUAH: 25_000, Revocable: true, EarnsUAH: 3_000},
		},
		// Ціль накопичення — доларова з гривневими рухами й дедлайном.
		//
		// Саме ця комбінація заповнює картку цілком: fx_mixed, розрив у двох
		// одиницях, потрібний темп, фактичний темп і прогноз. Гривнева ціль
		// без дати лишила б порожньою більшість полів, а нуль у фікстурі не
		// відрізнити від відсутнього поля — той самий довід, що вгорі.
		//
		// Суми подаються ГОТОВИМИ в обох одиницях: цей пакет курсів не має
		// (переклад робить state_goals.go у будівнику), і $680 тут — це
		// 30 000 ₴ за курсом фікстури 44.1234.
		Goals: []GoalInput{{
			ID: 1, Name: "Авто", Currency: money.USD,
			TargetNative: 20_000, TargetUAH: 882_468,
			CollectedNative: 679.93, CollectedUAH: 30_000,
			ByCurrency: map[string]float64{money.UAH: 30_000},
			Places:     map[string]float64{"готівка": 30_000},
			LastMove:   "2026-07-10", DueDate: "2027-05-01",
			ActualNative: 113.32, ActualUAH: 5_000,
		}},
		MonthDeposited: monthDep,
		MonthTarget:    monthTarget,
		TopN:           5,
	}
	return doc, in
}

func TestDerive(t *testing.T) {
	doc, in := sampleDoc(t)
	if err := Derive(doc, in); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != 1 {
		t.Errorf("schema = %d", doc.Schema)
	}
	// invested: 49500 грн + $1990×44.1234 = 49500 + 87805.57 (банківське) = 137305.57
	if doc.InvestedUAH != 137305.57 {
		t.Errorf("invested_uah = %v", doc.InvestedUAH)
	}
	// Найближча виплата — відсотки ВКЛАДУ, і разом із нею перевіряється
	// підпис: голий "deposit:1" на екрані показувати не можна, тож правило
	// живе в документі, а не в трьох клієнтах.
	if doc.NextPayment == nil || doc.NextPayment.Date != "2026-07-18" ||
		doc.NextPayment.Type != "coupon" || doc.NextPayment.ISIN != "deposit:1" {
		t.Errorf("next_payment: %+v", doc.NextPayment)
	}
	if doc.NextPayment != nil && doc.NextPayment.Label != "вклад" {
		t.Errorf("виплата вкладу без підпису: %+v", doc.NextPayment)
	}
	// В облігації підпис порожній навмисно: її ISIN і є назвою.
	if doc.Calendar[1].ISIN != "UA4000227748" || doc.Calendar[1].Label != "" {
		t.Errorf("облігація дістала зайвий підпис: %+v", doc.Calendar[1])
	}
	// Прогрес рахується від ПОПОВНЕНЬ, а не купівель: план виведений із
	// цілі й означає нові гроші.
	if doc.MonthProgressPct != 90 {
		t.Errorf("progress = %d", doc.MonthProgressPct)
	}
	if doc.MonthDepositedUAH != 4500 {
		t.Errorf("month_deposited = %v", doc.MonthDepositedUAH)
	}
	// 4137.50 купона + 300.00 відсотків вкладу: у «надходженнях місяця»
	// вклад рахується нарівні з папером.
	if doc.MonthIncomingUAH != 4437.50 {
		t.Errorf("month_incoming = %v", doc.MonthIncomingUAH)
	}
	if len(doc.Ladder) != 1 || doc.Ladder[0].UAH != 50000 || doc.Ladder[0].USD != 2000 {
		t.Errorf("ladder: %+v", doc.Ladder)
	}
	// $2 000 × 44.1234 = 88 246.80 ₴ від капіталу 273 246.80 ₴ (номінал
	// 138 246.80 + резерв 60 000 + цілі 30 000 + НПФ 45 000) — тобто ~32.3%.
	// Резерв, цілі й НПФ стоять у ЗНАМЕННИКУ, хоч усі троє гривневі: вони
	// частина капіталу, і саме тому поповнення матраца, відкладання на авто
	// чи внесок у пенсійний зменшують валютну частку, нічого не продаючи.
	//
	// Число переїжджало двічі, і обидва рази не регресією, а тим, заради
	// чого сутність і заводили: з ~44.5% на ~36.3%, коли в капітал увійшов
	// НПФ, і звідти на ~32.3%, коли ввійшли цілі накопичення. Доти будь-яка
	// частка при наявних цілях була завищеною.
	if doc.USDSharePct < 32 || doc.USDSharePct > 33 {
		t.Errorf("usd_share_pct = %v", doc.USDSharePct)
	}
	if len(doc.Calendar) != 4 || doc.Calendar[3].Type != "redemption" {
		t.Errorf("calendar: %+v", doc.Calendar)
	}
	if doc.Settings == nil || *doc.Settings.MonthlyTargetUAH != 5000 {
		t.Errorf("settings: %+v", doc.Settings)
	}
	if doc.XIRRPct["UAH"] != 16.51 {
		t.Errorf("xirr: %+v", doc.XIRRPct)
	}
	// Долар без XIRR, але з результатом: контракт обіцяє саме це.
	if _, ok := doc.XIRRPct["EUR"]; ok {
		t.Errorf("xirr: євро взялось нізвідки: %+v", doc.XIRRPct)
	}
	if r := doc.Realized["USD"]; r.MoneyDays != 18.5 || r.MinDays != 30 {
		t.Errorf("realized[USD]: %+v", r)
	}
}

// TestDeriveNextPaymentSkipsPast — «найближча виплата» не може бути
// вчорашньою.
//
// Сьогодні цю перевірку не видно ні в golden, ні у фікстурі: будівник
// складає календар уже відфільтрованим від сьогоднішнього дня, тож
// минулих виплат у ньому не буває, і мутація «прибрати фільтр» нічого не
// ламає. Тобто вона захисна, а не жива.
//
// Прибирати її через це не варто: Derive — публічна межа пакета, і
// нізвідки не випливає, що календар завжди прийде обрізаним. Але
// незакрита захисна перевірка нічим не краща за її відсутність, тож
// перевіряємо прямо — подаємо календар із минулим у ньому.
func TestDeriveNextPaymentSkipsPast(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	doc := &Doc{}
	err := Derive(doc, DeriveInput{
		Now: now,
		Cashflow: []domain.CashflowItem{
			{Date: "2026-06-01", ISIN: "UA1", Type: domain.PayCoupon, Amount: money.New(100_00, money.UAH)},
			{Date: "2026-08-01", ISIN: "UA2", Type: domain.PayCoupon, Amount: money.New(200_00, money.UAH)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.NextPayment == nil {
		t.Fatal("найближчої виплати немає, хоч майбутня в календарі є")
	}
	if doc.NextPayment.Date != "2026-08-01" {
		t.Errorf("найближча виплата %q — це вчорашній день, а не наступний",
			doc.NextPayment.Date)
	}
	// Календар при цьому лишається ПОВНИМ: він відповідає на інше питання,
	// і викидати з нього минуле означало б зламати звірку.
	if len(doc.Calendar) != 2 {
		t.Errorf("у календарі %d рядків, очікували 2 — минуле з нього не зникає", len(doc.Calendar))
	}
}

// Поповнення резерву: скільки ще відкласти й від чого це пораховано.
//
// САМА АРИФМЕТИКА ТУТ БІЛЬШЕ НЕ ЖИВЕ. Стеля рахується в будівнику
// (api.reserveMonthShare), бо ту саму місячну частку потребує ще й ребаланс,
// а він працює до Derive; сюди числа лише приходять. Тому тест перевіряє те,
// що лишилось за цією фазою: чи потрапляють вони в картку разом із базою — і
// чи мовчить картка, коли ціль зібрана.
//
// База стелі змінилась разом із механізмом: доти це була готівка на
// брокерських рахунках, і 40% від 6,19 ₴ давали пораду «спершу поповнити
// резерв — 2,48 ₴» при розриві в 359 500 ₴. Тепер це гроші МІСЯЦЯ.
//
// sampleDoc задає витрати 30 000 ₴ і ціль 3 місяці, тобто 90 000 ₴, а
// резерву в ньому 60 000 ₴ — розрив рівно 30 000 ₴.
func TestReserveFillCarriesMonthNumbers(t *testing.T) {
	share := 25.0
	doc, in := sampleDoc(t)
	doc.Settings.ReserveFillSharePct = &share
	doc.MonthPlan = &MonthPlan{Month: "2026-08", PlanUAH: 40_000,
		PlanReserveUAH: 40_000, PlanGoalsUAH: 40_000}
	in.ReserveFillMonthUAH, in.ReserveFillNowUAH, in.ReserveMovedUAH = 10_000, 6_000, 4_000
	if err := Derive(doc, in); err != nil {
		t.Fatal(err)
	}
	r := doc.Reserve
	if r == nil {
		t.Fatal("картки резерву немає")
	}
	if r.GapUAH != 30_000 {
		t.Fatalf("розрив = %v, очікували 30000 — змінився sampleDoc", r.GapUAH)
	}
	if r.FillMonthUAH != 10_000 || r.FillNowUAH != 6_000 || r.FillMovedUAH != 4_000 {
		t.Errorf("частка місяця %v, лишилось %v, уже відкладено %v — очікували 10000/6000/4000",
			r.FillMonthUAH, r.FillNowUAH, r.FillMovedUAH)
	}
	// База й стеля перевіряються разом із сумою навмисно: число без «звідки»
	// нема чим перевірити, і саме тому їх у документі кілька.
	if r.FillFromUAH != 40_000 || r.FillSharePct != share {
		t.Errorf("з чого пораховано: від %v за стелею %v%%, очікували від 40000 за 25%% — "+
			"базою мусять бути гроші місяця, а не готівка на рахунках",
			r.FillFromUAH, r.FillSharePct)
	}

	// Ціль зібрана — механізм мовчить цілком, хай би що прийшло з будівника:
	// нулі в документі читались би як «працює і радить нуль».
	doc, in = sampleDoc(t)
	doc.Settings.ReserveFillSharePct = &share
	doc.ReserveUAH = 90_000
	doc.MonthPlan = &MonthPlan{Month: "2026-08", PlanUAH: 40_000,
		PlanReserveUAH: 40_000, PlanGoalsUAH: 40_000}
	in.ReserveFillMonthUAH, in.ReserveFillNowUAH = 10_000, 10_000
	if err := Derive(doc, in); err != nil {
		t.Fatal(err)
	}
	if r := doc.Reserve; r.FillNowUAH != 0 || r.FillMonthUAH != 0 || r.FillFromUAH != 0 {
		t.Errorf("ціль зібрана, а картка радить відкласти %v з %v — механізм мусить мовчати",
			r.FillNowUAH, r.FillFromUAH)
	}
}

// Вимкнений механізм мовчить — і мовчить ОДНАКОВО в усіх трьох випадках.
// Той, хто про поповнення не просив, не побачить жодної зміни.
func TestReserveFillSilentWhenOff(t *testing.T) {
	zero := 0.0
	share := 25.0
	for _, c := range []struct {
		name  string
		share *float64
		free  float64
	}{
		{"стеля не задана", nil, 200_000},
		{"стеля нуль", &zero, 200_000},
		{"рахунки порожні", &share, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			doc, in := sampleDoc(t)
			doc.Settings.ReserveFillSharePct = c.share
			in.Capital.AccountUAH = c.free
			if err := Derive(doc, in); err != nil {
				t.Fatal(err)
			}
			r := doc.Reserve
			if r.FillNowUAH != 0 || r.FillFromUAH != 0 || r.FillSharePct != 0 {
				t.Errorf("механізм заговорив, хоч його не вмикали: %v ₴ від %v за %v%%",
					r.FillNowUAH, r.FillFromUAH, r.FillSharePct)
			}
			// Решта картки при цьому лишається на місці: вимкнена стеля — це
			// не вимкнений резерв.
			if r.GapUAH != 30_000 {
				t.Errorf("розрив зник разом зі стелею: %v", r.GapUAH)
			}
		})
	}
}

// Ціль зібрана — сказати нема чого. Перебір резерву не є браком, і це вже
// так працює для GapUAH; тут перевіряється, що поповнення від нього не
// відірвалось.
func TestReserveFillZeroWhenTargetReached(t *testing.T) {
	doc, in := sampleDoc(t)
	share := 40.0
	doc.Settings.ReserveFillSharePct = &share
	in.Capital.AccountUAH = 200_000
	doc.ReserveUAH, in.Capital.ReserveUAH = 120_000, 120_000 // ціль 90 000
	if err := Derive(doc, in); err != nil {
		t.Fatal(err)
	}
	if r := doc.Reserve; r.GapUAH != 0 || r.FillNowUAH != 0 {
		t.Errorf("резерву 120 000 при цілі 90 000, а застосунок радить докласти %v ₴ (розрив %v)",
			r.FillNowUAH, r.GapUAH)
	}
}

func TestDeriveEmptyPortfolio(t *testing.T) {
	doc := &Doc{}
	err := Derive(doc, DeriveInput{Now: time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if doc.InvestedUAH != 0 || doc.NextPayment != nil || len(doc.Calendar) != 0 {
		t.Errorf("порожній портфель: %+v", doc)
	}
}

// TestFixtureUpToDate гарантує, що фікстура контракту в contract/fixtures
// зібрана саме цим кодом: інтеграція (репо ha-oddinvest) тестується проти неї.
func TestFixtureUpToDate(t *testing.T) {
	doc, in := sampleDoc(t)
	if err := Derive(doc, in); err != nil {
		t.Fatal(err)
	}
	got, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../contract/fixtures/basic.json")
	if err != nil {
		t.Fatalf("фікстура відсутня: %v (перегенеруй: go test ./internal/state -run TestFixtureUpToDate -update)", err)
	}
	if string(got)+"\n" != string(want) {
		t.Errorf("фікстура застаріла — перегенеруй з -update\nмаємо:\n%s", got)
	}
}

// Стеля цілей міряється від ДОЗВОЛЕНОЇ їм частини плану (0041).
//
// Читачів у цього правила двоє — документ і прохід маршруту вперед, — і
// обидва беруть те саме число з MonthPlan. Тест тримає перший; другий
// перевіряється в api.TestRoutePlanLegCappedByAllowedPlan.
//
// Незалежність від подушки перевіряється прямо в тому ж рядку: PlanUAH
// лишається великим, а звужується лише те число, яке належить цілям.
// Спільне число на два кошики мусило б вибрати одну з двох неправд.
func TestGoalsFillUsesAllowedBase(t *testing.T) {
	share := 50.0
	doc, in := sampleDoc(t)
	doc.Settings.GoalsFillSharePct = &share
	// План дає 40 000, але цілям із них дозволено лише 10 000: решта —
	// дохід, позначений «не в накопичення».
	doc.MonthPlan = &MonthPlan{Month: "2026-08", PlanUAH: 40_000,
		PlanReserveUAH: 40_000, PlanGoalsUAH: 10_000}
	if err := Derive(doc, in); err != nil {
		t.Fatal(err)
	}
	if len(doc.Goals) != 1 {
		t.Fatalf("цілей %d, чекали 1", len(doc.Goals))
	}
	// 50% від дозволених 10 000, а не від усього плану (там було б 20 000).
	if got := doc.Goals[0].FillNowUAH; got != 5_000 {
		t.Errorf("цілі належить %v, чекали 5000 — 50%% від ДОЗВОЛЕНИХ 10 000", got)
	}
}
