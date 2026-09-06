// Планові витрати: вирішені разові гроші з датою й станом «сплачено».
//
// Окремо від handlers_plan.go з того самого доводу, що розводить самі
// таблиці (міграція 0056): потік описує РИТМ життя, а планова витрата —
// ПОДІЮ з датою й станом. У потоку є періодичність, індексація, частка в
// портфель і дозволи, яких у разової витрати немає; у витрати є контур і
// відмітка «сплачено», яких немає в потоку.
//
// СУТНІСТЬ ОДНА — ОДИН НАБІР РУЧОК, і журналу під нею немає навмисно:
// витрата або сплачена, або ні, і обидва стани видно в самому рядку.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

type planExpenseReq struct {
	Name string `json:"name"`
	// Amount — сума десятковим, У ВАЛЮТІ ВИТРАТИ й ЗАВЖДИ ДОДАТНА: напрям
	// несе сама сутність, а не знак (те саме правило, що в plan_flows).
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
	// DueDate обовʼязкова, на відміну від дедлайну цілі: витрата без дати
	// не тисне ні на який місяць, тобто не робить нічого.
	DueDate string `json:"due_date"`
	// PaidFrom — з ЯКИХ грошей вона піде. Порожньо = card: типова разова
	// витрата йде з картки, бо саме там за побудовою живе «все інше».
	PaidFrom string `json:"paid_from"`
	// PaidDate — гроші пішли. Порожньо = ще винна; сюди ж вертається
	// помилково закрита витрата.
	PaidDate string `json:"paid_date"`
	Place    string `json:"place"`
	Note     string `json:"note"`
}

func planExpenseFromReq(req planExpenseReq) (domain.PlanExpense, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return domain.PlanExpense{}, errors.New("планова витрата без назви: за нею її й шукатимуть")
	}
	cur := orUAH(strings.TrimSpace(req.Currency))
	minor, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		return domain.PlanExpense{}, err
	}
	// Нуль і мінус відхиляємо: нульова витрата не тисне ні на що, а
	// відʼємна означала б дохід, і місце йому в потоках.
	if minor <= 0 {
		return domain.PlanExpense{}, errors.New("сума планової витрати має бути більшою за нуль")
	}
	e := domain.PlanExpense{
		Name: name, Amount: minor, Currency: cur,
		Place: strings.TrimSpace(req.Place), Note: req.Note,
	}
	if e.DueDate, err = domain.ParseDate(strings.TrimSpace(req.DueDate)); err != nil {
		return domain.PlanExpense{}, fmt.Errorf("дата витрати: %w", err)
	}
	// Порожня дата сплати ЗАКОННА, і саме тому не йде в ParseDate
	// беззастережно: щойно заведена витрата ще нікому не сплачена.
	if d := strings.TrimSpace(req.PaidDate); d != "" {
		if e.PaidDate, err = domain.ParseDate(d); err != nil {
			return domain.PlanExpense{}, fmt.Errorf("дата сплати: %w", err)
		}
	}
	// Контур перевіряється ТУТ, а не лише CHECK-ом у базі: причина мусить
	// бути текстом. Цей набір читає арифметика — описка перекинула б гроші
	// не в той бік, і сира помилка обмеження нічого б про це не сказала.
	switch strings.TrimSpace(req.PaidFrom) {
	case "", domain.PaidFromCard:
		e.PaidFrom = domain.PaidFromCard
	case domain.PaidFromPlan:
		e.PaidFrom = domain.PaidFromPlan
	default:
		return domain.PlanExpense{}, fmt.Errorf(
			"невідомий контур %q: буває «card» (з картки, з побутових грошей) "+
				"або «plan» (з портфельних, тобто менше піде в папери)", req.PaidFrom)
	}
	return e, nil
}

func (s *Server) handleAddPlanExpense(w http.ResponseWriter, r *http.Request) {
	var req planExpenseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	e, err := planExpenseFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddPlanExpense(r.Context(), e)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// handleUpdatePlanExpense — ПОВНА заміна рядка, і саме нею робиться
// відмітка «сплачено»: окрема ручка на одне поле була б другим способом
// написати те, що вже вміє правка, а різниця між «не надіслали paid_date»
// і «спорожнили paid_date» у ній зникла б — тобто зняти помилкову
// позначку стало б неможливо.
func (s *Server) handleUpdatePlanExpense(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req planExpenseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	e, err := planExpenseFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	e.ID = id
	if err := s.st.UpdatePlanExpense(r.Context(), e); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeletePlanExpense(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeletePlanExpense(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// handleListPlanExpenses віддає СИРІ витрати, без «скільки з них тисне
// цього місяця».
//
// Пораховані числа живуть у /api/summary (month_plan.planned_uah,
// debt.exit.planned_uah), і другого їх джерела тут бути не мусить —
// правило «арифметика в одному місці» саме про це. Цей список потрібен
// формам правки: їм треба те, що ввів користувач.
//
// Сплачені теж у списку: вони не зникають, а стають історією — і саме
// тому кнопка «сплачено» не є видаленням.
func (s *Server) handleListPlanExpenses(w http.ResponseWriter, r *http.Request) {
	exps, err := s.st.ListPlanExpenses(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type planExpenseJSON struct {
		ID       int64     `json:"id"`
		Name     string    `json:"name"`
		Amount   moneyJSON `json:"amount"`
		DueDate  string    `json:"due_date"`
		PaidFrom string    `json:"paid_from"`
		PaidDate string    `json:"paid_date"`
		Place    string    `json:"place"`
		Note     string    `json:"note"`
	}
	out := make([]planExpenseJSON, 0, len(exps))
	for _, e := range exps {
		out = append(out, planExpenseJSON{e.ID, e.Name,
			toMoneyJSON(money.New(e.Amount, e.Currency)),
			string(e.DueDate), e.PaidFrom, string(e.PaidDate), e.Place, e.Note})
	}
	writeJSON(w, http.StatusOK, out)
}
