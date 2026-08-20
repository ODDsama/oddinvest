// Ребаланс і концентрація — два різні питання про склад портфеля.
//
// Дев'ята фаза розбиття buildState.
//
// РЕБАЛАНС відповідає на «чого бракує до цілі». Він має два виміри, і їх
// не можна плутати. Валютний каже, В ЧОМУ тримати гроші; видовий — ЧИМ
// ризикувати. Портфель на 100% в ОВДП і портфель на 100% у фондах можуть
// мати однакові валютні частки й геть різну поведінку: у першого ризик
// процентний і державний, у другого — ринковий і керуючої компанії.
//
// КОНЦЕНТРАЦІЯ відповідає на протилежне — «де зібрано надто щільно».
// Теж три виміри, теж три різні питання. Один папір: що буде, якщо саме
// цей емітент не заплатить. Одна установа: що буде, якщо цей брокер чи
// банк зникне. Один рік драбини: що буде, якщо саме тоді ставки впадуть —
// гроші повернуться всі одразу й підуть за гіршою ставкою.
//
// Знаменник обох — capital, той самий, що й у плитці «Частка USD» (див.
// state/capital.go). Доти ребаланс рахував від «номінал + рахунок» і лише
// по облігаціях, а плитка — від усього капіталу з фондами, вкладами й
// резервом. Стояли вони на одному екрані й показували різне: 57.6% проти
// 0% для тієї самої валюти.
package api

import (
	"math"
	"sort"
	"strconv"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"

	money "github.com/Rhymond/go-money"
)

// rebalanceInput — усе, від чого залежать ребаланс і концентрація.
type rebalanceInput struct {
	Capital  state.Capital
	Settings *state.SettingsDoc
	Rates    fx.Rates

	// CashByCur — готівка по валютах, мінорні (чи є за що купувати вже
	// зараз); MinNominalByCur / DepositMinByCur — найдешевший вхід у
	// валюту, мінорні.
	CashByCur       map[string]int64
	MinNominalByCur map[string]int64
	DepositMinByCur map[string]int64

	// Найдешевший вхід у КОЖЕН вид інструмента, грн-екв. (0 = невідомо).
	MinBondUAH    float64
	MinFundUAH    float64
	MinDepositUAH float64

	// Для концентрації: номінал по паперах (мінорні, грн-екв.), довідник
	// НБУ для людських назв, рядки фондів, експозиція контрагентів і
	// драбина по роках.
	NominalByISIN     map[string]int64
	Bonds             map[string]domain.Bond
	FundRows          []state.FundPositionRow
	NPFRows           []state.NPFPositionRow
	BrokerExposureUAH map[string]float64
	LadderUAH         []state.YearAmount
}

// fillPct — наскільки ціль закрита, %. Нульова ціль дає нуль, а не
// нескінченність: «цілі немає» і «ціль закрита на нуль відсотків» —
// різні речі, і перша з них не має права стати числом узагалі.
func fillPct(now, target float64) float64 {
	if target <= 0 {
		return 0
	}
	return now / target * 100
}

// rebalancePhase — рядки обох карток.
type rebalancePhase struct {
	Rebalance     []state.RebalanceRow
	Concentration []state.ConcentrationRow
}

