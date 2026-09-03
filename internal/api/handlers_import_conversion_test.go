package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
)

func mustJSON(t *testing.T, body string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("розбір: %v (%s)", err, body)
	}
}

// Звичайна виписка без конвертацій: облігація, сертифікати, дивіденд із
// податком окремим рядком, поповнення. Рівно те, що приходить щомісяця.
func statementXLSX() [][]string {
	return [][]string{
		{"Дата", "Тип операції", "Вид цінного паперу", "Дебет", "Кредит"},
		{"46224.65980324074", "Купівля 5 сертифікатів", "Inzhur REIT", "", "55.56"},
		{"46223.376238425924", "Купівля 1 облігації", "ОВДП UA4000237416", "", "1032.46"},
		{"46221.975694444445", "Поповнення брокерського рахунку", "-", "300", ""},
		{"46213.00001157408", "Сплата податку", "Inzhur REIT", "", "2.66"},
		{"46213", "Нарахування дивідендів", "Inzhur REIT", "18.99", ""},
	}
}

// postXLSX надсилає книгу так само, як це робить форма імпорту.
func postXLSX(t *testing.T, url string, rows [][]string) (*http.Response, string) {
	t.Helper()
	body, ctype := multipartFile(t, "file", "statement.xlsx", buildXLSX(t, rows))
	req, _ := http.NewRequest("POST", url, body)
	req.Header.Set("Content-Type", ctype)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 1<<16)
	tmp := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if rerr != nil {
			break
		}
	}
	return resp, string(buf)
}

// Виписка з конвертацією, зведена до суті з бойового файлу: Житній
// набирали двома купівлями, у вересні 2025-го він перетворився на REIT, а
// за тиждень REIT докупили. Порядок рядків — як у виписці, від новішого.
func conversionXLSX() [][]string {
	return [][]string{
		{"Дата", "Тип операції", "Вид цінного паперу", "Дебет", "Кредит"},
		{"45910.40642361111", "Купівля 44 сертифікатів", "Inzhur REIT", "", "444.40"},
		{"45903.000023148146", "Доплата", "Inzhur REIT", "", "6.95"},
		{"45903.00001157408", "Конвертація", "Inzhur REIT", "", "9193.05"},
		{"45903", "Конвертація", "Inzhur Житній", "9193.05", ""},
		{"45847.60125", "Купівля 1 сертифікату", "Inzhur Житній", "", "1018.96"},
		{"45567.427569444444", "Купівля 8 сертифікатів", "Inzhur Житній", "", "8040.96"},
	}
}

func fundIDByName(t *testing.T, srv string, name string) int64 {
	t.Helper()
	var funds []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	_, body := do(t, "GET", srv+"/api/fund-catalog", "")
	mustJSON(t, body, &funds)
	for _, f := range funds {
		if f.Name == name {
			return f.ID
		}
	}
	t.Fatalf("фонду %q немає в довіднику: %s", name, body)
	return 0
}

