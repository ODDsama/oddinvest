package domain

import (
	"math"
	"testing"

	money "github.com/Rhymond/go-money"
)

func TestXIRRSimpleYear(t *testing.T) {
	// −1000.00 і рівно через рік +1160.00 -> 16.00%
	flows := []Flow{
		{Date: "2025-01-01", Amount: -100000},
		{Date: "2026-01-01", Amount: 116000},
	}
	r, err := XIRR(flows)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(r-0.16) > 1e-4 {
		t.Errorf("XIRR = %.6f, хочемо 0.16", r)
	}
}

func TestXIRRWithCoupon(t *testing.T) {
	// −1000, купон +80 через півроку, +1080 через рік: r ≈ 16.32%
	flows := []Flow{
		{Date: "2025-01-01", Amount: -100000},
		{Date: "2025-07-02", Amount: 8000}, // ~0.4986 року
		{Date: "2026-01-01", Amount: 108000},
	}
	r, err := XIRR(flows)
	if err != nil {
		t.Fatal(err)
	}
	// перевірка: NPV у знайденій ставці ~0
	if r < 0.16 || r > 0.17 {
		t.Errorf("XIRR = %.6f, очікували ~0.163", r)
	}
}

func TestXIRRErrors(t *testing.T) {
	if _, err := XIRR([]Flow{{Date: "2025-01-01", Amount: -1}}); err == nil {
		t.Error("один потік має падати")
	}
	if _, err := XIRR([]Flow{
		{Date: "2025-01-01", Amount: -1}, {Date: "2026-01-01", Amount: -2},
	}); err == nil {
		t.Error("потоки одного знаку мають падати")
	}
}

func TestPortfolioFlowsPerCurrency(t *testing.T) {
	bonds := map[string]Bond{
		"UA1": {ISIN: "UA1", Nominal: uah(100000), Maturity: "2027-03-17"},
		"US1": {ISIN: "US1", Nominal: money.New(100000, money.USD), Maturity: "2027-09-17"},
	}
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-06-01", Type: PayCoupon, PerBond: uah(8000)},
	}
	lots := []Lot{
		{ID: 1, ISIN: "UA1", Qty: 5, PricePerBond: uah(98750), BuyDate: "2026-01-10"},
		{ID: 2, ISIN: "US1", Qty: 2, PricePerBond: money.New(99500, money.USD), BuyDate: "2026-02-01"},
	}
	sales := []Sale{{ID: 1, LotID: 1, SaleDate: "2026-07-01", Qty: 2,
		CleanPerBond: uah(99100), Accrued: uah(500)}}

	flows, err := PortfolioFlows(bonds, pays, lots, sales, "UAH", "2026-07-15")
	if err != nil {
		t.Fatal(err)
	}
	// покупка UA1, купон 5×80, продаж 2×991+5, термінал 3×1000 — і ЖОДНОГО USD
	want := map[Date]int64{
		"2026-01-10": -493750,
		"2026-06-01": 40000,
		"2026-07-01": 198700,
		"2026-07-15": 300000,
	}
	if len(flows) != len(want) {
		t.Fatalf("потоки: %+v", flows)
	}
	for _, f := range flows {
		if want[f.Date] != f.Amount {
			t.Errorf("%s: %d, хочемо %d", f.Date, f.Amount, want[f.Date])
		}
	}
	r, err := XIRR(flows)
	if err != nil {
		t.Fatal(err)
	}
	if r < 0.10 || r > 0.30 {
		t.Errorf("XIRR гривневого портфеля неправдоподібний: %.4f", r)
	}
}

