// Поточний місяць і фактичний темп поповнень.
//
// Сьома фаза розбиття buildState. Тут три різні відповіді на схоже
// питання «скільки я вклав», і плутати їх не можна:
//
//   - ВКЛАДЕНО цього місяця — покупки: облігації й сертифікати. Це рух
//     грошей із рахунку в папери.
//   - ВНЕСЕНО цього місяця — поповнення, нетто зі зняттями. Це НОВІ
//     гроші, яких у портфелі не було.
//   - ТЕМП — скільки нових грошей заходить на місяць у середньому за
//     останні півроку.
//
// План міряється ВНЕСЕНИМ, а не вкладеним, і це не дрібниця. План означає
// «скільки нових грошей треба принести до цілі»; купівля ж лише переносить
// гроші з рахунку в папери й до цілі не додає нічого. Порівнювати план із
// купівлями означало б показувати 100% виконання за папір, куплений на
// накопичені купони.
package api

import (
	"math"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"

	money "github.com/Rhymond/go-money"
)

// actualWindowDays — вікно, за яким міряється темп: останні півроку, а не
// вся історія.
//
// Усереднення за весь час міряє не темп, а біографію: якщо портфель колись
// виходив у нуль і починався заново, внески «до» і виведення «під час»
// гасять одне одного. На реальних даних 29 місяців історії з повним
// виходом посередині дали 0% від потрібного при живих внесках — сьогоднішні
// 7 500 ₴/міс виглядали як 430.
//
// Півроку — компроміс: досить довго, щоб пропущений місяць не обвалив
// оцінку, і досить коротко, щоб показник відповідав на «як я вкладаю
// ЗАРАЗ», а саме це питання йому й ставлять.
const actualWindowDays = 183

// monthPhase — рухи поточного місяця й темп поповнень.
type monthPhase struct {
	// InvestedUAH — куплено цього місяця (папери + сертифікати), грн-екв.
	InvestedUAH *money.Money
	// DepositedUAH — внесено НЕТТО (поповнення мінус зняття);
	// WithdrawnUAH — самі зняття, додатнім числом.
	DepositedUAH *money.Money
	WithdrawnUAH *money.Money
	// ActualMonthlyUAH — темп нових грошей, ₴/міс; ActualMonths — на якій
	// довжині історії він порахований (щоб було видно, наскільки вірити).
	ActualMonthlyUAH float64
	ActualMonths     int
	// Plan — що план доходу обіцяє САМЕ цього місяця. nil = плану немає.
	Plan *state.MonthPlan
	// Резерв цього місяця. ReserveMovedUAH — скільки вже покладено під
	// матрац (нетто, грн-екв.); ReserveMonthUAH — місячна частка подушки
	// (стеля від нових грошей, обрізана розривом); ReserveFillUAH — скільки
	// з неї ще лишилось відкласти.
	//
	// Рахується ТУТ, а не в deriveReserve, попри те, що живе воно в картці
	// резерву: споживачів двоє, і другий — поділ грошей місяця по видах
	// (spreadMonth у buildState), який ділить уже ПІСЛЯ подушки. Він
	// стоїть після Derive заради стелі цілей, але сама подушка потрібна
	// раніше — і мати два місця, де вона рахується, не можна.
	ReserveMovedUAH float64
	ReserveMonthUAH float64
	ReserveFillUAH  float64
}

