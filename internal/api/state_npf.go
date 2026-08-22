package api

import (
	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
)

// Фаза НПФ: пенсійні рахунки у стан.
//
// Головне правило файлу те саме, що в state_funds.go: усі суми рядка — у
// ГРН-ЕКВІВАЛЕНТІ, усі до одної. Виняток один і навмисний — Nav лишається
// НАТИВНОЮ. Це ціна однієї одиниці, і переводити її в гривню означало б
// показувати число, якого немає у виписці фонду.
//
// Чого тут немає й не буде, доки не зʼявиться графік виплат на пенсії:
// жодного CashflowItem. НПФ до 50 років не платить нічого, тож у календар,
// ціновий ризик і ризик перевкладення він не входить не через фільтр, а
// через відсутність потоку. Фільтри знадобляться тоді, коли потік буде.

// npfPhase — усе, що НПФ додає в документ.
type npfPhase struct {
	// Rows — рядки картки, суми в грн-екв.
	Rows []state.NPFPositionRow
	// TotalUAH — вартість пенсійних активів, CostUAH — сума внесків.
	// Пара, а не одне число: без другого лінія прибутку на кривій капіталу
	// завищується на весь баланс.
	TotalUAH float64
	CostUAH  float64
	// ExposureUAH — грн-екв. по валютах рахунків. На відміну від
	// сертифікатів, НПФ у чисельник валютних часток входить: одиниці
	// пенсійних активів — справжня експозиція, і замок на це не впливає.
	// Гроші, яких не можна забрати, знецінюються разом із гривнею так само.
	ExposureUAH map[string]float64
	// Accum — позиції для симуляції, по валютах і в НАТИВНІЙ валюті, усі з
	// Locked: продати їх не можна ні за яку ставку.
	Accum map[string][]domain.Accum
	// ContribDue — чи прострочено внесок цього місяця хоч десь.
	ContribDue bool
	// YieldRealPct — зведена реальна дохідність НПФ, зважена ВАРТІСТЮ
	// рахунку. Рядки її вже несуть кожен свою (RealPct нижче); тут та сама
	// величина одним числом на весь вид, щоб воронка НПФ могла показати
	// плитку, не перескладаючи зважування в JS.
	//
	// Зважуємо вартістю, а не внесками: питання «скільки заробляє те, що в
	// мене лежить», а лежить саме вартість. Внески — база прибутку, і на
	// них рахується GainUAH, а не ставка.
	YieldRealPct float64
	// YieldPct — НОМІНАЛЬНИЙ ДВІЙНИК тієї самої величини, з того самого
	// зважування й того самого циклу.
	//
	// Парою навмисно, і аргумент дослівно той, що у фондів: тримати їх
	// нарізно означає рано чи пізно зважити реальну з одного джерела поруч
	// із номінальною з іншого — і отримати реальну, вищу за номінальну.
	// Потрібен він відтоді, як НПФ увійшов у зведену дохідність: там
	// правило «реальна головна, номінальна дрібним поруч» діє для КОЖНОГО
	// доданка, інакше суміш нема з чого зібрати.
	YieldPct float64
	// YieldWeight — вартість рахунків, які в дохідність увійшли, грн-екв.
	YieldWeight float64
	// Basis — ЗВІДКИ ставка: «обіцяно фондом» чи виміряне зростання ЧВОПА.
	// «різні основи», коли рахунки міряні по-різному, — та сама відповідь,
	// що у фондів, і з тієї ж причини.
	Basis string
}

