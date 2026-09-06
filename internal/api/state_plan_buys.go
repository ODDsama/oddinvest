// Рядок плану купівель → числа. Одна фаза, два ЗОВСІМ різні виходи.
//
// Рядок «купую зараз» стає гіпотетичним портфелем: лот, операція фонду,
// вклад чи внесок дописуються в sources, і далі частки, драбину, дюрацію
// й концентрацію рахує той самий код, що й завжди (state_builder.go).
//
// Рядок із МАЙБУТНЬОЮ датою так не можна. Він збрехав би одразу про два
// числа: сьогоднішні валютні частки (папір, якого ще немає, уже в
// знаменнику) і готівку (state_builder списує тіло вкладу лише
// `if !dep.OpenDate.After(today)`, а НПФ-цикл робить `continue` на
// майбутню дату — тобто капітал виріс би, не заплативши за себе).
// Тому майбутній рядок іде не в портфель, а в ПЛАН. Каналів туди ТРИ, і
// вибір між ними — це вибір МОДЕЛІ, а не місця запису:
//
//   - замок plan_actions — ОВДП, вклад і РОЗПОДІЛЬНИЙ фонд. Спільне в
//     них те, що тіло не росте, а дохід приходить купоном: саме це
//     замок і робить. Для Dist він не компроміс, а точний опис.
//   - planFunds — НАКОПИЧУВАЛЬНИЙ і реінвестуючий фонд. У замкненому
//     такий фонд лежав би цеглиною (довід дослівно записаний у
//     domain/sleeves.go над Accum), а весь його дохід саме в зростанні.
//   - потік plan_flows — внесок у пенсійний.
//
// Чому саме ці механізми, а не інші. Кожен уже існує й уже вміє рівно
// те, що треба: stepSleeve переносить суму замка з ліквідного боку в
// замкнений, нічого не створюючи (domain/projection.go); Spend знімає
// гроші так само, але віддає їх позиції Accum, яка росте сама; а потік
// із Dest="npf:<id>" веде ОБИДВІ половини руху — мінус на ліквідному
// боці й плюс у накопичувальну позицію з прапорцем Locked
// (state_projection.go). Замок для НПФ не годиться: він поверне тіло
// цілком одним місяцем і втратить Locked, тобто декумуляція почала б
// витрачати пенсійні гроші — про що прямо попереджає шапка accum.go.
//
// ОДИН РЯДОК — ОДИН КАНАЛ. Розгалуження нижче рівно одне (isFuture), і
// це головний захист від подвійного рахунку: рядок не може одночасно
// лежати в портфелі й стояти в плані.
//
// «ЗАРАЗ» ТУТ І «СКОРО» В МАРШРУТІ — РІЗНІ ЧИСЛА, І ЦЕ НАВМИСНО.
//
// route.go стверджує, що «скоро» в застосунку одне число (taskSoonDays,
// ковзні 30 днів), і для НЬОГО це правда: воно про увагу — що варто
// подивитись найближчим часом. Поріг тут про інше — у яку МОДЕЛЬ
// потрапляє рядок, у портфель чи в прогноз, — і він календарно-місячний,
// бо такою є сітка симуляції.
//
// Вирівнювати їх не можна в жоден бік. Календарний місяць 31-го числа
// стиснув би «скоро» до одного дня; тридцятиденне вікно поклало б рядок
// у крок, якого прогноз не вміє виразити. Ця розбіжність написана тут
// саме тому, що жоден тест її не тримає: annotatePlanned звіряє ноги
// точним збігом рядка дати й Future не читає взагалі, тож «уніфікація»
// пройшла б зеленою.

// Синтетика НІКОЛИ не пишеться в сховище. Вона живе рівно один виклик
// buildStateWith, тож GET /api/plan/actions і /api/plan/flows після
// будь-якого whatif лишаються тими самими — це закріплено тестом.
package api

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// defaultDepositTaxBP — податок на відсотки вкладу за замовчуванням
// (ПДФО 18% + військовий збір 5%). Те саме число, що підставляє форма
// відкриття вкладу: планована й справжня однакова угода не має права
// давати два різні графіки.
const defaultDepositTaxBP = 2300

