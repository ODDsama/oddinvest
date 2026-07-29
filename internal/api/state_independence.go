// Точка незалежності: коли пасивний дохід покриє життя.
//
// Картка «Пасивний дохід» каже, скільки портфель приноситиме через рік,
// три, п'ять і десять. Питання, на яке вона не відповідає, — коли цього
// стане ДОСИТЬ, тобто коли потік перевищить те, у скільки обходиться
// місяць життя.
//
// «Досить» бере користувач, а не ми: income_target_uah у налаштуваннях,
// зі спадом на місячні витрати. Спад найчастіший, але не єдиний розумний
// — половина витрат теж ціль, і вгадувати за людину ми не будемо.
//
// ДВІ ДАТИ, і це не надмірність. За планом — якщо вносити стільки,
// скільки виходить із цілі; за фактом — скільки виходить насправді. Одна
// дата без другої або лестить, або лякає; різниця між ними і є ціна
// дисципліни, а не ринку.
package api

import (
	"math"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
)

// independenceInput — понад те, що вже порахувала проєкція.
type independenceInput struct {
	Factory sleeveFactory
	Deval   float64
	// ContribPlan — внесок плану; ContribActual — фактичний темп (0, якщо
	// історії ще замало).
	ContribPlan   float64
	ContribActual float64
	// TargetUAH — достатній дохід; TargetFrom — "setting" чи "expenses".
	TargetUAH  float64
	TargetFrom string
	// IncomeNowUAH — скільки портфель приносить уже зараз, ₴/міс. Береться
	// з готового income_monthly_now, а не рахується вдруге: те саме число
	// на двох сусідніх картках мусить бути тим самим.
	IncomeNowUAH float64
	Today        domain.Date
}

// buildIndependence шукає місяць, коли дохід перетне ціль.
func buildIndependence(in independenceInput) *state.Independence {
	if in.TargetUAH <= 0 {
		return nil
	}
	out := &state.Independence{
		TargetUAH:    round2(in.TargetUAH),
		TargetFrom:   in.TargetFrom,
		IncomeNowUAH: round2(in.IncomeNowUAH),
	}

	planSleeves := in.Factory.build(in.ContribPlan, 0)
	out.PlanMonths = domain.MonthsToIncomeSleeves(
		planSleeves, in.Deval, in.TargetUAH, goalHorizonMonths)
	out.PlanDate = goalDate(in.Today, out.PlanMonths)
	// Капітал на той момент — скільки за ним стоїть. Саме ПРОЄКТОВАНИЙ, а
	// не «потрібний»: потрібна сума залежить від ставки того місяця, і
	// друге незалежне число про те саме лише розходилось би з першим.
	if out.PlanMonths > 0 {
		out.CapitalUAH = round2(
			domain.ProjectSleeves(planSleeves, in.Deval, out.PlanMonths).TodayUAH)
	}

	if in.ContribActual > 0 {
		out.ActualMonths = domain.MonthsToIncomeSleeves(
			in.Factory.build(in.ContribActual, 0), in.Deval, in.TargetUAH, goalHorizonMonths)
		out.ActualDate = goalDate(in.Today, out.ActualMonths)
	}
	return out
}

// drawdownInput — понад те, що вже порахувала проєкція.
type drawdownInput struct {
	Factory sleeveFactory
	Deval   float64
	// WithdrawUAH — скільки знімати щомісяця, у СЬОГОДНІШНІХ гривнях;
	// WithdrawFrom — "setting" чи "expenses".
	WithdrawUAH  float64
	WithdrawFrom string
	// IncomeNowUAH — сьогоднішній дохід. Потрібен, щоб сказати, яку
	// частку зняття портфель покриває вже зараз: саме це пояснює, чому
	// число вийшло таким, а не інакшим.
	IncomeNowUAH float64
	Today        domain.Date
}

// buildDrawdown рахує, на скільки вистачить без внесків.
func buildDrawdown(in drawdownInput) *state.Drawdown {
	if in.WithdrawUAH <= 0 {
		return nil
	}
	// Рукави БЕЗ внеску: це й є декумуляція.
	sl := in.Factory.build(0, 0)
	months := domain.DrawdownMonths(sl, in.Deval, in.WithdrawUAH, goalHorizonMonths)
	out := &state.Drawdown{
		WithdrawUAH:  round2(in.WithdrawUAH),
		WithdrawFrom: in.WithdrawFrom,
		Months:       months,
		CoveredPct:   round1(in.IncomeNowUAH / in.WithdrawUAH * 100),
	}
	if months > 0 {
		out.Until = string(domain.NewDate(in.Today.Time().AddDate(0, months, 0)))
	}
	return out
}

// round1 — одна десята. Той самий крок, що й у goal_pct: два різні
// заокруглення для однорідних відсотків читались би як розбіжність.
func round1(v float64) float64 { return math.Round(v*10) / 10 }
