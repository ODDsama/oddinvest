// Похідні поля документа стану.
//
// Одинадцята, остання фаза розбиття buildState — і єдина, де межа
// зсувається не всередині api, а МІЖ api і state.
//
// Доти між ними стояв state.Input на пʼятдесят полів, з яких тридцять
// були дзеркалом Doc: тридцять рядків виду `doc.X = in.X`. Тобто пакет
// state здебільшого переписував із однієї структури в іншу, а виглядало
// це як обчислення — і кожне нове поле документа коштувало трьох правок
// у трьох файлах.
//
// Тепер будівник заповнює Doc НАПРЯМУ, а Derive добудовує лише те, що
// справді виводиться з інших полів:
//
//   - вкладене й номінал у грн-екв. — із позицій;
//   - капітал і валютні частки — з переданого Capital;
//   - собівартість фондів — сума по рядках фондів;
//   - картка резерву — з резерву, налаштувань і капіталу;
//   - найближча виплата, календар, драбина, надходження місяця — з
//     календаря виплат;
//   - прогрес місяця — з внесеного й плану.
//
// Ціна рішення, чесно. Input працював ще й як чек-лист: рецензент бачив
// в одному літералі, що подано все. Тепер ця перевірка розсипана по
// фазах, і замість неї стоїть TestDocFieldsPopulated — рефлексійний обхід
// документа з вимогою ненульового. Він сильніший: ловить не лише «поле
// забули подати», а й «підключили не до того джерела, і там випадково
// нуль».
package state

import (
	"math"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
)

// DeriveInput — те, чого в документі немає, але без чого похідні не
// порахувати. Дванадцять полів замість пʼятдесяти.
type DeriveInput struct {
	Now time.Time
	// Positions — позиції ОВДП у нативних валютах; Rates — курси ×10⁴.
	Positions []domain.Position
	Rates     fx.Rates
	// Capital приходить ГОТОВИЙ від будівника, і тут не збирається.
	//
	// Доти збирався — і це був не другий екземпляр того самого, а друге
	// ВИЗНАЧЕННЯ. Числа розходились двічі: номінал брався з
	// domain.Positions, яка (до виправлення) не відкидала погашених
	// паперів, а по валютах сумувався ПОЗИЦІЯМИ, тоді як будівник
	// конвертував агрегат по валюті — інше банківське заокруглення. Тобто
	// плитка «Частка USD» і картка ребалансу могли знову розійтись, хоч
	// саме заради їх сходження Capital і заводився.
	Capital Capital
	// Cashflow — календар виплат, уже відсортований; Ladder — драбина
	// погашень у нативних валютах.
	Cashflow []domain.CashflowItem
	Ladder   []domain.LadderEntry
	// Внесене за місяць і план — ГРІШМИ, бо прогрес рахується з мінорних
	// одиниць, і перехід через float дав би інше округлення.
	MonthDeposited *money.Money
	MonthTarget    *money.Money
	// Резерв: те, чого в документі немає окремими полями (сам ReserveUAH
	// будівник кладе прямо в Doc, і Derive читає його звідти).
	ReserveByCur    map[string]float64
	ReservePlaces   map[string]float64
	ReserveLastMove string
	// Стеля поповнення резерву, порахована будівником (state_month.go):
	// ReserveFillMonthUAH — частка МІСЯЦЯ, ReserveFillNowUAH — скільки з неї
	// ще лишилось відкласти, ReserveMovedUAH — скільки вже покладено під
	// матрац цього місяця.
	//
	// Рахується там, а не тут, попри те, що живе в цій картці: ту саму
	// місячну частку потребує ще й ребаланс (він ділить гроші вже ПІСЛЯ
	// подушки), а він працює до Derive. Два обчислення розійшлись би.
	ReserveFillMonthUAH float64
	ReserveFillNowUAH   float64
	ReserveMovedUAH     float64
	// ReserveLiquidUAH — частина подушки, доступна СЬОГОДНІ: журнал без
	// резервних вкладів. Приходить окремим числом, бо doc.ReserveUAH це вже
	// сума обох джерел, а різницю між «є» і «є в руках» з неї не відновити.
	ReserveLiquidUAH float64
	// ReserveDeposits — резервні вклади, зведені до того, що потрібно
	// драбині. Приходять ГОТОВИМИ, а не як domain.Deposit: перевід у гривню
	// й у місяці — робота будівника (там курси й «сьогодні»), а тут лишається
	// сама арифметика покриття. Той самий поділ, що в ReservePlaces.
	ReserveDeposits []ReserveDeposit
	// TopN — скільки виплат показати в «найближчих» (0 = 5).
	TopN int
}

