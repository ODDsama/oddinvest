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
// Поділ на «жорсткі» й «ковтані» збережено рівно такий, як був. Фонди,
// вклади, поповнення й довідники зʼявились у схемі пізніше за решту, і на
// старій БД їх могло не бути; валити через це весь стан означало б
// показати порожній екран замість портфеля, який чудово рахується без них.
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

	src.deval = s.devaluation(ctx)
	src.settings = s.loadSettings(ctx)
	src.depositMin = s.depositMinMinorByCur(ctx)

	src.fundOps, _ = s.st.ListFundOps(ctx)               //nolint:errcheck // свідомо: старій БД фондів могло не бути
	src.fundPrices, _ = s.st.ListFundPrices(ctx)         //nolint:errcheck // те саме: таблиця зʼявилась пізніше за схему, і порожня історія цін — звичайний стан
	src.termDeposits, _ = s.st.ListTermDeposits(ctx)     //nolint:errcheck // свідомо, як і фонди: вклади зʼявились пізніше за схему
	src.deposits, _ = s.st.ListDeposits(ctx)             //nolint:errcheck // свідомо: порожній журнал поповнень — не привід валити стан
	src.brokers, _ = s.st.ListBrokers(ctx)               //nolint:errcheck // свідомо: список для випадайок, не джерело істини
	src.nbuAt, _ = s.st.GetSetting(ctx, nbuRefreshedKey) //nolint:errcheck // свідомо: порожньо = довідник ще не оновлювався
	src.auctions, _ = s.st.AuctionLatestByBucket(ctx)    //nolint:errcheck // свідомо: на свіжій БД аукціонів ще немає, і портфель має малюватись
	src.fxHistory = s.fxHistorySince(ctx, today)
	src.planFlows, _ = s.st.ListPlanFlows(ctx)       //nolint:errcheck // свідомо: порожній план — звичайний стан, не привід валити документ
	src.planActions, _ = s.st.ListPlanActions(ctx)   //nolint:errcheck // те саме
	src.planReceipts, _ = s.st.ListPlanReceipts(ctx) //nolint:errcheck // те саме: невідмічений план — звичайний стан
	src.planBuys, _ = s.st.ListPlanBuys(ctx)         //nolint:errcheck // те саме: порожній план купівель — звичайний стан
	src.npfAccounts, _ = s.st.ListNPFAccounts(ctx)   //nolint:errcheck // свідомо, як фонди й вклади: НПФ зʼявився пізніше за схему
	src.npfOps, _ = s.st.ListNPFOps(ctx)             //nolint:errcheck // те саме
	src.npfNav, _ = s.st.ListNPFNav(ctx)             //nolint:errcheck // те саме: історія ЧВОПА може бути порожня, і це звичайний стан

	src.fundRefs = map[string]store.Fund{}
	if refs, ferr := s.st.ListFunds(ctx); ferr == nil {
		for _, f := range refs {
			src.fundRefs[f.Name] = f
		}
	}
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
// Помилку ковтаємо, як і в auctions поруч: на свіжій БД історії ще немає
// (беклог іде окремою горутиною при старті), і валити через це весь
// документ означало б показати порожній екран замість портфеля, який
// чудово рахується без перцентиля.
//
// Скільки саме років брати, вирішує НЕ цей файл: список вікон живе в
// state_fxwindow.go, і два місця з незалежними числами розійшлись би на
// першій же правці — вікно «10 років» мовчки читало б п'ять.
func (s *Server) fxHistorySince(ctx context.Context, today domain.Date) map[string][]store.RatePoint {
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
		if err != nil || len(pts) == 0 {
			continue
		}
		out[cur] = pts
	}
	return out
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