// synthID — лічильник id для гіпотетичних вкладів і внесків.
//
// ВІДʼЄМНІ й спадні, і це не косметика. Синтетичний ISIN вкладу —
// "deposit:<id>", і за цим ключем його шукають DepositSchedule, arrived(),
// підпис рунги в state/derive.go і задача reserve-rung-* у state_tasks.go.
// Два гіпотетичні вклади з id=0 були б одним інструментом для всіх
// чотирьох. AUTOINCREMENT ніколи не видає id ≤ 0, тож зіткнутися з
// реальним рядком неможливо, а payment_status на "deposit:-1" не існує —
// arrived() чесно каже «ще не надійшло».
type synthID struct{ n int64 }

func (s *synthID) next() int64 { s.n--; return s.n }

// planBuyExpansion — результат розгортання всього плану.
type planBuyExpansion struct {
	what   hypothetical
	basket basketDoc
	// spend — "брокер|валюта" → мінорні, ЛИШЕ по рядках «зараз», тобто
	// цього місяця й раніше.
	//
	// Майбутніх тут немає навмисно: сьогоднішній залишок нічого не каже
	// про покупку в березні, і назвати нестачею те, що станеться після
	// п'яти зарплат, означало б лякати даремно. Горизонт при цьому — саме
	// МІСЯЦЬ, а не день: рядок цього місяця вже пішов у портфель, і
	// state_builder за нього списав готівку брокера. Не порахувати його
	// тут означало б показати наслідок (залишок упав, ба навіть у мінус)
	// без рядка, який називає причину, — і написати «грошей вистачає»
	// поруч із від'ємним балансом.
	spend map[string]int64
}

