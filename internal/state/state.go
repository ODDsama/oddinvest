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

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

const SchemaVersion = 1

type Doc struct {
	Schema      int    `json:"schema"`
	GeneratedAt string `json:"generated_at"` // RFC3339

	InvestedUAH  float64 `json:"invested_uah"`   // вартість входу залишків, грн-екв.
	NominalUAHEq float64 `json:"nominal_uah_eq"` // номінал портфеля, грн-екв.
	// CapitalUAH — УВЕСЬ капітал: номінал ОВДП + рахунок + фонди + вклади +
	// резерв (адитивне поле). Знаменник, від якого рахуються всі частки.
	//
	// Довго його в документі не було, і кожен споживач складав своє: плитка
	// «Капітал» на фронтенді — одну суму, картка ребалансу — іншу, старт
	// проєкції — третю. Числа стояли на сусідніх картках і не сходились.
	CapitalUAH float64 `json:"capital_uah,omitempty"`
	// USDSharePct / EURSharePct — частка валюти в КАПІТАЛІ. У чисельнику —
	// справжня експозиція (папери + вклади + резерв цієї валюти), готівки
	// брокера там немає: вона от-от стане чимось іншим, тож лише розводить
	// частки зі знаменника. Див. state.Capital.
	USDSharePct   float64 `json:"usd_share_pct"`
	EURSharePct   float64 `json:"eur_share_pct"`
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

	// FundsUAH — вартість сертифікатів фондів (Inzhur REIT і подібні) у
	// грн-екв. за останньою відомою ціною. ОКРЕМЕ поле, а не додаток до
	// nominal_uah_eq: у сертифіката немає номіналу, і змішування зламало
	// б і драбину, і дюрацію, які будуються на номіналі облігацій.
	FundsUAH float64           `json:"funds_uah,omitempty"`
	Funds    []FundPositionRow `json:"funds,omitempty"`
	// FundsCostUAH — за скільки ці сертифікати куплені, грн-екв.
	// (адитивне поле).
	//
	// Сума по позиціях, і саме тому вона тут. Доти її складали в трьох
	// місцях: добова джоба перед записом у знімок, фронтенд для плитки
	// «Вкладено», і крива капіталу — уже з колонки знімка. Три копії
	// однієї суми, дві з них у різних мовах, і жодного способу помітити,
	// коли вони розійдуться.
	FundsCostUAH float64 `json:"funds_cost_uah,omitempty"`

	// DepositsUAH — тіло діючих банківських вкладів у грн-екв. ОКРЕМЕ поле,
	// як funds_uah: вклад — інший інструмент (є строк і фіксована ставка,
	// немає штук і ринкової ціни), тож у номінал ОВДП його не змішуємо.
	// Входить у капітал і валютні частки; сам список вкладів UI бере з
	// /api/term-deposits, тож масиву тут не тримаємо.
	DepositsUAH float64 `json:"deposits_uah,omitempty"`

	// ReserveUAH — резерв («матрац») у грн-екв.: гроші, відкладені на
	// чорний день. Четверта сутність, і за природою вона від решти
	// відрізняється — немає ні дохідності, ні строку, ні ціни.
	//
	// Входить у капітал і у валютні частки, але НЕ в дохідності й НЕ в
	// buying power: резерв — не купівельна спроможність, інакше помічник
	// реінвесту запропонував би купити папір за аварійні гроші.
	//
	// Валютні частки — свідоме розходження з готівкою брокера. Кеш на
	// рахунку стоїть лише у знаменнику («вільний кеш розводить частки»),
	// бо він от-от стане чимось іншим. Резерв же грішми й лишиться, тож
	// $5000 у матраці — справжня валютна експозиція, і рахується вона в
	// чисельнику нарівні з паперами.
	ReserveUAH float64  `json:"reserve_uah,omitempty"`
	Reserve    *Reserve `json:"reserve,omitempty"`

	// Rates — курси НБУ на сьогодні, ₴ за одиницю валюти (адитивне поле).
	// Досі курс у документ не потрапляв узагалі: UI діставав його
	// контрабандою як forecast.rate0_usd, тобто лише тоді, коли задано
	// ціль і дедлайн. А «скільки це в доларах» — питання, яке ставлять
	// незалежно від того, чи є в тебе ціль.
	Rates map[string]float64 `json:"rates,omitempty"`

	// LadderUAH — номінал, що повертається щороку, у грн-екв. (для стовпчиків
	// драбини). Income12m — очікуваний дохід (купони+погашення) по місяцях
	// на рік наперед, грн-екв.
	LadderUAH []YearAmount  `json:"ladder_uah,omitempty"`
	Income12m []MonthAmount `json:"income_12m,omitempty"`
	// Coupons12m — з них лише КУПОНИ: погашення це повернення власного
	// тіла, а не дохід, тож на питання «скільки я отримую» відповідає
	// саме цей ряд. IncomeMonthlyNow — середній місячний купонний дохід
	// за наступні 12 місяців.
	Coupons12m       []MonthAmount `json:"coupons_12m,omitempty"`
	IncomeMonthlyNow float64       `json:"income_monthly_now,omitempty"`

	// MonthInvestedUAH — куплено паперів цього місяця (перекладання
	// грошей з рахунку в папери). MonthDepositedUAH — НОВІ гроші, внесені
	// цього місяця. Прогрес рахується від поповнень: план виведений із
	// цілі й означає «скільки нових грошей треба вносити», а купівля за
	// накопичені купони до цілі не додає нічого.
	MonthInvestedUAH float64 `json:"month_invested_uah"`
	// MonthDepositedUAH — НЕТТО нових грошей за місяць: поповнення мінус
	// зняття. MonthWithdrawnUAH — самі зняття, додатнім числом, щоб UI міг
	// показати розклад, коли нетто не збігається з сумою поповнень.
	MonthDepositedUAH float64 `json:"month_deposited_uah"`
	MonthWithdrawnUAH float64 `json:"month_withdrawn_uah,omitempty"`
	MonthTargetUAH    float64 `json:"month_target_uah"`
	MonthProgressPct  int     `json:"month_progress_pct"`
	MonthIncomingUAH  float64 `json:"month_incoming_uah"` // купони+погашення в поточному місяці

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

	// FundsYieldPct — дохідність сертифікатів, зважена їхньою ринковою
	// вартістю (повна: дивіденди зі зміною ціни; де історії замало —
	// сама дивідендна). BlendedYieldPct — те й те разом: YTM облігацій і
	// дохідність фондів, зважені вкладеним.
	//
	// Тримаються ОКРЕМО навмисно. YTM — зафіксована обіцянка до погашення;
	// дохідність фонду — факт по прожитому, який завтра буде іншим. Одне
	// число замість двох сховало б цю різницю, а саме воно потім керує
	// проєкціями.
	FundsYieldPct   float64 `json:"funds_yield_pct,omitempty"`
	BlendedYieldPct float64 `json:"blended_yield_pct,omitempty"`

	// *Real — ті самі три величини після знецінення гривні, тобто в
	// сьогоднішній купівельній спроможності (адитивні поля, v0.5+).
	//
	// Навіщо парою. Номінальна — факт, який видно у виписці брокера;
	// реальна — той самий факт плюс припущення про знецінення, і саме
	// вона порівнює гривневий папір із доларовим. Доти плитки показували
	// лише номінальні, а таблиця під ними — лише реальні, тож один і той
	// самий фонд стояв на екрані двома різними числами без жодного
	// натяку, що бази різні.
	//
	// Рахуються ЗВАЖУВАННЯМ реальних, а не діленням готової номінальної:
	// знецінення торкається лише гривневих рукавів, і поділ суміші
	// цілком занизив би валютну частину.
	PortfolioYieldReal  map[string]float64 `json:"portfolio_yield_real,omitempty"`
	FundsYieldRealPct   float64            `json:"funds_yield_real_pct,omitempty"`
	BlendedYieldRealPct float64            `json:"blended_yield_real_pct,omitempty"`
	// FundsYieldBasis — ЗВІДКИ взялась дохідність фондів: «дивіденди +
	// зміна ціни», «обіцяно фондом», «дивіденди після податку» або «різні
	// основи», коли фонди міряні по-різному (адитивне поле).
	//
	// Без нього плитка зашивала основу рядком і називала ту, якої в числі
	// могло не бути: на живих даних вона писала «дивіденди + зміна ціни»
	// для фонду, у якого основою була обіцянка.
	FundsYieldBasis string `json:"funds_yield_basis,omitempty"`

	// Projection — прогноз капіталу помісячною симуляцією реальних потоків
	// (купони/погашення наявних паперів + внески, реінвест під дохідність).
	//
	// ProjectionRatePct — ставка реінвесту, що СПРАВДІ використана:
	// середня по валютних рукавах, зважена капіталом кожного. Доти сюди
	// йшла зведена дохідність (YTM облігацій + дохідність фондів), якої
	// жоден рукав не бачив — кожен рахував за власним YTM своєї валюти.
	// Число стояло поруч із кривою як пояснення до неї й нічого не
	// пояснювало.
	//
	// Goal* — прогноз і потрібний внесок під ціль (якщо задано ціль і дату).
	Projection        []ProjectionRow `json:"projection,omitempty"`
	ProjectionRatePct float64         `json:"projection_rate_pct,omitempty"`
	// Forecast — віяло прогнозів на дедлайн: скільки буде на цю дату за
	// трьох наборів допущень. Ціль — одна сума-орієнтир, з якою вони
	// порівнюються.
	Forecast *Forecast `json:"forecast,omitempty"`
	// Sensitivity — що зрушить ціль: по одному зсунутому входу за раз
	// (адитивне поле). Продовження прогнозу, а не його заміна: той каже
	// «за фактом 9%», цей — на скільки й від чого ці 9% зміняться.
	Sensitivity *Sensitivity `json:"sensitivity,omitempty"`
	// Independence — коли пасивний дохід покриє життя (адитивне поле).
	Independence *Independence `json:"independence,omitempty"`
	// Drawdown — на скільки вистачить, якщо перестати вносити й почати
	// знімати (адитивне поле).
	Drawdown *Drawdown `json:"drawdown,omitempty"`

	// Rebalance — підказка виходу на цільові валютні частки (по валютах,
	// де задано ціль). RateRisk — процентний ризик портфеля (дюрація).
	Rebalance []RebalanceRow `json:"rebalance,omitempty"`
	// Concentration — де зібрано надто щільно: один папір, одна установа,
	// один рік погашень. Показуються ВСІ рядки з заданим лімітом, не лише
	// порушення: «45% при ліміті 50%» — теж корисне знання, а список, що
	// зʼявляється лише коли вже пізно, читається як аварія.
	Concentration []ConcentrationRow `json:"concentration,omitempty"`
	RateRisk      *RateRisk          `json:"rate_risk,omitempty"`
	// Liquidity — коли гроші стають доступні (адитивне поле).
	Liquidity *Liquidity `json:"liquidity,omitempty"`

	// MarketYield — що ПЕРВИННИЙ ринок платить сьогодні: останній рівень
	// розміщення Мінфіну по кожній парі (валюта, строк).
	//
	// Єдиний зовнішній орієнтир у документі: доти PortfolioYieldPct не
	// було з чим порівняти взагалі, і питання «13.4% — це нормально?» не
	// мало відповіді ні тут, ні в Home Assistant.
	//
	// Історії тут НЕМАЄ навмисно — вона в /api/auctions/curve. Retained-
	// повідомлення це СТАН, а не довідник; те саме рішення вже записане
	// для знецінення, і ряд на кілька десятків точок роздув би стан, який
	// брокер тримає в пам'яті постійно.
	MarketYield []MarketYieldRow `json:"market_yield,omitempty"`

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
	// assumed_rate_pct прибрано. Поле оголошувалось тут, стояло в схемі з
	// поміткою «застаріле» й парсилось інтеграцією — але не читалось
	// НІКИМ: ставку проєкції давно дає portfolio_yield. Форми в нього теж
	// не було, тож задати його можна було лише сирим PUT.
	//
	// Прибирати поле з контракту загалом не можна (правило адитивності),
	// але це — виняток, який сам себе доводить: воно `omitempty`, і
	// значення в ньому не з'являлось, тож у виданому JSON його не бачив
	// жоден споживач.
	// GoalAmountUAH — ЧИННА ціль, одна. Міграція 0008 звела в неї три
	// старі цілі за рівнем амбіції; напрямок саме такий, і коментар тут
	// довго стояв навиворіт («застаріле, мігрує в реалістичну»).
	GoalAmountUAH *float64 `json:"goal_amount_uah,omitempty"`
	GoalDate      string   `json:"goal_date,omitempty"` // дедлайн (ISO), опційно
	// UAHDevaluationPct — очікуване річне знецінення гривні до твердої
	// валюти, %. Базове значення, від якого сценарії розходяться.
	UAHDevaluationPct *float64 `json:"uah_devaluation_pct,omitempty"`
	// TerminalRatePct — довгострокова гривнева ставка ОВДП, до якої
	// сповзає сьогоднішня; RateGlideYears — за скільки років.
	TerminalRatePct *float64 `json:"terminal_rate_pct,omitempty"`
	RateGlideYears  *float64 `json:"rate_glide_years,omitempty"`
	// Спадок моделі «три цілі за рівнем амбіції»: міграція 0008 замінила
	// її однією ціллю, а сценарії розвела ДОПУЩЕННЯМИ. Поля лишаються
	// запасним джерелом для GoalAmountUAH і читаються лише як fallback —
	// нового сюди не пишуть.
	GoalPessimisticUAH *float64 `json:"goal_pessimistic_uah,omitempty"`
	GoalRealisticUAH   *float64 `json:"goal_realistic_uah,omitempty"`
	GoalOptimisticUAH  *float64 `json:"goal_optimistic_uah,omitempty"`
	// Channels — канали купівлі через кому (mono, inzhur…). Форма покупки
	// показує їх у випадайці разом із тими, що вже зустрічались у лотах.
	Channels string `json:"channels,omitempty"`
	// ReinvestRank — критерій ранжування помічника:
	// plan (за замовчуванням) | rate | short | ladder.
	ReinvestRank string `json:"reinvest_rank,omitempty"`
	// Вклад як інструмент реінвесту. DepositMin* — мінімальне вкладення у
	// валюті (мажорні одиниці): нижче цієї суми простій валюти «до реінвесту»
	// не готовий, і саме ця сума — крок поради «відкрити новий вклад».
	// DepositRate*Pct — річна ставка нового вкладу, %: без неї поради у цій
	// валюті немає (поріг усе одно діє). USD/EUR за замовчуванням 100.
	DepositMinUSD     *float64 `json:"deposit_min_usd,omitempty"`
	DepositMinEUR     *float64 `json:"deposit_min_eur,omitempty"`
	DepositMinUAH     *float64 `json:"deposit_min_uah,omitempty"`
	DepositRateUSDPct *float64 `json:"deposit_rate_usd_pct,omitempty"`
	DepositRateEURPct *float64 `json:"deposit_rate_eur_pct,omitempty"`
	DepositRateUAHPct *float64 `json:"deposit_rate_uah_pct,omitempty"`
	// Резерв. MonthlyExpensesUAH — скільки коштує місяць життя, ₴. У
	// застосунку не було НІЧОГО про витрати: month_target_uah — це план
	// внесків, тобто протилежне. ReserveTargetMonths — на скільки місяців
	// хочеться мати запас (типово 3).
	//
	// Витрати живуть у налаштуваннях, а не рахуються зі знять із рахунку:
	// зняття — це і покупка холодильника, і переказ у резерв, і взагалі
	// будь-що, тож видавати їх за «місячні витрати» означало б рахувати
	// достатність резерву від випадкового числа.
	MonthlyExpensesUAH  *float64 `json:"monthly_expenses_uah,omitempty"`
	ReserveTargetMonths *float64 `json:"reserve_target_months,omitempty"`
	// IncomeTargetUAH — який пасивний дохід вважати достатнім, ₴/міс.
	// Порожньо = місячні витрати: за замовчуванням «достатньо» означає
	// «покриває життя», але ціль може бути й іншою — половина витрат теж
	// відповідь, і вигадувати за користувача ми не будемо.
	//
	// WithdrawMonthlyUAH — скільки знімати щомісяця в розрахунку «на
	// скільки вистачить». Теж порожньо = витрати.
	IncomeTargetUAH    *float64 `json:"income_target_uah,omitempty"`
	WithdrawMonthlyUAH *float64 `json:"withdraw_monthly_uah,omitempty"`
	// Ширина віяла сценаріїв, п.п. Доти обидва числа були константами в
	// коді, тобто «песимістично» означало рівно те, що хтось одного разу
	// вирішив за користувача. Дефолти ті самі (3 і 4), але тепер це його
	// припущення про ринок, а не наше.
	RateSpreadPP  *float64 `json:"rate_spread_pp,omitempty"`
	DevalSpreadPP *float64 `json:"deval_spread_pp,omitempty"`
	// Цільові частки за ВИДОМ інструмента, % капіталу. Валютна ціль
	// відповідає на «в чому я тримаю гроші», ця — на «чим я ризикую»:
	// портфель на 100% в ОВДП і портфель на 100% у фондах можуть мати
	// однакові валютні частки й геть різну поведінку.
	//
	// Резерву тут немає навмисно, і це не пропуск. Його ціль уже задана в
	// МІСЯЦЯХ ВИТРАТ — мірі, яка відповідає на питання, заради якого
	// резерв і збирають. Друга ціль у відсотках означала б дві величини,
	// що можуть суперечити одна одній, і жодного правила, яка з них
	// головна. Тому цільова частка резерву не вводиться, а виводиться:
	// витрати × місяці ÷ капітал.
	TargetBondsPct    *float64 `json:"target_bonds_pct,omitempty"`
	TargetFundsPct    *float64 `json:"target_funds_pct,omitempty"`
	TargetDepositsPct *float64 `json:"target_deposits_pct,omitempty"`
	// Ліміти концентрації, % — стеля, а не ціль. LimitISINPct: скільки
	// капіталу дозволено в ОДНОМУ папері; LimitBrokerPct: в одній установі
	// (брокер і банк — одне й те саме питання); LimitYearPct: яка частка
	// ВСІХ погашень може припадати на один рік драбини.
	//
	// Порожньо = ліміту немає, і вимір просто не показується. Дефолтів тут
	// свідомо немає: «не більше 20% в один папір» — це порада, а застосунок
	// порад не дає. Число вводить той, хто ним і живе.
	LimitISINPct   *float64 `json:"limit_isin_pct,omitempty"`
	LimitBrokerPct *float64 `json:"limit_broker_pct,omitempty"`
	LimitYearPct   *float64 `json:"limit_year_pct,omitempty"`
}