// buildMonth зводить рухи місяця, темп і план поточного місяця.
func buildMonth(src *sources, hold domain.Holdings, rates fx.Rates,
	now time.Time, today domain.Date, reserveUAH float64) (monthPhase, error) {
	out := monthPhase{
		InvestedUAH:  money.New(0, money.UAH),
		DepositedUAH: money.New(0, money.UAH),
		WithdrawnUAH: money.New(0, money.UAH),
	}

	for _, l := range hold.Lots {
		// Уся куплена кількість, а не залишок: питання «скільки я вклав
		// цього місяця», і продаж наступного дня факту покупки не скасовує.
		if l.BuyDate.Year() != now.Year() || l.BuyDate.Month() != now.Month() {
			continue
		}
		cost, err := domain.LotCost(l.Lot)
		if err != nil {
			return out, err
		}
		uahAmt, err := fx.ToUAH(cost, rates)
		if err != nil {
			return out, err
		}
		sum, err := out.InvestedUAH.Add(uahAmt)
		if err != nil {
			return out, err
		}
		out.InvestedUAH = sum
	}
	// Сертифікати фондів — теж купівля паперів, тож у «вкладено цього
	// місяця» вони входять нарівні з облігаціями. Досі не входили лише
	// тому, що фонди прибудовувались до моделі пізніше.
	for _, op := range src.fundOps {
		if op.Kind != domain.FundBuy ||
			op.Date.Year() != now.Year() || op.Date.Month() != now.Month() {
			continue
		}
		if u, cerr := fx.ToUAH(money.New(op.Amount, op.Currency), rates); cerr == nil {
			if sum, aerr := out.InvestedUAH.Add(u); aerr == nil {
				out.InvestedUAH = sum
			}
		}
	}

	// Внесено — нетто, а не сума поповнень: зняття зменшує капітал так
	// само, як поповнення його збільшує. Без цього переказ між брокерами
	// (він записується як зняття + поповнення, бо окремої сутності переказу
	// немає) роздував би «внесено» на свою суму, не додавши жодної нової
	// копійки.
	addMove := func(amount int64, cur string) {
		if amount < 0 {
			if u, cerr := fx.ToUAH(money.New(-amount, cur), rates); cerr == nil {
				if sum, aerr := out.WithdrawnUAH.Add(u); aerr == nil {
					out.WithdrawnUAH = sum
				}
			}
		}
		if u, cerr := fx.ToUAH(money.New(amount, cur), rates); cerr == nil {
			if sum, aerr := out.DepositedUAH.Add(u); aerr == nil {
				out.DepositedUAH = sum
			}
		}
	}
	for _, d := range src.deposits {
		if d.Date.Year() != now.Year() || int(d.Date.Month()) != int(now.Month()) {
			continue
		}
		addMove(d.Amount, d.Currency)
	}
	// Резерв рахується в тому самому нетто, і саме тому, що переміщення
	// гаманець → матрац записується ДВОМА ногами (мінус у deposits, плюс
	// тут): порізно перша нога виглядала б як втрата капіталу, а разом
	// вони дають нуль, як і має бути. Відкладені зовні гроші, які на
	// рахунок брокера не заходили, це й далі чесний внесок.
	for _, op := range src.reserveOps {
		if op.Date.Year() != now.Year() || int(op.Date.Month()) != int(now.Month()) {
			continue
		}
		addMove(op.Amount, op.Currency)
		// Окремо від addMove: там питання «чи побільшало капіталу», а тут
		// «скільки цього місяця вже пішло під матрац». Друге не залежить від
		// того, звідки взялись гроші, — саме тому воно й рахується сумою
		// операцій резерву, а не різницею балансів.
		if u, cerr := fx.ToUAH(money.New(op.Amount, op.Currency), rates); cerr == nil {
			out.ReserveMovedUAH += float64(u.Amount()) / 100
		}
	}
	out.ReserveMovedUAH = round2(out.ReserveMovedUAH)

	// Рухи ЦІЛЕЙ — у той самий нетто, і з того самого доводу, що резерв.
	// Переміщення гаманець → ціль записується двома ногами (мінус у
	// deposits, плюс у goal_ops); порізно перша нога виглядала б як втрата
	// капіталу, а разом вони дають нуль, як і має бути.
	//
	// Це найлегше проґавити місце в усій сутності: без цього циклу
	// відкладання на авто псувало б місячний прогрес, фактичний темп і
	// бенчмарк — усе одразу й тихо.
	//
	// «Скільки цього місяця пішло в цілі» тут НЕ рахується: воно вже
	// пораховане в buildGoals разом із рештою проходу по журналу, і другий
	// лічильник розійшовся б із першим (той самий випадок, що з
	// ReserveMovedUAH вище, лише вирішений на користь одного проходу).
	for _, op := range src.goalOps {
		if op.Date.Year() != now.Year() || int(op.Date.Month()) != int(now.Month()) {
			continue
		}
		addMove(op.Amount, op.Currency)
	}

	// --- фактичний темп поповнень ---
	// Саме поповнень, а не покупок: покупка лише переносить гроші з рахунку
	// в папери й нового капіталу не додає (а купони враховані окремо).
	//
	// Знаменник — це +1 місяць до проміжку «перше поповнення … сьогодні», і
	// це не косметика. Поповнення фінансують ПЕРІОДИ, а не проміжок між
	// собою: три щомісячні внески покривають три місяці, тоді як від
	// першого до сьогодні минуло лише два. Ділення на проміжок завищувало
	// темп у півтора раза (15 000 за 60 днів давали 7 610 ₴/міс замість
	// 5 000). Та сама поправка знімає й вибух на старті: одне поповнення
	// сьогодні дає знаменник 1, а не 0.1, тож окремий поріг не потрібен.
	if len(src.deposits) > 0 {
		first := today
		var totalUAH int64
		for _, d := range src.deposits {
			if n := domain.DaysBetween(d.Date, today); n < 0 || n > actualWindowDays {
				continue
			}
			if d.Date.Before(first) {
				first = d.Date
			}
			// Нетто: зняття теж рух капіталу. Інакше переказ між брокерами
			// (зняття + поповнення) завищував би темп на свою суму, а
			// прогноз «За фактом» через це малював би дисципліну, якої немає.
			if u, cerr := fx.ToUAH(money.New(d.Amount, d.Currency), rates); cerr == nil {
				totalUAH += u.Amount()
			}
		}
		if totalUAH > 0 {
			months := paceMonths(first, today)
			out.ActualMonths = int(months + 0.5)
			out.ActualMonthlyUAH = round2(float64(totalUAH) / 100 / months)
		}
	}

	out.Plan = buildMonthPlan(src, rates, today, 0, float64(out.DepositedUAH.Amount())/100, "")
	out.ReserveMonthUAH, out.ReserveFillUAH = reserveMonthShare(
		src.settings, reserveUAH, out.Plan, out.ReserveMovedUAH,
		debtCapsReserve(src.debts, src.debtMarks, src.debtOps, src.deval, today),
		debtCoverUAH(src.debts, src.debtMarks, src.debtOps, rates, today))
	return out, nil
}