// Derive добудовує документ похідними полями.
func Derive(doc *Doc, in DeriveInput) error {
	doc.Schema = SchemaVersion
	doc.GeneratedAt = in.Now.UTC().Format(time.RFC3339)
	doc.Ladder = []LadderRow{}
	doc.TopPayments = []PaymentRow{}
	doc.Calendar = []PaymentRow{}

	var investedUAH, nominalUAH int64
	for _, p := range in.Positions {
		inv, err := fx.ToUAH(p.Invested, in.Rates)
		if err != nil {
			return err
		}
		nom, err := fx.ToUAH(p.Nominal, in.Rates)
		if err != nil {
			return err
		}
		investedUAH += inv.Amount()
		nominalUAH += nom.Amount()
	}
	doc.InvestedUAH = float64(investedUAH) / 100
	doc.NominalUAHEq = float64(nominalUAH) / 100

	doc.CapitalUAH = round2(in.Capital.TotalUAH())
	doc.USDSharePct = in.Capital.SharePct(money.USD)
	doc.EURSharePct = in.Capital.SharePct(money.EUR)
	doc.MonthProgressPct = domain.ProgressPct(in.MonthDeposited, in.MonthTarget)

	// Курси віддаємо як звичайні числа: у сховищі вони ×10⁴, але це
	// внутрішня одиниця, і тягнути її в контракт означало б змусити
	// кожного споживача ділити самотужки.
	if len(in.Rates) > 0 {
		doc.Rates = make(map[string]float64, len(in.Rates))
		for code, e4 := range in.Rates {
			if e4 > 0 {
				if v, ok := fx.RateMajor(code, in.Rates); ok {
					doc.Rates[code] = v
				}
			}
		}
	}

	// Собівартість фондів — ЄДИНА сума, і зводиться вона тут, із тих самих
	// позицій, що йдуть у документ. Складати її окремо десь іще означало б
	// завести другу відповідь на те саме питання.
	doc.FundsCostUAH = 0
	for _, f := range doc.Funds {
		doc.FundsCostUAH += f.CostBasis
	}
	doc.FundsCostUAH = round2(doc.FundsCostUAH)

	if doc.Accounts == nil {
		doc.Accounts = map[string]float64{}
	}
	if doc.ReinvestMin == nil {
		doc.ReinvestMin = map[string]float64{}
	}

	deriveReserve(doc, in)

	nowDate := domain.NewDate(in.Now)
	var monthIncoming int64
	for _, cf := range in.Cashflow {
		if cf.Date.Year() == in.Now.Year() && cf.Date.Month() == in.Now.Month() {
			uahAmt, err := fx.ToUAH(cf.Amount, in.Rates)
			if err != nil {
				return err
			}
			monthIncoming += uahAmt.Amount()
		}
	}
	doc.MonthIncomingUAH = float64(monthIncoming) / 100

	for _, cf := range in.Cashflow {
		if cf.Date.Before(nowDate) {
			continue
		}
		doc.NextPayment = &NextPayment{
			Date:     string(cf.Date),
			ISIN:     cf.ISIN,
			Type:     payTypeStr(cf.Type),
			Amount:   Major(cf.Amount),
			Currency: cf.Amount.Currency().Code,
			Label:    payLabel(cf.ISIN),
		}
		break
	}

	// Складаємо, а не присвоюємо. Доки в драбині були самі облігації, на
	// рік і валюту припадав рівно один запис, і різниці не було. Відколи
	// туди ж лягли вклади, їх стало два — і облігації, що гасяться того ж
	// року в тій самій валюті, зникали з рядка. Стовпчики над таблицею
	// весь цей час сумували чесно, тож числа розходились одне з одним.
	byYear := map[int]*LadderRow{}
	years := []int{}
	for _, le := range in.Ladder {
		row, ok := byYear[le.Year]
		if !ok {
			row = &LadderRow{Year: le.Year}
			byYear[le.Year] = row
			years = append(years, le.Year)
		}
		switch le.Currency {
		case money.UAH:
			row.UAH += float64(le.Nominal) / 100
		case money.USD:
			row.USD += float64(le.Nominal) / 100
		case money.EUR:
			row.EUR += float64(le.Nominal) / 100
		}
	}
	for _, y := range years {
		doc.Ladder = append(doc.Ladder, *byYear[y])
	}

	topN := in.TopN
	if topN <= 0 {
		topN = 5
	}
	for i, cf := range in.Cashflow {
		row := PaymentRow{
			Date:     string(cf.Date),
			ISIN:     cf.ISIN,
			Type:     payTypeStr(cf.Type),
			Amount:   Major(cf.Amount),
			Currency: cf.Amount.Currency().Code,
			Label:    payLabel(cf.ISIN),
		}
		doc.Calendar = append(doc.Calendar, row)
		if i < topN {
			doc.TopPayments = append(doc.TopPayments, row)
		}
	}
	return nil
}

