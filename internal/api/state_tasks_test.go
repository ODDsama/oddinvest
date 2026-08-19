package api

import (
	"testing"

	"github.com/ODDsama/oddinvest/internal/state"
)

// Порядок черги — за ЧАСОМ, далі за типом задачі. Найважливіше, що тут
// перевіряється: sev упорядковується ЗМІСТОМ (now → soon → watch), а не
// алфавітом, за яким вийшло б now → soon → watch тільки випадково, і
// перший же перейменований ступінь усе б перевернув.
func TestSortTasksByTimeThenRank(t *testing.T) {
	in := []state.Task{
		{ID: "c", Sev: sevWatch, Rank: 10},
		{ID: "b", Sev: sevSoon, Rank: 20},
		{ID: "a2", Sev: sevNow, Rank: 20},
		{ID: "a1", Sev: sevNow, Rank: 10},
	}
	sortTasks(in)
	got := ""
	for _, t := range in {
		got += t.ID + " "
	}
	if got != "a1 a2 b c " {
		t.Errorf("порядок = %q, треба \"a1 a2 b c \"", got)
	}
}

// Порожній портфель витісняє все інше: доки нічого не куплено, решта черги
// або порожня за побудовою, або радить те, чого ще не існує.
func TestBuildTasksEmptyPortfolio(t *testing.T) {
	got := buildTasks(&state.Doc{}, nil, &sources{}, "2026-08-19")
	if len(got) != 1 || got[0].ID != "start" {
		t.Fatalf("порожній портфель дав %d задач: %+v", len(got), got)
	}
	if got[0].Action == "" {
		t.Error("першій задачі потрібна дія — інакше почати нема з чого")
	}
}

// Резерв стоїть ПЕРЕД покупкою, і це не смак: гроші, які підуть у резерв,
// не мають брати участі в покупці — на це вже спирається сам помічник.
func TestBuildTasksReserveBeforeBuy(t *testing.T) {
	doc := &state.Doc{
		NominalUAHEq: 50_000,
		Reserve:      &state.Reserve{FillNowUAH: 3_000, GapUAH: 20_000, TargetUAH: 60_000, TargetMonths: 6},
	}
	sug := []suggestion{{
		Kind: "bond", ISIN: "UA4000228811", Label: "UA4000228811", Currency: "UAH",
		RealPct: 12.5, CanBuy: true, CostPerBond: moneyJSON{Amount: "1000.00", Currency: "UAH"},
	}}
	got := buildTasks(doc, sug, &sources{}, "2026-08-19")
	if len(got) < 2 {
		t.Fatalf("очікували щонайменше дві задачі, отримали %d", len(got))
	}
	if got[0].ID != "reserve-fill" || got[1].ID != "buy-best" {
		t.Errorf("порядок = %s, %s; треба reserve-fill, buy-best", got[0].ID, got[1].ID)
	}
	if got[1].Kind != "bond" {
		t.Errorf("задача покупки мусить нести вид — сторінка інструмента фільтрує саме по ньому")
	}
}

// Порада, якої не по кишені, не стає задачею «купи» — вона стає задачею
// «ще збираєш», і та мусить сказати, СКІЛЬКИ бракує.
func TestBuildTasksSavingWhenNothingAffordable(t *testing.T) {
	doc := &state.Doc{
		NominalUAHEq: 50_000,
		Brokers:      map[string]map[string]float64{"mono": {"UAH": 400}},
	}
	sug := []suggestion{{
		Kind: "bond", Label: "UA4000228811", Currency: "UAH", RealPct: 12.5,
		CanBuy: false, CostPerBond: moneyJSON{Amount: "1000.00", Currency: "UAH"},
	}}
	got := buildTasks(doc, sug, &sources{}, "2026-08-19")
	var saving *state.Task
	for i := range got {
		if got[i].ID == "saving" {
			saving = &got[i]
		}
		if got[i].ID == "buy-best" {
			t.Fatal("непозволена покупка потрапила в чергу як «можеш купити»")
		}
	}
	if saving == nil {
		t.Fatal("задачі «ще збираєш» немає")
	}
	if saving.AmountUAH != 600 {
		t.Errorf("бракує = %v, треба 600 (1000 ціна − 400 на рахунку)", saving.AmountUAH)
	}
}

// Гроші у прозі — українською: нерозривні групи по три й кома в копійках.
// Тест саме на це, бо рядок їде і в браузер, і в Home Assistant, і
// розійтися вони не мають.
func TestUAHFormat(t *testing.T) {
	// Escape-послідовністю, а не самим символом: нерозривний пробіл від
	// звичайного в редакторі не відрізнити, і тест, який «просто виглядає
	// правильно», перестав би ловити саме те, заради чого існує — щоб число
	// не рвалось на межі рядка ні у вебі, ні в Home Assistant.
	const nb = "\u00a0"
	cases := map[float64]string{
		0:          "0,00" + nb + "₴",
		1234.5:     "1" + nb + "234,50" + nb + "₴",
		1000000:    "1" + nb + "000" + nb + "000,00" + nb + "₴",
		-42.75:     "−42,75" + nb + "₴",
		999.999:    "1" + nb + "000,00" + nb + "₴", // округлення копійок не має давати «999,100»
		100528.094: "100" + nb + "528,09" + nb + "₴",
	}
	for in, want := range cases {
		if got := uah(in); got != want {
			t.Errorf("uah(%v) = %q, треба %q", in, got, want)
		}
	}
	if got, want := cur(1234.5, "USD"), "1"+nb+"234,50"+nb+"$"; got != want {
		t.Errorf("cur(USD) = %q, треба %q", got, want)
	}
}
