package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Бекап користувацьких даних. Бекапимо лише те, що введено РУКАМИ й
// невідновне: лоти, продажі, поповнення, конвертації, вклади, рухи
// резерву, план (потоки й дії), довідники, налаштування, статуси виплат і
// добові знімки. Довідник НБУ, графіки виплат і курси — похідні,
// повертаються командою «Оновити НБУ», тож у бекап не входять.
//
// Формат — сирі колонки в мінорних одиницях (як у БД), із збереженими ID:
// продажі посилаються на лоти за id, і при відновленні цей зв'язок має
// лишитись. Це найнадійніший формат — стабільний і портабельний.
//
// Бекап — завжди ОДИН портфель (0054): ExportAll читає лише рядки з
// portfolio_id поточного Store, а ImportAll стирає й заповнює той портфель,
// на який націлений запит, не чіпаючи сусідні. Довідник фондів і позначки
// ціни спільні для всіх портфелів, але входять у кожен дамп — щоб файл був
// самодостатнім і відновлювався на порожній базі без другого файла.
//
// Спільний каталог фондів restore витирає ЛИШЕ там, де після очищення
// цього портфеля фондом не користується ніхто (pruneOrphanFundsIn): фонд,
// у якому сидить сусід, витерти не можна — FK, — і його позначки ціни
// лишаються. На одному портфелі це рівно старий контракт «відновлення
// заміщує». Фонд із файла зводиться за назвою: якого немає — створюється,
// наявний — переписується атрибутами з файла, тобто restore сателіта
// переписує ГЛОБАЛЬНІ параметри спільного фонду; його позначки лягають
// upsert-ом поверх наявних.
//
// І про id. Усі таблиці ділять одну послідовність AUTOINCREMENT, а бекап
// тримає id (продажі посилаються на лоти саме за ними). Відновлений в
// інший портфель, дамп упирався б у UNIQUE(id) на першому ж рядку, чий id
// уже зайнятий сусідом. Тому id зберігається, коли вільний (порожня база,
// той самий портфель — усе як доти), і замінюється новим, коли зайнятий,
// а діти перечіплюються за мапою (idMaps).

const BackupSchema = 1

type Backup struct {
	Schema     int    `json:"schema"`
	App        string `json:"app"`
	ExportedAt string `json:"exported_at"`
	// Portfolio — назва портфеля-джерела (0054), лише для людини: restore
	// її не читає, бо відновлює в той портфель, на який націлений запит.
	Portfolio   string             `json:"portfolio,omitempty"`
	Lots        []BackupLot        `json:"lots"`
	Sales       []BackupSale       `json:"sales"`
	Deposits    []BackupDeposit    `json:"deposits"`
	Conversions []BackupConversion `json:"conversions"`
	FundOps     []BackupFundOp     `json:"fund_ops"`
	// TermDeposits omitempty: бекапи, зроблені до появи вкладів, читаються
	// без цього поля так само, як раніше — restore просто не створить
	// жодного вкладу.
	TermDeposits []BackupTermDeposit `json:"term_deposits,omitempty"`
	// DepositTopups omitempty з тієї ж причини: старіші бекапи їх не мають.
	DepositTopups []BackupDepositTopup `json:"deposit_topups,omitempty"`
	// ReserveOps omitempty з тієї ж причини: бекапи до появи резерву його
	// не мають, і restore просто не створить жодного руху.
	ReserveOps []BackupReserveOp `json:"reserve_ops,omitempty"`
	// Цілі накопичення (0039) — ДВА поля, бо сутностей дві: сама ціль і
	// журнал під нею. Невідновні так само, як резерв, і навіть трохи
	// гірше: рух резерву хоч видно за просілим капіталом, а «збираю на
	// авто $20 000 до березня» не виводиться нізвідки взагалі — ні з
	// операцій, ні з плану.
	Goals   []BackupGoal   `json:"goals,omitempty"`
	GoalOps []BackupGoalOp `json:"goal_ops,omitempty"`
	// Борги (0045) — ТРИ поля, бо сутностей три: сам борг, журнал рухів і
	// звірки з банком. Невідновні навіть суворіше за цілі: умови картки
	// (розрахункова дата, ставка після пільгового, підвищена за
	// прострочення, мінімалка) не виводяться нізвідки взагалі — вони
	// живуть у договорі, а не в числах застосунку. А без звірок баланс
	// картки не відновлюється навіть приблизно: журнал веде лише великі
	// рухи, і саме звірка є тим, від чого він відлічується.
	Debts     []BackupDebt     `json:"debts,omitempty"`
	DebtOps   []BackupDebtOp   `json:"debt_ops,omitempty"`
	DebtMarks []BackupDebtMark `json:"debt_marks,omitempty"`
	// План (фаза 9) — так само невідновний журнал, і так само omitempty:
	// бекапи, зроблені до його появи, читаються без цих полів.
	PlanFlows   []BackupPlanFlow   `json:"plan_flows,omitempty"`
	PlanActions []BackupPlanAction `json:"plan_actions,omitempty"`
	// PlanBuys (0033) — план купівель. Так само невідновний: намір «узяти
	// цей папір у березні» не виводиться ні з операцій, ні з потоків, бо
	// операції ще немає, а потік описує гроші, а не інструмент.
	PlanBuys []BackupPlanBuy `json:"plan_buys,omitempty"`
	// Decisions (0035) — журнал рішень: що радив помічник у день купівлі.
	// Найневідновніше з усього тут: рейтинг перераховується щодня з
	// поточного довідника, курсів і часток, тож учорашній не відтворити
	// НІЯК — не «важко», а неможливо в принципі. Без цього поля restore
	// стер би саме ту єдину копію, задля якої таблиця й існує.
	Decisions []BackupDecision `json:"decisions,omitempty"`
	// Журнал ревізій потоків (0026). Без нього відновлення тихо стирало б
	// історію правок — а на ній стоїть увесь сенс картки «План проти
	// факту»: план за минулий місяць читається з журналу, і без журналу він
	// знову почав би виводитись із теперішньої таблиці, тобто минуле знову
	// стало б переписуваним.
	PlanFlowRevisions []BackupPlanFlowRevision `json:"plan_flow_revisions,omitempty"`
	// Відмітки надходжень (0027) — журнал, якого немає більше ніде: чи
	// прийшла зарплата, не вивести ні з поповнень, ні з потоків. Без нього
	// відновлення стерло б і минулі відмітки, і майбутні нулі, а прогноз
	// тихо повернувся б до рівного плану — тобто числа лишились би
	// правдоподібними й неправильними.
	PlanReceipts []BackupPlanReceipt `json:"plan_receipts,omitempty"`
	// НПФ (0028). Три поля, і кожне невідновне окремо.
	//
	// NPFAccounts — не «довідник, що відновиться з назв в операціях», як
	// funds: у npf_ops назви немає взагалі, зв'язок лише за id. Та й
	// відновлювати там нічого — ЧВОПА, дата доступу, ставка знижки й день
	// внеску не виводяться ні з чого. Без цього поля restore залишив би
	// капітал заниженим на весь пенсійний баланс.
	//
	// NPFNav — найдорожчі за працею дані в усій базі й єдині, яких не
	// відновити з жодної виписки: опублікована фондом історія ЧВОПА за
	// багато років, вклеєна руками. Втратити її означає втратити track
	// record, на якому стоїть уся обіцяна дохідність.
	NPFAccounts []BackupNPFAccount `json:"npf_accounts,omitempty"`
	NPFOps      []BackupNPFOp      `json:"npf_ops,omitempty"`
	NPFNav      []BackupNPFNav     `json:"npf_nav,omitempty"`
	// FundPrices (0034) — позначки ціни сертифіката, вклеєні руками. Так
	// само невідновні: для накопичувального фонду виписка приносить ціну
	// лише в мить купівлі, а купують такий папір раз. Без цього поля
	// відновлення тихо повертало б ринкову вартість до собівартості, а
	// дохідність — до нуля, і числа лишились би правдоподібними.
	FundPrices []BackupFundPrice `json:"fund_prices,omitempty"`
	// Довідники. omitempty з тієї ж причини, що й усе вище: бекапи, зроблені
	// до їхньої появи, читаються без цих полів так само, як раніше — фонди й
	// брокери відновляться з назв в операціях, рівно як доти.
	// ImportProfiles (0036) — розкладка колонок чужої виписки. Так само
	// невідновна: індекси колонок і словник операцій людина вивіряє по
	// власному файлу руками, і з жодних операцій їх не вивести. Без цього
	// поля restore тихо повертав би імпорт до однієї-єдиної виписки.
	ImportProfiles []BackupImportProfile `json:"import_profiles,omitempty"`
	Funds          []BackupFund          `json:"funds,omitempty"`
	Brokers        []BackupBroker        `json:"brokers,omitempty"`
	Settings       map[string]string     `json:"settings"`
	PaymentStatus  []BackupPayStatus     `json:"payment_status"`
	Snapshots      []Snapshot            `json:"snapshots"`
}

