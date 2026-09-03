package imports

import "testing"

// Рядки взято з реальної виписки Inzhur, включно з нерозривними пробілами
// в «1 782» і з тим, що податок стоїть окремим рядком після своєї події.
func statement() [][]string {
	return [][]string{
		{"Дата", "Тип операції", "Вид цінного паперу", "Дебет", "Кредит"},
		{"46224.65980324074", "Купівля 5 сертифікатів", "Inzhur REIT", "", "55.56"},
		{"46223.376238425924", "Купівля 1 облігації", "ОВДП UA4000237416", "", "1032.46"},
		{"46223.36895833333", "Сплата податку з продажу сертифікатів", "Inzhur REIT", "", "1.78"},
		{"46223.368946759256", "Продаж 72 сертифікатів", "Inzhur REIT", "798.3"},
		{"46221.975694444445", "Поповнення брокерського рахунку", "-", "300"},
		{"46213.00001157408", "Сплата податку", "Inzhur REIT", "", "2.66"},
		{"46213", "Нарахування дивідендів", "Inzhur REIT", "18.99"},
		{"45908.5", "Конвертація", "Inzhur REIT", "9193.05"},
		{"45900.5", "Виведення з брокерського рахунку", "-", "", "500"},
		{"45899.5", "Купівля 1 782 сертифікатів", "Inzhur REIT", "", "17986.80"},
	}
}

func find(t *testing.T, res Result, kind string) Row {
	t.Helper()
	for _, r := range res.Rows {
		if r.Kind == kind {
			return r
		}
	}
	t.Fatalf("не знайшов операцію %s", kind)
	return Row{}
}

func TestParseInzhur(t *testing.T) {
	res, err := ParseInzhur(statement())
	if err != nil {
		t.Fatal(err)
	}

	byQty := map[int64]Row{}
	for _, r := range res.Rows {
		if r.Kind == "fund_buy" {
			byQty[r.Qty] = r
		}
	}
	if b := byQty[5]; b.Date != "2026-07-21" || b.Amount != 5556 {
		t.Errorf("купівля 5 сертифікатів: %+v", b)
	}
	// нерозривний пробіл у кількості не має ламати розбір
	if b := byQty[1782]; b.Amount != 1798680 {
		t.Errorf("кількість із нерозривним пробілом: %+v", b)
	}

	// Операції мають повертатись У ХРОНОЛОГІЧНОМУ порядку: виписка йде
	// від новішого до старішого, а собівартість позиції рахується
	// послідовно, тож зворотний порядок дав би інший результат.
	for i := 1; i < len(res.Rows); i++ {
		if res.Rows[i-1].Date > res.Rows[i].Date {
			t.Errorf("порядок не хронологічний: %s після %s",
				res.Rows[i].Date, res.Rows[i-1].Date)
			break
		}
	}

	// Податок стоїть ОКРЕМИМ рядком — він має підтягнутись до своєї
	// події, інакше дохідність рахувалася б до податку.
	div := find(t, res, "dividend")
	if div.Amount != 1899 || div.Tax != 266 {
		t.Errorf("дивіденд мав отримати свій податок 2.66: %+v", div)
	}
	sell := find(t, res, "fund_sell")
	if sell.Amount != 79830 || sell.Tax != 178 {
		t.Errorf("продаж мав отримати податок 1.78: %+v", sell)
	}

	// Облігація тепер теж імпортується: ISIN береться з назви паперу,
	// а ціна за штуку виводиться із суми й кількості.
	bond := find(t, res, "bond_buy")
	if bond.Fund != "UA4000237416" || bond.Qty != 1 || bond.Amount != 103246 {
		t.Errorf("облігація: %+v", bond)
	}

	dep := find(t, res, "deposit")
	if dep.Amount != 30000 {
		t.Errorf("поповнення 300.00: %+v", dep)
	}
	wd := find(t, res, "withdrawal")
	if wd.Amount != 50000 {
		t.Errorf("виведення 500.00: %+v", wd)
	}

	// Пропуски мають бути НАЗВАНІ, а не проковтнуті.
	reasons := map[string]bool{}
	for _, s := range res.Skipped {
		reasons[s.Reason] = true
	}
	for _, want := range []string{"конвертація без другої ноги — вона поза цим файлом"} {
		if !reasons[want] {
			t.Errorf("очікували пропуск з причиною %q, маємо %+v", want, res.Skipped)
		}
	}
	if len(res.Rows)+len(res.Skipped) != len(statement())-1-2 { //nolint
		// −2, бо два рядки податку не стають окремими операціями
		t.Errorf("жоден рядок не має зникнути безслідно: %d операцій + %d пропусків",
			len(res.Rows), len(res.Skipped))
	}
}

// Податок без своєї події не можна тихо викидати: це або обрізаний файл,
// або помилка розбору, і про обидва треба сказати.
func TestOrphanTaxIsReported(t *testing.T) {
	res, err := ParseInzhur([][]string{
		{"Дата", "Тип операції", "Вид цінного паперу", "Дебет", "Кредит"},
		{"46213.5", "Сплата податку", "Inzhur REIT", "", "5.00"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Errorf("самотній податок не мав стати операцією: %+v", res.Rows)
	}
	if len(res.Skipped) != 1 || res.Skipped[0].Reason == "" {
		t.Errorf("самотній податок мав потрапити в пропуски: %+v", res.Skipped)
	}
}

func TestExcelDate(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"46224.65980324074", "2026-07-21"},
		{"46213", "2026-07-10"},
		{"2026-07-10", "2026-07-10"}, // звичайна дата теж має проходити
	} {
		got, err := excelDate(c.in)
		if err != nil || string(got) != c.want {
			t.Errorf("excelDate(%q) = %q, %v; очікували %q", c.in, got, err, c.want)
		}
	}
	if _, err := excelDate("не дата"); err == nil {
		t.Error("сміття мало дати помилку")
	}
}

