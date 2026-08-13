// Проєкція капіталу, місячний план і віяло прогнозів.
//
// Восьма фаза розбиття buildState — і найбільша. Проєкція залежить від
// УСІХ інструментів одразу, тож її вхід навмисно виписаний полем за
// полем: серед сотні локальних змінних цю залежність не було видно
// взагалі, і саме тому сюди роками не потрапляли то вклади, то фонди.
//
// Модель — помісячна симуляція РЕАЛЬНИХ потоків (купони й погашення
// наявних паперів) плюс внески, з реінвестом під дохідність портфеля.
// Готівка не працює, поки не реінвестована. Це замість сухої формули
// складного відсотка: біля-термінова частина будується з фактичного
// календаря виплат.
//
// Кожна валюта — ОКРЕМИЙ рукав у нативній валюті: своя дохідність, свій
// календар, свій поріг докупівлі. Інакше гривневий папір під 16% завжди
// бив би доларовий під 4%, бо модель просто не бачила б, що гривня
// знецінюється.
package api

import (
	"math"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"

	money "github.com/Rhymond/go-money"
)

// goalHorizonMonths — 60 років; далі ціль вважаємо недосяжною.
const goalHorizonMonths = 720

// Ширина віяла сценаріїв за замовчуванням, п.п. Не «правильні» числа, а
// розумний старт: розкид вішається на ДОВГОСТРОКОВУ ставку, бо
// сьогоднішня — факт, за яким можна купити просто зараз, а припущенням є
// те, куди вона прийде.
const (
	defaultRateSpreadPP  = 3.0
	defaultDevalSpreadPP = 4.0
)

// scenarioDef — один набір допущень віяла: що вносимо і який ринок.
//
// Тип рівня пакета, а не локальний, бо ті самі набори проганяє ще й
// крива (buildForecastCurve). Доти він жив усередині функції, і крива
// мусила б їх перелічити вдруге — з тим самим шансом розійтись, який уже
// коштував нам двох визначень капіталу.
type scenarioDef struct {
	key, label             string
	contrib, ratePP, deval float64
}

// projectionInput — усе, від чого залежить проєкція.
//
// Перелік довгий, і це не вада, а те, заради чого фаза виділена: він
// каже вголос, що проєкція бачить кожен інструмент. Доти на це питання
// відповідав лише уважний перечит 1800 рядків, і відповідь двічі
// виявлялась «не всі».
type projectionInput struct {
	// Capital — старт кривої; Cashflow — реальні майбутні потоки.
	Capital  state.Capital
	Cashflow []domain.CashflowItem
	Settings *state.SettingsDoc

	// Стан по валютах: готівка (мінорні), номінал ОВДП (мінорні), тіло
	// вкладів і ринкова вартість сертифікатів (мажорні, нативно).
	CashByCur        map[string]int64
	NominalByCur     map[string]int64
	DepositBodyByCur map[string]float64
	FundValueByCur   map[string]float64
	// AccumByCur — накопичувальні позиції фондів. У FundValueByCur їх
	// немає: там замкнений капітал, а ці ростуть.
	AccumByCur map[string][]domain.Accum

	// Ставки: дохідність портфеля по валютах, запасна середня для валюти,
	// якої ще немає, і поріг докупівлі.
	YieldByCur       map[string]float64
	AvgRateByCur     map[string]float64
	ReinvestMinByCur map[string]float64

	Rates fx.Rates
	// Deval — річне знецінення гривні. Те саме число, що й у дохідностях:
	// прогноз і помічник зобов'язані виходити з одного припущення, інакше
	// вони суперечать одне одному на одному екрані.
	Deval float64
	// ActualMonthly — фактичний темп поповнень, ₴/міс (0 = історії замало).
	ActualMonthly float64
	// IncomeMonthlyNow — скільки портфель приносить УЖЕ, ₴/міс. Готове
	// число з фази доходу, а не порахуване вдруге: те саме значення на
	// двох сусідніх картках мусить бути тим самим.
	IncomeMonthlyNow float64
	Today            domain.Date
}

