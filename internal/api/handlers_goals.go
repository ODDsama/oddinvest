// Цілі накопичення: сама ціль і журнал під нею.
//
// ДВІ СУТНОСТІ — ДВА НАБОРИ РУЧОК, і звести їх в один не можна. Ціль
// створюється порожньою («збираю на авто $20 000 до березня») і живе далі
// без жодного руху; рух же не має сенсу без цілі. Одна ручка на обидві
// вимагала б або заводити ціль першим внеском — тоді неможливо поставити
// ціль наперед, а це головне, чого від неї хочуть, — або приймати
// напівпорожнє тіло й вгадувати намір за набором полів.
//
// Окремо від handlers_reserve.go з того самого доводу, що розводить самі
// таблиці (міграція 0039): у резерву питають «чи вистачить», у цілі — «чи
// встигну», і в цілі є сума, дата й намір витратити, яких у подушки немає
// за визначенням.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

type goalReq struct {
	Name string `json:"name"`
	// Amount — сума цілі десятковим, У ВАЛЮТІ ЦІЛІ. Гривневого числа тут
	// бути не може: ціна авто в доларах і є ціллю (міграція 0039).
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
	DueDate  string `json:"due_date"`
	Priority string `json:"priority"`
	Place    string `json:"place"`
	Note     string `json:"note"`
	// DoneDate — ціль закрита, річ куплена. Порожньо = відкрита; сюди ж
	// вертається помилково закрита ціль.
	DoneDate string `json:"done_date"`
}

func goalFromReq(req goalReq) (store.Goal, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return store.Goal{}, errors.New("ціль без назви: за нею її й шукатимуть")
	}
	cur := orUAH(strings.TrimSpace(req.Currency))
	minor, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		return store.Goal{}, err
	}
	// Нуль і мінус відхиляємо: ціль — це сума, до якої йдуть, і нульова
	// перетворила б увесь прогрес на ділення на нуль, а відʼємна не означає
	// нічого взагалі.
	if minor <= 0 {
		return store.Goal{}, errors.New("сума цілі має бути більшою за нуль")
	}
	g := store.Goal{
		Name: name, TargetAmount: minor, Currency: cur,
		Place: strings.TrimSpace(req.Place), Note: req.Note,
	}
	// Порожня дата ЗАКОННА в обох полях, і саме тому вони не йдуть у
	// ParseDate беззастережно: ціль без дедлайну нікуди не поспішає, а
	// незакрита ціль не має дати закриття.
	if d := strings.TrimSpace(req.DueDate); d != "" {
		if g.DueDate, err = domain.ParseDate(d); err != nil {
			return store.Goal{}, err
		}
	}
	if d := strings.TrimSpace(req.DoneDate); d != "" {
		if g.DoneDate, err = domain.ParseDate(d); err != nil {
			return store.Goal{}, err
		}
	}
	if p := strings.TrimSpace(req.Priority); p != "" {
		n, perr := strconv.ParseInt(p, 10, 64)
		if perr != nil {
			return store.Goal{}, fmt.Errorf("пріоритет: %w", perr)
		}
		g.Priority = n
	}
	return g, nil
}

