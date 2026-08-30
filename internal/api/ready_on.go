// «Коли вистачить»: дата, на яку рахунок покриє цю покупку.
//
// Доти помічник відповідав двійково — can_buy або ні, — і рядок, на який
// сьогодні не стає, просто опускався нижче. Питання, яке при цьому
// лишалось без відповіді, і є головним питанням реінвесту: чекати три
// тижні до купона чи взяти те, на що стане вже сьогодні. Дата й ціна
// очікування (domain.WaitCost) роблять із нього вибір із двох названих
// сторін.
//
// РАХУНКИ РОЗДІЛЬНІ. Дата рахується по парах (брокер × валюта), а не по
// валюті: гривня в mono не купить папір в inzhur, і купон, що прийде на
// чужий рахунок, цієї покупки не наблизить. Дата рядка — найраніша серед
// брокерів, і брокер названий поруч.
//
// ЩО ВХОДИТЬ У ДАТУ. Лише те, що портфель уже винен сам собі: купони й
// погашення ОВДП, відсотки й тіло вкладів. Три речі свідомо не входять, і
// в кожної своя причина.
//
//	Планові надходження (plan_flows) — зарплата це намір, який застосунок
//	не може перевірити, і дата повзла б від кожної правки плану. Питання
//	«а якщо докласти» вже має свою відповідь — план купівель і
//	POST /api/whatif.
//
//	Оцінені дивіденди фондів — вони саме оцінені (state_schedule.go рахує
//	їх зі ставки, а не із зобовʼязання, і позначає ключем fund:<назва>).
//	У календарі оцінка стоїть підписаною й читається як оцінка; дата ж —
//	одне число, у якому припущення стало б невидимим.
//
//	Виплати НПФ — гроші звідти не приходять до пенсійного віку, і
//	підмішувати їх у «коли зможу купити папір» означало б рахувати
//	покупку за гроші, яких не буде.
//
// ДРУГЕ «КОЛИ» В ЗАСТОСУНКУ — і воно лишається. savingTask
// (state_tasks.go) уже каже «за твоїм темпом це ≈ N днів», рахуючи з
// МІСЯЧНОЇ ЦІЛІ ВНЕСКІВ. Це інша міра того самого питання: там гроші, які
// ти плануєш доносити, тут — які портфель платить сам. Зводити їх в одне
// число не можна (одне спирається на намір, друге на зобовʼязання), тому
// обидва називають свою основу прямо: «за твоїм темпом» проти «з
// надходжень портфеля». Без цих слів два різні числа на двох екранах
// читались би як розбіжність.
//
// ЩО НЕ ЗМІНЮЄТЬСЯ. Порядок рядків. Ланцюг компаратора (Locked → CanBuy →
// ліміт/транзит → planScore → stale) — це політика, а дата — факт поруч
// із нею. Пустити дату в сортування означало б тихо завести нове правило
// під виглядом показу.
//
// ЦІНА ЦЬОГО ФАЙЛУ — один зайвий прохід по джерелах на /api/reinvest:
// привʼязати виплату до брокера можна лише через лоти, а state.Doc
// брокера у виплатах не несе (і не має нести — той документ іде в MQTT).
// Тому анотація живе в обробнику, а не в reinvestSuggestions: та сама
// збірка порад працює ще й усередині buildState (черга задач), і другий
// loadSources подорожчав би кожен /api/summary заради поля, якого черга
// не показує.
package api