// projectionPhase — те, що проєкція віддає документу.
type projectionPhase struct {
	Rows     []state.ProjectionRow
	Forecast *state.Forecast
	// CapRatePct — ставка, ЯКУ ПРОЄКЦІЯ СПРАВДІ ВЖИЛА: середня по рукавах,
	// зважена капіталом кожного в грн-екв.
	//
	// Доти сюди йшла зведена дохідність (YTM облігацій + дохідність
	// фондів), і поле projection_rate_pct обіцяло «ставку реінвесту, що
	// використана», хоч жоден рукав її не бачив: кожен рахував за власним
	// YTM своєї валюти. Число стояло на екрані як пояснення до кривої,
	// якої воно не пояснювало.
	CapRatePct float64
	// ContribM — місячний внесок плану, виведений із цілі й дедлайну;
	// TargetUAH — він самий грішми (нуль, якщо цілі або дедлайну немає).
	ContribM  float64
	TargetUAH *money.Money
	// Sensitivity — «що як»: наслідки зсуву одного входу за раз
	// (state_sensitivity.go). Рахується тут, бо потребує тієї самої
	// фабрики рукавів, цілі й дедлайну; окремо вони довелось би вивести
	// вдруге, і два визначення дедлайну розійшлися б.
	Sensitivity *state.Sensitivity
	// Independence — коли дохід покриє життя (state_independence.go).
	// Теж тут і з тієї самої причини: потрібна та сама фабрика.
	Independence *state.Independence
	// Drawdown — на скільки вистачить без внесків (там само).
	Drawdown *state.Drawdown
}

// sleeveFactory — усе, що потрібно, щоб зібрати валютні рукави під
// довільний внесок і зсув ставки.
//
// Винесено з тіла buildProjection, бо рукави потрібні ще й фазі
// чутливості (state_sensitivity.go): вона проганяє ту саму модель із
// збуреним входом. Тримати збірку замиканням означало б або дублювати її
// там, або тягти всю проєкцію в чужу фазу.
type sleeveFactory struct {
	in     projectionInput
	share  map[string]float64
	coupon map[string]map[int]float64
	redeem map[string]map[int]float64
	// terminalUAH — куди прийде гривнева ставка; glideYears — за скільки
	// років вона туди сповзе. Валютні рукави лишаються на своїй ставці.
	terminalUAH float64
	glideYears  float64
}

func newSleeveFactory(in projectionInput) sleeveFactory {
	today := in.Today
	f := sleeveFactory{
		in:          in,
		share:       map[string]float64{},
		coupon:      map[string]map[int]float64{},
		redeem:      map[string]map[int]float64{},
		terminalUAH: defaultTerminalRatePct,
		glideYears:  defaultGlideYears,
	}

	// Реальні майбутні потоки, розкладені по валютах і місяцях.
	for _, cf := range in.Cashflow {
		cur := cf.Amount.Currency().Code
		mi := (cf.Date.Year()-today.Year())*12 + int(cf.Date.Month()) - int(today.Month())
		if mi < 1 {
			mi = 1
		}
		dst := f.coupon
		if cf.Type == domain.PayRedemption {
			dst = f.redeem
		}
		if dst[cur] == nil {
			dst[cur] = map[int]float64{}
		}
		dst[cur][mi] += float64(cf.Amount.Amount()) / 100
	}

	// Куди підуть майбутні поповнення: за цільовими валютними частками.
	// Це вже задано в налаштуваннях, тож нової здогадки не вводимо.
	if in.Settings.USDTargetSharePct != nil {
		f.share[money.USD] = *in.Settings.USDTargetSharePct / 100
	}
	if in.Settings.EURTargetSharePct != nil {
		f.share[money.EUR] = *in.Settings.EURTargetSharePct / 100
	}
	if rest := 1 - f.share[money.USD] - f.share[money.EUR]; rest > 0 {
		f.share[money.UAH] = rest
	} else {
		f.share[money.UAH] = 0
	}

	if in.Settings.TerminalRatePct != nil && *in.Settings.TerminalRatePct >= 0 {
		f.terminalUAH = *in.Settings.TerminalRatePct
	}
	if in.Settings.RateGlideYears != nil && *in.Settings.RateGlideYears >= 0 {
		f.glideYears = *in.Settings.RateGlideYears
	}
	return f
}

