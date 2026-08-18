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
	"context"
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

	{Key: "monthly_expenses_uah", Num: func(s *state.SettingsDoc) **float64 { return &s.MonthlyExpensesUAH },
		Why: "скільки коштує місяць життя; від нього достатність резерву"},
	{Key: "reserve_target_months", Num: func(s *state.SettingsDoc) **float64 { return &s.ReserveTargetMonths },
		Why: "на скільки місяців витрат хочеться запас"},
	// Ціль резерву казала, СКІЛЬКИ треба, і не казала нічого про те, звідки
	// воно візьметься: розрив до неї стояв у картці й ні на що не впливав.
	// Цей ключ і є механізм під нею — стеля на частку вільних грошей, доки
	// резерв не добраний. Порожньо = застосунок про резерв не заговорить,
	// тобто той, хто про це не просив, не побачить жодної зміни.
	{Key: "reserve_fill_share_pct", Num: func(s *state.SettingsDoc) **float64 { return &s.ReserveFillSharePct },
		Why: "яка частка вільних грошей іде в резерв, доки він не добраний; порожньо = не пропонувати"},
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
// документом, бере його через GetSetting так само, але вже знає, що
// список ключів не в нього.
//
// Помилка читання й порожнє значення ведуть в одне місце — до дефолту,
// тож розрізняти їх нема сенсу.
func (s *Server) loadSettings(ctx context.Context) *state.SettingsDoc {
	doc := &state.SettingsDoc{}
	for _, d := range settingsRegistry {
		raw, _ := s.st.GetSetting(ctx, d.Key) //nolint:errcheck // порожньо = не задано; помилка веде туди ж
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		switch {
		case d.Num != nil:
			if f, err := strconv.ParseFloat(raw, 64); err == nil {
				*d.Num(doc) = &f
			}
		case d.Str != nil:
			*d.Str(doc) = raw
		}
	}
	return doc
}