// Наскрізний шлях конвертації, і саме той, яким піде власник.
//
// Кількості у виписці немає ні для джерела, ні для призначення. Джерело
// бере її з позиції (конвертація забирає фонд цілком), призначення — з
// позначки ціни. Позначки спершу немає, і рядок пропускається з
// підказкою; людина заводить ціну, імпортує ще раз — і рядок заходить.
func TestImportConversionUsesPositionAndPriceMark(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)

	importSince(t, st, "2024-01-01")
	_, body := postXLSX(t, srv.URL+"/api/import", conversionXLSX())
	first := parseImportOut(t, body)
	if first.Imported != 3 {
		t.Fatalf("мали зайти три купівлі, зайшло %d: %s", first.Imported, body)
	}
	// Пропуск мусить КАЗАТИ, що зробити, а не лише що не вийшло.
	var told bool
	for _, s := range first.Skipped {
		if strings.Contains(s.Reason, "позначку ціни") {
			told = true
		}
	}
	if !told {
		t.Fatalf("пропуск не підказав про позначку ціни: %+v", first.Skipped)
	}

	// Ціна сертифіката REIT на день конвертації — та сама десятка, з якої
	// фонд починався. Саме вона й перетворює 9200.00 на 920 сертифікатів.
	id := fundIDByName(t, srv.URL, "Inzhur REIT")
	if _, err := st.AddFundPricePoints(ctx, id, []domain.FundPrice{
		{Date: "2025-09-03", Price: 100000},
	}); err != nil {
		t.Fatal(err)
	}

	importSince(t, st, "2024-01-01")
	_, body = postXLSX(t, srv.URL+"/api/import", conversionXLSX())
	second := parseImportOut(t, body)
	if second.Imported != 2 {
		t.Fatalf("мала зайти пара конвертації, зайшло %d: %s", second.Imported, body)
	}

	ops, err := st.ListFundOps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sell, buy *domain.FundOp
	for i := range ops {
		op := &ops[i]
		if op.Date != "2025-09-03" {
			continue
		}
		if op.Kind == domain.FundSell {
			sell = op
		}
		if op.Kind == domain.FundBuy {
			buy = op
		}
	}
	if sell == nil || buy == nil {
		t.Fatalf("пари конвертації немає: %+v", ops)
	}
	if sell.Fund != "Inzhur Житній" || sell.Qty != 9 {
		t.Errorf("продано %d сертифікатів %s, хочемо 9 Inzhur Житній", sell.Qty, sell.Fund)
	}
	if buy.Fund != "Inzhur REIT" || buy.Qty != 920 {
		t.Errorf("куплено %d сертифікатів %s, хочемо 920 Inzhur REIT", buy.Qty, buy.Fund)
	}
	if buy.Amount != 920000 {
		t.Errorf("сума купівлі %d — доплата 6.95 не долилась", buy.Amount)
	}
	if sell.PairID != buy.ID || buy.PairID != sell.ID {
		t.Errorf("ноги не зв'язані: продаж %d->%d, купівля %d->%d",
			sell.ID, sell.PairID, buy.ID, buy.PairID)
	}
	// І головне, заради чого пара: позиція Житнього порожня, а REIT не
	// має розбіжності — сертифікати перейшли, а не зникли.
	pos := domain.FundPositions(ops, nil)
	if p := pos["Inzhur Житній"]; p == nil || p.Qty != 0 {
		t.Errorf("Житній після конвертації: %+v", p)
	}
	if p := pos["Inzhur REIT"]; p == nil || p.Qty != 964 || p.Short != 0 {
		t.Errorf("REIT після конвертації: %+v", p)
	}
}

// ГОЛОВНИЙ тест напряму: власник імпортує виписку кілька разів на місяць,
// і вона щоразу несе всю історію.
//
// Кількості обох ніг ВИВЕДЕНІ, а не прочитані: після першого імпорту
// позиція джерела вже нульова. Якби кількість входила в ключ тотожності,
// другий прогін не впізнав би свою ж роботу й поклав другий, порожній
// продаж. Тому ключ ноги конвертації будується на сумі.
func TestImportConversionTwiceDoesNotDouble(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)

	importSince(t, st, "2024-01-01")
	postXLSX(t, srv.URL+"/api/import", conversionXLSX())
	id := fundIDByName(t, srv.URL, "Inzhur REIT")
	if _, err := st.AddFundPricePoints(ctx, id, []domain.FundPrice{
		{Date: "2025-09-03", Price: 100000},
	}); err != nil {
		t.Fatal(err)
	}
	importSince(t, st, "2024-01-01")
	postXLSX(t, srv.URL+"/api/import", conversionXLSX())

	before, err := st.ListFundOps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	importSince(t, st, "2024-01-01")
	_, body := postXLSX(t, srv.URL+"/api/import", conversionXLSX())
	if got := parseImportOut(t, body); got.Imported != 0 {
		t.Errorf("третій прогін записав %d рядків: %s", got.Imported, body)
	}
	after, err := st.ListFundOps(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("операцій було %d, стало %d — конвертація задвоїлась", len(before), len(after))
	}
	n := 0
	for _, op := range after {
		if op.Date == "2025-09-03" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("на день конвертації %d операцій, а має бути рівно дві", n)
	}
}

