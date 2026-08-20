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
			months := float64(domain.DaysBetween(first, today))/30.44 + 1
			if months < 1 {
				months = 1
			}
			out.ActualMonths = int(months + 0.5)
			out.ActualMonthlyUAH = round2(float64(totalUAH) / 100 / months)
		}
	}

	out.Plan = buildMonthPlan(src, rates, today, reserveUAH,
		float64(out.DepositedUAH.Amount())/100)
	return out, nil
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
// Місяць 0 — саме поточний: monthKeyAt(today, 0) дає його ключ, а
// planFlowAtMonth для m <= 0 іде в гілку минулого, де дата початку НЕ
// підтягується до першого місяця. Для поточного це правильно: потік,
// заведений завтра, у серпні ще не платив.
func buildMonthPlan(src *sources, rates fx.Rates, today domain.Date,
	reserveUAH, depositedUAH float64) *state.MonthPlan {
	if len(src.planFlows) == 0 && len(src.planReceipts) == 0 {
		return nil // плану доходу немає — це не «план обіцяє нуль»
	}
	month := monthKeyAt(today, 0)
	marks := newPlanMarks(src.planReceipts)
	out := &state.MonthPlan{Month: month}

	for _, f := range src.planFlows {
		// Чи платить потік цього місяця, вирішує ЧИСТИЙ план (marks = nil), а
		// не сума з відмітками. Різниця видна на відмітці «не прийшло»: вона
		// робить суму нулем, і за нею рядок зник би зі списку джерел — тобто
		// «зарплати цього місяця не було» перестало б відрізнятись від
		// «зарплати тут ніколи й не планувалось».
		if planFlowAtMonth(f, today, 0, nil) == 0 {
			continue
		}
		amt := planFlowUAH(planFlowAtMonth(f, today, 0, marks), f.Currency, rates)
		if f.Kind == "expense" {
			// У потоках витрата від'ємна; у контракті вона додатна, бо поле
			// зветься «витрати», і знак у ньому читався б як помилка.
			out.ExpenseUAH += -amt
			continue
		}
		out.IncomeUAH += amt
		out.Sources++
		if _, ok := marks.at(f.ID, today, 0); ok {
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
		gross := float64(r.Amount) / 100 * float64(r.InvestBP) / 10000
		out.ExtraUAH += planFlowUAH(gross, r.Currency, rates)
	}

	out.PlanUAH = out.IncomeUAH + out.ExtraUAH - out.ExpenseUAH

	// Стеля резерву — та сама, що керує Reserve.FillNowUAH, але прикладена до
	// НОВИХ грошей місяця, а не до готівки на рахунках. Обидва числа
	// лишаються, бо відповідають на різні питання: те — «відклади з того, що
	// вже лежить», це — «з місячних грошей у подушку піде стільки».
	//
	// Обрізання розривом обов'язкове з тієї ж причини, що й там: радити
	// відкласти БІЛЬШЕ за ціль означало б завести другу ціль поруч із тією,
	// яку задано в місяцях витрат.
	if _, gap := state.ReserveTarget(src.settings, reserveUAH); gap > 0 &&
		src.settings != nil && src.settings.ReserveFillSharePct != nil {
		if share := *src.settings.ReserveFillSharePct; share > 0 && out.PlanUAH > 0 {
			fill := out.PlanUAH * share / 100
			if fill > gap {
				fill = gap
			}
			out.ReserveUAH = round2(fill)
		}
	}

	// Лишилось закинути — проти ВНЕСЕНОГО, а не проти купленого: план
	// означає «скільки нових грошей принести», а купівля лише переносить їх
	// з рахунку в папери (та сама межа, що названа в шапці цього файла).
	if left := out.PlanUAH - depositedUAH; left > 0 {
		out.LeftUAH = round2(left)
	}

	out.IncomeUAH = round2(out.IncomeUAH)
	out.ExpenseUAH = round2(out.ExpenseUAH)
	out.ExtraUAH = round2(out.ExtraUAH)
	out.PlanUAH = round2(out.PlanUAH)
	out.ReceivedUAH = round2(out.ReceivedUAH)
	return out
}
