package api

import (
	"fmt"
	"net/http"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Звідки взялася інфляція і де вона стоїть серед власної історії.
//
// REST-only, поза MQTT — дослівно з тих самих міркувань, що й
// /api/devaluation поруч: це екран Політики, а не стан портфеля, і
// роздувати retained-повідомлення довідковою таблицею немає сенсу. Тому
// в контракті ані ряду, ані вікон немає, і state.Doc не змінювався.
//
// Вікна показуємо всі, а не лише чинне десятирічне, з тієї самої
// причини: побачивши поруч «за рік 14.1%» і «за десять 10.6%», людина
// розуміє, чому число не можна брати з короткого вікна.
//
// ПЕРЦЕНТИЛЬ — ФАКТ, А НЕ СИГНАЛ. Межа описана в шапці domain/fxwindow.go
// і діє тут дослівно: ні порога, ні підсвітки, ні поради «зачекай».
func (s *Server) handleInflation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	type window struct {
		Label string  `json:"label"`
		Years int     `json:"years"`
		Pct   float64 `json:"pct"`
		From  string  `json:"from"`
		To    string  `json:"to"`
	}
	type place struct {
		Years      int     `json:"years"`
		Points     int     `json:"points"`
		Percentile float64 `json:"percentile"`
		MedianPct  float64 `json:"median_pct"`
		MinPct     float64 `json:"min_pct"`
		MaxPct     float64 `json:"max_pct"`
	}
	out := struct {
		EffectivePct float64  `json:"effective_pct,omitempty"`
		Source       string   `json:"source"`
		NowPct       float64  `json:"now_pct,omitempty"`
		NowMonth     string   `json:"now_month,omitempty"`
		Windows      []window `json:"windows,omitempty"`
		Place        []place  `json:"place,omitempty"`
		Note         string   `json:"note,omitempty"`
	}{Source: "measured"}

	pts, err := s.st.CPISince(ctx, "")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if len(pts) < 2 {
		out.Source = "none"
		out.Note = "ряду цін ще немає — застосунок його добирає з НБУ при старті " +
			"й далі раз на місяць; друга лінійка реальності доти мовчить"
		writeJSON(w, http.StatusOK, out)
		return
	}
	dom := make([]domain.CPIPoint, 0, len(pts))
	for _, p := range pts {
		dom = append(dom, domain.CPIPoint{Period: p.Period, MoMBP: p.MoMBP, YoYBP: p.YoYBP})
	}
	levels := domain.CPIChain(dom)
	last := dom[len(dom)-1]
	out.NowPct, out.NowMonth = float64(last.YoYBP)/100, last.Period

	if pct, from, to, ok := s.measuredInflation(ctx); ok {
		out.EffectivePct = pct
		out.Note = fmt.Sprintf("міряно ланцюжком місячних змін НБУ, %s — %s", from, to)
	} else {
		out.Source = "short"
		out.Note = "історії ще замало на десятирічне вікно — друга лінійка мовчить, " +
			"поки бекфіл не набере ряд"
	}

	// ВІКНО ВІДЛІЧУЄТЬСЯ ВІД ОСТАННЬОЇ ТОЧКИ РЯДУ, а не від сьогодні.
	// ІСЦ виходить із затримкою 8-10 днів, тож «рік від сьогодні» дав би
	// одинадцять місяців і підпис «за 1 рік» над відрізком, який роком не
	// є. Тут це видно одразу (обидва місяці названі поруч), але число
	// однаково було б трохи не тим, що обіцяє підпис.
	lastDate, err := domain.ParseDate(last.Period + "-01")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	for _, y := range []int{1, 3, 5, 10} {
		want := string(lastDate.AddMonths(-12 * y))[:7]
		// Найближчий НАЯВНИЙ місяць, а не рівно потрібний: ряд починається
		// там, куди дістав бекфіл, і вимагати точного збігу означало б
		// мовчки втратити найдовше вікно — саме те, на якому стоїть чинне
		// число. Обидва місяці показані в рядку, тож коротший відрізок
		// видно, а не приховано.
		from := firstAtOrAfter(levels, want)
		if pct, ok := domain.CPIAnnualPct(levels, from, last.Period); ok && from != "" {
			out.Windows = append(out.Windows, window{
				Label: fmt.Sprintf("за %d %s", y, plural(y, "рік", "роки", "років")),
				Years: y, Pct: round2(pct), From: from, To: last.Period,
			})
		}
		// Місце нинішнього річного темпу серед темпів того самого вікна.
		var yoy []int64
		for _, p := range dom {
			if p.Period >= want {
				yoy = append(yoy, p.YoYBP)
			}
		}
		if pl, ok := domain.CPIPlace(yoy, last.YoYBP, y); ok {
			out.Place = append(out.Place, place{
				Years: y, Points: pl.Points, Percentile: round2(pl.Percentile),
				MedianPct: float64(pl.MedianBP) / 100,
				MinPct:    float64(pl.MinBP) / 100,
				MaxPct:    float64(pl.MaxBP) / 100,
			})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// firstAtOrAfter — перший місяць ряду, не давніший за want. Порожньо, коли
// таких немає.
func firstAtOrAfter(levels []domain.CPILevel, want string) string {
	for _, l := range levels {
		if l.Period >= want {
			return l.Period
		}
	}
	return ""
}
