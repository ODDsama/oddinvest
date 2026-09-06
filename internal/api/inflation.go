package api

import (
	"context"

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
// Вікно відлічується від ОСТАННЬОЇ ТОЧКИ РЯДУ назад, а не від сьогодні:
// ІСЦ виходить із затримкою 8-10 днів, і «десять років від сьогодні» дало
// б відрізок, коротший за свою назву. Той самий якір, що в /api/inflation,
// і саме тому чинне число дорівнює рядкові «за 10 років» у картці — два
// різні якорі давали б два різні числа на одному екрані.
//
// Повертає ще й межі вікна: картка мусить сказати, ЯКИМ відрізком міряно,
// інакше «10.6%/рік» неможливо ні перевірити, ні зрозуміти.
func (s *Server) measuredInflation(ctx context.Context) (pct float64, from, to string, ok bool) {
	pts, err := s.st.CPISince(ctx, "")
	if err != nil || len(pts) < 2 {
		return 0, "", "", false
	}
	dom := make([]domain.CPIPoint, 0, len(pts))
	for _, p := range pts {
		dom = append(dom, domain.CPIPoint{Period: p.Period, MoMBP: p.MoMBP, YoYBP: p.YoYBP})
	}
	// ДІРКА В РЯДУ РОБИТЬ ЧИСЛО ХИБНИМ МОВЧКИ, тож замість неї — мовчання.
	// Пропущений місяць просто не множиться, рівень виходить нижчим, а
	// результат лишається правдоподібним: на бойовому 32 дірки дали
	// 7.92%/рік замість 10.72%. Показати таке число гірше, ніж не
	// показати жодного, — це рівно той клас помилки, від якого README
	// застерігає в десятку інших місць.
	if gaps := domain.CPIGaps(dom); len(gaps) > 0 {
		return 0, "", "", false
	}
	levels := domain.CPIChain(dom)
	last := levels[len(levels)-1].Period
	lastDate, err := domain.ParseDate(last + "-01")
	if err != nil {
		return 0, "", "", false
	}
	start := firstAtOrAfter(levels, string(lastDate.AddMonths(-12 * inflWindowYears))[:7])
	if start == "" {
		return 0, "", "", false
	}
	a, errA := domain.ParseDate(start + "-01")
	if errA != nil || domain.DaysBetween(a, lastDate) < inflMinDays {
		return 0, "", "", false
	}
	v, ok := domain.CPIAnnualPct(levels, start, last)
	if !ok {
		return 0, "", "", false
	}
	return round2(v), start, last, true
}

// inflation — інфляція, з якою рахує застосунок: виміряна або ніякої.
func (s *Server) inflation(ctx context.Context) (float64, bool) {
	pct, _, _, ok := s.measuredInflation(ctx)
	return pct, ok
}
