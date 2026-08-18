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
		if targetMonths > 0 {
			r.TargetUAH = monthlyExp * targetMonths
			// Gap лише додатний: «перебір» резерву не є браком, і
			// від'ємне число тут UI прочитав би як «докласти −5 000».
			if gap := r.TargetUAH - doc.ReserveUAH; gap > 0 {
				r.GapUAH = gap
			}
		}
	}
	// Скільки з вільних грошей варто відкласти просто зараз.
	//
	// Стеля, а не черга: без неї «спершу добери резерв» зупиняло б покупки
	// на місяці, а резерв не заробляє нічого. Зі стелею частина йде в
	// матрац, решта — у папір, і рухаються обидва.
	//
	// Вільні гроші — саме готівка на рахунках (Capital.AccountUAH), а не
	// капітал: продавати папір, щоб набити подушку, застосунок не радить,
	// бо цін вторинного ринку не знає.
	if r.GapUAH > 0 && doc.Settings != nil && doc.Settings.ReserveFillSharePct != nil {
		share, free := *doc.Settings.ReserveFillSharePct, in.Capital.AccountUAH
		if share > 0 && free > 0 {
			fill := free * share / 100
			// Розрив менший за стелю — беремо розрив. Радити відкласти
			// БІЛЬШЕ за ціль означало б завести другу ціль поруч із тією,
			// яку користувач задав у місяцях витрат.
			if fill > r.GapUAH {
				fill = r.GapUAH
			}
			r.FillSharePct, r.FillFromUAH, r.FillNowUAH = share, round2(free), round2(fill)
		}
	}
	doc.Reserve = r
}
