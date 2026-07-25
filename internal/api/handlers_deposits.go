// Банківські строкові вклади й поповнення до них.
//
// Саме інструмент (term_deposits), а не рух коштів: у вкладу є строк,
// фіксована ставка й податок на відсотки. Рух грошей — у handlers_cash.go.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	money "github.com/Rhymond/go-money"
)

type termDepositReq struct {
	Bank          string `json:"bank"`
	Currency      string `json:"currency"`
	Principal     string `json:"principal"`
	RatePct       string `json:"rate_pct"`
	OpenDate      string `json:"open_date"`
	MaturityDate  string `json:"maturity_date"`
	Payout        string `json:"payout"`
	Capitalized   bool   `json:"capitalized"`
	Replenishable bool   `json:"replenishable"`
	TaxPct        string `json:"tax_pct"`
	ClosedDate    string `json:"closed_date"`
	ClosedAmount  string `json:"closed_amount"`
	Note          string `json:"note"`
}

// parsePercentBP: "16.5" -> 1650. Ставки й податок вводяться відсотками,
// а зберігаються базисними пунктами (×100), як RateBP у Bond.
func parsePercentBP(s string) (int64, error) {
	return domain.ParseDecimalToMinor(strings.TrimSpace(s), money.UAH)
}

func termDepositFromReq(req termDepositReq) (domain.Deposit, error) {
	var out domain.Deposit
	cur := strings.TrimSpace(req.Currency)
	if cur == "" {
		cur = money.UAH
	}
	principal, err := domain.ParseDecimalToMinor(req.Principal, cur)
	if err != nil {
		return out, err
	}
	if principal <= 0 {
		return out, errors.New("тіло вкладу має бути > 0")
	}
	rate, err := parsePercentBP(req.RatePct)
	if err != nil {
		return out, fmt.Errorf("ставка: %w", err)
	}
	open, err := domain.ParseDate(req.OpenDate)
	if err != nil {
		return out, fmt.Errorf("дата відкриття: %w", err)
	}
	mat, err := domain.ParseDate(req.MaturityDate)
	if err != nil {
		return out, fmt.Errorf("дата погашення: %w", err)
	}
	if !open.Before(mat) {
		return out, errors.New("дата погашення має бути пізніше за відкриття")
	}
	payout := domain.DepositPayout(strings.TrimSpace(req.Payout))
	switch payout {
	case "":
		payout = domain.PayoutEnd
	case domain.PayoutEnd, domain.PayoutMonthly, domain.PayoutQuarterly:
	default:
		return out, fmt.Errorf("payout має бути end, monthly або quarterly, маємо %q", req.Payout)
	}
	// Податок за замовчуванням 19.5% (ПДФО 18% + військовий збір 1.5%);
	// порожнє поле = це значення, а не «без податку».
	tax := int64(1950)
	if strings.TrimSpace(req.TaxPct) != "" {
		if tax, err = parsePercentBP(req.TaxPct); err != nil {
			return out, fmt.Errorf("податок: %w", err)
		}
	}
	out = domain.Deposit{
		Bank: strings.TrimSpace(req.Bank), Currency: cur, Principal: principal,
		RateBP: rate, OpenDate: open, MaturityDate: mat, Payout: payout,
		Capitalized: req.Capitalized, TaxBP: tax, Note: req.Note,
		Replenishable: req.Replenishable,
	}
	// Дострокове розірвання: обидва поля разом або жодного.
	if strings.TrimSpace(req.ClosedDate) != "" {
		cd, err := domain.ParseDate(req.ClosedDate)
		if err != nil {
			return out, fmt.Errorf("дата розірвання: %w", err)
		}
		ca, err := domain.ParseDecimalToMinor(req.ClosedAmount, cur)
		if err != nil {
			return out, fmt.Errorf("сума розірвання: %w", err)
		}
		out.ClosedDate, out.ClosedAmount = cd, ca
	}
	return out, nil
}