// RebalanceRow — що треба зробити, щоб вийти на цільову частку.
// DeficitUAH — скільки грн-екв бракує до цілі; BondCost* — найдешевша
// ОДИНИЦЯ входу (облігація АБО вклад — див. UnitKind), а не лише папір:
// відколи вклад із мінімумом $100/€100 став інструментом ребалансу,
// добрати частку можна ним задовго до $1000-ї облігації.
// ConvertUAH — скільки гривні сконвертувати, щоб її купити; MinPortfolioUAH —
// розмір портфеля, за якого одна така одиниця вже вписується в цільову
// частку (Feasible=false, поки не доріс).
//
// Рядок описує ВИМІР диверсифікації, а не лише валюту: питання «чи не
// перекошений портфель» ставлять і до виду інструмента, і до брокера, і
// до одного паперу, а відповідь усюди має однакову форму — ціль, факт,
// дефіцит, найдешевший крок до цілі. Тримати під кожен вимір власну
// структуру означало б чотири рази переписати ту саму арифметику.
type RebalanceRow struct {
	// Dimension — за чим міряємо: "currency" | "kind" | "broker" | "isin".
	// Порожнє = "currency" для сумісності зі старим станом, де інших
	// вимірів не існувало.
	Dimension string `json:"dimension,omitempty"`
	// Key — що саме в цьому вимірі: код валюти, вид інструмента
	// ("bonds"/"funds"/"deposits"/"reserve"), назва брокера, ISIN.
	// Для валютних рядків дублює Currency: старі споживачі читають
	// currency, нові — key.
	Key             string  `json:"key,omitempty"`
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
	// UnitKind — чим є ця одиниця входу: "bond" (найдешевша ОВДП) чи
	// "deposit" (мінімальний вклад). Керує формулюванням картки. Адитивне
	// поле; порожнє = "bond" для сумісності зі старим станом.
	UnitKind string `json:"unit_kind,omitempty"`
}

