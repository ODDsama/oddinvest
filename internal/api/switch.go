// Заміна паперу: чи варто продати тримане й купити краще — розрахунок
// для GET /api/switch (handlers_switch.go).

package api

import (
	"context"
	"sort"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	money "github.com/Rhymond/go-money"
)

// SwitchAlt — з чим порівнюємо. Одна альтернатива на всі рядки, а не
// своя на кожен: рейтинг помічника впорядкований за реальною дохідністю,
// і «найкраще доступне» — це один рядок, а не сто вісімдесят сім.
//
// Валюта альтернативи може відрізнятись від валюти паперу, і це не
// помилка: реальна дохідність на те й реальна, щоб бути порівнянною між
// валютами (див. RealYield). Дисконтування ж іде НОМІНАЛЬНОЮ ставкою
// валюти самого паперу — переклад робить NominalYield.
type SwitchAlt struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// ISIN — лише в облігації. Потрібен рівно для того, щоб не радити
	// перекласти папір сам у себе (див. SwitchRows).
	ISIN     string  `json:"isin,omitempty"`
	Currency string  `json:"currency"`
	RealPct  float64 `json:"real_pct"`
}

// SwitchRow — один папір у портфелі й поріг для нього.
type SwitchRow struct {
	ISIN     string `json:"isin"`
	Currency string `json:"currency"`
	Qty      int64  `json:"qty"`
	Maturity string `json:"maturity"`
	// CostPerBond — за скільки папір коштував ТОБІ (середня брудна ціна з
	// комісією). Стоїть поруч із порогом, бо без неї не видно головного:
	// поріг нижчий за собівартість означає продаж у збиток, і рішення про
	// нього приймають інакше.
	CostPerBond MoneyJSON `json:"cost_per_bond"`
	// Accrued — НКД на сьогодні, той самий, що в картці позиції.
	// Показується, бо котирування брокера ЧИСТЕ, а виручка — брудна.
	Accrued MoneyJSON `json:"accrued"`
	// BreakEven — чиста ціна за папір, за якої перекладання нічого не
	// змінює. BreakEvenPct — вона ж у відсотках номіналу: саме так
	// котирування й називають, і порівнювати з ним зручніше, ніж із
	// гривнями.
	BreakEven    MoneyJSON `json:"break_even"`
	BreakEvenPct float64   `json:"break_even_pct,omitempty"`
	// HoldRealPct — реальна дохідність, яку папір дає ТОБІ за твоєю
	// собівартістю. Не поріг і не альтернатива: третє число, яке пояснює
	// перші два («тримаю під 4.1%, дають 6.8%»).
	HoldRealPct float64 `json:"hold_real_pct,omitempty"`
	// Reason — чому рядок мовчить, коли поріг не порахувався. Порожньо
	// означає, що поріг є.
	Reason string `json:"reason,omitempty"`
}

// SwitchAlternative — найкраще, що помічник пропонує сьогодні.
//
// nil означає «нема з чим порівнювати»: порожній довідник, порожня
// політика або свіжа база. Це законний стан, і вигадувати замість нього
// нульову ставку не можна — під нуль поріг дорівнював би сумі всіх
// майбутніх виплат, тобто радив би продати будь-що за будь-яку ціну.
func (e *Engine) SwitchAlternative(ctx context.Context, now time.Time) (*SwitchAlt, error) {
	doc, err := e.BuildState(ctx, now)
	if err != nil {
		return nil, err
	}
	sugg, err := e.ReinvestSuggestions(ctx, now, doc)
	if err != nil {
		return nil, err
	}
	best := -1
	for i := range sugg {
		if best < 0 || sugg[i].RealPct > sugg[best].RealPct {
			best = i
		}
	}
	if best < 0 {
		return nil, nil
	}
	g := sugg[best]
	return &SwitchAlt{Kind: g.Kind, Label: g.Label, ISIN: g.ISIN,
		Currency: g.Currency, RealPct: g.RealPct}, nil
}

