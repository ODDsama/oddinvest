package state

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
)

// sampleDoc — документ у тому вигляді, у якому його заповнює будівник:
// прямі поля кладуться в Doc, похідні добудовує Derive.
//
// Доти тут був один літерал Input на пʼятдесят полів, і він працював ще й
// як чек-лист: видно було, що подано все. Тепер прямі поля й входи
// похідних розділені, і саме це розділення й перевіряється.
func sampleDoc(t *testing.T) (*Doc, DeriveInput) {
	t.Helper()
	monthDep := money.New(450_000, money.UAH)
	monthTarget := money.New(500_000, money.UAH)
	settings := func() *SettingsDoc {
		tgt, u := 5000.0, 50.0
		return &SettingsDoc{MonthlyTargetUAH: &tgt, USDTargetSharePct: &u}
	}()
	doc := &Doc{
		MonthInvestedUAH:  Major(money.New(450_000, money.UAH)),
		MonthDepositedUAH: Major(monthDep),
		MonthTargetUAH:    Major(monthTarget),
		UninvestedUAH:     Major(money.New(0, money.UAH)),
		Settings:          settings,
		XIRRPct:           map[string]float64{"UAH": 16.51, "USD": 3.22},
	}
	in := DeriveInput{
		Now: time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC),
		Positions: []domain.Position{
			{ISIN: "UA4000227748", Currency: "UAH", Qty: 50,
				Invested: money.New(4_950_000, money.UAH), Nominal: money.New(5_000_000, money.UAH),
				Maturity: "2027-03-17"},
			{ISIN: "UA4000230114", Currency: "USD", Qty: 2,
				Invested: money.New(199_000, money.USD), Nominal: money.New(200_000, money.USD),
				Maturity: "2027-09-17"},
		},
		Cashflow: []domain.CashflowItem{
			{Date: "2026-07-20", ISIN: "UA4000227748", Type: domain.PayCoupon, Amount: money.New(413_750, money.UAH)},
			{Date: "2026-09-16", ISIN: "UA4000227748", Type: domain.PayCoupon, Amount: money.New(413_750, money.UAH)},
			{Date: "2027-03-17", ISIN: "UA4000227748", Type: domain.PayRedemption, Amount: money.New(5_000_000, money.UAH)},
		},
		Ladder: []domain.LadderEntry{
			{Year: 2027, Currency: "UAH", Nominal: 5_000_000},
			{Year: 2027, Currency: "USD", Nominal: 200_000},
		},
		Rates: fx.Rates{"USD": 441234},
		// Capital подається ГОТОВИМ, як і в живому будівнику: цей пакет
		// його більше не збирає. Числа мусять відповідати Positions вище —
		// 50 000 ₴ номіналу гривневих плюс $2 000 × 44.1234 = 88 246.80 ₴.
		Capital: Capital{
			BondsUAH:   50_000 + 88_246.80,
			BondsByCur: map[string]float64{money.USD: 88_246.80},
		},
		MonthDeposited: monthDep,
		MonthTarget:    monthTarget,
		TopN:           5,
	}
	return doc, in
}

