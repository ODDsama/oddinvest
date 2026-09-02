// GET /api/progress — віхи, серія й поле колекції.
//
// Обробник тільки СКЛАДАЄ дані й кличе чисту функцію (state_progress.go):
// той самий поділ, що між handleSummary і buildState. Читати поведінку
// прогресу треба там, а тут — лише звідки що береться.

package api

import (
	"net/http"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
)

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	today := domain.NewDate(now)

	// Документ — обов'язковий: із нього беруться драбина, резерв,
	// ребаланс і концентрація, тобто дев'ять віх із чотирнадцяти. Без
	// нього відповідати нема чим.
	doc, err := s.buildState(ctx, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	src, err := s.loadSources(ctx, today)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// А далі — три м'які джерела. Кожне з них живить СВОЮ частину
	// відповіді, і падіння будь-якого не робить решту неправдою: краще
	// віддати дванадцять віх із прочерком у двох, ніж 500 на весь блок.
	// Той самий прийом, що в оболонки з soft(): маршрут може бути
	// новішим за дані.
	snaps, serr := s.st.ListSnapshots(ctx, "", "")
	if serr != nil {
		s.log.Warn("знімки для прогресу не зібрались", "err", serr)
		snaps = nil
	}
	ev, eerr := s.cashEvents(ctx)
	if eerr != nil {
		s.log.Warn("рух грошей для прогресу не зібрався", "err", eerr)
		ev = nil
	}

	// Дисципліна — з ТІЄЇ САМОЇ функції, що й /api/decisions, і з тим
	// самим порогом: доки журнал закороткий, зведення не показує його й
	// там. Один вдалий вибір із одного — це 100%, і доріжка, яка це
	// малює, обіцяє точність, якої немає.
	var dec *decisionsSummary
	if rows, derr := s.decisionRows(ctx); derr != nil {
		s.log.Warn("журнал рішень для прогресу не зібрався", "err", derr)
	} else if len(rows) >= decisionsMinRows {
		sum := summarizeDecisions(rows)
		dec = &sum
	}

	// Суперники — над УЖЕ зібраним документом, а не власним buildState, і
	// ОДНИМ прогоном на два читачі: бенчмарк («обіграв долари») і серію
	// «попереду долара» по місяцях — обидва з того самого добового ряду.
	var bench *benchResult
	var vs *vsDoc
	if rv, rerr := s.rivals(ctx, doc, levelPortfolio); rerr != nil {
		s.log.Warn("суперники для прогресу не зібрались", "err", rerr)
	} else if rates, ferr := s.rates(ctx); ferr != nil {
		s.log.Warn("курси для прогресу не зібрались", "err", ferr)
	} else {
		b := benchFromRivals(rv, rates)
		bench = &b
		vs = buildVsUSD(rv.Days, rv.row(domain.RivalUSDCash).PointsDiff, today)
	}

	writeJSON(w, http.StatusOK, buildProgress(doc, src, snaps, ev, dec, bench, vs, today))
}
