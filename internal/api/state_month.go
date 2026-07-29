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
}

// buildMonth зводить рухи місяця й темп.
func buildMonth(src *sources, hold domain.Holdings, rates fx.Rates,
	now time.Time, today domain.Date) (monthPhase, error) {
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
		cost := domain.MulQty(l.PricePerBond, l.Qty)
		if l.Fee != nil && !l.Fee.IsZero() {
			c2, err := cost.Add(l.Fee)
			if err != nil {
				return out, err
			}
			cost = c2
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
	return out, nil
}
