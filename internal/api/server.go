// Package api — HTTP-сервер: REST + вбудований веб-UI.
package api

import (
	"context"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

//go:embed web
var webFS embed.FS

// Refresher — те, що вміє фонова частина (оновити довідник/курси і
// републікувати стан). Реалізується в jobs, сюди приходить інтерфейсом.
type Refresher interface {
	RefreshAll(ctx context.Context) error
	PublishState(ctx context.Context) error
}

type Server struct {
	st  *store.Store
	ref Refresher
	log *slog.Logger
}

func New(st *store.Store, ref Refresher, log *slog.Logger) *Server {
	return &Server{st: st, ref: ref, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/summary", s.handleSummary)
	mux.HandleFunc("GET /api/positions", s.handlePositions)
	mux.HandleFunc("GET /api/calendar", s.handleCalendar)
	mux.HandleFunc("GET /api/ladder", s.handleLadder)
	mux.HandleFunc("GET /api/lots", s.handleListLots)
	mux.HandleFunc("POST /api/lots", s.handleAddLot)
	mux.HandleFunc("DELETE /api/lots/{id}", s.handleDeleteLot)
	mux.HandleFunc("POST /api/sales", s.handleAddSale)
	mux.HandleFunc("GET /api/sales", s.handleListSales)
	mux.HandleFunc("GET /api/bonds/search", s.handleSearchBonds)
	mux.HandleFunc("GET /api/bonds/{isin}", s.handleGetBond)
	mux.HandleFunc("GET /api/accrued/{isin}", s.handleAccrued)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handlePutSettings)
	mux.HandleFunc("POST /api/payments/status", s.handlePaymentStatus)
	mux.HandleFunc("POST /api/refresh", s.handleRefresh)
	mux.HandleFunc("GET /api/xirr", s.handleXIRR)
	mux.HandleFunc("GET /api/snapshots", s.handleSnapshots)
	mux.HandleFunc("GET /api/export/csv", s.handleExportCSV)

	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", http.FileServerFS(sub))
	return logMiddleware(s.log, mux)
}

func logMiddleware(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Info("http", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start))
	})
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

type moneyJSON struct {
	Amount   string `json:"amount"` // десятковий рядок "995.00"
	Currency string `json:"currency"`
}

func toMoneyJSON(m *money.Money) moneyJSON {
	if m == nil {
		return moneyJSON{Amount: "0", Currency: money.UAH}
	}
	minor := m.Amount()
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	return moneyJSON{
		Amount:   fmt.Sprintf("%s%d.%02d", sign, minor/100, minor%100),
		Currency: m.Currency().Code,
	}
}

func parseMoney(amount, currency string) (*money.Money, error) {
	minor, err := domain.ParseDecimalToMinor(amount, currency)
	if err != nil {
		return nil, err
	}
	return money.New(minor, currency), nil
}

// portfolio — все, що треба домену, одним заходом.
func (s *Server) portfolio(ctx context.Context) (lots []domain.Lot, sales []domain.Sale,
	bonds map[string]domain.Bond, pays []domain.Payment, err error) {
	lots, err = s.st.ListLots(ctx)
	if err != nil {
		return
	}
	sales, err = s.st.ListSales(ctx)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	var isins []string
	for _, l := range lots {
		if !seen[l.ISIN] {
			seen[l.ISIN] = true
			isins = append(isins, l.ISIN)
		}
	}
	bonds, err = s.st.BondsFor(ctx, isins)
	if err != nil {
		return
	}
	pays, err = s.st.PaymentsFor(ctx, isins)
	return
}

func (s *Server) rates(ctx context.Context) (fx.Rates, error) {
	usd, err := s.st.LatestRate(ctx, "USD")
	if err != nil {
		return nil, err
	}
	r := fx.Rates{}
	if usd > 0 {
		r["USD"] = usd
	}
	return r, nil
}

