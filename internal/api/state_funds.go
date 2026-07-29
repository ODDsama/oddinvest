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
	ValueByCur map[string]float64
	// YieldPct / YieldRealPct — зважена ринковою вартістю дохідність,
	// номінальна й реальна. Парою навмисно: одна без одної на екрані
	// читається як помилка, бо той самий інструмент показує різні числа.
	YieldPct     float64
	YieldRealPct float64
}

// buildFunds зводить позиції фондів у рядки картки.
//
// Окремої перевірки «а чи є взагалі операції» тут немає: порожній журнал
// дає порожній hold.Funds, і цикл просто не виконається.
func buildFunds(src *sources, hold domain.Holdings, rates fx.Rates,
	deval float64, today domain.Date) fundsPhase {
	out := fundsPhase{ValueByCur: map[string]float64{}}
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
		out.ValueByCur[fcur] += float64(fp.MarketValue()) / 100

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
		if ref.ExpectedYieldBP > 0 {
			row.ExpectedPct = float64(ref.ExpectedYieldBP) / 100
			row.ExpectedCurrency = ref.ExpectedYieldCur
		}
		if tot, ok := domain.FundTotalReturn(src.fundOps, fp.Fund, today); ok {
			row.TotalPct = tot
			row.RealPct = round2(realYield(tot/100, cur, deval) * 100)
			row.YieldBasis = "дивіденди + зміна ціни"
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
			exp := float64(ref.ExpectedYieldBP) / 100
			row.RealPct = round2(realYield(exp/100, expCur, deval) * 100)
			row.YieldBasis = "обіцяно фондом"
		} else if y > 0 {
			row.RealPct = round2(realYield(y/100, cur, deval) * 100)
			row.YieldBasis = "дивіденди після податку"
		}
		out.Rows = append(out.Rows, row)
	}

	// Дивідендний потік фондів сюди НЕ додається окремо: його оцінка
	// стоїть у cashflow, тож у couponSum вона вже прийшла, і друге
	// додавання рахувало б той самий потік двічі.
	//
	// Дохідність фондів — зважена ринковою вартістю: більший фонд має
	// важити більше, ніж дрібний із гучним відсотком. Зважуємо ПОВНУ
	// дохідність, а не саму дивідендну: у рядку позиції показано саме її,
	// і плитка, що підсумовує ті самі фонди іншою мірою, суперечила б
	// таблиці під собою. Де повної ще немає (замало історії) — падаємо на
	// дивідендну, як і сам рядок.
	var wSum, wReal, w float64
	for _, row := range out.Rows {
		if row.MarketValue <= 0 {
			continue
		}
		nominal := row.TotalPct
		if nominal == 0 {
			nominal = row.YieldNetPct
		}
		wSum += nominal * row.MarketValue
		wReal += row.RealPct * row.MarketValue
		w += row.MarketValue
	}
	if w > 0 {
		out.YieldPct = math.Round(wSum/w*100) / 100
		out.YieldRealPct = math.Round(wReal/w*100) / 100
	}
	return out
}
