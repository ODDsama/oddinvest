// Package state будує документ oddinvest/state — контракт між Go-сервісом
// та інтеграцією Home Assistant (репо ha-oddinvest).
//
// Правила еволюції: тільки додавання полів (backward compatible);
// зміна семантики існуючого поля = інкремент Schema. JSON Schema і
// фікстури — у каталозі contract/ цього репозиторію; CI інтеграції
// ганяє свої парсери проти цих фікстур.
//
// Суми в документі — у МАЖОРНИХ одиницях (грн, долари) як number з
// двома знаками: це шар відображення для HA-шаблонів. Уся точна
// математика залишається в мінорних одиницях до цієї межі.
package state

import (
	"encoding/json"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
)

const SchemaVersion = 1

type Doc struct {
	Schema      int    `json:"schema"`
	GeneratedAt string `json:"generated_at"` // RFC3339

	InvestedUAH   float64 `json:"invested_uah"`   // вартість входу залишків, грн-екв.
	NominalUAHEq  float64 `json:"nominal_uah_eq"` // номінал портфеля, грн-екв.
	USDSharePct   float64 `json:"usd_share_pct"`  // частка USD-паперів за номіналом (грн-екв.)
	EURSharePct   float64 `json:"eur_share_pct"`  // частка EUR-паперів за номіналом (грн-екв.)
	UninvestedUAH float64 `json:"uninvested_uah"` // надійшло і не перевкладено, грн-екв.

	// Грошовий рахунок (гаманець). AccountUAH — сумарний баланс у грн-екв.
	// (для «Разом» і плитки). Accounts — баланси по валютах (нативно).
	// ReinvestMinUAH — ціна найдешевшого паперу (грн-екв.); ReinvestMin —
	// найдешевший папір по кожній валюті (нативно) для по-валютного CTA.
	AccountUAH     float64            `json:"account_uah"`
	ReinvestMinUAH float64            `json:"reinvest_min_uah"`
	Accounts       map[string]float64 `json:"accounts"`
	ReinvestMin    map[string]float64 `json:"reinvest_min"`
	// Brokers — баланси в розрізі (брокер → валюта → сума). Рахунки
	// роздільні, тож «чи вистачає на папір» рахується саме тут, а
	// Accounts лишається зведенням по валютах для портфельних показників.
	Brokers map[string]map[string]float64 `json:"brokers,omitempty"`
	// InvestedByBroker — вкладено (вартість входу залишків, грн-екв.) по
	// кожному брокеру. Довідкова розбивка для «Портфеля».
	InvestedByBroker map[string]float64 `json:"invested_by_broker,omitempty"`

	// LadderUAH — номінал, що повертається щороку, у грн-екв. (для стовпчиків
	// драбини). Income12m — очікуваний дохід (купони+погашення) по місяцях
	// на рік наперед, грн-екв.
	LadderUAH []YearAmount  `json:"ladder_uah,omitempty"`
	Income12m []MonthAmount `json:"income_12m,omitempty"`

	// MonthInvestedUAH — куплено паперів цього місяця (перекладання
	// грошей з рахунку в папери). MonthDepositedUAH — НОВІ гроші, внесені
	// цього місяця. Прогрес рахується від поповнень: план виведений із
	// цілі й означає «скільки нових грошей треба вносити», а купівля за
	// накопичені купони до цілі не додає нічого.
	MonthInvestedUAH  float64 `json:"month_invested_uah"`
	MonthDepositedUAH float64 `json:"month_deposited_uah"`
	MonthTargetUAH    float64 `json:"month_target_uah"`
	MonthProgressPct  int     `json:"month_progress_pct"`
	MonthIncomingUAH float64 `json:"month_incoming_uah"` // купони+погашення в поточному місяці

	NextPayment *NextPayment `json:"next_payment,omitempty"`

	Ladder      []LadderRow  `json:"ladder"`
	TopPayments []PaymentRow `json:"top_payments"` // найближчі N виплат
	Calendar    []PaymentRow `json:"calendar"`     // повний горизонт майбутніх виплат

	// XIRRPct — річна дохідність портфеля по валютах, % (v1.0+,
	// адитивне поле). Відсутня валюта = XIRR нерахований (мало потоків).
	XIRRPct map[string]float64 `json:"xirr,omitempty"`

	// PortfolioYieldPct — очікувана дохідність за придбаними паперами:
	// дохідність до погашення (YTM) від фактично сплаченої ціни, зважена
	// вкладеними грішми. Використовується проєкціями як ставка за
	// замовчуванням (замість ручного вводу). 0 = паперів немає.
	PortfolioYieldPct float64 `json:"portfolio_yield_pct,omitempty"`

	// PortfolioYield — та сама дохідність, але окремо по кожній валюті
	// (нативно, без конвертації): YTM паперів цієї валюти, зважений
	// вкладеними грішми. Відсутня валюта = паперів немає.
	PortfolioYield map[string]float64 `json:"portfolio_yield,omitempty"`

	// Projection — прогноз капіталу помісячною симуляцією реальних потоків
	// (купони/погашення наявних паперів + внески, реінвест під дохідність).
	// ProjectionRatePct — ставка реінвесту, що використана. Goal* —
	// прогноз і потрібний внесок під ціль (якщо задано ціль і дату).
	Projection        []ProjectionRow `json:"projection,omitempty"`
	ProjectionRatePct float64         `json:"projection_rate_pct,omitempty"`
	// Forecast — віяло прогнозів на дедлайн: скільки буде на цю дату за
	// трьох наборів допущень. Ціль — одна сума-орієнтир, з якою вони
	// порівнюються.
	Forecast *Forecast `json:"forecast,omitempty"`

	// Rebalance — підказка виходу на цільові валютні частки (по валютах,
	// де задано ціль). RateRisk — процентний ризик портфеля (дюрація).
	Rebalance []RebalanceRow `json:"rebalance,omitempty"`
	RateRisk  *RateRisk      `json:"rate_risk,omitempty"`

	// AccruedUAH — накопичений купонний дохід на сьогодні, грн-екв.:
	// зароблено, але ще не виплачено. У проєкції НЕ додається (там майбутні
	// купони враховані повністю). NBURefreshedAt — коли востаннє успішно
	// оновлювався довідник НБУ (ISO), щоб ловити тиху несвіжість даних.
	AccruedUAH     float64 `json:"accrued_uah,omitempty"`
	NBURefreshedAt string  `json:"nbu_refreshed_at,omitempty"`

	// ActualMonthlyUAH — фактичний середній темп поповнень, грн/міс
	// (нові гроші, не покупки). ActualMonths — за скільки місяців історії
	// він порахований. 0 = історії ще замало (<60 днів).
	ActualMonthlyUAH float64 `json:"actual_monthly_uah,omitempty"`
	ActualMonths     int     `json:"actual_months,omitempty"`

	// Settings — сирі налаштування сервіса (v0.3+, адитивне поле).
	// Потрібні HA для number/date-сутностей: значення приходять сюди
	// MQTT-пушем, зміни йдуть у PUT /api/settings.
	Settings *SettingsDoc `json:"settings,omitempty"`
}

