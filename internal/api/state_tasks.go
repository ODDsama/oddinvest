// Черга «що робити»: усе, що чекає рішення, одним впорядкованим списком.
//
// ЧОМУ ЦЕ ОКРЕМА ФАЗА, А НЕ ЧАСТИНА buildState
//
// Задачі потребують ПОРАД реінвесту, а поради рахуються за готовим
// документом (reinvestSuggestions приймає *state.Doc). Покласти їх усередину
// buildStateWith означало б замкнути це в кільце — і, що дорожче, ганяти
// SearchBonds на п'ять тисяч паперів на КОЖНОМУ POST /api/whatif, де черга
// не потрібна взагалі.
//
// Тому черга — обгортка над готовим документом, і кличуть її рівно два
// шляхи: GET /api/summary і публікація в MQTT. Решта (whatif, план,
// cashflow, xirr) лишається на голому buildState.
//
// ЧОМУ ПРОЗА ТУТ, А НЕ В UI
//
// Споживачів двоє — веб і Home Assistant, — і формулювання мусить бути
// одне. Прецедент поруч: suggestion.Reason теж складається тут, українською
// й з числами всередині.
//
// Адрес тут немає: Action — семантичне дієслово, а куди воно веде, вирішує
// той, хто показує. Див. коментар до state.Task.

package api

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"

	money "github.com/Rhymond/go-money"
)

// Ступені терміновості. Це ЧАС, а не важливість — див. state.Task.Sev.
const (
	sevNow   = "now"
	sevSoon  = "soon"
	sevWatch = "watch"
)

// Семантичні дії — СТАЛІ ТОКЕНИ, а не підписи кнопок.
//
// Спокуса покласти сюди готове «Записати покупку» велика: рядок одразу
// показуваний, і перекладати нічого не треба. Але тоді веб маршрутизував
// би ПО ПРОЗІ — і перше ж переформулювання підпису тихо ламало б
// посилання, не зачепивши жодного тесту. Токен переживає редактуру.
//
// Підпис кнопки лишається за тим, хто показує: у вебі він поруч із
// маршрутом (одна мапа замість двох), у Home Assistant кнопки немає
// взагалі — там задачу читають, а не натискають.
const (
	actRecordBuy     = "record-buy"
	actTopUpDeposit  = "top-up-deposit"
	actFillReserve   = "fill-reserve"
	actRecordNPF     = "record-npf"
	actConfirmPay    = "confirm-payment"
	actRecordReceipt = "record-receipt"
	actReviewLimits  = "review-limits"
	actSeeSuggest    = "see-suggestions"
	actReviewDeposit = "review-deposit"
	actHowToFund     = "how-to-fund"
	actConfirmRoute  = "confirm-route"
	actFillGoal      = "fill-goal"
	// actPayCard веде до форми звірки картки: у неї два числа з додатка
	// банку, і саме вони роблять пороги правдою. actPayDebt — до журналу
	// боргу.
	actPayCard = "pay-card"
	actPayDebt = "pay-debt"
)

// taskSoonDays — вікно «скоро». Одне число на всі дати навмисно: вклад, що
// гаситься через три тижні, і вікно фонду, що закривається через три тижні,
// вимагають рішення однаково скоро.
const taskSoonDays = 30

// taskPastDays — як глибоко назад шукаємо невідмічені виплати.
//
// Не «від початку часів»: питання задачі — «гроші мали надійти, підтверди»,
// а виплата піврічної давнини без відмітки це вже не задача, а прогалина в
// журналі, і черга задач — не те місце, де її виправляють. Обмежене вікно
// заразом тримає ціну фази постійною: історія росте, робота — ні.
const taskPastDays = 90

// nbuStaleDays — після скількох днів мовчання довідник вважаємо несвіжим.
// Те саме число, що показував веб, коли ця перевірка жила в ньому.
const nbuStaleDays = 3

// uah — гроші українською для ПРОЗИ, а не для таблиці.
//
// Своя, бо x/text у залежностях немає, а тягнути його заради одного
// формату — обмін не на користь: тут потрібен рівно один вигляд, «1 234,56 ₴»
// з нерозривними пробілами між групами.
func uah(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	whole := int64(v)
	cents := int64(math.Round((v - float64(whole)) * 100))
	if cents == 100 { // округлення вгору перекинуло копійки в гривню
		whole++
		cents = 0
	}
	digits := fmt.Sprintf("%d", whole)
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteString(" ")
		}
		b.WriteRune(r)
	}
	sign := ""
	if neg {
		sign = "−"
	}
	return fmt.Sprintf("%s%s,%02d ₴", sign, b.String(), cents)
}

// cur — те саме в довільній валюті, символом.
func cur(v float64, code string) string {
	sym := map[string]string{"UAH": "₴", "USD": "$", "EUR": "€"}[code]
	if sym == "" {
		sym = code
	}
	return strings.Replace(uah(v), "₴", sym, 1)
}

var monthsGen = [...]string{"січня", "лютого", "березня", "квітня", "травня",
	"червня", "липня", "серпня", "вересня", "жовтня", "листопада", "грудня"}

// dayMonth — «10 вересня». Рік свідомо опущений: усе, про що говорить
// черга, лежить у межах кількох місяців, і рік у кожному рядку був би шумом.
func dayMonth(d domain.Date) string {
	t, err := time.Parse("2006-01-02", string(d))
	if err != nil {
		return string(d)
	}
	return fmt.Sprintf("%d %s", t.Day(), monthsGen[int(t.Month())-1])
}

