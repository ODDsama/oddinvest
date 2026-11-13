// Прогноз капіталу за нинішнім темпом — на будь-який місяць до тридцяти
// років, і сам темп помісячно.
//
// МОДЕЛЬ ТА САМА, що в рядку «За фактом» таблиці projection: рукави без
// плану надходжень (sleeveFactory.buildPlanFree), внесок — виміряний темп,
// ринок — реалістичний. Власної арифметики тут немає навмисно: точки на
// 12, 36, 60 і 120 місяцях мусять збігатися з projection[].with_reinvest_
// actual до копійки, і тест саме це й тримає. Нове тут одне — горизонт:
// доти він був прибитий до чотирьох років із переліку або до дедлайну мети.
//
// ЧОМУ ОКРЕМИЙ МАРШРУТ, А НЕ ПОЛЕ ДОКУМЕНТА СТАНУ. Довід той самий, що при
// /api/fx-shock: документ публікується в MQTT і щодня лягає в добовий
// знімок, а тут 361 точка по сім сум. Контракт, фікстури й інтеграція HA
// не чіпаються зовсім.
//
// ЧОМУ ВСІ ТОЧКИ ОДРАЗУ, А НЕ ?months=N. Повзунок у браузері мусить
// відповідати на кожен рух, а рахувати там не можна (арифметика живе в
// одному місці). Тож сервер віддає відповідь на КОЖЕН місяць, разом із
// «наросло», і браузер лише вибирає точку. Одна симуляція на серію, не на
// точку — ProjectSleevesSeries проходить місяці раз.
//
// ВІДОМА ВАДА, спільна з рядком «За фактом» і названа в state_month.go:
// темп рахує всі зовнішні гроші, зокрема ті, що пішли в резерв і цілі, а
// модель кладе їх у папери. Лікувати треба обидва місця разом.
package engine

import (
	"context"
	"math"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
)

// OutlookMonths — горизонт прогнозу: тридцять років. Довше не має сенсу
// не через модель, а через припущення: ставка після сповзання й знецінення
// — константи, і на шістдесяти роках крива показувала б лише їх.
const OutlookMonths = 360

// paceBarMonths — скільки календарних місяців темпу показувати стовпчиками.
// Удвічі довше за вікно самого темпу (183 дні): видно і те, з чого число
// складене, і те, що було до того.
const paceBarMonths = 12

// OutlookPoint — капітал на кінець одного місяця горизонту.
type OutlookPoint struct {
	Month int         `json:"month"`
	Date  domain.Date `json:"date"`
	// Amount — за нинішнім темпом, у сьогоднішніх гривнях. Нуль у всіх
	// точках, коли темпу немає (OutlookDoc.Why).
	Amount state.Money `json:"amount"`
	// Nominal — те саме гривнею СВОГО місяця: скільки буде на рахунку. У
	// доларовому документі обнуляється: майбутня гривня, поділена на
	// сьогоднішній курс, — число, якого не існує.
	Nominal state.Money `json:"nominal" money:"uah-only"`
	// Contributed — старт плюс внески темпом, без жодного відсотка.
	Contributed state.Money `json:"contributed"`
	// Gain — що наросло понад внесене. Різниця вже перекладених сусідів,
	// щоб у доларовому вигляді три числа сходились на екрані.
	Gain state.Money `json:"gain" money:"diff=amount,contributed"`
	// Low / High — той самий темп за гіршого й кращого ринку (розкид як у
	// віялі прогнозу до дедлайну).
	Low  state.Money `json:"low"`
	High state.Money `json:"high"`
	// Plan — за планом: планові надходження плюс внесок під мету. Колонка
	// «За планом» колишньої таблиці проєкцій.
	Plan state.Money `json:"plan"`
	// Income / IncomePlan — скільки капітал цього місяця приноситиме
	// ЩОМІСЯЦЯ, за темпом і за планом: реальний дохід, який можна забирати,
	// не проїдаючи тіло. Ті самі числа, що projection[].income_monthly_
	// actual / income_monthly, але на кожен місяць; перший місяць, де
	// Income сягає цільового доходу, — це independence.actual_months.
	Income     state.Money `json:"income"`
	IncomePlan state.Money `json:"income_plan"`
}