// build збирає рукави під заданий сумарний внесок і зсув ставки.
func (f sleeveFactory) build(contribTotal, ratePP float64) []domain.Sleeve {
	in, share := f.in, f.share
	var sleeves []domain.Sleeve
	for _, cur := range []string{money.UAH, money.USD, money.EUR} {
		cash := float64(in.CashByCur[cur]) / 100
		// Замкнений капітал — це НЕ лише номінал ОВДП. Тіло вкладу
		// поводиться точно як номінал паперу: лежить, платить за
		// відомим графіком і повертається в кінці строку, — а
		// сертифікат лежить безстроково й платить дивідендами. Обидва
		// потоки вже стоять у Coupon/Redeem нижче.
		//
		// Доти в базі рукава їх не було, і з цього виходило дві біди.
		// Перша: погашення вкладу приходило в готівку з тіла, якого
		// модель не тримала, — гроші зʼявлялись нізвідки. Друга:
		// колонка «Внесено» стартувала з усього капіталу, а «З
		// реінвестом» — лише з облігацій і рахунку, тож приріст між
		// ними був занижений рівно на фонди й вклади, а на портфелі,
		// де їх більшість, ставав відʼємним.
		//
		// РОЗПОДІЛЬНИЙ сертифікат лежить у `locked` і сам не росте: його
		// дохід приходить дивідендами — реальними або оціненими з
		// обіцянки, — і вони вже стоять у Coupon нижче. Подорожчання
		// ціни застосунок не моделює: його ніхто не обіцяв.
		//
		// Накопичувальний у `locked` лежати не може, і саме тут це доти
		// й ламалось. Дивідендів у нього немає за природою, замкнене не
		// росте за побудовою, — тож фонд, який обіцяє 25% річних, у
		// проєкції не додавав анічого. Він іде окремим кошиком (Accum),
		// росте власною ставкою й повертає гроші в дату закриття.
		nom := float64(in.NominalByCur[cur])/100 +
			in.DepositBodyByCur[cur] + in.FundValueByCur[cur]
		accum := in.AccumByCur[cur]
		contrib := contribTotal * share[cur]
		if cash == 0 && nom == 0 && contrib == 0 && len(accum) == 0 {
			continue // валюти немає і не планується
		}
		rate, ok := in.YieldByCur[cur]
		if !ok {
			rate = in.AvgRateByCur[cur] // паперів цієї валюти ще немає
		}
		if rate > 40 {
			rate = 40 // стеля, щоб компаунд не вибухав
		}
		// Сьогоднішня ставка — факт: за нею можна купити просто зараз.
		// Припущенням є те, куди вона прийде, тож розкид сценаріїв
		// вішаємо на довгострокову ставку, а не на сьогоднішню.
		terminal := rate
		if cur == money.UAH {
			terminal = f.terminalUAH
		}
		if terminal += ratePP; terminal < 0 {
			terminal = 0
		}
		if terminal > 40 {
			terminal = 40
		}
		rate0 := 1.0
		if cur != money.UAH {
			u, err := fx.ToUAH(money.New(100, cur), in.Rates)
			if err != nil {
				continue // курсу немає — рукав порахувати чесно не вийде
			}
			rate0 = float64(u.Amount()) / 100
		}
		sleeves = append(sleeves, domain.Sleeve{
			Currency: cur, Cash0: cash, Nominal0: nom, RatePct: rate,
			RateTerminalPct: terminal, GlideYears: f.glideYears,
			Threshold: in.ReinvestMinByCur[cur], Coupon: f.coupon[cur],
			Redeem: f.redeem[cur], ContribUAH: contrib, Rate0: rate0,
			Accum: accum,
		})
	}
	return sleeves
}

