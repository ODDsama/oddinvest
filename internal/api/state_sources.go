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
	goals        []store.Goal
	goalOps      []store.GoalOp

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
	// planReceipts — відмітки фактичних надходжень (0027). Так само сирі:
	// індекс (потік, місяць) будує newPlanMarks, а заміщення планової суми
	// робить те саме ядро, що й розгортання, — щоб означення надходження
	// лишалось одне.
	planReceipts []store.PlanReceipt
	// planBuys — план купівель (0033). Сирі рядки: розділення на «купую
	// зараз» і «купую потім» робить state_plan_buys.go, бо для цього
	// потрібне сьогодні, а sources його не знає (див. шапку файла).
	planBuys []store.PlanBuy

	// auctions — останнє розміщення Мінфіну по кожній парі (валюта,
	// строк). Єдине, що приходить сюди із ЗОВНІШНЬОГО світу, а не з
	// портфеля користувача.
	auctions []store.AuctionPoint

	// fxHistory — історія курсів НБУ по валютах, не старіша за найдовше
	// вікно. Так само зовнішній орієнтир, як і auctions поруч, і так само
	// читається РІВНО ПО РАЗУ на документ: перцентиль потрібен і картці
	// біля конвертації, і атрибутам сенсора в HA.
	fxHistory map[string][]store.RatePoint

	// Рух грошей: поповнення/зняття, їхній розріз по брокер-валюті,
	// нетто конверсій і статуси виплат.
	deposits []store.Deposit
	depByBC  map[store.BrokerCur]int64
	convBC   map[store.BrokerCur]int64
	statuses map[string]string

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
func (s *Server) loadSources(ctx context.Context, today domain.Date) (*sources, error) {
	src := &sources{}
	var err error

	if src.lots, src.sales, src.bonds, src.pays, err = s.portfolio(ctx); err != nil {
		return nil, err
	}
	if src.rates, err = s.rates(ctx); err != nil {
		return nil, err
	}
	if src.reserveOps, err = s.st.ListReserveOps(ctx); err != nil {
		return nil, err
	}
	if src.goals, err = s.st.ListGoals(ctx); err != nil {
		return nil, err
	}
	if src.goalOps, err = s.st.ListGoalOps(ctx); err != nil {
		return nil, err
	}
	if src.statuses, err = s.st.PaymentStatuses(ctx); err != nil {
		return nil, err
	}
	if src.depByBC, err = s.st.DepositsByBrokerCurrency(ctx); err != nil {
		return nil, err
	}
	if src.convBC, err = s.st.ConversionsNetByBroker(ctx); err != nil {
		return nil, err
	}
	if src.minNominal, err = s.st.MinNominalByCurrency(ctx); err != nil {
		return nil, err
	}
	if src.avgRate, err = s.st.AvgRateByCurrency(ctx, today); err != nil {
		return nil, err
	}
	if src.fundOps, err = s.st.ListFundOps(ctx); err != nil {
		return nil, err
	}
	if src.fundPrices, err = s.st.ListFundPrices(ctx); err != nil {
		return nil, err
	}
	if src.termDeposits, err = s.st.ListTermDeposits(ctx); err != nil {
		return nil, err
	}
	if src.deposits, err = s.st.ListDeposits(ctx); err != nil {
		return nil, err
	}
	if src.brokers, err = s.st.ListBrokers(ctx); err != nil {
		return nil, err
	}
	if src.auctions, err = s.st.AuctionLatestByBucket(ctx); err != nil {
		return nil, err
	}
	if src.planFlows, err = s.st.ListPlanFlows(ctx); err != nil {
		return nil, err
	}
	if src.planActions, err = s.st.ListPlanActions(ctx); err != nil {
		return nil, err
	}
	if src.planReceipts, err = s.st.ListPlanReceipts(ctx); err != nil {
		return nil, err
	}
	if src.planBuys, err = s.st.ListPlanBuys(ctx); err != nil {
		return nil, err
	}
	if src.npfAccounts, err = s.st.ListNPFAccounts(ctx); err != nil {
		return nil, err
	}
	if src.npfOps, err = s.st.ListNPFOps(ctx); err != nil {
		return nil, err
	}
	if src.npfNav, err = s.st.ListNPFNav(ctx); err != nil {
		return nil, err
	}
	if src.fxHistory, err = s.fxHistorySince(ctx, today); err != nil {
		return nil, err
	}

	// Налаштування — ОДНІЄЮ вибіркою на весь документ, а не запитом на
	// ключ (їх сорок один). Звідси ж беруть своє мінімуми вкладів і час
	// оновлення довідника: три різні читання тієї самої таблиці в одній
	// збірці — це три способи отримати три різні відповіді.
	rawSettings, err := s.st.AllSettings(ctx)
	if err != nil {
		return nil, err
	}
	// Порожньо тут законне: довідник ще не оновлювався.
	src.nbuAt = rawSettings[nbuRefreshedKey]

	refs, err := s.st.ListFunds(ctx)
	if err != nil {
		return nil, err
	}
	src.fundRefs = make(map[string]store.Fund, len(refs))
	for _, f := range refs {
		src.fundRefs[f.Name] = f
	}

	src.deval = s.devaluation(ctx)
	src.settings = loadSettings(rawSettings)
	// Витрати — у гривню одразу тут, бо саме тут уперше зустрічаються
	// налаштування й курс. Кожен, хто читає src.settings далі, дістає
	// MonthlyExpensesUAH уже гривневим — довід при resolveExpensesUAH.
	resolveExpensesUAH(src.settings, src.rates)
	src.depositMin = depositMinMinorByCur(rawSettings)
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
func (s *Server) fxHistorySince(ctx context.Context, today domain.Date) (map[string][]store.RatePoint, error) {
	longest := 0
	for _, y := range fxWindowYears {
		if y > longest {
			longest = y
		}
	}
	from := today.AddMonths(-12 * longest)
	out := make(map[string][]store.RatePoint, len(fxHistoryCurrencies))
	for _, cur := range fxHistoryCurrencies {
		pts, err := s.st.RatesSince(ctx, cur, from)
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
