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
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
)

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
