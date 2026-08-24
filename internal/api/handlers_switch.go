// Перекладання: за якою ціною варто продати папір і взяти те, що дають
// зараз.
//
// ЧОМУ ЦЕ ОКРЕМИЙ ЕНДПОЙНТ, А НЕ РЯДОК У ПОМІЧНИКУ. Помічник відповідає
// на «куди подіти ВІЛЬНІ гроші», і його перелік — це те, що можна купити
// сьогодні на наявний залишок. Тут питання протилежне: гроші не вільні,
// вони вже в папері, і рішення стосується не покупки, а ОБМІНУ. Змішати
// їх в один список означало б поставити поруч рядки з різною ціною
// помилки: не купити щось — це нічого не зробити, а продати — це дія,
// якої не скасувати.
//
// АЛЬТЕРНАТИВА БЕРЕТЬСЯ З ПОМІЧНИКА, і це головне рішення цього файла.
// Свій рейтинг тут означав би, що «Що купити» і «Чи продати» радять
// різне — рівно та розбіжність, проти якої в now-view.js уже стоїть
// окреме попередження. Тому reinvestSuggestions кличеться як є, а звідси
// беруться лише два числа з найкращого рядка.
//
// ЩО ТУТ НЕ ХОВАЄТЬСЯ. Порогів не буває «поганих»: папір, куплений під
// високу ставку, законно має поріг вище за номінал, і сховати такий
// рядок означало б відповісти «нема про що говорити» там, де відповідь
// насправді «тримай». Те саме правило, що в overLimit у помічника.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// switchAlt — з чим порівнюємо. Одна альтернатива на всі рядки, а не
// своя на кожен: рейтинг помічника впорядкований за реальною дохідністю,
// і «найкраще доступне» — це один рядок, а не сто вісімдесят сім.
//
// Валюта альтернативи може відрізнятись від валюти паперу, і це не
// помилка: реальна дохідність на те й реальна, щоб бути порівнянною між
// валютами (див. realYield). Дисконтування ж іде НОМІНАЛЬНОЮ ставкою
// валюти самого паперу — переклад робить nominalYield.
type switchAlt struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// ISIN — лише в облігації. Потрібен рівно для того, щоб не радити
	// перекласти папір сам у себе (див. switchRows).
	ISIN     string  `json:"isin,omitempty"`
	Currency string  `json:"currency"`
	RealPct  float64 `json:"real_pct"`
}

// switchRow — один папір у портфелі й поріг для нього.
type switchRow struct {
	ISIN     string `json:"isin"`
	Currency string `json:"currency"`
	Qty      int64  `json:"qty"`
	Maturity string `json:"maturity"`
	// CostPerBond — за скільки папір коштував ТОБІ (середня брудна ціна з
	// комісією). Стоїть поруч із порогом, бо без неї не видно головного:
	// поріг нижчий за собівартість означає продаж у збиток, і рішення про
	// нього приймають інакше.
	CostPerBond moneyJSON `json:"cost_per_bond"`
	// Accrued — НКД на сьогодні, той самий, що в картці позиції.
	// Показується, бо котирування брокера ЧИСТЕ, а виручка — брудна.
	Accrued moneyJSON `json:"accrued"`
	// BreakEven — чиста ціна за папір, за якої перекладання нічого не
	// змінює. BreakEvenPct — вона ж у відсотках номіналу: саме так
	// котирування й називають, і порівнювати з ним зручніше, ніж із
	// гривнями.
	BreakEven    moneyJSON `json:"break_even"`
	BreakEvenPct float64   `json:"break_even_pct,omitempty"`
	// HoldRealPct — реальна дохідність, яку папір дає ТОБІ за твоєю
	// собівартістю. Не поріг і не альтернатива: третє число, яке пояснює
	// перші два («тримаю під 4.1%, дають 6.8%»).
	HoldRealPct float64 `json:"hold_real_pct,omitempty"`
	// Reason — чому рядок мовчить, коли поріг не порахувався. Порожньо
	// означає, що поріг є.
	Reason string `json:"reason,omitempty"`
}

// handleSwitch — GET /api/switch: пороги для всіх паперів у портфелі.
func (s *Server) handleSwitch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	alt, err := s.switchAlternative(ctx, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rows, err := s.switchRows(ctx, now, alt)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Alt  *switchAlt  `json:"alt,omitempty"`
		Rows []switchRow `json:"rows"`
	}{Alt: alt, Rows: rows})
}

// switchVerdictOut — відповідь на введене котирування.
type switchVerdictOut struct {
	ISIN string `json:"isin"`
	Qty  int64  `json:"qty"`
	// HoldRealPct — реальна дохідність, від якої відмовляєшся, продаючи
	// за цією ціною; AltRealPct — та, яку натомість отримуєш.
	// EdgePP — різниця в п.п.: додатне означає «перекладати вигідно».
	HoldRealPct float64 `json:"hold_real_pct"`
	AltRealPct  float64 `json:"alt_real_pct"`
	EdgePP      float64 `json:"edge_pp"`
	// Gain — виграш у грошах: на папір і на всю позицію. Це різниця двох
	// СЬОГОДНІШНІХ сум, тож «строку окупності» поруч немає й не буде —
	// аргумент у шапці domain/switch.go.
	GainPerBond moneyJSON `json:"gain_per_bond"`
	GainTotal   moneyJSON `json:"gain_total"`
	Worth       bool      `json:"worth"`
}

