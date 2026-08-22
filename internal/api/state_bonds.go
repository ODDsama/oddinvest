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
	// YieldWeightUAH — грн-екв. собівартості лотів, для яких YTM узагалі
	// порахувався. Саме ця сума, а не номінал і не капітал, іде вагою в
	// зведену дохідність: ставка зважена нею всередині, і брати назовні
	// іншу означало б приписати частину грошей ставці, яка їх не описує.
	YieldWeightUAH float64
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
		out.YieldWeightUAH = ytmWeightUAH / 100
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

// yieldPart — внесок ОДНОГО виду інструмента в зведену дохідність: пара
// ставок і ГРОШІ, які саме ці ставки покривають.
//
// Вага — не «скільки цього виду в капіталі», а «на скільки грошей ставка
// справді порахована», і різниця не косметична. Вклад без заданої ставки у
// зважування не входить (state_builder.go: «нуль там був би не „нульова
// дохідність“, а „невідома“»); взяти в знаменник усе тіло вкладів означало
// б приписати невідомим грошам ставку відомих.
type yieldPart struct {
	Pct    float64 // номінальна, %
	Real   float64 // вона ж після знецінення, %
	Weight float64 // грн-екв., які ця ставка покриває
	Basis  string  // звідки число: обіцянка чи вимір
}

// blendYield — зведена дохідність ПО ВИДАХ інструмента.
//
// Повертає четвірку одним викликом навмисно: номінальна, реальна, база й
// основа мусять походити з ОДНОГО набору доданків. Розібрані нарізно, вони
// рано чи пізно розійдуться — рівно так, як це вже сталося у фондах, де
// реальна бралась із доларової обіцянки, а номінальна з гривневих
// дивідендів, і плитка малювала реальну ВИЩУ за номінальну.
//
// ЩО ЗМІНИЛОСЬ І ЧОМУ. Доти функція складала рівно дві половини — YTM
// облігацій і дохідність фондів, — а вагами брала номінал ОВДП і ринкову
// вартість сертифікатів. Три вади в одному рядку:
//
//   - «Дохідність портфеля» не була портфелем. Вклади й НПФ у число не
//     входили, хоч обидві їхні ставки вже пораховані поруч і вже зважені
//     кожна у своєму місці. Те саме поле публікується сенсором Home
//     Assistant, чия назва українською — дослівно «Дохідність портфеля»;
//     тобто воно вже було опубліковане під іменем, якого не виправдовує.
//   - Вагою облігаційної половини стояв НОМІНАЛ, хоч сама ставка зважена
//     собівартістю (див. WeightedYTM: «Вагою беремо саме СОБІВАРТІСТЬ, а
//     не номінал»). Папір, куплений із дисконтом, важив у суміші більше,
//     ніж у нього насправді вкладено.
//   - Підпис плитки казав «зважено вкладеним», а вкладеним не була жодна з
//     двох ваг.
//
// Первісний довід («саме вона й потрібна проєкціям») застарів раніше за
// цю правку: ставку реінвесту давно дає projection_rate_pct, зважена по
// валютних рукавах, і зведена дохідність лишилась споживачем рівно одного
// екрана.
//
// Резерву й готівки серед доданків немає, і це РІШЕННЯ ВЛАСНИКА, а не
// недогляд: у подушки своя, абсолютна ціль у місяцях витрат, і розмивати
// нею ставку означало б відповідати на питання «скільки заробляють мої
// інвестиції» числом про те, скільки з них не інвестовано. Скільки саме
// грошей лишилось поза числом, видно з base: capital_uah мінус вона й є
// та частина, що не заробляє.
func blendYield(parts []yieldPart) (nominal, real, base float64, basis string) {
	var wNom, wReal float64
	mixed := false
	for _, p := range parts {
		if p.Weight <= 0 {
			continue
		}
		wNom += p.Pct * p.Weight
		wReal += p.Real * p.Weight
		base += p.Weight
		switch {
		case basis == "":
			basis = p.Basis
		case p.Basis != "" && p.Basis != basis:
			mixed = true
		}
	}
	if base <= 0 {
		return 0, 0, 0, ""
	}
	if mixed {
		// Та сама відповідь, що у фондів: суміш обіцянки з виміром мусить
		// називатись уголос. ОВДП, вклад і НПФ дають ОБІЦЯНКУ — ставка
		// зафіксована договором чи графіком; фонд здебільшого дає ФАКТ по
		// прожитому. Назвати основу найбільшого доданка й видати її за
		// спільну означало б збрехати саме там, де людина звіряється.
		basis = "різні основи"
	}
	return math.Round(wNom/base*100) / 100,
		math.Round(wReal/base*100) / 100,
		math.Round(base*100) / 100,
		basis
}

// kindYieldReal збирає реальні дохідності ЧОТИРЬОХ видів в одну мапу.
//
// Функція нічого не рахує — усі чотири числа вже зважені там, де живуть
// їхні дані: ОВДП у buildBonds, фонди в buildFunds, вклади в циклі по
// termDeposits, НПФ у buildNPF. Тут лише збірка, і саме тому вона така
// коротка: інакше це було б п'яте місце, де відтворюється зважування.
//
// Нуль НЕ потрапляє в мапу. Вид, якого в портфелі немає (або в якого
// немає ставки), мусить бути ВІДСУТНІМ ключем, а не нулем: нуль читається
// як «заробляє нічого», і плитка воронки намалювала б «0.0%» там, де
// чесна відповідь — прочерк. Мапа з `omitempty` дає це задарма.
//
// Резерву тут немає за побудовою — див. коментар до KindYieldRealPct.
func kindYieldReal(bonds, funds, deposits, npf float64) map[string]float64 {
	out := map[string]float64{}
	for k, v := range map[string]float64{
		"bonds": bonds, "funds": funds, "deposits": deposits, "npf": npf,
	} {
		if v != 0 {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