// PaceBar — нетто зовнішніх грошей за один календарний місяць.
type PaceBar struct {
	Month  string      `json:"month"` // РРРР-ММ
	Amount state.Money `json:"amount"`
	// InWindow — місяць зачіпає вікно, з якого виміряно темп.
	InWindow bool `json:"in_window"`
}

// OutlookPace — темп і те, з чого він складений.
type OutlookPace struct {
	Monthly    state.Money `json:"monthly"`
	Months     int         `json:"months"`
	WindowDays int         `json:"window_days"`
	Bars       []PaceBar   `json:"bars"`
}

// OutlookDoc — відповідь GET /api/outlook.
type OutlookDoc struct {
	Pace OutlookPace `json:"pace"`
	// Start — із чого стартує модель (projectionStart): капітал без
	// резерву й цілей накопичення.
	Start  state.Money    `json:"start"`
	Points []OutlookPoint `json:"points"`
	// Ставка гривневого рукава сьогодні й куди вона сповзе; знецінення.
	// Для підпису під графіком: числа в точках уже містять їх.
	RatePct         float64 `json:"rate_pct"`
	RateTerminalPct float64 `json:"rate_terminal_pct"`
	GlideYears      float64 `json:"glide_years"`
	DevaluationPct  float64 `json:"devaluation_pct"`
	// Sleeves — на чому стоїть прогноз, по валютах: скільки в рукаві вже
	// лежить (нативно), яка ставка реінвесту сьогодні й звідки вона, куди
	// сповзе, і скільки з місячного внеску йде саме сюди. Ті самі рукави,
	// якими пораховано Points, а не окремий переказ припущень — інакше
	// підпис під графіком міг би назвати одне, а крива рости під інше.
	Sleeves []state.SleeveRow `json:"sleeves"`
	// RateSpreadPP / DevalSpreadPP — ширина коридору Low…High: на скільки
	// п.п. гірший і кращий ринок відрізняються ставкою й знеціненням.
	RateSpreadPP  float64 `json:"rate_spread_pp"`
	DevalSpreadPP float64 `json:"deval_spread_pp"`
	// Scope — що входить у суми: OutlookPortfolio чи OutlookAll.
	Scope string `json:"scope"`
	// ByCurrency / ByOrigin / ByKind — головна лінія (Amount, а без темпу
	// Plan), розкладена смугами трьома способами. У кожному розрізі смуги
	// в сумі дають ту саму лінію в кожній точці.
	ByCurrency []OutlookBand `json:"by_currency"`
	ByOrigin   []OutlookBand `json:"by_origin"`
	ByKind     []OutlookBand `json:"by_kind"`
	// Strategy — цільові частки видів, за якими в ByKind розкладено НОВІ
	// гроші, нормовані до 100. Порожньо — часток не задано, і нові гроші
	// стоять у ByKind окремою смугою.
	Strategy []OutlookShare `json:"strategy"`
	// Why непорожнє ⇒ темпу немає, і в точках є лише Plan.
	Why string `json:"why,omitempty"`
}

// OutlookBand — одна смуга розрізу: значення на кожен місяць горизонту, у
// сьогоднішніх гривнях.
//
// Ключі: валюта (UAH/USD/EUR); held / new; bonds / deposits / funds / npf
// (і new, коли стратегії за видами немає); outside — резерв і цілі
// накопичення в OutlookAll.
type OutlookBand struct {
	Key    string        `json:"key"`
	Values []state.Money `json:"values"`
	// Native — те саме у валюті рукава; лише в розрізі за валютами.
	Native []state.Money `json:"native,omitempty" money:"native"`
}

// OutlookShare — частка виду в нових грошах, %.
type OutlookShare struct {
	Key string  `json:"key"`
	Pct float64 `json:"pct"`
}

