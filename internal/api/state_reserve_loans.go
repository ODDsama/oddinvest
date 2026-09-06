// Позики в самого себе — переклад журналу резерву в те, з чого картка
// подушки бачить борг, а ціль отримує надбавку (0057).
//
// ЧОМУ ОКРЕМИЙ ФАЙЛ, А НЕ РЯДОК У БУДІВНИКУ. Той самий поділ, що в драбини
// подушки: арифметика залишку живе в domain, тлумачення — у state, а тут
// лише те, чого жоден із них не має, — курс і «сьогодні». Резерв можна
// тримати в доларах, а ціль міряється в гривні.
package api

import (
	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"

	money "github.com/Rhymond/go-money"
)

// reserveLoans — ВІДКРИТІ позики з нарахованим відсотком, у гривні.
//
// Закриті не повертаються зовсім: картка показує обіцянки, а не історію, і
// рядок «повернуто торік» щомісяця відсував би від очей ту одну позику,
// яку справді треба закрити. Сам факт нікуди не дівається — рухи резерву
// лежать у журналі, як лежали.
//
// # ЯК ПОВЕРНЕННЯ ЗНАХОДЯТЬ СВОЮ ПОЗИКУ
//
// Явно — через reserve_ops.loan_id. А поповнення БЕЗ нього гасить
// найстарішу відкриту позику (FIFO), і це не зручність, а необхідність:
// «Розкласти N ₴» і ноги «Маршруту грошей» постять у /api/reserve, нічого
// не знаючи про позики, тож без цього правила борг не гасився б ніколи —
// подушка наповнювалась би, а обіцянка висіла б вічно.
//
// Прецедент FIFO той самий, що в domain.FundSales і RemainingInflows:
// коли черга є, вона хронологічна.
//
// # ЧОМУ ЧЕРГА РОЗДІЛЕНА ПО ВАЛЮТАХ
//
// Позика живе в валюті свого зняття, і повернення міряється тією самою
// міркою: узяв 200 доларів — винен 200 доларів, а не «стільки гривень,
// скільки вони коштували в червні». Гривневе поповнення долларову позику
// не гасить, і це не обмеження, а те, що людина й так робить руками —
// журнал резерву тримає залишки по валютах саме тому.
//
// Технічно це ще й єдиний спосіб не збрехати: суми в журналі мінорні й
// без валюти, тож 1 000 копійок, застосовані до позики в доларах, стали б
// десятьма доларами.
func reserveLoans(loans []store.ReserveLoan, ops []store.ReserveOp,
	today domain.Date, rates fx.Rates) []state.ReserveLoan {
	if len(loans) == 0 {
		return nil
	}
	byLoan := reserveRepays(loans, ops, today)
	var out []state.ReserveLoan
	for _, l := range loans {
		owed, interest := domain.ReserveLoanBalance(l.TakenAmount, l.RateBP,
			l.TakenDate, byLoan[l.ID], today)
		if owed <= 0 {
			continue
		}
		owedUAH, err := fx.ToUAH(money.New(owed, l.TakenCurrency), rates)
		if err != nil {
			continue
		}
		interestUAH, err := fx.ToUAH(money.New(interest, l.TakenCurrency), rates)
		if err != nil {
			continue
		}
		takenUAH, err := fx.ToUAH(money.New(l.TakenAmount, l.TakenCurrency), rates)
		if err != nil {
			continue
		}
		row := state.ReserveLoan{
			ID: l.ID, OpID: l.OpID, Date: string(l.TakenDate),
			TakenUAH: float64(takenUAH.Amount()) / 100,
			RatePct:  float64(l.RateBP) / 100,
			Days:     domain.DaysBetween(l.TakenDate, today),
			OwedUAH:  float64(owedUAH.Amount()) / 100,
			// Саме це число піднімає ціль. Тіло її не піднімає: подушка
			// вже впала на нього самим зняттям (довід — шапка 0057).
			InterestUAH: float64(interestUAH.Amount()) / 100,
			DueDate:     l.DueDate, Note: l.Note,
		}
		if l.TakenCurrency != money.UAH {
			row.Currency = l.TakenCurrency
			row.TakenNative = float64(l.TakenAmount) / 100
		}
		// Простроченою може бути лише позика з дедлайном: без дати людина
		// собі нічого не обіцяла, і фарбувати її червоним — вимагати того,
		// чого не було.
		if d := domain.Date(l.DueDate); d.Valid() && d.Before(today) {
			row.Overdue = true
		}
		out = append(out, row)
	}
	return out
}

