// GET /api/decisions — журнал рішень і зведення по ньому. Що саме
// зводиться і чому зведення мовчить на малих числах — у decisions.go.

package api

import (
	"net/http"
)

func (s *Server) handleDecisions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DecisionRows(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	out := struct {
		Rows    []DecisionRow     `json:"rows"`
		Summary *DecisionsSummary `json:"summary,omitempty"`
		MinRows int               `json:"min_rows"`
	}{Rows: rows, MinRows: DecisionsMinRows}
	if len(rows) >= DecisionsMinRows {
		sum := SummarizeDecisions(rows)
		out.Summary = &sum
	}
	writeJSON(w, http.StatusOK, out)
}
