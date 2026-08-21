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
// Тому майбутній рядок іде не в портфель, а в ПЛАН: замок plan_actions
// для трьох видів і потік plan_flows у пенсійний для четвертого.
//
// Чому саме ці два механізми, а не третій. Обидва вже існують і вже
// вміють рівно те, що треба: stepSleeve переносить суму замка з
// ліквідного боку в замкнений, нічого не створюючи (domain/projection.go),
// а потік із Dest="npf:<id>" веде ОБИДВІ половини руху — мінус на
// ліквідному боці й плюс у накопичувальну позицію з прапорцем Locked
// (state_projection.go). Замок для НПФ не годиться: він поверне тіло
// цілком одним місяцем і втратить Locked, тобто декумуляція почала б
// витрачати пенсійні гроші — про що прямо попереджає шапка accum.go.
//
// ОДИН РЯДОК — ОДИН КАНАЛ. Розгалуження нижче рівно одне (isFuture), і
// це головний захист від подвійного рахунку: рядок не може одночасно
// лежати в портфелі й стояти в плані.
//
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
// (ПДФО 18% + військовий збір 1.5%). Те саме число, що підставляє форма
// відкриття вкладу: планована й справжня однакова угода не має права
// давати два різні графіки.
const defaultDepositTaxBP = 1950

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
	// spend — "брокер|валюта" → мінорні, ЛИШЕ по сьогоднішніх рядках.
	// Майбутніх тут немає навмисно: сьогоднішній залишок відповідає лише на
	// сьогоднішнє питання, і сказати «у mono бракує 40 000» про покупку в
	// березні означало б назвати нестачею те, що станеться після п'яти
	// зарплат.
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
		future := row.BuyDate != "" && row.BuyDate.After(today)
		// Дата покупки для сьогоднішнього рядка — СЬОГОДНІ, навіть якщо в
		// ньому стоїть учорашня. Прострочений намір — це намір, гроші за
		// ним досі не витрачені, і датувати гіпотетичний лот минулим
		// означало б дописати портфелю історію, якої не було.
		when := today
		if future {
			when = row.BuyDate
		}

		line := basketLine{Kind: row.Kind, Qty: row.Qty, ID: row.ID,
			BuyDate: string(row.BuyDate), Future: future, IsReserve: row.IsReserve}
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
				rateBP, months, rerr := fundLockTerms(ref, when)
				if rerr != nil {
					return out, rerr
				}
				act := lockAction(row, when, total, cur, rateBP, months, "план: "+row.Ref)
				emit = func(string) { out.what.actions = append(out.what.actions, act) }
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
			if future {
				if rateBP <= 0 {
					return out, badRequestf(
						"вклад у %s: ставки немає ні в рядку, ні в налаштуваннях — без неї замок у прогнозі лише заморозить гроші",
						cur)
				}
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
	// КОМПРОМІС, який варто знати. Накопичувальний фонд у моделі — це
	// Accum зі складним відсотком усередині; замок натомість платить
	// щомісячний купон, який далі реінвестується за ставкою рукава, і
	// кладе тіло туди, звідки декумуляція його не продасть. Обидві похибки
	// консервативні — прогноз занижує капітал і занижує запас у просадці, —
	// а чесна альтернатива (Accum зі стартовим місяцем) означала б зміну
	// домену за межею sleeve-state заради одного рядка плану.
	return bpFromPct(pct), months, nil
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