// ConcentrationRow — де портфель зібраний надто щільно.
//
// ОКРЕМИЙ тип від RebalanceRow, хоч поля й схожі. Ребаланс каже «цього
// замало, доклади», концентрація — «цього забагато, більше не додавай».
// Величини звуться однаково («ціль», «зараз»), але знак порівняння
// протилежний, і половина полів ребалансу (найдешевший вхід, скільки
// вистачає купити, скільки сконвертувати) тут не має сенсу взагалі:
// відповідь на перевищення — не купівля. Один тип на дві протилежні
// відповіді читався б як помилка рівно доти, доки нею б і не став.
//
// Порушення НЕ приховують порад у «Що купити»: це спостереження, а не
// заборона. Ліміт може бути порушений із причин, яких застосунок не
// знає, — і рішення лишається за людиною.
type ConcentrationRow struct {
	// Dimension — за чим міряємо: "isin" (один папір), "broker" (одна
	// установа), "year" (один рік погашень).
	Dimension string `json:"dimension"`
	// Key — ISIN, назва брокера/банку або рік.
	Key string `json:"key"`
	// AmountUAH — скільки грошей у цьому зосереджено, грн-екв.;
	// SharePct — яка це частка (капіталу для isin/broker, усіх погашень
	// для year); LimitPct — заданий ліміт; OverUAH — на скільки грошей
	// перевищено (0, якщо в межах).
	AmountUAH float64 `json:"amount_uah"`
	SharePct  float64 `json:"share_pct"`
	LimitPct  float64 `json:"limit_pct"`
	OverUAH   float64 `json:"over_uah,omitempty"`
	// Label — людська назва для рядка, коли ключ сам по собі мовчить
	// (опис паперу з довідника НБУ замість голого ISIN).
	Label string `json:"label,omitempty"`
}

