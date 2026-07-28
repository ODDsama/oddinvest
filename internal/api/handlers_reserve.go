// Резерв («матрац») — гроші на чорний день.
//
// Окремо від handlers_cash.go навмисно, хоч форма запиту майже та сама:
// там рух коштів НА РАХУНКАХ БРОКЕРІВ, тобто грошей, які чекають на
// вкладення, а тут — грошей, які вкладати не збираються. Злиття цих двох
// журналів зробило б резерв купівельною спроможністю, і помічник
// реінвесту запропонував би купити папір за аварійні гроші.

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

type reserveReq struct {
	Date     string `json:"date"`
	Amount   string `json:"amount"` // десятковий; + відклав, − узяв
	Currency string `json:"currency"`
	Place    string `json:"place"`
	Note     string `json:"note"`
}

func reserveFromReq(req reserveReq) (store.ReserveOp, error) {
	d := domain.NewDate(time.Now())
	if req.Date != "" {
		var err error
		if d, err = domain.ParseDate(req.Date); err != nil {
			return store.ReserveOp{}, err
		}
	}
	cur := req.Currency
	if cur == "" {
		cur = money.UAH
	}
	minor, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		return store.ReserveOp{}, err
	}
	// Нуль відхиляємо: рух на нуль нічого не змінює, але засмічує журнал і
	// зсуває «останній рух» на дату, коли насправді нічого не сталось.
	if minor == 0 {
		return store.ReserveOp{}, errors.New("сума руху не може бути нульовою")
	}
	return store.ReserveOp{Date: d, Amount: minor, Currency: cur,
		Place: strings.TrimSpace(req.Place), Note: req.Note}, nil
}

func (s *Server) handleAddReserveOp(w http.ResponseWriter, r *http.Request) {
	var req reserveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op, err := reserveFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddReserveOp(r.Context(), op)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdateReserveOp(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req reserveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op, err := reserveFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op.ID = id
	if err := s.st.UpdateReserveOp(r.Context(), op); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteReserveOp(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteReserveOp(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListReserveOps(w http.ResponseWriter, r *http.Request) {
	ops, err := s.st.ListReserveOps(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type opJSON struct {
		ID     int64     `json:"id"`
		Date   string    `json:"date"`
		Amount moneyJSON `json:"amount"`
		Place  string    `json:"place"`
		Note   string    `json:"note"`
	}
	out := make([]opJSON, 0, len(ops))
	for _, op := range ops {
		out = append(out, opJSON{op.ID, string(op.Date),
			toMoneyJSON(money.New(op.Amount, op.Currency)), op.Place, op.Note})
	}
	writeJSON(w, http.StatusOK, out)
}