// paceMonths — знаменник фактичного темпу: скільки МІСЯЦІВ фінансує
// проміжок «перший рух у вікні … сьогодні».
//
// +1 місяць, і це не косметика. Внески фінансують ПЕРІОДИ, а не проміжки
// між собою: три щомісячні внески покривають три місяці, тоді як від
// першого до сьогодні минуло лише два. Ділення на голий проміжок завищувало
// темп у півтора раза (15 000 за 60 днів давали 7 610 ₴/міс замість 5 000).
// Та сама поправка знімає вибух на старті: один рух сьогодні дає знаменник
// 1, а не 0.1, тож окремий поріг не потрібен.
//
// Спільна на двох читачів — темп поповнень портфеля вище й темп кожної
// цілі накопичення (state_goals.go). Друге означення «як я відкладаю зараз»
// розійшлося б із першим, і сторінка цілі почала б хвалити або лаяти за
// дисципліну інакше, ніж сторінка портфеля.
func paceMonths(first, today domain.Date) float64 {
	m := float64(domain.DaysBetween(first, today))/30.44 + 1
	if m < 1 {
		return 1
	}
	return m
}

// reserveMonthShare — скільки з грошей місяця належить подушці й скільки з
// того ще лишилось відкласти.
//
// # ЧОМУ БАЗА — НОВІ ГРОШІ, А НЕ ГОТІВКА НА РАХУНКАХ
//
// Доти стеля рахувалась від Capital.AccountUAH, і на живих даних це давало
// пораду «спершу поповнити резерв — 2,48 ₴» при розриві в 359 500 ₴: на
// брокерському рахунку лежало 6,19 ₴. Готівка там — стан однієї миті, а не
// потік: учора це була зарплата, сьогодні вона вже в папері, і подушка від
// цього не залежить ніяк. Наповнюють її з НОВИХ грошей, тож стеля й
// прикладається до них.
//
// # ЧОМУ ВІДНІМАЄТЬСЯ ВЖЕ ВІДКЛАДЕНЕ
//
// Без цього порада висіла б незмінною хоч би скільки ти відкладав: розрив
// зменшується повільно, а стеля від плану стала. Тепер записав рух у резерв
// — порада зменшилась рівно на цю суму, добрав місячну частку — зникла.
//
// Обрізаємо розривом ПЛЮС уже відкладеним, а не самим розривом: розрив уже
// не бачить того, що ти цього місяця поклав, і без поправки місячна частка
// сама себе з'їдала б — після переказу вона впала б на ту саму суму двічі.
func reserveMonthShare(set *state.SettingsDoc, reserveUAH float64,
	mp *state.MonthPlan, moved float64, debtCaps bool,
	coverUAH float64) (monthUAH, fillUAH float64) {
	if set == nil || set.ReserveFillSharePct == nil || mp == nil {
		return 0, 0
	}
	share := *set.ReserveFillSharePct
	// БАЗА — ДОЗВОЛЕНА ЧАСТИНА ПЛАНУ, а не весь план (0041). Доти стеля
	// міряла частку від усіх грошей місяця, а різати їх могла лише з
	// дозволених — тобто застосунок обіцяв подушці більше, ніж план їй
	// узагалі дозволяє, і різниця осідала в reserve_skip_why кожної
	// розкладки. Той самий довід, що привів сюди reserve_fill_from, лише
	// на рівні джерела замість рівня політики.
	if share <= 0 || mp.PlanReserveUAH <= 0 {
		return 0, 0
	}
	_, gap := state.ReserveTarget(set, reserveUAH, debtCaps, coverUAH)
	room := gap + moved
	if room <= 0 {
		return 0, 0 // ціль зібрана — стеля мовчить, і правильно робить
	}
	monthUAH = mp.PlanReserveUAH * share / 100
	if monthUAH > room {
		monthUAH = room
	}
	if fillUAH = monthUAH - moved; fillUAH < 0 {
		fillUAH = 0
	}
	return round2(monthUAH), round2(fillUAH)
}

