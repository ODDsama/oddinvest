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
	fundOps      []domain.FundOp
	fundRefs     map[string]store.Fund
	termDeposits []domain.Deposit
	reserveOps   []store.ReserveOp

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
	src.termDeposits, _ = s.st.ListTermDeposits(ctx)     //nolint:errcheck // свідомо, як і фонди: вклади зʼявились пізніше за схему
	src.deposits, _ = s.st.ListDeposits(ctx)             //nolint:errcheck // свідомо: порожній журнал поповнень — не привід валити стан
	src.brokers, _ = s.st.ListBrokers(ctx)               //nolint:errcheck // свідомо: список для випадайок, не джерело істини
	src.nbuAt, _ = s.st.GetSetting(ctx, nbuRefreshedKey) //nolint:errcheck // свідомо: порожньо = довідник ще не оновлювався

	src.fundRefs = map[string]store.Fund{}
	if refs, ferr := s.st.ListFunds(ctx); ferr == nil {
		for _, f := range refs {
			src.fundRefs[f.Name] = f
		}
	}
	return src, nil
}