// payLabel — людська назва виплати, коли ISIN сам по собі мовчить.
//
// Один помічник на обидва місця, де будуються рядки виплат (next_payment
// і calendar/top_payments): доти правило жило у фронтенді, і другий
// споживач написав би його вдруге.
//
// Назви банку тут немає навмисно: domain.CashflowItem несе лише дату,
// ключ, тип і суму, а тягти сюди перелік вкладів заради одного слова
// означало б розширити вхід Derive заради оформлення. «Вклад» — рівно те,
// що показував UI, і воно відрізняє потік від облігації, а більше від
// цього поля нічого й не потрібно.
func payLabel(isin string) string {
	if domain.IsDepositISIN(isin) {
		return "вклад"
	}
	// Пенсійна виплата ходить під ключем npf:<id>, і без підпису в календарі
	// стояло б «npf:1» — рядок, який нічого не означає для читача. Назва
	// самого фонду сюди не доходить: у ключі стоїть id саме тому, що план
	// мусить пережити перейменування рахунку (domain.NPFPlanDest), а
	// перекладати id назад у назву означало б тягнути сюди довідник.
	if domain.IsNPFISIN(isin) {
		return "пенсійна виплата"
	}
	return ""
}

// deriveReserve — картка резерву.
//
// Показуємо, лише коли резерв є або задано витрати: порожня картка з
// нулями нічого не додає, а «0 місяців із 3» без заданих витрат ще й
// вигадувала б ціль, якої користувач не ставив.
func deriveReserve(doc *Doc, in DeriveInput) {
	monthlyExp, targetMonths := 0.0, 0.0
	if doc.Settings != nil {
		if doc.Settings.MonthlyExpensesUAH != nil {
			monthlyExp = *doc.Settings.MonthlyExpensesUAH
		}
		if doc.Settings.ReserveTargetMonths != nil {
			targetMonths = *doc.Settings.ReserveTargetMonths
		}
	}
	if doc.ReserveUAH == 0 && monthlyExp <= 0 {
		return
	}
	r := &Reserve{
		UAH: doc.ReserveUAH, ByCurrency: in.ReserveByCur, Places: in.ReservePlaces,
		LastMove: in.ReserveLastMove, MonthlyExpensesUAH: monthlyExp,
		TargetMonths: targetMonths,
	}
	if total := in.Capital.TotalUAH(); total > 0 {
		r.SharePct = doc.ReserveUAH * 100 / total
	}
	if monthlyExp > 0 {
		r.Months = doc.ReserveUAH / monthlyExp
		r.TargetUAH, r.GapUAH = ReserveTarget(doc.Settings, doc.ReserveUAH)
	}
	// Скільки з нових грошей варто відкласти просто зараз.
	//
	// Стеля, а не черга: без неї «спершу добери резерв» зупиняло б покупки
	// на місяці, а резерв не заробляє нічого. Зі стелею частина йде в
	// матрац, решта — у папір, і рухаються обидва.
	//
	// БАЗА — ГРОШІ МІСЯЦЯ, а не готівка на рахунках. Доти рахувалось від
	// Capital.AccountUAH, і на живих даних це давало «спершу поповнити
	// резерв — 2,48 ₴» при розриві в 359 500 ₴: на рахунку лежало 6,19 ₴.
	// Готівка там — стан однієї миті, а не потік, і подушка від неї не
	// залежить; наповнюють її з нових грошей.
	//
	// Самі числа приходять із будівника (state_month.go): ту саму місячну
	// частку потребує ще й ребаланс, і рахувати її двічі означало б завести
	// два джерела правди про одну стелю.
	if r.GapUAH > 0 && doc.Settings != nil && doc.Settings.ReserveFillSharePct != nil {
		if share := *doc.Settings.ReserveFillSharePct; share > 0 && in.ReserveFillMonthUAH > 0 {
			r.FillSharePct = share
			r.FillMonthUAH = round2(in.ReserveFillMonthUAH)
			r.FillNowUAH = round2(in.ReserveFillNowUAH)
			r.FillMovedUAH = round2(in.ReserveMovedUAH)
			if doc.MonthPlan != nil {
				r.FillFromUAH = doc.MonthPlan.PlanUAH
			}
		}
	}
	deriveReserveLadder(r, doc.Settings, in)
	doc.Reserve = r
}

