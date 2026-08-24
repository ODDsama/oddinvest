// Фаза «валютне вікно»: де стоїть сьогоднішній курс серед історії.
//
// Друга з фаз buildState, що дивиться не на портфель, а на зовнішній
// орієнтир — поруч зі state_market.go і за тим самим зразком: чиста
// проєкція рядків сховища на рядки контракту, жодного звернення до бази
// (воно в state_sources.go, як вимагає sources-boundary).
//
// ЧОМУ РІЗНИЦЮ РАХУЄМО ТУТ, А НЕ В СПОЖИВАЧА. Споживачів у цього числа
// вже двоє — картка біля форми конвертації у вебі й атрибути сенсора в
// Home Assistant, — і кожен вибирав би базу самостійно. Той самий
// аргумент дослівно стоїть над buildMarket сусідом.
//
// Порада зі стратегії тут не з'являється: довгий абзац про цю межу — у
// шапці domain/fxwindow.go, і він стосується всього ланцюжка до самого
// екрана.
package api

import (
	"math"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// fxWindowYears — вікна, які показуємо. Три, а не одне: аргумент у шапці
// state.FXWindowRow.
var fxWindowYears = []int{1, 3, 10}

type fxWindowPhase struct {
	rows []state.FXWindowRow
}

// buildFXWindow — історія курсів на рядки контракту.
//
// hist — уся наявна історія по валюті (не старіша за найдовше вікно);
// відбір під конкретне вікно робимо тут, бо саме тут є сьогодні.
//
// deficitUAH — валютний дефіцит із фази ребалансу, грн-екв. Приходить
// ГОТОВИМ, а не рахується вдруге: власна копія арифметики часток у цьому
// пакеті вже одного разу розвела плитку з карткою (handlers_reinvest.go).
func buildFXWindow(hist map[string][]store.RatePoint, rates fx.Rates,
	deficitUAH map[string]float64, today domain.Date) fxWindowPhase {
	if len(hist) == 0 {
		return fxWindowPhase{}
	}
	out := make([]state.FXWindowRow, 0, len(hist)*len(fxWindowYears))
	// Валюти обходимо ЗА СПИСКОМ, а не мапою: порядок рядків у документі
	// має бути той самий від запуску до запуску, інакше retained-стан у
	// MQTT переписувався б тим самим змістом у новому порядку.
	for _, cur := range fxHistoryCurrencies {
		points := hist[cur]
		if len(points) == 0 {
			continue
		}
		nowE4 := rates[cur]
		nowMajor, ok := fx.RateMajor(cur, rates)
		if !ok {
			continue // курсу на сьогодні немає — немає й місця, куди його ставити
		}
		for _, years := range fxWindowYears {
			from := today.AddMonths(-12 * years)
			vals := make([]int64, 0, len(points))
			for _, p := range points {
				if !p.Date.Before(from) {
					vals = append(vals, p.RateE4)
				}
			}
			w, ok := domain.FXPlace(vals, nowE4, years)
			if !ok {
				continue // історії менше за рік: краще нічого, ніж перцентиль на трьох точках
			}
			row := state.FXWindowRow{
				Currency: cur, Years: w.Years, Points: w.Points,
				Percentile: round2(w.Percentile),
				NowRate:    round4(nowMajor),
				MedianRate: round4(fx.Major(w.MedianE4)),
				MinRate:    round4(fx.Major(w.MinE4)),
				MaxRate:    round4(fx.Major(w.MaxE4)),
			}
			// Скільки валюти дефіцит купує сьогодні проти медіани вікна.
			// Ділимо на МАЖОРНІ курси, тобто масштаб ×10⁴ за межі fx не
			// витікає (fx-boundary).
			if d := deficitUAH[cur]; d > 0 && row.MedianRate > 0 && row.NowRate > 0 {
				row.VsMedianNative = round2(d/row.NowRate - d/row.MedianRate)
			}
			out = append(out, row)
		}
	}
	return fxWindowPhase{rows: out}
}

// round4 — під курс, у якого чотири знаки за визначенням НБУ. round2
// поруч (state_builder.go) під гроші й відсотки; третього масштабу тут
// не заводимо.
func round4(v float64) float64 { return math.Round(v*10_000) / 10_000 }

// currencyDeficitUAH — валютний дефіцит із готових рядків ребалансу.
//
// Саме з рядків, а не з часток і капіталу: у цьому пакеті вже одного разу
// з'явилась власна копія арифметики часток, і через неї плитка й картка
// показували різні числа (розбір — у handlers_reinvest.go).
//
// Рядків без цілі тут немає за побудовою: buildRebalance не малює
// валютного рядка, доки цілі не задано, — тож порожня мапа на виході
// означає «валютної політики немає», і різниці до медіани не буде.
func currencyDeficitUAH(rows []state.RebalanceRow) map[string]float64 {
	out := map[string]float64{}
	for _, r := range rows {
		if r.Dimension == "currency" && r.DeficitUAH > 0 {
			out[r.Key] = r.DeficitUAH
		}
	}
	return out
}
