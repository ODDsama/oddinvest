package api

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
)

// excelSerial — дата як серійний номер Excel (полудень), так її пише
// виписка. Тесту купона потрібна САМЕ сьогоднішня дата: виплату з
// минулою датою Arrived і так вважає прийшлою, і позначка там нічого б
// не перевірила.
func excelSerial(d domain.Date) string {
	t, _ := time.Parse("2006-01-02", string(d)) //nolint:errcheck // дата з domain.NewDate завжди валідна
	days := t.Sub(time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)).Hours() / 24
	return strconv.FormatFloat(days+0.5, 'f', -1, 64)
}

// Купон у виписці — позначка «Отримано» на виплаті графіка, а не новий
// грошовий запис: суму й так рахує графік на лоти.
func TestImportCouponMarksPaymentReceived(t *testing.T) {
	srv, st := testServer(t)
	importSince(t, st, "2020-01-01")
	ctx := context.Background()

	const isin = "UA4000238976"
	today := domain.NewDate(time.Now())
	past := today.AddDays(-182)
	if err := st.ReplaceDirectory(ctx, []nbu.Security{{
		Bond: domain.Bond{ISIN: isin, Nominal: money.New(100000, money.UAH),
			RateBP: 1568, Maturity: today.AddDays(365), Descr: "тест"},
		Payments: []domain.Payment{
			{ISIN: isin, PayDate: past, Type: domain.PayCoupon, PerBond: money.New(7840, money.UAH)},
			{ISIN: isin, PayDate: today, Type: domain.PayCoupon, PerBond: money.New(7840, money.UAH)},
		},
	}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddLot(ctx, domain.Lot{ISIN: isin, Qty: 1, PricePerBond: money.New(105516, money.UAH),
		BuyDate: today.AddDays(-200)}); err != nil {
		t.Fatal(err)
	}

	type result struct {
		New      int `json:"new"`
		Imported int `json:"imported"`
		Rows     []struct {
			Kind     string `json:"kind"`
			Fund     string `json:"fund"`
			Date     string `json:"date"`
			Exists   bool   `json:"exists"`
			Conflict string `json:"conflict"`
		} `json:"rows"`
		Skipped []struct {
			Reason string `json:"reason"`
		} `json:"skipped"`
	}
	post := func(dry bool, rows ...[]string) result {
		t.Helper()
		url := srv.URL + "/api/import/inzhur"
		if dry {
			url += "?dry=1"
		}
		sheet := append([][]string{{"Дата", "Тип операції", "Вид цінного паперу", "Дебет", "Кредит"}}, rows...)
		resp, body := postXLSX(t, url, sheet)
		if resp.StatusCode != 200 {
			t.Fatalf("імпорт: %d %s", resp.StatusCode, body)
		}
		var out result
		mustJSON(t, body, &out)
		return out
	}
	received := func() bool {
		t.Helper()
		m, err := st.PaymentStatuses(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return m[isin+"|"+string(today)] == domain.StatusReceived
	}
	coupon := func(d domain.Date, amount string) []string {
		return []string{excelSerial(d), "Нарахування купону", "ОВДП " + isin, amount, ""}
	}

	// Перегляд: сьогоднішній купон новий, минулий уже зарахований графіком.
	pre := post(true, coupon(today, "78.4"), coupon(past, "78.4"))
	if pre.New != 1 || len(pre.Rows) != 2 {
		t.Fatalf("перегляд: %+v", pre)
	}
	for _, r := range pre.Rows {
		if r.Kind != "coupon" || r.Fund != isin || r.Conflict != "" {
			t.Errorf("рядок купона: %+v", r)
		}
		if r.Exists != (r.Date != string(today)) {
			t.Errorf("«вже є» лише для минулого купона: %+v", r)
		}
	}
	if received() {
		t.Fatal("перегляд не мав ставити позначку")
	}

	if got := post(false, coupon(today, "78.4")); got.Imported != 1 || !received() {
		t.Fatalf("імпорт мав позначити виплату отриманою: %+v", got)
	}
	// Справжній імпорт пересунув водяний знак на сьогодні; повертаємо, щоб
	// рядки з минулими датами нижче взагалі розглядались.
	importSince(t, st, "2020-01-01")
	if again := post(true, coupon(today, "78.4")); again.New != 0 || !again.Rows[0].Exists {
		t.Errorf("повтор мав бути «вже є»: %+v", again)
	}

	// Сума не та, що графік дає на лоти, — рядок лишається, але з приміткою.
	if bad := post(true, coupon(today, "156.8")); len(bad.Rows) != 1 || bad.Rows[0].Conflict == "" {
		t.Errorf("розбіжність із графіком мала бути названа: %+v", bad)
	}
	// Дата, біля якої в графіку купона немає, — пропуск із причиною.
	if lost := post(true, coupon(today.AddDays(-60), "78.4")); len(lost.Rows) != 0 ||
		len(lost.Skipped) != 1 || !strings.Contains(lost.Skipped[0].Reason, "графіку") {
		t.Errorf("купон без виплати в графіку мав піти в пропуски: %+v", lost)
	}
}
