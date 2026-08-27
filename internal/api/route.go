// Маршрут грошей: куди піде те, що ще тільки прийде.
//
// Розкладка (handlers_allocate.go) відповідає на «ось 32 000 ₴ — розклади
// саме їх», і відповідає лише про гроші, які вже в руках, і лише коли її
// про це спитали. При цьому застосунок знає КОЖНЕ надходження на рік
// уперед: купони й погашення ОВДП, відсотки й тіла вкладів. Питання «куди
// піде те, що прийде в листопаді» ніхто не ставив, хоч усе для відповіді
// вже лежало поруч.
//
// Маршрут прокручує ту саму розкладку вперед по календарю надходжень.
//
// # ВЛАСНОЇ АРИФМЕТИКИ ТУТ НЕМАЄ ЖОДНОЇ
//
// Це головна вимога до файла, і вона та сама, що й у розкладки. Вирізку
// подушки, бюджети видів і порядок рахує allocatePlan — незмінена, тим
// самим викликом. Стелю місяця рахує reserveMonthShare від buildMonthPlan
// свого місяця. Надходження збирає routeIncome. Тут лишається рівно
// прохід: узяти подію, віддати її розкладці, запам'ятати наслідок.
//
// Доказ механічний: TestRouteFirstLegEqualsAllocate вимагає, щоб перша
// нога маршруту збігалася з POST /api/allocate на ту саму суму. Якщо він
// колись упаде — хтось завів друге означення «куди йдуть ці гроші».
//
// # ПЕРЕНОС — ВІСІМ ЧИСЕЛ, А НЕ ДОКУМЕНТ
//
// Подія N мусить знати, що зробили події 1..N−1: подушка підросла, розрив
// виду закрився, капітал змінився. Спокуса — копіювати документ і
// просувати його цілком; ціна — обіцянка, що маршрут просуває драбину,
// концентрацію й позиції, а він їх не просуває й не може (цін майбутнього
// дня ми не знаємо).
//
// Тому просувається РІВНО те, що allocatePlan читає:
//
//	Reserve.FillNowUAH    вирізка подушки        мінус вирізка; ЩОМІСЯЦЯ ЗАНОВО
//	Reserve.FillMonthUAH  текст причини          щомісяця заново
//	Reserve.GapUAH        текст причини          мінус вирізка, без скидання
//	Rebalance[].CurrentUAH  spreadMonth          плюс витрачене в цьому виді
//	CapitalUAH            kindMajor              плюс НОВІ гроші (див. нижче)
//	ReserveUAH            kindMajor              плюс вирізка
//
// Rebalance[].CurrentPct не просувається навмисно: його на цьому шляху не
// читає ніхто, і просунуте число без читача наступний автор «полагодить»
// під щось інше. Brokers, Funds, NPF, Ladder, Concentration, Positions —
// так само не читаються й не чіпаються.
//
// ПОРЯДОК УСЕРЕДИНІ КРОКУ ЗНАЧУЩИЙ, і це не стиль. Тіло, що повернулось,
// виходить із виду ДО розкладки (redeem) — інакше гроші не побачили б
// розриву, який самі ж і відкрили. Дохід стає капіталом ПІСЛЯ неї (earn) —
// інакше те саме надходження стояло б у базі двічі, бо spreadMonth уже
// додає його як avail. Обидві межі коштували по одному червоному тесту.
//
// # СТЕЛЯ ПОДУШКИ ЩОМІСЯЦЯ СКИДАЄТЬСЯ
//
// Найлегша помилка в усьому файлі. FillNowUAH — не «скільки лишилось до
// цілі», а частка ОДНОГО МІСЯЦЯ: reserveMonthShare бере plan.PlanUAH ×
// стелю й лише потім обрізає розривом. Прохід, який просто зменшував би
// розрив, налив би в подушку стільки разів по стелі, скільки місяців у
// горизонті — і виглядав би при цьому цілком розумно. Звідси enterMonth і
// TestRouteReserveCeilingResetsEachMonth.
//
// moved у майбутніх місяцях нульовий: у місяць, який ще не настав, ніхто
// нічого не відкладав. Поточний місяць переносу не рахується взагалі — він
// береться з документа як є, разом із уже відкладеним, і саме тому перша
// нога дорівнює розкладці.
//
// # КУПОН І ПОГАШЕННЯ — РІЗНІ ПОДІЇ ДЛЯ КАПІТАЛУ
//
// Купон і відсотки вкладу — нові гроші: капітал росте. Погашення й тіло
// вкладу — переклад власного з інструмента в готівку: капітал незмінний, а
// вид, який погасився, худне рівно на тіло. Без цієї різниці прохід
// зараховував би повернення номіналу приростом і занижував би розрив саме
// того виду, з якого гроші щойно вийшли. Розділяє їх readyFlow.Principal.
//
// # ЩО ЛИШАЄТЬСЯ СЬОГОДНІШНІМ
//
// Порядок і ціна. Рейтинг рахується ОДИН раз, від сьогоднішнього
// документа, і не перераховується під кожну подію: по-перше, це
// SearchBonds на п'ять тисяч паперів сорок разів поспіль, по-друге —
// перерахунок під майбутню дату вимагав би майбутнього НКД, майбутніх
// аукціонних рівнів і майбутнього довідника НБУ, тобто числа, яке
// виглядає виведеним, а насправді вигадане. Ціна квитка — теж
// сьогоднішня (номінал + сьогоднішній НКД).
//
// Це твердження про СЬОГОДНІШНІ розриви політики, прикладене вперед, і
// сказати це вголос зобов'язаний той, хто малює таблицю. Правильна
// реакція на «це нечесно» — прибрати кількість і лишити вид, а не
// вигадати форвардну ціну; ринкової ціни паперу застосунок не показує
// ніде, і причина названа в README.
//
// # ЧОГО ТУТ НЕМАЄ
//
// Порад. Жодне призначення не вигадане: подушка бере своє за стелею, яку
// поставив користувач, види діляться за його ж цільовими частками, порядок
// усередині виду — за обраним ним reinvest_rank. Маршрут не забороняє й
// не ховає нічого; він лише каже, що станеться, якщо не втручатись.
//
// Обмеження залишком на рахунках — з тієї самої причини, що й у розкладки:
// ми ведемо гроші, які ПРИЙДУТЬ, а не ті, що вже лежать.
package api

