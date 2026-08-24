package imports

import "testing"

// mono — профіль вигаданої, але типової виписки: дата, операція, папір,
// кількість, надійшло, списано.
func mono() Profile {
	return Profile{
		Name: "mono", Header: 1,
		Date: 0, Op: 1, Ref: 2, Qty: 3, Debit: 4, Credit: 5,
		Kinds: map[string]string{
			"поповнення":        "deposit",
			"виведення":         "withdrawal",
			"купівля облігацій": "bond_buy",
			"купівля":           "fund_buy",
			"продаж":            "fund_sell",
			"дивіденди":         "dividend",
		},
	}
}

func head() []string {
	return []string{"Дата", "Операція", "Папір", "Кількість", "Надійшло", "Списано"}
}

func TestProfileParsesTypicalStatement(t *testing.T) {
	rows := [][]string{head(),
		{"2026-08-01", "Поповнення рахунку", "", "", "10 000,00", ""},
		{"2026-08-02", "Купівля облігацій", "ОВДП UA4000227748", "5", "", "4 975,00"},
		{"2026-08-03", "Купівля", "ІНЖУР ЗЕМЛЯ", "12", "", "1 200,00"},
		{"2026-08-04", "Дивіденди", "ІНЖУР ЗЕМЛЯ", "", "84,50", ""},
		{"2026-08-05", "Виведення", "", "", "", "500,00"},
	}
	res, err := Parse(rows, mono())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("нічого не мало пропаститись: %+v", res.Skipped)
	}
	if len(res.Rows) != 5 {
		t.Fatalf("рядків %d, очікували 5: %+v", len(res.Rows), res.Rows)
	}
	want := []struct {
		kind   string
		fund   string
		qty    int64
		amount int64
	}{
		{"deposit", "", 0, 1_000_000},
		{"bond_buy", "UA4000227748", 5, 497_500},
		{"fund_buy", "ІНЖУР ЗЕМЛЯ", 12, 120_000},
		{"dividend", "ІНЖУР ЗЕМЛЯ", 0, 8_450},
		{"withdrawal", "", 0, 50_000},
	}
	for i, w := range want {
		got := res.Rows[i]
		if got.Kind != w.kind || got.Fund != w.fund || got.Qty != w.qty || got.Amount != w.amount {
			t.Errorf("рядок %d: маємо %+v, очікували %s/%s/%d/%d",
				i, got, w.kind, w.fund, w.qty, w.amount)
		}
	}
}

// Найдовший збіг, а не перший-ліпший: «Купівля» і «Купівля облігацій»
// стоять в одному словнику, і коротший префікс не має перехоплювати те,
// що людина описала точніше.
func TestProfilePrefersLongestPhrase(t *testing.T) {
	rows := [][]string{head(),
		{"2026-08-02", "Купівля облігацій", "UA4000227748", "5", "", "4 975,00"},
	}
	res, err := Parse(rows, mono())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0].Kind != "bond_buy" {
		t.Errorf("очікували bond_buy, маємо %+v (пропуски %+v)", res.Rows, res.Skipped)
	}
}

// Колонки можна переставити як завгодно — результат той самий. Це і є та
// властивість, заради якої профіль існує.
func TestProfileToleratesReorderedColumns(t *testing.T) {
	straight, err := Parse([][]string{head(),
		{"2026-08-03", "Купівля", "ФОНД", "10", "", "1 000,00"},
	}, mono())
	if err != nil {
		t.Fatal(err)
	}
	shuffled := mono()
	shuffled.Date, shuffled.Op, shuffled.Ref = 3, 0, 2
	shuffled.Qty, shuffled.Debit, shuffled.Credit = 1, 5, 4
	other, err := Parse([][]string{
		{"h", "h", "h", "h", "h", "h"},
		{"Купівля", "10", "ФОНД", "2026-08-03", "1 000,00", ""},
	}, shuffled)
	if err != nil {
		t.Fatal(err)
	}
	if len(straight.Rows) != 1 || len(other.Rows) != 1 {
		t.Fatalf("обидва розбори мали дати рядок: %+v / %+v", straight, other)
	}
	if straight.Rows[0] != other.Rows[0] {
		t.Errorf("перестановка колонок змінила результат: %+v проти %+v",
			straight.Rows[0], other.Rows[0])
	}
}

// Невідома операція не зникає мовчки — вона стає пропуском із причиною.
// Тихо загублений рядок виписки — це розбіжність у балансі, яку потім
// шукатимеш руками.
func TestProfileSkipsUnknownOpWithReason(t *testing.T) {
	rows := [][]string{head(),
		{"2026-08-06", "Комісія за зберігання", "", "", "", "12,00"},
	}
	res, err := Parse(rows, mono())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Errorf("невідома операція не мала стати рядком: %+v", res.Rows)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason != "невідомий тип операції" {
		t.Errorf("очікували пропуск із причиною, маємо %+v", res.Skipped)
	}
}

// Виписка від новішого до старішого перевертається: собівартість позиції
// рахується послідовно, і зворотний порядок дав би інший результат.
func TestProfileReversesNewestFirstStatement(t *testing.T) {
	rows := [][]string{head(),
		{"2026-08-05", "Виведення", "", "", "", "500,00"},
		{"2026-08-01", "Поповнення", "", "", "10 000,00", ""},
	}
	res, err := Parse(rows, mono())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("рядків %d, очікували 2", len(res.Rows))
	}
	if res.Rows[0].Date != "2026-08-01" {
		t.Errorf("порядок не перевернувся: перший рядок %s", res.Rows[0].Date)
	}
}

func TestParseOpsRejectsUnknownKind(t *testing.T) {
	if _, err := ParseOps("Поповнення = попка"); err == nil {
		t.Error("невідомий вид мав дати помилку")
	}
	if _, err := ParseOps(""); err == nil {
		t.Error("порожній словник мав дати помилку: без нього жоден рядок не впізнати")
	}
	got, err := ParseOps("# коментар\n\nПоповнення = deposit\n  Виведення  =  withdrawal ")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["поповнення"] != "deposit" || got["виведення"] != "withdrawal" {
		t.Errorf("словник розібрався не так: %+v", got)
	}
}

// Профіль без колонки суми не зберігається й не працює: рядок виписки без
// грошей — не операція.
func TestParseRefusesProfileWithoutRequiredColumns(t *testing.T) {
	bad := mono()
	bad.Date = -1
	if _, err := Parse([][]string{head()}, bad); err == nil {
		t.Error("профіль без колонки дати мав дати помилку")
	}
	empty := mono()
	empty.Kinds = nil
	if _, err := Parse([][]string{head()}, empty); err == nil {
		t.Error("профіль без словника операцій мав дати помилку")
	}
}