// BackupFund — рядок довідника фондів (refs.go: Fund).
//
// Довідник довго відновлювався ПОБІЧНИМ ЕФЕКТОМ: фонд заводився з назви й
// валюти першої-ліпшої операції, а все, чого з операцій не вивести —
// обіцяна дохідність, її валюта й день виплати, — мовчки ставало нулем.
// Наслідок був не косметичний. У фонду з обіцянкою 9.5% USD відновлення
// збивало yield_basis з «обіцяно фондом» на «дивіденди після податку»,
// прибирало з календаря всі оцінені виплати (12 → 0) і разом із ними
// next_payout, а income_monthly_now просідав на чверть. Тобто restore тихо
// змінював числа портфеля — найгірший різновид втрати, бо цифри лишаються
// правдоподібними.
//
// Ключ — НАЗВА, а не id: бекап тримається назв усюди (див. ExportAll), і
// саме тому формат пережив нормалізацію.
type BackupFund struct {
	Name     string `json:"name"`
	Currency string `json:"currency"`
	// Три поля нижче — те, чого з операцій не вивести взагалі. omitempty:
	// у фонду без заданої обіцянки вони нулі, і дамп виглядає як раніше.
	ExpectedYieldBP  int64  `json:"expected_yield_bp,omitempty"`
	ExpectedYieldCur string `json:"expected_yield_currency,omitempty"`
	PayoutDay        int64  `json:"payout_day,omitempty"`
	// Строк, вид і податок — так само невивідні з операцій (міграція
	// 0022). Журнал купівель нічого не знає ні про те, що фонд
	// закривається в липні 2029, ні про те, що обіцянка задана простою.
	Kind             string `json:"kind,omitempty"`
	CloseDate        string `json:"close_date,omitempty"`
	BuyUntil         string `json:"buy_until,omitempty"`
	IncomeTaxBP      int64  `json:"income_tax_bp,omitempty"`
	YieldSimpleYears int64  `json:"yield_simple_years,omitempty"`
	ExitTaxBP        int64  `json:"exit_tax_bp,omitempty"`
}

// BackupNPFAccount — пенсійний рахунок. Ключ — ID, а не назва, попри
// загальне правило бекапу: npf_ops і npf_nav посилаються на рахунок лише
// за id, і назви в них немає, з якої зв'язок можна було б відтворити. Той
// самий випадок, що term_deposits, і з тієї ж причини — це водночас
// інструмент і батько журналу.
type BackupNPFAccount struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Administrator string `json:"administrator,omitempty"`
	Currency      string `json:"currency"`
	// NavE6 / NavDate — остання відома ЧВОПА ×10⁶. Не виводиться з
	// операцій, коли її оновлювали руками з кабінету.
	NavE6   int64  `json:"nav_e6,omitempty"`
	NavDate string `json:"nav_date,omitempty"`
	// Решта — те, чого з журналу не вивести взагалі: обіцянка, дата
	// доступу, обидва податки й день нагадування.
	ExpectedYieldBP  int64  `json:"expected_yield_bp,omitempty"`
	YieldSimpleYears int64  `json:"yield_simple_years,omitempty"`
	AccessDate       string `json:"access_date,omitempty"`
	IncomeTaxBP      int64  `json:"income_tax_bp,omitempty"`
	CreditRateBP     int64  `json:"credit_rate_bp,omitempty"`
	ContribDay       int64  `json:"contrib_day,omitempty"`
	// Строк і періодичність виплати (0031). Не виводяться з журналу
	// взагалі: це умови договору, а не наслідок внесків.
	PayoutYears int64  `json:"payout_years,omitempty"`
	PayoutFreq  string `json:"payout_freq,omitempty"`
	Note        string `json:"note,omitempty"`
}

// BackupNPFOp — внесок. UnitsE6 обовʼязкові: без них не вивести ні
// залишку одиниць, ні ЧВОПА на дату внеску, тобто позиція перетворилась би
// на суму без вартості.
type BackupNPFOp struct {
	ID      int64  `json:"id"`
	NPFID   int64  `json:"npf_id"`
	Date    string `json:"date"`
	UnitsE6 int64  `json:"units_e6"`
	Amount  int64  `json:"amount"`
	Broker  string `json:"broker,omitempty"`
	Note    string `json:"note,omitempty"`
}

// BackupNPFNav — одна точка ЧВОПА, заведена руками.
type BackupNPFNav struct {
	ID    int64  `json:"id"`
	NPFID int64  `json:"npf_id"`
	Date  string `json:"date"`
	NavE6 int64  `json:"nav_e6"`
}

// BackupFundPrice — одна позначка ціни сертифіката.
//
// Ключ — НАЗВА фонду, а не fund_id, і це розбіжність із BackupNPFNav поруч.
// Вона навмисна: у npf_ops назви немає взагалі, зв'язок лише за id, а тут
// funds.name — UNIQUE і вже є ключем усього бекапу фондів (BackupFundOp
// тримається так само). Саме через назви формат і пережив нормалізацію
// 0010.
//
// ID не тримаємо з тієї ж причини: на позначку ніхто не посилається, а
// UNIQUE(fund_id, date) і так робить рядок неповторним — тож формат не
// залежить від id-шників бази-джерела.
type BackupFundPrice struct {
	Fund    string `json:"fund"`
	Date    string `json:"date"`
	PriceE4 int64  `json:"price_e4"`
}

// BackupBroker — рядок довідника брокерів.
//
// Самих назв в операціях мало: брокер, заведений наперед і ще не вжитий у
// жодному записі, не згадується ніде, тож відновлення його просто губило —
// разом із випадайкою, до якої людина звикла.
type BackupBroker struct {
	Name string `json:"name"`
}

// BackupReserveOp — рух резерву. Резерв невідновний так само, як лоти:
// це журнал, який більше ніде не записаний, і без нього restore залишив
// би капітал заниженим рівно на суму матраца.
type BackupReserveOp struct {
	ID       int64  `json:"id"`
	Date     string `json:"date"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Place    string `json:"place"`
	Note     string `json:"note"`
}

// BackupGoal — ціль накопичення. Разом із BackupGoalOp нижче це та сама
// пара «запис + журнал», що в пенсійного рахунку з внесками, і порядок
// вставки тут так само значущий: goal_ops має FK на goals.
type BackupGoal struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	TargetAmount int64  `json:"target_amount"`
	Currency     string `json:"currency"`
	DueDate      string `json:"due_date"`
	Priority     int64  `json:"priority"`
	Place        string `json:"place"`
	Note         string `json:"note"`
	DoneDate     string `json:"done_date"`
}

// BackupGoalOp — один рух під ціллю.
type BackupGoalOp struct {
	ID       int64  `json:"id"`
	GoalID   int64  `json:"goal_id"`
	Date     string `json:"date"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Place    string `json:"place"`
	Note     string `json:"note"`
}

// BackupDebt — картка або розстрочка. Поля рівно ті, що в таблиці:
// звужувати їх до «потрібних» не можна, бо мертві для одного виду колонки
// живі для другого, і бекап мусить пережити обидва.
type BackupDebt struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Currency string `json:"currency"`
	// CardID нулем = самостійний борг (у базі NULL), як і в store.Debt.
	CardID           int64  `json:"card_id,omitempty"`
	LimitAmount      int64  `json:"limit_amount,omitempty"`
	StatementDay     int64  `json:"statement_day,omitempty"`
	APRBp            int64  `json:"apr_bp,omitempty"`
	APROverdueBp     int64  `json:"apr_overdue_bp,omitempty"`
	MinPaymentBp     int64  `json:"min_payment_bp,omitempty"`
	MinPaymentFloor  int64  `json:"min_payment_floor,omitempty"`
	LateFee          int64  `json:"late_fee,omitempty"`
	ExitBy           string `json:"exit_by,omitempty"`
	Principal        int64  `json:"principal,omitempty"`
	PaymentsTotal    int64  `json:"payments_total,omitempty"`
	FirstPaymentDate string `json:"first_payment_date,omitempty"`
	FeeMonthBp       int64  `json:"fee_month_bp,omitempty"`
	FeeFreeMonths    int64  `json:"fee_free_months,omitempty"`
	FeeOnPrepay      string `json:"fee_on_prepay,omitempty"`
	OpenedDate       string `json:"opened_date,omitempty"`
	ClosedDate       string `json:"closed_date,omitempty"`
	Place            string `json:"place,omitempty"`
	Note             string `json:"note,omitempty"`
}

// BackupDebtOp — один рух під боргом.
type BackupDebtOp struct {
	ID     int64  `json:"id"`
	DebtID int64  `json:"debt_id"`
	Date   string `json:"date"`
	Kind   string `json:"kind"`
	Amount int64  `json:"amount"`
	Note   string `json:"note,omitempty"`
}

// BackupDebtMark — одна звірка з додатком банку.
type BackupDebtMark struct {
	ID     int64  `json:"id"`
	DebtID int64  `json:"debt_id"`
	Date   string `json:"date"`
	// Balance без omitempty: нуль тут осмислена відповідь («на картці
	// рівно нуль»), і сховати його означало б показати відсутність поля
	// там, де є виміряне число.
	Balance      int64  `json:"balance"`
	StatementDue int64  `json:"statement_due,omitempty"`
	NonGrace     int64  `json:"non_grace,omitempty"`
	Note         string `json:"note,omitempty"`
}

// BackupPlanFlow — джерело доходу чи витрат із розділу «План». Невідновне
// так само, як резерв: цього журналу немає більше ніде, а без нього
// проєкція повертається до гіпотетичного рівного внеску й тихо показує
// зовсім інше майбутнє.
type BackupPlanFlow struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Cadence   string `json:"cadence"`
	FromDate  string `json:"from_date"`
	UntilDate string `json:"until_date"`
	GrowthBP  int64  `json:"growth_bp"`
	InvestBP  int64  `json:"invest_bp"`
	// Dest — куди потік потрапляє: "" = ліквідний портфель, "npf:<id>" =
	// пенсійний рахунок (0030). Без нього внесок у пенсійний був би
	// невідрізнимий від квартплати, і відновлення тихо перетворило б
	// пенсійний капітал у зʼїдені гроші.
	Dest string `json:"dest,omitempty"`
	// Uses — на що ці гроші можуть піти: "" = будь-куди (0041). Втратити
	// його означало б повернути з бекапу план, у якому подушка знову має
	// право на кожен дохід, — тобто мовчки скасувати заборону.
	Uses string `json:"uses,omitempty"`
	Note string `json:"note"`
}

