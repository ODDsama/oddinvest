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
		return &SettingsDoc{
			MonthlyTargetUAH: &tgt, USDTargetSharePct: &u,
			MonthlyExpensesUAH: &exp, ReserveTargetMonths: &months,
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
		// Нижче — поля, які читає інтеграція. Числа тут не мусять бути
		// звʼязними з портфелем вище (Derive їх не рахує й не звіряє), але
		// мусять бути НЕНУЛЬОВИМИ: нуль у фікстурі не відрізнити від
		// відсутнього поля, і тест «старий сервіс не надсилає» на тому боці
		// перестав би щось означати.
		ReserveUAH: 60_000,
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
		NBURefreshedAt:      "2026-07-15T06:10:00Z",
		Liquidity: &Liquidity{
			NowUAH: 1_500, In30UAH: 5_637.50, In90UAH: 9_775,
			ReserveUAH: 60_000, LockedUAH: 120_000, UnlockDate: "2027-03-17",
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
			NPFUAH:     45_000,
			BondsByCur: map[string]float64{money.USD: 88_246.80},
		},
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
	// $2 000 × 44.1234 = 88 246.80 ₴ від капіталу 243 246.80 ₴ (номінал
	// 138 246.80 + резерв 60 000 + НПФ 45 000) — тобто ~36.3%. Резерв і НПФ
	// стоять у ЗНАМЕННИКУ, хоч обидва гривневі: вони частина капіталу, і
	// саме тому поповнення матраца чи внесок у пенсійний зменшують валютну
	// частку, нічого не продаючи.
	//
	// Число тут переїхало з ~44.5% рівно тоді, коли НПФ увійшов у
	// Capital.TotalUAH, і це не регресія, а те, заради чого він туди
	// заводився: доти будь-яка частка, порахована при наявному пенсійному
	// рахунку, була заниженою.
	if doc.USDSharePct < 36 || doc.USDSharePct > 37 {
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

// Поповнення резерву: скільки з вільних грошей відкласти зараз.
//
// sampleDoc задає витрати 30 000 ₴ і ціль 3 місяці, тобто 90 000 ₴, а
// резерву в ньому 60 000 ₴ — розрив рівно 30 000 ₴. Вільних грошей у ньому
// немає (Capital.AccountUAH не заданий), і саме тому фікстура контракту
// цих полів не містить: механізм мовчить, доки його не ввімкнули. Тести
// нижче додають обидва числа руками.
func TestReserveFillIsCappedByShareAndGap(t *testing.T) {
	for _, c := range []struct {
		name              string
		share, free       float64
		wantNow, wantFrom float64
	}{
		// Стеля менша за розрив — віддаємо стелю, решта лишається на папери.
		{"стеля обмежує", 25, 40_000, 10_000, 40_000},
		// Розрив менший за стелю — віддаємо розрив: радити відкласти понад
		// ціль означало б завести другу ціль поруч із заданою в місяцях.
		{"розрив обмежує", 25, 200_000, 30_000, 200_000},
	} {
		t.Run(c.name, func(t *testing.T) {
			doc, in := sampleDoc(t)
			doc.Settings.ReserveFillSharePct = &c.share
			in.Capital.AccountUAH = c.free
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
			if r.FillNowUAH != c.wantNow {
				t.Errorf("відкласти зараз = %v, очікували %v", r.FillNowUAH, c.wantNow)
			}
			// Обидва «звідки» перевіряються разом із сумою навмисно: число без
			// них нема чим перевірити, і саме тому їх у документі троє.
			if r.FillFromUAH != c.wantFrom || r.FillSharePct != c.share {
				t.Errorf("з чого пораховано: від %v за стелею %v%%, очікували від %v за %v%%",
					r.FillFromUAH, r.FillSharePct, c.wantFrom, c.share)
			}
		})
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