func TestDerive(t *testing.T) {
	doc, in := sampleDoc(t)
	if err := Derive(doc, in); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != 1 {
		t.Errorf("schema = %d", doc.Schema)
	}
	// invested: 49500 грн + $1990×44.1234 = 49500 + 87805.57 (банківське) = 137305.57
	if doc.InvestedUAH != 137305.57 {
		t.Errorf("invested_uah = %v", doc.InvestedUAH)
	}
	if doc.NextPayment == nil || doc.NextPayment.Date != "2026-07-20" || doc.NextPayment.Type != "coupon" {
		t.Errorf("next_payment: %+v", doc.NextPayment)
	}
	// Прогрес рахується від ПОПОВНЕНЬ, а не купівель: план виведений із
	// цілі й означає нові гроші.
	if doc.MonthProgressPct != 90 {
		t.Errorf("progress = %d", doc.MonthProgressPct)
	}
	if doc.MonthDepositedUAH != 4500 {
		t.Errorf("month_deposited = %v", doc.MonthDepositedUAH)
	}
	if doc.MonthIncomingUAH != 4137.50 {
		t.Errorf("month_incoming = %v", doc.MonthIncomingUAH)
	}
	if len(doc.Ladder) != 1 || doc.Ladder[0].UAH != 50000 || doc.Ladder[0].USD != 2000 {
		t.Errorf("ladder: %+v", doc.Ladder)
	}
	if doc.USDSharePct < 63 || doc.USDSharePct > 64.5 {
		t.Errorf("usd_share_pct = %v", doc.USDSharePct)
	}
	if len(doc.Calendar) != 3 || doc.Calendar[2].Type != "redemption" {
		t.Errorf("calendar: %+v", doc.Calendar)
	}
	if doc.Settings == nil || *doc.Settings.MonthlyTargetUAH != 5000 {
		t.Errorf("settings: %+v", doc.Settings)
	}
	if doc.XIRRPct["UAH"] != 16.51 {
		t.Errorf("xirr: %+v", doc.XIRRPct)
	}
}

// TestDeriveNextPaymentSkipsPast — «найближча виплата» не може бути
// вчорашньою.
//
// Сьогодні цю перевірку не видно ні в golden, ні у фікстурі: будівник
// складає календар уже відфільтрованим від сьогоднішнього дня, тож
// минулих виплат у ньому не буває, і мутація «прибрати фільтр» нічого не
// ламає. Тобто вона захисна, а не жива.
//
// Прибирати її через це не варто: Derive — публічна межа пакета, і
// нізвідки не випливає, що календар завжди прийде обрізаним. Але
// незакрита захисна перевірка нічим не краща за її відсутність, тож
// перевіряємо прямо — подаємо календар із минулим у ньому.
func TestDeriveNextPaymentSkipsPast(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	doc := &Doc{}
	err := Derive(doc, DeriveInput{
		Now: now,
		Cashflow: []domain.CashflowItem{
			{Date: "2026-06-01", ISIN: "UA1", Type: domain.PayCoupon, Amount: money.New(100_00, money.UAH)},
			{Date: "2026-08-01", ISIN: "UA2", Type: domain.PayCoupon, Amount: money.New(200_00, money.UAH)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.NextPayment == nil {
		t.Fatal("найближчої виплати немає, хоч майбутня в календарі є")
	}
	if doc.NextPayment.Date != "2026-08-01" {
		t.Errorf("найближча виплата %q — це вчорашній день, а не наступний",
			doc.NextPayment.Date)
	}
	// Календар при цьому лишається ПОВНИМ: він відповідає на інше питання,
	// і викидати з нього минуле означало б зламати звірку.
	if len(doc.Calendar) != 2 {
		t.Errorf("у календарі %d рядків, очікували 2 — минуле з нього не зникає", len(doc.Calendar))
	}
}

func TestDeriveEmptyPortfolio(t *testing.T) {
	doc := &Doc{}
	err := Derive(doc, DeriveInput{Now: time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if doc.InvestedUAH != 0 || doc.NextPayment != nil || len(doc.Calendar) != 0 {
		t.Errorf("порожній портфель: %+v", doc)
	}
}

// TestFixtureUpToDate гарантує, що фікстура контракту в contract/fixtures
// зібрана саме цим кодом: інтеграція (репо ha-oddinvest) тестується проти неї.
func TestFixtureUpToDate(t *testing.T) {
	doc, in := sampleDoc(t)
	if err := Derive(doc, in); err != nil {
		t.Fatal(err)
	}
	got, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../contract/fixtures/basic.json")
	if err != nil {
		t.Fatalf("фікстура відсутня: %v (перегенеруй: go test ./internal/state -run TestFixtureUpToDate -update)", err)
	}
	if string(got)+"\n" != string(want) {
		t.Errorf("фікстура застаріла — перегенеруй з -update\nмаємо:\n%s", got)
	}
}