// --- handlers ---

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	doc, err := s.buildState(r.Context(), time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// buildState — спільна збірка документа стану для API і MQTT.
func (s *Server) BuildStateDoc(ctx context.Context, now time.Time) (*state.Doc, error) {
	return s.buildState(ctx, now)
}

func (s *Server) buildState(ctx context.Context, now time.Time) (*state.Doc, error) {
	lots, sales, bonds, pays, err := s.portfolio(ctx)
	if err != nil {
		return nil, err
	}
	rates, err := s.rates(ctx)
	if err != nil {
		return nil, err
	}
	today := domain.NewDate(now)
	positions, err := domain.Positions(bonds, pays, lots, sales, today)
	if err != nil {
		return nil, err
	}
	cashflow, err := domain.FuturePayments(pays, lots, sales, today)
	if err != nil {
		return nil, err
	}
	ladder := domain.Ladder(bonds, lots, sales, today)

	// внески місяця: покупки поточного місяця в грн-еквіваленті
	monthInv := money.New(0, money.UAH)
	for _, l := range lots {
		if l.BuyDate.Year() == now.Year() && l.BuyDate.Month() == now.Month() {
			uahAmt, err := fx.ToUAH(domain.MulQty(l.PricePerBond, l.Qty), rates)
			if err != nil {
				return nil, err
			}
			monthInv, err = monthInv.Add(uahAmt)
			if err != nil {
				return nil, err
			}
		}
	}

	targetStr, _ := s.st.GetSetting(ctx, "monthly_target_uah")
	target := money.New(0, money.UAH)
	if targetStr != "" {
		if t, err := parseMoney(targetStr, money.UAH); err == nil {
			target = t
		}
	}

	// неперевкладені: минулі виплати без статусу reinvested
	statuses, err := s.st.PaymentStatuses(ctx)
	if err != nil {
		return nil, err
	}
	pastCF, err := domain.FuturePayments(pays, lots, sales, "1970-01-01")
	if err != nil {
		return nil, err
	}
	unin := money.New(0, money.UAH)
	for _, cf := range pastCF {
		if cf.Date.After(today) || cf.Date == today {
			continue
		}
		if statuses[cf.ISIN+"|"+string(cf.Date)] == "reinvested" {
			continue
		}
		uahAmt, err := fx.ToUAH(cf.Amount, rates)
		if err != nil {
			return nil, err
		}
		unin, err = unin.Add(uahAmt)
		if err != nil {
			return nil, err
		}
	}

	settings := &state.SettingsDoc{}
	if !target.IsZero() {
		v := float64(target.Amount()) / 100
		settings.MonthlyTargetUAH = &v
	}
	if raw, _ := s.st.GetSetting(ctx, "usd_target_share_pct"); raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			settings.USDTargetSharePct = &f
		}
	}
	if raw, _ := s.st.GetSetting(ctx, "insurance_premium_uah"); raw != "" {
		if m, err := parseMoney(raw, money.UAH); err == nil {
			v := float64(m.Amount()) / 100
			settings.InsurancePremiumUAH = &v
		}
	}

	var insDays *int
	if insDate, _ := s.st.GetSetting(ctx, "insurance_renewal"); insDate != "" {
		if d, err := domain.ParseDate(insDate); err == nil {
			n := domain.DaysBetween(today, d)
			insDays = &n
			settings.InsuranceRenewal = string(d)
		}
	}

	xirr := map[string]float64{}
	for _, cur := range []string{money.UAH, money.USD} {
		flows, err := domain.PortfolioFlows(bonds, pays, lots, sales, cur, today)
		if err != nil || len(flows) < 2 {
			continue
		}
		// ануалізація на коротких горизонтах дає сміттєві сотні відсотків;
		// не показуємо XIRR, поки історії менше 30 днів
		if domain.DaysBetween(flows[0].Date, today) < 30 {
			continue
		}
		if r, err := domain.XIRR(flows); err == nil {
			xirr[cur] = math.Round(r*10000) / 100 // частка -> %, 2 знаки
		}
	}

	return state.Build(state.Input{
		Now: now, Positions: positions, Cashflow: cashflow, Ladder: ladder,
		Rates: rates, MonthInvestedUAH: monthInv, MonthTargetUAH: target,
		UninvestedUAH: unin, InsuranceDaysLeft: insDays, TopN: 5,
		Settings: settings, XIRRPct: xirr,
	})
}

func (s *Server) handlePositions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lots, sales, bonds, pays, err := s.portfolio(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	pos, err := domain.Positions(bonds, pays, lots, sales, domain.NewDate(time.Now()))
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
	}
	out := make([]posJSON, 0, len(pos))
	for _, p := range pos {
		out = append(out, posJSON{p.ISIN, p.Currency, p.Qty, toMoneyJSON(p.Invested),
			toMoneyJSON(p.Nominal), string(p.Maturity), p.DaysToMat,
			string(p.NextPayDate), toMoneyJSON(p.NextPayAmt)})
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
	statuses, _ := s.st.PaymentStatuses(ctx)
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
	writeJSON(w, http.StatusOK, domain.Ladder(bonds, lots, sales, domain.NewDate(time.Now())))
}

type lotReq struct {
	ISIN     string `json:"isin"`
	Qty      int64  `json:"qty"`
	Price    string `json:"price_per_bond"` // десятковий рядок
	Currency string `json:"currency"`
	BuyDate  string `json:"buy_date"`
	Channel  string `json:"channel"`
	Note     string `json:"note"`
}

