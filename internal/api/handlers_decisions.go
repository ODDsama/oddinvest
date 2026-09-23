// GET /api/decisions — журнал рішень і зведення по ньому. Що саме
// зводиться і чому зведення мовчить на малих числах — у decisions.go.

package api

import (
	"net/http"
)

func (s *Server) handleDecisions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.decisionRows(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	out := struct {
		Rows    []decisionRow     `json:"rows"`
		Summary *decisionsSummary `json:"summary,omitempty"`
		MinRows int               `json:"min_rows"`
	}{Rows: rows, MinRows: decisionsMinRows}
	if len(rows) >= decisionsMinRows {
		sum := summarizeDecisions(rows)
		out.Summary = &sum
	}
	writeJSON(w, http.StatusOK, out)
}