func daysBetween(from, to domain.Date) int {
	a, err1 := time.Parse("2006-01-02", string(from))
	b, err2 := time.Parse("2006-01-02", string(to))
	if err1 != nil || err2 != nil {
		return 0
	}
	return int(b.Sub(a).Hours() / 24)
}

// buildStateTasked — документ стану разом із чергою задач.
//
// Помилка збірки порад чергу ГАСИТЬ, а не документ: стан портфеля цінний
// сам по собі, і віддати порожню чергу замість п'ятисотки — єдина розумна
// поведінка, коли впав саме помічник.
func (s *Server) buildStateTasked(ctx context.Context, now time.Time) (*state.Doc, error) {
	doc, err := s.buildState(ctx, now)
	if err != nil {
		return nil, err
	}
	sug, serr := s.reinvestSuggestions(ctx, now, doc)
	if serr != nil {
		s.log.Warn("поради для черги задач не зібрались", "err", serr)
		sug = nil
	}
	src, serr := s.loadSources(ctx, domain.NewDate(now))
	if serr != nil {
		s.log.Warn("джерела для черги задач не зібрались", "err", serr)
		return doc, nil
	}
	doc.Tasks = buildTasks(doc, sug, src, domain.NewDate(now))
	return doc, nil
}

// buildTasks — сама черга. Чиста функція над готовими даними: жодного
// запиту, тож її поведінку читають згори вниз.
func buildTasks(doc *state.Doc, sug []suggestion, src *sources, today domain.Date) []state.Task {
	var out []state.Task
	add := func(t state.Task) { out = append(out, t) }

	// ---------- порожній портфель ----------
	// Витісняє всі інші: доки портфеля немає, решта черги або порожня за
	// побудовою, або радить те, чого ще не існує.
	if !hasPortfolio(doc) {
		return []state.Task{{
			ID: "start", Sev: sevNow, Rank: 0, Kind: "bond",
			Title:  "Почни з першої покупки",
			Why:    "Додай папір — і застосунок почне вести драбину, календар і проєкції.",
			Action: actRecordBuy,
		}}
	}

	// ---------- картка: платіж до розрахункової дати ----------
	//
	// РАНГ 1, вище за все, і це єдина задача застосунку, чия ціна лежить
	// ПОЗА грошима. Пропущений платіж коштує штрафу, підвищеної ставки на
	// весь борг і запису в кредитній історії, який не виправляється
	// доплатою. Решта черги — про вигоду, ця — про шкоду.
	//
	// Два пороги в одному рядку, бо помилки дві й вони різні за ціною
	// (довід — у шапці domain/debt.go).
	for _, c := range cardTasks(src, doc, today) {
		add(c)
	}

	// ---------- резерв ----------
	// Першим не тому, що найважливіший, а тому, що на це вже спирається сам
	// помічник: гроші, які підуть у резерв, не мають брати участі в покупці.
	if r := doc.Reserve; r != nil && r.FillNowUAH > 0 {
		// Чому саме стільки — стеля чи сам розрив. Мовчати про це не можна:
		// сума без причини читається як вимога, а не як стеля, яку людина
		// сама собі поставила.
		why := fmt.Sprintf("Стеля, яку ти сам поставив: до цілі ще %s, "+
			"решта грошей лишається на папери.", uah(r.GapUAH))
		if r.FillNowUAH >= r.GapUAH {
			why = fmt.Sprintf("Це все, чого бракує до цілі — %s, тобто %d %s витрат.",
				uah(r.TargetUAH), int(r.TargetMonths),
				plural(int(r.TargetMonths), "місяць", "місяці", "місяців"))
		}
		// У ЯКІЙ ФОРМІ — те, чого задачі бракувало. Стеля каже, СКІЛЬКИ
		// відкласти; драбина доступу додає, у що саме, і порядок тут не
		// косметичний: покласти правильну суму в неправильній формі гірше,
		// ніж не покласти нічого, бо гроші стають недосяжними рівно тоді,
		// коли подушка вперше знадобиться.
		//
		// Доки голова не добрана — тільки готівка, хай би скільки було
		// грошей. Далі — сходинка на NextRungMonths місяців; те число вже
		// враховує і стелю строку, і те, що набирати треба з ДАЛЬНЬОГО
		// кінця (state/derive.go).
		//
		// Меню строків банку застосунок не знає й не вдає, що знає: 5- чи
		// 7-місячних вкладів більшість не пропонує. Тому названо і потребу,
		// і наслідок округлення.
		switch {
		case r.LiquidTargetUAH > 0 && r.LiquidUAH+0.005 < r.LiquidTargetUAH:
			why += fmt.Sprintf(" Клади ГОТІВКОЮ: доступно миттєво %s із потрібних %s, "+
				"і вклад на цьому кроці погіршить доступ, а не покращить.",
				uah(r.LiquidUAH), uah(r.LiquidTargetUAH))
		case r.NextRungMonths > 0:
			why += fmt.Sprintf(" Голова добрана, тож це вже сходинка драбини: "+
				"потрібно ≈%d %s. Якщо банк такого строку не дає, бери довший — "+
				"тоді ближчий місяць лишиться на готівці, а драбина зсунеться.",
				int(r.NextRungMonths),
				plural(int(r.NextRungMonths), "місяць", "місяці", "місяців"))
		}
		add(state.Task{
			ID: "reserve-fill", Sev: sevNow, Rank: 10, Kind: "reserve",
			Title:     fmt.Sprintf("Спершу поповнити резерв — %s", uah(r.FillNowUAH)),
			Why:       why,
			Action:    actFillReserve,
			AmountUAH: round2(r.FillNowUAH),
		})
	}

	// ---------- сходинка, що гаситься ----------
	//
	// Драбина осідає САМА, якщо рунгу не перевкласти: через її строк
	// покриття зникає, і помітити це можна лише тоді, коли гроші вже
	// знадобились. Дату застосунок знає точно, тож вигадувати тут нічого.
	//
	// Окрема задача, а не рядок у поповненні: та про НОВІ гроші, ця — про
	// гроші, які вже в подушці й от-от випадуть із неї. Злиття означало б,
	// що задача зникає в місяць, коли поповнювати нема з чого.
	// Вікно — місяць, як і в решти задач із датою: раніше про це нема чого
	// думати, пізніше вже пізно домовлятися з банком.
	if doc.Reserve != nil {
		soon := today.AddMonths(1)
		for _, dep := range src.termDeposits {
			if !dep.IsReserve || !dep.Active(today) || dep.MaturityDate.After(soon) {
				continue
			}
			// Сума — в НАТИВНІЙ валюті вкладу, а не в гривні: перевкладати
			// доведеться рівно ці гроші, і гривневий еквівалент тут був би
			// числом, якого в банку не назвуть.
			amount := money.New(dep.BalanceAt(dep.MaturityDate), dep.Currency)
			add(state.Task{
				ID:  "reserve-rung-" + dep.SyntheticISIN(),
				Sev: sevSoon, Rank: 11, Kind: "reserve",
				Title: "Перевкласти сходинку подушки — " + amount.Display(),
				Why: fmt.Sprintf("Гаситься %s. Без перевкладення драбина осяде: "+
					"саме цей місяць подушки лишиться без покриття, а гроші "+
					"лежатимуть під нуль.", dep.MaturityDate),
				When:   string(dep.MaturityDate),
				Action: actFillReserve,
			})
		}
	}

	// ---------- цілі накопичення ----------
	//
	// ПІСЛЯ подушки, і саме тому рангом нижче за обидві її задачі: аварія не
	// має дати й може статись завтра, а річ, на яку збирають, дату має. Той
	// самий порядок, що й у розкладці надходження.
	//
	// ОДНА задача на ціль, а не дві. Спокуса завести окрему «відстаєш»
	// велика, але це не інша ДІЯ: робити треба те саме — відкласти, — а
	// відставання є причиною, чому саме стільки. Дві задачі на одну дію
	// заповнили б чергу дублями рівно тоді, коли цілей кілька.
	for i := range doc.Goals {
		g := doc.Goals[i]
		if g.DoneDate != "" || g.FillNowUAH <= 0 {
			continue
		}
		// Розрив називається РІВНО ОДИН раз, і в цілі з дедлайном — разом із
		// темпом, який із нього виводиться. Друга згадка тієї самої суми
		// через рядок читалась би як два різні числа, що випадково збіглись.
		why := fmt.Sprintf("Стеля, яку ти сам поставив: до цілі ще %s, "+
			"решта грошей лишається на папери.", uah(g.GapUAH))
		if g.DueDate != "" {
			why = fmt.Sprintf("До %s лишилось %s, тобто ≈%s на місяць. "+
				"Береться це зі стелі, яку ти сам поставив: решта грошей "+
				"лишається на папери.",
				g.DueDate, uah(g.GapUAH), uah(g.RequiredUAH))
		}
		// Найважливіший рядок задачі, коли він є: стеля фізично не дає
		// стільки, скільки потрібно, і жодна дисципліна цього не виправить.
		// Мовчати про це означало б щомісяця радити суму, яка до дедлайну
		// не веде, і жодного разу не сказати чому.
		if g.ShortMonthUAH > 0 {
			why += fmt.Sprintf(" Сама ця сума МЕНША за потрібний темп на %s: "+
				"стільки застосунок відрізати не може за твоєю ж стелею. "+
				"Підніми частку або зсунь дату.", uah(g.ShortMonthUAH))
		}
		add(state.Task{
			ID: fmt.Sprintf("goal-fill-%d", g.ID), Sev: sevNow, Rank: 12, Kind: "goal",
			Title:     fmt.Sprintf("Відкласти на «%s» — %s", g.Name, uah(g.FillNowUAH)),
			Why:       why,
			Action:    actFillGoal,
			AmountUAH: round2(g.FillNowUAH),
		})
	}

	// ---------- що купити ----------
	// Стрічка вже впорядкована reinvestSuggestions — беремо перше, що по
	// кишені, і перше взагалі. Власного сортування тут немає навмисно: два
	// порядки на одні поради означали б, що черга радить одне, а «Що
	// купити» інше.
	var bestCan, bestAny *suggestion
	for i := range sug {
		if bestAny == nil {
			bestAny = &sug[i]
		}
		if bestCan == nil && sug[i].CanBuy {
			bestCan = &sug[i]
		}
	}
	if bestCan != nil {
		add(buyTask(bestCan, bestAny))
	}

	// ---------- пенсійний внесок ----------
	for _, n := range doc.NPF {
		if !n.ContribDue {
			continue
		}
		why := "Внесок цього місяця не записаний."
		if n.ContribDay > 0 {
			why = fmt.Sprintf("Звичайний день внеску — %d числа.", n.ContribDay)
		}
		add(state.Task{
			ID: "npf-due:" + n.Name, Sev: sevNow, Rank: 30, Kind: "npf",
			Title:  fmt.Sprintf("Внеску в %s за цей місяць ще немає", n.Name),
			Why:    why,
			Action: actRecordNPF,
		})
	}

	// ---------- ціна сертифіката застаріла ----------
	//
	// Самогасне нагадування, як внесок у НПФ: зникає само, щойн зʼявиться
	// свіжа позначка. Потрібне через те, що вартість позиції й ЇЇ ДОХІДНІСТЬ
	// рахуються за останньою відомою ціною — а в накопичувального фонду
	// вона рухається лише руками, бо операцій між купівлями немає взагалі.
	//
	// Рядок на фонд, а не один спільний: фонди застарівають нарізно, і
	// «десь щось застаріло» не каже, куди йти. Порядок — за назвою, бо
	// doc.Funds уже відсортовані в Holdings, і на це спирається
	// TestBuildStateIsDeterministic.
	for _, f := range doc.Funds {
		if !f.PriceStale {
			continue
		}
		days := daysBetween(domain.Date(f.LastPriceDate), today)
		add(state.Task{
			ID: "fund-price-stale:" + f.Fund, Sev: sevWatch, Rank: 25, Kind: "fund",
			Title: fmt.Sprintf("%s: ціну не позначали %d %s", f.Fund, days,
				plural(days, "день", "дні", "днів")),
			Why: "Вартість позиції й її дохідність рахуються за цією ціною. " +
				"Доки її не оновити, зростання фонду лишається невидимим.",
			When: dayMonth(domain.Date(f.LastPriceDate)),
		})
	}

	// ---------- виплати, які вже мали надійти ----------
	if t, ok := unconfirmedTask(src, today); ok {
		add(t)
	}

	// ---------- гроші, що надходять сьогодні ----------
	// Після боргу відміток навмисно: аргумент при arrivedTodayTask.
	if t, ok := arrivedTodayTask(src, today); ok {
		add(t)
	}

	// ---------- надходження плану ----------
	if t, ok := receiptTask(src, today); ok {
		add(t)
	}

	// ---------- вклад гаситься ----------
	if t, ok := maturingDepositTask(src, today); ok {
		add(t)
	}

	// ---------- вікно купівлі фонду ----------
	if t, ok := fundWindowTask(src, today); ok {
		add(t)
	}

	// ---------- перевищені ліміти ----------
	if over := overLimits(doc); len(over) > 0 {
		add(state.Task{
			ID: "conc", Sev: sevWatch, Rank: 10,
			Title: fmt.Sprintf("%d %s", len(over), plural(len(over),
				"ліміт перевищено", "ліміти перевищено", "лімітів перевищено")),
			// Спостереження, а не заборона — рівно як каже коментар до
			// ConcentrationRow: ліміт може бути порушений із причин, яких
			// застосунок не знає, і рішення лишається за людиною.
			Why: fmt.Sprintf("Найбільше — %s. Це спостереження, а не заборона: "+
				"поради воно не ховає.", over[0]),
			Action: actReviewLimits,
		})
	}

	// ---------- довідник НБУ ----------
	if d := nbuStale(doc, today); d >= nbuStaleDays {
		add(state.Task{
			ID: "nbu", Sev: sevWatch, Rank: 20,
			Title: fmt.Sprintf("Довідник НБУ не оновлювався %d %s", d,
				plural(d, "день", "дні", "днів")),
			Why: "Ставки й графіки виплат можуть бути несвіжі.",
			// Дії немає: кнопка оновлення стоїть у шапці вебу й видима з
			// будь-якої сторінки, а в Home Assistant її натискає розклад.
		})
	}

	// ---------- ще збираєш ----------
	if bestCan == nil && bestAny != nil {
		add(savingTask(doc, bestAny))
	}

	sortTasks(out)
	return out
}