func (s *Server) handleAddLot(w http.ResponseWriter, r *http.Request) {
	var req lotReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Qty <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("qty має бути > 0"))
		return
	}
	bd, err := domain.ParseDate(req.BuyDate)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cur := req.Currency
	if cur == "" { // валюту беремо з довідника, якщо папір відомий
		if b, _ := s.st.GetBond(r.Context(), req.ISIN); b != nil {
			cur = b.Nominal.Currency().Code
		} else {
			writeErr(w, http.StatusBadRequest, errors.New("папір не в довіднику — вкажіть currency явно"))
			return
		}
	}
	price, err := parseMoney(req.Price, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddLot(r.Context(), domain.Lot{
		ISIN: req.ISIN, Qty: req.Qty, PricePerBond: price,
		BuyDate: bd, Channel: req.Channel, Note: req.Note,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleDeleteLot(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteLot(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListLots(w http.ResponseWriter, r *http.Request) {
	lots, err := s.st.ListLots(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	sales, err := s.st.ListSales(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type lotJSON struct {
		ID        int64     `json:"id"`
		ISIN      string    `json:"isin"`
		Qty       int64     `json:"qty"`
		Remaining int64     `json:"remaining"`
		Price     moneyJSON `json:"price_per_bond"`
		BuyDate   string    `json:"buy_date"`
		Channel   string    `json:"channel"`
		Note      string    `json:"note"`
	}
	out := make([]lotJSON, 0, len(lots))
	for _, l := range lots {
		out = append(out, lotJSON{l.ID, l.ISIN, l.Qty, domain.RemainingQtyNow(l, sales),
			toMoneyJSON(l.PricePerBond), string(l.BuyDate), l.Channel, l.Note})
	}
	writeJSON(w, http.StatusOK, out)
}

type saleReq struct {
	LotID    int64  `json:"lot_id"`
	SaleDate string `json:"sale_date"`
	Qty      int64  `json:"qty"`
	Clean    string `json:"clean_per_bond"`
	Accrued  string `json:"accrued"` // сумарний НКД, опційно
	Currency string `json:"currency"`
	Note     string `json:"note"`
}

func (s *Server) handleAddSale(w http.ResponseWriter, r *http.Request) {
	var req saleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sd, err := domain.ParseDate(req.SaleDate)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	clean, err := parseMoney(req.Clean, req.Currency)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	accrued := money.New(0, req.Currency)
	if req.Accrued != "" {
		if accrued, err = parseMoney(req.Accrued, req.Currency); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	id, err := s.st.AddSale(r.Context(), domain.Sale{
		LotID: req.LotID, SaleDate: sd, Qty: req.Qty,
		CleanPerBond: clean, Accrued: accrued, Note: req.Note,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleListSales(w http.ResponseWriter, r *http.Request) {
	sales, err := s.st.ListSales(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	lots, err := s.st.ListLots(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	lotByID := map[int64]domain.Lot{}
	isins := []string{}
	for _, l := range lots {
		lotByID[l.ID] = l
		isins = append(isins, l.ISIN)
	}
	pays, err := s.st.PaymentsFor(r.Context(), isins)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type saleJSON struct {
		ID       int64     `json:"id"`
		LotID    int64     `json:"lot_id"`
		ISIN     string    `json:"isin"`
		SaleDate string    `json:"sale_date"`
		Qty      int64     `json:"qty"`
		Clean    moneyJSON `json:"clean_per_bond"`
		Accrued  moneyJSON `json:"accrued"`
		Result   moneyJSON `json:"realized_result"`
	}
	out := make([]saleJSON, 0, len(sales))
	for _, sl := range sales {
		lot := lotByID[sl.LotID]
		res, err := domain.RealizedResult(lot, sl, pays)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, saleJSON{sl.ID, sl.LotID, lot.ISIN, string(sl.SaleDate),
			sl.Qty, toMoneyJSON(sl.CleanPerBond), toMoneyJSON(sl.Accrued), toMoneyJSON(res)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSearchBonds(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bonds, err := s.st.SearchBonds(r.Context(), q.Get("q"), q.Get("currency"),
		domain.Date(q.Get("maturity_from")), domain.Date(q.Get("maturity_to")), 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, bondsJSON(bonds))
}

func (s *Server) handleGetBond(w http.ResponseWriter, r *http.Request) {
	b, err := s.st.GetBond(r.Context(), r.PathValue("isin"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if b == nil {
		writeErr(w, http.StatusNotFound, errors.New("папір не знайдено в довіднику"))
		return
	}
	writeJSON(w, http.StatusOK, bondsJSON([]domain.Bond{*b})[0])
}

func (s *Server) handleAccrued(w http.ResponseWriter, r *http.Request) {
	isin := r.PathValue("isin")
	on := domain.NewDate(time.Now())
	if q := r.URL.Query().Get("on"); q != "" {
		var err error
		if on, err = domain.ParseDate(q); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	pays, err := s.st.PaymentsFor(r.Context(), []string{isin})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	acc, err := domain.EstimateAccrued(pays, isin, on)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"isin": isin, "on": string(on), "per_bond": toMoneyJSON(acc),
		"note": "оцінка ACT/ACT; фактичний НКД може відрізнятись",
	})
}

func bondsJSON(bonds []domain.Bond) []map[string]any {
	out := make([]map[string]any, 0, len(bonds))
	for _, b := range bonds {
		out = append(out, map[string]any{
			"isin": b.ISIN, "nominal": toMoneyJSON(b.Nominal),
			"rate_pct": fmt.Sprintf("%d.%02d", b.RateBP/100, b.RateBP%100),
			"maturity": string(b.Maturity), "descr": b.Descr,
		})
	}
	return out
}

var settingsKeys = []string{"monthly_target_uah", "usd_target_share_pct", "insurance_renewal", "insurance_premium_uah"}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{}
	for _, k := range settingsKeys {
		v, err := s.st.GetSetting(r.Context(), k)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	allowed := map[string]bool{}
	for _, k := range settingsKeys {
		allowed[k] = true
	}
	for k, v := range req {
		if !allowed[k] {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("невідомий ключ %q", k))
			return
		}
		if err := s.st.SetSetting(r.Context(), k, v); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePaymentStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ISIN    string `json:"isin"`
		PayDate string `json:"pay_date"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d, err := domain.ParseDate(req.PayDate)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.SetPaymentStatus(r.Context(), req.ISIN, d, req.Status); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if s.ref == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("refresher недоступний"))
		return
	}
	if err := s.ref.RefreshAll(r.Context()); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) publishAsync() {
	if s.ref == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.ref.PublishState(ctx); err != nil {
			s.log.Error("публікація стану", "err", err)
		}
	}()
}

func (s *Server) handleXIRR(w http.ResponseWriter, r *http.Request) {
	doc, err := s.buildState(r.Context(), time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"xirr_pct": doc.XIRRPct,
		"note":     "залишок оцінено за номіналом; по валютах окремо, без конвертації",
	})
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	snaps, err := s.st.ListSnapshots(r.Context(),
		domain.Date(q.Get("from")), domain.Date(q.Get("to")))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type snapJSON struct {
		Date          string  `json:"date"`
		InvestedUAH   float64 `json:"invested_uah"`
		NominalUAHEq  float64 `json:"nominal_uah_eq"`
		USDSharePct   float64 `json:"usd_share_pct"`
		UninvestedUAH float64 `json:"uninvested_uah"`
	}
	out := make([]snapJSON, 0, len(snaps))
	for _, sn := range snaps {
		out = append(out, snapJSON{string(sn.Date), float64(sn.InvestedUAH) / 100,
			float64(sn.NominalUAHEq) / 100, float64(sn.USDShareBP) / 100,
			float64(sn.UninvestedUAH) / 100})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleExportCSV — рухи за рік для декларації: отримані виплати і
// продажі з реалізованим результатом. Роздільник ';' і UTF-8 BOM —
// щоб україномовний Excel відкривав без танців.
func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	year := r.URL.Query().Get("year")
	if year == "" {
		year = fmt.Sprintf("%d", time.Now().Year())
	}
	ctx := r.Context()
	lots, sales, _, pays, err := s.portfolio(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	past, err := domain.FuturePayments(pays, lots, sales, domain.Date(year+"-01-01"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	today := domain.NewDate(time.Now())

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=oddinvest-%s.csv", year))
	w.Write([]byte{0xEF, 0xBB, 0xBF}) // BOM
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	cw.Write([]string{"тип", "дата", "ISIN", "кількість", "сума", "валюта", "коментар"})

	typeNames := map[domain.PayType]string{
		domain.PayCoupon: "купон", domain.PayRedemption: "погашення",
		domain.PayEarly: "дострокове погашення",
	}
	for _, cf := range past {
		if !strings.HasPrefix(string(cf.Date), year) || cf.Date.After(today) {
			continue
		}
		cw.Write([]string{typeNames[cf.Type], string(cf.Date), cf.ISIN, "",
			toMoneyJSON(cf.Amount).Amount, cf.Amount.Currency().Code, ""})
	}
	lotByID := map[int64]domain.Lot{}
	for _, l := range lots {
		lotByID[l.ID] = l
	}
	for _, sl := range sales {
		if !strings.HasPrefix(string(sl.SaleDate), year) {
			continue
		}
		lot := lotByID[sl.LotID]
		res, err := domain.RealizedResult(lot, sl, pays)
		if err != nil {
			continue
		}
		proceeds, _ := domain.SaleProceeds(sl)
		cw.Write([]string{"продаж", string(sl.SaleDate), lot.ISIN,
			fmt.Sprintf("%d", sl.Qty), toMoneyJSON(proceeds).Amount,
			proceeds.Currency().Code,
			"результат " + toMoneyJSON(res).Amount + " " + res.Currency().Code})
	}
	cw.Flush()
}
