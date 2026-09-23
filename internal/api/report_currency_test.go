package api

import (
	"github.com/ODDsama/oddinvest/internal/settings"
	"strings"
	"testing"
)

// Валюта звітності — перелік, і описка не проходить: «usd» замість «USD»
// мовчки лишив би документ у гривні, а людина шукала б, чому долар не
// вмикається. Той самий довід, що при reserve_fill_from.
func TestReportCurrencyEnum(t *testing.T) {
	for _, v := range []string{"UAH", "USD", "EUR", ""} {
		if err := settings.Validate(map[string]string{"report_currency": v}); err != nil {
			t.Errorf("значення %q відхилене: %v", v, err)
		}
	}
	err := settings.Validate(map[string]string{"report_currency": "usd"})
	if err == nil {
		t.Fatal("«usd» пройшло — валюта звітності приймає лише коди з переліку")
	}
	if !strings.Contains(err.Error(), "report_currency") {
		t.Errorf("помилка не називає ключа: %v", err)
	}
}