// hasPortfolio — портфель це БУДЬ-ЯКИЙ із чотирьох інструментів, не лише
// ОВДП. Вільна готівка сюди НЕ входить: це не позиція, і сама по собі
// «почав» не означає.
//
// Резерв і цілі накопичення входять, хоч жодне з них не інструмент. Довід
// той самий, що робить їх частиною капіталу: це відкладені гроші, тобто
// вже РІШЕННЯ, а не залишок на рахунку. Без цього рядка людина, яка
// завела ціль і поклала на неї перші гроші, бачила б у черзі саме лише
// «Почни з першої покупки», а задача про власну ціль до неї не доходила б
// узагалі. Спіймано живцем на порожній базі.
func hasPortfolio(doc *state.Doc) bool {
	return doc.NominalUAHEq > 0 || doc.FundsUAH > 0 ||
		doc.DepositsUAH > 0 || doc.ReserveUAH > 0 || doc.GoalsUAH > 0 ||
		// Борг — теж «портфель є»: людина, у якої лише картка й
		// розстрочка, приходить сюди саме по чергу погашення, а порада
		// «почни з першої покупки» була б знущанням.
		doc.Debt != nil
}

func buyTask(best, bestAny *suggestion) state.Task {
	action, verb := actRecordBuy, "купити"
	switch best.Kind {
	case "deposit":
		action, verb = actTopUpDeposit, "поповнити"
	case "npf":
		action, verb = actRecordNPF, "внести"
	case "fund":
		// У сертифіката ручної форми немає взагалі — його заводить виписка.
		// Тому дія веде до пояснення, а не до форми, якої не існує.
		action = actHowToFund
	}
	var where []string
	for _, f := range best.Brokers {
		where = append(where, fmt.Sprintf("%s ×%d", f.Broker, f.Qty))
	}
	why := cur(moneyAmount(best.CostPerBond), best.Currency)
	if len(where) > 0 {
		why += " · " + strings.Join(where, " · ")
	}
	why += "."
	// Якщо є щось дохідніше, але ще не по кишені — кажемо прямо: «можеш
	// зараз» не має ховати «краще зачекати».
	if bestAny != nil && !bestAny.CanBuy && bestAny.RealPct > best.RealPct {
		why += fmt.Sprintf(" Дохідніше — %s, але ще не по кишені.", suggestName(bestAny))
	}
	return state.Task{
		ID: "buy-best", Sev: sevNow, Rank: 20, Kind: best.Kind,
		Title: fmt.Sprintf("Можеш %s %s", verb, suggestName(best)),
		Why:   why,
		// Слово «реальних» обов'язкове: без нього відсоток читається як
		// ставка з договору, а це інша величина. Кома, а не крапка: решта
		// чисел у документі теж українською, і одне число з крапкою посеред
		// них читається як чужий рядок.
		When:   strings.Replace(fmt.Sprintf("%.1f%% реальних", best.RealPct), ".", ",", 1),
		Action: action,
	}
}

