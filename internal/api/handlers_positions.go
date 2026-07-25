// Позиції, календар виплат і драбина погашень.

package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
)

func (s *Server) handlePositions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lots, sales, bonds, pays, err := s.portfolio(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	today := domain.NewDate(time.Now())
	pos, err := domain.Positions(bonds, pays, lots, sales, today)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type posJSON struct {
		ISIN      string    `json:"isin"`
		Currency  string    `json:"currency"`
		Qty       int64     `json:"qty"`
		Invested  moneyJSON `json:"invested"`
		Nominal   moneyJSON `json:"nominal"`
		Maturity  string    `json:"maturity"`
		DaysToMat int       `json:"days_to_maturity"`
		NextDate  string    `json:"next_pay_date,omitempty"`
		NextAmt   moneyJSON `json:"next_pay_amount"`
		// YTMPct — дохідність до погашення за ТВОЄЮ собівартістю (з
		// комісією), а не за сьогоднішньою ціною довідника: питання тут
		// «скільки заробляю я», а не «скільки платить папір».
		// RealPct — вона ж після знецінення, тобто в сьогоднішній
		// купівельній спроможності. Саме RealPct порівнянний із фондом і
		// вкладом; YieldBasis каже, з чого число взялося.
		YTMPct     float64 `json:"ytm_pct,omitempty"`
		RealPct    float64 `json:"real_pct,omitempty"`
		YieldBasis string  `json:"yield_basis,omitempty"`
	}

	// Дохідність рахуємо по ISIN: позиція — це всі непродані лоти одного
	// паперу, і взята вона зважено по вкладеному, як і зведена цифра.
	deval := s.devaluation(ctx)
	ytmByISIN := map[string][]domain.YTMLot{}
	for _, l := range lots {
		b, ok := bonds[l.ISIN]
		if !ok || b.Maturity.Before(today) {
			continue
		}
		q := domain.RemainingQtyNow(l, sales)
		if q == 0 {
			continue
		}
		ytmByISIN[l.ISIN] = append(ytmByISIN[l.ISIN], ytmLot(l, q))
	}

	out := make([]posJSON, 0, len(pos))
	for _, p := range pos {
		row := posJSON{ISIN: p.ISIN, Currency: p.Currency, Qty: p.Qty,
			Invested: toMoneyJSON(p.Invested), Nominal: toMoneyJSON(p.Nominal),
			Maturity: string(p.Maturity), DaysToMat: p.DaysToMat,
			NextDate: string(p.NextPayDate), NextAmt: toMoneyJSON(p.NextPayAmt)}
		// WeightedYTM віддає вже ВІДСОТКИ (ytm.go), на відміну від YTM,
		// що віддає частку. realYield же працює з часткою — звідси /100.
		if y, ok := domain.WeightedYTM(ytmByISIN[p.ISIN], pays); ok {
			row.YTMPct = round2(y)
			row.RealPct = round2(realYield(y/100, p.Currency, deval) * 100)
			row.YieldBasis = "до погашення"
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCalendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lots, sales, _, pays, err := s.portfolio(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	from := domain.NewDate(time.Now())
	if q := r.URL.Query().Get("from"); q != "" {
		if d, err := domain.ParseDate(q); err == nil {
			from = d
		}
	}
	cf, err := domain.FuturePayments(pays, lots, sales, from)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	deposits, err := s.st.ListTermDeposits(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	cf = append(cf, domain.DepositCashflows(deposits, from)...)
	sort.Slice(cf, func(i, j int) bool { return cf[i].Date < cf[j].Date })
	statuses, err := s.st.PaymentStatuses(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type cfJSON struct {
		Date   string    `json:"date"`
		ISIN   string    `json:"isin"`
		Type   int       `json:"type"`
		Amount moneyJSON `json:"amount"`
		Status string    `json:"status,omitempty"`
	}
	out := make([]cfJSON, 0, len(cf))
	for _, item := range cf {
		out = append(out, cfJSON{string(item.Date), item.ISIN, int(item.Type),
			toMoneyJSON(item.Amount), statuses[item.ISIN+"|"+string(item.Date)]})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleLadder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lots, sales, bonds, _, err := s.portfolio(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	now := domain.NewDate(time.Now())
	ladder := domain.Ladder(bonds, lots, sales, now)
	deposits, err := s.st.ListTermDeposits(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	ladder = append(ladder, domain.DepositLadder(deposits, now)...)
	sort.Slice(ladder, func(i, j int) bool {
		if ladder[i].Year != ladder[j].Year {
			return ladder[i].Year < ladder[j].Year
		}
		return ladder[i].Currency < ladder[j].Currency
	})
	writeJSON(w, http.StatusOK, ladder)
}