// Термінальна вартість у XIRR має включати НКД.
//
// Папір купується «брудним»: у ціну входить купон, що наріс попередньому
// власнику. Якщо оцінювати залишок самим номіналом, ця частина зникає, і
// модель фіксує збиток у мить купівлі. На реальному портфелі це дало XIRR
// −41.96% через тиждень після покупки — при тому, що не втрачено нічого.
func TestPortfolioFlowsTerminalIncludesAccrued(t *testing.T) {
	bonds := map[string]Bond{"UA1": {
		ISIN: "UA1", Nominal: money.New(100000, money.UAH),
		Maturity: "2027-07-01", RateBP: 1600,
	}}
	// Купон раз на рік: 160.00 на папір, найближчий — через півроку.
	pays := []Payment{
		{ISIN: "UA1", PayDate: "2026-01-01", Type: PayCoupon, PerBond: money.New(16000, money.UAH)},
		{ISIN: "UA1", PayDate: "2027-01-01", Type: PayCoupon, PerBond: money.New(16000, money.UAH)},
		{ISIN: "UA1", PayDate: "2027-07-01", Type: PayRedemption, PerBond: money.New(100000, money.UAH)},
	}
	lots := []Lot{{ID: 1, ISIN: "UA1", Qty: 1, PricePerBond: money.New(104000, money.UAH),
		Fee: money.New(0, money.UAH), BuyDate: "2026-07-01"}}

	flows, err := PortfolioFlows(bonds, pays, lots, nil, money.UAH, "2026-07-01")
	if err != nil {
		t.Fatal(err)
	}
	var terminal int64
	for _, f := range flows {
		if f.Date == "2026-07-01" && f.Amount > 0 {
			terminal = f.Amount
		}
	}
	if terminal <= 100000 {
		t.Errorf("термінал %d — це самий номінал; НКД не врахований", terminal)
	}
	// Півроку від січневого купона: ≈ 80.00 з 160.00.
	if terminal < 106000 || terminal > 110000 {
		t.Errorf("термінал %d, чекали ≈108000 (номінал + піврічний НКД)", terminal)
	}
}

func TestRealizedGain(t *testing.T) {
	// −1000, купон +80, залишок оцінено в +1080: заробок +160 на 1000.
	flows := []Flow{
		{Date: "2025-01-01", Amount: -100000},
		{Date: "2025-07-02", Amount: 8000},
		{Date: "2026-01-01", Amount: 108000},
	}
	gain, invested := RealizedGain(flows)
	if gain != 16000 || invested != 100000 {
		t.Errorf("заробок/вкладено = %d/%d, хочемо 16000/100000", gain, invested)
	}
}

// Докупівля збільшує ВКЛАДЕНЕ, а не зменшує заробок: інакше портфель,
// у який щойно занесли грошей, виглядав би так, ніби він їх утратив.
func TestRealizedGainCountsEveryOutflow(t *testing.T) {
	flows := []Flow{
		{Date: "2025-01-01", Amount: -100000},
		{Date: "2025-06-01", Amount: -50000},
		{Date: "2026-01-01", Amount: 145000},
	}
	gain, invested := RealizedGain(flows)
	if gain != -5000 || invested != 150000 {
		t.Errorf("заробок/вкладено = %d/%d, хочемо -5000/150000", gain, invested)
	}
}

// convOps — портфель із конвертацією, зведений до суті з бойових даних:
// Житній набрали за 9 059,92, у вересні 2025-го він перетворився на REIT
// за 9 193,05, і згори довелось докласти 6,95. Ноги показують одна на
// одну через PairID — так їх і кладе імпорт виписки.
func convOps() []FundOp {
	return []FundOp{
		{ID: 1, Date: "2024-10-02", Fund: "Житній", Kind: FundBuy,
			Qty: 9, Amount: 905992, Currency: "UAH"},
		{ID: 2, Date: "2025-09-03", Fund: "Житній", Kind: FundSell,
			Qty: 9, Amount: 919305, Currency: "UAH", PairID: 3},
		{ID: 3, Date: "2025-09-03", Fund: "REIT", Kind: FundBuy,
			Qty: 920, Amount: 920000, Currency: "UAH", PairID: 2},
	}
}

// Конвертація — один потік на різницю, а не два на повні суми.
func TestFundFlowsPairIsNetOnly(t *testing.T) {
	flows := FundFlows(convOps(), nil, "UAH", "2026-09-03")
	var onConv []Flow
	for _, f := range flows {
		if f.Date == "2025-09-03" {
			onConv = append(onConv, f)
		}
	}
	if len(onConv) != 1 {
		t.Fatalf("на дату конвертації мав бути один потік, маємо %d: %+v", len(onConv), onConv)
	}
	if onConv[0].Amount != -695 {
		t.Errorf("потік = %d, хочемо -695 (сама лише доплата)", onConv[0].Amount)
	}
}