// buildNPF зводить рахунки в рядки картки.
//
// Порожній довідник дає порожню фазу — окремої перевірки «а чи є взагалі
// НПФ» тут немає, бо цикл просто не виконається.
func buildNPF(src *sources, rates fx.Rates, deval float64,
	today domain.Date) npfPhase {
	out := npfPhase{
		ExposureUAH: map[string]float64{},
		Accum:       map[string][]domain.Accum{},
	}
	// Накопичувачі зведеної ставки: сума «ставка × вартість» і сама вага.
	var realWeighted, nomWeighted, realWeight float64
	basisMixed := false
	positions := domain.NPFPositions(src.npfAccounts, src.npfOps)
	// Порядок — за довідником, який уже відсортований за назвою. Обхід мапи
	// позицій дав би документ, що змінюється між викликами без причини, а на
	// нього спирається TestBuildStateIsDeterministic.
	for _, acc := range src.npfAccounts {
		p := positions[acc.ID]
		if p == nil {
			continue
		}
		cur := p.Currency
		if cur == "" {
			cur = money.UAH
		}
		toUAH := func(minor int64) float64 {
			if u, err := fx.ToUAH(money.New(minor, cur), rates); err == nil {
				return round2(float64(u.Amount()) / 100)
			}
			return round2(float64(minor) / 100)
		}

		valueUAH, costUAH := toUAH(p.Value()), toUAH(p.Cost)
		out.TotalUAH += valueUAH
		out.CostUAH += costUAH
		out.ExposureUAH[cur] += valueUAH

		// Власна ставка: виміряне зростання ЧВОПА витісняє обіцянку, і
		// основа каже, котре з двох показано. Точки — обʼєднання ручних із
		// виведеними з внесків, бо тільки разом вони покривають і track
		// record фонду до мого входу, і свіжі дати після нього.
		navPoints := domain.NPFNavPoints(src.npfNav, src.npfOps, acc.ID)
		rate, basis := domain.NPFOwnRatePct(acc, navPoints, today)
		navReturn, measured := domain.NPFNavReturn(navPoints, today)

		// Реальна — після податку на виплату й після знецінення гривні.
		//
		// Податок береться як «дохід накопичується всередині й
		// оподатковується один раз, у кінці», тобто years > 0: саме так НПФ
		// і працює — до 50 років держава нічого не бере, бо нічого не
		// виплачено. Строк — до дати доступу; без неї податку модель не
		// малює, бо не знає, на скільки його розмазати.
		years := float64(domain.NPFAccessMonths(acc, today)) / 12
		net := rate
		if years > 0 {
			net = domain.NetOfTax(rate, float64(acc.IncomeTaxBP)/100, years)
		}
		realPct := round2(realYield(net/100, cur, deval) * 100)
		// Вагою йде ВАРТІСТЬ рахунку, і зважуємо тут, усередині циклу, а не
		// сумуємо готові рядки потім: знецінення торкається лише гривневих
		// рахунків, тож поділ уже змішаного числа занизив би валютні.
		realWeighted += realPct * valueUAH
		// Номінальний двійник — це net, тобто ставка ПІСЛЯ податку на
		// виплату, але ДО знецінення. Саме вона й є «скільки одиниць
		// додасться»: податок держава візьме напевно, а знецінення —
		// припущення з налаштувань.
		nomWeighted += net * valueUAH
		realWeight += valueUAH
		switch {
		case out.Basis == "":
			out.Basis = basis
		case basis != out.Basis:
			basisMixed = true
		}

		payoutM, _ := domain.NPFPayoutMonths(acc)
		contribDue := domain.NPFContribDue(acc, src.npfOps, today)
		if contribDue {
			out.ContribDue = true
		}

		row := state.NPFPositionRow{
			Name: acc.Name, Currency: cur,
			Units: p.UnitsMajor(), Nav: p.NavMajor(),
			NavDate:  string(p.NavDate),
			CostUAH:  costUAH,
			ValueUAH: valueUAH,
			GainUAH:  round2(valueUAH - costUAH),
			RealPct:  realPct, YieldBasis: basis,
			AccessDate: string(acc.AccessDate),
			ContribDay: acc.ContribDay, ContribDue: contribDue,
			Administrator: acc.Administrator,
			CreditEstUAH: npfCreditUAH(acc, src.npfOps, src.settings,
				today.Year()),
		}
		// Обидві дохідності в рядку, а не одна: пара «обіцяли / фактично» і
		// є головним, що картка показує. ExpectedPct стоїть навіть тоді, коли
		// виміряне його витіснило, — інакше звірка припущення була б
		// неможлива саме тоді, коли вона стала можливою.
		if measured {
			row.NavReturnPct = navReturn
		}
		if acc.ExpectedYieldBP > 0 {
			row.ExpectedPct = round2(domain.CompoundFromSimple(
				float64(acc.ExpectedYieldBP)/100, int(acc.YieldSimpleYears)))
		}
		out.Rows = append(out.Rows, row)

		// Симуляція. Накопичувальна позиція із замком: росте власною
		// ставкою, нічого не платить, на CloseM віддає тіло з доходом мінус
		// податок — і до того моменту її не можна ні продати, ні витратити.
		//
		// Cost0 — внески, а не сьогоднішня вартість: податок береться з
		// ДОХОДУ, і рахувати його від вартості означало б подарувати вже
		// набутий приріст.
		// Позиція входить у симуляцію навіть з НУЛЬОВОЮ вартістю, якщо в неї
		// планують вносити: рахунок, заведений сьогодні під внески з
		// наступного місяця, інакше не існував би для проєкції взагалі — а це
		// рівно той випадок, заради якого стадія 2 і робиться.
		if v := float64(p.Value()) / 100; v > 0 || acc.ContribDay > 0 {
			out.Accum[cur] = append(out.Accum[cur], domain.Accum{
				// Ключ за ID, а не за назвою: за ним фабрика рукавів чіпляє
				// планові внески, і перейменування рахунку не має їх
				// відчіплювати (див. domain.NPFPlanDest).
				Key:    domain.NPFPlanDest(acc.ID),
				Value0: v, Cost0: float64(p.Cost) / 100,
				RatePct: rate,
				CloseM:  domain.NPFAccessMonths(acc, today),
				TaxPct:  float64(acc.IncomeTaxBP) / 100,
				// ExitTaxPct не заповнюється навмисно: дострокового виходу в
				// НПФ не існує, тож ставки для нього немає. Locked і робить
				// це поле недосяжним — але лишати тут число означало б
				// натякнути, що вихід можливий за якусь ціну.
				Locked: true,
				// Виплата потоком, а не разово: за законом строк мінімум
				// десять років. Разова модель вивалювала весь капітал у
				// готівку одного місяця й далі реінвестувала ринковою
				// ставкою — тобто малювала гроші, яких не буде.
				PayoutM: payoutM,
			})
		}
	}
	if realWeight > 0 {
		out.YieldRealPct = round2(realWeighted / realWeight)
		out.YieldPct = round2(nomWeighted / realWeight)
		out.YieldWeight = realWeight
		if basisMixed {
			out.Basis = "різні основи"
		}
	}
	return out
}

// npfCreditUAH — оцінка податкової знижки за рік, грн.
//
// Порожній ПДФО за рік означає «не рахувати»: без стелі число перетворилось
// би на обіцянку держави, якої вона не давала. Саме це поле й є
// перемикачем — окремого прапорця немає навмисно (див. settings_registry).
//
// Ліміт внеску за місяць читається з налаштувань, бо щороку інший.
// Порожньо = ліміту не застосовувати; це чесніше за зашите число, яке з
// січня почало б занижувати або завищувати оцінку без жодного попередження.
func npfCreditUAH(acc domain.NPFAccount, ops []domain.NPFOp,
	set *state.SettingsDoc, year int) float64 {
	if set == nil || set.NPFCreditPDFOYearUAH == nil {
		return 0
	}
	pdfo := int64(*set.NPFCreditPDFOYearUAH * 100)
	if pdfo <= 0 {
		return 0
	}
	var capMonth int64
	if set.NPFCreditCapMonthUAH != nil {
		capMonth = int64(*set.NPFCreditCapMonthUAH * 100)
	}
	return round2(float64(domain.NPFCreditEstimate(acc, ops, year, capMonth, pdfo)) / 100)
}