// expandPlanBuys — увесь план купівель у гіпотезу й рядки кошика.
//
// before потрібен двічі: за цінами сертифікатів (їх знає лише зведення) і
// за брокером, коли його не назвали.
func (s *Server) expandPlanBuys(ctx context.Context, before *state.Doc,
	today domain.Date, rows []store.PlanBuy) (planBuyExpansion, error) {

	out := planBuyExpansion{
		basket: basketDoc{Lines: []basketLine{}},
		spend:  map[string]int64{},
	}
	totals := map[string]int64{}
	var ids synthID

	// Довідники читаються один раз на весь план, а не по разу на рядок:
	// план на десять рядків інакше давав би десять однакових запитів.
	var fundRefs map[string]store.Fund
	var npfByID map[int64]domain.NPFAccount

	for _, row := range rows {
		// МАЙБУТНЄ ТУТ МІРЯЄТЬСЯ МІСЯЦЯМИ, А НЕ ДНЯМИ, і це не округлення.
		//
		// Сітка симуляції календарно-місячна (domain.MonthsBetween), і
		// «покупці 28-го» в ній немає де стояти окремо від «покупки
		// сьогодні»: monthOffset однаково кладе обидві на перший крок.
		// Доти поріг був строгим (After(today)), і завтрашній рядок не
		// потрапляв НІКУДИ — у портфель не входив, бо майбутній, а в
		// прогнозі його разова половина зникала на тому самому нулі
		// (див. planFlowNative). Людина, ставлячи дату в межах цього
		// місяця, має на увазі «беру зараз» — і тепер це так і працює.
		future := row.BuyDate != "" && domain.MonthsBetween(today, row.BuyDate) > 0
		// Overdue — дата вже минула. ОКРЕМЕ ПОЛЕ, а не порівняння в
		// браузері: «сьогодні» на сервері й у браузері — різні дати в
		// різних поясах, і підпис розійшовся б із класифікацією, яка дала
		// самі числа.
		overdue := row.BuyDate != "" && row.BuyDate.Before(today)
		// Дата покупки для сьогоднішнього рядка — СЬОГОДНІ, навіть якщо в
		// ньому стоїть учорашня. Прострочений намір — це намір, гроші за
		// ним досі не витрачені, і датувати гіпотетичний лот минулим
		// означало б дописати портфелю історію, якої не було.
		when := today
		if future {
			when = row.BuyDate
		}

		line := basketLine{Kind: row.Kind, Qty: row.Qty, ID: row.ID,
			BuyDate: string(row.BuyDate), Future: future, Overdue: overdue,
			IsReserve: row.IsReserve}
		var unit *money.Money
		// emit дописує сам запис — і кличеться ПІСЛЯ того, як став відомий
		// брокер. Брокера не можна знати раніше: без назви його обирає
		// pickBroker за найбільшим залишком у ВАЛЮТІ, а валюту знає лише
		// сам інструмент. Проставляти його двічі — у записі й у рядку —
		// означало б дати двом місцям розійтись.
		var emit func(broker string)

		switch row.Kind {
		case store.BuyBond:
			b, err := s.st.GetBond(ctx, row.Ref)
			if err != nil || b == nil {
				return out, badRequestf("паперу %q немає в довіднику", row.Ref)
			}
			pays, perr := s.st.PaymentsFor(ctx, []string{row.Ref})
			if perr != nil {
				return out, perr
			}
			unit = bondUnitCost(*b, pays, when)
			line.Label = row.Ref
			if unit.Amount() <= 0 {
				return out, badRequestf("%s: ціни немає, купувати нема за чим", row.Ref)
			}
			total := unit.Amount() * row.Qty
			if future {
				// Ставка замка — дохідність до погашення, порахована тією
				// самою domain.YTM, якою її показує «Що купити». Друге
				// означення дохідності того самого паперу дало б два різні
				// числа на сусідніх екранах.
				ytm, yerr := domain.YTM(unit, when, pays, row.Ref)
				if yerr != nil || ytm <= 0 {
					return out, badRequestf(
						"%s: не вдалось порахувати дохідність до погашення — без неї замок у прогнозі лише заморозить гроші",
						row.Ref)
				}
				act := lockAction(row, when, total, unit.Currency().Code,
					bpFromPct(ytm*100), monthsUntil(when, b.Maturity), "план: "+row.Ref)
				emit = func(string) { out.what.actions = append(out.what.actions, act) }
			} else {
				// Довідник паперу їде РАЗОМ із лотом: loadSources тягне його
				// лише для ISIN, що вже зустрічаються в портфелі, і без цього
				// куплений уперше папір увійшов би в капітал нулем (див.
				// hypothetical.bonds).
				if out.what.bonds == nil {
					out.what.bonds = map[string]domain.Bond{}
				}
				out.what.bonds[row.Ref] = *b
				out.what.pays = append(out.what.pays, pays...)
				emit = func(broker string) {
					out.what.lots = append(out.what.lots, domain.Lot{
						ISIN: row.Ref, Qty: row.Qty, PricePerBond: unit,
						BuyDate: when, Channel: broker,
					})
				}
			}

		case store.BuyFund:
			if fundRefs == nil {
				refs, ferr := s.st.ListFunds(ctx)
				if ferr != nil {
					return out, ferr
				}
				fundRefs = map[string]store.Fund{}
				for _, f := range refs {
					fundRefs[f.Name] = f
				}
			}
			ref := fundRefs[row.Ref]
			cur := firstNonEmpty(row.Currency, ref.Currency, fundCurrencyOf(before, row.Ref), money.UAH)
			unit = fundUnitCost(planBuyFundPrice(row, before), cur)
			line.Label = row.Ref
			if unit.Amount() <= 0 {
				return out, badRequestf(
					"%s: ціни сертифіката немає — цього фонду ще немає в портфелі, тож задай ціну за штуку",
					row.Ref)
			}
			total := unit.Amount() * row.Qty
			if future {
				// РОЗВИЛКА ЗА ВИДОМ ФОНДУ, і вона не косметична.
				//
				// Розподільний лишається ЗАМКОМ, бо замок і є правильною
				// його моделлю: Dist так само не росте тілом і платить
				// власною ставкою в готівку. Накопичувальний у замкненому
				// лежав би цеглиною — довід дослівно записаний у
				// domain/sleeves.go над Accum, — а весь його дохід саме в
				// зростанні, тож він іде окремим каналом.
				if ref.Kind == store.FundDistributing {
					rateBP, months, rerr := fundLockTerms(ref, when)
					if rerr != nil {
						return out, rerr
					}
					act := lockAction(row, when, total, cur, rateBP, months, "план: "+row.Ref)
					emit = func(string) { out.what.actions = append(out.what.actions, act) }
				} else {
					fb, ferr := planFundAccum(ref, when, today, float64(total)/100, cur)
					if ferr != nil {
						return out, ferr
					}
					emit = func(string) { out.what.planFunds = append(out.what.planFunds, fb) }
				}
			} else {
				emit = func(broker string) {
					out.what.fundOps = append(out.what.fundOps, domain.FundOp{
						Date: when, Fund: row.Ref, Kind: domain.FundBuy, Qty: row.Qty,
						Amount: total, Currency: cur, Broker: broker,
					})
				}
			}

		case store.BuyDeposit:
			cur := orUAH(row.Currency)
			unit = money.New(row.Amount, cur)
			line.Qty = 1
			line.Label = row.Ref
			rateBP := row.RateBP
			if rateBP <= 0 {
				rateBP = settingsDepositRateBP(before, cur)
			}
			// Перевірка стоїть ДО розвилки, а не всередині майбутньої
			// гілки. Доти вона була там, і поки «майбутнє» починалось із
			// завтра, це збігалось. Тепер вклад, відкритий цього місяця,
			// іде в портфель — і мовчазний RateBP: 0 дав би нарахування
			// на нуль, тобто неправильне число там, де раніше була чесна
			// відмова.
			if rateBP <= 0 {
				return out, badRequestf(
					"вклад у %s: ставки немає ні в рядку, ні в налаштуваннях — без неї порахувати відсотки нема з чого",
					cur)
			}
			if future {
				// НЕТТО, як і в помічнику реінвесту: відсотки вкладу
				// оподатковані, купон ОВДП ні, і брутто в тій самій моделі
				// робило б вклад систематично кращим, ніж він є.
				net := domain.NetRate(rateBP, defaultDepositTaxBP)
				act := lockAction(row, when, row.Amount, cur,
					bpFromPct(net*100), row.Months, "план: вклад "+row.Ref)
				emit = func(string) { out.what.actions = append(out.what.actions, act) }
			} else {
				dep := domain.Deposit{
					ID: ids.next(), Bank: row.Ref, Currency: cur,
					Principal: row.Amount, RateBP: rateBP,
					OpenDate: when, MaturityDate: when.AddMonths(row.Months),
					// Payout і TaxBP — ті самі дефолти, що підставляє форма
					// відкриття вкладу. Літерали тут розійшлися б із нею на
					// першій же зміні податку.
					Payout: domain.PayoutEnd, TaxBP: defaultDepositTaxBP,
					IsReserve: row.IsReserve,
				}
				emit = func(string) { out.what.deposits = append(out.what.deposits, dep) }
			}

		case store.BuyNPF:
			if npfByID == nil {
				accs, aerr := s.st.ListNPFAccounts(ctx)
				if aerr != nil {
					return out, aerr
				}
				npfByID = map[int64]domain.NPFAccount{}
				for _, a := range accs {
					npfByID[a.ID] = a
				}
			}
			id, _ := strconv.ParseInt(row.Ref, 10, 64) //nolint:errcheck // форму перевірив planBuyFromReq
			acc, ok := npfByID[id]
			if !ok {
				return out, badRequestf("пенсійного рахунку %s немає", row.Ref)
			}
			cur := orUAH(acc.Currency)
			unit = money.New(row.Amount, cur)
			line.Qty = 1
			line.Label = acc.Name
			if future {
				// Внесок у пенсійний — ПОТІК, а не замок: лише потік уміє
				// обидві половини руху (див. шапку файла).
				fl := store.PlanFlow{
					Name: "план: " + acc.Name, Kind: "expense", Amount: row.Amount,
					Currency: cur, Cadence: "once", FromDate: when,
					InvestBP: 10000, Dest: domain.NPFPlanDest(acc.ID),
				}
				emit = func(string) { out.what.flows = append(out.what.flows, fl) }
			} else {
				// Одиниці купуються за сьогоднішньою ЧВОПА, інакше внесок
				// списав би гроші й не приніс вартості: капітал просів би
				// рівно на суму внеску.
				if acc.Nav <= 0 {
					return out, badRequestf(
						"%s: ЧВОПА невідома — порахувати, скільки одиниць купить внесок, нема з чого",
						acc.Name)
				}
				op := domain.NPFOp{
					ID: ids.next(), NPFID: acc.ID, Date: when,
					Units: row.Amount * 10_000_000_000 / acc.Nav, Amount: row.Amount,
				}
				emit = func(broker string) {
					op.Broker = broker
					out.what.npfOps = append(out.what.npfOps, op)
				}
			}

		default:
			return out, badRequestf("невідомий вид покупки %q", row.Kind)
		}

		cur := unit.Currency().Code
		total := unit.Amount() * max64(line.Qty, 1)
		broker, assumed := pickBroker(before, cur, row.Broker)
		if row.Kind == store.BuyDeposit {
			// У вкладу «брокер» — це банк, і він уже названий у ref: гроші
			// списуються саме з нього (state_builder.go), тож підставляти
			// сюди рахунок із найбільшим залишком означало б питати про
			// нестачу не в тієї установи.
			broker, assumed = row.Ref, false
		}
		emit(broker)
		line.Unit = toMoneyJSON(unit)
		line.Total = toMoneyJSON(money.New(total, cur))
		line.Currency = cur
		line.Broker, line.Assumed = broker, assumed
		out.basket.Lines = append(out.basket.Lines, line)
		totals[cur] += total
		if !future {
			out.spend[broker+"|"+cur] += total
		}
	}

	for cur, v := range totals {
		out.basket.Totals = append(out.basket.Totals, toMoneyJSON(money.New(v, cur)))
	}
	sortMoneyJSON(out.basket.Totals)
	return out, nil
}

