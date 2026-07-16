// Package state будує документ oddinvest/state — контракт між Go-сервісом
// та інтеграцією Home Assistant (репо ha-oddinvest).
//
// Правила еволюції: тільки додавання полів (backward compatible);
// зміна семантики існуючого поля = інкремент Schema. JSON Schema і
// фікстури — у каталозі contract/ цього репозиторію; CI інтеграції
// ганяє свої парсери проти цих фікстур.
//
// Суми в документі — у МАЖОРНИХ одиницях (грн, долари) як number з
// двома знаками: це шар відображення для HA-шаблонів. Уся точна
// математика залишається в мінорних одиницях до цієї межі.
package state

import (
	"encoding/json"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
)

const SchemaVersion = 1

type Doc struct {
	Schema      int    `json:"schema"`
	GeneratedAt string `json:"generated_at"` // RFC3339

	InvestedUAH   float64 `json:"invested_uah"`   // вартість входу залишків, грн-екв.
	NominalUAHEq  float64 `json:"nominal_uah_eq"` // номінал портфеля, грн-екв.
	USDSharePct   float64 `json:"usd_share_pct"`  // частка USD-паперів за номіналом (грн-екв.)
	EURSharePct   float64 `json:"eur_share_pct"`  // частка EUR-паперів за номіналом (грн-екв.)
	UninvestedUAH float64 `json:"uninvested_uah"` // надійшло і не перевкладено, грн-екв.

	// Грошовий рахунок (гаманець). AccountUAH — сумарний баланс у грн-екв.
	// (для «Разом» і плитки). Accounts — баланси по валютах (нативно).
	// ReinvestMinUAH — ціна найдешевшого паперу (грн-екв.); ReinvestMin —
	// найдешевший папір по кожній валюті (нативно) для по-валютного CTA.
	AccountUAH     float64            `json:"account_uah"`
	ReinvestMinUAH float64            `json:"reinvest_min_uah"`
	Accounts       map[string]float64 `json:"accounts"`
	ReinvestMin    map[string]float64 `json:"reinvest_min"`

	MonthInvestedUAH float64 `json:"month_invested_uah"`
	MonthTargetUAH   float64 `json:"month_target_uah"`
	MonthProgressPct int     `json:"month_progress_pct"`
	MonthIncomingUAH float64 `json:"month_incoming_uah"` // купони+погашення в поточному місяці

	NextPayment *NextPayment `json:"next_payment,omitempty"`

	Ladder      []LadderRow  `json:"ladder"`
	TopPayments []PaymentRow `json:"top_payments"` // найближчі N виплат
	Calendar    []PaymentRow `json:"calendar"`     // повний горизонт майбутніх виплат

	// XIRRPct — річна дохідність портфеля по валютах, % (v1.0+,
	// адитивне поле). Відсутня валюта = XIRR нерахований (мало потоків).
	XIRRPct map[string]float64 `json:"xirr,omitempty"`

	// Settings — сирі налаштування сервіса (v0.3+, адитивне поле).
	// Потрібні HA для number/date-сутностей: значення приходять сюди
	// MQTT-пушем, зміни йдуть у PUT /api/settings.
	Settings *SettingsDoc `json:"settings,omitempty"`
}

type SettingsDoc struct {
	MonthlyTargetUAH  *float64 `json:"monthly_target_uah,omitempty"`
	USDTargetSharePct *float64 `json:"usd_target_share_pct,omitempty"`
	EURTargetSharePct *float64 `json:"eur_target_share_pct,omitempty"`
	// v Phase 3: проєкції/цілі
	AssumedRatePct *float64 `json:"assumed_rate_pct,omitempty"` // очікувана річна дохідність, % (fallback до XIRR)
	GoalAmountUAH  *float64 `json:"goal_amount_uah,omitempty"`  // цільова сума капіталу, грн
	GoalDate       string   `json:"goal_date,omitempty"`        // цільова дата (ISO)
}

