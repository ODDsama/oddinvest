// Реєстр налаштувань: одне місце, де ключ описаний повністю.
//
// Досі ключ доводилось вписувати в тринадцять місць, і машиною
// перевірялися лише два зв'язки з них. Наслідок був не теоретичний:
// `monthly_target_uah` лишився сутністю в Home Assistant після того, як
// бекенд прибрав ключ, тож кожен рух слайдера давав 400 — і дізнатись про
// це не було де.
//
// Тепер ключ описується ТУТ, а `settingsKeys`, перевірка на число й
// заповнення SettingsDoc виводяться з опису. Схема лишається окремою
// (вона контракт, а не код), але звіряється з реєстром тестом.

package api

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ODDsama/oddinvest/internal/state"
)

// settingDef — опис одного ключа.
//
// Num і Str — куди класти значення в SettingsDoc; заповнений рівно один
// із них, і саме він каже, число це чи рядок. Обидва nil означає
// «ключ приймається, але в документ не публікується» — так живе
// `import_since`: він робочий стан імпорту, а не політика портфеля, і
// споживає його сам імпорт.
type settingDef struct {
	Key string
	Num func(*state.SettingsDoc) **float64
	Str func(*state.SettingsDoc) *string
	// Why — навіщо ключ існує. Не для UI: це підказка тому, хто
	// наступного разу питатиме «а це ще потрібно?».
	Why string
	// Enum — допустимі значення СТРОКОВОГО ключа. Порожньо = приймається
	// будь-який рядок, і саме так живуть goal_date (дата, не перелік) та
	// reinvest_rank (невідомий режим там просто не збігається з жодною
	// гілкою рейтингу — гірше не стає).
	//
	// Там, де від значення залежить, ЧИ ВІДБУДЕТЬСЯ дія, цього замало:
	// друкарська помилка в reserve_fill_from мовчки вимкнула б подушку, і
	// шукати причину довелось би в чужих числах. Тому перелік, і перевірка
	// в тому самому validateSettings, який ділять запис і превʼю.
	Enum []string
}

// numeric — чи мусить значення бути невід'ємним числом. Виводиться з
// того, куди ключ пишеться: окремого списку більше немає, тож «додав
// ключ, забув про валідацію» стало неможливим.
func (d settingDef) numeric() bool { return d.Num != nil }

