// Стрічка часу плану: усе, що на ній малюється, одним документом.
//
// Лишається в REST, у MQTT-контракт не йде — той самий прецедент, що й
// AuctionPoint (фаза 7, A5): дані для картинки, а не для сутностей Home
// Assistant.
package api

import (
	"net/http"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"

	money "github.com/Rhymond/go-money"
)

type timelineFlow struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	Cadence   string  `json:"cadence"`
	From      string  `json:"from"`
	Until     string  `json:"until,omitempty"`
	AmountUAH float64 `json:"amount_uah"`
}

type timelineAction struct {
	ID        int64   `json:"id"`
	Date      string  `json:"date"`
	Type      string  `json:"type"`
	Name      string  `json:"name,omitempty"`
	AmountUAH float64 `json:"amount_uah,omitempty"`
}

// timelineInstrument — термін реального інструмента: коли він повертає
// гроші (To) чи, для фонду, коли зачиняється вікно купівлі (BuyUntil).
type timelineInstrument struct {
	Kind     string `json:"kind"` // bond | deposit | fund
	Label    string `json:"label"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	BuyUntil string `json:"buy_until,omitempty"`
}

type timelineMilestone struct {
	Date  string `json:"date"`
	Label string `json:"label"`
}

type timelineCurvePoint struct {
	Date        string  `json:"date"`
	Plan        float64 `json:"plan"`
	Optimistic  float64 `json:"optimistic,omitempty"`
	Pessimistic float64 `json:"pessimistic,omitempty"`
	Actual      float64 `json:"actual,omitempty"`
}

type timelineDoc struct {
	From        string               `json:"from"`
	To          string               `json:"to"`
	Flows       []timelineFlow       `json:"flows"`
	Actions     []timelineAction     `json:"actions"`
	Instruments []timelineInstrument `json:"instruments"`
	Milestones  []timelineMilestone  `json:"milestones"`
	Curve       []timelineCurvePoint `json:"curve,omitempty"`
}

// handlePlanTimeline — GET /api/plan. Той самий buildState, що й
// /api/summary (тож дедлайн цілі, крива й точка незалежності — ті самі
// числа, що й на «Майбутньому»), плюс сирі терміни інструментів, яких у
// state.Doc немає: вони потрібні лише картинці, а не сутностям HA.
func (s *Server) handlePlanTimeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	today := domain.NewDate(now)

	doc, err := s.buildState(ctx, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	flows, err := s.st.ListPlanFlows(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	actions, err := s.st.ListPlanActions(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	lots, sales, bonds, pays, err := s.portfolio(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	positions, err := domain.Positions(bonds, pays, lots, sales, today)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rates, err := s.rates(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Свідомо ковтані, як і в state_sources.go: порожній довідник вкладів
	// чи фондів — звичайний стан, не привід валити стрічку.
	termDeposits, _ := s.st.ListTermDeposits(ctx) //nolint:errcheck
	funds, _ := s.st.ListFunds(ctx)               //nolint:errcheck

	out := timelineDoc{From: string(today)}

	// Горизонт — дедлайн цілі, якщо він далі за мінімум у рік; інакше рік
	// наперед. Той самий мінімум, що й у решти «Майбутнього».
	to := today.AddMonths(12)
	if doc.Forecast != nil && domain.Date(doc.Forecast.Date).After(to) {
		to = domain.Date(doc.Forecast.Date)
	}
	out.To = string(to)

	for _, f := range flows {
		amt := float64(f.Amount) / 100
		if f.Currency != "UAH" {
			if u, cerr := fx.ToUAH(money.New(f.Amount, f.Currency), rates); cerr == nil {
				amt = float64(u.Amount()) / 100
			}
		}
		// invest_bp: стрічка показує, скільки з потоку РЕАЛЬНО йде в
		// портфель — те саме число, що вже рахує plan_provides_uah,
		// інакше смуга на картинці не сходилась би з вердиктом над нею.
		amt *= float64(f.InvestBP) / 10000
		if f.Kind == "expense" {
			amt = -amt
		}
		out.Flows = append(out.Flows, timelineFlow{
			ID: f.ID, Name: f.Name, Kind: f.Kind, Cadence: f.Cadence,
			From: string(f.FromDate), Until: string(f.UntilDate), AmountUAH: round2(amt),
		})
	}

	for _, a := range actions {
		ta := timelineAction{ID: a.ID, Date: string(a.Date), Type: a.Type, Name: a.Name}
		if a.Type == "lock" {
			amt := float64(a.Amount) / 100
			if a.Currency != "UAH" {
				if u, cerr := fx.ToUAH(money.New(a.Amount, a.Currency), rates); cerr == nil {
					amt = float64(u.Amount()) / 100
				}
			}
			ta.AmountUAH = round2(amt)
		}
		out.Actions = append(out.Actions, ta)
	}

	// Погашення ОВДП — лише те, що ще в портфелі (Qty > 0): погашений чи
	// проданий папір на стрічці майбутнього не термін, а історія.
	for _, p := range positions {
		if p.Qty <= 0 || !p.Maturity.Valid() {
			continue
		}
		out.Instruments = append(out.Instruments,
			timelineInstrument{Kind: "bond", Label: p.ISIN, To: string(p.Maturity)})
	}
	for _, d := range termDeposits {
		if !d.Active(today) {
			continue
		}
		out.Instruments = append(out.Instruments, timelineInstrument{
			Kind: "deposit", Label: d.Bank, From: string(d.OpenDate), To: string(d.MaturityDate),
		})
	}
	for _, f := range funds {
		if f.BuyUntil == "" && f.CloseDate == "" {
			continue
		}
		out.Instruments = append(out.Instruments,
			timelineInstrument{Kind: "fund", Label: f.Name, To: f.CloseDate, BuyUntil: f.BuyUntil})
	}

	out.Milestones = append(out.Milestones, timelineMilestone{Date: string(today), Label: "сьогодні"})
	if doc.Forecast != nil && doc.Forecast.Date != "" {
		out.Milestones = append(out.Milestones, timelineMilestone{Date: doc.Forecast.Date, Label: "дедлайн цілі"})
	}
	if doc.Independence != nil && doc.Independence.PlanDate != "" {
		out.Milestones = append(out.Milestones,
			timelineMilestone{Date: doc.Independence.PlanDate, Label: "точка незалежності"})
	}

	if doc.Forecast != nil && doc.Forecast.Curve != nil {
		for _, p := range doc.Forecast.Curve.Points {
			out.Curve = append(out.Curve, timelineCurvePoint{
				Date: string(today.AddMonths(p.Month)), Plan: p.Plan,
				Optimistic: p.Optimistic, Pessimistic: p.Pessimistic, Actual: p.Actual,
			})
		}
	}

	writeJSON(w, http.StatusOK, out)
}
