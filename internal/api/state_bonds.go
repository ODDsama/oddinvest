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
	"github.com/ODDsama/oddinvest/internal/state"

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
	// YieldWeightUAH — грн-екв. НОМІНАЛУ лотів, для яких YTM узагалі
	// порахувався. Це вага облігаційної половини в зведеній дохідності.
	//
	// Номінал, а не собівартість, і це рішення далося не одразу.
	// Собівартість здається правильнішою: сама ставка зважена саме нею
	// (WeightedYTM: «вагою беремо саме СОБІВАРТІСТЬ»), і гроші, що
	// працюють, — це сплачене, а не номінал. Але зважена собівартістю база
	// НЕ ЗВОДИТЬСЯ З КАПІТАЛОМ: state.Capital міряє ОВДП номіналом, тож
	// сума «база + подушка + готівка» розходилась із capital_uah на премію
	// чи дисконт — на живих даних на 1 376 ₴, і взятись їм було нізвідки.
	//
	// А зводитись вона мусить, бо в цьому весь сенс числа: твердження
	// «портфель заробляє стільки» перевіряється рівно тим, що видно, яка
	// гривня всередині, а яка ні. Похибка ваги на премії коштує 0.06 в.п.;
	// головне число, чиї частини не сходяться з капіталом, коштує довіри до
	// всієї картки. Обмеження «лише лоти з порахованим YTM» лишається: це
	// та сама межа, що у вкладу без ставки — невідома ставка не має ваги.
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
	var ytmWeightUAH, ytmWeightedUAH, ytmWeightedRealUAH, ytmNominalUAH float64
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
			if n, err := fx.ToUAH(money.New(b.Nominal.Amount()*q, cur), rates); err == nil {
				ytmNominalUAH += float64(n.Amount())
			}
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
		out.YieldWeightUAH = ytmNominalUAH / 100
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
	// Measured — це ФАКТ по прожитому, а не ставка, зафіксована наперед.
	//
	// Прапорець, а не розбір Basis рядком, і це не педантизм. Basis писаний
	// для людини й міняється при редагуванні прози; switch по його тексту
	// мовчки перемкнув би вимір на обіцянку від однієї коми в підписі, і
	// жоден тест цього б не спіймав. Ставиться там, де гілку й обрано, — у
	// buildFunds їх три явні, у buildNPF measured уже повертає NPFNavReturn,
	// а ОВДП і вклад обіцяні за визначенням.
	Measured bool
}

// yieldMix — накопичувач зваженої дохідності з поділом на зароблене й
// обіцяне.
//
// Одне означення на три місця: фонди (buildFunds), НПФ (buildNPF) і зведена
// по видах (blendYield). Доти зважування було переписане в кожному, і
// правило «нуль не потрапляє у ваги» доводилось тримати незміненим у трьох
// копіях; поділ на половини зробив би їх шістьма.
type yieldMix struct {
	nom, real, weight    float64
	mNom, mReal, mWeight float64 // зароблене
	pNom, pReal, pWeight float64 // обіцяне
	basis                string
	mixed                bool
}

// add — один доданок. Нульова вага не входить нікуди: вид, у якого ставки
// немає, мусить бути ВІДСУТНІМ, а не нулем — нуль читався б як «заробляє
// нічого».
func (m *yieldMix) add(nominal, real, weight float64, measured bool, basis string) {
	if weight <= 0 {
		return
	}
	m.nom += nominal * weight
	m.real += real * weight
	m.weight += weight
	if measured {
		m.mNom += nominal * weight
		m.mReal += real * weight
		m.mWeight += weight
	} else {
		m.pNom += nominal * weight
		m.pReal += real * weight
		m.pWeight += weight
	}
	switch {
	case m.basis == "":
		m.basis = basis
	case basis != "" && basis != m.basis:
		m.mixed = true
	}
}

// result — зважені номінальна, реальна, база й основа.
func (m *yieldMix) result() (nominal, real, base float64, basis string) {
	if m.weight <= 0 {
		return 0, 0, 0, ""
	}
	basis = m.basis
	if m.mixed {
		// Та сама відповідь, що у фондів: суміш обіцянки з виміром мусить
		// називатись уголос. Назвати основу найбільшого доданка й видати її
		// за спільну означало б збрехати саме там, де людина звіряється.
		basis = "різні основи"
	}
	return round2(m.nom / m.weight), round2(m.real / m.weight), round2(m.weight), basis
}

// halves — той самий вид ДВОМА доданками для зведеної по видах: заробленою
// половиною й обіцяною.
//
// Потрібне тому, що вид сам по собі буває змішаний: фонди дають і факт
// (REIT платить дивіденди), і обіцянку (МілТех купили девʼять днів тому).
// Віддати такий вид одним усередненим доданком означало б, що портфельний
// розклад не зможе сказати, куди його віднести, — і половина числа
// загубилась би саме там, де питання й ставлять.
//
// Порожня половина не віддається зовсім: нульова вага в add() однаково
// відсіється, але пропустити її тут дешевше й зрозуміліше.
func (m *yieldMix) halves(basis string) []yieldPart {
	var out []yieldPart
	if m.mWeight > 0 {
		out = append(out, yieldPart{
			Pct: m.mNom / m.mWeight, Real: m.mReal / m.mWeight,
			Weight: m.mWeight, Basis: basis, Measured: true,
		})
	}
	if m.pWeight > 0 {
		out = append(out, yieldPart{
			Pct: m.pNom / m.pWeight, Real: m.pReal / m.pWeight,
			Weight: m.pWeight, Basis: basis,
		})
	}
	return out
}

// split — розклад на дві половини, або nil, коли розкладати нема чого.
//
// Порожня половина означає, що все зароблене або все обіцяне; показувати
// тоді розклад означало б повторити головне число й намалювати нуль там,
// де насправді порожньо.
func (m *yieldMix) split() *state.YieldSplit {
	if m.mWeight <= 0 || m.pWeight <= 0 {
		return nil
	}
	return &state.YieldSplit{
		MeasuredRealPct: round2(m.mReal / m.mWeight), MeasuredUAH: round2(m.mWeight),
		PromisedRealPct: round2(m.pReal / m.pWeight), PromisedUAH: round2(m.pWeight),
	}
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
func blendYield(parts []yieldPart) (nominal, real, base float64, basis string, split *state.YieldSplit) {
	var m yieldMix
	for _, p := range parts {
		m.add(p.Pct, p.Real, p.Weight, p.Measured, p.Basis)
	}
	nominal, real, base, basis = m.result()
	return nominal, real, base, basis, m.split()
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
//
// Одна функція на обидві мапи — реальну й номінальну: правило «нуль не
// потрапляє» однакове для них, і друга копія розійшлася б із першою рівно
// тоді, коли хтось виправить одну.
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
