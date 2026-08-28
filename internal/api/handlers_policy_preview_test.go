package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/ODDsama/oddinvest/internal/state"
)

// policyDraft — набір, який ставить «Гривневий потік» на рівні без замка.
// Саме пʼятнадцять ключів, як їх шле «Політика»: превʼю, що бачило б інший
// перелік, ніж запис, перевіряло б не те, що відбудеться.
const policyDraft = `{"settings":{
	"usd_target_share_pct":"10","eur_target_share_pct":"",
	"target_bonds_pct":"90","target_funds_pct":"10","target_deposits_pct":"",
	"target_npf_pct":"",
	"limit_isin_pct":"25","limit_broker_pct":"60","limit_year_pct":"40",
	"reserve_target_months":"3","reserve_fill_share_pct":"",
	"reserve_liquid_months":"","reserve_max_term_months":"",
	"reserve_fill_from":"any",
	"reinvest_rank":"rate"}}`

// policySettingsBody — те саме, але тілом PUT /api/settings.
const policySettingsBody = `{
	"usd_target_share_pct":"10","eur_target_share_pct":"",
	"target_bonds_pct":"90","target_funds_pct":"10","target_deposits_pct":"",
	"target_npf_pct":"",
	"limit_isin_pct":"25","limit_broker_pct":"60","limit_year_pct":"40",
	"reserve_target_months":"3","reserve_fill_share_pct":"",
	"reserve_liquid_months":"","reserve_max_term_months":"",
	"reserve_fill_from":"any",
	"reinvest_rank":"rate"}`

type policySections struct {
	Rebalance     []state.RebalanceRow     `json:"rebalance"`
	Concentration []state.ConcentrationRow `json:"concentration"`
}

// ГОЛОВНИЙ тест цього файлу: превʼю мусить показувати рівно ті числа, які
// зʼявляться після справжнього запису.
//
// Інакше воно не превʼю, а другий спосіб відповісти на те саме питання — і
// саме та розбіжність, від якої в цьому застосунку стоїть половина
// коментарів: два числа про одну частку капіталу вже двічі жили на одному
// екрані.
//
// Порядок дій у тесті значущий: спершу превʼю (політика ще СТАРА), і лише
// потім запис. Навпаки тест був би зеленим навіть тоді, коли накладка не
// працює взагалі.
func TestPolicyPreviewMatchesRealWrite(t *testing.T) {
	url := whatIfServer(t)

	resp, body := do(t, "POST", url+"/api/policy/preview", policyDraft)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("превʼю: %d %s", resp.StatusCode, body)
	}
	var preview policySections
	if err := json.Unmarshal([]byte(body), &preview); err != nil {
		t.Fatalf("превʼю не розбирається: %v", err)
	}
	// Порожня відповідь зробила б тест зеленим ні про що.
	if len(preview.Rebalance) == 0 {
		t.Fatal("превʼю без жодного рядка ребалансу — порівнювати нема чого")
	}

	if resp, body = do(t, "PUT", url+"/api/settings", policySettingsBody); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("запис налаштувань: %d %s", resp.StatusCode, body)
	}
	resp, body = do(t, "GET", url+"/api/summary", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("зведення: %d %s", resp.StatusCode, body)
	}
	var after policySections
	if err := json.Unmarshal([]byte(body), &after); err != nil {
		t.Fatalf("зведення не розбирається: %v", err)
	}
	if !reflect.DeepEqual(preview.Rebalance, after.Rebalance) {
		t.Errorf("ребаланс розійшовся:\nпревʼю %+v\nпісля  %+v", preview.Rebalance, after.Rebalance)
	}
	if !reflect.DeepEqual(preview.Concentration, after.Concentration) {
		t.Errorf("концентрація розійшлась:\nпревʼю %+v\nпісля  %+v",
			preview.Concentration, after.Concentration)
	}
}

// Гіпотеза не має права нічого записати. Перевіряється не полем у базі, а
// наслідком: зведення ПІСЛЯ превʼю мусить лишитись таким, яким було до
// нього, — тобто без цілей, яких ніхто не зберігав.
func TestPolicyPreviewWritesNothing(t *testing.T) {
	url := whatIfServer(t)

	resp, body := do(t, "GET", url+"/api/settings", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("налаштування: %d %s", resp.StatusCode, body)
	}
	before := body

	if resp, body = do(t, "POST", url+"/api/policy/preview", policyDraft); resp.StatusCode != http.StatusOK {
		t.Fatalf("превʼю: %d %s", resp.StatusCode, body)
	}

	if resp, body = do(t, "GET", url+"/api/settings", ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("налаштування після превʼю: %d %s", resp.StatusCode, body)
	}
	if body != before {
		t.Errorf("превʼю змінило налаштування:\nбуло  %s\nстало %s", before, body)
	}
}