import (
	"math"
	"sort"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// routeHorizonMonths — доки маршрут дивиться.
//
// Рік, і це той самий обрій, що й у календаря виплат та оцінок
// (scheduleFundMonths): питання «що впаде на рахунок найближчим часом»
// заслуговує рівно стільки. Далі за рік розклад ще відомий, але політика,
// за якою ці гроші розкладаються, вже ні.
const routeHorizonMonths = 12

// routeLeg — одна подія надходження й те, що з її грошей виходить.
//
// Розкладка вкладена БЕЗ ІМЕНІ: у JSON її поля лягають поруч із датою й
// брокером, тобто нога маршруту читається тим самим кодом, що й відповідь
// POST /api/allocate. Вкласти її полем означало б дати двом однаковим
// відповідям дві різні форми.
type routeLeg struct {
	Date     string `json:"date"`
	Broker   string `json:"broker"`
	Currency string `json:"currency"`
	Label    string `json:"label"`
	// CarryInUAH — скільки в цю ногу прийшло з попередніх надходжень, які
	// не склались у цілий квиток. Via називає ті надходження поіменно —
	// але лише доти, доки з них НІЧОГО не витрачено: щойно частина пішла в
	// діло, приписати залишок конкретній події вже не можна, і далі він
	// їде безіменним числом.
	CarryInUAH float64      `json:"carry_in_uah,omitempty"`
	Via        []readyEvent `json:"via,omitempty"`
	// InflowUAH — скільки надійде САМЕ ЦЬОГО ДНЯ, без переносу.
	//
	// Окремо від AmountUAH навмисно, і це не дублювання. AmountUAH — весь
	// горщик, тобто те, що розкладається; InflowUAH — те, що впаде на
	// рахунок. На другій нозі вони розходяться завжди, і сказати «16.09
	// надійде 3 123,49 ₴», коли насправді надійде 1 312,70 ₴, а решта
	// чекала з попереднього разу, було б неправдою про виписку банку.
	//
	// Числом із бекенда, а не відніманням у браузері: різницю двох
	// округлених чисел уже двічі показували як зайву копійку.
	InflowUAH float64 `json:"inflow_uah"`
	// PrincipalUAH — скільки з InflowUAH є поверненням власного тіла. Не
	// косметика: саме цим число відрізняється від «доходу» в підсумку
	// місяця, і без підпису нога на 10 000 ₴ погашення читалась би як
	// заробіток.
	PrincipalUAH float64 `json:"principal_uah,omitempty"`
	// Basis — на чому стоять ГРОШІ ЦІЄЇ НОГИ, разом із перенесеними.
	//
	// Не косметика й не дублювання основи події: горщик може зібратись із
	// купона (портфель це винен) і дивіденду фонду (оцінка зі ставки), і
	// тоді нога чесно каже basisMixed. Саме ця колонка й дозволяє маршруту
	// показувати оцінки там, де дата «коли вистачить» їх не показує:
	// припущення тут лишається видимим, бо в жодне спільне число воно не
	// зводиться (аргумент — у шапці routeIncome).
	Basis string `json:"basis"`

	allocPlan
}

// routeDoc — увесь маршрут.
type routeDoc struct {
	From string     `json:"from"`
	To   string     `json:"to"`
	Legs []routeLeg `json:"legs"`
	// Note — чому ніг немає взагалі. Порожня відповідь без причини
	// читається як поломка, а причина тут одна: портфель нічого не
	// винен собі до горизонту.
	Note string `json:"note,omitempty"`
}

// routeCarry — те, що подія N мусить знати про події 1..N−1.
//
// Аргумент, чому саме ці поля й чому не документ цілком, — у шапці файла.
type routeCarry struct {
	base *state.Doc
	set  *state.SettingsDoc

	capitalUAH float64
	reserveUAH float64
	gapUAH     float64
	fillMonth  float64
	fillNow    float64
	month      string
	kindUAH    map[string]float64
}

func newRouteCarry(doc *state.Doc, today domain.Date) *routeCarry {
	c := &routeCarry{
		base: doc, set: doc.Settings,
		capitalUAH: doc.CapitalUAH,
		reserveUAH: doc.ReserveUAH,
		// Поточний місяць береться з документа як є — разом із уже
		// відкладеним цього місяця. Перерахувати його тут означало б
		// втратити moved і розійтися з карткою резерву на першому ж рядку.
		month: monthKeyAt(today, 0),
		// Мапа заповнюється лише рядками виміру "kind": решта рядків
		// ребалансу (валюта, брокер, ISIN) розкладці не потрібна, і
		// тримати для них числа означало б обіцяти, що маршрут просуває
		// й валютні частки — а він їх не просуває.
		kindUAH: map[string]float64{},
	}
	if r := doc.Reserve; r != nil {
		c.gapUAH, c.fillMonth, c.fillNow = r.GapUAH, r.FillMonthUAH, r.FillNowUAH
	}
	for _, row := range doc.Rebalance {
		if row.Dimension == "kind" {
			c.kindUAH[row.Key] = row.CurrentUAH
		}
	}
	return c
}

// doc — документ, яким його бачить розкладка на цьому кроці.
//
// ЗНАЧЕНЕВА КОПІЯ, і джерело не мутується ніколи: той самий *state.Doc
// далі читають черга задач і сам обробник, і мовчки просунутий у них
// капітал був би найгіршим виглядом помилки — правдоподібним. Rebalance і
// Reserve перевиділяються, бо саме їх ми й підміняємо; решта полів
// спільна з базою й лише читається (allocatePlan чиста, вона нічого не
// пише).
// carryInUAH — гроші, які вже лежать у капіталі (вони зайшли туди
// доходом попередньої події) і зараз подаються розкладці ЩЕ РАЗ, у складі
// горщика. spreadMonth додає весь горщик до бази (after = kindMajor +
// avail), тож без цього віднімання та сама копійка стояла б у базі двічі.
// Це саме дедублікація, а не нове правило: жодного числа тут не
// зʼявляється, одне й те саме перестає рахуватись двічі.
func (c *routeCarry) doc(carryInUAH float64) *state.Doc {
	d := *c.base
	d.CapitalUAH = c.capitalUAH - carryInUAH
	d.ReserveUAH = c.reserveUAH
	if c.base.Reserve != nil {
		r := *c.base.Reserve
		r.GapUAH, r.FillMonthUAH, r.FillNowUAH = c.gapUAH, c.fillMonth, c.fillNow
		d.Reserve = &r
	}
	rows := make([]state.RebalanceRow, len(c.base.Rebalance))
	copy(rows, c.base.Rebalance)
	for i := range rows {
		if rows[i].Dimension != "kind" {
			continue
		}
		if v, ok := c.kindUAH[rows[i].Key]; ok {
			rows[i].CurrentUAH = round2(v)
		}
	}
	d.Rebalance = rows
	return &d
}

// enterMonth переставляє стелю подушки на новий місяць.
//
// Мовчить, доки місяць той самий, — і саме тому поточний місяць лишається
// таким, яким його порахував документ.
func (c *routeCarry) enterMonth(month string, mp *state.MonthPlan) {
	if month == c.month {
		return
	}
	c.month = month
	// moved = 0: у місяці, який ще не настав, у подушку ще нічого не клали.
	c.fillMonth, c.fillNow = reserveMonthShare(c.set, c.reserveUAH, mp, 0)
}

// redeem — тіло вийшло з інструмента, ДО розкладки.
//
// Саме до неї, і це змістовно: коли папір гаситься, розрив ОВДП
// відкривається рівно на його номінал, і гроші мусять побачити цей розрив
// уже цього кроку. Інакше маршрут вів би погашення кудись убік, а драбина
// осідала б без жодного слова.
//
// Капітал при цьому не змінюється: тіло лише поміняло форму.
func (c *routeCarry) redeem(principalUAH float64, kind string) {
	if principalUAH > 0 && kind != "" {
		c.kindUAH[kind] -= principalUAH
	}
}

// earn — дохід став капіталом, ПІСЛЯ розкладки.
//
// Саме після, і це теж змістовно: усередині розкладки ці гроші вже
// враховані як avail (spreadMonth: after = kindMajor + avail), і додати їх
// іще й до kindMajor означало б порахувати одне надходження двічі. Рівно
// на цьому місці перша нога маршруту й розійшлася з POST /api/allocate,
// доки порядок був зворотний.
func (c *routeCarry) earn(incomeUAH float64) {
	c.capitalUAH += incomeUAH
}

// apply — наслідки однієї розкладки.
func (c *routeCarry) apply(p allocPlan) {
	if p.Reserve != nil && p.Reserve.AmountUAH > 0 {
		v := p.Reserve.AmountUAH
		c.reserveUAH += v
		c.gapUAH = math.Max(0, c.gapUAH-v)
		c.fillNow = math.Max(0, c.fillNow-v)
	}
	for _, l := range p.Lines {
		if k, ok := allocKind[l.Kind]; ok {
			c.kindUAH[k] += l.TotalUAH
		}
	}
}

// routeEvent — одне надходження, вирване з мапи incomeAhead у плаский
// список. Мапа обходиться в довільному порядку, а маршрут іде В ЧАСІ й по
// ВСЬОМУ портфелю одразу: подушка й розриви видів спільні для всіх
// брокерів, і розкладати спершу один рахунок, потім другий означало б
// віддати першому всю місячну стелю подушки.
type routeEvent struct {
	readyFlow
	bc store.BrokerCur
}

// flattenIncome — надходження одним списком, упорядкованим ПОВНИМ ключем.
//
// Повним, а не самою датою: sort.Slice нестабільний, дві виплати одного дня
// в різних брокерів інакше ставали б у порядку обходу мапи, і два запуски
// на тих самих даних дали б різні маршрути. Та сама пастка описана в шапці
// state_schedule.go — з тією різницею, що там ключ свідомо лишили неповним.
func flattenIncome(inc incomeAhead, horizon domain.Date) []routeEvent {
	var out []routeEvent
	for bc, flows := range inc {
		for _, f := range flows {
			if f.Date.After(horizon) {
				continue
			}
			out = append(out, routeEvent{readyFlow: f, bc: bc})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Date != b.Date:
			return a.Date < b.Date
		case a.bc.Broker != b.bc.Broker:
			return a.bc.Broker < b.bc.Broker
		case a.bc.Currency != b.bc.Currency:
			return a.bc.Currency < b.bc.Currency
		default:
			return a.Label < b.Label
		}
	})
	return out
}