// settingsRegistry — усі ключі застосунку.
//
// Порядок групами, як їх бачить користувач у «Налаштуваннях».
var settingsRegistry = []settingDef{
	{Key: "usd_target_share_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.USDTargetSharePct },
		Why: "цільова частка валюти в капіталі"},
	{Key: "eur_target_share_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.EURTargetSharePct },
		Why: "цільова частка валюти в капіталі"},
	{Key: "goal_amount_uah", Num: func(s *state.SettingsDoc) **float64 { return &s.GoalAmountUAH },
		Why: "скільки хочу накопичити; з нього виводиться місячний план"},
	{Key: "goal_date", Str: func(s *state.SettingsDoc) *string { return &s.GoalDate },
		Why: "дедлайн цілі"},
	{Key: "uah_devaluation_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.UAHDevaluationPct },
		Why: "очікуване знецінення гривні; порожньо = виміряне з курсів"},
	{Key: "terminal_rate_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.TerminalRatePct },
		Why: "довгострокова ставка ОВДП, до якої сповзає сьогоднішня"},
	{Key: "rate_glide_years", Num: func(s *state.SettingsDoc) **float64 { return &s.RateGlideYears },
		Why: "за скільки років ставка туди сповзає"},

	{Key: "reinvest_rank", Str: func(s *state.SettingsDoc) *string { return &s.ReinvestRank },
		Why: "критерій порядку в «Що купити»"},

	{Key: "deposit_min_usd", Num: func(s *state.SettingsDoc) **float64 { return &s.DepositMinUSD },
		Why: "мінімальне вкладення у вклад; воно ж поріг «готовий до реінвесту»"},
	{Key: "deposit_min_eur", Num: func(s *state.SettingsDoc) **float64 { return &s.DepositMinEUR }},
	{Key: "deposit_min_uah", Num: func(s *state.SettingsDoc) **float64 { return &s.DepositMinUAH }},
	{Key: "deposit_rate_usd_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.DepositRateUSDPct },
		Why: "ставка НОВОГО вкладу; без неї поради у цій валюті немає"},
	{Key: "deposit_rate_eur_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.DepositRateEURPct }},
	{Key: "deposit_rate_uah_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.DepositRateUAHPct }},

	// Витрати ДВОМА ключами, а не одним. Сума й валюта нарізно тому, що
	// перевіряються вони по-різному: сума — невід'ємне число, валюта —
	// перелік. Перелік тут обов'язковий, і не для охайності: описка в коді
	// валюти мовчки помножила б ціль резерву на курс (fx.ToUAH на
	// невідомому коді віддає помилку, а гілка помилки веде в дефолт), і
	// шукати причину довелось би в чужих числах — рівно той випадок, що
	// названий при Enum вище про reserve_fill_from.
	{Key: "monthly_expenses", Num: func(s *state.SettingsDoc) **float64 { return &s.MonthlyExpenses },
		Why: "скільки коштує місяць життя; від нього достатність резерву"},
	{Key: "monthly_expenses_currency", Str: func(s *state.SettingsDoc) *string { return &s.MonthlyExpensesCurrency },
		Enum: []string{"UAH", "USD", "EUR"},
		Why:  "у якій валюті мисляться витрати; порожньо = гривня"},
	// Спадок: до 0038 витрати були гривневі за побудовою, і ключ казав це
	// назвою. Читається лише як запасне джерело MonthlyExpensesUAH — форми
	// для нього немає навмисно, нового сюди не пишуть. Той самий прийом,
	// що з goal_*_uah нижче.
	{Key: "monthly_expenses_uah", Num: func(s *state.SettingsDoc) **float64 { return &s.MonthlyExpensesUAH },
		Why: "спадок 0038; лише читається, доки не задані витрати з валютою"},
	{Key: "reserve_target_months", Num: func(s *state.SettingsDoc) **float64 { return &s.ReserveTargetMonths },
		Why: "на скільки місяців витрат хочеться запас"},
	// Ціль резерву казала, СКІЛЬКИ треба, і не казала нічого про те, звідки
	// воно візьметься: розрив до неї стояв у картці й ні на що не впливав.
	// Цей ключ і є механізм під нею — стеля на частку вільних грошей, доки
	// резерв не добраний. Порожньо = застосунок про резерв не заговорить,
	// тобто той, хто про це не просив, не побачить жодної зміни.
	{Key: "reserve_fill_share_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.ReserveFillSharePct },
		Why: "яка частка вільних грошей іде в резерв, доки він не добраний; порожньо = не пропонувати"},
	// А цей — з ЯКИХ саме грошей та стеля ріже. Стеля вже міряється від
	// планового доходу місяця, тож без цього ключа застосунок казав одне
	// («подушку наповнює план») і робив інше (різ із купонів).
	{Key: "reserve_fill_from", Str: func(s *state.SettingsDoc) *string { return &s.ReserveFillFrom },
		Enum: []string{"any", "redeem", "plan"},
		Why:  "з яких грошей наповнювати подушку: з усіх, з планових і погашень, чи лише з планових"},

	// Подушку можна тримати на строкових вкладах — але лише в темпі, у
	// якому її витрачають. Ці два числа виводити не можна: перше залежить
	// від того, якою аварією людина боїться, друге — від того, що пропонує
	// її банк. Обидва вона знає про себе сама, як і решта питань набору.
	{Key: "reserve_liquid_months", Num: func(s *state.SettingsDoc) **float64 { return &s.ReserveLiquidMonths },
		Why: "скільки місяців витрат подушки має лишатись доступними СЬОГОДНІ, поза вкладом; " +
			"аварія не витрачається помісячно, і драбина її не покриває"},
	{Key: "reserve_max_term_months", Num: func(s *state.SettingsDoc) **float64 { return &s.ReserveMaxTermMonths },
		Why: "найдовша сходинка драбини подушки, місяців; порожньо або 0 = подушку не замикати"},
	// Цілі накопичення. Стеля ОДНА на всі цілі разом, а не частка в рядку
	// кожної, і це рішення, а не спрощення.
	//
	// Частка в рядку — це число, яке треба тримати в синхроні з дедлайном
	// РУКАМИ, і воно розійдеться з ним на першій же правці дати. Потрібний
	// темп застосунок виводить сам (розрив ÷ місяців до дати), а стеля
	// відповідає на інше питання — скільки місяць узагалі витримає.
	//
	// Коли сума потрібних темпів не влазить у стелю, це й Є сигнал
	// відставання, і сказати його треба словами, а не мовчки порізати.
	{Key: "goals_fill_share_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.GoalsFillSharePct },
		Why: "яка частка планового доходу місяця йде в цілі накопичення РАЗОМ, після подушки; " +
			"порожньо = не пропонувати"},
	{Key: "goals_fill_from", Str: func(s *state.SettingsDoc) *string { return &s.GoalsFillFrom },
		Enum: []string{"any", "redeem", "plan"},
		Why:  "з яких грошей наповнювати цілі: з усіх, з планових і погашень, чи лише з планових"},

	// Борг (0045). Стеля дострокового погашення — рівно такої ж природи, що
	// й дві попередні; обовʼязкові платежі стелі не мають і в реєстрі не
	// зʼявляються, бо вони не політика, а зобовʼязання.
	{Key: "debt_fill_share_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.DebtFillSharePct },
		Why: "яка частка планового доходу місяця йде на ДОСТРОКОВЕ погашення боргу, після подушки; " +
			"порожньо = не пропонувати"},
	{Key: "debt_fill_from", Str: func(s *state.SettingsDoc) *string { return &s.DebtFillFrom },
		Enum: []string{"any", "redeem", "plan"},
		Why:  "з яких грошей гасити борг достроково: з усіх, з планових і погашень, чи лише з планових"},
	{Key: "reserve_debt_months", Num: func(s *state.SettingsDoc) **float64 { return &s.ReserveDebtMonths },
		Why: "до скількох місяців витрат обрізати ціль подушки, доки живий борг дорожчий за портфель; " +
			"порожньо = не обрізати"},
	{Key: "goals_while_debt", Str: func(s *state.SettingsDoc) *string { return &s.GoalsWhileDebt },
		Enum: []string{"keep", "pause"},
		Why:  "чи наповнювати цілі накопичення, доки є дорогий борг"},

	{Key: "income_target_uah", Num: func(s *state.SettingsDoc) **float64 { return &s.IncomeTargetUAH },
		Why: "який пасивний дохід вважати достатнім; порожньо = місячні витрати"},
	{Key: "withdraw_monthly_uah", Num: func(s *state.SettingsDoc) **float64 { return &s.WithdrawMonthlyUAH },
		Why: "скільки знімати щомісяця в розрахунку «на скільки вистачить»; порожньо = місячні витрати"},

	// Ширина віяла сценаріїв. Доти це були дві константи в коді, і
	// «песимістично» означало рівно те, що хтось одного разу вирішив за
	// користувача. Числа лишились ті самі за замовчуванням, але тепер це
	// його припущення, а не наше.
	{Key: "rate_spread_pp", Num: func(s *state.SettingsDoc) **float64 { return &s.RateSpreadPP },
		Why: "на скільки п.п. розходяться ставки сценаріїв; порожньо = 3"},
	{Key: "deval_spread_pp", Num: func(s *state.SettingsDoc) **float64 { return &s.DevalSpreadPP },
		Why: "на скільки п.п. розходиться знецінення сценаріїв; порожньо = 4"},

	{Key: "target_bonds_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.TargetBondsPct },
		Why: "цільова частка за видом інструмента"},
	{Key: "target_funds_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.TargetFundsPct }},
	{Key: "target_deposits_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.TargetDepositsPct }},
	{Key: "target_npf_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.TargetNPFPct }},

	// Податкова знижка на внески в НПФ — припущення, а не процедура.
	// Порожній ПДФО за рік і є «вимкнено»: окремого прапорця немає навмисно,
	// бо «увімкнено, але число не введене» — стан, з якого нічого не
	// порахувати. Ліміт не константа: щороку інший (ПМ працездатних × 1.4).
	{Key: "npf_credit_pdfo_year_uah", Num: func(s *state.SettingsDoc) **float64 { return &s.NPFCreditPDFOYearUAH },
		Why: "утриманий за рік ПДФО — стеля знижки; порожньо = знижку не рахувати"},
	{Key: "npf_credit_cap_month_uah", Num: func(s *state.SettingsDoc) **float64 { return &s.NPFCreditCapMonthUAH },
		Why: "ліміт внеску за місяць у знижку; 4660 ₴ у 2026, щороку інший"},

	{Key: "limit_isin_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.LimitISINPct },
		Why: "стеля концентрації; дефолту немає навмисно — це була б порада"},
	{Key: "limit_broker_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.LimitBrokerPct }},
	{Key: "limit_year_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.LimitYearPct }},

	// Спадок моделі «три цілі за рівнем амбіції»: міграція 0008 замінила
	// її однією ціллю. Ключі лишаються запасним джерелом для
	// GoalAmountUAH і читаються лише як fallback — форми для них немає
	// навмисно, нового сюди не пишуть.
	{Key: "goal_pessimistic_uah", Num: func(s *state.SettingsDoc) **float64 { return &s.GoalPessimisticUAH },
		Why: "спадок 0008; лише читається як запасне джерело цілі"},
	{Key: "goal_realistic_uah", Num: func(s *state.SettingsDoc) **float64 { return &s.GoalRealisticUAH }},
	{Key: "goal_optimistic_uah", Num: func(s *state.SettingsDoc) **float64 { return &s.GoalOptimisticUAH }},

	// Публічна адреса застосунку — та, з якої він відкривається З
	// ТЕЛЕФОНА. Пишеться сама, коли тунель підключають зі сторінки
	// «Доступ ззовні» (internal/tunnel), і лишається редагованою для тих,
	// у кого свій реверс-проксі.
	//
	// НАЛАШТУВАННЯ, А НЕ СЕКРЕТ: адреса публічна за визначенням, і саме
	// тому вона в документі стану — Home Assistant бере її звідти для
	// посилань у сповіщеннях, замість того щоб питати вдруге у власному
	// майстрі налаштування. Реквізити тунелю поруч із нею НЕ лежать: вони
	// в таблиці secrets (міграція 0053).
	{Key: "public_url", Str: func(s *state.SettingsDoc) *string { return &s.PublicURL },
		Why: "адреса, з якої застосунок відкривається ззовні; звідси її бере HA"},

	// Не публікується в документ: це стан імпорту, а не політика.
	// Редагованим лишається навмисно — інакше «перезавантажити
	// позаминулий місяць» стало б неможливим.
	{Key: "import_since", Why: "водяний знак імпорту виписки"},
}

// settingsKeys — ключі, які приймає API. Виводяться з реєстру.
var settingsKeys = func() []string {
	out := make([]string, 0, len(settingsRegistry))
	for _, d := range settingsRegistry {
		out = append(out, d.Key)
	}
	return out
}()

var settingsByKey = func() map[string]settingDef {
	out := make(map[string]settingDef, len(settingsRegistry))
	for _, d := range settingsRegistry {
		out[d.Key] = d
	}
	return out
}()

// loadSettings збирає SettingsDoc одним проходом по реєстру.
//
// Доти двадцять ключів читались циклом, ще шість — окремими блоками
// поруч, і два (uah_devaluation_pct, import_since) мали ТРЕТЄ читання в
// інших файлах. Тепер прохід один; хто потребує сирого значення поза
// документом, бере його з тієї самої мапи, але вже знає, що список
// ключів не в нього.
//
// МАПА, А НЕ КОНТЕКСТ, і це не стиль. Доти кожен ключ реєстру читався
// власним SELECT — сорок один запит на кожну збірку документа, тобто на
// кожній із шести десятків мутацій і на кожному читанні, до таблиці, яка
// цілком менша за один рядок лоту. Тепер її читають ОДИН раз (AllSettings)
// там, де вже читають усе інше, — у loadSources.
//
// Відсутній ключ дає порожній рядок — рівно те, що робив GetSetting, тож
// «не задано» веде до дефолту так само, як і раніше.
func loadSettings(raw map[string]string) *state.SettingsDoc {
	doc := &state.SettingsDoc{}
	for _, d := range settingsRegistry {
		applySetting(doc, d, raw[d.Key])
	}
	return doc
}

// applySetting кладе СИРЕ значення ключа в документ.
//
// Винесене з циклу вище не заради охайності, а тому, що споживачів стало
// двоє: прочитане зі сховища й НАКЛАДКА (overrideSettings нижче). Другий
// розбір тих самих рядків означав би, що превʼю політики вміє прочитати
// число інакше, ніж його прочитає застосунок після запису, — і різницю
// між ними ніхто б не побачив, бо обидва відповіді виглядають правдиво.
//
// Порожнє значення ПРИБИРАЄ налаштування — те саме, що в PUT /api/settings,
// і саме тому тут явний nil, а не "пропустити". У свіжому документі різниці
// немає, у накладці поверх прочитаного вона вся: набір, який мовчить про
// ціль НПФ, мусить її стерти, а не лишити від попереднього.
//
// Сміття лишає значення незайманим: сюди воно доходить лише повз
// validateSettings, тобто ніколи, — а падати на розборі того, що вже
// лежить у базі, довелось би на кожній сторінці.
func applySetting(doc *state.SettingsDoc, d settingDef, raw string) {
	raw = strings.TrimSpace(raw)
	switch {
	case d.Num != nil:
		if raw == "" {
			*d.Num(doc) = nil
			return
		}
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			*d.Num(doc) = &f
		}
	case d.Str != nil:
		*d.Str(doc) = raw
	}
}

// overrideSettings — політика, якої ще немає, поверх прочитаної.
//
// Перебирається РЕЄСТР, а не мапа: порядок тоді сталий, а ключ, якого в
// реєстрі немає, не має шансу дійти сюди мовчки (його вже відхилив
// validateSettings).
func overrideSettings(doc *state.SettingsDoc, over map[string]string) {
	for _, d := range settingsRegistry {
		if raw, ok := over[d.Key]; ok {
			applySetting(doc, d, raw)
		}
	}
}

// validateSettings — чи можна такі значення взагалі приймати.
//
// Спільна для запису (PUT /api/settings) і для превʼю політики. Доти
// перевірка жила всередині циклу запису, і превʼю мусило б завести другу —
// тобто рівно той випадок, від якого застережено в шапці цього файлу:
// ключ, описаний у двох місцях, розходиться тихо.
//
// Порожнє значення дозволене й означає «прибрати»: саме так знецінення
// повертається з ручного на виміряне.
func validateSettings(req map[string]string) error {
	for k, v := range req {
		d, ok := settingsByKey[k]
		if !ok {
			return fmt.Errorf("невідомий ключ %q", k)
		}
		if v == "" {
			continue
		}
		if !d.numeric() {
			// Перелік є не в кожного строкового ключа — аргумент при Enum.
			if len(d.Enum) > 0 && !slices.Contains(d.Enum, strings.TrimSpace(v)) {
				return fmt.Errorf("%s: %q — не одне з %s", k, v, strings.Join(d.Enum, ", "))
			}
			continue
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return fmt.Errorf("%s: %q не число", k, v)
		}
		if f < 0 {
			return fmt.Errorf("%s: від'ємне значення %v", k, f)
		}
	}
	return nil
}