// Превʼю приймає рівно те, що прийме запис. Розійтися вони не можуть за
// побудовою (перевірка спільна), і цей тест стереже саме цю спільність.
func TestPolicyPreviewRejectsWhatWriteRejects(t *testing.T) {
	url := whatIfServer(t)
	for _, tc := range []struct{ name, body string }{
		{"невідомий ключ", `{"settings":{"target_gold_pct":"10"}}`},
		{"не число", `{"settings":{"target_bonds_pct":"багато"}}`},
		{"відʼємне", `{"settings":{"target_bonds_pct":"-5"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := do(t, "POST", url+"/api/policy/preview", tc.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("очікували 400, дістали %d %s", resp.StatusCode, body)
			}
			// Той самий драфт мусить так само відлетіти й від запису —
			// інакше превʼю суворіше за реальність.
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.body), &raw); err != nil {
				t.Fatal(err)
			}
			if resp, body = do(t, "PUT", url+"/api/settings",
				string(raw["settings"])); resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("запис прийняв те, що превʼю відхилило: %d %s", resp.StatusCode, body)
			}
		})
	}
}

// Порожній портфель — звичайний стан, а не крайній випадок: у «Політику»
// заходять і до першої покупки. Відповідь мусить бути, і мусить бути 200.
func TestPolicyPreviewOnEmptyPortfolio(t *testing.T) {
	srv, st := testServer(t)
	seed(t, st)

	resp, body := do(t, "POST", srv.URL+"/api/policy/preview", policyDraft)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("превʼю на порожньому: %d %s", resp.StatusCode, body)
	}
	var out policySections
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("не розбирається: %v", err)
	}
	// Валютна ціль на порожньому портфелі — це рядок про НЕДОСЯЖНІСТЬ, і
	// саме його тут і чекаємо: цілі є, капіталу немає, тож ані дефіциту
	// (ділити нема чого), ані здійсненності бути не може. Рядок за видом
	// не зʼявляється зовсім — там знаменник нульовий.
	//
	// Це не дрібниця для превʼю: людина, яка ще нічого не купила, дістає
	// не порожню таблицю, а число, при якому ця ціль почне працювати.
	for _, r := range out.Rebalance {
		if r.Dimension != "currency" {
			t.Errorf("на порожньому портфелі зайвий рядок: %+v", r)
			continue
		}
		if r.Feasible {
			t.Errorf("ціль %s названа здійсненною при нульовому капіталі: %+v", r.Key, r)
		}
		if r.MinPortfolioUAH <= 0 {
			t.Errorf("ціль %s недосяжна, але не сказано, при якому капіталі вписалась би: %+v", r.Key, r)
		}
	}
}

// Порожнє тіло — законний запит: це стан за ЧИННОЇ політики. Окремої гілки
// в обробнику під нього немає, і тест стереже саме те, що вона не потрібна.
func TestPolicyPreviewEmptyDraftIsCurrentState(t *testing.T) {
	url := whatIfServer(t)

	if resp, body := do(t, "PUT", url+"/api/settings", policySettingsBody); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("запис налаштувань: %d %s", resp.StatusCode, body)
	}
	resp, body := do(t, "POST", url+"/api/policy/preview", `{}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("превʼю: %d %s", resp.StatusCode, body)
	}
	var preview policySections
	if err := json.Unmarshal([]byte(body), &preview); err != nil {
		t.Fatalf("не розбирається: %v", err)
	}
	resp, body = do(t, "GET", url+"/api/summary", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("зведення: %d %s", resp.StatusCode, body)
	}
	var now policySections
	if err := json.Unmarshal([]byte(body), &now); err != nil {
		t.Fatalf("не розбирається: %v", err)
	}
	if !reflect.DeepEqual(preview.Rebalance, now.Rebalance) {
		t.Errorf("порожня накладка змінила числа:\nпревʼю %+v\nстан   %+v",
			preview.Rebalance, now.Rebalance)
	}
}