// Що входить у суми прогнозу.
//
// OutlookPortfolio — лише те, що інвестується: старт без резерву й цілей
// накопичення (projectionStart). OutlookAll додає їх зверху рівною смугою —
// «як сьогодні»: модель не знає ні їхнього поповнення, ні дня, коли гроші
// під ціль підуть, тож не вигадує ні того, ні того. Потрібне, щоб старт
// кривої збігався з плиткою «Капітал».
const (
	OutlookPortfolio = "portfolio"
	OutlookAll       = "all"
)

// OutlookScopes — значення, які можна питати.
var OutlookScopes = []string{OutlookPortfolio, OutlookAll}

// BuildOutlook — прогноз на сьогоднішньому портфелі.
func (e *Engine) BuildOutlook(ctx context.Context, now time.Time, scope string) (*OutlookDoc, error) {
	tap, err := e.realProjectionTap(ctx, now)
	if err != nil {
		return nil, err
	}
	return buildOutlook(tap, OutlookMonths, scope), nil
}

// outsideMoney — резерв і цілі накопичення: гривнева частина й валютна,
// обидві в грн-екв. сьогодні.
type outsideMoney struct{ uah, fx float64 }

func outsideOf(c state.Capital) outsideMoney {
	total := c.ReserveUAH.Major() + c.GoalsUAH.Major()
	uah := c.ReserveByCur[money.UAH].Major() + c.GoalsByCur[money.UAH].Major()
	// Без розкладу по валютах (старі дані, тести) — усе вважається гривнею:
	// це обережніший бік, бо гривня в прогнозі знецінюється.
	if len(c.ReserveByCur)+len(c.GoalsByCur) == 0 {
		uah = total
	}
	uah = math.Max(0, math.Min(uah, total))
	return outsideMoney{uah: uah, fx: total - uah}
}

// at — ці гроші через m місяців, якщо їх не чіпати: у сьогоднішніх
// гривнях і номіналом. Та сама угода, що в Sleeve.toUAH: гривня втрачає
// знеціненням, валюта тримає купівельну спроможність.
func (o outsideMoney) at(devalPct float64, m int) (today, nominal float64) {
	g := math.Pow(1+domain.MonthlyRate(devalPct), float64(m))
	return o.uah/g + o.fx, o.uah + o.fx*g
}

