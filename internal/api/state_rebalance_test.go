package api

import (
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
)

func pct(v float64) *float64 { return &v }

// TestRebalanceFallsBackToDepositWhenBondTooBig — коли найдешевша
// облігація більша за всю цільову частку, одиницею входу стає ВКЛАД.
//
// Golden цього не бачить: у багатій фікстурі капітал такий, що доларовий
// папір на $1000 вільно вписується в 25% частки, тож гілка вкладу не
// виконується — мутація «прибрати запасний вклад» golden не завалила.
//
// Гілка потрібна саме на малих портфелях, тобто на самому початку, коли
// поради найважливіші. Доти картка казала «ще зарано» на $1000-й папір,
// хоча частку добирає й вклад на $100, і людина без потреби чекала.
func TestRebalanceFallsBackToDepositWhenBondTooBig(t *testing.T) {
	in := rebalanceInput{
		// Капітал 10 000 ₴ — доларова ціль 25% це лише 2 500 ₴.
		Capital:  state.Capital{AccountUAH: 10000},
		Settings: &state.SettingsDoc{USDTargetSharePct: pct(25)},
		Rates:    fx.Rates{money.USD: 441234},
		// Найдешевший папір — $1000 (≈44 123 ₴), тобто далеко за ціллю.
		MinNominalByCur: map[string]int64{money.USD: 100_000},
		// Найменший вклад — $100 (≈4 412 ₴). Теж більший за ціль, але
		// на порядок ближчий, і саме він тут доречний.
		DepositMinByCur: map[string]int64{money.USD: 10_000},
	}
	out := buildRebalance(in)

	var row *state.RebalanceRow
	for i := range out.Rebalance {
		if out.Rebalance[i].Key == money.USD {
			row = &out.Rebalance[i]
		}
	}
	if row == nil {
		t.Fatal("рядка ребалансу для USD немає")
	}
	if row.UnitKind != "deposit" {
		t.Errorf("одиниця входу %q, очікували \"deposit\": папір на $1000 не влазить у ціль 2 500 ₴",
			row.UnitKind)
	}
	if row.BondCostNative != 100 {
		t.Errorf("вхід %v, очікували 100 (мінімальний вклад)", row.BondCostNative)
	}
	// Здійсненність міряється ОДИНИЦЕЮ ВХОДУ проти цілі, і тут вона чесно
	// негативна: навіть вклад на $100 більший за 2 500 ₴ цілі.
	if row.Feasible {
		t.Error("рядок позначено здійсненним, хоч і вклад більший за цільову суму")
	}
}

// TestConcentrationOrderIsStableOnEqualShares — рядки з ОДНАКОВОЮ часткою
// стоять у сталому порядку.
//
// Це та сама пастка, що вже коштувала копійки в over_uah: рядки сюди
// приходять обходом мапи, а sort.Slice нестабільний. Поки часток-двійників
// немає, порядок відтворюваний випадково — мутація «прибрати ключ третім
// критерієм» golden не валить, і це очікувано.
//
// Але дві облігації з однаковим номіналом — не екзотика, а звичайний
// наслідок рівних покупок. Того дня golden почав би блимати, і причину
// шукали б довго.
func TestConcentrationOrderIsStableOnEqualShares(t *testing.T) {
	in := rebalanceInput{
		Capital:  state.Capital{AccountUAH: 100000},
		Settings: &state.SettingsDoc{LimitISINPct: pct(20)},
		Rates:    fx.Rates{},
		// Три папери, два з них — рівно однакового номіналу.
		NominalByISIN: map[string]int64{
			"UA0000000009": 2_000_000,
			"UA0000000001": 1_000_000,
			"UA0000000005": 1_000_000,
		},
	}
	want := []string{"UA0000000009", "UA0000000001", "UA0000000005"}

	// Порядок обходу мап Go рандомізує на кожному проході, тож одного
	// порівняння замало, щоб відрізнити сталий порядок від випадкового.
	for i := 0; i < 20; i++ {
		out := buildRebalance(in)
		var got []string
		for _, r := range out.Concentration {
			got = append(got, r.Key)
		}
		if len(got) != len(want) {
			t.Fatalf("рядків %d, очікували %d", len(got), len(want))
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("прохід %d: порядок %v, очікували %v — рівні частки стають у випадковому порядку",
					i+1, got, want)
			}
		}
	}
}

