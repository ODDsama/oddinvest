package api

import (
	"testing"

	"github.com/ODDsama/oddinvest/internal/state"
)

func sensSettings() *state.SettingsDoc {
	goal := 2_000_000.0
	return &state.SettingsDoc{GoalAmountUAH: &goal, GoalDate: "2032-01-01"}
}

// sens будує чутливість на тому самому мінімальному вході, що й
// forecastInput, але з відомим фактичним темпом.
func sens(t *testing.T, actual float64) *state.Sensitivity {
	t.Helper()
	in := forecastInput(t, sensSettings())
	in.ActualMonthly = actual
	out := buildProjection(in).Sensitivity
	if out == nil {
		t.Fatal("чутливості немає, хоч ціль, дедлайн і темп задані")
	}
	return out
}

// TestSensitivityContribIsMonotone — більший внесок НЕ може віддалити
// ціль.
//
// Головний сторож фази. Кожен рядок — окремий прогін симуляції зі
// збуреним входом, і зіпсувати можна кожен окремо: переплутати множник,
// подати не той внесок, зібрати рукави від базового значення. Усе це
// дасть правдоподібні числа, і помітити їх на око неможливо — а от
// порушення монотонності видно одразу.
//
// Нуль у goal_months означає «не досягається за 60 років», тобто
// найгірший результат, а не найкращий: у порівнянні його треба
// розгортати, інакше тест сам себе обдурить.
func TestSensitivityContribIsMonotone(t *testing.T) {
	out := sens(t, 20_000)

	// Місяці до цілі за зростанням внеску: ×0.5, база, ×1.5, ×2.
	byFactor := map[float64]int{}
	for _, r := range out.Rows {
		if r.Lever == "contrib" {
			byFactor[r.Factor] = r.GoalMonths
		}
	}
	seq := []struct {
		what   string
		months int
	}{
		{"×0.5", byFactor[0.5]},
		{"база", out.BaseGoalMonths},
		{"×1.5", byFactor[1.5]},
		{"×2", byFactor[2]},
	}
	rank := func(m int) int {
		if m == 0 {
			return 1 << 30 // не досягається — гірше за будь-який строк
		}
		return m
	}
	for i := 1; i < len(seq); i++ {
		if rank(seq[i].months) > rank(seq[i-1].months) {
			t.Errorf("внесок %s дав ціль ПІЗНІШЕ (%d міс), ніж %s (%d міс) — "+
				"більший внесок не може віддаляти ціль",
				seq[i].what, seq[i].months, seq[i-1].what, seq[i-1].months)
		}
	}

	// Те саме для сум на дедлайн: більший внесок — не менша сума.
	byFactorAmt := map[float64]float64{}
	for _, r := range out.Rows {
		if r.Lever == "contrib" {
			byFactorAmt[r.Factor] = r.AmountUAH
		}
	}
	if byFactorAmt[0.5] >= out.BaseAmountUAH || byFactorAmt[1.5] <= out.BaseAmountUAH ||
		byFactorAmt[2] <= byFactorAmt[1.5] {
		t.Errorf("суми на дедлайн не зростають із внеском: ×0.5=%v база=%v ×1.5=%v ×2=%v",
			byFactorAmt[0.5], out.BaseAmountUAH, byFactorAmt[1.5], byFactorAmt[2])
	}
}

// TestSensitivityMarketLeversPointTheRightWay — кращий ринок наближає
// ціль, гірший віддаляє.
//
// Знак тут переплутати особливо легко: для ставки «+» означає кращий
// ринок, а для знецінення «+» — ГІРШИЙ. Дві протилежні угоди в сусідніх
// циклах — саме та форма, у якій помилка виглядає нормально.
func TestSensitivityMarketLeversPointTheRightWay(t *testing.T) {
	out := sens(t, 20_000)
	pick := func(lever string, pp float64) state.SensitivityRow {
		for _, r := range out.Rows {
			if r.Lever == lever && r.DeltaPP == pp {
				return r
			}
		}
		t.Fatalf("немає рядка %s %+v", lever, pp)
		return state.SensitivityRow{}
	}
	if up := pick("rate", 3); up.AmountUAH <= out.BaseAmountUAH {
		t.Errorf("ставка +3 п.п. дала не більше грошей (%v проти %v)", up.AmountUAH, out.BaseAmountUAH)
	}
	if down := pick("rate", -3); down.AmountUAH >= out.BaseAmountUAH {
		t.Errorf("ставка −3 п.п. дала не менше грошей (%v проти %v)", down.AmountUAH, out.BaseAmountUAH)
	}
	// Знецінення: мінус до нього — це КРАЩЕ, бо гривня слабшає повільніше.
	if better := pick("deval", -4); better.AmountUAH <= out.BaseAmountUAH {
		t.Errorf("менше знецінення дало не більше сьогоднішніх грошей (%v проти %v)",
			better.AmountUAH, out.BaseAmountUAH)
	}
	if worse := pick("deval", 4); worse.AmountUAH >= out.BaseAmountUAH {
		t.Errorf("більше знецінення дало не менше сьогоднішніх грошей (%v проти %v)",
			worse.AmountUAH, out.BaseAmountUAH)
	}
}

