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
