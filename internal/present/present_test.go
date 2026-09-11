package present

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
)

// stubRates — курс по датах; сьогодні — 40, точки лише на названі дні.
type stubRates map[domain.Date]float64

func (r stubRates) At(on domain.Date) (float64, bool) {
	var best domain.Date
	for d := range r {
		if d <= on && d > best {
			best = d
		}
	}
	if best == "" {
		return 0, false
	}
	return r[best], true
}

const today = domain.Date("2026-09-10")

func rates() stubRates {
	return stubRates{"2026-01-01": 20, "2026-06-01": 25, today: 40}
}

type row struct {
	Date string      `json:"date" money:"asof"`
	Sum  state.Money `json:"sum"`
}

type inner struct {
	Own   state.Money `json:"own"`
	Delta state.Money `json:"delta" money:"diff=cap,own"` // cap — у предка
}

// plan — неіменоване вкладення приватного типу (routeLeg так вкладає
// allocPlan): json піднімає його поля до господаря, і презентер мусить у
// нього зайти, хоч поле й неекспортоване. Before дивиться на дату предка.
type plan struct {
	Amount state.Money `json:"amount"`
	Before state.Money `json:"before_leg" money:"asof=from_date"`
}

type leg struct {
	Label string `json:"label"`
	plan
}

type doc struct {
	Currency string                 `json:"currency" money:"code"`
	Cap      state.Money            `json:"cap"`
	Native   state.Money            `json:"native"`
	FromDate string                 `json:"from_date"`
	ToDate   string                 `json:"to_date"`
	Before   state.Money            `json:"before" money:"asof=from_date"`
	After    state.Money            `json:"after" money:"asof=to_date"`
	Delta    state.Money            `json:"delta" money:"diff=after,before"`
	Nominal  float64                `json:"nominal_pct" money:"ruler=real_pct"`
	Real     float64                `json:"real_pct,omitempty"`
	Kind     map[string]float64     `json:"kind_pct" money:"ruler=kind_real_pct"`
	KindReal map[string]float64     `json:"kind_real_pct,omitempty"`
	CPI      *float64               `json:"cpi,omitempty" money:"uah-only"`
	Rows     []row                  `json:"rows"`
	ByCur    map[string]state.Money `json:"by_cur"`
	Any      map[string]any         `json:"any"`
	Days     []string               `json:"days"`
	Series   []state.Money          `json:"series" money:"asof=days"`
	Ptr      *state.Money           `json:"ptr"`
	Nested   *inner                 `json:"nested"`
	Pct      float64                `json:"pct"`
	Legs     []leg                  `json:"legs"`
}

func sample() *doc {
	cpi := 12.5
	return &doc{
		Cap:      state.UAH(400_000), // 4 000 ₴
		Native:   state.Minor(50_00, "USD"),
		FromDate: "2026-01-15", ToDate: "2026-06-15",
		Before:  state.UAH(200_000), // за курсом 20 → 100 $
		After:   state.UAH(250_000), // за курсом 25 → 100 $
		Delta:   state.UAH(50_000),
		Nominal: 15, Real: 8,
		Kind: map[string]float64{"bonds": 15}, KindReal: map[string]float64{"bonds": 8},
		CPI:    &cpi,
		Rows:   []row{{Date: "2026-01-20", Sum: state.UAH(2000)}, {Date: "2026-07-01", Sum: state.UAH(2500)}},
		ByCur:  map[string]state.Money{"UAH": state.UAH(4000), "USD": state.Minor(100, "USD")},
		Any:    map[string]any{"date": "2026-01-01", "x": state.UAH(4000), "pct": 1.5},
		Days:   []string{"2026-01-01", "2026-06-01", "2026-09-10"},
		Series: []state.Money{state.UAH(2000), state.UAH(2500), state.UAH(4000)},
		Ptr:    func() *state.Money { m := state.UAH(8000); return &m }(),
		Nested: &inner{Own: state.UAH(100_000)},
		Pct:    3.3,
		Legs:   []leg{{Label: "нога", plan: plan{Amount: state.UAH(4000), Before: state.UAH(2000)}}},
	}
}