// Liquidity — коли гроші стають доступні. Питання не про дохідність, а
// про те, що робити, коли гроші раптом знадобились.
//
// NowUAH — на рахунках просто зараз. In30UAH / In90UAH — НАКОПИЧУВАЛЬНО:
// стільки буде в розпорядженні через місяць і через квартал, якщо нічого
// не купувати; сюди входять купони, погашення й тіла вкладів, що
// гасяться у вікні. LockedUAH — тіла вкладів зі строком далі, з
// найближчою датою UnlockDate.
//
// ОБЛІГАЦІЙ у «замкненому» немає навмисно. Продати їх на вторинному
// ринку можна, але застосунок ринкової ціни не моделює (те саме рішення
// записане в domain/xirr.go), і вигадане число тут було б гірше за чесну
// відсутність.
// Reserve — стан «матраца»: скільки відкладено, у чому і чи вистачає.
//
// Достатність міряється ДВОМА мірами, бо вони відповідають на різні
// питання. Months («на скільки місяців витрат вистачить») — про життя:
// саме заради нього резерв і збирають. SharePct («яка це частка
// капіталу») — про портфель: він показує, чи не з'їв матрац усе, що мало
// працювати. Одна міра без другої бреше: 12 місяців витрат можуть бути
// і 5%, і 60% капіталу, і це геть різні ситуації.
//
// Months = 0, коли не задано місячні витрати: без них питання «на скільки
// вистачить» не має відповіді, і вигадувати її не будемо.
type Reserve struct {
	UAH        float64            `json:"uah"`
	ByCurrency map[string]float64 `json:"by_currency,omitempty"` // нативно
	SharePct   float64            `json:"share_pct"`
	// Months — на скільки місяців витрат вистачить; TargetMonths — ціль;
	// TargetUAH — та ціль у грошах; GapUAH — скільки ще докласти (0, якщо
	// вистачає). MonthlyExpensesUAH — витрати, від яких усе пораховано:
	// без них число «3.4 місяця» неможливо перевірити.
	Months             float64 `json:"months,omitempty"`
	TargetMonths       float64 `json:"target_months,omitempty"`
	TargetUAH          float64 `json:"target_uah,omitempty"`
	GapUAH             float64 `json:"gap_uah,omitempty"`
	MonthlyExpensesUAH float64 `json:"monthly_expenses_uah,omitempty"`
	// Places — де лежить, грн-екв. по місцях зберігання. Резерв тим і
	// цінний, що доступний миттєво, а це залежить від місця.
	Places map[string]float64 `json:"places,omitempty"`
	// LastMove — дата останнього руху (ISO). Резерв, якого не чіпали рік,
	// і резерв, з якого щойно взяли, — різні речі.
	LastMove string `json:"last_move,omitempty"`
}

