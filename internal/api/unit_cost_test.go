package api

import (
	"math"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// Живий папір із фікстури finomo: UA4000239107, купон 80.35 піврічно,
// погашення 07.02.2029. Числа взяті з тієї самої сторінки, з якої взято
// ціну, — саме тому регресія нижче щось означає.
const (
	fxISIN      = "UA4000239107"
	fxToday     = domain.Date("2026-09-06")
	fxPriceDirt = int64(101365) // 1013.65 ₴ — брудна ціна найдешевшого продавця
	fxYieldPct  = 16.60         // те, що джерело опублікувало для цієї ціни
)

func fxBond() domain.Bond {
	return domain.Bond{
		ISIN: fxISIN, Nominal: money.New(100000, money.UAH),
		RateBP: 1607, Maturity: "2029-02-07",
	}
}

func fxPays() []domain.Payment {
	c := func(d domain.Date) domain.Payment {
		return domain.Payment{ISIN: fxISIN, PayDate: d, Type: domain.PayCoupon,
			PerBond: money.New(8035, money.UAH)}
	}
	return []domain.Payment{
		c("2027-02-10"), c("2027-08-11"), c("2028-02-09"), c("2028-08-09"), c("2029-02-07"),
		{ISIN: fxISIN, PayDate: "2029-02-07", Type: domain.PayRedemption,
			PerBond: money.New(100000, money.UAH)},
	}
}

func fxQuote(date domain.Date, minor int64) *store.Quote {
	return &store.Quote{ISIN: fxISIN, Source: "Inzhur", Date: date,
		PriceMinor: minor, Currency: money.UAH, Origin: store.QuoteOriginFinomo}
}

func TestBondUnitCostUsesFreshMarketPrice(t *testing.T) {
	got, basis := bondUnitCost(fxBond(), fxPays(), fxToday, fxQuote("2026-09-05", fxPriceDirt))
	if basis != CostBasisMarket {
		t.Fatalf("підстава %q, чекали %q", basis, CostBasisMarket)
	}
	if got.Amount() != fxPriceDirt {
		t.Errorf("ціна %d, чекали %d", got.Amount(), fxPriceDirt)
	}
}

// НАЙВАЖЛИВІШИЙ ТЕСТ УСІЄЇ РОБОТИ.
//
// Ціна джерела БРУДНА — вже містить накопичений купон. Якщо колись
// хтось «полагодить» bondUnitCost, додавши до неї EstimateAccrued,
// дохідність кожного паперу в застосунку просяде приблизно на 0,6 в.п.
// мовчки: числа лишаться правдоподібними, рейтинг просто стане іншим.
//
// Ловимо це так: рахуємо YTM тією самою domain.YTM, якою його рахує
// «Що купити», і звіряємо з тим, що для ЦІЄЇ САМОЇ ціни опублікувало
// джерело. Збіг доводить, що ми віддали ціну як є.
func TestBondUnitCostMarketPriceIsDirty(t *testing.T) {
	cost, basis := bondUnitCost(fxBond(), fxPays(), fxToday, fxQuote(fxToday, fxPriceDirt))
	if basis != CostBasisMarket {
		t.Fatalf("підстава %q — тест не про те", basis)
	}
	ytm, err := domain.YTM(cost, fxToday, fxPays(), fxISIN)
	if err != nil {
		t.Fatalf("YTM: %v", err)
	}
	if got := ytm * 100; math.Abs(got-fxYieldPct) > 0.05 {
		t.Errorf("YTM %.3f%%, джерело для цієї ж ціни дає %.2f%%.\n"+
			"Найімовірніша причина: до ринкової ціни додали НКД. Вона вже брудна — "+
			"довід і перевірка на п'яти паперах у шапці internal/finomo.", got, fxYieldPct)
	}
}

func TestBondUnitCostFallsBackWhenStale(t *testing.T) {
	stale := fxQuote(fxToday.AddDays(-(quoteFreshDays + 1)), fxPriceDirt)
	got, basis := bondUnitCost(fxBond(), fxPays(), fxToday, stale)
	if basis != CostBasisNominal {
		t.Fatalf("підстава %q, чекали %q", basis, CostBasisNominal)
	}
	if got.Amount() == fxPriceDirt {
		t.Error("протухла ціна не мала стати ціною кроку")
	}
	if got.Amount() < 100000 {
		t.Errorf("відкат мусить бути номінал + НКД, маємо %d", got.Amount())
	}
}

// Той самий поріг свіжості сам вирішує питання плану купівель: рядок на
// півроку вперед не дістає сьогоднішньої ціни, і особливого випадку в
// місці виклику для цього не треба.
func TestBondUnitCostFutureBuyIgnoresTodayPrice(t *testing.T) {
	when := fxToday.AddDays(180)
	_, basis := bondUnitCost(fxBond(), fxPays(), when, fxQuote(fxToday, fxPriceDirt))
	if basis != CostBasisNominal {
		t.Errorf("підстава %q — сьогоднішня ціна не є ціною на %s", basis, when)
	}
}

func TestBondUnitCostWithoutQuote(t *testing.T) {
	got, basis := bondUnitCost(fxBond(), fxPays(), fxToday, nil)
	if basis != CostBasisNominal {
		t.Fatalf("підстава %q, чекали %q", basis, CostBasisNominal)
	}
	if got.Amount() < 100000 {
		t.Errorf("ціна %d менша за номінал", got.Amount())
	}
}

func TestBondUnitCostIgnoresWrongCurrency(t *testing.T) {
	q := fxQuote(fxToday, fxPriceDirt)
	q.Currency = money.USD
	if _, basis := bondUnitCost(fxBond(), fxPays(), fxToday, q); basis != CostBasisNominal {
		t.Errorf("підстава %q — доларова ціна не стосується гривневого паперу", basis)
	}
}

// Сторож масштабу другим шаром (перший — звірка номіналу в jobs). Помилка
// масштабу не виглядає помилкою: папір просто стає в сто разів дорожчим і
// тихо зникає з усіх порад, бо на нього «не вистачає».
func TestBondUnitCostIgnoresAbsurdPrice(t *testing.T) {
	for _, minor := range []int64{fxPriceDirt * 100, fxPriceDirt / 100} {
		if _, basis := bondUnitCost(fxBond(), fxPays(), fxToday, fxQuote(fxToday, minor)); basis != CostBasisNominal {
			t.Errorf("ціна %d мусила бути відкинута як помилка масштабу", minor)
		}
	}
}

// Ціна «з майбутнього» означає не свіжість, а зіпсований ряд.
func TestBondUnitCostIgnoresFutureQuote(t *testing.T) {
	if _, basis := bondUnitCost(fxBond(), fxPays(), fxToday,
		fxQuote(fxToday.AddDays(3), fxPriceDirt)); basis != CostBasisNominal {
		t.Errorf("підстава %q — ціна, датована пізніше за питання, не є ціною", basis)
	}
}

// Рядок розкладки мусить нести ту саму підставу ціни, що й порада, з якої
// він зроблений.
//
// СТОРОЖ ПРОТИ МОВЧАЗНОЇ ПІДМІНИ. Без цих полів на екрані лишалось би саме
// число, і різниця між «1 013,65 у Inzhur, ціна за 6 вересня» та «1 011,42
// за номіналом плюс НКД» читалась би як рух ринку — тобто застосунок
// повідомляв би про подію, якої не було. Арифметики в перенесенні немає
// жодної, і саме тому тест перевіряє КОЖНЕ поле: пропущене мовчить.
func TestAllocLineCarriesCostProvenance(t *testing.T) {
	alt := toMoneyJSON(money.New(101862, money.UAH))
	sg := suggestion{
		Kind: "bond", Label: fxISIN, ISIN: fxISIN, Currency: money.UAH,
		CostPerBond:    toMoneyJSON(money.New(fxPriceDirt, money.UAH)),
		CostBasis:      CostBasisMarket,
		CostAsOf:       "2026-09-06",
		CostWhere:      "Inzhur",
		CostWhereLabel: "inzhur",
		CostAlt:        &alt,
		CostAltWhere:   "Privat24",
	}
	line, _, ok := allocOne(sg, 5000, nil, money.UAH, nil)
	if !ok {
		t.Fatal("на 5 000 ₴ мусив уміститись хоча б один папір")
	}
	switch {
	case line.CostBasis != CostBasisMarket:
		t.Errorf("підстава %q", line.CostBasis)
	case line.CostAsOf != "2026-09-06":
		t.Errorf("дата ціни %q", line.CostAsOf)
	case line.CostWhere != "Inzhur" || line.CostWhereLabel != "inzhur":
		t.Errorf("продавець %q/%q", line.CostWhere, line.CostWhereLabel)
	case line.CostAlt == nil || line.CostAltWhere != "Privat24":
		t.Errorf("друга ціна %+v/%q", line.CostAlt, line.CostAltWhere)
	}
}

// Порада за номіналом мусить сказати про це так само голосно.
func TestAllocLineSaysNominalToo(t *testing.T) {
	sg := suggestion{
		Kind: "bond", Label: fxISIN, ISIN: fxISIN, Currency: money.UAH,
		CostPerBond: toMoneyJSON(money.New(101142, money.UAH)),
		CostBasis:   CostBasisNominal,
	}
	line, _, ok := allocOne(sg, 5000, nil, money.UAH, nil)
	if !ok {
		t.Fatal("рядок мусив скластись")
	}
	if line.CostBasis != CostBasisNominal {
		t.Errorf("підстава %q, чекали %q", line.CostBasis, CostBasisNominal)
	}
	if line.CostWhere != "" || line.CostAlt != nil {
		t.Errorf("за номіналом продавця бути не може: %+v", line)
	}
}

// Папір із РИНКОВОЮ ціною більше не понижується за те, що його рік не
// розміщували на аукціоні.
//
// Довід пониження був про НАШУ невпевненість у власному числі: ціна
// виводилась із первинного розміщення, і для паперу, якого рік не
// розміщували, вона була вигадкою. Відколи ціну приносить джерело, ця
// невпевненість зникла — а факт «лише вторинний ринок» лишився в причині
// рядка, бо він і далі правда.
func TestStaleDoesNotDemoteWhenPriceIsReal(t *testing.T) {
	priced := suggestion{Kind: "bond", ISIN: "UA-PRICED", Currency: money.UAH,
		CostBasis: CostBasisMarket, stale: true, RealPct: 10}
	guessed := suggestion{Kind: "bond", ISIN: "UA-GUESS", Currency: money.UAH,
		CostBasis: CostBasisNominal, stale: false, RealPct: 9}
	if !lessSuggestion(priced, guessed, "rate", orderReal) {
		t.Error("папір із ринковою ціною мусить стояти вище: його дохідність вища, " +
			"а старий аукціон до знання ціни стосунку не має")
	}
}

// А без ринкової ціни все лишається як було: там головне число справді
// виведене з номіналу, і stale каже про нього правду.
func TestStaleStillDemotesGuessedPrice(t *testing.T) {
	stale := suggestion{Kind: "bond", ISIN: "UA-STALE", Currency: money.UAH,
		CostBasis: CostBasisNominal, stale: true, RealPct: 20}
	fresh := suggestion{Kind: "bond", ISIN: "UA-FRESH", Currency: money.UAH,
		CostBasis: CostBasisNominal, stale: false, RealPct: 9}
	if lessSuggestion(stale, fresh, "rate", orderReal) {
		t.Error("без ринкової ціни папір без розміщення мусить лишатись нижчим " +
			"навіть із більшою дохідністю — вона порахована з вигаданої ціни")
	}
}