func (s *Server) handleTermDeposits(w http.ResponseWriter, r *http.Request) {
	deps, err := s.st.ListTermDeposits(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type topupJSON struct {
		ID     int64     `json:"id"`
		Date   string    `json:"date"`
		Amount moneyJSON `json:"amount"`
	}
	type row struct {
		ID        int64     `json:"id"`
		Bank      string    `json:"bank,omitempty"`
		Principal moneyJSON `json:"principal"`
		// Balance — накопичене тіло (початкове + поповнення) на сьогодні:
		// UI показує саме його, а principal лишається сумою відкриття.
		Balance       moneyJSON   `json:"balance"`
		RatePct       float64     `json:"rate_pct"`
		OpenDate      string      `json:"open_date"`
		MaturityDate  string      `json:"maturity_date"`
		Payout        string      `json:"payout"`
		Capitalized   bool        `json:"capitalized,omitempty"`
		Replenishable bool        `json:"replenishable"`
		TaxPct        float64     `json:"tax_pct"`
		ClosedDate    string      `json:"closed_date,omitempty"`
		ClosedAmount  moneyJSON   `json:"closed_amount,omitempty"`
		Note          string      `json:"note,omitempty"`
		Topups        []topupJSON `json:"topups,omitempty"`
		// NetPct — ставка після податку, але ДО знецінення: номінальний
		// двійник до RealPct. Поруч уже є RatePct, але це ставка з
		// договору, до податку, і показувати її як «номінальну дохідність»
		// означало б порівнювати з реальною через дві поправки одразу.
		//
		// RealPct — та сама після знецінення, тобто в базі, спільній із
		// ОВДП і фондами. Ставка вкладу зафіксована, як і купон, тож це
		// обіцянка, а не оцінка.
		NetPct     float64 `json:"net_pct,omitempty"`
		RealPct    float64 `json:"real_pct,omitempty"`
		YieldBasis string  `json:"yield_basis,omitempty"`
	}
	deval := s.devaluation(r.Context())
	today := domain.NewDate(time.Now())
	out := make([]row, 0, len(deps))
	for _, d := range deps {
		tj := make([]topupJSON, 0, len(d.Topups))
		for _, t := range d.Topups {
			tj = append(tj, topupJSON{ID: t.ID, Date: string(t.Date),
				Amount: toMoneyJSON(money.New(t.Amount, d.Currency))})
		}
		dr := row{
			ID: d.ID, Bank: d.Bank,
			Principal: toMoneyJSON(money.New(d.Principal, d.Currency)),
			Balance:   toMoneyJSON(money.New(d.BalanceAt(today), d.Currency)),
			RatePct:   float64(d.RateBP) / 100,
			OpenDate:  string(d.OpenDate), MaturityDate: string(d.MaturityDate),
			Payout: string(d.Payout), Capitalized: d.Capitalized,
			Replenishable: d.Replenishable,
			TaxPct:        float64(d.TaxBP) / 100,
			ClosedDate:    string(d.ClosedDate),
			ClosedAmount:  toMoneyJSON(money.New(d.ClosedAmount, d.Currency)),
			Note:          d.Note, Topups: tj,
		}
		// Та сама формула, що й у реінвест-помічнику: ставка мінус податок,
		// далі знецінення. Розійтися їм не можна — інакше помічник радив би
		// поповнити вклад із однією цифрою, а портфель показував іншу.
		if d.RateBP > 0 {
			net := float64(d.RateBP) / 10000 * (1 - float64(d.TaxBP)/10000)
			dr.NetPct = round2(net * 100)
			dr.RealPct = round2(realYield(net, d.Currency, deval) * 100)
			dr.YieldBasis = "ставка вкладу"
		}
		out = append(out, dr)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAddDepositTopup — POST /api/term-deposits/{id}/topups.
// Сума в валюті вкладу; за замовчуванням у UI — тіло відкриття.
func (s *Server) handleAddDepositTopup(w http.ResponseWriter, r *http.Request) {
	depID, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Date   string `json:"date"`
		Amount string `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Валюта поповнення = валюта вкладу; тягнемо вклад, щоб її знати.
	deps, err := s.st.ListTermDeposits(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var cur string
	for _, d := range deps {
		if d.ID == depID {
			cur = d.Currency
		}
	}
	if cur == "" {
		writeErr(w, http.StatusNotFound, errors.New("вклад не знайдено"))
		return
	}
	d, err := domain.ParseDate(req.Date)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	amt, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if amt <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("сума поповнення має бути > 0"))
		return
	}
	id, err := s.st.AddDepositTopup(r.Context(), domain.DepositTopup{DepositID: depID, Date: d, Amount: amt})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// handleDeleteDepositTopup — DELETE /api/term-deposits/{id}/topups/{topupId}.
func (s *Server) handleDeleteDepositTopup(w http.ResponseWriter, r *http.Request) {
	tid, err := strconv.ParseInt(r.PathValue("topupId"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteDepositTopup(r.Context(), tid); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAddTermDeposit(w http.ResponseWriter, r *http.Request) {
	var req termDepositReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d, err := termDepositFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddTermDeposit(r.Context(), d)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdateTermDeposit(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req termDepositReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d, err := termDepositFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d.ID = id
	if err := s.st.UpdateTermDeposit(r.Context(), d); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteTermDeposit(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteTermDeposit(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// --- імпорт виписки ---