// --- місячні гроші по видах ---

// monthRows — три види з цілями 50/10/15 і різним станом: один у перекосі,
// два порожні. Той самий розклад, що на живих даних, лише круглими числами.
func monthRows() []state.RebalanceRow {
	return []state.RebalanceRow{
		{Dimension: "kind", Key: "bonds", TargetPct: 50, CurrentUAH: 55_000},
		{Dimension: "kind", Key: "funds", TargetPct: 10, CurrentUAH: 40_000},
		{Dimension: "kind", Key: "deposits", TargetPct: 15, CurrentUAH: 0},
		{Dimension: "kind", Key: "npf", CurrentUAH: 10_000}, // без цілі — не чіпаємо
	}
}

func rowByKey(rows []state.RebalanceRow, key string) state.RebalanceRow {
	for _, r := range rows {
		if r.Key == key {
			return r
		}
	}
	return state.RebalanceRow{}
}

// TestSpreadMonthShareIgnoresSkew — колонка «за часткою» тримає пропорцію
// й перекосу не помічає.
//
// Це не вада, а її призначення: фонди тут утричі понад ціль і все одно
// дістають свої 10%. Саме тому поруч стоїть друга колонка — одна без
// одної вони відповідали б на питання лише наполовину.
func TestSpreadMonthShareIgnoresSkew(t *testing.T) {
	rows := monthRows()
	// План 12 000, з них 2 000 у резерв → ділиться 10 000.
	spreadMonth(rows, 10_000, 110_000)

	for _, c := range []struct {
		key  string
		want float64
	}{{"bonds", 5000}, {"funds", 1000}, {"deposits", 1500}} {
		if got := rowByKey(rows, c.key).MonthShareUAH; got != c.want {
			t.Errorf("%s за часткою %v, очікували %v", c.key, got, c.want)
		}
	}
	// Нерозподілені 25% нікому не дістаються: сума колонки менша за
	// доступне рівно на них.
	var sum float64
	for _, r := range rows {
		sum += r.MonthShareUAH
	}
	if sum != 7500 {
		t.Errorf("сума колонки %v, очікували 7500 — решта 25%% лишається користувачу", sum)
	}
	// Рядок без цілі місячних чисел не дістає взагалі.
	if r := rowByKey(rows, "reserve"); r.MonthShareUAH != 0 || r.MonthBalanceUAH != 0 {
		t.Errorf("рядок без цілі дістав місячні гроші: %+v", r)
	}
}

// TestSpreadMonthBalanceSkipsOverweight — колонка «на вирівнювання» не дає
// нічого виду, який уже понад ціль, і ділить пропорційно потребам, коли
// грошей на всіх не вистачає.
//
// База після місяця — 110 000 + 10 000 = 120 000, тобто РОЗДІЛЮВАНІ гроші, а
// не весь план: те, що пішло в подушку, з цього знаменника виходить зовсім.
// Звідси: bonds 60 000 при 55 000 → треба 5 000; funds 12 000 при 40 000 →
// нуль; deposits 18 000 при нулі → треба 18 000. Разом 23 000 при доступних
// 10 000, тобто пропорційно.
func TestSpreadMonthBalanceSkipsOverweight(t *testing.T) {
	rows := monthRows()
	spreadMonth(rows, 10_000, 110_000)

	if got := rowByKey(rows, "funds").MonthBalanceUAH; got != 0 {
		t.Errorf("фонди на вирівнювання %v, очікували 0 — вони вчетверо понад ціль", got)
	}
	bonds := rowByKey(rows, "bonds").MonthBalanceUAH
	deps := rowByKey(rows, "deposits").MonthBalanceUAH
	if bonds <= 0 || deps <= bonds {
		t.Errorf("bonds %v, deposits %v — більший розрив мусить дістати більше", bonds, deps)
	}
	if sum := bonds + deps; sum < 9999.9 || sum > 10000.1 {
		t.Errorf("роздано %v, а доступно було 10000", sum)
	}
}