// ReserveDeposit — резервний вклад у вигляді, потрібному драбині.
type ReserveDeposit struct {
	// Months — за скільки місяців від сьогодні тіло звільниться само.
	Months float64
	// AmountUAH — тіло, грн-екв.
	AmountUAH float64
	// Revocable — договір дозволяє забрати достроково. Властивість
	// ДОГОВОРУ, не строку: за ЦКУ строковий вклад фізособи безвідкличний,
	// доки в договорі не написано інакше.
	Revocable bool
	// EarnsUAH — скільки цей вклад приносить за рік після податку.
	EarnsUAH float64
}

// deriveReserveLadder — коли подушка стає доступною.
//
// # ЧОМУ ЦЕ НЕ ОДНЕ ЧИСЛО
//
// Питання «на скільки місяців вистачить» (Months вище) і «коли я до цього
// дістануся» різні, і друге не виводиться з першого. Подушка на 600 000 ₴
// при витратах 50 000 ₴ дає рівно 12 місяців у обох випадках — і коли вона
// лежить готівкою, і коли вона в одному річному вкладі. У другому випадку
// на третій місяць безробіття в руках не буде нічого.
//
// # ГОЛОВА Й ХВІСТ
//
// ГОЛОВА — тверда вимога, і єдина, що має право сказати «не сходиться».
// Аварія не витрачається помісячно: машина ламається на всю суму одразу.
// Скільки саме тримати миттєво доступним, знає лише людина, тож це
// налаштування (reserve_liquid_months), а не виведене число.
//
// ХВІСТ — не вимога, а РОЗМІН, і саме тому горизонт несе три числа замість
// прапорця. Строковий вклад в Україні безвідкличний за замовчуванням, але
// договір може дозволяти дострокове повернення — і тоді замок це штраф:
// тіло віддадуть, відсотки згорять. Порахувати той штраф застосунок не
// може (ставку знає банк), тож він називає лише те, що знає: доки драбина
// тягне сама, доки дотягує з розірванням, і скільки заробляє за це.
//
// Правило «сходинка на кожен місяць» тут навмисно НЕ діє. Воно змушувало б
// дробити подушку тим дрібніше, чим вона глибша — дванадцять сходинок на
// дванадцять місяців, — і оголошувало б порушенням цілком розумний стан:
// голова готівкою плюс один річний відкличний вклад на решту.
func deriveReserveLadder(r *Reserve, s *SettingsDoc, in DeriveInput) {
	if r.MonthlyExpensesUAH <= 0 {
		return // без витрат жодне з цих питань не має відповіді
	}
	liquidMonths, maxTerm := 0.0, 0.0
	if s != nil {
		if s.ReserveLiquidMonths != nil {
			liquidMonths = *s.ReserveLiquidMonths
		}
		if s.ReserveMaxTermMonths != nil {
			maxTerm = *s.ReserveMaxTermMonths
		}
	}
	r.LiquidUAH = round2(in.ReserveLiquidUAH)
	r.LiquidTargetUAH = round2(liquidMonths * r.MonthlyExpensesUAH)
	for _, d := range in.ReserveDeposits {
		r.LadderRungs++
		r.LadderEarnsUAH += d.EarnsUAH
	}
	r.LadderEarnsUAH = round2(r.LadderEarnsUAH)
	// Горизонти рахуємо лише до ЦІЛІ подушки: далі витрачати вже нічого, і
	// рядок «на 13-й місяць бракує» описував би подушку, якої ніхто не
	// обіцяв.
	horizon := int(math.Ceil(r.TargetMonths))
	if horizon <= 0 {
		return
	}
	// Скільки сходинок треба НА РЕЖИМІ: хвіст (ціль мінус голова), але не
	// більше стелі строку — сходинку, довшу за стелю, відкрити не можна, а
	// коротших, ніж хвіст, вистачає.
	if tail := r.TargetMonths - liquidMonths; tail > 0 && maxTerm > 0 {
		r.LadderRungsTarget = int(math.Ceil(math.Min(tail, maxTerm)))
	}
	var firstGapH, lastUncovered float64
	covers, reach := 0.0, 0.0
	coversOpen, reachOpen := true, true
	for h := 1; h <= horizon; h++ {
		hf := float64(h)
		avail, reachable := in.ReserveLiquidUAH, in.ReserveLiquidUAH
		for _, d := range in.ReserveDeposits {
			switch {
			case d.Months <= hf:
				avail += d.AmountUAH
				reachable += d.AmountUAH
			case d.Revocable:
				// Ще не погашений, але договір дозволяє забрати: у
				// «доступно» він не входить, у «дістати можна» — входить.
				reachable += d.AmountUAH
			}
		}
		spent := hf * r.MonthlyExpensesUAH
		// «Доки тягне» — до ПЕРШОГО недобору, а не до останнього: покриття
		// з дірою посередині це не покриття, і рахувати його далі означало
		// б назвати драбину справною через місяць після того, як вона
		// перестала бути такою.
		if coversOpen && avail+0.005 >= spent {
			covers = hf
		} else {
			coversOpen = false
		}
		if reachOpen && reachable+0.005 >= spent {
			reach = hf
		} else {
			reachOpen = false
		}
		if reachable+0.005 < spent && firstGapH == 0 {
			firstGapH, r.LadderGapUAH = hf, round2(spent-reachable)
		}
		if avail+0.005 < spent {
			lastUncovered = hf
		}
		r.Ladder = append(r.Ladder, ReserveRung{
			Months: hf, AvailableUAH: round2(avail),
			ReachableUAH: round2(reachable), SpentUAH: round2(spent),
		})
	}
	r.LadderCoversMonths, r.LadderReachMonths, r.LadderGapMonth = covers, reach, firstGapH
	// НАСТУПНА СХОДИНКА — і порядок тут головне.
	//
	// Доки голова не добрана, вкладу не пропонуємо взагалі, хай би скільки
	// було грошей: покласти правильну суму в неправильній формі гірше, ніж
	// не покласти нічого — гроші стануть недосяжними рівно тоді, коли
	// подушка вперше знадобиться.
	//
	// Далі беремо НАЙДАЛЬШИЙ непокритий місяць, а не найближчий: ближні
	// тримає готівка голови, а гроші, які не знадобляться півроку, мусять
	// півроку й заробляти. Стеля строку обрізає результат — і саме тому
	// картка окремо каже, що робити, коли банк такого строку не пропонує.
	if maxTerm > 0 && r.LiquidUAH+0.005 >= r.LiquidTargetUAH && lastUncovered > 0 {
		r.NextRungMonths = math.Min(lastUncovered, maxTerm)
	}
}

// ReserveTarget — ціль резерву в гривнях і розрив до неї.
//
// Винесене з deriveReserve, бо споживачів стало двоє: сама картка резерву й
// план поточного місяця (state_month.go), який відраховує подушці її стелю
// від НОВИХ грошей. Друга копія цих двох рядків розійшлася б із першою рівно
// тоді, коли хтось поправить одну з них, — а помітити це було б нічим: обидва
// числа виглядають правдоподібно поодинці.
//
// Gap лише додатний: «перебір» резерву не є браком, і від'ємне число тут UI
// прочитав би як «докласти −5 000». Ціль без місячних витрат або без цілі в
// місяцях не існує — обидва нулі означають «міряти нема чим».
func ReserveTarget(s *SettingsDoc, reserveUAH float64) (target, gap float64) {
	if s == nil || s.MonthlyExpensesUAH == nil || s.ReserveTargetMonths == nil {
		return 0, 0
	}
	exp, months := *s.MonthlyExpensesUAH, *s.ReserveTargetMonths
	if exp <= 0 || months <= 0 {
		return 0, 0
	}
	target = exp * months
	if d := target - reserveUAH; d > 0 {
		gap = d
	}
	return target, gap
}
