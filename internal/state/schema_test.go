package state

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// Сторож контракту: JSON Schema має описувати РІВНО те, що віддає код.
//
// Досі схему не перевіряв ніхто. Фікстури звірялись лише самі з собою
// (TestFixtureUpToDate), а валідація фікстури проти схеми пропускає зайве:
// незадокументоване поле просто проходить повз. За цей час схема встигла
// розійтися з Doc в обидва боки — обіцяла чотири поля, яких код ніколи не
// віддавав, і мовчала про дев'ятнадцять, які віддавав. Причому README і
// сам пакет називають її джерелом правди контракту.
//
// Перевірка йде рефлексією по структурі, а не по фікстурі: фікстура
// показує лише те, що трапилось у sampleInput, і поле, якого там немає,
// лишалось би непоміченим.

func schemaProps(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("../../contract/oddinvest-state.schema.json")
	if err != nil {
		t.Fatalf("схема не читається: %v", err)
	}
	var s struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("схема не розбирається: %v", err)
	}
	return s.Properties
}

// jsonNames — імена полів структури так, як вони виходять у JSON.
func jsonNames(typ reflect.Type) []string {
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if name := strings.Split(tag, ",")[0]; name != "" {
			out = append(out, name)
		}
	}
	return out
}

// nested — вкладений об'єкт схеми (наприклад, properties.settings.properties).
func nested(t *testing.T, props map[string]any, key string) map[string]any {
	t.Helper()
	obj, ok := props[key].(map[string]any)
	if !ok {
		t.Fatalf("у схемі немає об'єкта %q", key)
	}
	inner, ok := obj["properties"].(map[string]any)
	if !ok {
		t.Fatalf("у схемі немає %q.properties", key)
	}
	return inner
}

// additional — властивості ЗНАЧЕННЯ мапи
// (properties.<key>.additionalProperties.properties). Мапа по валютах —
// такий самий рядок контракту, як елемент масиву, тільки під іншим
// ключем схеми, і розійтися зі структурою може так само тихо.
func additional(t *testing.T, props map[string]any, key string) map[string]any {
	t.Helper()
	obj, ok := props[key].(map[string]any)
	if !ok {
		t.Fatalf("у схемі немає мапи %q", key)
	}
	ap, ok := obj["additionalProperties"].(map[string]any)
	if !ok {
		t.Fatalf("у схемі немає %q.additionalProperties", key)
	}
	inner, ok := ap["properties"].(map[string]any)
	if !ok {
		t.Fatalf("у схемі немає %q.additionalProperties.properties", key)
	}
	return inner
}

// items — властивості ЕЛЕМЕНТА масиву (properties.<key>.items.properties).
func items(t *testing.T, props map[string]any, key string) map[string]any {
	t.Helper()
	obj, ok := props[key].(map[string]any)
	if !ok {
		t.Fatalf("у схемі немає масиву %q", key)
	}
	it, ok := obj["items"].(map[string]any)
	if !ok {
		t.Fatalf("у схемі немає %q.items", key)
	}
	inner, ok := it["properties"].(map[string]any)
	if !ok {
		t.Fatalf("у схемі немає %q.items.properties", key)
	}
	return inner
}

func checkAgainst(t *testing.T, what string, typ reflect.Type, props map[string]any) {
	t.Helper()
	fields := jsonNames(typ)
	inCode := map[string]bool{}
	for _, f := range fields {
		inCode[f] = true
		if _, ok := props[f]; !ok {
			t.Errorf("%s: код віддає %q, а схема про нього мовчить — допиши в contract/oddinvest-state.schema.json", what, f)
		}
	}
	for name := range props {
		if !inCode[name] {
			t.Errorf("%s: схема обіцяє %q, якого код не віддає — прибери зі схеми або поверни в код", what, name)
		}
	}
}

func TestSchemaMatchesDoc(t *testing.T) {
	props := schemaProps(t)
	checkAgainst(t, "Doc", reflect.TypeOf(Doc{}), props)
}

func TestSchemaMatchesSettings(t *testing.T) {
	props := schemaProps(t)
	checkAgainst(t, "SettingsDoc", reflect.TypeOf(SettingsDoc{}), nested(t, props, "settings"))
}

