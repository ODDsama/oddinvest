// Облігаційна частина — номінал і дохідність до погашення.
//
// Шоста фаза розбиття buildState.
//
// Дохідність тут — ДО ПОГАШЕННЯ (YTM) від того, що фактично сплачено, і
// зважена вкладеними грішми. Раніше на її місці стояло «річний купон ÷
// номінал»: воно відповідало на питання «скільки папір платить», а не
// «скільки я заробляю». Ціна купівлі не впливала взагалі, тож папір,
// узятий із дисконтом, і папір, узятий з премією, виглядали однаково.
// YTM бачить і дисконт, і комісію, і те, що піврічний купон складається
// всередині року.
//
// Кожне число тут іде ПАРОЮ — номінальне й реальне. Доти плитки говорили
// номінальними, а таблиця під ними реальними, і той самий папір
// показувався двома різними числами на одному екрані без жодної позначки,
// що бази різні.
package api

import (
	"math"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"

	money "github.com/Rhymond/go-money"
)

// ytmLot — лот у вигляді, який розуміє розрахунок дохідності: собівартість
// одного паперу — «брудна» ціна плюс частка комісії. Комісія теж з'їдає
// дохідність, тож ховати її означало б завищувати результат.
func ytmLot(l domain.Lot, qty int64) domain.YTMLot {
	cost := l.PricePerBond
	if fee, err := domain.Apportion(l.Fee, 1, l.Qty); err == nil && !fee.IsZero() {
		if c2, aerr := cost.Add(fee); aerr == nil {
			cost = c2
		}
	}
	return domain.YTMLot{CostPerBond: cost, Qty: qty, BuyDate: l.BuyDate, ISIN: l.ISIN}
}

// bondsPhase — усе, що фази нижче знають про облігації.
type bondsPhase struct {
	// NominalUAH — сумарний номінал у грн-екв., МІНОРНІ одиниці.
	NominalUAH int64
	// NominalByCur — номінал НАТИВНО по валютах (для рукавів проєкції);
	// NominalByCurUAH — він самий у грн-екв., мажорні (для капіталу й
	// валютних часток, які міряються в спільній одиниці).
	NominalByCur    map[string]int64
	NominalByCurUAH map[string]float64
	// NominalByISIN — номінал по ПАПЕРАХ, грн-екв., мінорні. Рахувався тут
	// транзитом і викидався — а це єдиний вимір диверсифікації, якого в
	// застосунку не було зовсім: «половина портфеля в одному ISIN»
	// побачити не було де.
	NominalByISIN map[string]int64
	// YieldPct / YieldRealPct — зважена по всьому портфелю ОВДП;
	// YieldByCur / YieldRealByCur — те саме окремо по валютах.
	YieldPct       float64
	YieldRealPct   float64
	YieldByCur     map[string]float64
	YieldRealByCur map[string]float64
}

// buildBonds рахує номінал і дохідності по лотах, які ще в портфелі.
func buildBonds(hold domain.Holdings, pays []domain.Payment,
	rates fx.Rates, deval float64) bondsPhase {
	out := bondsPhase{
		NominalByCur:    map[string]int64{},
		NominalByCurUAH: map[string]float64{},
		NominalByISIN:   map[string]int64{},
		YieldByCur:      map[string]float64{},
		YieldRealByCur:  map[string]float64{},
	}
	ytmLotsByCur := map[string][]domain.YTMLot{}
	var ytmWeightUAH, ytmWeightedUAH, ytmWeightedRealUAH float64
	for _, l := range hold.Lots {
		if !l.Held() {
			continue
		}
		b, q := l.Bond, l.Remaining
		cur := b.Nominal.Currency().Code
		out.NominalByCur[cur] += b.Nominal.Amount() * q
		if n, err := fx.ToUAH(money.New(b.Nominal.Amount()*q, cur), rates); err == nil {
			out.NominalUAH += n.Amount()
			out.NominalByISIN[l.ISIN] += n.Amount()
		}
		lot := ytmLot(l.Lot, q)
		cost := lot.CostPerBond
		ytmLotsByCur[cur] = append(ytmLotsByCur[cur], lot)
		// Для зведеної цифри вагу переводимо в гривню, щоб валюти
		// складались коректно, а самі ставки лишались нативними.
		if y, ok := domain.WeightedYTM([]domain.YTMLot{lot}, pays); ok {
			if w, err := fx.ToUAH(money.New(cost.Amount()*q, cur), rates); err == nil {
				ytmWeightUAH += float64(w.Amount())
				ytmWeightedUAH += float64(w.Amount()) * y
				// Реальну зважуємо тут само, лотом за лотом, а не ділимо
				// готову суміш на знецінення: знецінення торкається лише
				// гривневих рукавів, і поділ суміші цілком занизив би
				// доларову частину.
				ytmWeightedRealUAH += float64(w.Amount()) * realYield(y/100, cur, deval) * 100
			}
		}
	}
	if ytmWeightUAH > 0 {
		out.YieldPct = math.Round(ytmWeightedUAH/ytmWeightUAH*100) / 100
		out.YieldRealPct = math.Round(ytmWeightedRealUAH/ytmWeightUAH*100) / 100
	}
	for cur, ls := range ytmLotsByCur {
		if y, ok := domain.WeightedYTM(ls, pays); ok {
			out.YieldByCur[cur] = math.Round(y*100) / 100
			out.YieldRealByCur[cur] = round2(realYield(y/100, cur, deval) * 100)
		}
	}
	// Номінал по валютах у грн-екв. — переводиться тут, а не в капіталі,
	// бо це та сама облігаційна величина, тільки в іншій одиниці.
	for cur, minor := range out.NominalByCur {
		if u, err := fx.ToUAH(money.New(minor, cur), rates); err == nil {
			out.NominalByCurUAH[cur] = float64(u.Amount()) / 100
		}
	}
	return out
}

// blendYield — зведена дохідність портфеля: облігації важать номіналом у
// грн-екв., фонди — ринковою вартістю.
//
// Саме вона й потрібна проєкціям: доти капітал у сертифікатах ріс у них
// за ставкою облігацій, яких у ньому немає.
func blendYield(bondPct, fundPct, bondsUAH, fundsUAH float64) float64 {
	w := bondsUAH + fundsUAH
	if w <= 0 {
		return 0
	}
	return math.Round((bondPct*bondsUAH+fundPct*fundsUAH)/w*100) / 100
}