// BackupPlanFlowRevision — рядок журналу правок потоку.
//
// FlowID навмисно НЕ посилається на живий потік: ревізія переживає той
// потік, про який вона розповідає, і саме тому видалення взагалі можна
// відрізнити від «ніколи не було».
type BackupPlanFlowRevision struct {
	ID        int64  `json:"id"`
	FlowID    int64  `json:"flow_id"`
	ChangedAt string `json:"changed_at"`
	Op        string `json:"op"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	Cadence   string `json:"cadence"`
	FromDate  string `json:"from_date"`
	UntilDate string `json:"until_date"`
	GrowthBP  int64  `json:"growth_bp"`
	InvestBP  int64  `json:"invest_bp"`
	// Dest — те саме, що в потоці, і в журналі воно ОБОВʼЯЗКОВЕ: без
	// нього реконструкція плану на минулу дату брала б призначення з
	// теперішньої таблиці, тобто переведення внеску на інший рахунок
	// переписувало б усю історію заднім числом.
	Dest string `json:"dest,omitempty"`
	// Uses — те саме, що в потоці, і в журналі воно теж обовʼязкове: від
	// дозволу залежить стеля місяця, тож без нього реконструкція минулого
	// брала б сьогоднішню заборону (аргумент при 0041).
	Uses string `json:"uses,omitempty"`
	Note string `json:"note"`
}

// BackupPlanReceipt — відмітка фактичного надходження.
//
// Amount БЕЗ omitempty, і це не недогляд, а рівно та сама пастка, що
// описана в BackupPlanAction: нуль тут означає «не прийшло» — саме той
// факт, заради якого таблиця й існує. omitempty з'їв би його, і
// відновлення перетворило б записаний нуль на невідмічений місяць.
type BackupPlanReceipt struct {
	ID       int64  `json:"id"`
	FlowID   int64  `json:"flow_id"`
	Month    string `json:"month"`
	Name     string `json:"name"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	InvestBP int64  `json:"invest_bp"`
	Uses     string `json:"uses,omitempty"`
	Note     string `json:"note"`
}

// BackupPlanAction — планова дія (set_shares або lock).
//
// USDBP/EURBP БЕЗ omitempty, і це не недогляд: -1 означає «не задано», а 0
// — «долара не лишається зовсім» (див. 0025_plan.sql). omitempty на int64
// з'їв би нуль і перетворив задану частку на незадану, тобто відновлення
// мовчки змінило б сенс дії.
type BackupPlanAction struct {
	ID       int64  `json:"id"`
	Date     string `json:"date"`
	Type     string `json:"type"`
	USDBP    int64  `json:"usd_bp"`
	EURBP    int64  `json:"eur_bp"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	RateBP   int64  `json:"rate_bp"`
	Months   int64  `json:"months"`
	Name     string `json:"name"`
	Note     string `json:"note"`
}

// BackupPlanBuy — планована купівля (0033). Дзеркалить store.PlanBuy
// колонка в колонку: бекап цієї таблиці — дамп, а не зведення.
type BackupPlanBuy struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Ref       string `json:"ref"`
	Qty       int64  `json:"qty"`
	Amount    int64  `json:"amount"`
	UnitPrice int64  `json:"unit_price"`
	Currency  string `json:"currency"`
	Broker    string `json:"broker"`
	BuyDate   string `json:"buy_date"`
	RateBP    int64  `json:"rate_bp"`
	Months    int64  `json:"months"`
	IsReserve bool   `json:"is_reserve"`
	Note      string `json:"note"`
}

// BackupImportProfile — профіль імпорту (0036). Дзеркалить
// store.ImportProfile колонка в колонку.
type BackupImportProfile struct {
	Name   string `json:"name"`
	Format string `json:"format"`
	Header int    `json:"header"`
	Date   int    `json:"col_date"`
	Op     int    `json:"col_op"`
	Ref    int    `json:"col_ref"`
	Qty    int    `json:"col_qty"`
	Debit  int    `json:"col_debit"`
	Credit int    `json:"col_credit"`
	// Колонки виписки картки (0051). ВКАЗІВНИКИ, і це не педантизм: у
	// бекапі старішої схеми полів немає, і нуль замість них означав би
	// «перша колонка» — залишок читався б із колонки дати. nil при
	// відновленні стає -1 («колонки немає»), як у міграції.
	Balance *int   `json:"col_balance,omitempty"`
	MCC     *int   `json:"col_mcc,omitempty"`
	DebtID  int64  `json:"debt_id,omitempty"`
	Ops     string `json:"ops"`
	Note    string `json:"note"`
}

// BackupDecision — рядок журналу рішень (0035). Дзеркалить
// store.Decision колонка в колонку: бекап цієї таблиці — дамп, а не
// зведення. «Дохідність за фактом» сюди не потрапляє й не мусить — вона
// рахується на льоту з лота й виплат (handlers_decisions.go).
type BackupDecision struct {
	ID         int64   `json:"id"`
	MadeOn     string  `json:"made_on"`
	Kind       string  `json:"kind"`
	Ref        string  `json:"ref"`
	Currency   string  `json:"currency"`
	Amount     int64   `json:"amount"`
	RealPct    float64 `json:"real_pct"`
	RankPos    int64   `json:"rank_pos"`
	TopLabel   string  `json:"top_label"`
	TopRealPct float64 `json:"top_real_pct"`
	RankMode   string  `json:"rank_mode"`
	OpID       int64   `json:"op_id"`
	Note       string  `json:"note"`
}

// BackupDepositTopup — поповнення вкладу. Без нього відновлення втратило б
// докладені суми, і баланс вкладу занизився б до суми відкриття.
type BackupDepositTopup struct {
	ID        int64  `json:"id"`
	DepositID int64  `json:"deposit_id"`
	Date      string `json:"date"`
	Amount    int64  `json:"amount"`
}

// BackupTermDeposit — банківський вклад. Без нього відновлення стерло б
// усі вклади разом зі ставками й строками, а помітно це стало б тоді,
// коли відновлюватись уже нема з чого.
type BackupTermDeposit struct {
	ID           int64  `json:"id"`
	Bank         string `json:"bank"`
	Currency     string `json:"currency"`
	Principal    int64  `json:"principal"`
	RateBP       int64  `json:"rate_bp"`
	OpenDate     string `json:"open_date"`
	MaturityDate string `json:"maturity_date"`
	Payout       string `json:"payout"`
	Capitalized  bool   `json:"capitalized,omitempty"`
	TaxBP        int64  `json:"tax_bp"`
	ClosedDate   string `json:"closed_date,omitempty"`
	ClosedAmount int64  `json:"closed_amount,omitempty"`
	Note         string `json:"note"`
	// Replenishable omitempty: старіші бекапи поля не мають, і відновлення
	// прочитає їх як «не поповнюваний» — це типове значення колонки.
	Replenishable bool `json:"replenishable,omitempty"`
	// IsReserve / Revocable — omitempty з тієї ж причини й з тим самим
	// наслідком: обидва типові значення нульові, тож старіший бекап
	// відновиться в стан «вклад інвестиційний і безвідкличний». Для
	// другого це не наближення, а законне замовчування — за ЦКУ строковий
	// вклад фізособи безвідкличний, доки в договорі не написано інакше.
	IsReserve bool `json:"is_reserve,omitempty"`
	Revocable bool `json:"revocable,omitempty"`
}

// BackupFundOp — операція з сертифікатами фонду. Без неї бекап був
// неповним: відновлення стерло б усю історію фондів разом із дивідендами
// й податками, а помітно це стало б лише тоді, коли відновлюватись уже
// нема з чого.
type BackupFundOp struct {
	ID       int64  `json:"id"`
	Date     string `json:"date"`
	Fund     string `json:"fund"`
	Kind     string `json:"kind"`
	Qty      int64  `json:"qty"`
	Amount   int64  `json:"amount"`
	Tax      int64  `json:"tax"`
	Currency string `json:"currency"`
	Broker   string `json:"broker"`
	// PairID — друга нога конвертації між фондами. omitempty, щоб бекапи
	// без конвертацій виглядали рівно так само, як раніше.
	PairID int64  `json:"pair_id,omitempty"`
	Note   string `json:"note"`
}

type BackupLot struct {
	ID       int64  `json:"id"`
	ISIN     string `json:"isin"`
	Qty      int64  `json:"qty"`
	Price    int64  `json:"price_per_bond"` // мінорні
	Currency string `json:"currency"`
	BuyDate  string `json:"buy_date"`
	Channel  string `json:"channel"`
	Fee      int64  `json:"fee"`
	Note     string `json:"note"`
}

type BackupSale struct {
	ID       int64  `json:"id"`
	LotID    int64  `json:"lot_id"`
	SaleDate string `json:"sale_date"`
	Qty      int64  `json:"qty"`
	Clean    int64  `json:"clean_per_bond"`
	Accrued  int64  `json:"accrued"`
	Currency string `json:"currency"`
	Note     string `json:"note"`
}

type BackupDeposit struct {
	ID       int64  `json:"id"`
	Date     string `json:"date"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Broker   string `json:"broker"`
	Note     string `json:"note"`
}

type BackupConversion struct {
	ID           int64  `json:"id"`
	Date         string `json:"date"`
	FromCurrency string `json:"from_currency"`
	FromAmount   int64  `json:"from_amount"`
	ToCurrency   string `json:"to_currency"`
	ToAmount     int64  `json:"to_amount"`
	Broker       string `json:"broker"`
	Note         string `json:"note"`
}

type BackupPayStatus struct {
	ISIN     string `json:"isin"`
	PayDate  string `json:"pay_date"`
	Status   string `json:"status"`
	MarkedAt string `json:"marked_at"`
}

// Знімок у бекапі — це та сама Snapshot (snapshot.go), із її json-тегами.
//
// Окрема BackupSnapshot тут була доти, і саме вона показує, чому дублі
// небезпечні: довго вона несла лише перші п'ять полів, і відновлення
// мовчки стирало решту — місячну ціль, рахунок і фонди. Помітно це стало
// б тоді, коли відновлюватись уже нема з чого, а крива «Як росте» після
// restore просіла б рівно на вартість фондів.

