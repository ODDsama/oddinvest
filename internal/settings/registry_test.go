package settings

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ODDsama/oddinvest/internal/state"
)

// Реєстр налаштувань і SettingsDoc мусять описувати те саме.
//
// Саме цей зв'язок і був порваний: `monthly_target_uah` лишився полем
// документа й сутністю в Home Assistant після того, як бекенд прибрав
// ключ, тож кожен рух слайдера давав 400 — і жодна перевірка про це не
// знала. Тепер ключ описується в одному місці, а цей тест не дає
// реєстру й документу розійтись у будь-який бік.
func TestSettingsRegistryMatchesDoc(t *testing.T) {
	// Поля SettingsDoc, які НЕ походять від ключа: їх виводить сам
	// будівник. Кожне тут — із поясненням, інакше список стане смітником.
	derived := map[string]string{
		"monthly_target_uah": "виводиться з цілі й дедлайну, а не задається",
		"channels":           "збирається з довідника брокерів",
	}

	docFields := map[string]bool{}
	st := reflect.TypeOf(state.SettingsDoc{})
	for i := 0; i < st.NumField(); i++ {
		tag := st.Field(i).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		docFields[name] = true
	}

	// Реєстр → документ.
	for _, d := range Registry {
		if d.Num == nil && d.Str == nil {
			continue // ключ навмисно не публікується (import_since)
		}
		if !docFields[d.Key] {
			t.Errorf("реєстр знає ключ %q, а в SettingsDoc такого поля немає", d.Key)
		}
	}
	// Документ → реєстр.
	inRegistry := map[string]bool{}
	for _, d := range Registry {
		if d.Num != nil || d.Str != nil {
			inRegistry[d.Key] = true
		}
	}
	for f := range docFields {
		if inRegistry[f] {
			continue
		}
		if _, ok := derived[f]; ok {
			continue
		}
		t.Errorf("SettingsDoc віддає %q, якого немає в реєстрі — задати його буде нічим, "+
			"а PUT відповість 400", f)
	}

	// Кожне числове налаштування мусить писати в *float64, і навпаки:
	// доти список numericSettings був окремим, тож «додав ключ, забув про
	// валідацію» ніде не спливало.
	for _, d := range Registry {
		if d.Num != nil && d.Str != nil {
			t.Errorf("ключ %q описаний і як число, і як рядок", d.Key)
		}
	}
}
