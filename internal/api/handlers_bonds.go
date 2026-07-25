// Довідник ОВДП від НБУ: пошук, папір за ISIN, накопичений купон.

package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
)

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
