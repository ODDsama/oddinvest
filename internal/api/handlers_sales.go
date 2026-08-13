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

// saleFromReq — розбір тіла запиту в продаж. Спільний для POST і PUT,
// за тією ж причиною, що й lotFromReq: дві копії правил (дата, валюта,
// НКД) розійшлись би, і редагування почало б приймати не те, що
// створення.
func saleFromReq(req saleReq) (domain.Sale, error) {
	var out domain.Sale
	sd, err := domain.ParseDate(req.SaleDate)
	if err != nil {
		return out, err
	}
	clean, err := parseMoney(req.Clean, req.Currency)
	if err != nil {
		return out, err
	}
	accrued := money.New(0, req.Currency)
	if req.Accrued != "" {
		if accrued, err = parseMoney(req.Accrued, req.Currency); err != nil {
			return out, err
		}
	}
	return domain.Sale{LotID: req.LotID, SaleDate: sd, Qty: req.Qty,
		CleanPerBond: clean, Accrued: accrued, Note: req.Note}, nil
}

func (s *Server) handleAddSale(w http.ResponseWriter, r *http.Request) {
	var req saleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sale, err := saleFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddSale(r.Context(), sale)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// handleUpdateSale — виправлення вже записаного продажу.
//
// Доти продаж був єдиною операцією, яку не можна ні виправити, ні
// скасувати: у роутері стояли самі POST і GET. А помилка в ньому не
// косметична — від кількості й ціни залежать і реалізований результат,
// і залишок лота, і XIRR, тобто рівно ті числа, заради яких застосунок
// існує. Перерахувати їх «в голові» не можна: вони зводяться з усієї
// історії.
func (s *Server) handleUpdateSale(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req saleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sale, err := saleFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sale.ID = id
	if err := s.st.UpdateSale(r.Context(), sale); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteSale(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteSale(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
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
