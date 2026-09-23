// Джерела документа стану — усе, що buildState читає зі сховища.
//
// Перша фаза розбиття buildState. Доти читання були розсипані по всій
// функції: lots на початку, statuses на 330-му рядку, налаштування на
// 950-му, середній курс на 1215-му. Наслідок передбачуваний — ListDeposits
// викликався ДВІЧІ, за пʼятсот рядків один від одного, і жодне з двох
// місць не знало про інше.
//
// Тепер правило просте: якщо фаза щось читає зі сховища, вона бере це
// звідси, а не з s.st. Дописати сюди поле дешево; додати друге читання
// того самого — помітно.
//
// Межа проведена по «сирих фактах»: sources НІЧОГО не рахує, не
// конвертує й не знає про сьогоднішній день, окрім місць, де сам запит
// його вимагає (AvgRateByCurrency). Усе, що з цих фактів виводиться,
// лишається у фазах — інакше це був би просто buildState під іншою назвою.
package api

import (
	"context"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/settings"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

// sources — сирі факти зі сховища, прочитані РІВНО ПО РАЗУ на документ.
type sources struct {
	// Портфель ОВДП: лоти, продажі, довідник паперів і графік виплат.
	lots  []domain.Lot
	sales []domain.Sale
	bonds map[string]domain.Bond
	pays  []domain.Payment

	// Курси й річне знецінення гривні. Знецінення читається раз на весь
	// документ навмисно: його бачать дохідності позицій, зведені
	// дохідності, прогноз і сценарії, і якби кожен читав сам, вони могли б
	// розійтися між собою в межах однієї відповіді.
	rates fx.Rates
	deval float64
	// cpi — виміряна інфляція, %/рік; нуль означає «ряду ще замало», і
	// тоді все, що на ній стоїть, мовчить, а не показує нулі.
	//
	// НУЛЬ І ПРИ ВАЛЮТІ ЗВІТНОСТІ ≠ ГРИВНІ. ІСЦ — лінійка гривневих цін:
	// цілі «у майбутніх грошах» і друга лінійка розкладу ставки індексують
	// саме гривню, і в доларовому документі їм немає місця. Гасити ряд тут,
	// а не в кожному споживачі, дешевше й безпечніше: споживачі вже вміють
	// мовчати при нулі (свіжа база), і другої гілки їм не треба.
	cpi float64
	// report — ефективна валюта звітності (reportCurrency): вона вибирає
	// лінійку дохідності й валюту зведеного результату; суми будівник і
	// далі кладе в гривні, їх перекладає презентер.
	report string

	// Решта інструментів: фонди (операції + довідник), вклади, резерв.
	fundOps  []domain.FundOp
	fundRefs map[string]store.Fund
	// fundPrices — позначки ціни сертифіката (0034). Сирими, поруч із
	// операціями: звести їх в «останню відому ціну» вміє лише
	// domain.FundPositions, і робити це двічі (тут і у фазі) означало б
	// дати двом місцям розійтись у тому, яке джерело важить більше. Те
	// саме, що з npfNav.
	fundPrices   []domain.FundPrice
	termDeposits []domain.Deposit
	reserveOps   []store.ReserveOp
	reserveLoans []store.ReserveLoan
	goals        []store.Goal
	goalOps      []store.GoalOp

	// Борги (0045): картки, розстрочки, журнал рухів і звірки з банком.
	//
	// Звірки лежать сирими, а не зведеними в стан картки: звести їх із
	// рухами вміє лише domain.CardState, і робити це двічі (тут і у фазі)
	// означало б дати двом місцям розійтись у тому, що вважати сьогоднішнім
	// балансом. Те саме, що з npfNav і fundPrices.
	debts     []domain.Debt
	debtOps   []domain.DebtOp
	debtMarks []domain.DebtMark

	// НПФ (0028): рахунки, внески й вклеєні руками точки ЧВОПА.
	//
	// Точки лежать сирими, а не зведеними в криву: обʼєднати їх із ЧВОПА,
	// виведеними з внесків, умію лише domain.NPFNavPoints, і робити це двічі
	// (тут і в фазі) означало б дати двом місцям розійтись у тому, яке
	// джерело важить більше.
	npfAccounts []domain.NPFAccount
	npfOps      []domain.NPFOp
	npfNav      []domain.NPFNav

	// План (фаза 9): джерела доходу й точкові дії. Сирі рядки — розгортання
	// в помісячні вектори робить sleeveFactory (state_projection.go).
	planFlows   []store.PlanFlow
	planActions []store.PlanAction
	// planFunds — планована купівля накопичувального фонду. Читанню зі
	// сховища не підлягає: це ЛИШЕ гіпотеза (див. planFundBuy у
	// state_builder.go), тож loadSources її не наповнює — вона
	// зʼявляється рівно в блоці домішування.
	planFunds []planFundBuy
	// planReceipts — відмітки фактичних надходжень (0027). Так само сирі:
	// індекс (потік, місяць) будує newPlanMarks, а заміщення планової суми
	// робить те саме ядро, що й розгортання, — щоб означення надходження
	// лишалось одне.
	planReceipts []store.PlanReceipt
	// planBuys — план купівель (0033). Сирі рядки: розділення на «купую
	// зараз» і «купую потім» робить state_plan_buys.go, бо для цього
	// потрібне сьогодні, а sources його не знає (див. шапку файла).
	planBuys []store.PlanBuy
	// planExpenses — вирішені разові витрати (0056). Сирими: «чи тисне вона
	// в жовтні» вміє сказати лише domain.PlanExpense.PressMonth, і другого
	// означення цього не зʼявляється — читачів у нього троє (план місяця,
	// вихід із ліміту, таблиця горизонту), і розійшлися б вони тихо.
	planExpenses []domain.PlanExpense

	// auctions — останнє розміщення Мінфіну по кожній парі (валюта,
	// строк). Єдине, що приходить сюди із ЗОВНІШНЬОГО світу, а не з
	// портфеля користувача.
	auctions []store.AuctionPoint

	// fxHistory — історія курсів НБУ по валютах, не старіша за найдовше
	// вікно. Так само зовнішній орієнтир, як і auctions поруч, і так само
	// читається РІВНО ПО РАЗУ на документ: перцентиль потрібен і картці
	// біля конвертації, і атрибутам сенсора в HA.
	fxHistory map[string][]store.RatePoint

	// capitalAgo — добовий знімок місячної давнини (останній на ≥30 днів
	// тому), з яким порівнюється капітал у CapitalDelta30. Один рядок, а
	// не вся історія: документ збирається й на кожен POST /api/whatif, і
	// обхід знімків там платити нема за що. nil — знімка ще немає.
	capitalAgo *store.Snapshot

	// Рух грошей: поповнення/зняття, конвертації і статуси виплат. Обидва
	// списки З ДАТАМИ, а не підсумками по парах: гаманець (state_cash.go)
	// веде журнал подій, і з нього виводиться не лише баланс, а й з якого
	// дня гроші лежать.
	deposits    []store.Deposit
	conversions []store.Conversion
	statuses    map[string]string
	// ratesAsOf — дата НАЙСТАРІШОГО з останніх курсів USD і EUR: вік
	// гривневих еквівалентів визначає той курс, що відстав. Порожньо, коли
	// курсів немає зовсім (свіжа база) — тоді й старіти нічому.
	ratesAsOf domain.Date

	// Пороги й курси-запаснки: найдешевший папір у валюті, мінімум вкладу,
	// середній курс купівлі валюти.
	minNominal map[string]int64
	depositMin map[string]int64
	avgRate    map[string]float64

	// Налаштування, список брокерів і час останнього оновлення довідника.
	settings *state.SettingsDoc
	brokers  []store.Broker
	nbuAt    string
}

// loadSources читає все й одразу.
//
// Про порядок помилок. Доти «жорсткі» читання були розкидані по функції,
// і якщо падали два, назовні йшло те, яке трапилось раніше ПО КОДУ. Тепер
// перемагає те, яке раніше в цьому списку. Спостережувано це нічого не
// змінює — buildState в обох випадках повертає помилку й не будує
// документа, — але сказати про це варто, бо текст помилки може бути інший.
//
// ЧОМУ ТУТ БІЛЬШЕ НЕМАЄ «М'ЯКИХ» ЧИТАНЬ. Сімнадцять читань нижче раніше
// ковтали помилку з доводом «на старій БД цієї таблиці могло не бути:
// фонди, вклади, НПФ і цілі зʼявились у схемі пізніше за решту». Довід
// був справедливий рівно доти, доки міграції були необовʼязкові. Тепер
// migrate() виконується БЕЗУМОВНО в store.Open, а його помилка валить
// Open, і main виходить з кодом 1 — тобто на будь-якій відкритій базі всі
// ці таблиці існують, і випадку, заради якого ковталось, не буває.
//
// Ковталось натомість інше: справжня відмова читання. Вона віддавала
// порожній зріз, порожній зріз ставав нулем у документі — а цей документ
// іде в MQTT і ЩОДНЯ ЛЯГАЄ В ДОБОВИЙ ЗНІМОК. Тобто збій сховища
// матеріалізувався як «того дня фондів (НПФ, цілей) не було», назавжди й
// у правдоподібному вигляді: на кривій за півроку таку діру вже не
// відрізнити від правди. Порожня таблиця й зламане читання мусять
// говорити різне, і саме тому тепер друге — помилка.
func (e *engine) loadSources(ctx context.Context, today domain.Date) (*sources, error) {
	src := &sources{}
	var err error

	if src.lots, src.sales, src.bonds, src.pays, err = e.portfolio(ctx); err != nil {
		return nil, err
	}
	if src.rates, err = e.rates(ctx); err != nil {
		return nil, err
	}
	if src.reserveOps, err = e.st.ListReserveOps(ctx); err != nil {
		return nil, err
	}
	if src.reserveLoans, err = e.st.ListReserveLoans(ctx); err != nil {
		return nil, err
	}
	if src.goals, err = e.st.ListGoals(ctx); err != nil {
		return nil, err
	}
	if src.goalOps, err = e.st.ListGoalOps(ctx); err != nil {
		return nil, err
	}
	if src.debts, err = e.st.ListDebts(ctx); err != nil {
		return nil, err
	}
	if src.debtOps, err = e.st.ListDebtOps(ctx); err != nil {
		return nil, err
	}
	if src.debtMarks, err = e.st.ListDebtMarks(ctx); err != nil {
		return nil, err
	}
	if src.statuses, err = e.st.PaymentStatuses(ctx); err != nil {
		return nil, err
	}
	for _, code := range []string{money.USD, money.EUR} {
		d, derr := e.st.LatestRateDate(ctx, code)
		if derr != nil {
			return nil, derr
		}
		if d != "" && (src.ratesAsOf == "" || d.Before(src.ratesAsOf)) {
			src.ratesAsOf = d
		}
	}
	if src.conversions, err = e.st.ListConversions(ctx); err != nil {
		return nil, err
	}
	if src.minNominal, err = e.st.MinNominalByCurrency(ctx); err != nil {
		return nil, err
	}
	if src.avgRate, err = e.st.AvgRateByCurrency(ctx, today); err != nil {
		return nil, err
	}
	if src.fundOps, err = e.st.ListFundOps(ctx); err != nil {
		return nil, err
	}
	if src.fundPrices, err = e.st.ListFundPrices(ctx); err != nil {
		return nil, err
	}
	if src.termDeposits, err = e.st.ListTermDeposits(ctx); err != nil {
		return nil, err
	}
	if src.deposits, err = e.st.ListDeposits(ctx); err != nil {
		return nil, err
	}
	if src.brokers, err = e.st.ListBrokers(ctx); err != nil {
		return nil, err
	}
	if src.auctions, err = e.st.AuctionLatestByBucket(ctx); err != nil {
		return nil, err
	}
	if src.planFlows, err = e.st.ListPlanFlows(ctx); err != nil {
		return nil, err
	}
	if src.planActions, err = e.st.ListPlanActions(ctx); err != nil {
		return nil, err
	}
	if src.planReceipts, err = e.st.ListPlanReceipts(ctx); err != nil {
		return nil, err
	}
	if src.planBuys, err = e.st.ListPlanBuys(ctx); err != nil {
		return nil, err
	}
	if src.planExpenses, err = e.st.ListPlanExpenses(ctx); err != nil {
		return nil, err
	}
	if src.npfAccounts, err = e.st.ListNPFAccounts(ctx); err != nil {
		return nil, err
	}
	if src.npfOps, err = e.st.ListNPFOps(ctx); err != nil {
		return nil, err
	}
	if src.npfNav, err = e.st.ListNPFNav(ctx); err != nil {
		return nil, err
	}
	if src.fxHistory, err = e.fxHistorySince(ctx, today); err != nil {
		return nil, err
	}
	if src.capitalAgo, err = e.snapshotAgo(ctx, today); err != nil {
		return nil, err
	}

	// Налаштування — ОДНІЄЮ вибіркою на весь документ, а не запитом на
	// ключ (їх сорок один). Звідси ж беруть своє мінімуми вкладів: два
	// різні читання тієї самої таблиці в одній збірці — це два способи
	// отримати дві різні відповіді.
	rawSettings, err := e.st.AllSettings(ctx)
	if err != nil {
		return nil, err
	}
	// Час оновлення довідника — з app_state, а не з налаштувань: довідник
	// НБУ спільний для всіх портфелів (0054). Порожньо тут законне:
	// довідник ще не оновлювався.
	if src.nbuAt, err = e.st.GetAppState(ctx, nbuRefreshedKey); err != nil {
		return nil, err
	}

	refs, err := e.st.ListFunds(ctx)
	if err != nil {
		return nil, err
	}
	src.fundRefs = make(map[string]store.Fund, len(refs))
	for _, f := range refs {
		src.fundRefs[f.Name] = f
	}

	src.deval = e.devaluation(ctx)
	src.cpi, _ = e.inflation(ctx)
	if src.report, err = e.reportCurrency(ctx); err != nil {
		return nil, err
	}
	if src.report != money.UAH {
		src.cpi = 0 // довід при полі
	}
	src.settings = settings.Load(rawSettings)
	// Витрати — у гривню одразу тут, бо саме тут уперше зустрічаються
	// налаштування й курс. Кожен, хто читає src.settings далі, дістає
	// MonthlyExpensesUAH уже гривневим — довід при settings.ResolveExpensesUAH.
	settings.ResolveExpensesUAH(src.settings, src.rates)
	src.depositMin = settings.DepositMinMinorByCur(rawSettings)
	return src, nil
}

// fxHistoryCurrencies — валюти, для яких тримаємо історію курсів.
//
// Той самий порядок і той самий набір, що у валютному вимірі ребалансу
// (state_rebalance.go): гривня власного курсу не має, а третьої валюти в
// застосунку немає ніде — ані в довіднику, ані в джобі, що курси тягне
// (jobs.RefreshAll).
var fxHistoryCurrencies = []string{money.USD, money.EUR}

// fxHistorySince — історія курсів за найдовше з вікон.
//
// Помилку читання ТЕПЕР повертаємо (довід — у шапці loadSources): вона
// означає зламане сховище, а не «історії ще немає». Порожню історію
// свідомо лишаємо законною — беклог іде окремою горутиною при старті, і
// на свіжій базі точок справді нема; такий код просто не кладе валюту в
// мапу, і споживач читає це як «перцентиля не буде», а не як помилку.
//
// Скільки саме років брати, вирішує НЕ цей файл: список вікон живе в
// state_fxwindow.go, і два місця з незалежними числами розійшлись би на
// першій же правці — вікно «10 років» мовчки читало б п'ять.
func (e *engine) fxHistorySince(ctx context.Context, today domain.Date) (map[string][]store.RatePoint, error) {
	longest := 0
	for _, y := range fxWindowYears {
		if y > longest {
			longest = y
		}
	}
	from := today.AddMonths(-12 * longest)
	out := make(map[string][]store.RatePoint, len(fxHistoryCurrencies))
	for _, cur := range fxHistoryCurrencies {
		pts, err := e.st.RatesSince(ctx, cur, from)
		if err != nil {
			return nil, err
		}
		if len(pts) == 0 {
			continue
		}
		out[cur] = pts
	}
	return out, nil
}

// payoutDays — день виплати кожного фонду в тому вигляді, якого чекає
// NewHoldings. З журналу операцій його не вивести: одна виплата ритму не
// задає, а дві поспіль можуть розійтись через вихідні.
func (s *sources) payoutDays() map[string]int64 {
	out := make(map[string]int64, len(s.fundRefs))
	for name, ref := range s.fundRefs {
		out[name] = ref.PayoutDay
	}
	return out
}

// arrived — предикат domain.Arrived на позначках портфеля: для викликачів,
// що вантажать лоти через s.portfolio, а не через loadSources. Без нього
// папір, погашення якого вже позначене «Отримано», у сам день погашення
// лишався б позицією на одній сторінці й зникав на іншій.
func (e *engine) arrived(ctx context.Context, today domain.Date) (func(string, domain.Date) bool, error) {
	statuses, err := e.st.PaymentStatuses(ctx)
	if err != nil {
		return nil, err
	}
	return domain.Arrived(statuses, today), nil
}
