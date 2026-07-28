// Довідники брокерів і фондів: перейменування підхоплюють усі записи
// разом, бо записи тримаються за id.

package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ODDsama/oddinvest/internal/store"
)

func (s *Server) handleListBrokers(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListBrokers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleAddBroker(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddBroker(r.Context(), req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleRenameBroker(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.RenameBroker(r.Context(), id, req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteBroker(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteBroker(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListFundCatalog(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListFunds(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// Фонд заводиться сам при першій операції, тож окремого POST немає —
// лишається виправити назву, валюту й те, чого з операцій не вивести:
// обіцяну дохідність і день виплати.
func (s *Server) handleUpdateFundCatalog(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Name string `json:"name"`
		// Валюта самого сертифіката (ціна, купівлі, дивіденди).
		Currency string `json:"currency"`
		// Обіцяна дохідність у відсотках рядком — як усі відсотки в API,
		// щоб порожнє поле означало «не задано», а не нуль.
		ExpectedYieldPct string `json:"expected_yield_pct"`
		// Валюта, В ЯКІЙ обіцяна дохідність. Може відрізнятись від валюти
		// фонду: гривневий сертифікат, чия ціна йде за курсом НБУ, обіцяє
		// у доларах, і гривневий штраф за знецінення до такої обіцянки
		// застосовувати не можна.
		ExpectedYieldCurrency string `json:"expected_yield_currency"`
		PayoutDay             int64  `json:"payout_day"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	bp, err := parsePercentBP(req.ExpectedYieldPct)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("обіцяна дохідність: %w", err))
		return
	}
	if err := s.st.RenameFund(r.Context(), id, store.Fund{
		Name: req.Name, Currency: req.Currency,
		ExpectedYieldBP: bp, ExpectedYieldCur: req.ExpectedYieldCurrency,
		PayoutDay: req.PayoutDay,
	}); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteFundCatalog(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteFund(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// --- сертифікати фондів ---