// reserveRepays — які повернення належать якій позиці.
//
// ОКРЕМО ВІД reserveLoans, бо читачів двоє й вони питають різне: картка
// хоче гривню (там курси), журнал позик — власну валюту рядка. Спільним у
// них є саме розподіл, і другий його екземпляр розійшовся б із першим на
// першому ж поверненні без явної привʼязки.
func reserveRepays(loans []store.ReserveLoan, ops []store.ReserveOp,
	today domain.Date) map[int64][]domain.LoanRepay {
	// Повернення по позиках. Спершу явні, потім вільні гроші — інакше
	// FIFO забрало б поповнення, на яке вже вказує рядок.
	byLoan := map[int64][]domain.LoanRepay{}
	loanCur := map[int64]string{}
	for _, l := range loans {
		loanCur[l.ID] = l.TakenCurrency
	}
	free := map[string][]domain.LoanRepay{}
	for _, op := range ops {
		if op.Amount <= 0 {
			continue
		}
		r := domain.LoanRepay{Date: op.Date, Amount: op.Amount}
		if op.LoanID != 0 {
			// Привʼязка чужою валютою мовчки не спрацьовує: сума пішла б у
			// залишок як своя й збрехала б у стільки разів, у скільки
			// різняться курси. Рядок від цього не зникає — він лишається
			// звичайним поповненням подушки.
			if loanCur[op.LoanID] == op.Currency {
				byLoan[op.LoanID] = append(byLoan[op.LoanID], r)
				continue
			}
		}
		free[op.Currency] = append(free[op.Currency], r)
	}
	// Вільні поповнення розливаються по позиках у хронологічному порядку
	// узяття: ListReserveLoans уже віддає їх саме так.
	//
	// РОЗЛИВ ІДЕ ПО ЧЕРЗІ, А НЕ ПРОПОРЦІЙНО. Пропорційний поділ лишив би
	// відкритими ВСІ позики одразу — і жодна не закрилась би, доки не
	// покрито останню. Той самий довід, яким цілі накопичення беруть
	// стелю по черзі за пріоритетом, а не діляться нею.
	for _, l := range loans {
		q := free[l.TakenCurrency]
		if len(q) == 0 {
			continue
		}
		owed, _ := domain.ReserveLoanBalance(l.TakenAmount, l.RateBP, l.TakenDate,
			byLoan[l.ID], today)
		for owed > 0 && len(q) > 0 {
			take := q[0]
			// Поповнення, старіше за саму позику, її не гасить — і не
			// згодиться нікому далі: позики йдуть від найстарішої, тож
			// решта ще новіша. Тому воно ВИКИДАЄТЬСЯ з черги, а не
			// зупиняє її: інакше один давній рядок затуляв би собою всі
			// поповнення після нього, і борг не танув би ніколи.
			if take.Date.Before(l.TakenDate) {
				q = q[1:]
				continue
			}
			if take.Amount > owed {
				// Решта поповнення лишається в черзі наступним позикам:
				// одне поповнення може закрити дві.
				byLoan[l.ID] = append(byLoan[l.ID], domain.LoanRepay{Date: take.Date, Amount: owed})
				q[0].Amount = take.Amount - owed
				break
			}
			byLoan[l.ID] = append(byLoan[l.ID], take)
			owed -= take.Amount
			q = q[1:]
		}
		free[l.TakenCurrency] = q
	}

	return byLoan
}

// reserveOwedInterestUAH — надбавка до цілі подушки: сума НАРАХОВАНОГО
// відсотка за відкритими позиками.
//
// Окремою функцією, бо читачів троє (картка через deriveReserve, стеля
// місяця, прохід маршруту), а складати ту саму суму в кожному означало б
// завести три означення однієї надбавки.
func reserveOwedInterestUAH(loans []state.ReserveLoan) float64 {
	var sum float64
	for _, l := range loans {
		sum += l.InterestUAH
	}
	return sum
}