// buildProjection рахує криву капіталу, місячний план і віяло прогнозів.
func buildProjection(in projectionInput) projectionPhase {
	out := projectionPhase{TargetUAH: money.New(0, money.UAH)}
	today := in.Today
	factory := newSleeveFactory(in)
	buildSleeves := factory.build
	glideYears := factory.glideYears

	// Ширина віяла — з налаштувань, зі спадом на ті самі числа, що доти
	// стояли константами. Доти «песимістично» означало рівно те, що хтось
	// одного разу вирішив за користувача, і змінити це можна було лише
	// перезбіркою.
	rateSpreadPP, devalSpreadPP := defaultRateSpreadPP, defaultDevalSpreadPP
	if in.Settings.RateSpreadPP != nil && *in.Settings.RateSpreadPP >= 0 {
		rateSpreadPP = *in.Settings.RateSpreadPP
	}
	if in.Settings.DevalSpreadPP != nil && *in.Settings.DevalSpreadPP >= 0 {
		devalSpreadPP = *in.Settings.DevalSpreadPP
	}

	if sl := buildSleeves(0, 0); len(sl) > 0 {
		var w, wr float64
		for _, s := range sl {
			base := (s.Cash0 + s.Nominal0) * s.Rate0
			w += base
			wr += base * s.RatePct
		}
		if w > 0 {
			out.CapRatePct = round2(wr / w)
		}
	}

	rate0USD := 0.0
	if u, err := fx.ToUAH(money.New(100, money.USD), in.Rates); err == nil {
		rate0USD = float64(u.Amount()) / 100
	}

	// --- місячний план: скільки треба вносити, щоб дійти до цілі ---
	//
	// Раніше це було ручне число в налаштуваннях. Воно дублювало ціль і
	// дедлайн, які вже задані, і нічого не заважало їм суперечити: можна
	// було планувати 5 000/міс під ціль, для якої треба 20 000.
	//
	// Тепер план — це відповідь на «скільки треба». Наслідок, про який
	// варто пам'ятати: реалістичний сценарій тепер за побудовою впирається
	// рівно в ціль, тож питання «чи досяжна ціль» переїхало в рядок «За
	// фактом» — порівняння потрібного темпу з тим, що є насправді.
	deadlineMonths := 0
	if domain.Date(in.Settings.GoalDate).Valid() {
		gd := domain.Date(in.Settings.GoalDate)
		deadlineMonths = (gd.Year()-today.Year())*12 + int(gd.Month()) - int(today.Month())
	}
	// Ціль читаємо з нового одиночного поля, зі спадом на старі три — щоб
	// профілі, які ще не пройшли міграцію 0008, не лишились без цілі.
	goalAmount := 0.0
	for _, c := range []*float64{in.Settings.GoalAmountUAH, in.Settings.GoalOptimisticUAH,
		in.Settings.GoalRealisticUAH, in.Settings.GoalPessimisticUAH} {
		if c != nil && *c > 0 {
			goalAmount = *c
			break
		}
	}
	if goalAmount > 0 && deadlineMonths > 0 {
		// Рукави тут потрібні лише щоб задати ПРОПОРЦІЇ між валютами;
		// саму суму підбирає бісекція, тож стартове число довільне.
		out.ContribM = round2(domain.RequiredMonthlySleeves(
			buildSleeves(1, 0), in.Deval, goalAmount, deadlineMonths))
		out.TargetUAH = money.New(int64(math.Round(out.ContribM*100)), money.UAH)
	}

	// «Що як» — від ФАКТИЧНОГО темпу, коли він відомий: людина стоїть
	// там, де стоїть, і «×2 від плану, якого вона не тягне» — марна
	// відповідь. Розкид ставки й знецінення береться той самий, що задає
	// ширину віяла, інакше «песимістично» в одній картці й «гірший ринок»
	// у сусідній означали б різне.
	contribBase, baseFrom := out.ContribM, "plan"
	if in.ActualMonthly > 0 {
		contribBase, baseFrom = in.ActualMonthly, "actual"
	}
	out.Sensitivity = buildSensitivity(sensitivityInput{
		Factory: factory, Deval: in.Deval, Goal: goalAmount, Deadline: deadlineMonths,
		ContribBase: contribBase, BaseFrom: baseFrom,
		RateSpreadPP: rateSpreadPP, DevalSpreadPP: devalSpreadPP, Today: today,
	})

	// Достатній дохід — з налаштувань, зі спадом на місячні витрати.
	// Спад найчастіший, але не єдиний розумний, тож у документ іде ще й
	// звідки взято: читач має право знати, з чим саме порівнюють.
	incomeTarget, targetFrom := 0.0, "expenses"
	if in.Settings.MonthlyExpensesUAH != nil {
		incomeTarget = *in.Settings.MonthlyExpensesUAH
	}
	if in.Settings.IncomeTargetUAH != nil && *in.Settings.IncomeTargetUAH > 0 {
		incomeTarget, targetFrom = *in.Settings.IncomeTargetUAH, "setting"
	}
	out.Independence = buildIndependence(independenceInput{
		Factory: factory, Deval: in.Deval,
		ContribPlan: out.ContribM, ContribActual: in.ActualMonthly,
		TargetUAH: incomeTarget, TargetFrom: targetFrom,
		IncomeNowUAH: in.IncomeMonthlyNow, Today: today,
	})

	// Скільки знімати — окреме питання від того, який дохід достатній:
	// жити можна й скромніше за ціль. Спад той самий, на витрати.
	withdraw, withdrawFrom := 0.0, "expenses"
	if in.Settings.MonthlyExpensesUAH != nil {
		withdraw = *in.Settings.MonthlyExpensesUAH
	}
	if in.Settings.WithdrawMonthlyUAH != nil && *in.Settings.WithdrawMonthlyUAH > 0 {
		withdraw, withdrawFrom = *in.Settings.WithdrawMonthlyUAH, "setting"
	}
	out.Drawdown = buildDrawdown(drawdownInput{
		Factory: factory, Deval: in.Deval,
		WithdrawUAH: withdraw, WithdrawFrom: withdrawFrom,
		IncomeNowUAH: in.IncomeMonthlyNow, Today: today,
	})

	// Старт проєкції — капітал БЕЗ резерву. Решта входить уся, разом із
	// сертифікатами й вкладами: інакше крива починалась би нижче за
	// плитку «Капітал» на ту саму суму (доти так і було — вклади сюди не
	// потрапляли зовсім).
	//
	// Резерв — свідомий виняток, і саме тому він тут віднімається явно, а
	// не «просто не додається»: він не інвестується й не компаундиться,
	// тож включити його означало б показати приріст на гроші, які лежать
	// без руху. Через це проєкція стартує нижче за плитку рівно на суму
	// матраца — і це правильно, а не розбіжність, яку треба ховати.
	p0 := in.Capital.TotalUAH() - in.Capital.ReserveUAH
	out.Rows = make([]state.ProjectionRow, 0, 4)
	for _, y := range []int{1, 3, 5, 10} {
		m := y * 12
		res := domain.ProjectSleeves(buildSleeves(out.ContribM, 0), in.Deval, m)
		row := state.ProjectionRow{
			Years: y,
			// Обидві колонки — у сьогоднішніх гривнях, інакше таблиця
			// віднімала б номінальні гроші від реальних і на коротких
			// горизонтах показувала б від'ємний приріст.
			Contributed:   round2(domain.RealContributed(p0, out.ContribM, in.Deval, m)),
			WithReinvest:  round2(res.TodayUAH),
			IncomeMonthly: round2(res.IncomeMonthlyTodayUAH),
		}
		if in.ActualMonthly > 0 {
			act := domain.ProjectSleeves(buildSleeves(in.ActualMonthly, 0), in.Deval, m)
			row.WithReinvestActual = round2(act.TodayUAH)
			row.IncomeMonthlyActual = round2(act.IncomeMonthlyTodayUAH)
		}
		out.Rows = append(out.Rows, row)
	}

	// --- віяло прогнозів на дедлайн ---
	//
	// Ціль — ОДНА сума-орієнтир. Дата в усіх рядків одна (дедлайн), тож
	// суми між собою порівнянні.
	//
	// Три сценарії описують РИНОК і відрізняються лише ринковими
	// допущеннями — ставкою й знеціненням; внесок в усіх трьох плановий.
	// Окремий рядок «За фактом» описує ТЕБЕ: плановий внесок замінено на
	// фактичний темп поповнень за ринкових допущень реалістичного
	// сценарію. Так різниця між ним і «Реалістично» — це рівно твоя
	// поведінка, без домішки ринку.
	//
	// Раніше фактичний темп підмішувався в межі внеску песимістичного й
	// оптимістичного сценаріїв, і два різні джерела невизначеності —
	// ринок і дисципліна — злипались в одне число.
	if deadlineMonths <= 0 {
		return out
	}
	defs := []scenarioDef{
		{"optimistic", "Оптимістично", out.ContribM, rateSpreadPP, math.Max(0, in.Deval-devalSpreadPP)},
		{"realistic", "Реалістично", out.ContribM, 0, in.Deval},
		{"pessimistic", "Песимістично", out.ContribM, -rateSpreadPP, in.Deval + devalSpreadPP},
	}
	if in.ActualMonthly > 0 {
		defs = append(defs, scenarioDef{"actual", "За фактом", in.ActualMonthly, 0, in.Deval})
	}
	f := &state.Forecast{
		Date:        string(domain.NewDate(today.Time().AddDate(0, deadlineMonths, 0))),
		Months:      deadlineMonths,
		GoalAmount:  goalAmount,
		ContribPlan: round2(out.ContribM),
		Rate0USD:    round2(rate0USD),
		GlideYears:  glideYears,
	}
	for _, d := range defs {
		sl := buildSleeves(d.contrib, d.ratePP)
		res := domain.ProjectSleeves(sl, d.deval, deadlineMonths)
		row := state.ForecastRow{Key: d.key, Label: d.label,
			Amount: round2(res.TodayUAH), AmountNominal: round2(res.NominalUAH),
			ContribMonthly: round2(d.contrib), DevaluationPct: round2(d.deval)}
		// Скільки треба вносити САМЕ ЗА ЦИХ допущень. За гіршого ринку
		// той самий фінансовий результат коштує більшого внеску — це і
		// показує, наскільки ціль посильна, а не лише чи вона досяжна.
		if goalAmount > 0 && d.key != "actual" {
			row.RequiredMonthly = round2(domain.RequiredMonthlySleeves(
				buildSleeves(1, d.ratePP), d.deval, goalAmount, deadlineMonths))
		}
		// Ставку показуємо ту, під яку реально росте основна валюта
		// портфеля, а не середню по лікарні.
		for _, s := range sl {
			if s.Currency == money.UAH {
				row.RatePct = round2(s.RatePct)
				row.RateTerminalPct = round2(s.RateTerminalPct)
			}
			row.ByCurrency = append(row.ByCurrency, state.SleeveRow{
				Currency: s.Currency, RatePct: round2(s.RatePct),
				RateTerminalPct: round2(s.RateTerminalPct),
				ContribMonthly:  round2(s.ContribUAH),
				Amount:          round2(res.ByCurrency[s.Currency]),
			})
		}
		if goalAmount > 0 {
			row.GoalPct = math.Round(res.TodayUAH/goalAmount*1000) / 10
			hit := domain.MonthsToReachSleeves(sl, d.deval, goalAmount, goalHorizonMonths)
			row.GoalMonths = hit
			if hit > 0 {
				row.GoalDate = string(domain.NewDate(today.Time().AddDate(0, hit, 0)))
			}
		}
		f.Rows = append(f.Rows, row)
	}
	f.Curve = buildForecastCurve(factory, defs, in.Deval, goalAmount, deadlineMonths)
	out.Forecast = f
	return out
}

