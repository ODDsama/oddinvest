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
	// Ref — чим цю виплату позначають отриманою (ISIN або "deposit:<id>").
	// Порожньо в оцінок: дивіденд фонду позначати нема чого. Саме за цим
	// полем сторінка й вирішує, чи має нога кнопку «Прийшло».
	Ref string `json:"ref,omitempty"`
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
	// InflowWhy — чому InflowUAH менший за те, що інструмент нарахував.
	// Сьогодні єдиний випадок — фонд, який утримує виплату й докуповує на
	// неї свої ж сертифікати: на рахунок падає лише решта.
	//
	// Поруч із PrincipalUAH і за тим самим доводом, який написаний вище:
	// без підпису число, менше за очікуване, читається як помилка
	// застосунку. Прозою з бекенда — арифметику в браузері не рахуємо.
	InflowWhy string `json:"inflow_why,omitempty"`
	// Principal — те саме тіло в НАТИВНІЙ валюті. Окремо від PrincipalUAH,
	// і не заради симетрії: кнопка «Прийшло» шле в POST /api/allocate суму
	// та тіло в одній валюті, і гривневе число на нозі в доларах віддало б
	// подушці рівно курс. Показує його ніхто — воно для запиту.
	Principal *moneyJSON `json:"principal,omitempty"`
	// Basis — на чому стоять ГРОШІ ЦІЄЇ НОГИ, разом із перенесеними.
	//
	// Не косметика й не дублювання основи події: горщик може зібратись із
	// купона (портфель це винен) і дивіденду фонду (оцінка зі ставки), і
	// тоді нога чесно каже basisMixed. Саме ця колонка й дозволяє маршруту
	// показувати оцінки там, де дата «коли вистачить» їх не показує:
	// припущення тут лишається видимим, бо в жодне спільне число воно не
	// зводиться (аргумент — у шапці routeIncome).
	Basis string `json:"basis"`
	// Planned — посилання рядків плану купівель, які вже стоять на цю дату
	// й цей рахунок. Захист від найпростішої помилки: натиснути «Закріпити»
	// двічі й дістати дві однакові покупки, які потім треба знаходити й
	// видаляти руками.
	Planned []string `json:"planned,omitempty"`
	// Pinnable — чи можна цю ногу класти в план купівель.
	//
	// ЧОМУ НЕ ЗАВЖДИ. Сторінка сама каже, що ціна кроку тут сьогоднішня, і
	// закріпити конкретний ISIN на липень 2027-го означало б зафіксувати
	// саме те число, про яке ми щойно сказали «ми його не знаємо».
	// Найближчі тридцять днів — інша річ: там сьогоднішня ціна ще щось
	// означає, і саме там рішення справді ухвалюють.
	//
	// Вікно те саме taskSoonDays, що й у черги задач: «скоро» в застосунку
	// одне, і друге число поруч розійшлося б із ним при першій же правці.
	Pinnable bool `json:"pinnable,omitempty"`

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
	// debtCaps — чи діє стеля подушки на час боргу. Прапорцем із документа,
	// а не перерахунком: правило одне на застосунок (state_debts.go).
	debtCaps bool
	// Борг у проході вперед: скільки лишилось під ставкою й скільки з
	// місячної стелі дострокового ще не віддано. Без цих двох чисел
	// маршрут гнав би гроші в борг усі дванадцять місяців поспіль — рівно
	// та вада, яку вже виправляли подушці й цілям.
	debtLeft    float64
	debtFillNow float64
	debtFillMon float64
	month       string
	kindUAH     map[string]float64
	// goals — КОПІЯ рядків документа: прохід уперед мутує їхні розриви й
	// місячні частки, а джерело лишається недоторканим (той самий довід, що
	// при doc() нижче).
	//
	// goalsUAH росте на кожну вирізку, як і reserveUAH: гроші, відкладені в
	// ціль на кроці N, уже в ній лежать на кроці N+1.
	goals    []state.Goal
	goalsUAH float64
}

