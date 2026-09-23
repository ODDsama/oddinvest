// GET /api/plan — стрічка часу плану. Збірка документа — engine/plan_timeline.go.

package api

import (
	"net/http"
	"time"
)

// handlePlanTimeline — GET /api/plan.
func (s *Server) handlePlanTimeline(w http.ResponseWriter, r *http.Request) {
	out, err := s.PlanTimeline(r.Context(), time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.Present(r.Context(), &out); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
