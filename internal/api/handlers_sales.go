// Продажі паперів до погашення.

package api

import (
	"encoding/json"
	"net/http"

	"github.com/ODDsama/oddinvest/internal/domain"
	money "github.com/Rhymond/go-money"
)

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