// buildOutlook — чиста частина: вхід проєкції → точки й стовпчики.
func buildOutlook(tap projectionTap, months int, scope string) *OutlookDoc {
	in := tap.in
	factory := newSleeveFactory(in)
	rateSpreadPP, devalSpreadPP := projectionSpreads(in.Settings)
	p0 := projectionStart(in.Capital)
	var outside outsideMoney
	if scope == OutlookAll {
		outside = outsideOf(in.Capital)
	} else {
		scope = OutlookPortfolio
	}

	out := &OutlookDoc{
		Pace: OutlookPace{
			Monthly:    state.Major(in.ActualMonthly, money.UAH),
			WindowDays: actualWindowDays,
			Bars:       paceBars(tap.moves, in.Rates, in.Today),
		},
		Start:          state.Major(p0+outside.uah+outside.fx, money.UAH),
		Scope:          scope,
		GlideYears:     factory.glideYears,
		DevaluationPct: domain.Round2(in.Deval),
	}

	planSleeves := factory.build(tap.contribM, 0)
	// Ставка — гривневого рукава, як і в рядках прогнозу до дедлайну: під
	// неї росте основна валюта портфеля. Від внеску вона не залежить, тож
	// береться з рукавів плану, які є завжди.
	for _, s := range planSleeves {
		if s.Currency == money.UAH {
			out.RatePct = domain.Round2(s.RatePct)
			out.RateTerminalPct = domain.Round2(s.RateTerminalPct)
		}
	}
	plan := domain.ProjectSleevesSeries(planSleeves, in.Deval, months, 1)
	out.RateSpreadPP, out.DevalSpreadPP = rateSpreadPP, devalSpreadPP
	// Рукави, якими малюється головна лінія: за темпом, а без темпу — плану.
	leadSleeves := planSleeves
	var actual, low, high []domain.SeriesPoint
	if in.ActualMonthly > 0 {
		leadSleeves = factory.buildPlanFree(in.ActualMonthly, 0)
		actual = domain.ProjectSleevesSeries(leadSleeves, in.Deval, months, 1)
		low = domain.ProjectSleevesSeries(factory.buildPlanFree(in.ActualMonthly, -rateSpreadPP),
			in.Deval+devalSpreadPP, months, 1)
		high = domain.ProjectSleevesSeries(factory.buildPlanFree(in.ActualMonthly, rateSpreadPP),
			math.Max(0, in.Deval-devalSpreadPP), months, 1)
	} else {
		out.Why = "Темпу ще немає: за останні пів року нових грошей у портфель не додалось."
	}

	// Скільки в кожному рукаві лежить сьогодні — нульовий крок тієї самої
	// симуляції, нативно.
	have := domain.ProjectSleeves(leadSleeves, in.Deval, 0).ByCurrency
	out.Sleeves = make([]state.SleeveRow, 0, len(leadSleeves))
	for _, s := range leadSleeves {
		_, src, srcDate := factory.startRate(s.Currency)
		out.Sleeves = append(out.Sleeves, state.SleeveRow{
			Currency: s.Currency, RatePct: domain.Round2(s.RatePct),
			RateTerminalPct: domain.Round2(s.RateTerminalPct),
			RateSource:      src, RateDate: string(srcDate),
			ContribMonthly: state.Major(s.ContribUAH, money.UAH),
			Amount:         state.Major(have[s.Currency], s.Currency),
		})
	}

	// Довжина — з серії плану: вона є завжди, а на порожньому портфелі
	// (жодного рукава) складається з нулів, і графік чесно лежить на осі.
	//
	// extra — резерв і цілі цього місяця (нуль у OutlookPortfolio). Кожен
	// сценарій бере своє знецінення: гірший ринок знецінює й гривню під
	// матрацом, а не лише ту, що в паперах.
	lowDeval, highDeval := in.Deval+devalSpreadPP, math.Max(0, in.Deval-devalSpreadPP)
	uah := func(s []domain.SeriesPoint, i int, devalPct float64) state.Money {
		if i >= len(s) {
			return state.Major(0, money.UAH)
		}
		extra, _ := outside.at(devalPct, s[i].Month)
		return state.Major(domain.Round2(s[i].UAH+extra), money.UAH)
	}
	for i, p := range plan {
		extra, extraNominal := outside.at(in.Deval, p.Month)
		pt := OutlookPoint{
			Month: p.Month, Date: in.Today.AddMonthsClamp(p.Month),
			Plan:   uah(plan, i, in.Deval),
			Amount: uah(actual, i, in.Deval), Low: uah(low, i, lowDeval), High: uah(high, i, highDeval),
			IncomePlan: state.Major(domain.Round2(p.IncomeUAH), money.UAH),
		}
		if i < len(actual) {
			pt.Income = state.Major(domain.Round2(actual[i].IncomeUAH), money.UAH)
			pt.Nominal = state.Major(domain.Round2(actual[i].NominalUAH+extraNominal), money.UAH)
			pt.Contributed = state.Major(domain.Round2(
				domain.RealContributed(p0, in.ActualMonthly, in.Deval, p.Month)+extra), money.UAH)
			pt.Gain = state.Major(pt.Amount.Major()-pt.Contributed.Major(), money.UAH)
		}
		out.Points = append(out.Points, pt)
	}

	lead := plan
	if len(actual) > 0 {
		lead = actual
	}
	out.Strategy = kindShares(in.Settings)
	out.ByCurrency, out.ByOrigin, out.ByKind = outlookBands(lead, in, out.Strategy, outside)

	// Скільки місяців історії стоїть за числом темпу — те саме, що
	// actual_months документа стану; тут рахується з тих самих рухів.
	out.Pace.Months = paceSpanMonths(tap.moves, in.Today)
	return out
}

