// Сертифікати фондів — рядки картки й зважена дохідність.
//
// П'ята фаза розбиття buildState. Позиція фонду — сальдо журналу
// операцій; дивіденди беруться ПІСЛЯ податку, бо купон ОВДП від нього
// звільнений, а дивіденд фонду ні, і в спільну картку доходу вони можуть
// потрапити лише в одній мірі.
//
// Головне правило файлу: усі суми рядка — у ГРН-ЕКВІВАЛЕНТІ, усі до
// одної. Доти в гривні була лише MarketValue, а собівартість, дивіденди,
// податок і реалізоване лишались НАТИВНИМИ — в одному struct, за десять
// рядків одне від одного. UI форматує їх усі як гривню, «Портфель»
// рахує pnl = market_value − cost_basis, а doc.FundsCostUAH сумує їх у
// поле, назва якого закінчується на UAH. Для гривневого фонду це
// збігається й тому не помічалось; для будь-якого іншого — ні.
//
// Виняток один і навмисний: LastPrice лишається НАТИВНОЮ. Це ціна одного
// сертифіката, показується вона поруч із Currency («11.1366 ₴», «$1.02»),
// а помічник реінвесту будує з неї money у валюті фонду. Ціна за штуку й
// підсумок — різні за природою величини, і зводити їх до однієї одиниці
// означало б зламати обидва місця.
package api

import (
	"math"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"

	money "github.com/Rhymond/go-money"
)

// fundsPhase — усе, що фази нижче знають про фонди.
type fundsPhase struct {
	// Rows — рядки картки «Фонди», суми в грн-екв.
	Rows []state.FundPositionRow
	// TotalUAH — ринкова вартість усіх сертифікатів, грн-екв.
	TotalUAH float64
	// ValueByCur — та сама вартість, але в НАТИВНІЙ валюті: рукави
	// проєкції рахують кожен у своїй, і грн-еквівалент там був би
	// подвійним переведенням.
	//
	// РОЗПОДІЛЬНІ фонди, і лише вони. Накопичувальні виходять окремо, у
	// Accum: у проєкції їм місце не в замкненому капіталі, а у власному
	// кошику, який росте.
	ValueByCur map[string]float64
	// Accum — накопичувальні позиції, розкладені по валютах сертифіката.
	//
	// Окремо від ValueByCur, бо це різні ролі в симуляції, а не різні
	// суми. Доти обидва види лежали в замкненому: розподільному там
	// правильно (він віддає дивіденди в календар), накопичувальному —
	// ні. Фонд, що обіцяв 25% річних, у проєкції не додавав НІЧОГО, бо
	// потоку в нього немає за природою, а замкнене не росте за побудовою.
	Accum map[string][]domain.Accum
	// YieldPct / YieldRealPct — зважена ринковою вартістю дохідність,
	// номінальна й реальна. Парою навмисно: одна без одної на екрані
	// читається як помилка, бо той самий інструмент показує різні числа.
	YieldPct     float64
	YieldRealPct float64
	// Basis — ЗВІДКИ це число: та сама основа, що в рядку фонду
	// («дивіденди + зміна ціни», «обіцяно фондом», «дивіденди після
	// податку»), або «різні основи», коли рядки міряні по-різному.
	//
	// Доти зведення основи не мало взагалі, і плитка зашивала її рядком —
	// тобто називала основу, якої в числі могло й не бути.
	Basis string
}

// fundWeight — внесок одного фонду в зведену дохідність.
//
// Номінальна й реальна тут ідуть ПАРОЮ з одного джерела. Тримати їх
// нарізно означає рано чи пізно зважити реальну з обіцянки поруч із
// номінальною з виміру — і отримати реальну, вищу за номінальну.
type fundWeight struct {
	MarketValue float64
	NominalPct  float64
	RealPct     float64
	Basis       string
}

// accumRatePct — ставка, під яку накопичувальна позиція росте В СВОЇЙ
// валюті.
//
// Обіцянка може бути в чужій валюті: гривневий сертифікат, чия ціна йде
// за курсом НБУ, обіцяє у доларах. Така обіцянка вже РЕАЛЬНА — приріст
// ціни в гривні і є компенсацією знецінення, — тож у гривневому рукаві
// вона мусить зрости на це знецінення, інакше модель загубить рівно ту
// частину, заради якої валютний інструмент і тримають.
//
// Дзеркало realYield, який ходить у зворотний бік.
func accumRatePct(expected float64, expCur, fundCur string, deval float64) float64 {
	if expected <= 0 || expCur == "" || expCur == fundCur {
		return expected
	}
	if fundCur != money.UAH {
		// Обіцянка в одній чужій валюті, сертифікат в іншій. Курсу між
		// ними модель не тримає, і вигадувати його гірше, ніж лишити
		// ставку як є.
		return expected
	}
	return ((1+expected/100)*(1+deval/100) - 1) * 100
}