// Головне: переказ між фондами не є вкладеними грішми.
//
// Доти нога купівлі проходила в RealizedGain як свіжий капітал, і на
// бойових даних знаменник роздувався на 17 380 грн — відсоток прибутку
// виходив занижений у півтора раза.
func TestFundFlowsPairDoesNotInflateInvested(t *testing.T) {
	ops := convOps()
	_, invested := RealizedGain(FundFlows(ops, nil, "UAH", "2026-09-03"))
	// Справді вкладено: 9 059,92 купівлі Житнього плюс 6,95 доплати.
	if invested != 906687 {
		t.Errorf("вкладено %d, хочемо 906687 (905992 + 695)", invested)
	}
	if invested >= 905992+920000 {
		t.Errorf("нога конвертації порахувалась вкладенням: %d", invested)
	}
}

// А ставка й прибуток мусять лишитись тими самими: у самому XIRR ноги
// гасились і доти, бо стоять на одну дату. Тест порівнює зведений список
// із тим, який був ДО зміни — дві повні ноги замість різниці, — і стереже,
// щоб зведення не зсунуло нічого, що вже було правильним.
func TestFundFlowsPairKeepsXIRRAndGain(t *testing.T) {
	marks := []FundPrice{{Fund: "REIT", Date: "2026-09-03", Price: 111114}}
	netted := FundFlows(convOps(), marks, "UAH", "2026-09-03")

	// Той самий список, але з парою в дві ноги, як його будував старий код.
	var legs []Flow
	for _, f := range netted {
		if f.Date == "2025-09-03" && f.Amount == -695 {
			legs = append(legs,
				Flow{Date: "2025-09-03", Amount: 919305},
				Flow{Date: "2025-09-03", Amount: -920000})
			continue
		}
		legs = append(legs, f)
	}
	if len(legs) != len(netted)+1 {
		t.Fatalf("не знайшов зведеного потоку пари: %+v", netted)
	}

	gainNet, investedNet := RealizedGain(netted)
	gainLegs, investedLegs := RealizedGain(legs)
	if gainNet != gainLegs {
		t.Errorf("прибуток зсунувся: %d проти %d", gainNet, gainLegs)
	}
	// А ось ЦЕ і є вада, заради якої все. Надлишок дорівнює сумі ноги
	// ПРОДАЖУ: доти в invested заходила ціла нога купівлі (920 000), тепер
	// заходить сама доплата (695), і різниця між ними — 919 305, тобто
	// рівно те, що продаж приніс і що ніхто заново не вкладав.
	if investedLegs-investedNet != 919305 {
		t.Errorf("вкладене змінилось на %d, хочемо 919305", investedLegs-investedNet)
	}

	rateNet, err := XIRR(netted)
	if err != nil {
		t.Fatal(err)
	}
	rateLegs, err := XIRR(legs)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(rateNet-rateLegs) > 1e-9 {
		t.Errorf("ставка зсунулась: %.9f проти %.9f", rateNet, rateLegs)
	}
}

// Середній вік грошей не молодшає від переказу: ті самі гроші не можна
// вкласти двічі.
func TestMoneyWeightedDaysIgnoresConversion(t *testing.T) {
	got := MoneyWeightedDays(FundFlows(convOps(), nil, "UAH", "2026-09-03"), "2026-09-03")
	// Майже все вкладене — купівля 2024-10-02, тобто 701 день.
	if got < 690 {
		t.Errorf("середній вік %.1f дня — конвертація порахувалась новими грішми", got)
	}
}

// Дзеркало: у власній дохідності фонду-джерела конвертація ЛИШАЄТЬСЯ
// виходом, інакше в Житнього була б сама купівля й жодного повернення.
func TestFundFlowsOneSeesConversionAsExit(t *testing.T) {
	flows := FundFlowsOne(convOps(), nil, "Житній", "2026-09-03")
	var in int64
	for _, f := range flows {
		if f.Amount > 0 {
			in += f.Amount
		}
	}
	if in != 919305 {
		t.Errorf("надходження фонду = %d, хочемо 919305", in)
	}
	if _, err := XIRR(flows); err != nil {
		t.Errorf("дохідність фонду не порахувалась: %v", err)
	}
}