// routePot — гроші однієї пари (брокер × валюта), що чекають цілого квитка.
//
// НАКОПИЧЕННЯ ВИХОДИТЬ САМО, і окремого механізму під нього немає. Розкладка
// вже вміє відповідати «на цілий крок не вистачило» і називати залишок
// (RestUAH); маршрут лише везе цей залишок до наступного надходження тієї
// самої пари. Тому «340 + 1 200 + 900 → у березні набереться на папір» — не
// нове правило, а той самий рядок розкладки, прочитаний тричі поспіль.
//
// Пари роздільні з тієї самої причини, що й у «коли вистачить»: гривня в
// mono не купить папір в inzhur.
type routePot struct {
	minor   int64 // нативні мінорні, що лишились чекати
	pending []readyEvent
	// basis — основа грошей, які в горщику лежать. Порожньо, доки горщик
	// порожній; далі зливається з основою кожної події, що в нього впала.
	basis string
}

// mergeBasis — основа горщика після того, як у нього впала подія.
//
// Різні основи не «перемагають» одна одну й не усереднюються: горщик, у
// якому зійшлись зобовʼязання й оцінка, каже про це прямо. Обрати одну з
// двох означало б сховати саме те, заради чого колонка й існує.
func mergeBasis(pot, ev string) string {
	switch {
	case pot == "":
		return ev
	case pot == ev:
		return pot
	default:
		return basisMixed
	}
}