// lockAction — синтетична дія «замкнути суму на строк». Id не має
// навмисно: у сховище вона не потрапляє ніколи.
func lockAction(row store.PlanBuy, when domain.Date, amount int64,
	cur string, rateBP int64, months int, name string) store.PlanAction {
	return store.PlanAction{
		Date: when, Type: "lock", USDBP: -1, EURBP: -1,
		Amount: amount, Currency: cur, RateBP: rateBP, Months: months,
		Name: name, Note: row.Note,
	}
}

// fundLockTerms — під яку ставку й на який строк замикається планований
// сертифікат.
//
// Ставка береться з КАТАЛОГУ, а не з виміряних дивідендів позиції, і це
// свідомо: замок описує фонд УПЕРЕД, а yield_net_pct рядка позиції міряє
// минуле. Заразом це єдине джерело працює однаково для фонда, який уже є
// в портфелі, і для того, якого ще немає.
//
// Строк — до закриття фонду; безстроковий лишається з months == 0, і
// planLockFlows тоді платить купон до кінця горизонту й не повертає тіла.
// Для накопичувального це компроміс, записаний нижче.
func fundLockTerms(ref store.Fund, when domain.Date) (int64, int, error) {
	if ref.Name == "" {
		return 0, 0, badRequestf(
			"фонду немає в довіднику — без обіцяної дохідності замок у прогнозі лише заморозить гроші")
	}
	pct := float64(ref.ExpectedYieldBP) / 100
	if ref.YieldSimpleYears > 0 {
		pct = domain.CompoundFromSimple(pct, int(ref.YieldSimpleYears))
	}
	months := 0
	years := 0.0
	if ref.CloseDate != "" {
		if d, derr := domain.ParseDate(ref.CloseDate); derr == nil {
			if m := domain.MonthsBetween(when, d); m > 0 {
				months, years = m, float64(m)/12
			}
		}
	}
	// Податок береться з доходу, і брутто поруч зі звільненим від податку
	// купоном ОВДП робило б фонд систематично кращим, ніж він є.
	if ref.IncomeTaxBP > 0 {
		pct = domain.NetOfTax(pct, float64(ref.IncomeTaxBP)/100, years)
	}
	if pct <= 0 {
		return 0, 0, badRequestf(
			"у фонда «%s» не задана очікувана дохідність — без неї замок у прогнозі лише заморозить гроші",
			ref.Name)
	}
	// КОМПРОМІС, ЯКОГО ТУТ БІЛЬШЕ НЕМАЄ — і абзац лишається, щоб його не
	// винайшли заново.
	//
	// Доти сюди приходили ВСІ види фондів, і накопичувальному замок
	// приписував чужу механіку: у моделі це Accum зі складним відсотком
	// усередині, а замок платив простий купон від замороженого тіла.
	// Похибка звалась консервативною, і нею вона й була — але коштувала
	// на живій фікстурі 73% користі від покупки (місячний план дешевшав
	// на 898 ₴ замість 3282 ₴). Названа тут же альтернатива — «Accum зі
	// стартовим місяцем» — виявилась дешевшою, ніж здавалось: Accum уже
	// вмів народитись порожнім і профінансуватись у місяці m0, бракувало
	// лише дебетної половини (domain.Sleeve.Spend).
	//
	// Тепер сюди доходить лише РОЗПОДІЛЬНИЙ фонд, і для нього замок — не
	// компроміс, а правильна модель: Dist так само не росте тілом і
	// платить власною ставкою в готівку.
	return bpFromPct(pct), months, nil
}