type SettingsDoc struct {
	MonthlyTargetUAH  *float64 `json:"monthly_target_uah,omitempty"`
	USDTargetSharePct *float64 `json:"usd_target_share_pct,omitempty"`
	EURTargetSharePct *float64 `json:"eur_target_share_pct,omitempty"`
	// v Phase 3: проєкції/цілі
	AssumedRatePct *float64 `json:"assumed_rate_pct,omitempty"` // очікувана річна дохідність, % (fallback до XIRR)
	GoalAmountUAH  *float64 `json:"goal_amount_uah,omitempty"`  // застаріле: мігрує в «реалістичну»
	GoalDate       string   `json:"goal_date,omitempty"`        // дедлайн (ISO), опційно
	// UAHDevaluationPct — очікуване річне знецінення гривні до твердої
	// валюти, %. Базове значення, від якого сценарії розходяться.
	UAHDevaluationPct *float64 `json:"uah_devaluation_pct,omitempty"`
	// TerminalRatePct — довгострокова гривнева ставка ОВДП, до якої
	// сповзає сьогоднішня; RateGlideYears — за скільки років.
	TerminalRatePct *float64 `json:"terminal_rate_pct,omitempty"`
	RateGlideYears  *float64 `json:"rate_glide_years,omitempty"`
	// Три цілі за рівнем амбіції. Дату досягнення рахує застосунок за
	// поточним темпом — вона результат, а не введення.
	GoalPessimisticUAH *float64 `json:"goal_pessimistic_uah,omitempty"`
	GoalRealisticUAH   *float64 `json:"goal_realistic_uah,omitempty"`
	GoalOptimisticUAH  *float64 `json:"goal_optimistic_uah,omitempty"`
	// Channels — канали купівлі через кому (mono, inzhur…). Форма покупки
	// показує їх у випадайці разом із тими, що вже зустрічались у лотах.
	Channels string `json:"channels,omitempty"`
	// ReinvestRank — критерій ранжування помічника:
	// plan (за замовчуванням) | rate | short | ladder.
	ReinvestRank string `json:"reinvest_rank,omitempty"`
}