import (
	"context"
	"fmt"
	"math"
	"sort"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// noBrokerLabel — як показується рахунок без брокера. Те саме «—», що й у
// гаманці (state_cash.go): два різні позначення одного рахунку розвели б
// баланс із датою.
const noBrokerLabel = "—"

// readyFlow — одне майбутнє надходження на конкретний рахунок.
//
// PRINCIPAL І KIND — ДЛЯ МАРШРУТУ, І ЛИШЕ ДЛЯ НЬОГО. «Коли вистачить» питає
// СКІЛЬКИ грошей буде на рахунку, і на це питання купон і погашення
// відповідають однаково: гроші є гроші. Маршрут (route.go) веде прохід
// уперед по капіталу, а там різниця принципова — купон це НОВІ гроші, а
// погашення лише перекладає власне тіло з паперу в готівку. Без цієї пари
// прохід рахував би повернення номіналу приростом капіталу й через це
// занижував би розрив до цілі саме того виду, який щойно погасився.
//
// Обидва поля тут, а не в окремій структурі поруч, бо джерело в них одне й
// те саме — розклад, — і другий збирач розкладу розійшовся б із першим.
// readyFor і annotateReady їх не читають: дата від цього не змінюється.
type readyFlow struct {
	Date   domain.Date
	Amount int64 // мінорні, у валюті рахунку
	Label  string
	// Principal — скільки з Amount є поверненням ВЛАСНОГО тіла (погашення
	// ОВДП, тіло вкладу). Решта — дохід. Kind — вид, з якого це тіло
	// повертається, у термінах ребалансу ("bonds" | "deposits").
	Principal int64
	Kind      string
	// Basis — на чому це надходження стоїть. Порожньо = ЗОБОВʼЯЗАННЯ, і
	// саме таким futureIncome лишає кожен свій рядок: інших основ вона не
	// знає й знати не мусить. Непорожнє ставить лише routeIncome нижче.
	Basis string
	// Ref — ключ, яким цю виплату позначають отриманою: ISIN паперу або
	// синтетичний "deposit:<id>" вкладу. ОКРЕМО від Label, бо в вкладу вони
	// різні — на екрані «вклад ПУМБ», у payment_status «deposit:7», — а
	// кнопка «Прийшло» мусить надіслати саме другий.
	//
	// Порожньо в оцінок: дивіденд фонду позначати нема чого, його справжній
	// запис — операція фонду, а не рядок статусу.
	Ref string
	// Why — чому в нозі стоїть менше, ніж фонд нарахував. Порожньо в усіх
	// інших надходжень: там сума і є сумою.
	//
	// Готовою прозою з бекенда, а не парою чисел на складання в браузері, —
	// з того самого доводу, що RestWhy й ReserveSkipWhy: сума, яка
	// «зменшилась сама», читається як поломка, а рахувати її вдруге в JS
	// заборонено (CLAUDE.md §5).
	Why string
}

// reinvestFlowWhy — підпис до ноги фонду, який докуповує сертифікати сам.
// Порожньо, коли нічого не докупив: тоді сума й так уся.
func reinvestFlowWhy(s domain.FundReinvestSplit, cur string) string {
	if s.Units <= 0 || s.Spent <= 0 {
		return ""
	}
	return fmt.Sprintf("фонд утримав виплату %s і докупив %d %s на %s — на рахунок іде лише решта",
		money.New(s.Gross, cur).Display(), s.Units,
		plural(int(s.Units), "сертифікат", "сертифікати", "сертифікатів"),
		money.New(s.Spent, cur).Display())
}

// Основи надходження. Порожній рядок у readyFlow.Basis означає basisOwed —
// назовні воно завжди назване словом, бо колонка «основа» з порожньою
// коміркою читалась би як «невідомо», а не як «портфель це винен».
const (
	basisOwed     = "owed"     // портфель винен сам собі: купон, погашення, відсотки
	basisEstimate = "estimate" // оцінка: дивіденд фонду, порахований зі ставки
	basisPlan     = "plan"     // намір: дохід із plan_flows
	basisMixed    = "mixed"    // у горщику зійшлись різні
)

// flowBasis — основа надходження словом. Порожнє поле означає
// зобовʼязання, і назовні воно мусить бути названим (див. коментар вище).
func flowBasis(f readyFlow) string {
	if f.Basis == "" {
		return basisOwed
	}
	return f.Basis
}

// readyEvent — те саме назовні: з чого саме склалася сума.
type readyEvent struct {
	Date   string    `json:"date"`
	Label  string    `json:"label"`
	Amount moneyJSON `json:"amount"`
}

// incomeAhead — майбутні надходження в розрізі (брокер × валюта).
type incomeAhead map[store.BrokerCur][]readyFlow

// futureIncome — що ще прийде на рахунки і на які саме.
//
// Розкладка по брокерах дзеркалить гаманець (state_builder.go): купон
// кредитує рахунок того брокера, де куплено папір, відсотки вкладу —
// рахунок його банку. Порожня назва стає «—» тим самим правилом, що й у
// cashLedger.byBroker: гроші без привʼязки — це теж місце, і мовчки
// зливати їх із чиїмось рахунком не можна.
//
// Виплати рахує domain.FuturePayments — та сама функція, якою збирається
// календар і зведення. Розділені по брокерах лоти дають розділені потоки:
// сума по всіх брокерах дорівнює загальному розкладу, бо HolderQty
// лінійна за лотами (на це є тест).
//
// arrived фільтрує те, що гаманець УЖЕ порахував балансом. Без цього
// виплата, датована сьогодні й позначена «отримано», лічилась би двічі —
// один раз у балансі, другий як майбутнє надходження.
func (s *Server) futureIncome(src *sources, today domain.Date) (incomeAhead, error) {
	arrived := domain.Arrived(src.statuses, today)
	out := incomeAhead{}
	add := func(broker, currency string, f readyFlow) {
		if broker == "" {
			broker = noBrokerLabel
		}
		k := store.BrokerCur{Broker: broker, Currency: currency}
		out[k] = append(out[k], f)
	}

	byChannel := map[string][]domain.Lot{}
	for _, l := range src.lots {
		byChannel[l.Channel] = append(byChannel[l.Channel], l)
	}
	for channel, lots := range byChannel {
		cfs, err := domain.FuturePayments(src.pays, lots, src.sales, today)
		if err != nil {
			return nil, err
		}
		for _, cf := range cfs {
			if arrived(cf.ISIN, cf.Date) {
				continue
			}
			f := readyFlow{Date: cf.Date, Amount: cf.Amount.Amount(),
				Label: cf.ISIN, Kind: "bonds", Ref: cf.ISIN}
			if cf.Type == domain.PayRedemption {
				f.Principal = cf.Amount.Amount()
			}
			add(channel, cf.Amount.Currency().Code, f)
		}
	}

	for _, dep := range src.termDeposits {
		// Назву банку в підписі видно саме тут, і це навмисне розходження зі
		// state.payLabel, де стоїть просто «вклад»: там перелік вкладів у
		// функцію не передається, а тут він і є входом.
		label := "вклад"
		if dep.Bank != "" {
			label += " " + dep.Bank
		}
		for _, cf := range domain.DepositSchedule(dep, today) {
			if arrived(cf.ISIN, cf.Date) {
				continue
			}
			f := readyFlow{Date: cf.Date, Amount: cf.Amount.Amount(),
				Label: label, Kind: "deposits", Ref: cf.ISIN}
			if cf.Type == domain.PayRedemption {
				f.Principal = cf.Amount.Amount()
			}
			add(dep.Bank, cf.Amount.Currency().Code, f)
		}
	}

	for k := range out {
		sortFlows(out[k])
		out[k] = coalesceSameDay(out[k])
	}
	return out, nil
}

// sortFlows — сталий порядок усередині зрізу: дата, потім підпис.
//
// Повним ключем, а не самою датою: sort.Slice нестабільний, і дві виплати
// одного дня ставали б у порядку, у якому їх поклав збирач. Спільна
// функція, бо зрізи сортуються в трьох місцях, і четверта копія цього
// порівняння розійшлася б із рештою тихо.
func sortFlows(flows []readyFlow) {
	sort.Slice(flows, func(i, j int) bool {
		if flows[i].Date != flows[j].Date {
			return flows[i].Date < flows[j].Date
		}
		return flows[i].Label < flows[j].Label
	})
}

// routeIncome — те саме, що futureIncome, плюс половина, якої та свідомо
// не бачить: оцінені дивіденди фондів.
//
// # ЧОМУ ТУТ ЇМ МІСЦЕ, А В ДАТІ «КОЛИ ВИСТАЧИТЬ» — НІ
//
// Відмова в шапці цього файла стосується ОДНОГО ЧИСЛА. Дата — саме одне
// число, і оцінка, підмішана в нього, стає невидимою: «набереться 16
// вересня» не має де сказати, що воно набереться, лише якщо фонд заплатить
// стільки, скільки платив торік.
//
// Маршрут — таблиця названих рядків, і в кожного рядка своя основа, яку
// видно (Basis). Зобовʼязання й оцінка не зводяться в ньому в одне число
// ніде: горщик, у якому вони зійшлись, чесно каже basisMixed. Це той самий
// стандарт, за яким оцінка вже живе в календарі виплат — «стоїть
// підписаною й читається як оцінка».
//
// futureIncome при цьому не змінюється НІЯК, і на це є регресійний тест:
// саме її незмінність і тримає ту відмову.
//
// # ЧОГО НЕМАЄ Й ТУТ
//
// Виплат НПФ: гроші звідти не приходять до пенсійного віку, а обрій
// маршруту — рік. Ця межа лишається.
//
// А от планового доходу тут більше НЕ бракує, і відмова, що стояла на
// цьому місці, знялась не поступкою, а знахідкою. Вона казала: план
// нетиться з витратами, тож завести його сюди без другого означення
// «скільки план дає чистими» не виходить. Друге означення й не знадобилось
// — перше вже було: MonthPlan.PlanUAH (state_month.go) і є тим числом,
// дохід плюс позапланове мінус витрати, з уже застосованою часткою в
// портфель. Від нього ж рахується стеля подушки, тож маршрут і стеля
// говорять про одні й ті самі гроші. Збирає ті події planAhead нижче.
func (s *Server) routeIncome(src *sources, today domain.Date, months int) (incomeAhead, error) {
	out, err := s.futureIncome(src, today)
	if err != nil {
		return nil, err
	}
	if len(src.fundOps) == 0 {
		return out, nil
	}
	// Зведення позицій те саме, що будує документ: друге означення «скільки
	// сертифікатів у мене зараз» розійшлося б із першим на першій же
	// позначці ціни.
	hold := domain.NewHoldings(src.lots, src.sales, src.bonds,
		src.fundOps, src.fundPrices, src.payoutDays(), today)

	for i := range hold.Funds {
		fp := &hold.Funds[i].FundPosition
		// Накопичувальний не платить нічого: увесь його дохід сидить у ціні
		// сертифіката. Та сама перевірка, що в buildSchedule, і з тієї самої
		// причини — проставлений комусь payout_day інакше вигадав би
		// щомісячні дивіденди фонду, який їх не платить.
		if src.fundRefs[fp.Fund].Kind == store.FundAccumulating {
			continue
		}
		measured, _ := domain.DividendYieldNet(src.fundOps, fp, today)
		ref := src.fundRefs[fp.Fund]
		y := fundOwnRatePct(ref, measured)
		broker := fundBroker(src.fundOps, fp.Fund)
		// ТУТ — ЛИШЕ ГОТІВКОВА ЧАСТИНА, на відміну від календаря, який
		// показує всю нараховану ренту (довід — у buildSchedule). Маршрут
		// відповідає на питання «що впаде на рахунок і куди воно піде», а
		// сертифікатами, які фонд докупив сам, у НПФ не внесеш.
		//
		// Ціна помилки була не косметична: на живих даних застосунок
		// показував 68,60 ₴ там, де приходить близько 1,85 ₴, і щомісяця
		// радив віднести в пенсійний гроші, яких не існуватиме.
		for _, f := range domain.FundDividendFlows(fp, y, months, today,
			ref.Kind == store.FundReinvesting) {
			if f.Amount.Amount() <= 0 {
				// Місяць, у якому решти не лишилось (рента поділилась на
				// сертифікати без остачі). Нога на 0 ₴ подією не є, а горщик
				// і стелю подушки прокрутила б.
				continue
			}
			k := store.BrokerCur{Broker: broker, Currency: f.Amount.Currency().Code}
			out[k] = append(out[k], readyFlow{
				Date: f.Date, Amount: f.Amount.Amount(),
				Label: fp.Fund, Kind: "funds", Basis: basisEstimate,
				Why: reinvestFlowWhy(f.Split, fp.Currency),
			})
		}
	}
	// Пересортувати треба ті пари, куди дивіденди справді лягли: futureIncome
	// віддає кожен зріз відсортованим, а append це порушив. Зведення одного
	// дня повторно безпечне — воно ідемпотентне за побудовою.
	for k := range out {
		sortFlows(out[k])
		out[k] = coalesceSameDay(out[k])
	}
	return out, nil
}

// planAhead — плановий дохід як події маршруту.
//
// # ЧОМУ ОДНА НОГА НА МІСЯЦЬ, А НЕ ПО ОДНІЙ НА ПОТІК
//
// Бо чесного числа «скільки цей потік дає на свою дату» не існує. План —
// нетто: комуналка, внесок у пенсійний і зарплата зводяться в одне
// PlanUAH, і рознести витрати місяця по датах доходу можна лише вигаданим
// правилом (порівну? пропорційно? з першої зарплати?). Будь-яке з них
// виглядало б виведеним і насправді було б вибором автора коду.
//
// PlanUAH при цьому не наближення: це те саме число, від якого
// reserveMonthShare рахує стелю подушки й від якого ребаланс ділить гроші
// місяця між видами. Третій читач того самого числа розійтись із ними не
// може за побудовою.
//
// # ДАТА — ДЕНЬ НАЙПІЗНІШОГО ДОХОДУ МІСЯЦЯ
//
// Раніше за нього місячна сума ще неповна, а ставити її першим числом
// означало б обіцяти гроші, яких того дня немає. День береться з дати
// початку потоку тим самим receiptDueDate, що малює чеклист надходжень
// («зарплата 17-го приходить 17-го»), тож два екрани називають один день.
//
// # БРОКЕРА НЕМАЄ, І ЦЕ НЕ ПРОГАЛИНА
//
// Планових грошей на брокерському рахунку ще немає — вони на картці. Тому
// «—», той самий рахунок без привʼязки, що й у гаманці. Наслідок треба
// назвати вголос: зарплата НЕ доскладеться до купона в mono, щоб разом
// набрати на цілий квиток. Це чесніше за протилежне — сказати, що гроші
// вже лежать там, де їх нема.
//
// Ref порожній: планове надходження відмічають у чеклисті плану
// (plan/receipts), а не статусом виплати, і кнопка «Прийшло» на такій нозі
// вела б не туди. Kind і Principal нульові: жоден вид не худне.
func planAhead(src *sources, plans map[string]*state.MonthPlan,
	today domain.Date, months int) []readyFlow {

	if len(src.planFlows) == 0 {
		return nil
	}
	var out []readyFlow
	for m := 0; m <= months; m++ {
		key := monthKeyAt(today, m)
		mp := plans[key]
		if mp == nil {
			continue
		}
		// Поточний місяць віддає лише НЕДОНЕСЕНЕ. Частину плану могли вже
		// закинути, і показати її ще раз означало б повести ті самі гроші
		// двічі — спершу в підсумку місяця, потім у маршруті.
		amount := mp.PlanUAH
		if m == 0 {
			amount = mp.LeftUAH
		}
		if amount <= 0 {
			continue
		}
		day := lastIncomeDay(src.planFlows, today, m)
		if day == 0 {
			continue
		}
		date := domain.Date(receiptDueDate(key, day))
		if date == "" || date < today {
			// День уже минув: гроші або прийшли (і лежать у балансі), або не
			// прийшли — і ні того, ні того маршрут вести не має.
			continue
		}
		out = append(out, readyFlow{
			Date: date, Amount: int64(math.Round(amount * 100)),
			Label: "план місяця", Basis: basisPlan,
		})
	}
	return out
}

// lastIncomeDay — день найпізнішого ДОХОДНОГО потоку цього місяця.
//
// Валовим фокусом (InvestBP = 10000), як і чеклист: питання тут «чи платить
// цього місяця», а не «скільки з нього доходить до портфеля», і потік із
// нульовою часткою в портфель дату однаково задає. Витрати не рахуються —
// їхні дні до «коли гроші зібрались» стосунку не мають.
func lastIncomeDay(flows []store.PlanFlow, today domain.Date, m int) int {
	day := 0
	for _, f := range flows {
		if f.Kind != "income" {
			continue
		}
		gross := f
		gross.InvestBP = 10000
		if planFlowAtMonth(gross, today, m, nil) == 0 {
			continue
		}
		if d := f.FromDate.Day(); d > day {
			day = d
		}
	}
	return day
}

// fundBroker — рахунок, на який фонд платить.
//
// Мажоритарний: брокер, у якого цього фонду куплено найбільше. Точної
// відповіді тут немає в принципі — операція фонду несе брокера, а виплата
// ні, — і ділити оцінений дивіденд пропорційно між двома рахунками означало
// б розбити його на суми, менші за будь-який квиток, тобто зробити маршрут
// гіршим заради видимості точності. Нічия й порожнеча дають «—», той самий
// рахунок без привʼязки, що й у гаманці.
func fundBroker(ops []domain.FundOp, fund string) string {
	byBroker := map[string]int64{}
	for _, op := range ops {
		if op.Kind != domain.FundBuy || op.Fund != fund {
			continue
		}
		b := op.Broker
		if b == "" {
			b = noBrokerLabel
		}
		byBroker[b] += op.Amount
	}
	best, bestAmt, tie := noBrokerLabel, int64(0), false
	// Обхід за відсортованими ключами: мапа в Go обходиться в довільному
	// порядку, і два запуски на тих самих даних інакше давали б різних
	// брокерів, а разом із ними — різні горщики.
	names := make([]string, 0, len(byBroker))
	for b := range byBroker {
		names = append(names, b)
	}
	sort.Strings(names)
	for _, b := range names {
		switch v := byBroker[b]; {
		case v > bestAmt:
			best, bestAmt, tie = b, v, false
		case v == bestAmt && bestAmt > 0:
			tie = true
		}
	}
	if tie {
		return noBrokerLabel
	}
	return best
}

// coalesceSameDay зводить виплати одного паперу одного дня в одну подію.
//
// Купон і погашення приходять окремими рядками розкладу — типи різні, і в
// календарі це правильно. Але на рахунок вони лягають одним приходом того
// самого дня, і в складі суми «UA4000235865 817,50 ₴ + UA4000235865
// 10 000,00 ₴» назва повторюється двічі там, де сталася одна подія.
// Знайдено на живих даних НБУ, а не тестом.
//
// Зведення нічого не міняє в даті: доданки того самого дня в будь-якому
// разі накопичуються разом.
func coalesceSameDay(flows []readyFlow) []readyFlow {
	out := flows[:0:0]
	for _, f := range flows {
		if n := len(out); n > 0 && out[n-1].Date == f.Date && out[n-1].Label == f.Label {
			out[n-1].Amount += f.Amount
			// Тіло складається разом із сумою: зведений рядок і далі каже,
			// скільки в ньому доходу, а скільки повернення власного. Купон і
			// погашення одного паперу одного дня — саме той випадок, заради
			// якого це зведення й існує.
			out[n-1].Principal += f.Principal
			continue
		}
		out = append(out, f)
	}
	return out
}

// readiness — коли й де набереться потрібна сума.
type readiness struct {
	Date   domain.Date
	Broker string
	Via    []readyFlow
}

// readyFor — перший день, коли якийсь із рахунків покриє costMinor.
//
// Обходимо кожного брокера окремо: баланс сьогодні плюс його власні
// надходження по датах. Найраніша дата серед брокерів і є відповіддю; при
// однакових датах виграє менша назва — щоб два запуски на тих самих даних
// не давали різних брокерів (мапа в Go обходиться в довільному порядку).
func (inc incomeAhead) readyFor(doc *state.Doc, currency string, costMinor int64) (readiness, bool) {
	if costMinor <= 0 {
		return readiness{}, false
	}
	brokers := map[string]bool{}
	for name := range doc.Brokers {
		brokers[name] = true
	}
	for k := range inc {
		if k.Currency == currency {
			brokers[k.Broker] = true
		}
	}
	names := make([]string, 0, len(brokers))
	for name := range brokers {
		names = append(names, name)
	}
	sort.Strings(names)

	var best readiness
	found := false
	for _, name := range names {
		bal := brokerBalanceMinor(doc, name, currency)
		var via []readyFlow
		for _, f := range inc[store.BrokerCur{Broker: name, Currency: currency}] {
			bal += f.Amount
			via = append(via, f)
			if bal < costMinor {
				continue
			}
			if !found || f.Date.Before(best.Date) {
				best = readiness{Date: f.Date, Broker: name, Via: via}
				found = true
			}
			break
		}
	}
	return best, found
}

// annotateReady дописує до порад дату доступності й ціну очікування.
//
// Мовчить там, де відповіді немає: рядок, на який стає вже сьогодні, дати
// не отримує (він і так зверху), а рядок, на який із відомих надходжень не
// набереться, отримує названу причину замість порожнечі.
func (s *Server) annotateReady(ctx context.Context, today domain.Date,
	doc *state.Doc, sug []suggestion) error {
	src, err := s.loadSources(ctx, today)
	if err != nil {
		return err
	}
	inc, err := s.futureIncome(src, today)
	if err != nil {
		return err
	}
	for i := range sug {
		if sug[i].CanBuy {
			continue
		}
		cost, cerr := parseMoney(sug[i].CostPerBond.Amount, sug[i].CostPerBond.Currency)
		if cerr != nil || cost.Amount() <= 0 {
			continue
		}
		r, ok := inc.readyFor(doc, sug[i].Currency, cost.Amount())
		if !ok {
			sug[i].ReadyNote = "з відомих надходжень портфеля не набереться"
			continue
		}
		sug[i].ReadyOn = string(r.Date)
		sug[i].ReadyBroker = r.Broker
		sug[i].ReadyDays = domain.DaysBetween(today, r.Date)
		sug[i].ReadyVia = make([]readyEvent, 0, len(r.Via))
		for _, f := range r.Via {
			sug[i].ReadyVia = append(sug[i].ReadyVia, readyEvent{
				Date: string(f.Date), Label: f.Label,
				Amount: toMoneyJSON(money.New(f.Amount, sug[i].Currency)),
			})
		}
		annotateWaitCost(&sug[i], sug, doc)
	}
	return nil
}

// annotateWaitCost — скільки коштує це очікування, міряне альтернативою.
//
// Альтернатива береться НЕ найдохідніша взагалі, а та, яку справді можна
// виконати замість очікування: та сама валюта і той самий рахунок, на
// якому ми чекаємо. Найдохідніший рядок в іншого брокера — не вибір, а
// сусідній рядок таблиці, і міряти ним втрату означало б порахувати
// втраченим те, чого не було.
//
// Працюють не всі гроші рахунку, а стільки, скільки складається в цілу
// кількість кроків альтернативи: решта однаково лежала б без діла, і
// зараховувати їй дохід було б тим самим вигаданим числом, від якого
// застосунок відмовляється всюди.
func annotateWaitCost(row *suggestion, all []suggestion, doc *state.Doc) {
	if row.ReadyDays <= 0 {
		return
	}
	bal := brokerBalanceMinor(doc, row.ReadyBroker, row.Currency)
	if bal <= 0 {
		return
	}
	var alt *suggestion
	var altCost int64
	for i := range all {
		a := &all[i]
		if !a.CanBuy || a.Currency != row.Currency || a.Label == row.Label {
			continue
		}
		fitsHere := false
		for _, f := range a.Brokers {
			if f.Broker == row.ReadyBroker {
				fitsHere = true
				break
			}
		}
		if !fitsHere {
			continue
		}
		c, cerr := parseMoney(a.CostPerBond.Amount, a.CostPerBond.Currency)
		if cerr != nil || c.Amount() <= 0 || c.Amount() > bal {
			continue
		}
		if alt == nil || a.RealPct > alt.RealPct {
			alt, altCost = a, c.Amount()
		}
	}
	if alt == nil {
		return
	}
	working := bal / altCost * altCost
	cost := domain.WaitCost(working, alt.RealPct, row.ReadyDays)
	if cost <= 0 {
		return
	}
	m := toMoneyJSON(money.New(cost, row.Currency))
	row.WaitCost = &m
	row.WaitAlt = alt.Label
}
