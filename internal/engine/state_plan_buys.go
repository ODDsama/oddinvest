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
// BuildStateWith, тож GET /api/plan/actions і /api/plan/flows після
// будь-якого whatif лишаються тими самими — це закріплено тестом.
package engine

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

// defaultDepositTaxBP — податок на відсотки вкладу за замовчуванням: «за
// законом» (domain.TaxBPByLaw), ставкою на дату кожної виплати. Те саме,
// що підставляє форма відкриття вкладу: планована й справжня однакова
// угода не має права давати два різні графіки.
const defaultDepositTaxBP = domain.TaxBPByLaw

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
	what   Hypothetical
	basket BasketDoc
}

// expandPlanBuys — увесь план купівель у гіпотезу й рядки кошика.
//
// before потрібен двічі: за цінами сертифікатів (їх знає лише зведення) і
// за брокером, коли його не назвали.
func (e *Engine) expandPlanBuys(ctx context.Context, before *state.Doc,
	today domain.Date, rows []store.PlanBuy) (planBuyExpansion, error) {

	out := planBuyExpansion{basket: BasketDoc{Lines: []basketLine{}}}
	totals := map[string]int64{}
	var ids synthID

	// Довідники читаються один раз на весь план, а не по разу на рядок:
	// план на десять рядків інакше давав би десять однакових запитів.
	var fundRefs map[string]store.Fund
	var npfByID map[int64]domain.NPFAccount
	// Ринкові ціни — тим самим правилом «один запит на весь план».
	//
	// Ліниво, бо план часто без жодного паперу: читання цін для плану з
	// самих фондів було б запитом заради нічого.
	var quotes *quoteBook
	quoteFor := func(isin string) (*store.Quote, error) {
		if quotes == nil {
			b, err := e.QuotesFor(ctx, nil, today)
			if err != nil {
				return nil, err
			}
			quotes = &b
		}
		return quotes.pick(isin), nil
	}

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
			b, err := e.st.GetBond(ctx, row.Ref)
			if err != nil || b == nil {
				return out, BadRequestf("паперу %q немає в довіднику", row.Ref)
			}
			pays, perr := e.st.PaymentsFor(ctx, []string{row.Ref})
			if perr != nil {
				return out, perr
			}
			// Ціна НА ДАТУ ПОКУПКИ, і саме тому котировка сюди подається
			// без жодного особливого випадку: рядок плану на півроку
			// вперед не пройде перевірку свіжості в bondUnitCost і сам
			// відкотиться на номінал плюс НКД, як було завжди. Рядок же,
			// який купують сьогодні, дістане справжню ціну — ту саму, що
			// показує «Що купити».
			q, qerr := quoteFor(row.Ref)
			if qerr != nil {
				return out, qerr
			}
			unit, _ = bondUnitCost(*b, pays, when, q)
			line.Label = row.Ref
			if unit.Amount() <= 0 {
				return out, BadRequestf("%s: ціни немає, купувати нема за чим", row.Ref)
			}
			total := unit.Amount() * row.Qty
			if future {
				// Ставка замка — дохідність до погашення, порахована тією
				// самою domain.YTM, якою її показує «Що купити». Друге
				// означення дохідності того самого паперу дало б два різні
				// числа на сусідніх екранах.
				ytm, yerr := domain.YTM(unit, when, pays, row.Ref)
				if yerr != nil || ytm <= 0 {
					return out, BadRequestf(
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
				// Hypothetical.bonds).
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
				refs, ferr := e.st.ListFunds(ctx)
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
				return out, BadRequestf(
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
					// KeepPrice: гіпотеза не переоцінює пакет, який уже
					// лежить. Повний довід — над самим полем у
					// domain.FundOp; тут досить знати, що без нього
					// покупка на 46 ₴ додавала 352 ₴ капіталу.
					out.what.fundOps = append(out.what.fundOps, domain.FundOp{
						Date: when, Fund: row.Ref, Kind: domain.FundBuy, Qty: row.Qty,
						Amount: total, Currency: cur, Broker: broker,
						KeepPrice: true,
					})
				}
			}

		case store.BuyDeposit:
			cur := OrUAH(row.Currency)
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
				return out, BadRequestf(
					"вклад у %s: ставки немає ні в рядку, ні в налаштуваннях — без неї порахувати відсотки нема з чого",
					cur)
			}
			if future {
				// НЕТТО, як і в помічнику реінвесту: відсотки вкладу
				// оподатковані, купон ОВДП ні, і брутто в тій самій моделі
				// робило б вклад систематично кращим, ніж він є.
				// І ЕФЕКТИВНА: строк тут відомий (row.Months), а рукав
				// прогнозу компаундить щомісяця — підставити туди просту
				// ставку тризначного вкладу означало б домалювати йому
				// відсотки, яких договір не обіцяє. Договір збирається тими
				// самими дефолтами, що в гілці нижче.
				net := domain.Deposit{
					Currency: cur, Principal: row.Amount, RateBP: rateBP,
					OpenDate: when, MaturityDate: when.AddMonths(row.Months),
					Payout: domain.PayoutEnd, TaxBP: defaultDepositTaxBP,
				}.EffectiveNetRate()
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
				accs, aerr := e.st.ListNPFAccounts(ctx)
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
				return out, BadRequestf("пенсійного рахунку %s немає", row.Ref)
			}
			cur := OrUAH(acc.Currency)
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
					return out, BadRequestf(
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
			return out, BadRequestf("невідомий вид покупки %q", row.Kind)
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
		// ГРОШІ, ЯКИМИ ЦЕ ОПЛАЧЕНО — дзеркальним поповненням того самого
		// рахунку в тій самій валюті й на ту саму суму.
		//
		// ОДНЕ МІСЦЕ, А НЕ ЧОТИРИ. cur і total вище — це рівно те, що
		// касовий журнал списує за кожен вид: лот віддає LotCost, операція
		// фонду — Amount, внесок і вклад — свою суму. Дзеркалити кожну
		// гілку окремо означало б завести чотири нагоди розійтись зі
		// списанням, і розійшлися б вони тихо: помилку видно лише як
		// повільний дрейф капіталу.
		//
		// ЧОМУ ВЗАГАЛІ. Гіпотеза додавала витрату й нічого більше — і
		// капітал через це ПАДАВ на покупці. Довід повністю записаний при
		// Hypothetical.topUps.
		//
		// НЕ ГРИВНЕВИМ ЕКВІВАЛЕНТОМ однією сумою: доларовий папір списує
		// долари, і компенсація в гривні лишила б валютний рахунок у
		// мінусі, а гривневий — у плюсі. Рахунки роздільні (state_cash.go).
		//
		// МАЙБУТНІ РЯДКИ НЕ ЧІПАЄМО. Вони й не списують нічого сьогодні:
		// папір стає замком, фонд — накопиченням, внесок — потоком, а
		// вклад із майбутньою датою журнал пропускає сам. Дати їм гроші
		// означало б підняти сьогоднішній капітал за покупку, якої ще
		// немає.
		if !future {
			out.what.topUps = append(out.what.topUps, store.Deposit{
				Date: when, Amount: total, Currency: cur, Broker: broker,
				Note: "план купівель",
			})
		}
		line.Unit = ToMoneyJSON(unit)
		line.Total = ToMoneyJSON(money.New(total, cur))
		line.Currency = cur
		line.Broker, line.Assumed = broker, assumed
		out.basket.Lines = append(out.basket.Lines, line)
		totals[cur] += total
	}

	for cur, v := range totals {
		out.basket.Totals = append(out.basket.Totals, ToMoneyJSON(money.New(v, cur)))
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
		return 0, 0, BadRequestf(
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
		return 0, 0, BadRequestf(
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
		return planFundBuy{}, BadRequestf(
			"фонду немає в довіднику — без обіцяної дохідності позиція в прогнозі не росла б")
	}
	rate := fundOwnRatePct(ref, 0)
	// Реінвестуючий росте ВИПЛАТАМИ, тож його ставка — після податку з
	// кожного дивіденду, як і для наявної позиції (state_funds.go);
	// накопичувальний — брутто, податок на закритті окремим полем.
	if ref.Kind != store.FundAccumulating {
		rate = fundPayoutRatePct(ref, 0)
	}
	if rate <= 0 {
		return planFundBuy{}, BadRequestf(
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
//
// Ця ціна веде ВИТРАЧЕНУ ГОТІВКУ й колонку «За штуку» — і саме тому вона
// лишається копійчаною, попри те, що LastPrice тримає чотири знаки.
// Округлення тут більше не коштує нічого зайвого: відколи гіпотетична
// операція йде з KeepPrice, його похибка обмежена розміром покупки, а не
// розміром пакета (domain.FundOp.KeepPrice).
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
// (api/handlers_npf.go), і це НЕ те саме: той розбирає рядок із форми, цей
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

// BadRequestf — помилка, яку handleWhatIf віддає як 400. Окремий тип, бо
// решта помилок фази — це поломки сховища, і плутати їх з опискою у формі
// означало б показати людині 500 там, де вона просто не дозаповнила поле.
type BadRequestError struct{ msg string }

func (e BadRequestError) Error() string { return e.msg }

func BadRequestf(format string, args ...any) error {
	return BadRequestError{msg: fmt.Sprintf(format, args...)}
}

// basketLine — один рядок плану, вже з ціною.
type basketLine struct {
	// ID — рядок у plan_buys; 0 означає чернетку, якої в базі ще немає.
	// Саме за ним UI чіпляє «змінити», «виконано» й «прибрати».
	ID       int64     `json:"id,omitempty"`
	Kind     string    `json:"kind"`
	Label    string    `json:"label"`
	Qty      int64     `json:"qty"`
	Unit     MoneyJSON `json:"unit"`
	Total    MoneyJSON `json:"total"`
	Currency string    `json:"currency"`
	// BuyDate — коли планую; порожньо = «зараз».
	//
	// Future каже, чи дата в НАСТУПНОМУ місяці або далі — саме місяць, а
	// не день: сітка симуляції календарно-місячна, і рядок цього місяця
	// їй нема куди покласти окремо від сьогоднішнього (state_plan_buys.go).
	//
	// Overdue — дата вже минула. Прострочений намір рахується як «зараз»
	// (гроші за ним досі не витрачені), тож у нього Future = false, як і в
	// рядка цього місяця, — і без окремого поля підпис у таблиці не
	// відрізнив би одне від одного. Порівнювати дати в браузері не можна:
	// його «сьогодні» й серверне — різні дати в різних поясах.
	BuyDate string `json:"buy_date,omitempty"`
	Future  bool   `json:"future,omitempty"`
	Overdue bool   `json:"overdue,omitempty"`
	// IsReserve — планований вклад є подушкою. У відповіді він потрібен не
	// заради значка: саме за ним картка наслідків вирішує, чи взагалі
	// малювати рядки подушки й драбини (рядок, який структурно не може
	// зрушити, гірший за його відсутність).
	IsReserve bool `json:"is_reserve,omitempty"`
	// Broker — у кого купуємо. Assumed каже, що брокера обрав застосунок,
	// а не людина: припущення, яке впливає на «вистачає / не вистачає»,
	// має бути видно, а не лежати мовчки в обчисленні.
	Broker  string `json:"broker"`
	Assumed bool   `json:"broker_assumed,omitempty"`
}

// BasketDoc — план купівель у грошах.
//
// НЕСТАЧІ ТУТ БІЛЬШЕ НЕМАЄ, і абзац лишається, щоб її не завели заново.
// Доти поруч із Totals стояли Shorts: скільки бракує кожному брокеру,
// пораховане проти СЬОГОДНІШНЬОГО залишку. Питання виявилось не тим.
// План купівель міряється ПЛАНОВИМИ грошима — тим, що надійде, — а не
// тим, що лежить на рахунку зараз: якщо на рахунку бракує, він
// поповниться з планових надходжень раніше, ніж покупка станеться. Тобто
// «у mono бракує 1 000» було тривогою про стан, який не настане.
//
// Половина цього доводу вже стояла в коді — і стосувалась лише далеких
// рядків («назвати нестачею те, що станеться після п'яти зарплат»). Вона
// просто не була поширена на найближчі.
//
// Сама арифметика жива й недоторкана: ShortfallMinor у cash_shortfall.go
// обслуговує форми запису (лот, вклад, поповнення, НПФ) і дату «коли
// вистачить» у ready_on.go. Там питання інше — «я записую платіж ЗАРАЗ»,
// — і сьогоднішній залишок відповідає на нього правильно.
type BasketDoc struct {
	Lines  []basketLine `json:"lines"`
	Totals []MoneyJSON  `json:"totals"` // разом по кожній валюті
}

// findFundRow — фонд у вже зібраному стані. Беремо звідти, а не з
// довідника, бо потрібна остання ЦІНА, а її знає саме зведення.
func findFundRow(doc *state.Doc, name string) *state.FundPositionRow {
	for i := range doc.Funds {
		if doc.Funds[i].Fund == name {
			return &doc.Funds[i]
		}
	}
	return nil
}

// pickBroker — у кого купуємо. Названого беремо як є; без назви —
// того, у кого найбільше грошей у цій валюті, і кажемо про це вголос.
//
// Рахунки роздільні: гривня на inzhur не купить папір у mono, тож
// «вистачає / не вистачає» без імені брокера відповіді не має.
func pickBroker(doc *state.Doc, cur, want string) (string, bool) {
	if want != "" {
		return want, false
	}
	best, bestAmt := "", -1.0
	for name, byCur := range doc.Brokers {
		if v := byCur[cur]; v.Major() > bestAmt {
			best, bestAmt = name, v.Major()
		}
	}
	if best == "" {
		return "—", true
	}
	return best, true
}

// addTopup — чим добрати решту грошей місяця, щоб частки вирівнялись.
//
// ЧОМУ ЦЕ ТУТ, А НЕ ОКРЕМИМ ЕНДПОЙНТОМ. Обидва входи є разом рівно в цьому
// місці й більше ніде: повний документ ПІСЛЯ плану (after) і сам план у
// грошах (basket). Окремий ендпойнт мусив би зібрати їх удруге — тобто
// вдруге розгорнути plan_buys і вдруге перебудувати стан, — і два
// перерахунки одного дня давали б два різні числа щоразу, коли між ними
// щось запишуть.
//
// ВЛАСНОЇ АРИФМЕТИКИ ТУТ ОДНЕ ВІДНІМАННЯ, і воно нижче. Усе решта —
// AllocatePlan, та сама чиста функція, що обслуговує розкладку надходження
// й ногу маршруту.
func (e *Engine) addTopup(ctx context.Context, now time.Time,
	after *state.Doc, basket BasketDoc, pickISIN string, out *whatIfPayload) error {

	if after.MonthPlan == nil || after.MonthPlan.LeftUAH.Major() <= 0 {
		return nil
	}
	rates, err := e.Rates(ctx)
	if err != nil {
		return err
	}
	// ВІДНІМАННЯ ТУТ БІЛЬШЕ НЕМАЄ, І ЦЕ ГОЛОВНЕ, ЩО ТРЕБА ЗНАТИ ПРО ЦЮ
	// ФУНКЦІЮ.
	//
	// Стояло `avail = LeftUAH − Σ(рядки кошика)`: LeftUAH міряв гроші,
	// ВНЕСЕНІ в портфель, а план купівель у ньому не був урахований —
	// намір, а не рух грошей, — тож без явного віднімання картка радила б
	// докупити рівно те, що вже заплановане.
	//
	// Відколи гіпотеза приносить гроші, якими план оплачений
	// (Hypothetical.topUps), синтетичне поповнення потрапляє в
	// MonthDepositedUAH, і LeftUAH зменшується САМ. Лишити віднімання
	// означало б відняти план ДВІЧІ — і картка мовчала б там, де гроші ще
	// є.
	//
	// Майбутні рядки й тут не рахуються: вони грошей не приносять, тож і
	// LeftUAH не чіпають (state_plan_buys.go, гілка при topUps).
	//
	// Через це `basket` лишається в підписі, хоч більше не читається: воно
	// й далі описує ті самі гроші, і наступний автор, шукаючи «де ж тут
	// віднімання», мусить знайти цей абзац, а не порожній параметр.
	avail := after.MonthPlan.LeftUAH
	out.TopupPlanUAH = Round2(planCostUAH(basket, rates) + avail.Major())
	out.TopupLeftUAH = Round2(math.Max(0, avail.Major()))
	// Поріг той самий, що в розкладки: сума, з якої не вийде жодного руху,
	// не варта картки. Нуль і від'ємне значення сюди ж — план купівель
	// може бути й більшим за те, що місяць обіцяє.
	if avail.Major() < allocMinCutUAH {
		return nil
	}
	// ПОРАДИ ВІД `after`, А НЕ ВІД `before`. Рейтинг ранжує сумою розривів
	// (suggPlanScore), і розриви мусять бути ті, що лишились ПІСЛЯ плану:
	// інакше вершиною стане саме той вид, який план уже закрив.
	sug, err := e.ReinvestSuggestions(ctx, now, after)
	if err != nil {
		return err
	}
	// Вибір перевіряється ТІЄЮ САМОЮ PickSuggestion, що й у розкладці, і
	// над порадами від after: невідомий ISIN мусить дати одну й ту саму
	// відмову з обох екранів, інакше два різні тексти на один папір
	// читались би як дві різні причини.
	pick, err := PickSuggestion(sug, pickISIN)
	if err != nil {
		return err
	}
	// БЕЗ ОБМЕЖЕНЬ ЗА ДЖЕРЕЛОМ, і це не недогляд. Розкладають не одне
	// надходження, а зведений залишок місяця — десяток потоків із різними
	// дозволами (plan_flows.uses), — і одне слово «чиї це гроші» на нього
	// було б неправдою для половини суми. Той самий довід, що при
	// ReserveEligibleUAH, лише з протилежним висновком: там сума одна й
	// дозвіл у неї один, тут сум багато.
	plan := AllocatePlan(after, sug, rates,
		ToMoneyJSON(money.New(int64(math.Round(avail.Major()*100)), money.UAH)), avail.Major(),
		AllocAllow{ReserveUAH: avail.Major(), GoalsUAH: avail.Major(), PickISIN: pick},
		money.UAH, e.NPFIDByName(ctx))
	out.Topup = &plan
	return nil
}

// planCostUAH — скільки коштують рядки «зараз», грн-екв.
//
// Потрібне ЛИШЕ шапці картки: вона показує три числа — скільки місяць
// обіцяв, скільки з того вже розписано планом, скільки лишилось, — і без
// середнього результат віднімання стояв би без самого віднімання.
//
// Саме віднімання при цьому робить уже не картка: гіпотеза приносить гроші
// плану, тож LeftUAH зменшується сам (довід — при avail вище). Тут лише
// відновлюється те, що місяць обіцяв ДО плану: залишок плюс його вартість.
func planCostUAH(basket BasketDoc, rates fx.Rates) float64 {
	out := 0.0
	for _, l := range basket.Lines {
		if l.Future {
			continue
		}
		out += moneyAmount(l.Total) * allocRate(l.Currency, rates)
	}
	return out
}

// whatIf — стан портфеля ПІСЛЯ рядків плану купівель rows і добір решти
// грошей місяця (POST /api/whatif). Помилка в самих рядках чи невідомий
// pickISIN — BadRequestError.
func (e *Engine) WhatIf(ctx context.Context, now time.Time, rows []store.PlanBuy, pickISIN string) (whatIfPayload, error) {
	today := domain.NewDate(now)
	// Стан ДО — потрібен, щоб знати, у кого скільки грошей, за якою ціною
	// йде сертифікат і кого обрати брокером, коли його не назвали.
	before, err := e.BuildState(ctx, now)
	if err != nil {
		return whatIfPayload{}, err
	}
	exp, err := e.expandPlanBuys(ctx, before, today, rows)
	if err != nil {
		return whatIfPayload{}, err
	}
	basket := exp.basket

	after, err := e.BuildStateWith(ctx, now, exp.what)
	if err != nil {
		return whatIfPayload{}, err
	}
	out := whatIfPayload{After: after, Basket: basket}
	if err := e.addTopup(ctx, now, after, basket, pickISIN, &out); err != nil {
		// Невідомий папір — помилка ЗАПИТУ, а не збій: людина назвала ISIN,
		// якого немає серед порад. П'ятисотка тут читалась би як поломка
		// застосунку, і сторінка не змогла б показати причину дослівно —
		// а причина в тому й полягає, щоб її прочитали.
		return whatIfPayload{}, err
	}
	return out, nil
}

type whatIfPayload struct {
	After  *state.Doc `json:"after"`
	Basket BasketDoc  `json:"basket"`
	// Topup — чим добрати РЕШТУ грошей місяця, щоб частки вирівнялись.
	//
	// Тим самим типом, що розкладка надходження (allocPlan), і тією ж
	// функцією: питання одне — «ось сума, розклади її цілими квитками», —
	// і друга відповідь на нього розійшлася б із першою. Різниця лише в
	// тому, ЯКА це сума й ВІД ЯКОГО портфеля міряються частки.
	//
	// nil означає «розкладати нема чого»: плану доходу немає, або місяць
	// уже закритий, або весь залишок розписаний планом купівель. Порожня
	// розкладка нуля читалась би як поломка, тому нуля тут не буває.
	Topup *allocPlan `json:"topup,omitempty"`
	// TopupPlanUAH / TopupLeftUAH — два числа, з яких вийшла сума Topup:
	// скільки план місяця ще обіцяє і скільки з того вже розписав план
	// купівель. Без них картка показала б результат віднімання, не
	// показавши самого віднімання, — а питання «чому пропонують так мало»
	// виникає рівно на ньому.
	TopupPlanUAH float64 `json:"topup_plan_uah,omitempty"`
	TopupLeftUAH float64 `json:"topup_left_uah,omitempty"`
}
