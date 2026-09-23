// GET /api/reinvest — поради «що купити». Збірка й порядок — у
// reinvest.go; тут лише те, що належить екрану: перемикач лінійки порядку
// й дата доступності.

package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	money "github.com/Rhymond/go-money"
)

// handleReinvest — помічник реінвестиції: доступні папери з довідника,
// відранжовані під план (розрив до цільової частки — валютної й за видом
// інструмента разом → реальна дохідність → рік з «діркою» в драбині).
// Це інструмент, не порада.
//
// «Купонної ставки» в цьому переліку немає вже давно: сира ставка
// непорівнянна між валютами, і порядок задає реальна дохідність.
// handleReinvest — GET /api/reinvest. Уся робота в ReinvestSuggestions:
// те саме число потрібне ще й черзі задач, а дві збірки порад означали б,
// що «Що робити» і «Що купити» радять різне — рівно та розбіжність, проти
// якої в now-view.js уже стоїть окреме попередження.
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
	// ЛІНІЙКА ПОРЯДКУ — ЛИШЕ ТУТ, і це межа, а не зручність. Черга задач,
	// журнал рішень, прогноз і гейт боргу кличуть ту саму збірку порад і
	// дістають її впорядкованою за РЕАЛЬНОЮ: їм потрібне одне число, і
	// перемикач на екрані не має права переписувати те, чим застосунок
	// міряє власні рішення. Тому переупорядкування живе в обробнику, а не
	// в ReinvestSuggestions (межу тримає make order-boundary).
	//
	// У валюті звітності ≠ гривні номінальної лінійки немає — 15% ОВДП це
	// не 15% у доларах, — тож і перемикача немає: поради йдуть за реальною.
	if report, err := s.ReportCurrency(r.Context()); err == nil && report == money.UAH &&
		r.URL.Query().Get("order") == OrderNominal {
		rank := RankOf(doc)
		sort.SliceStable(out, func(i, j int) bool {
			return LessSuggestion(out[i], out[j], rank, OrderNominal)
		})
	}
	// Дата доступності — лише тут, і лише для екрана: чому не всередині
	// ReinvestSuggestions, сказано в шапці ready_on.go.
	if err := s.AnnotateReady(r.Context(), domain.NewDate(now), doc, out); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Ліміту немає свідомо: у таблиці є фільтри, сортування й пагінація,
	// тож звужує користувач, а не бекенд мовчки.
	if err := s.Present(r.Context(), &out); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