func TestMoneyParsing(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int64
	}{
		{"55.56", 5556}, {"1 234,56", 123456}, {"17986.80", 1798680},
		{"", 0}, {"—", 0},
	} {
		if got := money(c.in); got != c.want {
			t.Errorf("money(%q) = %d, очікували %d", c.in, got, c.want)
		}
	}
}

// Конвертація — одна подія двома рядками, і розбір мусить бачити її саме
// так. Рядки взято з виписки: Житній віддав 9193.05 (Дебет), REIT забрав
// ту саму суму (Кредит), і згори лягла доплата 6.95.
func conversionStatement() [][]string {
	return [][]string{
		{"Дата", "Тип операції", "Вид цінного паперу", "Дебет", "Кредит"},
		{"45903.000023148146", "Доплата", "Inzhur REIT", "", "6.95"},
		{"45903.00001157408", "Конвертація", "Inzhur REIT", "", "9193.05"},
		{"45903", "Конвертація", "Inzhur Житній", "9193.05", ""},
	}
}

func TestInzhurPairsConversion(t *testing.T) {
	res, err := ParseInzhur(conversionStatement())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("нічого не мало пропаснути: %+v", res.Skipped)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("конвертація — рівно дві ноги, маємо %d: %+v", len(res.Rows), res.Rows)
	}
	sell, buy := find(t, res, "fund_sell"), find(t, res, "fund_buy")
	if sell.Fund != "Inzhur Житній" || buy.Fund != "Inzhur REIT" {
		t.Errorf("напрямок переплутано: продано %q, куплено %q", sell.Fund, buy.Fund)
	}
	if sell.Pair == 0 || sell.Pair != buy.Pair {
		t.Errorf("ноги не зв'язані: продаж %d, купівля %d", sell.Pair, buy.Pair)
	}
	// Кількості тут БУТИ НЕ МОЖЕ: виписка її не несе. Проставляє обробник.
	if sell.Qty != 0 || buy.Qty != 0 {
		t.Errorf("кількість вигадано з нічого: продаж %d, купівля %d", sell.Qty, buy.Qty)
	}
}

// Доплата доливається в суму КУПІВЛІ. Без неї 9193.05 замість 9200.00 дасть
// 919 сертифікатів замість 920, і далі розійдеться вся позиція.
func TestInzhurFoldsSurchargeIntoConversion(t *testing.T) {
	res, err := ParseInzhur(conversionStatement())
	if err != nil {
		t.Fatal(err)
	}
	buy := find(t, res, "fund_buy")
	if buy.Amount != 920000 {
		t.Errorf("сума купівлі = %d, хочемо 920000 (9193.05 + 6.95)", buy.Amount)
	}
	sell := find(t, res, "fund_sell")
	if sell.Amount != 919305 {
		t.Errorf("доплата не мала чіпати ногу продажу: %d", sell.Amount)
	}
}

// Доплата без своєї конвертації — не привід тихо її ковтнути.
func TestInzhurLoneSurchargeSkipped(t *testing.T) {
	res, err := ParseInzhur([][]string{
		{"Дата", "Тип операції", "Вид цінного паперу", "Дебет", "Кредит"},
		{"45903.000023148146", "Доплата", "Inzhur REIT", "", "6.95"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Errorf("доплата сама по собі не операція: %+v", res.Rows)
	}
	if len(res.Skipped) != 1 {
		t.Fatalf("пропуск мав бути названий: %+v", res.Skipped)
	}
}

// Дві конвертації в один файл, різні суми й дати — кожна знаходить СВОЮ
// половину, а не сусідню.
func TestInzhurPairsTwoConversionsIndependently(t *testing.T) {
	res, err := ParseInzhur([][]string{
		{"Дата", "Тип операції", "Вид цінного паперу", "Дебет", "Кредит"},
		{"45904.000023148146", "Доплата", "Inzhur REIT", "", "2.44"},
		{"45904.00001157408", "Конвертація", "Inzhur REIT", "", "8177.56"},
		{"45904", "Конвертація", "Inzhur Ocean", "8177.56", ""},
		{"45903.000023148146", "Доплата", "Inzhur REIT", "", "6.95"},
		{"45903.00001157408", "Конвертація", "Inzhur REIT", "", "9193.05"},
		{"45903", "Конвертація", "Inzhur Житній", "9193.05", ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("нічого не мало пропаснути: %+v", res.Skipped)
	}
	if len(res.Rows) != 4 {
		t.Fatalf("дві конвертації — чотири ноги, маємо %d", len(res.Rows))
	}
	pairs := map[int][]Row{}
	for _, r := range res.Rows {
		if r.Pair == 0 {
			t.Fatalf("нога без пари: %+v", r)
		}
		pairs[r.Pair] = append(pairs[r.Pair], r)
	}
	if len(pairs) != 2 {
		t.Fatalf("пар мало бути дві, маємо %d", len(pairs))
	}
	// Доплата кожної пари мала лягти на СВОЮ купівлю: 9200.00 і 8180.00.
	sums := map[int64]bool{}
	for _, r := range res.Rows {
		if r.Kind == "fund_buy" {
			sums[r.Amount] = true
		}
	}
	if !sums[920000] || !sums[818000] {
		t.Errorf("доплати розійшлись не по своїх конвертаціях: %+v", res.Rows)
	}
}