// accumCloseM — на якому місяці симуляції фонд закривається.
// 0 = не закривається; дата в минулому теж дає 0, бо закриття, яке вже
// сталося, у майбутню симуляцію не переносять.
func accumCloseM(closeDate string, today domain.Date) int {
	if closeDate == "" {
		return 0
	}
	d, err := domain.ParseDate(closeDate)
	if err != nil {
		return 0
	}
	if m := domain.MonthsBetween(today, d); m > 0 {
		return m
	}
	return 0
}

// buildFunds зводить позиції фондів у рядки картки.
//
// Окремої перевірки «а чи є взагалі операції» тут немає: порожній журнал
// дає порожній hold.Funds, і цикл просто не виконається.
func buildFunds(src *sources, hold domain.Holdings, rates fx.Rates,
	deval float64, today domain.Date) fundsPhase {
	out := fundsPhase{
		ValueByCur: map[string]float64{},
		Accum:      map[string][]domain.Accum{},
	}
	weights := make([]fundWeight, 0, len(hold.Funds))
	// Позиції зведені раз, у Holdings, і вже у сталому порядку за назвою —
	// сортувати тут більше нема чого.
	for i := range hold.Funds {
		fp := &hold.Funds[i].FundPosition
		mv := money.New(fp.MarketValue(), fp.Currency)
		mvUAH := float64(fp.MarketValue()) / 100
		if u, cerr := fx.ToUAH(mv, rates); cerr == nil {
			mvUAH = float64(u.Amount()) / 100
		}
		out.TotalUAH += mvUAH
		fcur := fp.Currency
		if fcur == "" {
			fcur = money.UAH
		}

		toUAH := func(minor int64) float64 {
			if u, cerr := fx.ToUAH(money.New(minor, fp.Currency), rates); cerr == nil {
				return round2(float64(u.Amount()) / 100)
			}
			return round2(float64(minor) / 100)
		}
		// Довідник і день виплати проставлені в Holdings, разом із потоками.
		ref := src.fundRefs[fp.Fund]
		y, _ := domain.DividendYieldNet(src.fundOps, fp, today)
		row := state.FundPositionRow{
			Fund: fp.Fund, Currency: fp.Currency, Qty: fp.Qty,
			CostBasis:     toUAH(fp.CostBasis),
			LastPrice:     math.Round(float64(fp.LastPrice)) / 10000,
			LastPriceDate: string(fp.LastPriceDate),
			MarketValue:   round2(mvUAH),
			DividendsNet:  toUAH(fp.DividendsGross - fp.DividendsTax),
			DividendsTax:  toUAH(fp.DividendsTax),
			Realized:      toUAH(fp.Realized),
			YieldNetPct:   y,
			Short:         fp.Short,
		}
		// Дохідність позиції — ПОВНА: дивіденди разом зі зміною ціни.
		// Самі дивіденди поряд з облігацією нечесні, бо YTM ловить і
		// купон, і дисконт, тобто весь дохід паперу. Якщо історії ще
		// замало для ануалізації, відступаємо до дивідендної частини —
		// краще менше, ніж вигадані сотні відсотків із трьох днів.
		cur := fp.Currency
		if cur == "" {
			cur = money.UAH
		}
		if d, ok := domain.NextPayoutDate(int(ref.PayoutDay), today); ok {
			row.NextPayout = string(d)
		}
		// Обіцянка заповнюється ЗАВЖДИ, коли задана, — навіть якщо рядок
		// портфеля показує виміряну повну дохідність. Помічник реінвесту
		// бере саме її: там питання про майбутнє, і минулий приріст ціни
		// туди не годиться.
		//
		// І заповнюється вона СКЛАДНОЮ річною, хоч би як її назвав фонд.
		// Це не косметика: ExpectedPct стоїть поруч із YTM облігації в
		// одній таблиці, іде в помічник реінвесту й задає ставку росту в
		// проєкції — усюди як складна. Фонд же обіцяє «середньорічну
		// ПРОСТУ 25% за три роки», і це ×1.75, а не ×1.95. Переводимо
		// тут, один раз, щоб далі по застосунку їздила одна одиниця.
		//
		// Обіцянка «як її назвав фонд» лишається поруч окремими полями:
		// інакше людина бачила б у картці 20.5% там, де сама вписала 25,
		// і не мала б жодного способу зрозуміти, звідки взялась різниця.
		if ref.ExpectedYieldBP > 0 {
			simple := float64(ref.ExpectedYieldBP) / 100
			row.ExpectedPct = round2(domain.CompoundFromSimple(simple, int(ref.YieldSimpleYears)))
			row.ExpectedCurrency = ref.ExpectedYieldCur
			if ref.YieldSimpleYears > 0 {
				row.ExpectedSimplePct = simple
				row.ExpectedSimpleYears = int(ref.YieldSimpleYears)
			}
		}
		// Накопичувальний фонд виходить із замкненого капіталу у власний
		// кошик; розподільний лишається там, де був, бо його дохід і
		// далі приходить дивідендами в календар.
		if ref.Kind == store.FundAccumulating {
			out.Accum[fcur] = append(out.Accum[fcur], domain.Accum{
				Value0:  float64(fp.MarketValue()) / 100,
				Cost0:   float64(fp.CostBasis) / 100,
				RatePct: accumRatePct(row.ExpectedPct, row.ExpectedCurrency, fcur, deval),
				CloseM:  accumCloseM(ref.CloseDate, today),
				TaxPct:  float64(ref.IncomeTaxBP) / 100,
			})
		} else {
			out.ValueByCur[fcur] += float64(fp.MarketValue()) / 100
		}
		// nominalPct — НОМІНАЛЬНИЙ ДВІЙНИК RealPct: те саме число до
		// поправки на знецінення, з того самого джерела.
		//
		// Тримається поруч навмисно. Доти зведення брало номінальну окремо
		// (TotalPct зі спадом на YieldNetPct), і в гілці «обіцяно фондом»
		// це давало пару з РІЗНИХ джерел: реальна з доларової обіцянки
		// (9.5%), номінальна з виміряних гривневих дивідендів (2.77%).
		// Плитка ставила їх поруч як «реальна / номінальна», хоч друге не
		// є першим до поправки, і виходило неможливе — реальна ВИЩА за
		// номінальну.
		var nominalPct float64
		if tot, ok := domain.FundTotalReturn(src.fundOps, fp.Fund, today); ok {
			row.TotalPct = tot
			row.RealPct = round2(realYield(tot/100, cur, deval) * 100)
			row.YieldBasis = "дивіденди + зміна ціни"
			nominalPct = tot
		} else if ref.ExpectedYieldBP > 0 {
			// Обіцянка фонду — між виміряною повною і самою дивідендною.
			// Доти, доки повної немає, дивідендна частина в парі з
			// гривневим знеціненням давала число, яке вводить в оману:
			// фонд, що обіцяє 9.5% у доларі, показувався як −2.9%
			// реальних, бо приросту ціни ще не видно, а штраф уже
			// відняли. Обіцянка ближча до правди — але це саме обіцянка,
			// і yield_basis каже про це вголос.
			//
			// Валюта береться з ОБІЦЯНКИ, а не з сертифіката: 9.5% у
			// доларі вже є реальною дохідністю, і гривневий штраф до неї
			// застосовувати не можна — приріст ціни в гривні і є тією
			// компенсацією знецінення.
			expCur := ref.ExpectedYieldCur
			if expCur == "" {
				expCur = cur
			}
			// Обіцянка береться з РЯДКА, а не заново з довідника, і це не
			// економія рядка. У довіднику вона лежить так, як її назвав
			// фонд, — а назвати він міг простою середньорічною. Порахувати
			// реальну з сирих 25%, поки номінальна в тому ж рядку вже
			// 20.5% складних, означає поставити поруч дві величини з
			// однією назвою й різними одиницями. Рівно те, від чого
			// стереже коментар нижче про пару.
			exp := row.ExpectedPct
			row.RealPct = round2(realYield(exp/100, expCur, deval) * 100)
			row.YieldBasis = "обіцяно фондом"
			// Номінальна тут — сама обіцянка, ЯК ВОНА ЗАДАНА, без переводу
			// в гривню. Це та сама угода, що й для валютних ОВДП: доларовий
			// папір під 4% показує 4% і номінальних, і реальних, бо ставка
			// котирується у своїй валюті, а не в гривневому еквіваленті.
			// Пара знову стає парою, і real <= nominal тримається за
			// побудовою.
			nominalPct = exp
		} else if y > 0 {
			row.RealPct = round2(realYield(y/100, cur, deval) * 100)
			row.YieldBasis = "дивіденди після податку"
			nominalPct = y
		}
		out.Rows = append(out.Rows, row)
		weights = append(weights, fundWeight{
			MarketValue: row.MarketValue, NominalPct: nominalPct,
			RealPct: row.RealPct, Basis: row.YieldBasis,
		})
	}

	// Дивідендний потік фондів сюди НЕ додається окремо: його оцінка
	// стоїть у cashflow, тож у couponSum вона вже прийшла, і друге
	// додавання рахувало б той самий потік двічі.
	//
	// Дохідність фондів — зважена ринковою вартістю: більший фонд має
	// важити більше, ніж дрібний із гучним відсотком.
	var wSum, wReal, w float64
	basis := ""
	mixed := false
	for _, k := range weights {
		if k.MarketValue <= 0 {
			continue
		}
		wSum += k.NominalPct * k.MarketValue
		wReal += k.RealPct * k.MarketValue
		w += k.MarketValue
		switch {
		case basis == "":
			basis = k.Basis
		case basis != k.Basis:
			mixed = true
		}
	}
	if w > 0 {
		out.YieldPct = math.Round(wSum/w*100) / 100
		out.YieldRealPct = math.Round(wReal/w*100) / 100
		// Основа зведення має сенс, лише коли всі рядки міряні однаково.
		// Інакше чесніше сказати, що вона змішана, ніж назвати основу
		// найбільшого фонду й видати її за спільну.
		out.Basis = basis
		if mixed {
			out.Basis = "різні основи"
		}
	}
	return out
}