// planFundAccum — планована купівля НАКОПИЧУВАЛЬНОГО (чи реінвестуючого)
// сертифіката: позиція, якої сьогодні ще немає.
//
// ДЗЕРКАЛО state_funds.go (збірка Accum/Dist для вже наявної позиції), і
// це головне про цю функцію. Той самий fundOwnRatePct, той самий
// accumCloseM, та сама поправка на валюту лише зростанню й той самий
// податок ОКРЕМИМ полем. Планований фонд і той, що вже лежить у
// портфелі, мусять оцінюватись однією моделлю — інакше «Що зміниться»
// казало б одне про покупку й інше про неї ж наступного дня.
//
// Відмінність від fundLockTerms поруч варто знати: замок згортає податок
// у ставку через NetOfTax, бо іншого місця в нього немає. Accum тримає
// TaxPct окремо й бере податок із ПРИБУТКУ на закритті — це і чесніше, і
// збігається з тим, як порахована вже наявна позиція.
func planFundAccum(ref store.Fund, when, today domain.Date, amount float64,
	cur string) (planFundBuy, error) {

	if ref.Name == "" {
		return planFundBuy{}, badRequestf(
			"фонду немає в довіднику — без обіцяної дохідності позиція в прогнозі не росла б")
	}
	rate := fundOwnRatePct(ref, 0)
	if rate <= 0 {
		return planFundBuy{}, badRequestf(
			"у фонда «%s» не задана очікувана дохідність — без неї позиція в прогнозі не росла б",
			ref.Name)
	}
	out := planFundBuy{
		Currency: cur, When: when, Amount: amount,
		Rate: rate, RateCur: ref.ExpectedYieldCur,
		ExitTaxPct: float64(ref.ExitTaxBP) / 100,
	}
	// Реінвестуючий не закривається й податку на закритті не має —
	// довід дослівно той самий, що в state_funds.go для наявної позиції:
	// він докуповує сам себе, тож моменту виходу в моделі немає. Заразом
	// і поправки на валюту йому не роблять.
	if ref.Kind == store.FundAccumulating {
		out.Growth = true
		out.CloseM = accumCloseM(ref.CloseDate, today)
		out.TaxPct = float64(ref.IncomeTaxBP) / 100
	}
	return out, nil
}