func usd(minor int64) state.Money { return state.Minor(minor, "USD") }

// У книжковій валюті прохід — тотожність, лише code проставлено.
func TestIdentity(t *testing.T) {
	d := sample()
	want, _ := json.Marshal(d)
	if err := Apply(d, Opts{Book: "UAH", Report: "UAH", Rates: rates(), Today: today}); err != nil {
		t.Fatal(err)
	}
	if d.Currency != "UAH" {
		t.Errorf("code: %q", d.Currency)
	}
	d.Currency = ""
	got, _ := json.Marshal(d)
	if string(got) != string(want) {
		t.Errorf("тотожність порушена:\n%s\n%s", want, got)
	}
}

func TestConvert(t *testing.T) {
	d := sample()
	if err := Apply(d, Opts{Book: "UAH", Report: "USD", Rates: rates(), Today: today}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		got  state.Money
		want state.Money
	}{
		{"сьогоднішній курс", d.Cap, usd(10_000)},
		{"натуральне не чіпається", d.Native, usd(50_00)},
		{"before за from_date", d.Before, usd(10_000)},
		{"after за to_date", d.After, usd(10_000)},
		{"дельта — різниця перекладених, не дельта/курс", d.Delta, usd(0)},
		{"рядок за своєю датою (січень)", d.Rows[0].Sum, usd(100)},
		{"рядок за своєю датою (липень → червнева точка)", d.Rows[1].Sum, usd(100)},
		{"мапа: гривня", d.ByCur["UAH"], usd(100)},
		{"мапа: натуральне", d.ByCur["USD"], usd(100)},
		{"ряд поіндексно [0]", d.Series[0], usd(100)},
		{"ряд поіндексно [1]", d.Series[1], usd(100)},
		{"ряд поіндексно [2]", d.Series[2], usd(100)},
		{"вказівник", *d.Ptr, usd(200)},
		{"вкладене", d.Nested.Own, usd(2500)},
		{"дельта від предка", d.Nested.Delta, usd(7500)},
		{"вкладене без імені (приватний тип)", d.Legs[0].Amount, usd(100)},
		{"вкладене без імені: asof= бачить дату господаря", d.Legs[0].Before, usd(100)},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: %v, хочемо %v", c.name, c.got, c.want)
		}
	}
	if d.Currency != "USD" {
		t.Errorf("code: %q", d.Currency)
	}
	// Мапа з ключем "date" — рядок зі своєю датою: 4 000 ₴ за січневим
	// курсом 20 — це 200 $, а не 100 за сьогоднішнім.
	if got := d.Any["x"].(state.Money); got != usd(200) {
		t.Errorf("any у мапі за своєю датою: %v", got)
	}
	if d.Any["pct"] != 1.5 || d.Any["date"] != "2026-01-01" {
		t.Errorf("не-гроші в any зрушили: %v", d.Any)
	}
	// Лінійка одна: номінал бере реальну, реальна лишається рівною йому.
	if d.Nominal != 8 || d.Real != 8 {
		t.Errorf("лінійка: nominal %v, real %v", d.Nominal, d.Real)
	}
	if d.Kind["bonds"] != 8 || d.KindReal["bonds"] != 8 {
		t.Errorf("лінійка-мапа: %v / %v", d.Kind, d.KindReal)
	}
	if d.CPI != nil {
		t.Error("uah-only не обнулено")
	}
	if d.Pct != 3.3 {
		t.Error("відсоток зрушив")
	}
}

// Двічі на свіжих копіях — той самий результат: прохід детермінований.
func TestDeterministic(t *testing.T) {
	a, b := sample(), sample()
	o := Opts{Book: "UAH", Report: "USD", Rates: rates(), Today: today}
	if err := Apply(a, o); err != nil {
		t.Fatal(err)
	}
	if err := Apply(b, o); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Error("два проходи дали різне")
	}
}

func TestNoRateToday(t *testing.T) {
	d := sample()
	err := Apply(d, Opts{Book: "UAH", Report: "USD", Rates: stubRates{}, Today: today})
	if err == nil {
		t.Fatal("без сьогоднішнього курсу мала бути помилка")
	}
	if d.Cap != state.UAH(400_000) {
		t.Error("без курсу документ мусить лишитись у гривні")
	}
}