// TestSensitivityDeadlineDoesNotMoveGoalMonths — зсув дедлайну змінює
// суму, але НЕ місяць досягнення цілі.
//
// Це та властивість, яку легко зламати «наведенням ладу»: рядок виглядає
// як усі інші, і хочеться перерахувати в ньому все. Але місяць
// досягнення каже, КОЛИ ціль буде досягнута, а дедлайн — коли її чекають;
// це різні питання, і перерахунок зробив би з рядка тавтологію.
func TestSensitivityDeadlineDoesNotMoveGoalMonths(t *testing.T) {
	out := sens(t, 20_000)
	var seen int
	for _, r := range out.Rows {
		if r.Lever != "deadline" {
			continue
		}
		seen++
		if r.GoalMonths != out.BaseGoalMonths {
			t.Errorf("дедлайн %+d міс зрушив місяць досягнення (%d проти %d) — "+
				"а він від дедлайну не залежить", r.DeltaMonths, r.GoalMonths, out.BaseGoalMonths)
		}
		if r.Value != float64(out.DeadlineMonths+r.DeltaMonths) {
			t.Errorf("value %v не дорівнює дедлайну після зсуву (%d%+d)",
				r.Value, out.DeadlineMonths, r.DeltaMonths)
		}
	}
	if seen != 2 {
		t.Errorf("рядків дедлайну %d, очікували 2", seen)
	}
}

// TestSensitivityBaseAgreesWithForecastActual — база чутливості й рядок
// «За фактом» у прогнозі мусять давати ОДНЕ І ТЕ САМЕ.
//
// Це дві сусідні картки на одному екрані, і питання в них одне: «куди я
// прийду за нинішнім темпом». Розійтись їм не можна — читач побачив би
// 9% в одній і 7.6% в іншій, і жодного натяку, чому.
//
// Обидві мусять бути наслідком одного прогону: той самий внесок, той
// самий ринок, той самий дедлайн. Тест звіряє всі три виходи, а не лише
// відсоток: збіг одного числа буває випадковим.
func TestSensitivityBaseAgreesWithForecastActual(t *testing.T) {
	in := forecastInput(t, sensSettings())
	in.ActualMonthly = 20_000
	out := buildProjection(in)
	if out.Sensitivity == nil || out.Forecast == nil {
		t.Fatal("немає чутливості або прогнозу")
	}
	var actual *state.ForecastRow
	for i := range out.Forecast.Rows {
		if out.Forecast.Rows[i].Key == "actual" {
			actual = &out.Forecast.Rows[i]
		}
	}
	if actual == nil {
		t.Fatal("у прогнозі немає рядка «За фактом», хоч темп заданий")
	}
	s := out.Sensitivity
	if actual.ContribMonthly != s.BaseContribUAH {
		t.Errorf("внесок: прогноз %v, чутливість %v", actual.ContribMonthly, s.BaseContribUAH)
	}
	if actual.Amount != s.BaseAmountUAH {
		t.Errorf("сума на дедлайн: прогноз %v, чутливість %v", actual.Amount, s.BaseAmountUAH)
	}
	if actual.GoalMonths != s.BaseGoalMonths {
		t.Errorf("місяців до цілі: прогноз %d, чутливість %d", actual.GoalMonths, s.BaseGoalMonths)
	}
}

// TestSensitivityBasePrefersActualPace — база береться з ФАКТИЧНОГО
// темпу, коли він відомий.
//
// «×2 від плану, якого ти не тягнеш» — марна відповідь: на живих даних
// план 94 251 ₴/міс проти факту 7 955, і важелі від плану говорили б про
// портфель, якого немає.
func TestSensitivityBasePrefersActualPace(t *testing.T) {
	withPace := sens(t, 20_000)
	if withPace.BaseFrom != "actual" || withPace.BaseContribUAH != 20_000 {
		t.Errorf("база %q %v; очікували фактичний темп 20000",
			withPace.BaseFrom, withPace.BaseContribUAH)
	}
	// Без історії поповнень лишається план — інакше картки не було б зовсім.
	noPace := sens(t, 0)
	if noPace.BaseFrom != "plan" {
		t.Errorf("без фактичного темпу база %q; очікували план", noPace.BaseFrom)
	}
}