func savingTask(doc *state.Doc, best *suggestion) state.Task {
	purse := 0.0
	for _, byCur := range doc.Brokers {
		if v := byCur[best.Currency]; v > purse {
			purse = v
		}
	}
	need := math.Max(0, moneyAmount(best.CostPerBond)-purse)
	why := fmt.Sprintf("Найкраще зараз — %s.", suggestName(best))
	// Темп беремо з ЦІЛІ місяця. Це найчесніше з того, що є в документі:
	// скільки треба вносити, щоб вийти на ціль.
	if perDay := doc.MonthTargetUAH / 30; perDay > 0 && need > 0 {
		d := int(math.Ceil(need / perDay))
		why += fmt.Sprintf(" За твоїм темпом це ≈ %d %s.", d,
			plural(d, "день", "дні", "днів"))
	}
	return state.Task{
		ID: "saving", Sev: sevWatch, Rank: 30, Kind: best.Kind,
		Title:     fmt.Sprintf("Купувати ще рано — бракує %s", cur(need, best.Currency)),
		Why:       why,
		Action:    actSeeSuggest,
		AmountUAH: round2(need),
	}
}

// arrivedTodayTask — гроші, які надходять САМЕ СЬОГОДНІ.
//
// # ЧОМУ ЦЕ ОКРЕМА ЗАДАЧА, А НЕ ЧАСТИНА pay-confirm
//
// Та свідомо пропускає сьогоднішній день (`it.Date >= today` — continue), і
// правильно робить: у неї питання про МИНУЛЕ, про борг відміток, що
// назбирався. Тут питання інше й свіже — «гроші приходять зараз, і для них
// уже є маршрут». Злити їх в одну задачу означало б поставити під одну
// кнопку «звірся з випискою за три місяці» і «розклади те, що прийшло
// вранці».
//
// Порядок між ними теж не випадковий: борг відміток (ранг 40) іде першим,
// бо тих грошей помічник не бачить уже давно, а сьогоднішні (45) нікуди не
// дінуться до вечора.
//
// # ЧОМУ ЗАДАЧА НЕ НАЗИВАЄ ПРИЗНАЧЕННЯ
//
// Назвати, куди саме підуть ці гроші, могла б лише збірка маршруту — а вона
// коштує другого loadSources і повного рейтингу на КОЖЕН /api/summary, тобто
// й на кожну публікацію в MQTT. Задача натомість каже суму й веде на
// сторінку, де призначення вже пораховане. Один клік дешевший за постійний
// прохід по пʼяти тисячах паперів.
//
// Оцінені дивіденди фондів сюди не входять із тієї самої причини, що й у
// сусідню задачу: позначати оцінку нема чого, її справжній запис — операція
// фонду з виписки.
func arrivedTodayTask(src *sources, today domain.Date) (state.Task, bool) {
	cf, err := domain.FuturePayments(src.pays, src.lots, src.sales, today)
	if err != nil {
		return state.Task{}, false
	}
	cf = append(cf, domain.DepositCashflows(src.termDeposits, today)...)
	n := 0
	sum := map[string]int64{}
	for _, it := range cf {
		if it.Date != today {
			continue
		}
		// Уже позначене — гроші вже на рахунку, і помічник їх бачить.
		if src.statuses[it.ISIN+"|"+string(it.Date)] != "" {
			continue
		}
		n++
		sum[it.Amount.Currency().Code] += it.Amount.Amount()
	}
	if n == 0 {
		return state.Task{}, false
	}
	// Суми по валютах НЕ зводяться в гривню: для цього потрібні курси, яких
	// у цій функції немає, а тягнути їх сюди заради заголовка означало б
	// завести в чергу задач власну конвертацію. Валюти перелічуються, як їх
	// і отримають — окремими сумами на окремі рахунки.
	codes := make([]string, 0, len(sum))
	for c := range sum {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	parts := make([]string, 0, len(codes))
	for _, c := range codes {
		parts = append(parts, money.New(sum[c], c).Display())
	}
	return state.Task{
		ID: "route-arrived", Sev: sevNow, Rank: 45,
		Title: fmt.Sprintf("Сьогодні надходить %s", strings.Join(parts, " + ")),
		Why: "Маршрут уже знає, куди ці гроші ведуть за твоєю політикою. " +
			"Позначиш отриманими — і розкладка відкриється на цю саму суму.",
		When:   dayMonth(today),
		Action: actConfirmRoute,
	}, true
}

// unconfirmedTask — виплати, дата яких минула, а відмітки немає.
//
// Чому це взагалі задача: гроші не зараховуються самі. domain.Arrived
// вважає виплату отриманою, якщо дата в минулому АБО стоїть відмітка, — але
// для купівельної спроможності важлива саме відмітка, і поки її немає,
// помічник цих грошей не бачить.
//
// Оцінені дивіденди фондів сюди НЕ входять: у минулому оцінок не буває,
// там фактичні операції з виписки. Тому беремо лише те, що має розклад —
// купони, погашення й відсотки вкладів.
func unconfirmedTask(src *sources, today domain.Date) (state.Task, bool) {
	from := domain.Date(mustShift(today, -taskPastDays))
	cf, err := domain.FuturePayments(src.pays, src.lots, src.sales, from)
	if err != nil {
		return state.Task{}, false
	}
	cf = append(cf, domain.DepositCashflows(src.termDeposits, from)...)
	n, last := 0, domain.Date("")
	for _, it := range cf {
		if it.Date >= today {
			continue
		}
		if src.statuses[it.ISIN+"|"+string(it.Date)] != "" {
			continue
		}
		n++
		if it.Date > last {
			last = it.Date
		}
	}
	if n == 0 {
		return state.Task{}, false
	}
	return state.Task{
		ID: "pay-confirm", Sev: sevNow, Rank: 40,
		Title: fmt.Sprintf("%d %s без відмітки", n,
			plural(n, "виплата", "виплати", "виплат")),
		Why: "Дата минула, але надходження не підтверджене — доки його немає, " +
			"помічник не бачить цих грошей.",
		When:   dayMonth(last),
		Action: actConfirmPay,
	}, true
}

// receiptTask — надходження ПОТОЧНОГО місяця, яке вже мало прийти.
//
// Ревізії плану передаються порожніми свідомо: buildExpectedReceipts читає
// їх лише для МИНУЛИХ місяців (щоб не підставляти сьогоднішню зарплату в
// травневий рядок), а задача дивиться рівно на поточний.
func receiptTask(src *sources, today domain.Date) (state.Task, bool) {
	exp := buildExpectedReceipts(src.planFlows, src.planReceipts, nil, today, src.rates)
	month := string(today)[:7]
	var names []string
	due := domain.Date("")
	for _, e := range exp {
		if e.Month != month || e.DueDate > string(today) || e.Receipt != nil {
			continue
		}
		names = append(names, e.Name)
		if due == "" || domain.Date(e.DueDate) < due {
			due = domain.Date(e.DueDate)
		}
	}
	if len(names) == 0 {
		return state.Task{}, false
	}
	return state.Task{
		ID: "receipt", Sev: sevNow, Rank: 50,
		Title: "Запиши, скільки зайшло: " + strings.Join(names, ", "),
		Why: "Дата вже минула, а факт не відмічений — без нього план і факт " +
			"розходяться мовчки.",
		When:   dayMonth(due),
		Action: actRecordReceipt,
	}, true
}

func maturingDepositTask(src *sources, today domain.Date) (state.Task, bool) {
	var best *domain.Deposit
	bestDays := taskSoonDays + 1
	for i, d := range src.termDeposits {
		if d.ClosedDate != "" || d.MaturityDate == "" {
			continue
		}
		days := daysBetween(today, d.MaturityDate)
		if days < 0 || days > taskSoonDays || days >= bestDays {
			continue
		}
		best, bestDays = &src.termDeposits[i], days
	}
	if best == nil {
		return state.Task{}, false
	}
	return state.Task{
		ID: "dep-maturing", Sev: sevSoon, Rank: 10, Kind: "deposit",
		Title: fmt.Sprintf("Вклад %s гаситься", best.Bank),
		Why:   "Тіло повернеться на рахунок — варто вирішити наперед, куди воно піде.",
		When: fmt.Sprintf("%s · %d %s", dayMonth(best.MaturityDate), bestDays,
			plural(bestDays, "день", "дні", "днів")),
		Action: actReviewDeposit,
	}, true
}

// fundWindowTask — вікно підписки фонду, що закривається. Після цієї дати
// фонд перестає з'являтись у порадах: помічник його відкидає (див.
// handlers_reinvest.go), тож попередити треба ДО того, як він зникне.
func fundWindowTask(src *sources, today domain.Date) (state.Task, bool) {
	var name string
	var until domain.Date
	bestDays := taskSoonDays + 1
	// Обхід мапи дав би документ, що змінюється між викликами без причини,
	// — на це спирається TestBuildStateIsDeterministic. Тому при рівних
	// днях перемагає менше ім'я, а не той, хто випав першим.
	for _, f := range src.fundRefs {
		if f.BuyUntil == "" {
			continue
		}
		days := daysBetween(today, domain.Date(f.BuyUntil))
		if days < 0 || days > taskSoonDays {
			continue
		}
		if days > bestDays || (days == bestDays && name != "" && f.Name >= name) {
			continue
		}
		name, until, bestDays = f.Name, domain.Date(f.BuyUntil), days
	}
	if name == "" {
		return state.Task{}, false
	}
	return state.Task{
		ID: "fund-window", Sev: sevSoon, Rank: 20, Kind: "fund",
		Title: fmt.Sprintf("%s: вікно купівлі закривається", name),
		Why:   "Після цієї дати фонд перестане з'являтись у порадах.",
		When: fmt.Sprintf("%s · %d %s", dayMonth(until), bestDays,
			plural(bestDays, "день", "дні", "днів")),
		Action: actSeeSuggest,
	}, true
}

func overLimits(doc *state.Doc) []string {
	var out []string
	for _, c := range doc.Concentration {
		if c.OverUAH <= 0 {
			continue
		}
		name := c.Label
		if name == "" {
			name = c.Key
		}
		out = append(out, name)
	}
	return out
}

func nbuStale(doc *state.Doc, today domain.Date) int {
	if doc.NBURefreshedAt == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339, doc.NBURefreshedAt)
	if err != nil {
		return 0
	}
	return daysBetween(domain.Date(t.Format("2006-01-02")), today)
}