// buildMonthPlan — скільки план доходу заводить у портфель ЦЬОГО місяця.
//
// # ЧОМУ ЦЕ НЕ PlanProvidesUAH
//
// Те число — СЕРЕДНЄ за дванадцять місяців НАПЕРЕД, і поточний місяць у нього
// не входить узагалі: вектор проєкції починається з місяця 1. Тобто на
// питання «скільки мені закинути в серпні» воно не відповідає ніяк — разова
// премія у вересні його підіймає, а зарплата, яка прийшла сімнадцятого
// серпня, на нього не впливає. Питання ставлять щомісяця, і відповіді в
// документі не було.
//
// # ВЛАСНОЇ АРИФМЕТИКИ ТУТ НЕМАЄ
//
// Періодичність, дата «до», індексація, частка в портфель і підстановка
// відмітки — усе це живе в planFlowAtMonth, тобто в тому самому ядрі, з якого
// рахуються проєкція, профіль надходжень і колонка «дає ₴/міс». Друге
// означення «скільки цей потік платить у серпні» розійшлося б із першим на
// першій же правці періодичності, і помітили б це не одразу.
//
// # ЗСУВ МІСЯЦЯ
//
// m — на скільки місяців уперед від сьогодні. Нуль — поточний місяць, і
// саме його бере buildMonth: monthKeyAt(today, 0) дає його ключ, а
// planFlowAtMonth для m <= 0 іде в гілку минулого, де дата початку НЕ
// підтягується до першого місяця. Для поточного це правильно: потік,
// заведений завтра, у серпні ще не платив.
//
// Другий читач — маршрут (route.go): щомісячна стеля подушки міряється від
// плану СВОГО місяця, і без зсуву прохід уперед мусив би завести друге
// означення «скільки план дає в березні». Параметр узагальнено рівно тому,
// що читачів справді два, а не про запас.
//
// # ДОЗВОЛИ: ТРИ ЧИСЛА ЗАМІСТЬ ОДНОГО
//
// PlanUAH каже, скільки план дає ВСЬОГО. Але подушка й цілі мають право не
// на всі ці гроші: потік уміє сказати про себе «цей дохід — лише на
// інвестиції» (plan_flows.uses, 0041). Тому поруч рахуються PlanReserveUAH
// і PlanGoalsUAH — ті самі гроші, звужені дозволом, — і саме від них
// міряються стелі наповнення.
//
// ВИТРАТИ ВІДНІМАЮТЬСЯ ПОВНІСТЮ З КОЖНОГО, а не пропорційно. Рознести
// комуналку між дозволеними й недозволеними доходами можна лише вигаданим
// правилом (порівну? пропорційно? з першої зарплати?) — рівно та відмова,
// що вже стоїть у шапці planAhead. Повне віднімання не вимагає вибирати:
// воно каже «витрати з'їдають спершу ті гроші, які подушці й так
// дозволені», консервативне в потрібний бік і за побудовою ніколи не дає
// більше за PlanUAH.
//
// Коли жоден потік нічого не забороняє, обидва числа дорівнюють PlanUAH —
// тобто для плану, набраного до 0041, не змінюється нічого.
//
// after — НЕПОРОЖНЄ лише для місяця звірки картки: тоді входять самі потоки,
// чий платіжний день СТРОГО ПІЗНІШИЙ за цю дату. Дохід, що прийшов на дату
// звірки або раніше, уже сидить у її балансі, і рахувати його ще раз
// означало б обіцяти ті самі гроші двічі. Фільтр живе тут, а не другим
// циклом у виході з ліміту, бо «чи платить потік цього місяця» мусить мати
// одне означення.
func buildMonthPlan(src *sources, rates fx.Rates, today domain.Date,
	m int, depositedUAH float64, after domain.Date) *state.MonthPlan {
	if len(src.planFlows) == 0 && len(src.planReceipts) == 0 {
		return nil // плану доходу немає — це не «план обіцяє нуль»
	}
	month := monthKeyAt(today, m)
	marks := newPlanMarks(src.planReceipts)
	out := &state.MonthPlan{Month: month}

	// Дозволені суми накопичуються ОКРЕМИМИ лічильниками в тому самому
	// циклі: другий прохід по тих самих потоках був би другим означенням
	// «скільки цей потік платить у серпні».
	incReserve, incGoals, incDebt := 0.0, 0.0, 0.0
	for _, f := range src.planFlows {
		// Валова копія — з часткою в портфель 100%. Той самий фокус, що в
		// planFlowGrossUAH, і потрібен він тут ДВІЧІ: для самого валового
		// числа й для охорони нижче.
		gross := f
		gross.InvestBP = 10000

		// Чи платить потік цього місяця, вирішує ЧИСТИЙ план (marks = nil), а
		// не сума з відмітками. Різниця видна на відмітці «не прийшло»: вона
		// робить суму нулем, і за нею рядок зник би зі списку джерел — тобто
		// «зарплати цього місяця не було» перестало б відрізнятись від
		// «зарплати тут ніколи й не планувалось».
		//
		// І рахується вона на ВАЛОВІЙ копії, а не на самому потоці. Питання
		// тут КАЛЕНДАРНЕ — «чи є виплата в цьому місяці», — а сума потоку
		// множиться на частку в портфель, тож нульова частка давала нуль і
		// читалась як «не платить». Потік зникав цілком: не лише з
		// income_uah (де його справді немає), а й із валового доходу та зі
		// списку джерел.
		//
		// Спіймано на бойових даних. Власник свідомо виставив 0% двом
		// найбільшим потокам («поки виходжу з ліміту, у портфель не йде
		// нічого»), і застосунок оголосив його дохід 48 970 ₴/міс замість
		// 191 500, а стелю витрат — відʼємною. Нульова частка означає
		// «нічого не інвестую», а не «нічого не отримую».
		if planFlowAtMonth(gross, today, m, nil) == 0 {
			continue
		}
		// Платіжний день — день from_date (єдине означення, receiptDueDate).
		// Спіймано власником 1 вересня: звірка того ж дня робила прожитим
		// увесь вересень, хоч три зарплати місяця ще були попереду.
		if after != "" && !domain.Date(receiptDueDate(month, f.FromDate.Day())).After(after) {
			continue
		}
		amt := planFlowUAH(planFlowAtMonth(f, today, m, marks), f.Currency, rates)
		if f.Kind == "expense" {
			// У потоках витрата від'ємна; у контракті вона додатна, бо поле
			// зветься «витрати», і знак у ньому читався б як помилка.
			out.ExpenseUAH += -amt
			continue
		}
		out.IncomeUAH += amt
		out.GrossUAH += planFlowUAH(planFlowAtMonth(gross, today, m, marks), f.Currency, rates)
		if domain.PlanUseAllowed(f.Uses, domain.UsePlanReserve) {
			incReserve += amt
		}
		if domain.PlanUseAllowed(f.Uses, domain.UsePlanGoals) {
			incGoals += amt
		}
		if domain.PlanUseAllowed(f.Uses, domain.UsePlanDebt) {
			incDebt += amt
		}
		out.Sources++
		if _, ok := marks.at(f.ID, today, m); ok {
			out.ReceivedUAH += amt
			out.Marked++
		}
	}

	// Позапланове — окремо, і не з примхи: у planMarks воно не входить
	// навмисно (немає потоку, який можна замістити), тож без цього циклу
	// премія просто зникла б із місяця, у якому вона прийшла.
	for _, r := range src.planReceipts {
		if r.FlowID != 0 || r.Month != month {
			continue
		}
		// Після звірки позапланове не рахується: записане надходження — це
		// гроші, які ВЖЕ прийшли, тобто вони в балансі звірки, а дати, за
		// якою можна було б відрізнити «до» від «після», у нього немає.
		if after != "" {
			continue
		}
		share := float64(r.Amount) / 100 * float64(r.InvestBP) / 10000
		v := planFlowUAH(share, r.Currency, rates)
		out.ExtraUAH += v
		out.GrossUAH += planFlowUAH(float64(r.Amount)/100, r.Currency, rates)
		// Позапланове читає ВЛАСНИЙ дозвіл, і лише воно: потоку за ним
		// немає, тож успадкувати нема від кого (та сама межа, що з InvestBP).
		if domain.PlanUseAllowed(r.Uses, domain.UsePlanReserve) {
			incReserve += v
		}
		if domain.PlanUseAllowed(r.Uses, domain.UsePlanGoals) {
			incGoals += v
		}
		if domain.PlanUseAllowed(r.Uses, domain.UsePlanDebt) {
			incDebt += v
		}
	}

	// Обовʼязкові платежі за боргами — така сама неминучість, як витрати,
	// тож віднімаються звідусіль і повністю (довід — при PlanReserveUAH).
	out.DebtDueUAH = debtDueForMonth(src, rates, today, m)
	spent := out.ExpenseUAH + out.DebtDueUAH

	out.PlanUAH = out.IncomeUAH + out.ExtraUAH - spent
	out.PlanReserveUAH = math.Max(0, incReserve-spent)
	out.PlanGoalsUAH = math.Max(0, incGoals-spent)
	out.PlanDebtUAH = math.Max(0, incDebt-spent)

	// Лишилось закинути — проти ВНЕСЕНОГО, а не проти купленого: план
	// означає «скільки нових грошей принести», а купівля лише переносить їх
	// з рахунку в папери (та сама межа, що названа в шапці цього файла).
	if left := out.PlanUAH - depositedUAH; left > 0 {
		out.LeftUAH = round2(left)
	}
	if out.PlanUAH > 0 {
		out.CoveredPct = round2(depositedUAH / out.PlanUAH * 100)
	}

	// Залишок — валовий мінус те, що дійшло до портфеля. Це і є «все інше»:
	// гроші, які нікуди не розподіляються, бо вони просто лишаються там,
	// куди прийшли, — на картці, і витрачаються з неї.
	//
	// Окремим числом, а не відніманням у голові: питання «скільки лишається
	// на життя» ставлять щомісяця, і два доданки поруч без різниці між ними
	// змушують рахувати очима.
	out.OnCardUAH = round2(math.Max(0, out.GrossUAH-out.IncomeUAH-out.ExtraUAH))
	out.IncomeUAH = round2(out.IncomeUAH)
	out.GrossUAH = round2(out.GrossUAH)
	out.ExpenseUAH = round2(out.ExpenseUAH)
	out.ExtraUAH = round2(out.ExtraUAH)
	out.PlanUAH = round2(out.PlanUAH)
	out.PlanReserveUAH = round2(out.PlanReserveUAH)
	out.PlanGoalsUAH = round2(out.PlanGoalsUAH)
	out.PlanDebtUAH = round2(out.PlanDebtUAH)
	out.DebtDueUAH = round2(out.DebtDueUAH)
	out.ReceivedUAH = round2(out.ReceivedUAH)
	return out
}

