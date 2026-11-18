// GET /api/reinvest — поради «що купити». Збірка й порядок — у
// engine/reinvest.go; тут лише подача.

package api

import (
	"net/http"
	"time"
)

// handleReinvest — помічник реінвестиції: доступні папери з довідника,
// відранжовані під план (розрив до цільової частки — валютної й за видом
// інструмента разом → реальна дохідність → рік з «діркою» в драбині).
// Це інструмент, не порада.
//
// «Купонної ставки» в цьому переліку немає вже давно: сира ставка
// непорівнянна між валютами, і порядок задає реальна дохідність.
// Уся робота в ReinvestSuggestions: те саме число потрібне ще й черзі
// задач, розкладці й маршруту, а дві збірки порад радили б різне.
//
// Екранного читача в переліку лишився один — форма «Купив» задачі в черзі
// (web/js/views/now-view.js, openBought): вона бере звідси ціну кроку й
// брокера. Картку «Що взяти» прибрано 2026-11-18, а з нею й лінійку
// reinvest_order, якою цей обробник переупорядковував відповідь за
// номінальною дохідністю: порядок тепер рівно той, що віддав рушій.
func (s *Server) handleReinvest(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	doc, err := s.BuildState(r.Context(), now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out, err := s.ReinvestSuggestions(r.Context(), now, doc)
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
