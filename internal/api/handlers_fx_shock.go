// GET /api/fx-shock?window=1|3|12 — валютний шок на сьогоднішньому
// портфелі. Поведінку читати в state_fx_shock.go; тут — лише звідки що
// береться (той самий поділ, що між handleProgress і state_progress.go).
//
// ЧОМУ ОКРЕМИЙ МАРШРУТ, А НЕ ПОЛЕ ДОКУМЕНТА СТАНУ. Довід той самий, що
// при /api/progress: документ публікується в MQTT і щодня лягає в
// добовий знімок, а шок коштує ДРУГОЇ повної збірки стану й відповідає
// на питання з параметром. Наслідок приємний: контракт, фікстури й
// інтеграція HA не чіпаються зовсім.
//
// ЧОМУ GET, А НЕ ПОЛЕ В /api/whatif. Три причини, і всі вже записані при
// handlers_policy_preview.go: whatif типово тягне ЗБЕРЕЖЕНИЙ план
// купівель (saved=true), а питання тут — про портфель, як він є
// сьогодні; whatif збирає стан двічі, бо йому потрібен винуватець
// нестачі, а тут потрібне лише «стане», бо «зараз» уже лежить на
// фронтенді відповіддю /api/summary; і тіла від людини тут немає взагалі
// — вхід виводиться з власної історії курсів, а параметр один.
//
// ВІКНО, ЯКОГО НЕ ВИМІРЯТИ, НЕ ПІДМІНЯЄТЬСЯ ІНШИМ. Відповідь лишається
// 200, Episode порожній, причина названа. Тихо віддати замість річного
// вікна квартальне означало б відповісти на інше питання під запитаною
// назвою — і читач цього не побачив би ніяк.

package api

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// fxPointsOf — переклад історії зі сховища в терміни domain.
//
// Потрібен рівно тому, що domain про сховище не знає: той самий поділ,
// через який FXPlace бере голий зріз int64, а відбір за датою лишає
// викликачеві.
func fxPointsOf(hist map[string][]store.RatePoint) map[string][]domain.FXPoint {
	out := make(map[string][]domain.FXPoint, len(hist))
	for cur, pts := range hist {
		conv := make([]domain.FXPoint, 0, len(pts))
		for _, p := range pts {
			conv = append(conv, domain.FXPoint{Date: p.Date, RateE4: p.RateE4})
		}
		out[cur] = conv
	}
	return out
}

func (s *Server) handleFXShock(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	today := domain.NewDate(now)

	window := fxShockWindows[len(fxShockWindows)-1]
	if q := r.URL.Query().Get("window"); q != "" {
		v, err := strconv.Atoi(q)
		if err != nil || !slices.Contains(fxShockWindows, v) {
			writeErr(w, http.StatusBadRequest,
				errors.New("вікно буває 1, 3 або 12 місяців"))
			return
		}
		window = v
	}

	hist, err := s.fxHistorySince(ctx, today)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rates, err := s.rates(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	doc, shocked := buildFXShock(fxPointsOf(hist), rates, window)
	if len(shocked) > 0 {
		after, err := s.buildStateWith(ctx, now, hypothetical{rates: shocked})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		doc.After = after
	}
	writeJSON(w, http.StatusOK, doc)
}