// Ключі смуг розрізів. Види — ті самі рядки, що в rebalance[].key і в
// налаштуваннях target_*_pct: UI підписує їх одним словником (KIND_LABEL).
const (
	kindBonds    = "bonds"
	kindDeposits = "deposits"
	kindFunds    = "funds"
	kindNPF      = "npf"

	bandHeld    = "held"
	bandNew     = "new"
	bandOutside = "outside"
)

// kindShares — цільові частки видів зі стратегії, нормовані до 100 по
// тих, що задані. Те саме правило, яким розкладка ділить місяць
// (MonthShareUAH у spreadMonth): вид без цілі нових грошей не дістає.
func kindShares(set *state.SettingsDoc) []OutlookShare {
	raw := []struct {
		key string
		v   *float64
	}{
		{kindBonds, set.TargetBondsPct}, {kindDeposits, set.TargetDepositsPct},
		{kindFunds, set.TargetFundsPct}, {kindNPF, set.TargetNPFPct},
	}
	sum := 0.0
	for _, r := range raw {
		if r.v != nil && *r.v > 0 {
			sum += *r.v
		}
	}
	out := []OutlookShare{}
	if sum <= 0 {
		return out
	}
	for _, r := range raw {
		if r.v != nil && *r.v > 0 {
			out = append(out, OutlookShare{Key: r.key, Pct: domain.Round2(*r.v / sum * 100)})
		}
	}
	return out
}

// bandSet — смуги одного розрізу в порядку появи ключів.
type bandSet struct {
	n      int
	keys   []string
	values map[string][]float64
}

func newBandSet(n int) *bandSet { return &bandSet{n: n, values: map[string][]float64{}} }

func (b *bandSet) add(key string, i int, v float64) {
	if _, ok := b.values[key]; !ok {
		b.keys = append(b.keys, key)
		b.values[key] = make([]float64, b.n)
	}
	b.values[key][i] += v
}

// bands — смуги як їх бачить документ. Смуга з самих нулів не віддається:
// виду, якого в портфелі немає й не буде, на графіку робити нічого.
func (b *bandSet) bands() []OutlookBand {
	out := []OutlookBand{}
	for _, k := range b.keys {
		vals, seen := make([]state.Money, b.n), false
		for i, v := range b.values[k] {
			vals[i] = state.Major(domain.Round2(v), money.UAH)
			seen = seen || math.Abs(v) >= 0.005
		}
		if seen {
			out = append(out, OutlookBand{Key: k, Values: vals})
		}
	}
	return out
}