// RebalanceRow — що треба зробити, щоб вийти на цільову частку валюти.
// DeficitUAH — скільки грн-екв бракує до цілі; BondCost* — найдешевший
// папір цієї валюти; ConvertUAH — скільки гривні сконвертувати, щоб його
// купити; MinPortfolioUAH — розмір портфеля, за якого один такий папір
// уже вписується в цільову частку (Feasible=false, поки не доріс).
type RebalanceRow struct {
	Currency        string  `json:"currency"`
	TargetPct       float64 `json:"target_pct"`
	CurrentPct      float64 `json:"current_pct"`
	DeficitUAH      float64 `json:"deficit_uah"`
	DeficitNative   float64 `json:"deficit_native"`
	CashNative      float64 `json:"cash_native"`
	BondCostNative  float64 `json:"bond_cost_native"`
	BondCostUAH     float64 `json:"bond_cost_uah"`
	CanBuy          int64   `json:"can_buy"`
	ConvertUAH      float64 `json:"convert_uah"`
	MinPortfolioUAH float64 `json:"min_portfolio_uah"`
	Feasible        bool    `json:"feasible"`
}

// RateRisk — процентний ризик: дюрація Маколея (років), модифікована
// дюрація і сценарії зміни вартості при зсуві ставок.
type RateRisk struct {
	DurationYears float64            `json:"duration_years"`
	ModifiedDur   float64            `json:"modified_dur"`
	PVUAH         float64            `json:"pv_uah"`
	ByCurrency    map[string]float64 `json:"by_currency,omitempty"`
	Scenarios     []RiskScenario     `json:"scenarios,omitempty"`
}

type RiskScenario struct {
	DeltaPP   float64 `json:"delta_pp"`
	ChangePct float64 `json:"change_pct"`
	ChangeUAH float64 `json:"change_uah"`
}