// TestSpreadMonthBalanceBaseExcludesReserve — майбутня база росте рівно на
// РОЗДІЛЮВАНІ гроші, а не на весь план місяця.
//
// Саме на цьому механізм і ловився на живих даних: база бралась як «портфель
// + весь план», тобто разом із подушкою, майбутня ціль виходила завищеною на
// її розмір, і ОВДП у переборі діставали 2 766 ₴ замість 800. Числа тут
// зафіксовані точно, бо перевіряється саме знаменник: 110 000 + 10 000, а не
// 110 000 + 20 000.
func TestSpreadMonthBalanceBaseExcludesReserve(t *testing.T) {
	rows := monthRows()
	spreadMonth(rows, 10_000, 110_000)

	// Потреби від бази 120 000: bonds 5 000, deposits 18 000, разом 23 000
	// при доступних 10 000 → коефіцієнт 10/23.
	for _, c := range []struct {
		key  string
		want float64
	}{
		{"bonds", round2(5_000.0 / 23_000 * 10_000)},
		{"deposits", round2(18_000.0 / 23_000 * 10_000)},
	} {
		if got := rowByKey(rows, c.key).MonthBalanceUAH; got != c.want {
			t.Errorf("%s на вирівнювання %v, очікували %v — база після місяця мусить бути "+
				"110 000 + 10 000, без грошей подушки", c.key, got, c.want)
		}
	}
}

// TestSpreadMonthBalanceRestBySharePct — коли грошей більше, ніж потреб,
// потреби закриваються повністю, а лишок ділиться за ЦІЛЬОВИМИ частками.
//
// Саме за частками від НАЗВАНИХ, а не нормалізованими до сотні: інакше
// нерозподілені відсотки мовчки дісталися б тим видам, у яких ціль є, і
// застосунок сам вирішив би долю грошей, які користувач лишив собі.
func TestSpreadMonthBalanceRestBySharePct(t *testing.T) {
	rows := []state.RebalanceRow{
		{Dimension: "kind", Key: "bonds", TargetPct: 50, CurrentUAH: 1_000},
		{Dimension: "kind", Key: "funds", TargetPct: 10, CurrentUAH: 100},
	}
	// Капітал 1 100, план 100 000 — потреби мізерні проти доступного.
	spreadMonth(rows, 100_000, 1_100)

	b, f := rowByKey(rows, "bonds"), rowByKey(rows, "funds")
	// Потреби: bonds 50 550 − 1 000 = 49 550; funds 10 110 − 100 = 10 010.
	// Разом 59 560, лишок 40 440 → за частками 50% і 10%.
	if want := 49_550 + 40_440*0.5; b.MonthBalanceUAH != want {
		t.Errorf("bonds на вирівнювання %v, очікували %v", b.MonthBalanceUAH, want)
	}
	if want := 10_010 + 40_440*0.1; f.MonthBalanceUAH != want {
		t.Errorf("funds на вирівнювання %v, очікували %v", f.MonthBalanceUAH, want)
	}
}

// TestSpreadMonthSilentWithoutPlan — без плану місяця чисел немає взагалі.
//
// Нулі тут читались би як «плану вистачає рівно на нуль», а це інша
// відповідь, ніж «плану доходу немає».
func TestSpreadMonthSilentWithoutPlan(t *testing.T) {
	for _, c := range []struct {
		name  string
		avail float64
	}{
		{"плану немає", 0},
		{"усе забрав резерв", 0},
		{"мінус (стеля більша за план)", -500},
	} {
		rows := monthRows()
		spreadMonth(rows, c.avail, 110_000)
		for _, r := range rows {
			if r.MonthShareUAH != 0 || r.MonthBalanceUAH != 0 {
				t.Errorf("%s: %s дістав місячні гроші (%v / %v)",
					c.name, r.Key, r.MonthShareUAH, r.MonthBalanceUAH)
			}
		}
	}
}