func (s *Server) handleAddGoal(w http.ResponseWriter, r *http.Request) {
	var req goalReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	g, err := goalFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddGoal(r.Context(), g)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdateGoal(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req goalReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	g, err := goalFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	g.ID = id
	if err := s.st.UpdateGoal(r.Context(), g); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteGoal — видалити можна лише ціль БЕЗ рухів; відмова приходить
// зі сховища зі своєю причиною (goals.go). 400, а не 500: це не збій, а
// відповідь «так не можна, і ось чому».
func (s *Server) handleDeleteGoal(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteGoal(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// handleListGoals віддає СИРІ цілі, без прогресу.
//
// Пораховані числа живуть у /api/summary (doc.goals), і другого їх джерела
// тут бути не мусить — правило «арифметика в одному місці» саме про це.
// Цей список потрібен формам правки: їм треба те, що ввів користувач, а не
// те, що з нього вивелось.
func (s *Server) handleListGoals(w http.ResponseWriter, r *http.Request) {
	goals, err := s.st.ListGoals(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type goalJSON struct {
		ID       int64     `json:"id"`
		Name     string    `json:"name"`
		Amount   moneyJSON `json:"amount"`
		DueDate  string    `json:"due_date"`
		Priority int64     `json:"priority"`
		Place    string    `json:"place"`
		Note     string    `json:"note"`
		DoneDate string    `json:"done_date"`
	}
	out := make([]goalJSON, 0, len(goals))
	for _, g := range goals {
		out = append(out, goalJSON{g.ID, g.Name,
			toMoneyJSON(money.New(g.TargetAmount, g.Currency)),
			string(g.DueDate), g.Priority, g.Place, g.Note, string(g.DoneDate)})
	}
	writeJSON(w, http.StatusOK, out)
}

type goalOpReq struct {
	GoalID   string `json:"goal_id"`
	Date     string `json:"date"`
	Amount   string `json:"amount"` // десятковий; + відклав, − узяв
	Currency string `json:"currency"`
	Place    string `json:"place"`
	Note     string `json:"note"`
}

func goalOpFromReq(req goalOpReq) (store.GoalOp, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(req.GoalID), 10, 64)
	if err != nil || id <= 0 {
		return store.GoalOp{}, errors.New("рух без цілі: вкажи, у яку ціль він іде")
	}
	d := domain.NewDate(time.Now())
	if req.Date != "" {
		if d, err = domain.ParseDate(req.Date); err != nil {
			return store.GoalOp{}, err
		}
	}
	cur := orUAH(strings.TrimSpace(req.Currency))
	minor, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		return store.GoalOp{}, err
	}
	// Нуль відхиляємо з того самого доводу, що в резерву: рух на нуль нічого
	// не змінює, але засмічує журнал і зсуває «останній рух» на дату, коли
	// насправді нічого не сталось.
	if minor == 0 {
		return store.GoalOp{}, errors.New("сума руху не може бути нульовою")
	}
	return store.GoalOp{GoalID: id, Date: d, Amount: minor, Currency: cur,
		Place: strings.TrimSpace(req.Place), Note: req.Note}, nil
}

func (s *Server) handleAddGoalOp(w http.ResponseWriter, r *http.Request) {
	var req goalOpReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op, err := goalOpFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Знімок рейтингу — ДО запису, тим самим порядком, що й у покупок і в
	// подушки (див. шапку decisions.go): після нього ціль уже підросла,
	// стеля місяця впала, і «від чого ці гроші відмовились» стало б
	// відповіддю про портфель, у якому вони вже відмовились.
	//
	// ЛИШЕ НА ПОПОВНЕННІ, і довід той самий, що в резерву, лише привід
	// інший. Зняття з цілі — рішення протилежного знаку, і альтернатива в
	// ньому не при чому: гроші звідти беруть тоді, коли річ КУПЛЕНО, тобто
	// коли ціль досягнута. Записати таке рядком «відмовився від 9.4%»
	// означало б назвати покупку авто втраченою вигодою.
	now := time.Now()
	var snap decisionSnapshot
	if op.Amount > 0 {
		snap = s.takeOutsideSnapshot(r.Context(), now)
	}
	id, err := s.st.AddGoalOp(r.Context(), op)
	if err != nil {
		// Ціль, якої немає, — помилка запиту, а не збій: про це каже FK, і
		// 400 із його текстом чесніше за 500.
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if op.Amount > 0 {
		// Ref — НАЗВА цілі, а не її id і не місце зберігання. Журнал читає
		// людина, і «Авто» каже про рішення все, тоді як «3» не каже
		// нічого, а «сейф» відповідає на інше питання. Назва може змінитись
		// потім — рядок журналу лишиться з тією, що була в ту хвилину, і це
		// правильно: він і є знімок моменту.
		s.saveDecision(r.Context(), snap, now, decisionKindGoal, s.goalName(r.Context(), op.GoalID),
			money.New(op.Amount, op.Currency), id)
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// goalName — назва цілі за id, для рядка журналу рішень.
//
// Порожнє значення законне: помилка читання не має валити запис руху
// (рішення — примітка до факту, а не факт; шапка decisions.go), а рядок
// без назви все одно несе суму, дату й альтернативу.
func (s *Server) goalName(ctx context.Context, id int64) string {
	goals, err := s.st.ListGoals(ctx)
	if err != nil {
		return ""
	}
	for _, g := range goals {
		if g.ID == id {
			return g.Name
		}
	}
	return ""
}

func (s *Server) handleUpdateGoalOp(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req goalOpReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op, err := goalOpFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op.ID = id
	if err := s.st.UpdateGoalOp(r.Context(), op); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteGoalOp(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteGoalOp(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusInternalServerError)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListGoalOps(w http.ResponseWriter, r *http.Request) {
	ops, err := s.st.ListGoalOps(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type opJSON struct {
		ID     int64     `json:"id"`
		GoalID int64     `json:"goal_id"`
		Date   string    `json:"date"`
		Amount moneyJSON `json:"amount"`
		Place  string    `json:"place"`
		Note   string    `json:"note"`
	}
	out := make([]opJSON, 0, len(ops))
	for _, op := range ops {
		out = append(out, opJSON{op.ID, op.GoalID, string(op.Date),
			toMoneyJSON(money.New(op.Amount, op.Currency)), op.Place, op.Note})
	}
	writeJSON(w, http.StatusOK, out)
}