// Forecast — «скільки в мене буде на дедлайн» за трьох сценаріїв.
//
// Раніше тут були три ЦІЛІ з різними сумами. Вони лежали на одній і тій
// самій кривій прогнозу — відрізнялись лише числа, які ввів користувач,
// — тож слова «песимістично/оптимістично» нічого не моделювали, а
// «реалістична» взагалі виводилась із прогнозу на дедлайн і тому завжди
// досягалась рівно в дедлайн. Тепер навпаки: ціль одна, а сценарії
// відрізняються ДОПУЩЕННЯМИ (темп поповнень і ставка реінвесту).
type Forecast struct {
	Date   string `json:"date"`   // дедлайн, на який рахуємо
	Months int    `json:"months"` // скільки місяців до нього
	// GoalAmount — сума-орієнтир; 0 = ціль не задана, показуємо самі суми.
	GoalAmount float64 `json:"goal_amount,omitempty"`
	// ContribPlan — місячний внесок плану. Він же і є «скільки треба
	// відкладати, щоб устигнути до дедлайну»: план виводиться з цілі,
	// тож окреме поле під потрібну суму лише дублювало б це число.
	ContribPlan float64 `json:"contrib_plan,omitempty"`
	// Rate0USD — сьогоднішній курс, ₴ за долар. UI ділить на нього, щоб
	// показати ті самі числа в доларах: це та сама величина в іншій
	// одиниці, а не окремий розрахунок.
	Rate0USD float64 `json:"rate0_usd,omitempty"`
	// GlideYears — за скільки років ставка проходить шлях від сьогоднішньої
	// до довгострокової.
	GlideYears float64       `json:"glide_years,omitempty"`
	Rows       []ForecastRow `json:"rows"`
}

// ForecastRow — один сценарій: допущення і що з них виходить.
// GoalMonths: -1 = ціль уже досягнута, 0 = не досягається в межах горизонту.
type ForecastRow struct {
	Key   string `json:"key"` // optimistic | realistic | pessimistic
	Label string `json:"label"`
	// Amount — капітал на дедлайн у гривні СЬОГОДНІШНЬОЇ купівельної
	// спроможності; саме він порівнюється з ціллю. AmountNominal — те
	// саме в гривні того дня, тобто скільки буде намальовано на рахунку.
	Amount        float64 `json:"amount"`
	AmountNominal float64 `json:"amount_nominal,omitempty"`
	// RequiredMonthly — скільки треба вносити щомісяця, щоб дійти до цілі
	// САМЕ ЗА ЦИХ допущень. Головне число сценарію: платіж під ціль один,
	// але ринок вирішує, наскільки він посильний. Для рядка «За фактом»
	// порожнє — там головне число це сам фактичний темп.
	RequiredMonthly float64 `json:"required_monthly,omitempty"`
	RatePct        float64 `json:"rate_pct"`                   // сьогоднішня дохідність гривневої частини
	RateTerminalPct float64 `json:"rate_terminal_pct,omitempty"` // куди вона сповзає
	ContribMonthly float64 `json:"contrib_monthly"` // припущений внесок, ₴/міс
	DevaluationPct float64 `json:"devaluation_pct"` // припущене знецінення гривні, %/рік
	GoalPct        float64 `json:"goal_pct,omitempty"`
	GoalMonths     int     `json:"goal_months,omitempty"`
	GoalDate       string  `json:"goal_date,omitempty"`
	// ByCurrency — розклад по валютних рукавах: під що саме росте кожна
	// валюта і скільки грошей у неї спрямовується.
	ByCurrency []SleeveRow `json:"by_currency,omitempty"`
}

// SleeveRow — один валютний рукав сценарію. Amount — у НАТИВНІЙ валюті.
type SleeveRow struct {
	Currency        string  `json:"currency"`
	RatePct         float64 `json:"rate_pct"`
	RateTerminalPct float64 `json:"rate_terminal_pct,omitempty"`
	ContribMonthly float64 `json:"contrib_monthly"` // ₴/міс, що йдуть у цю валюту
	Amount         float64 `json:"amount"`
}

type YearAmount struct {
	Year int     `json:"year"`
	UAH  float64 `json:"uah"`
}

type MonthAmount struct {
	Month  string  `json:"month"` // "2026-07"
	Amount float64 `json:"amount"`
}

type ProjectionRow struct {
	Years        int     `json:"years"`
	Contributed  float64 `json:"contributed"`   // внесено без %, грн-екв.
	WithReinvest float64 `json:"with_reinvest"` // з реінвестом за ПЛАНОМ, грн-екв.
	// WithReinvestActual — те саме, але за фактичним темпом поповнень.
	WithReinvestActual float64 `json:"with_reinvest_actual,omitempty"`
}