type Liquidity struct {
	NowUAH  float64 `json:"now_uah"`
	In30UAH float64 `json:"in_30_uah"`
	In90UAH float64 `json:"in_90_uah"`
	// ReserveUAH — резерв, доступний негайно. ОКРЕМИМ полем, а не додатком
	// до NowUAH: на нього спирається інваріант now_uah == account_uah, який
	// зводить звіт про рух коштів зі зведенням (TestCashflowStatementReconciles).
	// LockedUAH теж не підходить — воно означає «доведеться щось ламати», а
	// резерв на те й резерв, що ламати нічого не треба.
	ReserveUAH float64 `json:"reserve_uah,omitempty"`
	LockedUAH  float64 `json:"locked_uah,omitempty"`
	UnlockDate string  `json:"unlock_date,omitempty"`
}

// RateRisk — те, що ставки роблять із портфелем. Це ДВА різні ризики, і
// раніше вони були злиті в одне число.
//
// DurationYears / ModifiedDur / PVUAH / ByCurrency / Scenarios — ЦІНОВИЙ
// ризик і лише ОВДП: тільки в них є вторинний ринок, де ціна ходить
// проти ставки. Вклад переоцінити нікуди — сума погашення в договорі, —
// тож його потоки сюди більше не входять. Доти входили, і портфель із
// самого вкладу на 100 000 ₴ показував приведену вартість 125 054 ₴.
//
// Reinvest* — ризик ПЕРЕВКЛАДЕННЯ, ОВДП + вклади. Питання не «скільки це
// коштує сьогодні», а «коли гроші повернуться й за якою ставкою їх
// доведеться вкладати заново». Фонди не входять: сертифікат не
// гаситься, повертати нічого.
type RateRisk struct {
	DurationYears float64            `json:"duration_years"`
	ModifiedDur   float64            `json:"modified_dur"`
	PVUAH         float64            `json:"pv_uah"`
	ByCurrency    map[string]float64 `json:"by_currency,omitempty"`
	Scenarios     []RiskScenario     `json:"scenarios,omitempty"`
	// ReinvestYears — середній строк повернення грошей, зважений сумами
	// БЕЗ дисконтування: питання «коли», а не «скільки це варте нині».
	// ReturningUAH — скільки всього повернеться, грн-екв.
	// ReinvestSoonUAH — скільки з цього протягом 12 місяців: саме ці
	// гроші доведеться перевкладати за ставкою, якої ще не знаєш.
	ReinvestYears   float64 `json:"reinvest_years,omitempty"`
	ReturningUAH    float64 `json:"returning_uah,omitempty"`
	ReinvestSoonUAH float64 `json:"reinvest_soon_uah,omitempty"`
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
	// Curve — та сама модель, але помісячно, а не самим кінцем.
	//
	// Рядки вище кажуть, ЧИМ усе скінчиться; крива — коли саме траєкторія
	// розходиться з планом. А розходиться вона не рівномірно: перші роки
	// будуються з фактичного календаря виплат, тож мають форму, якої в
	// сухому складному відсотку немає.
	Curve *ForecastCurve `json:"curve,omitempty"`
}