// sortTasks — Sev, далі Rank. Порядок Sev саме такий і саме списком, а не
// алфавітом: "now" < "soon" < "watch" за змістом, а не за літерами.
func sortTasks(t []state.Task) {
	order := map[string]int{sevNow: 0, sevSoon: 1, sevWatch: 2}
	sort.SliceStable(t, func(i, j int) bool {
		if order[t[i].Sev] != order[t[j].Sev] {
			return order[t[i].Sev] < order[t[j].Sev]
		}
		return t[i].Rank < t[j].Rank
	})
}

// suggestName — як задача називає пораду. ISIN для паперу, назва для решти:
// те саме, що показує список «Що купити».
func suggestName(r *suggestion) string {
	if r.Label != "" {
		return r.Label
	}
	return r.ISIN
}

func moneyAmount(m moneyJSON) float64 {
	var v float64
	_, _ = fmt.Sscanf(m.Amount, "%f", &v) //nolint:errcheck // нерозбірлива сума лишає нуль, і це та сама відповідь, що й порожня
	return v
}

func mustShift(d domain.Date, days int) string {
	t, err := time.Parse("2006-01-02", string(d))
	if err != nil {
		return string(d)
	}
	return t.AddDate(0, 0, days).Format("2006-01-02")
}

// cardTasks — задачі про пільговий цикл карток.
//
// ТРИ РІЗНІ ПИТАННЯ, а не одне з відтінками:
//
//	платіж    — до дати треба внести стільки-то, інакше почнуть нараховувати;
//	перевитрата — картка «в плюсі», але плюс уже обіцяний виписці;
//	звірка    — числа застаріли, і всі попередні відповіді стоять на них.
//
// Злиття будь-яких двох зробило б задачу, що зникає в момент, коли саме
// вона й потрібна: перевитрата найгостріша тоді, коли до дати ще далеко.
func cardTasks(src *sources, doc *state.Doc, today domain.Date) []state.Task {
	var out []state.Task
	// Числа режиму виходу приходять ГОТОВИМИ з документа: середній дохід
	// місяців до цілі вміє порахувати лише будівник (шапка state.DebtExit).
	var exit *state.DebtExit
	if doc.Debt != nil {
		exit = doc.Debt.Exit
	}
	for _, d := range src.debts {
		if !d.IsCard() || d.Closed() {
			continue
		}
		st := domain.CardState(d, src.debtMarks, src.debtOps, src.debts, today)
		cur := d.Currency

		if !st.Known {
			out = append(out, state.Task{
				ID: "card-mark-" + d.Name, Sev: sevNow, Rank: 1, Kind: "debt",
				Title: "Звірити картку «" + d.Name + "» з додатком банку",
				Why: "Поки звірки немає, застосунок не знає ні балансу, ні суми до сплати — " +
					"тобто не може сказати ні скільки внести, ні скільки ще можна витратити.",
				Action: actPayCard,
			})
			continue
		}

		if st.StatementDue > 0 && st.DaysToDue <= taskSoonDays {
			why := fmt.Sprintf(
				"До %s принести %s — і відсотків не буде взагалі. Мінімум %s: менше — "+
					"штраф і підвищена ставка на весь борг.",
				st.DueDate, debtMoney(st.BringByDue, cur),
				debtMoney(st.MinDue, cur))
			if st.InstallmentDue > 0 {
				// Частини розстрочок до цієї суми НЕ додаються: вони підуть
				// із картки до тієї ж дати, але лягають у НАСТУПНУ виписку.
				// Склавши їх разом, задача вимагала б грошей на місяць
				// раніше (спіймано вживу — див. CardStatus.BringByDue).
				why += fmt.Sprintf(" Ще %s спишеться частинами розстрочок до тієї ж дати, "+
					"але вони йдуть у наступну виписку — до цієї їх вносити не треба.",
					debtMoney(st.InstallmentDue, cur))
			}
			if st.NonGrace > 0 {
				// Готівка не має пільгового ніколи, і мовчати про це не
				// можна: людина внесе «суму до сплати» й буде впевнена, що
				// нарахувань немає.
				why += fmt.Sprintf(" %s із цього — готівка або переказ: на них "+
					"пільговий не діє, відсоток уже йде.",
					debtMoney(st.NonGrace, cur))
			}
			sev := sevSoon
			if st.DaysToDue <= 7 {
				sev = sevNow
			}
			out = append(out, state.Task{
				ID:  "card-due-" + d.Name,
				Sev: sev, Rank: 1, Kind: "debt",
				Title: fmt.Sprintf("Внести на «%s» — %s до %s",
					d.Name, debtMoney(st.BringByDue, cur), st.DueDate),
				Why:    why,
				When:   string(st.DueDate),
				Action: actPayCard,
				// Гривня — і лише вона: поле зветься AmountUAH, і покласти
				// туди долари означало б збрехати сенсору в Home Assistant,
				// який складає ці суми. Валютна картка лишається без числа,
				// а сума названа в самому заголовку.
				AmountUAH: cardAmountUAH(st.BringByDue, cur),
			})
		}

		// Перевитрата — те, про що власник і просив: «виводжу в плюс і
		// потім знову просаджую». Рядок зʼявляється рівно тоді, коли плюса
		// вже не вистачає на те, що з нього мусить піти.
		if st.Free < 0 {
			why := fmt.Sprintf("На картці %s, але %s із цього вже обіцяно виписці",
				debtMoney(st.Balance, cur),
				debtMoney(st.StatementDue, cur))
			if st.InstallmentDue > 0 {
				why += fmt.Sprintf(" і ще %s спишуть частинами розстрочок до %s",
					debtMoney(st.InstallmentDue, cur), st.DueDate)
			}
			why += ". Витратиш ці гроші — безкоштовний оборот стане боргом під ставку."
			out = append(out, state.Task{
				ID: "card-overspend-" + d.Name, Sev: sevNow, Rank: 2, Kind: "debt",
				Title: fmt.Sprintf("«%s»: бракує %s до безпечного нуля",
					d.Name, debtMoney(-st.Free, cur)),
				Why:       why,
				Action:    actPayCard,
				AmountUAH: cardAmountUAH(-st.Free, cur),
			})
		}

		// Вихід із ліміту: зʼявляється лише тоді, коли темп НЕ ВСТИГАЄ до
		// названої дати. Коли встигає — задачі немає, і це правильно:
		// черга рішень не місце для «усе гаразд».
		// План виходу СПІЛЬНИЙ на всі картки, тож задача ставиться один
		// раз — при першій із них. Інакше та сама вимога зʼявилась би в
		// черзі стільки разів, скільки карток, і читалась як кілька різних.
		if exit != nil && len(exit.Cards) > 0 && exit.Cards[0] == d.Name &&
			exit.ShortPerMonthUAH > 0 {
			why := fmt.Sprintf(
				"Щоб вивести в нуль %s до %s, треба звільняти %s на місяць — тобто "+
					"витрачати не більше %s. Зараз виходить на %s більше.",
				strings.Join(exit.Cards, " і "), exit.ExitBy, uah(exit.NeedPerMonthUAH),
				uah(exit.SpendCapUAH), uah(exit.ShortPerMonthUAH))
			if exit.ETADate != "" {
				why += " За нинішнім темпом вихід буде " + exit.ETADate + "."
			} else {
				why += " За нинішнім темпом виходу не буде взагалі: витрати зʼїдають усе, що приходить."
			}
			out = append(out, state.Task{
				ID: "card-exit-" + d.Name, Sev: sevNow, Rank: 2, Kind: "debt",
				Title: fmt.Sprintf("Не встигаєш вийти з лімітів до %s", exit.ExitBy),
				Why:   why, When: exit.ExitBy, Action: actPayCard,
				AmountUAH: exit.ShortPerMonthUAH,
			})
		}

		// Застаріла звірка лікується ПОКАЗОМ, а не блокуванням — той самий
		// підхід, що з ціною фонду (price_stale). Числа лишаються на
		// екрані, але вік названий.
		if st.MarkAgeDays > cardMarkStaleDays {
			out = append(out, state.Task{
				ID: "card-mark-stale-" + d.Name, Sev: sevWatch, Rank: 20, Kind: "debt",
				Title: fmt.Sprintf("Звірити «%s»: числам %d днів", d.Name, st.MarkAgeDays),
				Why: "Баланс кредитки рухається щодня. Пороги, «вільно» і черга погашення " +
					"стоять на цій звірці, тож місячної давнини число — це вже спогад.",
				Action: actPayCard,
			})
		}
	}
	return out
}

// cardAmountUAH — сума задачі числом, і лише в гривні (довід при
// виклику).
func cardAmountUAH(minor int64, cur string) float64 {
	if cur != money.UAH {
		return 0
	}
	return round2(float64(minor) / 100)
}

// debtMoney — сума боргу для прози задачі.
//
// Гривня йде через uah(), решта — через Display(): у гривні застосунок
// скрізь пише «18 400,00 ₴», і одне місце з «18,400.00 UAH» читалось би як
// чужий екран. Валютний борг рідкість, і для нього рідний формат money
// чесніший за підроблений під гривню.
func debtMoney(minor int64, cur string) string {
	if cur == money.UAH {
		return uah(float64(minor) / 100)
	}
	return money.New(minor, cur).Display()
}

// cardMarkStaleDays — з якого віку звірка перестає бути виміром.
//
// Два тижні, а не місяць: пільговий цикл місячний, і звірка, старша за
// півцикла, не встигає попередити про той самий цикл, про який говорить.
const cardMarkStaleDays = 14
