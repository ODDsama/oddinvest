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