// debtDueForMonth — обовʼязкові платежі за боргами в місяці зі зсувом m.
//
// ЩО САМЕ ВВАЖАЄТЬСЯ ОБОВʼЯЗКОВИМ: частини розстрочок (тіло + комісія) і
// мінімалка на НЕПІЛЬГОВУ частину картки — готівку й перекази, на які
// відсоток нараховують з першого дня. Пільговий оборот сюди не входить, і
// це головна межа фази: довід — у полі MonthPlan.DebtDueUAH і в шапці
// міграції 0045.
//
// РОЗСТРОЧКА, ПРИВʼЯЗАНА ДО КАРТКИ, СЮДИ НЕ ВХОДИТЬ, і це виправлення,
// знайдене на бойових даних.
//
// Вона списується З КАРТКИ, тобто живе в побутовому контурі — там само, де
// сам пільговий оборот. Доти її платежі віднімались від ПОРТФЕЛЬНИХ
// грошей: на живих даних 8 606,70 ₴/міс уронили план місяця з 26 902 до
// 18 296, хоча з тих 9 000, що доходять до портфеля, ніхто цих розстрочок
// не платить. Ті самі платежі при цьому не віднімались там, де вони
// справді відбуваються, — у проході виходу з ліміту.
//
// Тепер вони цілком у картковому контурі (cardInstallmentsInMonth), а тут
// лишаються САМОСТІЙНІ розстрочки — ті, що платяться з інших грошей, — і
// мінімалка на непільгову частину картки.
func debtDueForMonth(src *sources, rates fx.Rates, today domain.Date, m int) float64 {
	if len(src.debts) == 0 {
		return 0
	}
	first := monthStart(today, m)
	last := monthStart(today, m+1).AddDays(-1)

	total := 0.0
	for _, d := range src.debts {
		if d.Closed() {
			continue
		}
		if !d.IsCard() && d.CardID != 0 {
			continue // карткова розстрочка — це картковий контур
		}
		balance := int64(0)
		if d.IsCard() {
			st := domain.CardState(d, src.debtMarks, src.debtOps, nil, today)
			balance = st.NonGrace
			if st.Debt > 0 && balance > st.Debt {
				balance = st.Debt
			}
			if balance <= 0 {
				continue
			}
		}
		for _, p := range domain.DebtSchedule(d, balance, first, last) {
			if u, err := fx.ToUAH(money.New(p.Amount, d.Currency), rates); err == nil {
				total += float64(u.Amount()) / 100
			}
		}
	}
	return total
}

// monthStart — перше число місяця зі зсувом m від сьогодні. Окремо від
// monthKeyAt, бо тому потрібен ключ "YYYY-MM", а тут — сама дата.
func monthStart(today domain.Date, m int) domain.Date {
	t := today.Time()
	return domain.NewDate(time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, m, 0))
}
