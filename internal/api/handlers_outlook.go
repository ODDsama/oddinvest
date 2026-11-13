// GET /api/outlook — прогноз капіталу за нинішнім темпом, помісячно на
// тридцять років, і сам темп стовпчиками. Поведінку читати в
// engine/outlook.go; там же довід, чому це окремий маршрут, а не поле
// документа стану, і чому без параметра горизонту.

package api

import (
	"net/http"
	"time"
)

func (s *Server) handleOutlook(w http.ResponseWriter, r *http.Request) {
	doc, err := s.BuildOutlook(r.Context(), time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Суми — у валюті звітності, як і в /api/summary поруч на екрані.
	if err := s.Present(r.Context(), doc); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}
