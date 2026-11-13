// GET /api/outlook?scope=portfolio|all — прогноз капіталу за нинішнім
// темпом, помісячно на тридцять років, його розрізи й сам темп стовпчиками.
// Поведінку читати в engine/outlook.go; там же довід, чому це окремий
// маршрут, а не поле документа стану, і чому без параметра горизонту.
//
// scope — єдиний параметр, і він серверний навмисно: «усі гроші» додає
// резерв і цілі до КОЖНОЇ суми відповіді, а складати їх у браузері означало
// б завести там другу арифметику (CLAUDE.md §5). Чуже значення — 400, а не
// тихий спад на типове: відповісти про портфель на питання про всі гроші
// означало б збрехати під запитаною назвою.

package api

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/ODDsama/oddinvest/internal/engine"
)

func (s *Server) handleOutlook(w http.ResponseWriter, r *http.Request) {
	scope := engine.OutlookPortfolio
	if q := r.URL.Query().Get("scope"); q != "" {
		if !slices.Contains(engine.OutlookScopes, q) {
			writeErr(w, http.StatusBadRequest, errors.New("scope буває portfolio або all"))
			return
		}
		scope = q
	}
	doc, err := s.BuildOutlook(r.Context(), time.Now(), scope)
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