func newRouteCarry(doc *state.Doc, today domain.Date) *routeCarry {
	c := &routeCarry{
		base: doc, set: doc.Settings,
		// Стеля подушки на час боргу — тим самим правилом, що в документі.
		// Прапорець береться з уже порахованої картки резерву, а не
		// зважується вдруге: друге означення розійшлося б із першим на
		// першому ж місяці проходу.
		debtCaps:   doc.Reserve != nil && doc.Reserve.DebtCapped,
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
	// Цілі беруться з документа ЯК Є — разом із уже покладеним цього
	// місяця, з тієї ж причини, що й подушка: перерахувати їх тут означало
	// б втратити moved і розійтися з карткою на першому ж рядку.
	c.goals = append([]state.Goal(nil), doc.Goals...)
	c.goalsUAH = doc.GoalsUAH
	if dp := doc.Debt; dp != nil {
		// Борг — так само ЯК Є: разом із уже сплаченим достроково цього
		// місяця. Той самий довід, що в подушки й цілей.
		c.debtLeft, c.debtFillNow, c.debtFillMon = dp.TotalUAH, dp.FillNowUAH, dp.FillMonthUAH
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
	if c.base.Debt != nil {
		dp := *c.base.Debt
		dp.TotalUAH, dp.FillNowUAH, dp.FillMonthUAH = c.debtLeft, c.debtFillNow, c.debtFillMon
		d.Debt = &dp
	}
	d.GoalsUAH = c.goalsUAH
	if len(c.goals) > 0 {
		g := make([]state.Goal, len(c.goals))
		copy(g, c.goals)
		d.Goals = g
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
	c.fillMonth, c.fillNow = reserveMonthShare(c.set, c.reserveUAH, mp, 0, c.debtCaps)

	// Стеля дострокового погашення — теж частка ОДНОГО МІСЯЦЯ, і без цього
	// скидання прохід уперед віддав би річну норму за перші два купони.
	// Формула та сама, що в buildDebtPlan: частка від ДОЗВОЛЕНОЇ частини
	// плану, обрізана самим боргом.
	c.debtFillMon, c.debtFillNow = 0, 0
	if c.set != nil && c.set.DebtFillSharePct != nil && mp != nil && c.debtLeft > 0 {
		if share := *c.set.DebtFillSharePct; share > 0 && mp.PlanDebtUAH > 0 {
			c.debtFillMon = round2(math.Min(mp.PlanDebtUAH*share/100, c.debtLeft))
			c.debtFillNow = c.debtFillMon
		}
	}

	// Цілі — тим самим правилом і тією самою функцією, що й у документі.
	// Стеля цілей це теж частка ОДНОГО МІСЯЦЯ, і без цього скидання прохід
	// уперед роздав би річну норму за перші два купони.
	//
	// Потрібний темп (RequiredUAH) при цьому лишається таким, яким його
	// порахували на СЬОГОДНІ, і це свідоме спрощення: перераховувати його
	// щомісяця означало б вести за собою ще й «скільки місяців лишилось»,
	// а сходиться воно й так — GoalsFill обрізає потребу розривом, який на
	// кожному кроці меншає.
	if len(c.goals) > 0 && mp != nil {
		for i := range c.goals {
			c.goals[i].MovedUAH = 0
			c.goals[i].FillMonthUAH, c.goals[i].FillNowUAH = 0, 0
			c.goals[i].ShortMonthUAH = 0
		}
		// Дозволеною частиною, тією самою, що в документі: інакше прохід
		// уперед рахував би стелю цілей від усього плану, а картка — від
		// звуженого, і дві сторінки називали б різні числа про той самий
		// місяць.
		// Прапорець боргу — той самий, що тримає стелю подушки: пауза цілей
		// у проході вперед мусить діяти так само, як у документі.
		state.GoalsFill(c.set, c.goals, mp.PlanGoalsUAH, c.debtCaps)
	}
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
	if p.Debt != nil && p.Debt.AmountUAH > 0 {
		v := p.Debt.AmountUAH
		c.debtLeft = math.Max(0, c.debtLeft-v)
		c.debtFillNow = math.Max(0, c.debtFillNow-v)
	}
	for _, gc := range p.Goals {
		if gc.AmountUAH <= 0 {
			continue
		}
		c.goalsUAH += gc.AmountUAH
		for i := range c.goals {
			if c.goals[i].ID != gc.ID {
				continue
			}
			c.goals[i].GapUAH = math.Max(0, c.goals[i].GapUAH-gc.AmountUAH)
			c.goals[i].FillNowUAH = math.Max(0, c.goals[i].FillNowUAH-gc.AmountUAH)
			break
		}
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
// Повним, а не самою датою: сортування нестабільне, дві виплати одного дня
// в різних брокерів інакше ставали б у порядку обходу мапи, і два запуски
// на тих самих даних дали б різні маршрути. Та сама пастка описана в шапці
// state_schedule.go — з тією різницею, що там ключ свідомо лишили неповним.
//
// І стабільним понад те, з того самого доводу, що в sortFlows: відколи в
// маршруті по нозі на плановий потік, повний ключ уже не унікальний — два
// потоки з однією назвою й одним днем ідуть без брокера й розрізняються
// лише дозволом, якого компаратор не бачить.
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
	sort.SliceStable(out, func(i, j int) bool {
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
	// eligible — скільки з minor подушка має право взяти за політикою
	// reserve_fill_from. Нуль при "any" не буває: там дозволено все, і
	// число просто дорівнює горщику.
	//
	// ПОКУПКИ ЇДЯТЬ СПЕРШУ НЕДОЗВОЛЕНЕ, і це правило, а не округлення.
	// Стеля подушки щомісяця скидається, тож планові гроші, яких вона цього
	// місяця вже не змогла взяти, лишаються її здобиччю наступного; віддати
	// їх паперам першими означало б, що подушка втратила свою чергу через
	// те, що в тому ж горщику випадково опинився купон.
	//
	// Змішані горщики при цьому рідкість за побудовою: планова подія йде
	// без брокера ("—"), а виплати портфеля — на рахунок свого. Зійтись
	// вони можуть лише там, де в лота немає каналу.
	eligible int64
	// goalsEligible — те саме для цілей накопичення, ОКРЕМИМ числом.
	//
	// Окремим, бо goals_fill_from — свій ключ: подушку можна наповнювати
	// лише зарплатою, а цілі — усім, що прийде. Спільне число віддало б
	// обом найсуворішу з двох політик.
	goalsEligible int64
	// debtEligible — те саме для дострокового погашення боргу. Третє число,
	// а не спільне з попередніми: дозволи незалежні, і зарплата, яку можна
	// класти в подушку, не обовʼязково дозволена на борг.
	debtEligible int64
}

// spend знімає віддані гроші з усіх лічильників дозволу горщика.
func (p *routePot) spend(uah, rate float64) {
	if uah <= 0 || rate <= 0 {
		return
	}
	minor := int64(math.Round(uah / rate * 100))
	for _, c := range []*int64{&p.eligible, &p.debtEligible, &p.goalsEligible} {
		if *c -= minor; *c < 0 {
			*c = 0
		}
	}
}

// mergeBasis — основа горщика після того, як у нього впала подія.
//
// Різні основи не «перемагають» одна одну й не усереднюються: горщик, у
// якому зійшлись зобовʼязання й оцінка, каже про це прямо. Обрати одну з
// двох означало б сховати саме те, заради чого колонка й існує.
func mergeBasis(pot, ev string) string {
	switch pot {
	case "":
		return ev
	case ev:
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
			"ні купонів, ні погашень, ні відсотків вкладів, — " +
			"а план доходу порожній або вже виконаний"
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
		// Джерело події — те саме слово, що його шле кнопка «Прийшло», і те
		// саме означення дозволеного, що в POST /api/allocate. Друга копія
		// вирішувала б за подушку інакше, ніж модалка того самого дня.
		src := allocFromPortfolio
		if flowBasis(ev.readyFlow) == basisPlan {
			src = allocFromPlan
		}
		// ДОЗВІЛ ДЖЕРЕЛА ТЕПЕР ПЕРЕДАЄТЬСЯ, і аргумент, який стояв тут проти
		// цього, зник разом зі зведеною ногою місяця. Він казав: заборонити
		// НПФ «наполовину» не можна, а вирішувати за людину, чи вважати
		// місяць забороненим, коли одна зарплата з трьох так позначена,
		// застосунок права не має. Відколи planAhead дає по нозі на потік,
		// ділити нема чого — у ноги рівно один дозвіл, її власний, і «сюди
		// можна / сюди ні» нарешті стосується всієї її суми.
		//
		// ГРОШОВИХ СТЕЛЬ ЦЕ НЕ ДУБЛЮЄ, і другим зрізом того самого числа не
		// є. Стеля місяця вже звужена дозволом у buildMonthPlan
		// (plan_reserve_uah, plan_goals_uah), а тут ріжеться не стеля, а
		// конкретна нога. Відібрати в подушки те, що їй стеля дала, воно не
		// може за побудовою: витрати місяця відняті від стелі ПОВНІСТЮ, а від
		// ніг — лише пропорційно їхнім часткам, тож сума дозволених ніг
		// завжди не менша за стелю (доведення — у шапці planAhead). Місяць, у
		// якому весь дохід позначений «не в подушку», і далі не дає їй
		// нічого — але тепер це видно з обох боків однаково.
		//
		// ДОЗВІЛ — ЦІЄЇ ПОДІЇ, А НЕ ГОРЩИКА, і наслідок треба знати вголос.
		// Перенесені гроші їдуть далі без свого дозволу так само, як їдуть
		// без імені (Via) і без власної основи: щойно частина пішла в діло,
		// приписати залишок конкретному потоку вже не можна. Тобто гроші
		// потоку, позначеного «лише подушка й цілі», яких подушка того місяця
		// не змогла взяти, наступного разу підуть у папери разом із рештою
		// горщика. Лічильник, який би це стеріг, тут не заводиться
		// заздалегідь: другий випадок — привід виносити спільне, перший — ні
		// (CLAUDE.md §3).
		//
		// Різниця з ручною розкладкою (POST /api/allocate) лишилась одна — і
		// вона про те, ЗВІДКИ береться дозвіл: там його читають зі сховища за
		// source_ref, тут він приїхав із подією.
		evEligible := reserveEligibleUAH(doc.Settings, src, amountUAH, principalUAH,
			sourceCapUAH(ev.Uses, domain.UsePlanReserve, amountUAH))
		evDebtEligible := debtEligibleUAH(doc.Settings, src, amountUAH, principalUAH,
			sourceCapUAH(ev.Uses, domain.UsePlanDebt, amountUAH))
		evGoalsEligible := goalsEligibleUAH(doc.Settings, src, amountUAH, principalUAH,
			sourceCapUAH(ev.Uses, domain.UsePlanGoals, amountUAH))

		carryIn := pot.minor
		pot.minor += ev.Amount
		pot.eligible += int64(math.Round(evEligible / rate * 100))
		if pot.eligible > pot.minor {
			pot.eligible = pot.minor
		}
		pot.debtEligible += int64(math.Round(evDebtEligible / rate * 100))
		if pot.debtEligible > pot.minor {
			pot.debtEligible = pot.minor
		}
		pot.goalsEligible += int64(math.Round(evGoalsEligible / rate * 100))
		if pot.goalsEligible > pot.minor {
			pot.goalsEligible = pot.minor
		}
		pot.basis = mergeBasis(pot.basis, flowBasis(ev.readyFlow))
		pot.pending = append(pot.pending, readyEvent{
			Date: string(ev.Date), Label: ev.Label,
			Amount: toMoneyJSON(money.New(ev.Amount, cur)),
		})

		carryInUAH := float64(carryIn) / 100 * rate
		potUAH := float64(pot.minor) / 100 * rate
		// Uses — ЦІЄЇ ПОДІЇ, і тепер він тут є: дозвіл ПО ВИДАХ інструментів
		// («ця зарплата не в пенсійний») ділиться не краще за грошовий, а
		// ділити його більше й не треба — нога стала одним потоком. Довід
		// цілком — при evEligible вище.
		plan := allocatePlan(carry.doc(carryInUAH), sug, rates,
			toMoneyJSON(money.New(pot.minor, cur)), potUAH,
			allocAllow{
				ReserveUAH: float64(pot.eligible) / 100 * rate,
				DebtUAH:    float64(pot.debtEligible) / 100 * rate,
				GoalsUAH:   float64(pot.goalsEligible) / 100 * rate,
				Uses:       ev.Uses,
			}, cur, npfID)
		carry.apply(plan)
		// Будь-яка вирізка зменшує ВСІ ТРИ лічильники: гроші не можна
		// віддати двічі, а політики в них незалежні. Три однакові блоки по
		// два оновлення розійшлися б на першій же правці, тож це один
		// прохід.
		if plan.Reserve != nil {
			pot.spend(plan.Reserve.AmountUAH, rate)
		}
		if plan.Debt != nil {
			pot.spend(plan.Debt.AmountUAH, rate)
		}
		pot.spend(plan.GoalsUAH, rate)
		// Дохід стає капіталом аж тепер — аргумент при earn.
		carry.earn(amountUAH - principalUAH)

		leg := routeLeg{
			Date: string(ev.Date), Broker: ev.bc.Broker, Currency: cur,
			Label: ev.Label, Ref: ev.Ref, allocPlan: plan,
			CarryInUAH:   round2(carryInUAH),
			InflowUAH:    round2(amountUAH),
			PrincipalUAH: round2(principalUAH),
			InflowWhy:    ev.Why,
			Basis:        pot.basis,
		}
		if ev.Principal > 0 {
			p := toMoneyJSON(money.New(ev.Principal, cur))
			leg.Principal = &p
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
		if pot.eligible > pot.minor {
			pot.eligible = pot.minor
		}
		out.Legs = append(out.Legs, leg)
	}
	return out
}

// annotatePlanned дописує до ніг те, що вже стоїть у плані купівель.
//
// ОКРЕМИМ ПРОХОДОМ, а не всередині buildRoute, і з тієї самої причини, з
// якої annotateReady живе окремо від reinvestSuggestions: сам маршрут — це
// прохід над розкладом і політикою, а план купівель до його арифметики
// стосунку не має. Він лише каже, чи ти вже щось про цю дату вирішив.
//
// ЗБІГ ЗА ДАТОЮ, РАХУНКОМ І ВАЛЮТОЮ — усіма трьома. За самою датою було б
// хибно: 28 жовтня в цьому портфелі три різні ноги в двох брокерів, і
// позначка на всіх трьох через один закріплений рядок читалась би як
// «усе вирішено». Рядок, заведений руками без брокера, сюди не потрапить —
// позначка це зручність, а не облік, і мовчазний недооблік тут дешевший за
// впевнене «вже в плані» на чужій нозі.
//
// ВІДНІМАННЯ ЗАКРІПЛЕНОГО З ГОРЩИКА НЕМАЄ, і це не пропуск. Маршрут —
// вигляд, а не стан: у прогноз він не входить, у знімок не потрапляє, тож
// подвійного рахунку немає ніде. Відняти означало б завести тут власне
// правило «скільки з цих грошей уже витрачено», якого більше ніхто не знає.
func annotatePlanned(legs []routeLeg, buys []store.PlanBuy, today domain.Date) {
	soon := string(today.AddDays(taskSoonDays))
	for i := range legs {
		leg := &legs[i]
		leg.Planned = nil
		for _, b := range buys {
			if b.BuyDate == "" {
				continue // «купую зараз» — не про майбутню ногу
			}
			if string(b.BuyDate) != leg.Date || b.Broker != leg.Broker {
				continue
			}
			// Валюта звіряється, ЛИШЕ коли вона в рядку названа. Порожня в
			// plan_buys означає «вивести із сутності» — папір і фонд знають
			// свою валюту самі, — і вимагати збігу з порожнечею означало б
			// ніколи не побачити власного ж закріплення. Брокер натомість
			// строгий: саме він розрізняє три ноги того самого дня.
			if b.Currency != "" && b.Currency != leg.Currency {
				continue
			}
			leg.Planned = append(leg.Planned, b.Ref)
		}
		// Закріплювати нічого, якщо в нозі немає жодного рядка, який кладеться
		// в план одним рухом: у вкладу такого рядка немає взагалі (див.
		// allocLine.Addable), а порожня кнопка гірша за її відсутність.
		addable := false
		for _, l := range leg.Lines {
			if l.Addable {
				addable = true
				break
			}
		}
		leg.Pinnable = addable && len(leg.Planned) == 0 && leg.Date <= soon
	}
}
