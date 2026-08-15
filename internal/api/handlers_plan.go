// Джерела доходу й планові дії (фаза 9 «План»).
//
// CRUD за зразком handlers_deposits.go/handlers_reserve.go. Сама
// арифметика — розгортання потоку в помісячний вектор, дії set_shares/lock
// у рукавах — живе в internal/api/state_projection.go (sleeveFactory):
// тут лише зберігання й перевірка форми.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

// writeStoreErr розрізняє «такого id немає» й решту. Доти PUT за
// неіснуючим id віддавав 400 — той самий код, що й «сума має бути > 0», —
// і клієнт не міг відповісти на просте питання: це я надіслав дурницю чи
// рядок уже видалили в іншій вкладці.
//
// store.ErrNotFound поки читають лише планові гендлери; решта користується
// тим самим affectedOne і далі віддає 400 (див. коментар при сентинелі).
func writeStoreErr(w http.ResponseWriter, err error, fallback int) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeErr(w, fallback, err)
}

// --- потоки ---

type planFlowReq struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`   // income | expense
	Amount    string `json:"amount"` // десятковий, > 0
	Currency  string `json:"currency"`
	Cadence   string `json:"cadence"` // once | month | quarter | year
	FromDate  string `json:"from_date"`
	UntilDate string `json:"until_date,omitempty"`
	GrowthPct string `json:"growth_pct,omitempty"` // порожньо = 0
	InvestPct string `json:"invest_pct,omitempty"` // порожньо = 100
	Note      string `json:"note,omitempty"`
}

func planFlowFromReq(req planFlowReq) (store.PlanFlow, error) {
	var out store.PlanFlow
	switch req.Kind {
	case "income", "expense":
	default:
		return out, fmt.Errorf("kind має бути income або expense, маємо %q", req.Kind)
	}
	switch req.Cadence {
	case "once", "month", "quarter", "year":
	default:
		return out, fmt.Errorf("cadence має бути once, month, quarter або year, маємо %q", req.Cadence)
	}
	cur := strings.TrimSpace(req.Currency)
	if cur == "" {
		cur = money.UAH
	}
	amt, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		return out, fmt.Errorf("сума: %w", err)
	}
	if amt <= 0 {
		return out, errors.New("сума потоку має бути > 0")
	}
	from, err := domain.ParseDate(req.FromDate)
	if err != nil {
		return out, fmt.Errorf("дата початку: %w", err)
	}
	var until domain.Date
	if strings.TrimSpace(req.UntilDate) != "" {
		if until, err = domain.ParseDate(req.UntilDate); err != nil {
			return out, fmt.Errorf("дата завершення: %w", err)
		}
		if from.After(until) {
			return out, errors.New("дата завершення має бути не раніше початку")
		}
	}
	growth := int64(0)
	if strings.TrimSpace(req.GrowthPct) != "" {
		if growth, err = parsePercentBP(req.GrowthPct); err != nil {
			return out, fmt.Errorf("індексація: %w", err)
		}
	}
	// Межі, бо індексація складається щороку на весь горизонт: описка
	// «1000» замість «10» дає ×11 на рік, тобто на десятирічному прогнозі
	// число, у яке ніхто не вдивлятиметься — воно просто зробить ціль
	// «досягнутою». −100% знизу виключено: потік, що за рік зникає
	// повністю, описується датою завершення, а не індексацією.
	if growth <= -10000 || growth > 10000 {
		return out, errors.New("індексація має бути більшою за -100% і не більшою за 100%/рік")
	}
	// invest_pct за замовчуванням 100%: типовий випадок — уся сума йде в
	// портфель — не має вимагати вписувати очевидне число.
	invest := int64(10000)
	if strings.TrimSpace(req.InvestPct) != "" {
		if invest, err = parsePercentBP(req.InvestPct); err != nil {
			return out, fmt.Errorf("частка в портфель: %w", err)
		}
	}
	if invest < 0 || invest > 10000 {
		return out, errors.New("частка в портфель має бути від 0 до 100%")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return out, errors.New("назва потоку не може бути порожньою")
	}
	return store.PlanFlow{
		Name: name, Kind: req.Kind, Amount: amt, Currency: cur, Cadence: req.Cadence,
		FromDate: from, UntilDate: until, GrowthBP: growth, InvestBP: invest, Note: req.Note,
	}, nil
}

type planFlowRow struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Amount    moneyJSON `json:"amount"`
	Cadence   string    `json:"cadence"`
	FromDate  string    `json:"from_date"`
	UntilDate string    `json:"until_date,omitempty"`
	GrowthPct float64   `json:"growth_pct,omitempty"`
	InvestPct float64   `json:"invest_pct"`
	Note      string    `json:"note,omitempty"`
	// ProvidesUAH — скільки цей рядок дає в середньому за найближчі 12
	// місяців, ₴/міс; GrossUAH — те саме до «частки в портфель».
	//
	// БЕЗ omitempty обидва: нуль тут осмислена відповідь («цей потік
	// почнеться пізніше ніж за рік»), і колонка мусить його намалювати, а
	// не лишити порожнє місце, яке читається як помилка.
	//
	// Сума ProvidesUAH по всіх рядках == summary.plan_provides_uah. Це не
	// збіг, а те саме означення з переставленими сумами — і воно закріплене
	// тестом, бо саме на ньому стоїть підсумок таблиці.
	ProvidesUAH float64 `json:"provides_uah"`
	GrossUAH    float64 `json:"gross_uah"`
}

func toPlanFlowRow(f store.PlanFlow, today domain.Date, rates fx.Rates) planFlowRow {
	return planFlowRow{
		ID: f.ID, Name: f.Name, Kind: f.Kind,
		Amount: toMoneyJSON(money.New(f.Amount, f.Currency)), Cadence: f.Cadence,
		FromDate: string(f.FromDate), UntilDate: string(f.UntilDate),
		GrowthPct: round2(float64(f.GrowthBP) / 100), InvestPct: round2(float64(f.InvestBP) / 100),
		Note:        f.Note,
		ProvidesUAH: round2(planFlowProvidesUAH(f, today, rates, planProvidesMonths)),
		GrossUAH:    round2(planFlowGrossUAH(f, today, rates, planProvidesMonths)),
	}
}

func (s *Server) handleListPlanFlows(w http.ResponseWriter, r *http.Request) {
	flows, err := s.st.ListPlanFlows(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Курс ковтаємо свідомо: без нього валютні потоки дадуть 0 у гривневій
	// колонці — рівно те, що вже робить plan_provides_uah, тож тотожність
	// не ламається. Єдина поверхня правки плану не має вмирати від того,
	// що довідник НБУ сьогодні не оновився.
	today := domain.NewDate(time.Now())
	rates, _ := s.rates(r.Context()) //nolint:errcheck // свідомо: див. вище
	out := make([]planFlowRow, 0, len(flows))
	for _, f := range flows {
		out = append(out, toPlanFlowRow(f, today, rates))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddPlanFlow(w http.ResponseWriter, r *http.Request) {
	var req planFlowReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	f, err := planFlowFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddPlanFlow(r.Context(), f)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdatePlanFlow(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req planFlowReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	f, err := planFlowFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	f.ID = id
	if err := s.st.UpdatePlanFlow(r.Context(), f); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeletePlanFlow(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeletePlanFlow(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusInternalServerError)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// --- дії ---

type planActionReq struct {
	Date        string `json:"date"`
	Type        string `json:"type"` // set_shares | lock
	USDSharePct string `json:"usd_share_pct,omitempty"`
	EURSharePct string `json:"eur_share_pct,omitempty"`
	Amount      string `json:"amount,omitempty"`
	Currency    string `json:"currency,omitempty"`
	RatePct     string `json:"rate_pct,omitempty"`
	Months      int    `json:"months,omitempty"`
	Name        string `json:"name,omitempty"`
	Note        string `json:"note,omitempty"`
}

// bpOrUnset — рядок відсотка → базисні пункти, -1 якщо порожньо.
// -1, а не 0: 0 тут легальна ціль («долара не лишається зовсім»), і
// NOT NULL DEFAULT 0 тихо означав би протилежне.
func bpOrUnset(s string) (int64, error) {
	if strings.TrimSpace(s) == "" {
		return -1, nil
	}
	bp, err := parsePercentBP(s)
	if err != nil {
		return 0, err
	}
	if bp < 0 || bp > 10000 {
		return 0, errors.New("частка має бути від 0 до 100%")
	}
	return bp, nil
}

func planActionFromReq(req planActionReq) (store.PlanAction, error) {
	var out store.PlanAction
	d, err := domain.ParseDate(req.Date)
	if err != nil {
		return out, fmt.Errorf("дата: %w", err)
	}
	out = store.PlanAction{Date: d, Type: req.Type, USDBP: -1, EURBP: -1, Name: strings.TrimSpace(req.Name), Note: req.Note}
	switch req.Type {
	case "set_shares":
		if out.USDBP, err = bpOrUnset(req.USDSharePct); err != nil {
			return out, fmt.Errorf("частка USD: %w", err)
		}
		if out.EURBP, err = bpOrUnset(req.EURSharePct); err != nil {
			return out, fmt.Errorf("частка EUR: %w", err)
		}
		if out.USDBP < 0 && out.EURBP < 0 {
			return out, errors.New("задай хоча б одну валютну частку")
		}
		usd, eur := out.USDBP, out.EURBP
		if usd < 0 {
			usd = 0
		}
		if eur < 0 {
			eur = 0
		}
		if usd+eur > 10000 {
			return out, errors.New("сума часток USD і EUR не може перевищувати 100%")
		}
	case "lock":
		cur := strings.TrimSpace(req.Currency)
		if cur == "" {
			cur = money.UAH
		}
		amt, err := domain.ParseDecimalToMinor(req.Amount, cur)
		if err != nil {
			return out, fmt.Errorf("сума: %w", err)
		}
		if amt <= 0 {
			return out, errors.New("сума замка має бути > 0")
		}
		rate, err := parsePercentBP(req.RatePct)
		if err != nil {
			return out, fmt.Errorf("ставка: %w", err)
		}
		if rate < 0 {
			return out, errors.New("ставка не може бути відʼємною")
		}
		if req.Months < 0 {
			return out, errors.New("строк не може бути відʼємним")
		}
		if out.Name == "" {
			return out, errors.New("задай назву — нею підпишеться стрічка")
		}
		out.Amount, out.Currency, out.RateBP, out.Months = amt, cur, rate, req.Months
	default:
		return out, fmt.Errorf("type має бути set_shares або lock, маємо %q", req.Type)
	}
	return out, nil
}

type planActionRow struct {
	ID   int64  `json:"id"`
	Date string `json:"date"`
	Type string `json:"type"`
	// USDSharePct/EURSharePct — покажчики, а не float64: 0 тут ЛЕГАЛЬНА
	// задана частка («долара не лишається зовсім»), а omitempty на
	// float64 мовчки з'їв би її так само, як і незадану. nil = не задано.
	USDSharePct *float64   `json:"usd_share_pct,omitempty"`
	EURSharePct *float64   `json:"eur_share_pct,omitempty"`
	Amount      *moneyJSON `json:"amount,omitempty"`
	RatePct     float64    `json:"rate_pct,omitempty"`
	Months      int        `json:"months,omitempty"`
	Name        string     `json:"name,omitempty"`
	Note        string     `json:"note,omitempty"`
}

func toPlanActionRow(a store.PlanAction) planActionRow {
	out := planActionRow{ID: a.ID, Date: string(a.Date), Type: a.Type, Name: a.Name, Note: a.Note}
	if a.USDBP >= 0 {
		v := round2(float64(a.USDBP) / 100)
		out.USDSharePct = &v
	}
	if a.EURBP >= 0 {
		v := round2(float64(a.EURBP) / 100)
		out.EURSharePct = &v
	}
	if a.Type == "lock" {
		v := toMoneyJSON(money.New(a.Amount, a.Currency))
		out.Amount = &v
		out.RatePct = round2(float64(a.RateBP) / 100)
		out.Months = a.Months
	}
	return out
}

func (s *Server) handleListPlanActions(w http.ResponseWriter, r *http.Request) {
	actions, err := s.st.ListPlanActions(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]planActionRow, 0, len(actions))
	for _, a := range actions {
		out = append(out, toPlanActionRow(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddPlanAction(w http.ResponseWriter, r *http.Request) {
	var req planActionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	a, err := planActionFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddPlanAction(r.Context(), a)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdatePlanAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req planActionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	a, err := planActionFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	a.ID = id
	if err := s.st.UpdatePlanAction(r.Context(), a); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeletePlanAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeletePlanAction(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusInternalServerError)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}