// ExportAll читає всі користувацькі таблиці в один знімок.
func (s *Store) ExportAll(ctx context.Context) (*Backup, error) {
	b := &Backup{Schema: BackupSchema, App: "oddinvest", Settings: map[string]string{}}
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM portfolios WHERE id=?`, s.pid).Scan(&b.Portfolio); err != nil {
		return nil, fmt.Errorf("портфель %d: %w", s.pid, err)
	}

	// Бекап тримає НАЗВИ брокерів і фондів, а не їхні id. Так формат
	// пережив нормалізацію: файли, вивантажені до появи довідників,
	// відновлюються далі без жодних перетворень.
	if err := s.scan(ctx, `SELECT l.id,l.isin,l.qty,l.price_per_bond,l.currency,l.buy_date,
		COALESCE(b.name,''),l.fee,l.note
		FROM lots l LEFT JOIN brokers b ON b.id=l.broker_id
		WHERE l.portfolio_id=? ORDER BY l.id`,
		func(scan func(...any) error) error {
			var r BackupLot
			if err := scan(&r.ID, &r.ISIN, &r.Qty, &r.Price, &r.Currency, &r.BuyDate, &r.Channel, &r.Fee, &r.Note); err != nil {
				return err
			}
			b.Lots = append(b.Lots, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id,lot_id,sale_date,qty,clean_per_bond,accrued,currency,note FROM sales
		WHERE portfolio_id=? ORDER BY id`,
		func(scan func(...any) error) error {
			var r BackupSale
			if err := scan(&r.ID, &r.LotID, &r.SaleDate, &r.Qty, &r.Clean, &r.Accrued, &r.Currency, &r.Note); err != nil {
				return err
			}
			b.Sales = append(b.Sales, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT d.id,d.date,d.amount,d.currency,COALESCE(b.name,''),d.note
		FROM deposits d LEFT JOIN brokers b ON b.id=d.broker_id
		WHERE d.portfolio_id=? ORDER BY d.id`,
		func(scan func(...any) error) error {
			var r BackupDeposit
			if err := scan(&r.ID, &r.Date, &r.Amount, &r.Currency, &r.Broker, &r.Note); err != nil {
				return err
			}
			b.Deposits = append(b.Deposits, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT c.id,c.date,c.from_currency,c.from_amount,c.to_currency,
		c.to_amount,COALESCE(b.name,''),c.note
		FROM conversions c LEFT JOIN brokers b ON b.id=c.broker_id
		WHERE c.portfolio_id=? ORDER BY c.id`,
		func(scan func(...any) error) error {
			var r BackupConversion
			if err := scan(&r.ID, &r.Date, &r.FromCurrency, &r.FromAmount, &r.ToCurrency, &r.ToAmount, &r.Broker, &r.Note); err != nil {
				return err
			}
			b.Conversions = append(b.Conversions, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT o.id,o.date,f.name,o.kind,o.qty,o.amount,o.tax,f.currency,
		COALESCE(b.name,''),COALESCE(o.pair_id,0),o.note
		FROM fund_ops o JOIN funds f ON f.id=o.fund_id
		LEFT JOIN brokers b ON b.id=o.broker_id
		WHERE o.portfolio_id=? ORDER BY o.id`,
		func(scan func(...any) error) error {
			var r BackupFundOp
			if err := scan(&r.ID, &r.Date, &r.Fund, &r.Kind, &r.Qty, &r.Amount, &r.Tax,
				&r.Currency, &r.Broker, &r.PairID, &r.Note); err != nil {
				return err
			}
			b.FundOps = append(b.FundOps, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT `+termDepositCols()+`
		FROM term_deposits d LEFT JOIN brokers b ON b.id=d.broker_id
		WHERE d.portfolio_id=? ORDER BY d.id`,
		func(scan func(...any) error) error {
			var r BackupTermDeposit
			var capInt, replInt, resInt, revInt int64
			if err := scan(&r.ID, &r.Bank, &r.Currency, &r.Principal, &r.RateBP,
				&r.OpenDate, &r.MaturityDate, &r.Payout, &capInt, &r.TaxBP,
				&r.ClosedDate, &r.ClosedAmount, &r.Note, &replInt,
				&resInt, &revInt); err != nil {
				return err
			}
			r.Capitalized = capInt != 0
			r.Replenishable = replInt != 0
			r.IsReserve, r.Revocable = resInt != 0, revInt != 0
			b.TermDeposits = append(b.TermDeposits, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id, deposit_id, date, amount FROM deposit_topups
		WHERE portfolio_id=? ORDER BY id`,
		func(scan func(...any) error) error {
			var r BackupDepositTopup
			if err := scan(&r.ID, &r.DepositID, &r.Date, &r.Amount); err != nil {
				return err
			}
			b.DepositTopups = append(b.DepositTopups, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT `+npfAccountCols()+` FROM npf_accounts
		WHERE portfolio_id=? ORDER BY id`,
		func(scan func(...any) error) error {
			var r BackupNPFAccount
			if err := scan(&r.ID, &r.Name, &r.Administrator, &r.Currency, &r.NavE6,
				&r.NavDate, &r.ExpectedYieldBP, &r.YieldSimpleYears, &r.AccessDate,
				&r.IncomeTaxBP, &r.CreditRateBP, &r.ContribDay,
				&r.PayoutYears, &r.PayoutFreq, &r.Note); err != nil {
				return err
			}
			b.NPFAccounts = append(b.NPFAccounts, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT o.id,o.npf_id,o.date,o.units_e6,o.amount,
		COALESCE(b.name,''),o.note
		FROM npf_ops o LEFT JOIN brokers b ON b.id=o.broker_id
		WHERE o.portfolio_id=? ORDER BY o.id`,
		func(scan func(...any) error) error {
			var r BackupNPFOp
			if err := scan(&r.ID, &r.NPFID, &r.Date, &r.UnitsE6, &r.Amount,
				&r.Broker, &r.Note); err != nil {
				return err
			}
			b.NPFOps = append(b.NPFOps, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id,npf_id,date,nav_e6 FROM npf_nav
		WHERE portfolio_id=? ORDER BY id`,
		func(scan func(...any) error) error {
			var r BackupNPFNav
			if err := scan(&r.ID, &r.NPFID, &r.Date, &r.NavE6); err != nil {
				return err
			}
			b.NPFNav = append(b.NPFNav, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT f.name,p.date,p.price_e4 FROM fund_prices p
		JOIN funds f ON f.id = p.fund_id ORDER BY p.fund_id, p.date`,
		func(scan func(...any) error) error {
			var r BackupFundPrice
			if err := scan(&r.Fund, &r.Date, &r.PriceE4); err != nil {
				return err
			}
			b.FundPrices = append(b.FundPrices, r)
			return nil
		}); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id,date,amount,currency,place,note FROM reserve_ops
		WHERE portfolio_id=? ORDER BY id`,
		func(scan func(...any) error) error {
			var r BackupReserveOp
			if err := scan(&r.ID, &r.Date, &r.Amount, &r.Currency, &r.Place, &r.Note); err != nil {
				return err
			}
			b.ReserveOps = append(b.ReserveOps, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id,name,target_amount,currency,due_date,priority,
		place,note,done_date FROM goals WHERE portfolio_id=? ORDER BY id`,
		func(scan func(...any) error) error {
			var g BackupGoal
			if err := scan(&g.ID, &g.Name, &g.TargetAmount, &g.Currency, &g.DueDate,
				&g.Priority, &g.Place, &g.Note, &g.DoneDate); err != nil {
				return err
			}
			b.Goals = append(b.Goals, g)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id,goal_id,date,amount,currency,place,note
		FROM goal_ops WHERE portfolio_id=? ORDER BY id`,
		func(scan func(...any) error) error {
			var o BackupGoalOp
			if err := scan(&o.ID, &o.GoalID, &o.Date, &o.Amount, &o.Currency,
				&o.Place, &o.Note); err != nil {
				return err
			}
			b.GoalOps = append(b.GoalOps, o)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id,name,kind,currency,COALESCE(card_id,0),
		limit_amount,statement_day,apr_bp,apr_overdue_bp,min_payment_bp,
		min_payment_floor,late_fee,exit_by,principal,payments_total,first_payment_date,
		fee_month_bp,fee_free_months,fee_on_prepay,opened_date,closed_date,place,note
		FROM debts WHERE portfolio_id=? ORDER BY id`,
		func(scan func(...any) error) error {
			var d BackupDebt
			if err := scan(&d.ID, &d.Name, &d.Kind, &d.Currency, &d.CardID,
				&d.LimitAmount, &d.StatementDay, &d.APRBp, &d.APROverdueBp,
				&d.MinPaymentBp, &d.MinPaymentFloor, &d.LateFee, &d.ExitBy,
				&d.Principal, &d.PaymentsTotal, &d.FirstPaymentDate,
				&d.FeeMonthBp, &d.FeeFreeMonths, &d.FeeOnPrepay,
				&d.OpenedDate, &d.ClosedDate, &d.Place, &d.Note); err != nil {
				return err
			}
			b.Debts = append(b.Debts, d)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id,debt_id,date,kind,amount,note
		FROM debt_ops WHERE portfolio_id=? ORDER BY id`,
		func(scan func(...any) error) error {
			var o BackupDebtOp
			if err := scan(&o.ID, &o.DebtID, &o.Date, &o.Kind, &o.Amount, &o.Note); err != nil {
				return err
			}
			b.DebtOps = append(b.DebtOps, o)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id,debt_id,date,balance,statement_due,non_grace,note
		FROM debt_marks WHERE portfolio_id=? ORDER BY id`,
		func(scan func(...any) error) error {
			var m BackupDebtMark
			if err := scan(&m.ID, &m.DebtID, &m.Date, &m.Balance,
				&m.StatementDue, &m.NonGrace, &m.Note); err != nil {
				return err
			}
			b.DebtMarks = append(b.DebtMarks, m)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id,name,kind,amount,currency,cadence,from_date,until_date,
		growth_bp,invest_bp,dest,uses,note FROM plan_flows WHERE portfolio_id=? ORDER BY id`,
		func(scan func(...any) error) error {
			var r BackupPlanFlow
			if err := scan(&r.ID, &r.Name, &r.Kind, &r.Amount, &r.Currency, &r.Cadence,
				&r.FromDate, &r.UntilDate, &r.GrowthBP, &r.InvestBP, &r.Dest,
				&r.Uses, &r.Note); err != nil {
				return err
			}
			b.PlanFlows = append(b.PlanFlows, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id,flow_id,changed_at,op,name,kind,amount,currency,cadence,
		from_date,until_date,growth_bp,invest_bp,dest,uses,note FROM plan_flow_revisions
		WHERE portfolio_id=? ORDER BY id`,
		func(scan func(...any) error) error {
			var r BackupPlanFlowRevision
			if err := scan(&r.ID, &r.FlowID, &r.ChangedAt, &r.Op, &r.Name, &r.Kind,
				&r.Amount, &r.Currency, &r.Cadence, &r.FromDate, &r.UntilDate,
				&r.GrowthBP, &r.InvestBP, &r.Dest, &r.Uses, &r.Note); err != nil {
				return err
			}
			b.PlanFlowRevisions = append(b.PlanFlowRevisions, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id,flow_id,month,name,amount,currency,invest_bp,uses,note
		FROM plan_receipts WHERE portfolio_id=? ORDER BY id`,
		func(scan func(...any) error) error {
			var r BackupPlanReceipt
			if err := scan(&r.ID, &r.FlowID, &r.Month, &r.Name, &r.Amount,
				&r.Currency, &r.InvestBP, &r.Uses, &r.Note); err != nil {
				return err
			}
			b.PlanReceipts = append(b.PlanReceipts, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id,date,type,usd_bp,eur_bp,amount,currency,rate_bp,months,name,note
		FROM plan_actions WHERE portfolio_id=? ORDER BY id`,
		func(scan func(...any) error) error {
			var r BackupPlanAction
			if err := scan(&r.ID, &r.Date, &r.Type, &r.USDBP, &r.EURBP, &r.Amount,
				&r.Currency, &r.RateBP, &r.Months, &r.Name, &r.Note); err != nil {
				return err
			}
			b.PlanActions = append(b.PlanActions, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	// Назва брокера, а не broker_id (0043): бекап тримається НАЗВ усюди, і
	// саме тому формат пережив нормалізацію 0010 — тепер так само переживе
	// й цю. LEFT JOIN, бо «не привʼязано» — законний стан (обрати за
	// залишком), і в дампі воно лишається порожнім рядком, як було.
	if err := s.scan(ctx, `SELECT b.id,b.kind,b.ref,b.qty,b.amount,b.unit_price,
		b.currency,COALESCE(br.name,''),b.buy_date,b.rate_bp,b.months,b.is_reserve,b.note
		FROM plan_buys b LEFT JOIN brokers br ON br.id = b.broker_id
		WHERE b.portfolio_id=? ORDER BY b.id`,
		func(scan func(...any) error) error {
			var r BackupPlanBuy
			if err := scan(&r.ID, &r.Kind, &r.Ref, &r.Qty, &r.Amount, &r.UnitPrice,
				&r.Currency, &r.Broker, &r.BuyDate, &r.RateBP, &r.Months,
				&r.IsReserve, &r.Note); err != nil {
				return err
			}
			b.PlanBuys = append(b.PlanBuys, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT id,made_on,kind,ref,currency,amount,real_pct,
		rank_pos,top_label,top_real_pct,rank_mode,op_id,note FROM decisions
		WHERE portfolio_id=? ORDER BY id`,
		func(scan func(...any) error) error {
			var r BackupDecision
			if err := scan(&r.ID, &r.MadeOn, &r.Kind, &r.Ref, &r.Currency, &r.Amount,
				&r.RealPct, &r.RankPos, &r.TopLabel, &r.TopRealPct, &r.RankMode,
				&r.OpID, &r.Note); err != nil {
				return err
			}
			b.Decisions = append(b.Decisions, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT name,format,header,col_date,col_op,col_ref,
		col_qty,col_debit,col_credit,col_balance,col_mcc,debt_id,ops,note FROM import_profiles
		WHERE portfolio_id=? ORDER BY name COLLATE NOCASE`,
		func(scan func(...any) error) error {
			var r BackupImportProfile
			var bal, mcc int
			if err := scan(&r.Name, &r.Format, &r.Header, &r.Date, &r.Op, &r.Ref,
				&r.Qty, &r.Debit, &r.Credit, &bal, &mcc, &r.DebtID, &r.Ops, &r.Note); err != nil {
				return err
			}
			r.Balance, r.MCC = &bal, &mcc
			b.ImportProfiles = append(b.ImportProfiles, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	// Довідники — цілком, а не лише тими рядками, які згадані в операціях.
	// Порядок за назвою, як у ListFunds/ListBrokers: дамп того самого стану
	// має бути тим самим файлом.
	if err := s.scan(ctx, `SELECT name,currency,expected_yield_bp,expected_yield_currency,payout_day,
		kind,close_date,buy_until,income_tax_bp,yield_simple_years,exit_tax_bp
		FROM funds ORDER BY name COLLATE NOCASE`,
		func(scan func(...any) error) error {
			var r BackupFund
			if err := scan(&r.Name, &r.Currency, &r.ExpectedYieldBP, &r.ExpectedYieldCur, &r.PayoutDay,
				&r.Kind, &r.CloseDate, &r.BuyUntil, &r.IncomeTaxBP, &r.YieldSimpleYears,
				&r.ExitTaxBP); err != nil {
				return err
			}
			b.Funds = append(b.Funds, r)
			return nil
		}); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT name FROM brokers WHERE portfolio_id=? ORDER BY name COLLATE NOCASE`,
		func(scan func(...any) error) error {
			var r BackupBroker
			if err := scan(&r.Name); err != nil {
				return err
			}
			b.Brokers = append(b.Brokers, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT key,value FROM settings WHERE portfolio_id=?`,
		func(scan func(...any) error) error {
			var k, v string
			if err := scan(&k, &v); err != nil {
				return err
			}
			b.Settings[k] = v
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	if err := s.scan(ctx, `SELECT isin,pay_date,status,marked_at FROM payment_status WHERE portfolio_id=?`,
		func(scan func(...any) error) error {
			var r BackupPayStatus
			if err := scan(&r.ISIN, &r.PayDate, &r.Status, &r.MarkedAt); err != nil {
				return err
			}
			b.PaymentStatus = append(b.PaymentStatus, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	// Колонки й цілі для Scan — з реєстру (snapshot.go). Тут це важить
	// найбільше: s.scan приймає func(...any), тож типи не перевіряються
	// взагалі, і переплутаний порядок був би тихим до останнього.
	if err := s.scan(ctx, `SELECT `+snapshotColumnList()+` FROM snapshots
		WHERE portfolio_id=? ORDER BY date`,
		func(scan func(...any) error) error {
			var r Snapshot
			var d string
			if err := scan(snapshotScanTargets(&r, &d)...); err != nil {
				return err
			}
			r.Date = domain.Date(d)
			b.Snapshots = append(b.Snapshots, r)
			return nil
		}, s.pid); err != nil {
		return nil, err
	}
	return b, nil
}

// ImportAll ЗАМІНЮЄ всі користувацькі дані вмістом бекапу. Атомарно: або
// вся база замінюється, або нічого (при помилці транзакція відкочується).
// Довідник НБУ/графіки/курси не чіпаються — вони похідні.
// importAllTables — що саме ImportAll витирає перед відновленням, у
// порядку «діти → батьки».
//
// Винесене з тіла функції не заради охайності: перелік ведеться руками, а
// пропущена в ньому таблиця означає не «дублікати після відновлення», а
// ВІДМОВУ відновлення (див. доводи нижче). Тепер його читає
// TestBackupCoversEveryUserTable і звіряє зі схемою — таблиця, забута тут,
// валить збірку, а не день відновлення.
//
// Порядок значущий і в межах переліку: довідники (funds, brokers) — в
// самому кінці, бо на них посилається майже все.
var importAllTables = []string{
	"sales", "lots", "deposits", "conversions", "fund_ops",
	"fund_prices", "deposit_topups", "term_deposits", "reserve_ops",
	"goal_ops", "goals", "debt_ops", "debt_marks", "debts",
	"npf_ops", "npf_nav",
	"npf_accounts", "plan_flows", "plan_flow_revisions",
	"plan_receipts", "plan_actions", "plan_buys", "decisions", "import_profiles",
	"settings", "payment_status", "snapshots",
	"funds", "brokers",
}

// importGlobalTables — ті з importAllTables, що НЕ мають portfolio_id
// (0054): каталог фондів і позначки ціни спільні для всіх портфелів, тож
// ImportAll витирає їх не за портфелем, а за вжитком (pruneOrphanFundsIn).
// Окремий набір, а не правка importAllTables: той перелік звіряється зі
// схемою тестом, і саме ним ведеться порядок «діти → батьки».
var importGlobalTables = map[string]bool{"funds": true, "fund_prices": true}

// pruneOrphanFundsIn — витерти фонди (з позначками), якими не користується
// жоден портфель. Кличеться ПІСЛЯ очищення власних операцій, тож фонди
// цього портфеля йдуть у смітник, а сусідові лишаються — і саме тому запит
// дивиться на fund_ops УСІХ портфелів, а не свого (виняток у сторожі
// scope_guard_test.go).
func (s *Store) pruneOrphanFundsIn(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM fund_prices WHERE fund_id IN
		(SELECT f.id FROM funds f WHERE NOT EXISTS (SELECT 1 FROM fund_ops o WHERE o.fund_id=f.id))`); err != nil {
		return fmt.Errorf("очищення позначок ціни: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM funds
		WHERE NOT EXISTS (SELECT 1 FROM fund_ops o WHERE o.fund_id=funds.id)`); err != nil {
		return fmt.Errorf("очищення каталогу фондів: %w", err)
	}
	return nil
}

// decisionOpTable — у якій таблиці живе decisions.op_id за kind (0035:
// «яка саме таблиця, каже kind»). Ті самі слова, що в plan_buys і в
// журналі рішень (api/decisions.go: reserve, goal).
var decisionOpTable = map[string]string{
	BuyBond: "lots", BuyFund: "fund_ops", BuyDeposit: "term_deposits", BuyNPF: "npf_ops",
	"goal": "goal_ops", "reserve": "reserve_ops",
}

// idMaps — старий id → новий, по таблицях, для рядків, чий id із бекапу
// був зайнятий іншим портфелем (шапка файла).
type idMaps map[string]map[int64]int64

// of — новий id за старим; сам старий, якщо заміни не було (0 лишається 0).
func (m idMaps) of(table string, old int64) int64 {
	if n, ok := m[table][old]; ok {
		return n
	}
	return old
}

// insert — вставити рядок з його id з бекапу, якщо той вільний, інакше з
// новим, і запамʼятати заміну. tmpl має два %s: місце для «id,» у переліку
// колонок і для «?,» у VALUES. Назва таблиці стоїть у самому tmpl, а не
// підставляється, — щоб сторож звуження (scope_guard_test.go) бачив і
// таблицю, і portfolio_id в одному літералі.
//
// Перевірка «зайнятий?» — на мить вставки, а не наперед: ExportAll іде за
// id, а новий id завжди більший за все наявне, тож пізніший рядок із файла
// не може зайняти те, що вже роздано.
func (m idMaps) insert(ctx context.Context, tx *sql.Tx, table string, id int64, tmpl string, args ...any) error {
	var taken int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE id=?`, id).Scan(&taken); err != nil {
		return err
	}
	if taken == 0 {
		_, err := tx.ExecContext(ctx, fmt.Sprintf(tmpl, "id,", "?,"), append([]any{id}, args...)...)
		return err
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf(tmpl, "", ""), args...)
	if err != nil {
		return err
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return err
	}
	if m[table] == nil {
		m[table] = map[int64]int64{}
	}
	m[table][id] = newID
	return nil
}

func (s *Store) ImportAll(ctx context.Context, b *Backup) error {
	if b == nil || b.Schema != BackupSchema {
		return fmt.Errorf("несумісний бекап (schema=%d, очікуємо %d)", b.Schema, BackupSchema)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op після Commit

	// діти → батьки, щоб не спіткнутись об FK. Довідники йдуть ОСТАННІМИ
	// і заповнюються заново з назв у бекапі: тримати бекап у назвах, а не
	// в id, означає, що відновлення не залежить від того, які id були в
	// базі-джерелі.
	//
	// term_deposits і deposit_topups сюди не потрапили, коли вклади
	// з'явились, і наслідок був не «дублікати після відновлення», а
	// повна відмова: term_deposits.broker_id посилається на brokers, тож
	// DELETE FROM brokers упирався у FK, і restore падав у будь-кого, хто
	// має бодай один вклад. Поповнення — перед самим вкладом, вони його
	// діти.
	//
	// reserve_ops — той самий випадок, що й вклади: бекап тримає id, тож
	// забути її в цьому переліку означає не «дублікати», а відмову
	// відновлення на UNIQUE(id) у будь-кого, хто має бодай один рух
	// резерву. Перевірено тестом.
	// plan_flows/plan_actions — рівно той самий випадок, що й reserve_ops:
	// бекап тримає id, тож забути їх ТУТ означає не «дублікати після
	// відновлення», а відмову на UNIQUE(id) у будь-кого, хто має бодай
	// один рядок плану.
	//
	// npf_ops і npf_nav — перед npf_accounts (FK на нього), а всі три —
	// перед brokers, і це не стиль. npf_ops.broker_id посилається на
	// brokers, тож забути npf_ops тут означало б не «дублікати після
	// відновлення», а повну ВІДМОВУ: DELETE FROM brokers упирався б у FK у
	// будь-кого, хто має бодай один внесок. Рівно те, що вже сталося з
	// вкладами, коли їх забули в цьому переліку.
	//
	// fund_prices (0034) — той самий випадок утретє, лише FK веде у funds:
	// забути її тут означає не «дублікати після відновлення», а повну
	// ВІДМОВУ, бо DELETE FROM funds упреться у FK у будь-кого, хто має
	// бодай одну позначку ціни.
	//
	// goal_ops — перед goals (FK на них), і обидві в переліку з тієї самої
	// причини, що reserve_ops: бекап тримає id, тож пропуск тут означає не
	// «дублікати після відновлення», а відмову на UNIQUE(id) у будь-кого,
	// хто має бодай одну ціль.
	// Розстрочки треба відчепити від карток ДО переліку, і це не стиль.
	// debts має FK на саму себе (card_id → debts.id), а DELETE FROM без
	// WHERE видаляє рядки в порядку rowid — тобто картку РАНІШЕ за її
	// розстрочку — і впирається у власний FK. Один UPDATE дешевший, ніж
	// заводити в переліку поняття «частина таблиці».
	if _, err := tx.ExecContext(ctx, `UPDATE debts SET card_id=NULL WHERE portfolio_id=?`, s.pid); err != nil {
		return fmt.Errorf("відчеплення розстрочок: %w", err)
	}
	for _, t := range importAllTables {
		if importGlobalTables[t] {
			continue
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+t+" WHERE portfolio_id=?", s.pid); err != nil {
			return fmt.Errorf("очищення %s: %w", t, err)
		}
	}
	if err := s.pruneOrphanFundsIn(ctx, tx); err != nil {
		return err
	}
	ids := idMaps{}

	brokers := map[string]any{}
	brokerRef := func(name string) (any, error) {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, nil
		}
		if id, ok := brokers[name]; ok {
			return id, nil
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO brokers(portfolio_id,name) VALUES(?,?)`, s.pid, name)
		if err != nil {
			return nil, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		brokers[name] = id
		return id, nil
	}
	funds := map[string]int64{}
	fundRef := func(name, currency string) (int64, error) {
		name = strings.TrimSpace(name)
		if name == "" {
			return 0, fmt.Errorf("операція без фонду")
		}
		if id, ok := funds[name]; ok {
			return id, nil
		}
		// Спершу пошук: каталог спільний і НЕ витирається перед restore,
		// тож фонд із файла може вже бути — заведений сусіднім портфелем
		// або цим самим до відновлення.
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT id FROM funds WHERE name=?`, name).Scan(&id)
		if err == nil {
			funds[name] = id
			return id, nil
		}
		if err != sql.ErrNoRows {
			return 0, err
		}
		if strings.TrimSpace(currency) == "" {
			currency = "UAH"
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO funds(name,currency) VALUES(?,?)`, name, currency)
		if err != nil {
			return 0, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return 0, err
		}
		funds[name] = id
		return id, nil
	}

	// Довідники — ПЕРШИМИ й цілком, доки жодна операція їх ще не завела
	// побічним ефектом. Порядок тут і є суттю виправлення: fundRef задає
	// валюту ЛИШЕ при створенні й нічого не знає про обіцяну дохідність,
	// тож фонд, заведений з операції, назавжди лишався б із нулями в
	// трьох полях, яких з операцій не вивести. Заселивши довідник наперед,
	// ми робимо подальший fundRef простим пошуком у мапі.
	//
	// Старі дампи цих переліків не мають — обидва цикли просто не
	// виконуються, і відновлення йде рівно тим шляхом, що й доти.
	for _, br := range b.Brokers {
		if _, err := brokerRef(br.Name); err != nil {
			return fmt.Errorf("брокер %q: %w", br.Name, err)
		}
	}
	for _, f := range b.Funds {
		id, err := fundRef(f.Name, f.Currency)
		if err != nil {
			return fmt.Errorf("фонд %q: %w", f.Name, err)
		}
		// Дати з чужого файла перевіряються тут-таки: інакше сміття лягло
		// б у базу мовчки, а зламалось би вже в моделі, за три фази звідси.
		closeDate, err := checkFundDate("дата закриття", f.CloseDate)
		if err != nil {
			return fmt.Errorf("фонд %q: %w", f.Name, err)
		}
		buyUntil, err := checkFundDate("остання дата купівлі", f.BuyUntil)
		if err != nil {
			return fmt.Errorf("фонд %q: %w", f.Name, err)
		}
		kind, err := checkFundKind(f.Kind, f.PayoutDay)
		if err != nil {
			return fmt.Errorf("фонд %q: %w", f.Name, err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE funds SET expected_yield_bp=?,
			expected_yield_currency=?, payout_day=?, kind=?, close_date=?,
			buy_until=?, income_tax_bp=?, yield_simple_years=?, exit_tax_bp=?
			WHERE id=?`,
			f.ExpectedYieldBP, strings.TrimSpace(f.ExpectedYieldCur), f.PayoutDay,
			kind, closeDate, buyUntil, f.IncomeTaxBP,
			f.YieldSimpleYears, f.ExitTaxBP, id); err != nil {
			return fmt.Errorf("фонд %q: %w", f.Name, err)
		}
	}

	for _, l := range b.Lots {
		broker, err := brokerRef(l.Channel)
		if err != nil {
			return fmt.Errorf("лот %d: %w", l.ID, err)
		}
		if err := ids.insert(ctx, tx, "lots", l.ID,
			`INSERT INTO lots (%sportfolio_id,isin,qty,price_per_bond,currency,buy_date,broker_id,fee,note) VALUES (%s?,?,?,?,?,?,?,?,?)`,
			s.pid, l.ISIN, l.Qty, l.Price, l.Currency, l.BuyDate, broker, l.Fee, l.Note); err != nil {
			return fmt.Errorf("лот %d: %w", l.ID, err)
		}
	}
	for _, sl := range b.Sales {
		if err := ids.insert(ctx, tx, "sales", sl.ID,
			`INSERT INTO sales (%sportfolio_id,lot_id,sale_date,qty,clean_per_bond,accrued,currency,note) VALUES (%s?,?,?,?,?,?,?,?)`,
			s.pid, ids.of("lots", sl.LotID), sl.SaleDate, sl.Qty, sl.Clean, sl.Accrued, sl.Currency, sl.Note); err != nil {
			return fmt.Errorf("продаж %d: %w", sl.ID, err)
		}
	}
	for _, d := range b.Deposits {
		broker, err := brokerRef(d.Broker)
		if err != nil {
			return fmt.Errorf("поповнення %d: %w", d.ID, err)
		}
		if err := ids.insert(ctx, tx, "deposits", d.ID,
			`INSERT INTO deposits (%sportfolio_id,date,amount,currency,broker_id,note) VALUES (%s?,?,?,?,?,?)`,
			s.pid, d.Date, d.Amount, d.Currency, broker, d.Note); err != nil {
			return fmt.Errorf("поповнення %d: %w", d.ID, err)
		}
	}
	for _, c := range b.Conversions {
		broker, err := brokerRef(c.Broker)
		if err != nil {
			return fmt.Errorf("конвертація %d: %w", c.ID, err)
		}
		if err := ids.insert(ctx, tx, "conversions", c.ID,
			`INSERT INTO conversions (%sportfolio_id,date,from_currency,from_amount,to_currency,to_amount,broker_id,note) VALUES (%s?,?,?,?,?,?,?,?)`,
			s.pid, c.Date, c.FromCurrency, c.FromAmount, c.ToCurrency, c.ToAmount, broker, c.Note); err != nil {
			return fmt.Errorf("конвертація %d: %w", c.ID, err)
		}
	}
	// Операції фондів — двома заходами: pair_id посилається на інший
	// рядок цієї ж таблиці, і при вставці «за один прохід» друга нога
	// конвертації вказувала б на рядок, якого ще немає.
	for _, op := range b.FundOps {
		fund, err := fundRef(op.Fund, op.Currency)
		if err != nil {
			return fmt.Errorf("операція фонду %d: %w", op.ID, err)
		}
		broker, err := brokerRef(op.Broker)
		if err != nil {
			return fmt.Errorf("операція фонду %d: %w", op.ID, err)
		}
		if err := ids.insert(ctx, tx, "fund_ops", op.ID,
			`INSERT INTO fund_ops (%sportfolio_id,date,fund_id,kind,qty,amount,tax,broker_id,note) VALUES (%s?,?,?,?,?,?,?,?,?)`,
			s.pid, op.Date, fund, op.Kind, op.Qty, op.Amount, op.Tax, broker, op.Note); err != nil {
			return fmt.Errorf("операція фонду %d: %w", op.ID, err)
		}
	}
	for _, op := range b.FundOps {
		if op.PairID == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE fund_ops SET pair_id=? WHERE id=? AND portfolio_id=?`,
			ids.of("fund_ops", op.PairID), ids.of("fund_ops", op.ID), s.pid); err != nil {
			return fmt.Errorf("конвертація фонду %d: %w", op.ID, err)
		}
	}
	for _, d := range b.TermDeposits {
		broker, err := brokerRef(d.Bank)
		if err != nil {
			return fmt.Errorf("вклад %d: %w", d.ID, err)
		}
		if err := ids.insert(ctx, tx, "term_deposits", d.ID,
			`INSERT INTO term_deposits (%sportfolio_id,broker_id,currency,principal,rate_bp,open_date,
			 maturity_date,payout,capitalized,tax_bp,closed_date,closed_amount,note,replenishable,
			 is_reserve,revocable)
			 VALUES (%s?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			s.pid, broker, d.Currency, d.Principal, d.RateBP, d.OpenDate, d.MaturityDate,
			d.Payout, boolInt(d.Capitalized), d.TaxBP, d.ClosedDate, d.ClosedAmount, d.Note,
			boolInt(d.Replenishable), boolInt(d.IsReserve), boolInt(d.Revocable)); err != nil {
			return fmt.Errorf("вклад %d: %w", d.ID, err)
		}
	}
	// Поповнення — після вкладів: FK на term_deposits.
	for _, t := range b.DepositTopups {
		if err := ids.insert(ctx, tx, "deposit_topups", t.ID,
			`INSERT INTO deposit_topups (%sportfolio_id,deposit_id,date,amount) VALUES (%s?,?,?,?)`,
			s.pid, ids.of("term_deposits", t.DepositID), t.Date, t.Amount); err != nil {
			return fmt.Errorf("поповнення вкладу %d: %w", t.ID, err)
		}
	}
	for _, r := range b.ReserveOps {
		if err := ids.insert(ctx, tx, "reserve_ops", r.ID,
			`INSERT INTO reserve_ops (%sportfolio_id,date,amount,currency,place,note) VALUES (%s?,?,?,?,?,?)`,
			s.pid, r.Date, r.Amount, r.Currency, r.Place, r.Note); err != nil {
			return fmt.Errorf("рух резерву %d: %w", r.ID, err)
		}
	}
	// Цілі — ПЕРЕД рухами: goal_ops має на них FK. Той самий порядок, що в
	// пенсійного рахунку з внесками нижче.
	for _, g := range b.Goals {
		if err := ids.insert(ctx, tx, "goals", g.ID, `INSERT INTO goals
			(%sportfolio_id,name,target_amount,currency,due_date,priority,place,note,done_date)
			VALUES (%s?,?,?,?,?,?,?,?,?)`,
			s.pid, g.Name, g.TargetAmount, g.Currency, g.DueDate, g.Priority,
			g.Place, g.Note, g.DoneDate); err != nil {
			return fmt.Errorf("ціль %d: %w", g.ID, err)
		}
	}
	for _, o := range b.GoalOps {
		if err := ids.insert(ctx, tx, "goal_ops", o.ID, `INSERT INTO goal_ops
			(%sportfolio_id,goal_id,date,amount,currency,place,note) VALUES (%s?,?,?,?,?,?,?)`,
			s.pid, ids.of("goals", o.GoalID), o.Date, o.Amount, o.Currency, o.Place, o.Note); err != nil {
			return fmt.Errorf("рух цілі %d: %w", o.ID, err)
		}
	}
	// Борги вставляються У ДВА ПРОХОДИ: спершу всі з порожнім card_id,
	// потім привʼязка. Порядок у бекапі покладатись не можна — картка
	// завжди має менший id, ніж її розстрочка, лише доки файл не
	// правили руками, — а FK на саму себе карає за це відмовою всього
	// відновлення. Два проходи не залежать від порядку взагалі.
	for _, d := range b.Debts {
		if err := ids.insert(ctx, tx, "debts", d.ID, `INSERT INTO debts
			(%sportfolio_id,name,kind,currency,card_id,limit_amount,statement_day,apr_bp,
			 apr_overdue_bp,min_payment_bp,min_payment_floor,late_fee,exit_by,principal,
			 payments_total,first_payment_date,fee_month_bp,fee_free_months,fee_on_prepay,
			 opened_date,closed_date,place,note)
			VALUES (%s?,?,?,?,NULL,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			s.pid, d.Name, d.Kind, d.Currency, d.LimitAmount, d.StatementDay,
			d.APRBp, d.APROverdueBp, d.MinPaymentBp, d.MinPaymentFloor, d.LateFee, d.ExitBy,
			d.Principal, d.PaymentsTotal, d.FirstPaymentDate, d.FeeMonthBp,
			d.FeeFreeMonths, d.FeeOnPrepay,
			d.OpenedDate, d.ClosedDate, d.Place, d.Note); err != nil {
			return fmt.Errorf("борг %d: %w", d.ID, err)
		}
	}
	for _, d := range b.Debts {
		if d.CardID == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE debts SET card_id=? WHERE id=? AND portfolio_id=?`,
			ids.of("debts", d.CardID), ids.of("debts", d.ID), s.pid); err != nil {
			return fmt.Errorf("привʼязка боргу %d до картки %d: %w", d.ID, d.CardID, err)
		}
	}
	for _, o := range b.DebtOps {
		if err := ids.insert(ctx, tx, "debt_ops", o.ID, `INSERT INTO debt_ops
			(%sportfolio_id,debt_id,date,kind,amount,note) VALUES (%s?,?,?,?,?,?)`,
			s.pid, ids.of("debts", o.DebtID), o.Date, o.Kind, o.Amount, o.Note); err != nil {
			return fmt.Errorf("рух боргу %d: %w", o.ID, err)
		}
	}
	for _, m := range b.DebtMarks {
		if err := ids.insert(ctx, tx, "debt_marks", m.ID, `INSERT INTO debt_marks
			(%sportfolio_id,debt_id,date,balance,statement_due,non_grace,note) VALUES (%s?,?,?,?,?,?,?)`,
			s.pid, ids.of("debts", m.DebtID), m.Date, m.Balance, m.StatementDue, m.NonGrace, m.Note); err != nil {
			return fmt.Errorf("звірка боргу %d: %w", m.ID, err)
		}
	}
	// Пенсійні рахунки — ПЕРЕД внесками й точками ЧВОПА: обидва мають на
	// них FK.
	for _, a := range b.NPFAccounts {
		if err := ids.insert(ctx, tx, "npf_accounts", a.ID, `INSERT INTO npf_accounts
			(%sportfolio_id,name,administrator,currency,nav_e6,nav_date,expected_yield_bp,
			 yield_simple_years,access_date,income_tax_bp,credit_rate_bp,contrib_day,
			 payout_years,payout_freq,note)
			 VALUES (%s?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			s.pid, a.Name, a.Administrator, a.Currency, a.NavE6, a.NavDate,
			a.ExpectedYieldBP, a.YieldSimpleYears, a.AccessDate, a.IncomeTaxBP,
			a.CreditRateBP, a.ContribDay, a.PayoutYears, a.PayoutFreq, a.Note); err != nil {
			return fmt.Errorf("пенсійний рахунок %d: %w", a.ID, err)
		}
	}
	for _, o := range b.NPFOps {
		broker, err := brokerRef(o.Broker)
		if err != nil {
			return fmt.Errorf("внесок у НПФ %d: %w", o.ID, err)
		}
		if err := ids.insert(ctx, tx, "npf_ops", o.ID, `INSERT INTO npf_ops
			(%sportfolio_id,npf_id,date,units_e6,amount,broker_id,note) VALUES (%s?,?,?,?,?,?,?)`,
			s.pid, ids.of("npf_accounts", o.NPFID), o.Date, o.UnitsE6, o.Amount, broker, o.Note); err != nil {
			return fmt.Errorf("внесок у НПФ %d: %w", o.ID, err)
		}
	}
	for _, n := range b.NPFNav {
		if err := ids.insert(ctx, tx, "npf_nav", n.ID,
			`INSERT INTO npf_nav (%sportfolio_id,npf_id,date,nav_e6) VALUES (%s?,?,?,?)`,
			s.pid, ids.of("npf_accounts", n.NPFID), n.Date, n.NavE6); err != nil {
			return fmt.Errorf("точка ЧВОПА %d: %w", n.ID, err)
		}
	}
	// Позначки ціни — після довідника фондів: fundRef уже заселений циклом
	// по b.Funds вище, тож тут лишається пошук у мапі. Валюту йому не
	// передаємо — фонд на цю мить уже існує, і другий аргумент лише
	// перезаписав би її порожнім рядком.
	for _, p := range b.FundPrices {
		fid, err := fundRef(p.Fund, "")
		if err != nil {
			return fmt.Errorf("позначка ціни %q %s: %w", p.Fund, p.Date, err)
		}
		// Upsert: позначки не витираються перед restore (довідник спільний),
		// тож та сама дата з файла переписує наявну, а не падає на UNIQUE.
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO fund_prices (fund_id,date,price_e4) VALUES (?,?,?)
			 ON CONFLICT(fund_id,date) DO UPDATE SET price_e4=excluded.price_e4`,
			fid, p.Date, p.PriceE4); err != nil {
			return fmt.Errorf("позначка ціни %q %s: %w", p.Fund, p.Date, err)
		}
	}
	// План FK не має — порядок серед решти вставок вільний.
	for _, f := range b.PlanFlows {
		if err := ids.insert(ctx, tx, "plan_flows", f.ID,
			`INSERT INTO plan_flows (%sportfolio_id,name,kind,amount,currency,cadence,from_date,until_date,
			 growth_bp,invest_bp,dest,uses,note) VALUES (%s?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			s.pid, f.Name, f.Kind, f.Amount, f.Currency, f.Cadence, f.FromDate, f.UntilDate,
			f.GrowthBP, f.InvestBP, f.Dest, f.Uses, f.Note); err != nil {
			return fmt.Errorf("плановий потік %d: %w", f.ID, err)
		}
	}
	// Ревізії — після потоків, хоч FK між ними й немає: порядок читається
	// як «спершу що є, потім як воно таким стало».
	for _, r := range b.PlanFlowRevisions {
		if err := ids.insert(ctx, tx, "plan_flow_revisions", r.ID,
			`INSERT INTO plan_flow_revisions (%sportfolio_id,flow_id,changed_at,op,name,kind,amount,currency,
			 cadence,from_date,until_date,growth_bp,invest_bp,dest,uses,note)
			 VALUES (%s?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			s.pid, ids.of("plan_flows", r.FlowID), r.ChangedAt, r.Op, r.Name, r.Kind, r.Amount, r.Currency,
			r.Cadence, r.FromDate, r.UntilDate, r.GrowthBP, r.InvestBP, r.Dest,
			r.Uses, r.Note); err != nil {
			return fmt.Errorf("ревізія потоку %d: %w", r.ID, err)
		}
	}
	// Відмітки — після потоків із тієї ж причини, що й ревізії: FK між ними
	// немає, але порядок читається як «спершу план, потім що з нього вийшло».
	for _, r := range b.PlanReceipts {
		if err := ids.insert(ctx, tx, "plan_receipts", r.ID,
			`INSERT INTO plan_receipts (%sportfolio_id,flow_id,month,name,amount,currency,invest_bp,uses,note)
			 VALUES (%s?,?,?,?,?,?,?,?,?)`,
			s.pid, ids.of("plan_flows", r.FlowID), r.Month, r.Name, r.Amount, r.Currency, r.InvestBP,
			r.Uses, r.Note); err != nil {
			return fmt.Errorf("відмітка надходження %d: %w", r.ID, err)
		}
	}
	for _, a := range b.PlanActions {
		if err := ids.insert(ctx, tx, "plan_actions", a.ID,
			`INSERT INTO plan_actions (%sportfolio_id,date,type,usd_bp,eur_bp,amount,currency,rate_bp,months,name,note)
			 VALUES (%s?,?,?,?,?,?,?,?,?,?,?)`,
			s.pid, a.Date, a.Type, a.USDBP, a.EURBP, a.Amount, a.Currency, a.RateBP,
			a.Months, a.Name, a.Note); err != nil {
			return fmt.Errorf("планова дія %d: %w", a.ID, err)
		}
	}
	for _, p := range b.PlanBuys {
		broker, err := brokerRef(p.Broker)
		if err != nil {
			return err
		}
		if err := ids.insert(ctx, tx, "plan_buys", p.ID,
			`INSERT INTO plan_buys (%sportfolio_id,kind,ref,qty,amount,unit_price,currency,broker_id,
			 buy_date,rate_bp,months,is_reserve,note)
			 VALUES (%s?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			s.pid, p.Kind, p.Ref, p.Qty, p.Amount, p.UnitPrice, p.Currency, broker,
			p.BuyDate, p.RateBP, p.Months, p.IsReserve, p.Note); err != nil {
			return fmt.Errorf("планована купівля %d: %w", p.ID, err)
		}
	}
	for _, d := range b.Decisions {
		// op_id — «на що рішення перетворилось», і таблицю каже kind
		// (0035). Перечіплюється за тією самою мапою, що й діти: інакше
		// після перемапи журнал показував би чужий лот.
		if err := ids.insert(ctx, tx, "decisions", d.ID,
			`INSERT INTO decisions (%sportfolio_id,made_on,kind,ref,currency,amount,real_pct,
			 rank_pos,top_label,top_real_pct,rank_mode,op_id,note)
			 VALUES (%s?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			s.pid, d.MadeOn, d.Kind, d.Ref, d.Currency, d.Amount, d.RealPct,
			d.RankPos, d.TopLabel, d.TopRealPct, d.RankMode,
			ids.of(decisionOpTable[d.Kind], d.OpID), d.Note); err != nil {
			return fmt.Errorf("рішення %d: %w", d.ID, err)
		}
	}
	for _, p := range b.ImportProfiles {
		bal, mcc := -1, -1
		if p.Balance != nil {
			bal = *p.Balance
		}
		if p.MCC != nil {
			mcc = *p.MCC
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO import_profiles (portfolio_id,name,format,header,col_date,col_op,col_ref,
			 col_qty,col_debit,col_credit,col_balance,col_mcc,debt_id,ops,note)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			s.pid, p.Name, p.Format, p.Header, p.Date, p.Op, p.Ref, p.Qty, p.Debit,
			p.Credit, bal, mcc, ids.of("debts", p.DebtID), p.Ops, p.Note); err != nil {
			return fmt.Errorf("профіль імпорту %q: %w", p.Name, err)
		}
	}
	for k, v := range b.Settings {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (portfolio_id,key,value) VALUES (?,?,?)`, s.pid, k, v); err != nil {
			return fmt.Errorf("налаштування %q: %w", k, err)
		}
	}
	for _, p := range b.PaymentStatus {
		// Зводимо статус до єдиного, який домен ще пише. Дамп, зроблений до
		// 0017, несе 'reinvested', і відколи 0044 закрила його в CHECK,
		// сира вставка валила б ВСЕ відновлення на одному старому рядку.
		// Те саме перетворення, що робила 0017, лише на вході: у бекапі
		// статус — сирий рядок, і нормалізувати його нема кому, крім цього
		// місця.
		status := p.Status
		if status != domain.StatusReceived {
			status = domain.StatusReceived
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO payment_status (portfolio_id,isin,pay_date,status,marked_at) VALUES (?,?,?,?,?)`,
			s.pid, p.ISIN, p.PayDate, status, p.MarkedAt); err != nil {
			return fmt.Errorf("статус виплати %s/%s: %w", p.ISIN, p.PayDate, err)
		}
	}
	for _, sn := range b.Snapshots {
		// Забута тут колонка означала б, що відновлення мовчки її обнуляє —
		// і саме це вже двічі ставалось. Перелік і аргументи з реєстру.
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO snapshots (portfolio_id, `+snapshotColumnList()+`)
			 VALUES(?, `+snapshotPlaceholders()+`)`,
			append([]any{s.pid}, snapshotArgs(&sn)...)...); err != nil {
			return fmt.Errorf("знімок %s: %w", sn.Date, err)
		}
	}
	return tx.Commit()
}

// scan — дрібний хелпер: виконати запит і прогнати кожен рядок через fn.
// args — параметри запиту (0054: майже кожен SELECT тут тепер несе s.pid).
func (s *Store) scan(ctx context.Context, query string, fn func(scan func(...any) error) error, args ...any) error {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows.Scan); err != nil {
			return err
		}
	}
	return rows.Err()
}