// buildRoute — увесь прохід.
//
// plans — план доходу по місяцях горизонту (ключ YYYY-MM). Приходить
// готовим, а не будується тут: для нього потрібні sources, а цей файл
// свідомо працює лише над документом і розкладом, як і allocatePlan поруч.
func buildRoute(doc *state.Doc, sug []suggestion, inc incomeAhead,
	plans map[string]*state.MonthPlan, rates fx.Rates,
	npfID map[string]int64, today domain.Date) routeDoc {

	horizon := today.AddMonths(routeHorizonMonths)
	out := routeDoc{From: string(today), To: string(horizon), Legs: []routeLeg{}}

	events := flattenIncome(inc, horizon)
	if len(events) == 0 {
		out.Note = "до горизонту портфель нічого не винен сам собі — " +
			"ні купонів, ні погашень, ні відсотків вкладів"
		return out
	}

	carry := newRouteCarry(doc, today)
	pots := map[store.BrokerCur]*routePot{}

	for _, ev := range events {
		carry.enterMonth(monthKeyAt(today, monthOffsetRaw(today, ev.Date)),
			plans[monthKeyAt(today, monthOffsetRaw(today, ev.Date))])

		cur := ev.bc.Currency
		rate := allocRate(cur, rates)

		amountUAH := float64(ev.Amount) / 100 * rate
		principalUAH := float64(ev.Principal) / 100 * rate
		carry.redeem(principalUAH, ev.Kind)

		pot := pots[ev.bc]
		if pot == nil {
			pot = &routePot{}
			pots[ev.bc] = pot
		}
		carryIn := pot.minor
		pot.minor += ev.Amount
		pot.basis = mergeBasis(pot.basis, flowBasis(ev.readyFlow))
		pot.pending = append(pot.pending, readyEvent{
			Date: string(ev.Date), Label: ev.Label,
			Amount: toMoneyJSON(money.New(ev.Amount, cur)),
		})

		carryInUAH := float64(carryIn) / 100 * rate
		potUAH := float64(pot.minor) / 100 * rate
		plan := allocatePlan(carry.doc(carryInUAH), sug, rates,
			toMoneyJSON(money.New(pot.minor, cur)), potUAH, cur, npfID)
		carry.apply(plan)
		// Дохід стає капіталом аж тепер — аргумент при earn.
		carry.earn(amountUAH - principalUAH)

		leg := routeLeg{
			Date: string(ev.Date), Broker: ev.bc.Broker, Currency: cur,
			Label: ev.Label, allocPlan: plan,
			CarryInUAH:   round2(carryInUAH),
			InflowUAH:    round2(amountUAH),
			PrincipalUAH: round2(principalUAH),
			Basis:        pot.basis,
		}
		// Витрачене — це те, чого в залишку вже немає. Рахуємо саме так, а
		// не сумою рядків: розкладка сама знає, що з суми пішло в діло, і
		// друге складання розійшлося б із нею на копійку округлення.
		spentUAH := plan.AmountUAH - plan.RestUAH
		if spentUAH > 0.005 {
			// Гроші пішли в діло — і поіменно назвати, з яких надходжень
			// вони склались, можна рівно зараз. Далі залишок безіменний.
			if len(pot.pending) > 1 {
				leg.Via = pot.pending
			}
			pot.pending = nil
		}
		pot.minor = int64(math.Round(plan.RestUAH / rate * 100))
		if pot.minor <= 0 {
			// Горщик спорожнів — основа наступних грошей буде їхня власна, а
			// не успадкована від тих, що вже пішли в діло.
			pot.basis = ""
		}
		out.Legs = append(out.Legs, leg)
	}
	return out
}