// outlookBands розкладає головну лінію трьома способами.
//
// ЗА ВАЛЮТАМИ й НАЯВНЕ / НОВЕ — точні: це кошики самої симуляції
// (domain.SleeveSlice), і в сумі вони дають лінію до копійки округлення.
// «Нове» — пул реінвесту: і внески, і купони та погашення наявного, що
// вже пішли в нові папери. «Наявне» тому з часом тане — це не втрата, а
// переїзд у сусідню смугу.
//
// ЗА ВИДАМИ — точне лише для наявного. Нові гроші модель тримає ОДНИМ пулом
// під ставку валюти й не знає, чим їх куплять; тут вони розкладені за
// цільовими частками стратегії. Це атрибуція, а не окремий розрахунок:
// фонд у цій смузі росте під ту саму ставку, що й ОВДП. Справжні підпули з
// власними ставками змінили б самі числа прогнозу — і розвели б його з
// таблицею projection.
//
// І друга межа точності там само: уже куплені ОВДП і тіло вкладів лежать в
// одному кошику (locked), тож між двома видами він ділиться за СТАРТОВОЮ
// пропорцією номіналу до тіла вкладів своєї валюти. Якщо вклади
// погашаються раніше за папери, їхня смуга насправді тане швидше.
func outlookBands(lead []domain.SeriesPoint, in projectionInput, strategy []OutlookShare,
	outside outsideMoney) (byCur, byOrigin, byKind []OutlookBand) {
	n := len(lead)
	cur, origin, kind := newBandSet(n), newBandSet(n), newBandSet(n)
	native := map[string][]state.Money{}

	bondShare := func(c string) float64 {
		bonds, deps := float64(in.NominalByCur[c])/100, in.DepositBodyByCur[c]
		if bonds+deps <= 0 {
			return 1
		}
		return bonds / (bonds + deps)
	}
	for i, p := range lead {
		for _, s := range p.BySleeve {
			held := s.LockedUAH + s.FundsUAH + s.NPFUAH
			cur.add(s.Currency, i, held+s.NewUAH)
			if native[s.Currency] == nil {
				native[s.Currency] = make([]state.Money, n)
			}
			native[s.Currency][i] = state.Major(domain.Round2(s.Native), s.Currency)

			origin.add(bandHeld, i, held)
			origin.add(bandNew, i, s.NewUAH)

			bs := bondShare(s.Currency)
			kind.add(kindBonds, i, s.LockedUAH*bs)
			kind.add(kindDeposits, i, s.LockedUAH*(1-bs))
			kind.add(kindFunds, i, s.FundsUAH)
			kind.add(kindNPF, i, s.NPFUAH)
			if len(strategy) == 0 {
				kind.add(bandNew, i, s.NewUAH)
			}
			for _, sh := range strategy {
				kind.add(sh.Key, i, s.NewUAH*sh.Pct/100)
			}
		}
		if extra, _ := outside.at(in.Deval, p.Month); extra > 0 {
			cur.add(bandOutside, i, extra)
			origin.add(bandOutside, i, extra)
			kind.add(bandOutside, i, extra)
		}
	}
	byCur = cur.bands()
	for i := range byCur {
		byCur[i].Native = native[byCur[i].Key]
	}
	return byCur, origin.bands(), kind.bands()
}

// paceSpanMonths — на скільки місяців поділено суму у вікні темпу.
// Близнюк блоку в buildMonth (state_month.go): той самий відбір і той
// самий paceMonths, лише без суми.
func paceSpanMonths(moves []moneyMove, today domain.Date) int {
	first, seen := today, false
	for _, m := range moves {
		if n := domain.DaysBetween(m.Date, today); n < 0 || n > actualWindowDays {
			continue
		}
		seen = true
		if m.Date.Before(first) {
			first = m.Date
		}
	}
	if !seen {
		return 0
	}
	return int(paceMonths(first, today) + 0.5)
}

// paceBars — нетто зовнішніх грошей по календарних місяцях, від
// найдавнішого до поточного, за СЬОГОДНІШНІМ курсом (як і сам темп).
//
// Місяці календарні, а вікно темпу — ковзні 183 дні, тож середнє стовпчиків
// у вікні НЕ дорівнює темпу, і так має бути: крайній місяць вікна входить
// у стовпчик цілим, а в темп — лише днями після межі. Лінію середнього
// малюють із Pace.Monthly, а не з цих стовпчиків.
func paceBars(moves []moneyMove, rates fx.Rates, today domain.Date) []PaceBar {
	t := today.Time()
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	windowFrom := today.AddDays(-actualWindowDays)

	sums := map[string]int64{}
	for _, m := range moves {
		if !m.Date.Valid() || m.Date.After(today) {
			continue
		}
		if u, err := fx.ToUAH(money.New(m.Amount, m.Currency), rates); err == nil {
			sums[string(m.Date)[:7]] += u.Amount()
		}
	}
	out := make([]PaceBar, 0, paceBarMonths)
	for i := paceBarMonths - 1; i >= 0; i-- {
		mo := first.AddDate(0, -i, 0)
		key := mo.Format("2006-01")
		// Місяць у вікні, якщо його останній день не раніший за межу вікна.
		lastDay := domain.NewDate(mo.AddDate(0, 1, -1))
		out = append(out, PaceBar{
			Month:    key,
			Amount:   state.Major(float64(sums[key])/100, money.UAH),
			InWindow: !lastDay.Before(windowFrom),
		})
	}
	return out
}