// settingsDepositRateBP — ставка вкладу з налаштувань, коли в рядку її не
// задали. Те саме джерело, що в помічника реінвесту: два місця, що
// вгадують ставку по-різному, дали б різні прогнози на тому самому вкладі.
func settingsDepositRateBP(doc *state.Doc, cur string) int64 {
	if doc == nil || doc.Settings == nil {
		return 0
	}
	var p *float64
	switch cur {
	case money.USD:
		p = doc.Settings.DepositRateUSDPct
	case money.EUR:
		p = doc.Settings.DepositRateEURPct
	default:
		p = doc.Settings.DepositRateUAHPct
	}
	if p == nil || *p <= 0 {
		return 0
	}
	return bpFromPct(*p)
}

// planBuyFundPrice — ціна одного сертифіката: задана вручну або остання
// відома з позиції. Каталог цін фондів у застосунку відсутній навмисно
// (ціна приходить із виписки разом з операцією), тому про фонд, якого ще
// немає в портфелі, без ручної ціни сказати нічого не можна.
func planBuyFundPrice(row store.PlanBuy, doc *state.Doc) float64 {
	if row.UnitPrice > 0 {
		return float64(row.UnitPrice) / 100
	}
	if f := findFundRow(doc, row.Ref); f != nil {
		return f.LastPrice
	}
	return 0
}

func fundCurrencyOf(doc *state.Doc, name string) string {
	if f := findFundRow(doc, name); f != nil {
		return f.Currency
	}
	return ""
}

// monthsUntil — скільки повних місяців від дати покупки до погашення.
// Нуль означає «строку немає», і замок тоді платить до кінця горизонту:
// для паперу, що гаситься завтра, це було б неправдою, тож нижня межа —
// один місяць.
func monthsUntil(from domain.Date, to domain.Date) int {
	m := domain.MonthsBetween(from, to)
	if m < 1 {
		return 1
	}
	return m
}

// bpFromPct — відсоток у базисні пункти. Поруч живе pctToBP
// (handlers_npf.go), і це НЕ те саме: той розбирає рядок із форми, цей
// переводить уже пораховане число. Спільна назва змусила б читача щоразу
// перевіряти, який із двох перед ним.
func bpFromPct(pct float64) int64 { return int64(math.Round(pct * 100)) }

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// badRequestf — помилка, яку handleWhatIf віддає як 400. Окремий тип, бо
// решта помилок фази — це поломки сховища, і плутати їх з опискою у формі
// означало б показати людині 500 там, де вона просто не дозаповнила поле.
type badRequestError struct{ msg string }

func (e badRequestError) Error() string { return e.msg }

func badRequestf(format string, args ...any) error {
	return badRequestError{msg: fmt.Sprintf(format, args...)}
}
