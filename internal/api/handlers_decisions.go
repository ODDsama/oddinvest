// GET /api/decisions — журнал рішень і зведення по ньому. Що саме
// зводиться і чому зведення мовчить на малих числах — у engine/decisions.go.

package api

import (
	"github.com/ODDsama/oddinvest/internal/engine"
	"net/http"
)

func (s *Server) handleDecisions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DecisionRows(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	out := struct {
		Rows    []engine.DecisionRow     `json:"rows"`
		Summary *engine.DecisionsSummary `json:"summary,omitempty"`
		MinRows int                      `json:"min_rows"`
	}{Rows: rows, MinRows: engine.DecisionsMinRows}
	if len(rows) >= engine.DecisionsMinRows {
		sum := engine.SummarizeDecisions(rows)
		out.Summary = &sum
	}
	writeJSON(w, http.StatusOK, out)
}