// TestSchemaMatchesNestedTypes — те саме правило на рівень глибше.
//
// Доти сторож дивився ЛИШЕ на верхній рівень Doc і на settings, тож
// вкладені рядки могли розходитися зі схемою скільки завгодно й нічим не
// падати. Небезпека тут гостріша, ніж нагорі, з двох причин.
//
// По-перше, схема не має $defs: форма PaymentRow виписана в ній ТРИЧІ —
// у calendar.items, top_payments.items і next_payment, — і кожне нове
// поле треба дописати в усі три. Забути одне з трьох легко, а помітити
// нічим: фікстура валідується проти схеми, а зайве поле схема просто
// пропускає повз.
//
// По-друге, саме сюди й ростуть нові поля контракту: верхній рівень
// стабільний, а рядки набирають ознак.
func TestSchemaMatchesNestedTypes(t *testing.T) {
	props := schemaProps(t)
	// Масиви: тип елемента проти <ключ>.items.properties.
	for _, c := range []struct {
		key string
		typ reflect.Type
	}{
		{"ladder", reflect.TypeOf(LadderRow{})},
		{"top_payments", reflect.TypeOf(PaymentRow{})},
		{"calendar", reflect.TypeOf(PaymentRow{})},
		{"projection", reflect.TypeOf(ProjectionRow{})},
		{"rebalance", reflect.TypeOf(RebalanceRow{})},
		{"concentration", reflect.TypeOf(ConcentrationRow{})},
		{"market_yield", reflect.TypeOf(MarketYieldRow{})},
		{"fx_window", reflect.TypeOf(FXWindowRow{})},
		{"funds", reflect.TypeOf(FundPositionRow{})},
		{"ladder_uah", reflect.TypeOf(YearAmount{})},
		{"income_12m", reflect.TypeOf(MonthAmount{})},
		{"coupons_12m", reflect.TypeOf(MonthAmount{})},
	} {
		checkAgainst(t, c.key+".items", c.typ, items(t, props, c.key))
	}
	// Об'єкти: тип проти <ключ>.properties.
	for _, c := range []struct {
		key string
		typ reflect.Type
	}{
		{"next_payment", reflect.TypeOf(NextPayment{})},
		{"total_return", reflect.TypeOf(TotalReturn{})},
		{"funds_yield_split", reflect.TypeOf(YieldSplit{})},
		{"blended_yield_split", reflect.TypeOf(YieldSplit{})},
		{"reserve", reflect.TypeOf(Reserve{})},
		{"month_plan", reflect.TypeOf(MonthPlan{})},
		{"liquidity", reflect.TypeOf(Liquidity{})},
		{"rate_risk", reflect.TypeOf(RateRisk{})},
		{"drawdown", reflect.TypeOf(Drawdown{})},
		{"independence", reflect.TypeOf(Independence{})},
		{"sensitivity", reflect.TypeOf(Sensitivity{})},
		{"forecast", reflect.TypeOf(Forecast{})},
		{"capital_delta_30", reflect.TypeOf(CapitalDelta{})},
		{"debt", reflect.TypeOf(DebtPlan{})},
	} {
		checkAgainst(t, c.key, c.typ, nested(t, props, c.key))
	}
	// Другий рівень під debt: доти цих трьох типів сторож не бачив зовсім,
	// і поля DebtExit могли розходитись зі схемою, нічим не падаючи.
	debt := nested(t, props, "debt")
	checkAgainst(t, "debt.exit", reflect.TypeOf(DebtExit{}), nested(t, debt, "exit"))
	checkAgainst(t, "debt.cards.items", reflect.TypeOf(DebtCard{}), items(t, debt, "cards"))
	checkAgainst(t, "debt.exit.schedule.items", reflect.TypeOf(DebtExitStep{}),
		items(t, nested(t, debt, "exit"), "schedule"))
	// Мапи: тип значення проти <ключ>.additionalProperties.properties.
	checkAgainst(t, "realized[*]", reflect.TypeOf(RealizedRow{}),
		additional(t, props, "realized"))
}