// buildForecastCurve — помісячна траєкторія кожного сценарію.
//
// Одна симуляція на сценарій, тобто чотири разом: ProjectSleevesSeries
// проходить місяці один раз і віддає зрізи, а не перезапускається на
// кожну точку.
//
// Крок підбирається так, щоб точок було близько дюжини. Помісячна крива
// на десятирічному горизонті — це 120 точок на серію, тобто півтисячі
// чисел у документі заради лінії, у якій сусідні місяці візуально не
// відрізняються.
func buildForecastCurve(factory sleeveFactory, defs []scenarioDef,
	deval, goal float64, months int) *state.ForecastCurve {
	if months <= 0 {
		return nil
	}
	step := months / 12
	if step < 1 {
		step = 1
	}
	// Ключ сценарію → зрізи. Місяці в усіх однакові, бо крок і горизонт
	// одні; збирати їх у точки можна за індексом.
	series := map[string][]domain.SeriesPoint{}
	for _, d := range defs {
		series[d.key] = domain.ProjectSleevesSeries(
			factory.build(d.contrib, d.ratePP), d.deval, months, step)
	}
	plan := series["realistic"]
	if len(plan) == 0 {
		return nil
	}
	at := func(key string, i int) float64 {
		s := series[key]
		if i >= len(s) {
			return 0
		}
		return round2(s[i].UAH)
	}
	out := &state.ForecastCurve{StepMonths: step, GoalUAH: goal}
	for i, p := range plan {
		out.Points = append(out.Points, state.ForecastCurvePoint{
			Month: p.Month, Plan: round2(p.UAH),
			Optimistic:  at("optimistic", i),
			Pessimistic: at("pessimistic", i),
			Actual:      at("actual", i),
		})
	}
	return out
}