// Дата без точки в історії бере сьогоднішній курс, а не мовчить.
func TestOldDateFallsBackToToday(t *testing.T) {
	d := &struct {
		Rows []row `json:"rows"`
	}{Rows: []row{{Date: "2015-01-01", Sum: state.UAH(4000)}}}
	if err := Apply(d, Opts{Book: "UAH", Report: "USD", Rates: rates(), Today: today}); err != nil {
		t.Fatal(err)
	}
	if d.Rows[0].Sum != usd(100) {
		t.Errorf("стара дата: %v", d.Rows[0].Sum)
	}
}

// Посилання asof= знаходить дні в батьківській відповіді (суперники),
// місяць «YYYY-MM» читається як його кінець, а мапа з ключем "date" — як
// рядок зі своєю датою (знімки).
func TestAsOfAncestorMonthAndMap(t *testing.T) {
	type rowMonth struct {
		Month string      `json:"month" money:"asof"`
		Sum   state.Money `json:"sum"`
	}
	type rival struct {
		Points []state.Money `json:"points" money:"asof=days"`
	}
	type resp struct {
		Days   []string           `json:"days"`
		Rivals []rival            `json:"rivals"`
		Months []rowMonth         `json:"months"`
		Snaps  []map[string]any   `json:"snaps"`
		ByCur  map[string]float64 `json:"by_cur"`
	}
	d := &resp{
		Days:   []string{"2026-01-01", "2026-09-10"},
		Rivals: []rival{{Points: []state.Money{state.UAH(2000), state.UAH(4000)}}},
		Months: []rowMonth{{Month: "2026-01", Sum: state.UAH(2000)}, {Month: "2026-09", Sum: state.UAH(4000)}},
		Snaps: []map[string]any{
			{"date": "2026-01-05", "cap": state.UAH(2000), "pct": 1.5},
			{"date": "2026-09-10", "cap": state.UAH(4000)},
		},
		ByCur: map[string]float64{"USD": 1},
	}
	if err := Apply(d, Opts{Book: "UAH", Report: "USD", Rates: rates(), Today: today}); err != nil {
		t.Fatal(err)
	}
	if d.Rivals[0].Points[0] != usd(100) || d.Rivals[0].Points[1] != usd(100) {
		t.Errorf("дні з предка: %v", d.Rivals[0].Points)
	}
	// Січень — кінець місяця → курс 20; вересень — кінець ще попереду →
	// останній відомий, тобто сьогоднішній 40.
	if d.Months[0].Sum != usd(100) || d.Months[1].Sum != usd(100) {
		t.Errorf("місяці: %v / %v", d.Months[0].Sum, d.Months[1].Sum)
	}
	if got := d.Snaps[0]["cap"].(state.Money); got != usd(100) {
		t.Errorf("знімок за своєю датою: %v", got)
	}
	if got := d.Snaps[1]["cap"].(state.Money); got != usd(100) {
		t.Errorf("знімок сьогодні: %v", got)
	}
	if d.Snaps[0]["pct"] != 1.5 || d.ByCur["USD"] != 1 {
		t.Error("не-гроші зрушили")
	}
}

func TestBadTags(t *testing.T) {
	type badRef struct {
		X state.Money `json:"x" money:"asof=nope"`
	}
	type badTag struct {
		X state.Money `json:"x" money:"whatever"`
	}
	type badDiff struct {
		A float64     `json:"a"`
		B state.Money `json:"b"`
		D state.Money `json:"d" money:"diff=a,b"`
	}
	o := Opts{Book: "UAH", Report: "USD", Rates: rates(), Today: today}
	if err := Apply(&badRef{}, o); err == nil {
		t.Error("невідомий сусід у asof= мав бути помилкою")
	}
	if err := Apply(&badTag{}, o); err == nil {
		t.Error("невідомий тег мав бути помилкою")
	}
	if err := Apply(&badDiff{}, o); err == nil {
		t.Error("diff не над Money мав бути помилкою")
	}
	if err := Apply(doc{}, o); err == nil {
		t.Error("не вказівник мав бути помилкою")
	}
}