// ForecastCurve — помісячна траєкторія капіталу до дедлайну, у
// СЬОГОДНІШНІХ гривнях (як і суми в рядках прогнозу).
type ForecastCurve struct {
	// StepMonths — крок між точками. Не одиниця навмисно: на десятирічному
	// горизонті помісячна крива це 120 точок на серію, тобто півтисячі
	// чисел у документі заради лінії, у якій сусідні місяці візуально не
	// відрізняються.
	StepMonths int `json:"step_months"`
	// GoalUAH — лінія цілі, щоб UI не діставав її з іншого місця й не
	// малював криву проти числа, якого в цьому ж об'єкті немає.
	GoalUAH float64              `json:"goal_uah,omitempty"`
	Points  []ForecastCurvePoint `json:"points"`
}

// ForecastCurvePoint — один зріз. Місяць 0 — це сьогодні.
//
// Optimistic/Pessimistic задають коридор ринку, Plan іде посередині.
// Actual зʼявляється лише коли відомий фактичний темп поповнень — і саме
// його розрив із Plan показує, що це поведінка, а не ринок.
type ForecastCurvePoint struct {
	Month       int     `json:"month"`
	Plan        float64 `json:"plan"`
	Optimistic  float64 `json:"optimistic,omitempty"`
	Pessimistic float64 `json:"pessimistic,omitempty"`
	Actual      float64 `json:"actual,omitempty"`
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
	RatePct         float64 `json:"rate_pct"`                    // сьогоднішня дохідність гривневої частини
	RateTerminalPct float64 `json:"rate_terminal_pct,omitempty"` // куди вона сповзає
	ContribMonthly  float64 `json:"contrib_monthly"`             // припущений внесок, ₴/міс
	DevaluationPct  float64 `json:"devaluation_pct"`             // припущене знецінення гривні, %/рік
	GoalPct         float64 `json:"goal_pct,omitempty"`
	GoalMonths      int     `json:"goal_months,omitempty"`
	GoalDate        string  `json:"goal_date,omitempty"`
	// ByCurrency — розклад по валютних рукавах: під що саме росте кожна
	// валюта і скільки грошей у неї спрямовується.
	ByCurrency []SleeveRow `json:"by_currency,omitempty"`
}

// Sensitivity — що станеться з ціллю, якщо змінити ОДИН вхід.
//
// Відповідь на питання, з яким лишається користувач після картки
// прогнозу: «за фактом покривається 9% — а що саме змінити». Кожен рядок
// рухає рівно один важіль; змішані сценарії виглядають переконливіше,
// але в них не видно, що саме дало ефект.
//
// Рядки НЕ відсортовані «найкращий зверху» і не мають позначки
// «рекомендовано» — це наслідки припущень, а не поради. Половина
// важелів (ставка, знецінення) від людини взагалі не залежить.
type Sensitivity struct {
	// BaseContribUAH — від чого відштовхуються важелі внеску;
	// BaseFrom — "actual" (фактичний темп) або "plan". Фактичний
	// береться, коли відомий: людина стоїть там, де стоїть, і «×2 від
	// плану, якого вона не тягне» — марна відповідь.
	BaseContribUAH float64 `json:"base_contrib_uah"`
	BaseFrom       string  `json:"base_from"`
	// Базовий результат — із чим порівнюються рядки. GoalMonths: -1 =
	// вже досягнуто, 0 = не досягається за 60 років.
	BaseGoalMonths int     `json:"base_goal_months"`
	BaseGoalDate   string  `json:"base_goal_date,omitempty"`
	BaseAmountUAH  float64 `json:"base_amount_uah"`
	BaseGoalPct    float64 `json:"base_goal_pct"`
	// Умови, спільні для всіх рядків.
	GoalUAH        float64          `json:"goal_uah"`
	DeadlineMonths int              `json:"deadline_months"`
	Rows           []SensitivityRow `json:"rows,omitempty"`
}

// Independence — коли пасивний дохід покриє життя.
//
// Продовження картки «Пасивний дохід», а не окреме питання: та каже,
// скільки портфель приноситиме, ця — коли цього стане досить. Досить —
// це IncomeTargetUAH із налаштувань, зі спадом на місячні витрати.
//
// Дві дати навмисно. За планом — якщо вносити стільки, скільки виходить
// із цілі; за фактом — якщо вносити стільки, скільки виходить насправді.
// Одна дата без другої або лестить, або лякає, а різниця між ними — це
// рівно ціна дисципліни.
type Independence struct {
	// TargetUAH — який дохід вважаємо достатнім, ₴/міс; IncomeNowUAH —
	// скільки портфель приносить уже зараз.
	TargetUAH    float64 `json:"target_uah"`
	IncomeNowUAH float64 `json:"income_now_uah"`
	// TargetFrom — "setting" (задано явно) чи "expenses" (спад на місячні
	// витрати). Читач має право знати, з чим саме порівнюють.
	TargetFrom string `json:"target_from"`
	// PlanMonths / ActualMonths: -1 = дохід уже покриває, 0 = не
	// досягається за 60 років. ActualMonths від'ємний лишається нулем,
	// коли фактичного темпу ще немає.
	PlanMonths   int    `json:"plan_months"`
	PlanDate     string `json:"plan_date,omitempty"`
	ActualMonths int    `json:"actual_months,omitempty"`
	ActualDate   string `json:"actual_date,omitempty"`
	// CapitalUAH — скільки капіталу буде на плановий момент, у
	// сьогоднішніх гривнях. Не «скільки треба»: потрібна сума залежить від
	// ставки на той момент, і називати її окремо означало б дати друге,
	// незалежне число про те саме.
	CapitalUAH float64 `json:"capital_uah,omitempty"`
}