type NextPayment struct {
	Date     string  `json:"date"` // ISO
	ISIN     string  `json:"isin"`
	Type     string  `json:"type"` // coupon | redemption | early
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type LadderRow struct {
	Year int     `json:"year"`
	UAH  float64 `json:"uah"`
	USD  float64 `json:"usd"` // номінал у доларах (не еквівалент)
	EUR  float64 `json:"eur"` // номінал у євро (не еквівалент)
}

type PaymentRow struct {
	Date     string  `json:"date"`
	ISIN     string  `json:"isin"`
	Type     string  `json:"type"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// Input — все, що потрібно для побудови документа (домен уже порахований).
type Input struct {
	Now               time.Time
	Positions         []domain.Position
	Cashflow          []domain.CashflowItem // майбутні виплати від сьогодні
	Ladder            []domain.LadderEntry
	Rates             fx.Rates
	MonthInvestedUAH *money.Money
	MonthTargetUAH   *money.Money
	UninvestedUAH    *money.Money
	AccountUAH       *money.Money
	ReinvestMinUAH   *money.Money
	Accounts         map[string]float64
	ReinvestMinByCur map[string]float64
	TopN             int
	Settings         *SettingsDoc
	XIRRPct          map[string]float64
}

func payTypeStr(t domain.PayType) string {
	switch t {
	case domain.PayCoupon:
		return "coupon"
	case domain.PayRedemption:
		return "redemption"
	case domain.PayEarly:
		return "early"
	}
	return "unknown"
}

func major(m *money.Money) float64 {
	if m == nil {
		return 0
	}
	return float64(m.Amount()) / 100.0
}

// Build збирає документ стану.
func Build(in Input) (*Doc, error) {
	doc := &Doc{
		Schema:      SchemaVersion,
		GeneratedAt: in.Now.UTC().Format(time.RFC3339),
		Ladder:      []LadderRow{},
		TopPayments: []PaymentRow{},
		Calendar:    []PaymentRow{},
	}

	var investedUAH, nominalUAH, nominalUSD, nominalEUR int64
	for _, p := range in.Positions {
		inv, err := fx.ToUAH(p.Invested, in.Rates)
		if err != nil {
			return nil, err
		}
		nom, err := fx.ToUAH(p.Nominal, in.Rates)
		if err != nil {
			return nil, err
		}
		investedUAH += inv.Amount()
		nominalUAH += nom.Amount()
		switch p.Currency {
		case money.USD:
			nominalUSD += nom.Amount()
		case money.EUR:
			nominalEUR += nom.Amount()
		}
	}
	doc.InvestedUAH = float64(investedUAH) / 100
	doc.NominalUAHEq = float64(nominalUAH) / 100
	// частки рахуються від усього капіталу: папери за номіналом + кеш на
	// рахунку (гривня). Так вільний кеш «розводить» валютні частки.
	accountMinor := int64(0)
	if in.AccountUAH != nil {
		accountMinor = in.AccountUAH.Amount()
	}
	capital := nominalUAH + accountMinor
	if capital > 0 {
		doc.USDSharePct = float64(nominalUSD) * 100 / float64(capital)
		doc.EURSharePct = float64(nominalEUR) * 100 / float64(capital)
	}

	doc.MonthInvestedUAH = major(in.MonthInvestedUAH)
	doc.MonthTargetUAH = major(in.MonthTargetUAH)
	doc.MonthProgressPct = domain.ProgressPct(in.MonthInvestedUAH, in.MonthTargetUAH)
	doc.UninvestedUAH = major(in.UninvestedUAH)
	doc.AccountUAH = major(in.AccountUAH)
	doc.ReinvestMinUAH = major(in.ReinvestMinUAH)
	doc.Accounts = in.Accounts
	doc.ReinvestMin = in.ReinvestMinByCur
	if doc.Accounts == nil {
		doc.Accounts = map[string]float64{}
	}
	if doc.ReinvestMin == nil {
		doc.ReinvestMin = map[string]float64{}
	}
	doc.Settings = in.Settings
	doc.XIRRPct = in.XIRRPct

	nowDate := domain.NewDate(in.Now)
	var monthIncoming int64
	for _, cf := range in.Cashflow {
		if cf.Date.Year() == in.Now.Year() && cf.Date.Month() == in.Now.Month() {
			uahAmt, err := fx.ToUAH(cf.Amount, in.Rates)
			if err != nil {
				return nil, err
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
			Amount:   major(cf.Amount),
			Currency: cf.Amount.Currency().Code,
		}
		break
	}

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
			row.UAH = float64(le.Nominal) / 100
		case money.USD:
			row.USD = float64(le.Nominal) / 100
		case money.EUR:
			row.EUR = float64(le.Nominal) / 100
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
			Amount:   major(cf.Amount),
			Currency: cf.Amount.Currency().Code,
		}
		doc.Calendar = append(doc.Calendar, row)
		if i < topN {
			doc.TopPayments = append(doc.TopPayments, row)
		}
	}
	return doc, nil
}

func (d *Doc) JSON() ([]byte, error) { return json.Marshal(d) }