// Та сама обіцянка для звичайної виписки, без конвертацій: другий прогін
// не додає нічого — ні дивідендів, ні лотів, ні поповнень.
func TestImportTwiceIsIdempotent(t *testing.T) {
	ctx := context.Background()
	srv, st := testServer(t)

	importSince(t, st, "2024-01-01")
	_, body := postXLSX(t, srv.URL+"/api/import", statementXLSX())
	first := parseImportOut(t, body)
	if first.Imported == 0 {
		t.Fatalf("перший прогін нічого не записав: %s", body)
	}

	ops, _ := st.ListFundOps(ctx)
	lots, _ := st.ListLots(ctx)
	deps, _ := st.ListDeposits(ctx)

	importSince(t, st, "2024-01-01")
	_, body = postXLSX(t, srv.URL+"/api/import", statementXLSX())
	if got := parseImportOut(t, body); got.Imported != 0 || got.New != 0 {
		t.Errorf("другий прогін: нових %d, записано %d: %s", got.New, got.Imported, body)
	}
	ops2, _ := st.ListFundOps(ctx)
	lots2, _ := st.ListLots(ctx)
	deps2, _ := st.ListDeposits(ctx)
	if len(ops2) != len(ops) || len(lots2) != len(lots) || len(deps2) != len(deps) {
		t.Errorf("журнали виросли: операції %d→%d, лоти %d→%d, рухи %d→%d",
			len(ops), len(ops2), len(lots), len(lots2), len(deps), len(deps2))
	}
}

// Перегляд мусить обіцяти те, що зробить імпорт.
//
// Однакові рядки одного файлу зводяться в один запис — так само, як це
// вже роблять лоти й поповнення. Доти операції фондів питали про дубль
// БАЗУ, а не множину: у сухому прогоні база ще порожня, тож перегляд
// обіцяв дві операції, а справжній імпорт писав одну, і різницю ніхто не
// пояснював.
func TestImportPreviewMatchesImportOnTwins(t *testing.T) {
	srv, st := testServer(t)
	twins := [][]string{
		{"Дата", "Тип операції", "Вид цінного паперу", "Дебет", "Кредит"},
		{"46224.65980324074", "Купівля 5 сертифікатів", "Inzhur REIT", "", "55.56"},
		{"46224.65980324074", "Купівля 5 сертифікатів", "Inzhur REIT", "", "55.56"},
	}
	importSince(t, st, "2024-01-01")
	_, body := postXLSX(t, srv.URL+"/api/import?dry=1", twins)
	dry := parseImportOut(t, body)

	importSince(t, st, "2024-01-01")
	_, body = postXLSX(t, srv.URL+"/api/import", twins)
	real := parseImportOut(t, body)

	if dry.New != real.Imported {
		t.Errorf("перегляд обіцяв %d нових, імпорт записав %d", dry.New, real.Imported)
	}
}

// Водяний знак мусить називати ПЕРІОД, який він приховав, а не лише
// кількість рядків. Саме через мовчазне «117 рядків не розглядались» два з
// половиною роки дивідендів не потрапляли в базу при кожному імпорті.
func TestImportReportsHiddenRange(t *testing.T) {
	srv, st := testServer(t)
	importSince(t, st, "2026-01-01")

	_, body := postXLSX(t, srv.URL+"/api/import?dry=1", conversionXLSX())
	var out struct {
		Before     int    `json:"before"`
		BeforeFrom string `json:"before_from"`
		BeforeTo   string `json:"before_to"`
	}
	mustJSON(t, body, &out)
	if out.Before == 0 {
		t.Fatalf("рядки 2024-2025 мали відсіятись: %s", body)
	}
	if out.BeforeFrom != "2024-10-02" || out.BeforeTo != "2025-09-10" {
		t.Errorf("прихований період %s → %s, хочемо 2024-10-02 → 2025-09-10",
			out.BeforeFrom, out.BeforeTo)
	}
}