type NextPayment struct {
	Date     string  `json:"date"` // ISO
	ISIN     string  `json:"isin"`
	Type     string  `json:"type"` // coupon | redemption | early
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type LadderRow struct {
	Year int     `json:"year"`
	UAH  float64 `json:"uah"`
	USD  float64 `json:"usd"` // номінал у доларах (не еквівалент)
	EUR  float64 `json:"eur"` // номінал у євро (не еквівалент)
}

type PaymentRow struct {
	Date     string  `json:"date"`
	ISIN     string  `json:"isin"`
	Type     string  `json:"type"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// Input — все, що потрібно для побудови документа (домен уже порахований).
type Input struct {
	Now               time.Time
	Positions         []domain.Position
	Cashflow          []domain.CashflowItem // майбутні виплати від сьогодні
	Ladder            []domain.LadderEntry
	Rates             fx.Rates
	MonthInvestedUAH  *money.Money
	MonthDepositedUAH *money.Money
	MonthTargetUAH    *money.Money
	UninvestedUAH    *money.Money
	AccountUAH       *money.Money
	ReinvestMinUAH   *money.Money
	Accounts         map[string]float64
	Brokers          map[string]map[string]float64
	InvestedByBroker map[string]float64
	LadderUAH        []YearAmount
	Income12m        []MonthAmount
	ReinvestMinByCur map[string]float64
	TopN              int
	Settings          *SettingsDoc
	XIRRPct             map[string]float64
	PortfolioYieldPct   float64
	PortfolioYield      map[string]float64
	Projection          []ProjectionRow
	ProjectionRatePct   float64
	Forecast            *Forecast
	Rebalance           []RebalanceRow
	RateRisk            *RateRisk
	AccruedUAH          float64
	NBURefreshedAt      string
	ActualMonthlyUAH    float64
	ActualMonths        int
}

func payTypeStr(t domain.PayType) string {
	switch t {
	case domain.PayCoupon:
		return "coupon"
	case domain.PayRedemption:
		return "redemption"
	case domain.PayEarly:
		return "early"
	}
	return "unknown"
}

func major(m *money.Money) float64 {
	if m == nil {
		return 0
	}
	return float64(m.Amount()) / 100.0
}

// Build збирає документ стану.
func Build(in Input) (*Doc, error) {
	doc := &Doc{
		Schema:      SchemaVersion,
		GeneratedAt: in.Now.UTC().Format(time.RFC3339),
		Ladder:      []LadderRow{},
		TopPayments: []PaymentRow{},
		Calendar:    []PaymentRow{},
	}

	var investedUAH, nominalUAH, nominalUSD, nominalEUR int64
	for _, p := range in.Positions {
		inv, err := fx.ToUAH(p.Invested, in.Rates)
		if err != nil {
			return nil, err
		}
		nom, err := fx.ToUAH(p.Nominal, in.Rates)
		if err != nil {
			return nil, err
		}
		investedUAH += inv.Amount()
		nominalUAH += nom.Amount()
		switch p.Currency {
		case money.USD:
			nominalUSD += nom.Amount()
		case money.EUR:
			nominalEUR += nom.Amount()
		}
	}
	doc.InvestedUAH = float64(investedUAH) / 100
	doc.NominalUAHEq = float64(nominalUAH) / 100
	// частки рахуються від усього капіталу: папери за номіналом + кеш на
	// рахунку (гривня). Так вільний кеш «розводить» валютні частки.
	accountMinor := int64(0)
	if in.AccountUAH != nil {
		accountMinor = in.AccountUAH.Amount()
	}
	capital := nominalUAH + accountMinor
	if capital > 0 {
		doc.USDSharePct = float64(nominalUSD) * 100 / float64(capital)
		doc.EURSharePct = float64(nominalEUR) * 100 / float64(capital)
	}

	doc.MonthInvestedUAH = major(in.MonthInvestedUAH)
	doc.MonthDepositedUAH = major(in.MonthDepositedUAH)
	doc.MonthTargetUAH = major(in.MonthTargetUAH)
	doc.MonthProgressPct = domain.ProgressPct(in.MonthDepositedUAH, in.MonthTargetUAH)
	doc.UninvestedUAH = major(in.UninvestedUAH)
	doc.AccountUAH = major(in.AccountUAH)
	doc.ReinvestMinUAH = major(in.ReinvestMinUAH)
	doc.Accounts = in.Accounts
	doc.Brokers = in.Brokers
	doc.InvestedByBroker = in.InvestedByBroker
	doc.LadderUAH = in.LadderUAH
	doc.Income12m = in.Income12m
	doc.ReinvestMin = in.ReinvestMinByCur
	if doc.Accounts == nil {
		doc.Accounts = map[string]float64{}
	}
	if doc.ReinvestMin == nil {
		doc.ReinvestMin = map[string]float64{}
	}
	doc.Settings = in.Settings
	doc.XIRRPct = in.XIRRPct
	doc.PortfolioYieldPct = in.PortfolioYieldPct
	doc.PortfolioYield = in.PortfolioYield
	doc.Projection = in.Projection
	doc.ProjectionRatePct = in.ProjectionRatePct
	doc.Forecast = in.Forecast
	doc.Rebalance = in.Rebalance
	doc.RateRisk = in.RateRisk
	doc.AccruedUAH = in.AccruedUAH
	doc.NBURefreshedAt = in.NBURefreshedAt
	doc.ActualMonthlyUAH = in.ActualMonthlyUAH
	doc.ActualMonths = in.ActualMonths

	nowDate := domain.NewDate(in.Now)
	var monthIncoming int64
	for _, cf := range in.Cashflow {
		if cf.Date.Year() == in.Now.Year() && cf.Date.Month() == in.Now.Month() {
			uahAmt, err := fx.ToUAH(cf.Amount, in.Rates)
			if err != nil {
				return nil, err
			}
			monthIncoming += uahAmt.Amount()
		}
	}
	doc.MonthIncomingUAH = float64(monthIncoming) / 100

	for _, cf := range in.Cashflow {
		if cf.Date.Before(nowDate) {
			continue
		}
		doc.NextPayment = &NextPayment{
			Date:     string(cf.Date),
			ISIN:     cf.ISIN,
			Type:     payTypeStr(cf.Type),
			Amount:   major(cf.Amount),
			Currency: cf.Amount.Currency().Code,
		}
		break
	}

	byYear := map[int]*LadderRow{}
	years := []int{}
	for _, le := range in.Ladder {
		row, ok := byYear[le.Year]
		if !ok {
			row = &LadderRow{Year: le.Year}
			byYear[le.Year] = row
			years = append(years, le.Year)
		}
		switch le.Currency {
		case money.UAH:
			row.UAH = float64(le.Nominal) / 100
		case money.USD:
			row.USD = float64(le.Nominal) / 100
		case money.EUR:
			row.EUR = float64(le.Nominal) / 100
		}
	}
	for _, y := range years {
		doc.Ladder = append(doc.Ladder, *byYear[y])
	}

	topN := in.TopN
	if topN <= 0 {
		topN = 5
	}
	for i, cf := range in.Cashflow {
		row := PaymentRow{
			Date:     string(cf.Date),
			ISIN:     cf.ISIN,
			Type:     payTypeStr(cf.Type),
			Amount:   major(cf.Amount),
			Currency: cf.Amount.Currency().Code,
		}
		doc.Calendar = append(doc.Calendar, row)
		if i < topN {
			doc.TopPayments = append(doc.TopPayments, row)
		}
	}
	return doc, nil
}

func (d *Doc) JSON() ([]byte, error) { return json.Marshal(d) }