// handleSwitchVerdict — POST /api/switch: вердикт за котируванням брокера.
//
// Ціна приймається тілом, а не запитом: це введене людиною число, і місце
// введених чисел — тіло, як у /api/whatif поруч. Нікуди не зберігається:
// котирування живе годину, а рядок у базі — вічно.
func (s *Server) handleSwitchVerdict(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ISIN  string `json:"isin"`
		Clean string `json:"clean"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx := r.Context()
	now := time.Now()
	today := domain.NewDate(now)

	alt, err := s.switchAlternative(ctx, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if alt == nil {
		writeErr(w, http.StatusConflict,
			fmt.Errorf("нема з чим порівнювати: помічник не пропонує жодного інструмента"))
		return
	}
	// Довідник питаємо ОКРЕМО від портфеля, і це не зайвий запит.
	// s.portfolio віддає лише папери, на які є лоти, тож перевірка по
	// ньому злила б дві різні відповіді в одну: «такого паперу не існує»
	// (404, помилка клієнта) і «такий папір є, але не в тебе» (409, стан
	// портфеля). Людині це різні речі, і коду теж.
	b, err := s.st.GetBond(ctx, req.ISIN)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if b == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("паперу %s немає в довіднику", req.ISIN))
		return
	}
	lots, sales, _, pays, err := s.portfolio(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	cur := b.Nominal.Currency().Code
	clean, err := parseMoney(req.Clean, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var qty int64
	for _, l := range lots {
		if l.ISIN == req.ISIN {
			qty += domain.RemainingQtyNow(l, sales)
		}
	}
	if qty == 0 {
		writeErr(w, http.StatusConflict, fmt.Errorf("паперів %s у портфелі немає", req.ISIN))
		return
	}

	deval := s.devaluation(ctx)
	res, err := domain.SwitchVerdict(domain.SwitchInput{
		ISIN: req.ISIN, Payments: pays, Today: today,
		AltRatePct: nominalYield(alt.RealPct/100, cur, deval) * 100,
	}, clean)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	holdReal := round2(realYield(res.HoldRatePct/100, cur, deval) * 100)
	writeJSON(w, http.StatusOK, switchVerdictOut{
		ISIN: req.ISIN, Qty: qty,
		HoldRealPct: holdReal, AltRealPct: alt.RealPct,
		EdgePP:      round2(alt.RealPct - holdReal),
		GainPerBond: toMoneyJSON(res.GainPerBond),
		GainTotal:   toMoneyJSON(domain.MulQty(res.GainPerBond, qty)),
		Worth:       res.GainPerBond.Amount() > 0,
	})
}

// switchAlternative — найкраще, що помічник пропонує сьогодні.
//
// nil означає «нема з чим порівнювати»: порожній довідник, порожня
// політика або свіжа база. Це законний стан, і вигадувати замість нього
// нульову ставку не можна — під нуль поріг дорівнював би сумі всіх
// майбутніх виплат, тобто радив би продати будь-що за будь-яку ціну.
func (s *Server) switchAlternative(ctx context.Context, now time.Time) (*switchAlt, error) {
	doc, err := s.buildState(ctx, now)
	if err != nil {
		return nil, err
	}
	sugg, err := s.reinvestSuggestions(ctx, now, doc)
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
	return &switchAlt{Kind: g.Kind, Label: g.Label, ISIN: g.ISIN,
		Currency: g.Currency, RealPct: g.RealPct}, nil
}

// switchRows — поріг на кожен папір, який ще в портфелі.
func (s *Server) switchRows(ctx context.Context, now time.Time, alt *switchAlt) ([]switchRow, error) {
	lots, sales, bonds, pays, err := s.portfolio(ctx)
	if err != nil {
		return nil, err
	}
	today := domain.NewDate(now)
	deval := s.devaluation(ctx)
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

	rows := make([]switchRow, 0, len(isins))
	for _, isin := range isins {
		b, h := bonds[isin], held[isin]
		cur := b.Nominal.Currency().Code
		row := switchRow{ISIN: isin, Currency: cur, Qty: h.qty,
			Maturity:    string(b.Maturity),
			CostPerBond: toMoneyJSON(avgPerBond(h.cost, h.qty))}
		if acc, aerr := domain.EstimateAccrued(pays, isin, today); aerr == nil {
			row.Accrued = toMoneyJSON(acc)
		}
		if y, ok := domain.WeightedYTM(h.ytm, pays); ok {
			row.HoldRealPct = round2(realYield(y/100, cur, deval) * 100)
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
			AltRatePct: nominalYield(alt.RealPct/100, cur, deval) * 100,
		})
		if berr != nil {
			row.Reason = berr.Error()
			rows = append(rows, row)
			continue
		}
		row.BreakEven = toMoneyJSON(be)
		if b.Nominal != nil && b.Nominal.Amount() > 0 {
			row.BreakEvenPct = round2(float64(be.Amount()) / float64(b.Nominal.Amount()) * 100)
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
		h.ytm = append(h.ytm, ytmLot(l, q))
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
