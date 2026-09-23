// GET /api/summary — документ стану, той самий, що їде в MQTT, але вже
// перекладений у валюту звітності. Збирання — state_builder.go.

package api

import (
	"net/http"
	"time"
)

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	doc, err := s.buildStateTasked(r.Context(), time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Валюта звітності — на виході, на готовому документі (presenter.go).
	if err := s.present(r.Context(), doc); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}