// SwitchRows — поріг на кожен папір, який ще в портфелі.
func (e *Engine) SwitchRows(ctx context.Context, now time.Time, alt *SwitchAlt) ([]SwitchRow, error) {
	lots, sales, bonds, pays, err := e.Portfolio(ctx)
	if err != nil {
		return nil, err
	}
	today := domain.NewDate(now)
	deval := e.Devaluation(ctx)
	held, err := heldByISIN(lots, sales, bonds, today)
	if err != nil {
		return nil, err
	}

	isins := make([]string, 0, len(held))
	for isin := range held {
		isins = append(isins, isin)
	}
	// Порядок сталий — за ISIN. Мапа дала б новий порядок на кожен запит,
	// і таблиця перетасовувалась би сама собою між оновленнями.
	sort.Strings(isins)

	rows := make([]SwitchRow, 0, len(isins))
	for _, isin := range isins {
		b, h := bonds[isin], held[isin]
		cur := b.Nominal.Currency().Code
		row := SwitchRow{ISIN: isin, Currency: cur, Qty: h.qty,
			Maturity:    string(b.Maturity),
			CostPerBond: ToMoneyJSON(avgPerBond(h.cost, h.qty))}
		if acc, aerr := domain.EstimateAccrued(pays, isin, today); aerr == nil {
			row.Accrued = ToMoneyJSON(acc)
		}
		if y, ok := domain.WeightedYTM(h.ytm, pays); ok {
			row.HoldRealPct = Round2(RealYield(y/100, cur, deval) * 100)
		}
		if alt == nil {
			row.Reason = "нема з чим порівнювати: помічник не пропонує жодного інструмента"
			rows = append(rows, row)
			continue
		}
		// Найкраще доступне — цей самий папір. Поріг тут вироджується в
		// його ж справедливу ціну, тобто в пораду «продай і купи те саме»,
		// якої не буває: спред брокера з'їв би різницю ще до угоди.
		//
		// Рядок не ховаємо, бо це змістовна відповідь, а не порожнеча:
		// «краще за те, що вже в тебе, зараз не пропонують» — саме те, що
		// людина хотіла почути, ставлячи питання.
		if alt.Kind == "bond" && alt.ISIN == isin {
			row.Reason = "найкраще, що зараз дають, — цей самий папір"
			rows = append(rows, row)
			continue
		}
		be, berr := domain.BreakEvenClean(domain.SwitchInput{
			ISIN: isin, Payments: pays, Today: today,
			AltRatePct: NominalYield(alt.RealPct/100, cur, deval) * 100,
		})
		if berr != nil {
			row.Reason = berr.Error()
			rows = append(rows, row)
			continue
		}
		row.BreakEven = ToMoneyJSON(be)
		if b.Nominal != nil && b.Nominal.Amount() > 0 {
			row.BreakEvenPct = Round2(float64(be.Amount()) / float64(b.Nominal.Amount()) * 100)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// heldPaper — залишок одного паперу, зведений із лотів.
type heldPaper struct {
	qty  int64
	cost *money.Money
	ytm  []domain.YTMLot
}

// heldByISIN — те, що ще в портфелі, зведене по паперу.
//
// Позиція — це всі непродані лоти одного ISIN, і поріг ставиться до
// ПАПЕРУ, а не до окремої покупки: продають папір, а який саме лот при
// цьому зникає — питання обліку, а не рішення.
//
// Погашені папери відсіюються тут же: у них немає майбутніх виплат, тож
// поріг для них не існує, а рядок із причиною «немає виплат» був би
// шумом на кожен колишній папір портфеля.
func heldByISIN(lots []domain.Lot, sales []domain.Sale,
	bonds map[string]domain.Bond, today domain.Date) (map[string]*heldPaper, error) {
	out := map[string]*heldPaper{}
	for _, l := range lots {
		b, ok := bonds[l.ISIN]
		if !ok || b.Maturity.Before(today) {
			continue
		}
		q := domain.RemainingQtyNow(l, sales)
		if q == 0 {
			continue
		}
		cost := domain.MulQty(l.PricePerBond, q)
		if l.Fee != nil && !l.Fee.IsZero() {
			fee, err := domain.Apportion(l.Fee, q, l.Qty)
			if err != nil {
				return nil, err
			}
			if cost, err = cost.Add(fee); err != nil {
				return nil, err
			}
		}
		h := out[l.ISIN]
		if h == nil {
			h = &heldPaper{cost: money.New(0, cost.Currency().Code)}
			out[l.ISIN] = h
		}
		sum, err := h.cost.Add(cost)
		if err != nil {
			return nil, err
		}
		h.cost, h.qty = sum, h.qty+q
		h.ytm = append(h.ytm, YTMLot(l, q))
	}
	return out, nil
}

// avgPerBond — середня ціна за папір із сумарної вартості позиції.
//
// Ділення грошей на кількість у пакеті domain немає, і це не недогляд:
// там гроші або множаться на кількість (MulQty), або розкладаються між
// частинами без утрати копійки (Apportion). Тут потрібне саме СЕРЕДНЄ —
// число для показу, а не сума, з якою щось звірятимуть, — тож залишок від
// ділення відкидається, і жити такому діленню варто тут, а не поруч із
// точною арифметикою.
func avgPerBond(m *money.Money, qty int64) *money.Money {
	if m == nil || qty <= 0 {
		return m
	}
	return money.New(m.Amount()/qty, m.Currency().Code)
}
