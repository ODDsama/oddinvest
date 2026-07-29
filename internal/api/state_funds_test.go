package api

import (
	"math"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/store"
)

// TestFundsYieldIsWeightedByMarketValue — зведена дохідність фондів важить
// ринковою вартістю, а не рахує просте середнє.
//
// Golden цього не стереже: у багатій фікстурі лише ОДИН фонд має
// ненульову вартість, а на одному фонді зважене й просте середнє
// збігаються. Мутаційна перевірка це й показала — підміна ваг на одиниці
// golden не завалила. Тож перевірка тут, на двох фондах різного розміру.
//
// Питання не косметичне: дрібний фонд із гучним відсотком інакше тягнув
// би плитку на себе, і вона суперечила б таблиці під собою.
func TestFundsYieldIsWeightedByMarketValue(t *testing.T) {
	today := domain.Date("2026-07-15")
	ops := []domain.FundOp{
		// Великий фонд: 100 сертифікатів по 10.00 ₴ = 1 000 ₴ вартості.
		{Date: "2025-01-10", Fund: "Великий", Kind: domain.FundBuy,
			Qty: 100, Amount: 100_000, Currency: money.UAH},
		{Date: "2025-07-10", Fund: "Великий", Kind: domain.FundDividend,
			Amount: 5_000, Currency: money.UAH},
		// Дрібний: 10 сертифікатів по тій самій ціні = 100 ₴ вартості,
		// але дивіденд удвічі більший за розміром позиції.
		{Date: "2025-01-11", Fund: "Дрібний", Kind: domain.FundBuy,
			Qty: 10, Amount: 10_000, Currency: money.UAH},
		{Date: "2025-07-11", Fund: "Дрібний", Kind: domain.FundDividend,
			Amount: 4_000, Currency: money.UAH},
	}
	src := &sources{fundOps: ops, fundRefs: map[string]store.Fund{}}
	hold := domain.NewHoldings(nil, nil, nil, ops, nil, today)

	out := buildFunds(src, hold, fx.Rates{}, 0, today)
	if len(out.Rows) != 2 {
		t.Fatalf("очікували 2 рядки фондів, маємо %d", len(out.Rows))
	}

	nom := map[string]float64{}
	mv := map[string]float64{}
	for _, r := range out.Rows {
		n := r.TotalPct
		if n == 0 {
			n = r.YieldNetPct
		}
		nom[r.Fund] = n
		mv[r.Fund] = r.MarketValue
	}
	if mv["Великий"] <= mv["Дрібний"] {
		t.Fatalf("фікстура зіпсована: великий фонд не більший (%v проти %v)",
			mv["Великий"], mv["Дрібний"])
	}
	if nom["Великий"] == nom["Дрібний"] {
		t.Fatalf("фікстура зіпсована: дохідності однакові (%v), зважування не видно",
			nom["Великий"])
	}

	// Дві незалежні властивості будь-якого середньозваженого. Порівнювати
	// з переписаною тут же формулою було б безглуздо — такий тест повторює
	// код, а не перевіряє його.
	//
	// Перша: результат лежить МІЖ крайніми значеннями. Ловить зіпсовані
	// ваги, від яких число вилітає за межі обох фондів.
	lo, hi := math.Min(nom["Великий"], nom["Дрібний"]), math.Max(nom["Великий"], nom["Дрібний"])
	if out.YieldPct < lo || out.YieldPct > hi {
		t.Errorf("зведена дохідність %v поза межами [%v; %v] — це вже не середнє",
			out.YieldPct, lo, hi)
	}
	// Друга: воно ближче до ВЕЛИКОГО фонду, ніж просте середнє. Ловить
	// втрату самих ваг, від якої число лишається в межах, але з'їжджає в
	// середину.
	mean := (nom["Великий"] + nom["Дрібний"]) / 2
	if math.Abs(out.YieldPct-nom["Великий"]) >= math.Abs(mean-nom["Великий"]) {
		t.Errorf("зведена дохідність %v не ближча до великого фонду (%v), ніж просте середнє (%v) — ваги не працюють",
			out.YieldPct, nom["Великий"], mean)
	}
}
