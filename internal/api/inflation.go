package api

import (
	"context"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Інфляція — ДРУГА лінійка реальності поруч зі знеціненням.
//
// Знецінення (devaluation.go) відповідає на питання «скільки доларів
// купить моя гривня», ІСЦ — «скільки товарів». Ці два числа в Україні
// розходяться на роках, і жодне не є поправкою до іншого; складати їх не
// можна й ніде не складено.
//
// СХОДИНКА ТУТ ОДНА, на відміну від знецінення, у якого їх три. У
// знецінення ручний ярус є тому, що очікування людини з даних не
// виводиться, а типове число — тому, що без курсів застосунок мусить
// якось рахувати з першої хвилини. ІСЦ — опублікований факт: або він
// виміряний, або його немає, і тоді рядок мовчить. Вигадане типове
// значення тут було б рівно тією помилкою, від якої застерігає README у
// десятку інших місць.
const (
	// inflWindowYears — те саме десятирічне вікно, що й у знецінення:
	// два числа стоятимуть поруч на кожному екрані, і виміряні різними
	// відрізками вони порівнювались би нечесно.
	inflWindowYears = 10
	// inflMinDays — вісім років із десяти, дослівно як devalMinDays.
	inflMinDays = 8 * 365
)

// measuredInflation — річний темп зростання цін із ряду НБУ.
//
// Повертає ще й межі вікна: картка мусить сказати, ЯКИМ відрізком
// міряно, — інакше «8.1%/рік» неможливо ні перевірити, ні зрозуміти.
func (s *Server) measuredInflation(ctx context.Context) (pct float64, from, to string, ok bool) {
	start := monthOf(domain.NewDate(time.Now().AddDate(-inflWindowYears, 0, 0)))
	pts, err := s.st.CPISince(ctx, start)
	if err != nil || len(pts) < 2 {
		return 0, "", "", false
	}
	dom := make([]domain.CPIPoint, 0, len(pts))
	for _, p := range pts {
		dom = append(dom, domain.CPIPoint{Period: p.Period, MoMBP: p.MoMBP, YoYBP: p.YoYBP})
	}
	levels := domain.CPIChain(dom)
	first, last := levels[0].Period, levels[len(levels)-1].Period
	a, errA := domain.ParseDate(first + "-01")
	b, errB := domain.ParseDate(last + "-01")
	if errA != nil || errB != nil || domain.DaysBetween(a, b) < inflMinDays {
		return 0, "", "", false
	}
	v, ok := domain.CPIAnnualPct(levels, first, last)
	if !ok {
		return 0, "", "", false
	}
	return round2(v), first, last, true
}

// inflation — інфляція, з якою рахує застосунок: виміряна або ніякої.
func (s *Server) inflation(ctx context.Context) (float64, bool) {
	pct, _, _, ok := s.measuredInflation(ctx)
	return pct, ok
}
