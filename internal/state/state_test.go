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

func sampleInput(t *testing.T) Input {
	t.Helper()
	return Input{
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
		Rates:             fx.Rates{"USD": 441234},
		MonthInvestedUAH:  money.New(450_000, money.UAH),
		MonthDepositedUAH: money.New(450_000, money.UAH),
		MonthTargetUAH:    money.New(500_000, money.UAH),
		UninvestedUAH:     money.New(0, money.UAH),
		TopN:              5,
		Settings: func() *SettingsDoc {
			t, u := 5000.0, 50.0
			return &SettingsDoc{
				MonthlyTargetUAH:  &t,
				USDTargetSharePct: &u,
			}
		}(),
		XIRRPct: map[string]float64{"UAH": 16.51, "USD": 3.22},
	}
}

func TestBuild(t *testing.T) {
	doc, err := Build(sampleInput(t))
	if err != nil {
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

func TestBuildEmptyPortfolio(t *testing.T) {
	doc, err := Build(Input{Now: time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)})
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
	doc, err := Build(sampleInput(t))
	if err != nil {
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
