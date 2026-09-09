package api

import (
	"encoding/json"
	"os"
	"testing"
)

// Сторож ПЕРЕЛІКІВ у схемі контракту.
//
// TestSchemaMatchesNestedTypes поруч звіряє, що кожне поле коду згадане в
// схемі. Але значення всередині enum він не бачить, і саме там драйф і
// стався: у `tasks.action` схема відстала на пʼять токенів
// (`confirm-route` з маршруту грошей, `pay-planned` з планових витрат,
// `pay-card`/`pay-debt` з боргу). Тобто документ, який застосунок публікує
// в MQTT, не проходив власної схеми — а дізнатись про це не було де: у Go
// схема не валідується, а `ha-oddinvest` парсить лише ті поля, які читає.
//
// Ціна мовчання не теоретична: схема — це контракт із сусіднім
// репозиторієм, і читач, який їй вірить, відкинув би задачу «внести на
// картку» як невідому.
//
// Перевірка читає схему як дані й порівняє з константами. Так само вже
// зроблено зі сторожем накладок стратегій, який парсить текст JS.

func schemaEnum(t *testing.T, path ...string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile("../../contract/oddinvest-state.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var node any
	if err := json.Unmarshal(raw, &node); err != nil {
		t.Fatal(err)
	}
	for _, k := range path {
		m, ok := node.(map[string]any)
		if !ok {
			t.Fatalf("схема: %v не веде до обʼєкта на кроці %q", path, k)
		}
		node, ok = m[k]
		if !ok {
			t.Fatalf("схема: у %v немає кроку %q", path, k)
		}
	}
	list, ok := node.([]any)
	if !ok {
		t.Fatalf("схема: %v не перелік", path)
	}
	out := map[string]bool{}
	for _, v := range list {
		out[v.(string)] = true
	}
	return out
}

// TestSchemaKnowsEveryTaskAction — кожен токен дії, який застосунок уміє
// покласти в задачу, названий у схемі.
func TestSchemaKnowsEveryTaskAction(t *testing.T) {
	got := schemaEnum(t, "properties", "tasks", "items", "properties", "action", "enum")
	// Перелік із самого коду: додаючи константу поруч, її треба назвати й
	// тут — інакше сторож не сторожить.
	want := []string{
		actRecordBuy, actTopUpDeposit, actFillReserve, actRecordNPF,
		actConfirmPay, actRecordReceipt, actReviewLimits, actSeeSuggest,
		actReviewDeposit, actHowToFund, actConfirmRoute, actFillGoal,
		actPayPlanned, actPayCard, actPayDebt,
	}
	for _, a := range want {
		if !got[a] {
			t.Errorf("токен дії %q є в коді, а схема про нього мовчить — "+
				"допиши в enum contract/oddinvest-state.schema.json", a)
		}
	}
	if len(got) != len(want) {
		t.Errorf("у схемі %d токенів, у коді %d — перелік розійшовся в інший бік",
			len(got), len(want))
	}
}

// TestSchemaKnowsEverySensitivityLever — те саме для важелів чутливості.
//
// Заведено разом із парою step_contrib/step_rate: вони й були б наступним
// драйфом, бо додаються в буквальному рядку, а не константою.
func TestSchemaKnowsEverySensitivityLever(t *testing.T) {
	got := schemaEnum(t, "properties", "sensitivity", "properties", "rows",
		"items", "properties", "lever", "enum")
	want := []string{"contrib", "rate", "deval", "deadline", "goal",
		"step_contrib", "step_rate"}
	for _, l := range want {
		if !got[l] {
			t.Errorf("важіль %q є в коді, а схема про нього мовчить", l)
		}
	}
	if len(got) != len(want) {
		t.Errorf("у схемі %d важелів, у коді %d", len(got), len(want))
	}
}