// Drawdown — на скільки вистачить, якщо перестати вносити й почати
// знімати. Питання, зворотне до всього іншого в застосунку.
//
// Одне число тут спирається на чотири припущення, і вони названі в
// domain/drawdown.go: знімаємо спершу готівку, потім працююче; замкнене
// достроково НЕ продаємо; зняття задане в сьогоднішніх гривнях; між
// валютами ділиться пропорційно їхнім часткам.
type Drawdown struct {
	// WithdrawUAH — скільки знімати щомісяця, ₴ у сьогоднішніх грошах;
	// WithdrawFrom — "setting" чи "expenses".
	WithdrawUAH  float64 `json:"withdraw_uah"`
	WithdrawFrom string  `json:"withdraw_from"`
	// Months — на скільки місяців вистачить; -1 = не вичерпується за 60
	// років, бо потоки покривають зняття. Нуль означає, що не вистачає
	// навіть на перший місяць, і це теж відповідь.
	Months int    `json:"months"`
	Until  string `json:"until,omitempty"`
	// CoveredPct — яку частку зняття покриває сьогоднішній дохід. Саме
	// вона пояснює, ЧОМУ число таке: покриття 100% означає, що портфель
	// живе з потоку й тіла не чіпає.
	CoveredPct float64 `json:"covered_pct"`
}

// SensitivityRow — один зсунутий вхід.
//
// Заповнене рівно одне зі зсувів: Factor (внесок, ціль), DeltaPP
// (ставка, знецінення) або DeltaMonths (дедлайн). Value — сама величина
// ПІСЛЯ зсуву, у своїй одиниці: ₴/міс для внеску, % для знецінення, п.п.
// для ставки, ₴ для цілі, місяців для дедлайну.
//
// Підписів тут немає навмисно: складати «внесок ×2» у бекенді означало б
// тримати форматування у двох місцях — тут і в панелі.
type SensitivityRow struct {
	Lever       string  `json:"lever"` // contrib | rate | deval | deadline | goal
	Factor      float64 `json:"factor,omitempty"`
	DeltaPP     float64 `json:"delta_pp,omitempty"`
	DeltaMonths int     `json:"delta_months,omitempty"`
	Value       float64 `json:"value"`
	// GoalMonths / GoalDate — коли ціль буде досягнута за цього входу.
	// Для важеля «дедлайн» вони БАЗОВІ, і це не помилка: місяць
	// досягнення від дедлайну не залежить — він каже, коли ціль буде
	// досягнута, а не коли її чекають.
	GoalMonths int    `json:"goal_months"`
	GoalDate   string `json:"goal_date,omitempty"`
	// AmountUAH — скільки буде на дедлайн, у сьогоднішніх гривнях;
	// GoalPct — яка це частка цілі.
	AmountUAH float64 `json:"amount_uah"`
	GoalPct   float64 `json:"goal_pct"`
}

// SleeveRow — один валютний рукав сценарію. Amount — у НАТИВНІЙ валюті.
type SleeveRow struct {
	Currency        string  `json:"currency"`
	RatePct         float64 `json:"rate_pct"`
	RateTerminalPct float64 `json:"rate_terminal_pct,omitempty"`
	ContribMonthly  float64 `json:"contrib_monthly"` // ₴/міс, що йдуть у цю валюту
	Amount          float64 `json:"amount"`
}

