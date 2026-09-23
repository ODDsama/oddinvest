// GET /api/progress — віхи, серія й поле колекції.
//
// Обробник лише віддає відповідь. Звідки що береться — e.progress, як
// воно зводиться у віхи — чиста buildProgress (обидва в state_progress.go):
// той самий поділ, що між handleSummary і buildState.

package api

import (
	"net/http"
	"time"
)

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out, err := s.progress(ctx, time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.present(ctx, &out); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