// buildRebalance рахує ребаланс за валютою й видом, а також концентрацію.
func buildRebalance(in rebalanceInput) rebalancePhase {
	var out rebalancePhase
	cap, set := in.Capital, in.Settings
	totalMajor := cap.TotalUAH()

	// --- валютний ребаланс ---
	//
	// Окремо перевіряємо здійсненність: найдешевший папір може бути
	// більший за всю цільову суму — тоді ціль поки недосяжна без перекосу
	// структури.
	targets := map[string]*float64{money.USD: set.USDTargetSharePct, money.EUR: set.EURTargetSharePct}
	for _, cur := range []string{money.USD, money.EUR} {
		tp := targets[cur]
		if tp == nil || *tp <= 0 {
			continue
		}
		rateMajor, ok := fx.RateMajor(cur, in.Rates)
		if !ok {
			continue // курсу немає — рядок чесніше не малювати
		}
		curUAH := cap.ExposureUAH(cur)
		currentPct := cap.SharePct(cur)
		targetUAH := totalMajor * (*tp) / 100
		deficitUAH := math.Max(0, targetUAH-curUAH)
		cashNative := float64(in.CashByCur[cur]) / 100
		// Одиниця входу з ПРІОРИТЕТОМ облігації: якщо найдешевший папір
		// вписується в цільову частку — радимо його (безподатковий купон,
		// справжній інструмент). Вклад ($100/€100) — запасний, менший вхід
		// лише коли до облігації ще не доросли: доти картка казала «ще
		// зарано» на $1000-й папір, хоча частку добирає й вклад на $100.
		bondNative := float64(in.MinNominalByCur[cur]) / 100
		bondUAH := bondNative * rateMajor
		depNative := 0.0
		if dm, ok := in.DepositMinByCur[cur]; ok {
			depNative = float64(dm) / 100
		}
		var unitNative, unitUAH float64
		var unitKind string
		switch {
		case bondNative > 0 && bondUAH <= targetUAH:
			unitNative, unitUAH, unitKind = bondNative, bondUAH, "bond"
		case depNative > 0:
			unitNative, unitUAH, unitKind = depNative, depNative*rateMajor, "deposit"
		default:
			unitNative, unitUAH, unitKind = bondNative, bondUAH, "bond"
		}
		var canBuy int64
		convertUAH := 0.0
		if unitNative > 0 {
			canBuy = int64(cashNative / unitNative)
			if cashNative < unitNative {
				convertUAH = (unitNative - cashNative) * rateMajor
			}
		}
		out.Rebalance = append(out.Rebalance, state.RebalanceRow{
			Dimension: "currency", Key: cur,
			Currency: cur, TargetPct: *tp, CurrentPct: round2(currentPct),
			DeficitUAH: round2(deficitUAH), DeficitNative: round2(deficitUAH / rateMajor),
			CashNative: round2(cashNative), BondCostNative: round2(unitNative),
			BondCostUAH: round2(unitUAH), CanBuy: canBuy, ConvertUAH: round2(convertUAH),
			MinPortfolioUAH: round2(unitUAH / (*tp / 100)),
			Feasible:        unitUAH > 0 && unitUAH <= targetUAH,
			UnitKind:        unitKind,
			TargetUAH:       round2(targetUAH),
			CurrentUAH:      round2(curUAH),
			FillPct:         round2(fillPct(curUAH, targetUAH)),
		})
	}

	// --- ребаланс за ВИДОМ інструмента ---
	//
	// Сума цілей НЕ мусить давати 100. Нормалізувати мовчки означало б
	// підмінити введене користувачем: поставив 40/20 — і отримав 67/33,
	// не питаючи. Нерозподілене показуємо числом і лишаємо як є.
	kindTargets := []struct {
		key    string
		nowUAH float64
		target *float64
		unit   float64 // найдешевший вхід у цей вид, грн-екв.; 0 = невідомо
	}{
		{"bonds", cap.BondsUAH, set.TargetBondsPct, in.MinBondUAH},
		{"funds", cap.FundsUAH, set.TargetFundsPct, in.MinFundUAH},
		{"deposits", cap.DepositsUAH, set.TargetDepositsPct, in.MinDepositUAH},
		// НПФ із одиницею входу 0 — і це не «не дізнались», а «її немає»:
		// внести в пенсійний можна будь-яку суму, порога входу він не має.
		// Нуль тут вимикає перевірку здійсненності, як і в резерву.
		{"npf", cap.NPFUAH, set.TargetNPFPct, 0},
	}
	for _, k := range kindTargets {
		if k.target == nil || *k.target <= 0 || totalMajor <= 0 {
			continue
		}
		currentPct := k.nowUAH / totalMajor * 100
		targetUAH := totalMajor * (*k.target) / 100
		row := state.RebalanceRow{
			Dimension: "kind", Key: k.key, Currency: money.UAH,
			TargetPct: round2(*k.target), CurrentPct: round2(currentPct),
			DeficitUAH: round2(math.Max(0, targetUAH-k.nowUAH)),
			// Одиниця входу тут завжди в гривні-еквіваленті: питання «яким
			// інструментом», а не «якою валютою», і мішати сюди ще й
			// нативні суми означало б два виміри в одному рядку.
			BondCostUAH: round2(k.unit), UnitKind: k.key,
			// Без заданої одиниці входу здійсненність не перевіряється:
			// у резерв кладуть будь-яку суму, а не «мінімальний внесок».
			Feasible: k.unit == 0 || k.unit <= targetUAH,
			// Ті самі ціль і факт у грошах. Питання «скільки ще докласти»
			// ставиться в гривнях, і рахувати їх зі згаданих поруч відсотків
			// означало б відповідати на нього з похибкою округлення.
			TargetUAH:  round2(targetUAH),
			CurrentUAH: round2(k.nowUAH),
			FillPct:    round2(fillPct(k.nowUAH, targetUAH)),
		}
		if k.unit > 0 {
			row.MinPortfolioUAH = round2(k.unit / (*k.target / 100))
		}
		out.Rebalance = append(out.Rebalance, row)
	}
	// Резерв — рядок БЕЗ цілі, суто довідковий. Спокуса вивести його
	// цільову частку з місяців витрат виглядає природною, але дає число,
	// яке нічого не означає: на живих даних ціль у 150 000 ₴ при капіталі
	// 38 913 ₴ показалась як «385% капіталу». Частка рухається щоразу, коли
	// росте портфель, навіть якщо резерву ніхто не чіпав, — а ціль резерву
	// за природою абсолютна, бо міряється місяцями життя, не портфелем.
	//
	// Тож ціль лишається там, де вона осмислена (картка резерву, у місяцях
	// і гривнях), а сюди резерв входить лише щоб картина складу була
	// повною: без нього частки видів не сходились би з очевидним «а решта
	// де?».
	if cap.ReserveUAH > 0 && totalMajor > 0 {
		out.Rebalance = append(out.Rebalance, state.RebalanceRow{
			Dimension: "kind", Key: "reserve", Currency: money.UAH,
			CurrentPct: round2(cap.ReserveUAH / totalMajor * 100),
			// Гроші є, цілі за часткою немає — тож заповнене лише «зараз».
			// Ціль резерву абсолютна (місяці витрат) і живе у своїй картці;
			// TargetUAH тут лишається нулем навмисно, інакше рядок почав би
			// обіцяти ціль, якої в цьому вимірі не існує.
			CurrentUAH: round2(cap.ReserveUAH),
			UnitKind:   "reserve", Feasible: true,
		})
	}
	// НПФ без заданої цілі — так само довідковий рядок, з тієї ж причини, що
	// й резерв: без нього частки видів не сходяться з «а решта де?», і
	// пенсійна частка, яка може бути найбільшою в портфелі, просто зникала б
	// із картини складу.
	if cap.NPFUAH > 0 && totalMajor > 0 && (set.TargetNPFPct == nil || *set.TargetNPFPct <= 0) {
		out.Rebalance = append(out.Rebalance, state.RebalanceRow{
			Dimension: "kind", Key: "npf", Currency: money.UAH,
			CurrentPct: round2(cap.NPFUAH / totalMajor * 100),
			CurrentUAH: round2(cap.NPFUAH),
			UnitKind:   "npf", Feasible: true,
		})
	}

	// --- концентрація ---
	//
	// Показуємо ВСІ рядки з заданим лімітом, а не самі порушення: «45% при
	// ліміті 50%» теж варте знання, а список, що з'являється лише коли вже
	// пізно, читається як аварія, а не як приладова панель.
	addConc := func(dim, key, label string, amount, base, limit float64) {
		if base <= 0 {
			return
		}
		share := amount / base * 100
		row := state.ConcentrationRow{
			Dimension: dim, Key: key, Label: label,
			AmountUAH: round2(amount), SharePct: round2(share), LimitPct: limit,
		}
		if share > limit {
			row.OverUAH = round2(amount - base*limit/100)
		}
		out.Concentration = append(out.Concentration, row)
	}
	if set.LimitISINPct != nil && *set.LimitISINPct > 0 {
		for isin, minor := range in.NominalByISIN {
			label := ""
			if b, ok := in.Bonds[isin]; ok {
				label = b.Descr
			}
			addConc("isin", isin, label, float64(minor)/100, totalMajor, *set.LimitISINPct)
		}
		// Фонди — сюди ж. Питання виміру не «скільки в облігаціях», а
		// «скільки залежить від ОДНОГО емітента», і сертифікат відповідає
		// на нього так само, як папір. Без цього список брехав найгучніше
		// саме там, де ризик найбільший: на живих даних один фонд важив
		// 16.7% капіталу — більше за будь-яку окрему облігацію в списку.
		for _, row := range in.FundRows {
			if row.MarketValue <= 0 {
				continue
			}
			addConc("isin", domain.FundISINPrefix+row.Fund, row.Fund,
				row.MarketValue, totalMajor, *set.LimitISINPct)
		}
		// НПФ — тим самим виміром і з тієї ж причини. «Що буде, якщо ця КУА
		// не заплатить» — те саме питання, що про емітента паперу, і на
		// довгому горизонті воно навіть гостріше: вийти з фонду до пенсії не
		// можна, тобто помилку концентрації тут не виправити продажем.
		for _, row := range in.NPFRows {
			if row.ValueUAH <= 0 {
				continue
			}
			addConc("isin", domain.NPFSyntheticISIN(row.Name), row.Name,
				row.ValueUAH, totalMajor, *set.LimitISINPct)
		}
	}
	if set.LimitBrokerPct != nil && *set.LimitBrokerPct > 0 {
		for name, v := range in.BrokerExposureUAH {
			addConc("broker", name, "", v, totalMajor, *set.LimitBrokerPct)
		}
	}
	if set.LimitYearPct != nil && *set.LimitYearPct > 0 {
		// База тут — УСІ погашення, а не капітал: питання «чи рівномірно
		// рознесені повернення», і міряти його від капіталу означало б
		// вважати порушенням будь-яку драбину в портфелі, де більшість
		// грошей у фондах, — тобто там, де погашень немає взагалі.
		ladderTotal := 0.0
		for _, y := range in.LadderUAH {
			ladderTotal += y.UAH
		}
		for _, y := range in.LadderUAH {
			addConc("year", strconv.Itoa(y.Year), "", y.UAH, ladderTotal, *set.LimitYearPct)
		}
	}
	// Найщільніше — зверху: список читають згори, і перше, що впадає в
	// око, має бути найбільшим ризиком, а не найдавнішим роком.
	//
	// Ключ третім критерієм — не косметика. Рядки сюди приходять обходом
	// МАПИ, а sort.Slice нестабільний: два папери з однаковою часткою
	// ставали б у випадковому порядку, різному від запуску до запуску.
	// Сьогодні часток-двійників у жодному вимірі немає, тож порядок від
	// цього не змінився — але без ключа golden став би блимати того дня,
	// коли вони зʼявляться, і шукали б причину довго.
	sort.Slice(out.Concentration, func(i, j int) bool {
		a, b := out.Concentration[i], out.Concentration[j]
		if a.Dimension != b.Dimension {
			return a.Dimension < b.Dimension
		}
		if a.SharePct != b.SharePct {
			return a.SharePct > b.SharePct
		}
		return a.Key < b.Key
	})
	return out
}