// FundPositionRow — позиція в одному фонді. YieldNetPct — дивідендна
// дохідність ПІСЛЯ податку за останні 12 місяців: дивіденд фонду
// оподатковується, на відміну від купона ОВДП, тож до податку ці числа
// непорівнянні.
//
// ОДИНИЦІ. Усі СУМИ тут — у грн-еквіваленті: собівартість, ринкова
// вартість, дивіденди, податок, реалізоване. Currency лишається, бо
// каже, В ЧОМУ фонд номінований (це потрібно і для дохідності, і для
// помічника), але суми вже переведені.
//
// Виняток один і навмисний — LastPrice: це ціна ОДНОГО сертифіката, вона
// нативна й показується поруч із Currency. Ціна за штуку й підсумок —
// різні за природою величини.
//
// Доти в гривні була лише MarketValue, а решта лишалась нативною, хоч UI
// форматує їх усі як гривню. Для гривневого фонду це збігалось і тому не
// помічалось.
type FundPositionRow struct {
	Fund          string  `json:"fund"`
	Currency      string  `json:"currency"`
	Qty           int64   `json:"qty"`
	CostBasis     float64 `json:"cost_basis"`
	LastPrice     float64 `json:"last_price"`
	LastPriceDate string  `json:"last_price_date,omitempty"`
	MarketValue   float64 `json:"market_value"`
	DividendsNet  float64 `json:"dividends_net"`
	DividendsTax  float64 `json:"dividends_tax"`
	Realized      float64 `json:"realized,omitempty"`
	YieldNetPct   float64 `json:"yield_net_pct,omitempty"`
	// TotalPct — ПОВНА дохідність позиції, % річних: дивіденди після
	// податку разом зі зміною ціни (XIRR по операціях фонду з
	// термінальною ринковою вартістю). YieldNetPct вище — лише дохідна
	// частина; поряд з облігацією її самої мало, бо YTM ловить і купон,
	// і дисконт, тобто весь дохід паперу.
	//
	// RealPct — вона ж після знецінення гривні, тобто в сьогоднішній
	// купівельній спроможності: саме вона порівнянна з ОВДП і вкладом.
	// YieldBasis каже, з чого число взялося, — і зокрема те, що у фонду
	// це ФАКТ по прожитому, а не обіцянка на майбутнє, як YTM. Коли
	// історії замало для ануалізації, TotalPct порожній, а RealPct
	// рахується з самих дивідендів — про що YieldBasis і повідомляє.
	TotalPct float64 `json:"total_pct,omitempty"`
	// ExpectedPct — обіцяна фондом дохідність, % (з довідника). Не
	// вимірюється, а задається людиною, тож стоїть окремо від TotalPct і
	// YieldNetPct: yield_basis каже, котре з трьох потрапило в RealPct.
	//
	// ExpectedCurrency — валюта, В ЯКІЙ обіцянка дана. Може відрізнятись
	// від валюти сертифіката: гривневий фонд, чия ціна йде за курсом НБУ,
	// обіцяє у доларах, і гривневе знецінення до такої обіцянки не
	// застосовується — приріст ціни в гривні і є тією компенсацією.
	//
	// ExpectedPct — завжди СКЛАДНА річна, хоч би як її назвав фонд: воно
	// стоїть поруч із YTM облігації, іде в помічник реінвесту й задає
	// ставку росту в проєкції, а там усюди складна.
	ExpectedPct      float64 `json:"expected_pct,omitempty"`
	ExpectedCurrency string  `json:"expected_currency,omitempty"`
	// ExpectedSimplePct / ExpectedSimpleYears — обіцянка ЯК ЇЇ НАЗИВАЄ
	// ФОНД, коли вона задана простою середньорічною: 25% за 3 роки.
	// Порожні, коли обіцянка й так складна.
	//
	// Потрібні саме для показу. Проста 25% за три роки — це ×1.75, тобто
	// 20.5% складних, і без цієї пари людина бачила б у картці 20.5% там,
	// де сама вписала 25, без жодного способу зрозуміти різницю.
	ExpectedSimplePct   float64 `json:"expected_simple_pct,omitempty"`
	ExpectedSimpleYears int     `json:"expected_simple_years,omitempty"`
	RealPct             float64 `json:"real_pct,omitempty"`
	YieldBasis          string  `json:"yield_basis,omitempty"`
	// NextPayout — коли фонд заплатить наступного разу (ISO). Рахується з
	// дня виплати в довіднику з поправкою на вихідні; порожньо = день не
	// заданий. Сума не прогнозується: вона залежить від результату місяця.
	NextPayout string `json:"next_payout,omitempty"`
	// Kind — «accum» для накопичувального фонду, порожньо для
	// розподільного. CloseDate — коли фонд закривається й повертає гроші;
	// BuyUntil — доки його можна купити. IncomeTaxPct — податок на дохід.
	//
	// Усе це є в довіднику, але має бути й у документі: помічник реінвесту
	// й таблиця позицій читають саме його. Без цих полів обидва писали про
	// сертифікат неправду — «безстроково» й «без строку й погашення», — а
	// фонд із закритим вікном залучення далі пропонувався до купівлі.
	Kind         string  `json:"kind,omitempty"`
	CloseDate    string  `json:"close_date,omitempty"`
	BuyUntil     string  `json:"buy_until,omitempty"`
	IncomeTaxPct float64 `json:"income_tax_pct,omitempty"`
	// Short — скільки сертифікатів продано понад куплені. Не нуль означає,
	// що в журналі бракує надходження, і всі числа рядка занижені.
	Short int64 `json:"short,omitempty"`
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
	// IncomeMonthly — скільки капітал приноситиме ЩОМІСЯЦЯ на цьому
	// горизонті, у сьогоднішніх гривнях: купонний потік, який можна
	// забирати, не проїдаючи тіло. IncomeMonthlyActual — те саме за
	// фактичним темпом поповнень.
	IncomeMonthly       float64 `json:"income_monthly,omitempty"`
	IncomeMonthlyActual float64 `json:"income_monthly_actual,omitempty"`
}

type NextPayment struct {
	Date     string  `json:"date"` // ISO
	ISIN     string  `json:"isin"`
	Type     string  `json:"type"` // coupon | redemption | early
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// MarketYieldRow — один строк однієї валюти на первинному ринку.
//
// Дата тут обов'язкова разом із рівнем: аукціони бувають раз на тиждень,
// а на валютні строки — раз на місяць і рідше, тож «3.2%» без дати не
// каже, це позавчора чи півроку тому. ISIN поруч — щоб було видно, який
// саме папір тоді розміщували.
type MarketYieldRow struct {
	Currency string  `json:"currency"`
	Bucket   string  `json:"bucket"` // строк, як його назвав НБУ: "1y", "1.5y"…
	Pct      float64 `json:"pct"`    // дохідність розміщення, до податку
	Date     string  `json:"date"`   // день аукціону, ISO
	ISIN     string  `json:"isin"`
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

// Major — мінорні одиниці в мажорні. Експортована, бо будівник кладе в
// документ сім грошових полів, і робити для кожного окремий вхід у Derive
// означало б відновити той самий п'ятдесятипольовий літерал, від якого
// Derive і рятує.
func Major(m *money.Money) float64 {
	if m == nil {
		return 0
	}
	return float64(m.Amount()) / 100.0
}

func (d *Doc) JSON() ([]byte, error) { return json.Marshal(d) }
