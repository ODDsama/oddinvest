// Резерв («матрац») — гроші на чорний день.
//
// Окремо від handlers_cash.go навмисно, хоч форма запиту майже та сама:
// там рух коштів НА РАХУНКАХ БРОКЕРІВ, тобто грошей, які чекають на
// вкладення, а тут — грошей, які вкладати не збираються. Злиття цих двох
// журналів зробило б резерв купівельною спроможністю, і помічник
// реінвесту запропонував би купити папір за аварійні гроші.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

type reserveReq struct {
	Date     string `json:"date"`
	Amount   string `json:"amount"` // десятковий; + відклав, − узяв
	Currency string `json:"currency"`
	Place    string `json:"place"`
	Note     string `json:"note"`
	// Loan — це ЗНЯТТЯ є позикою в самого себе (0057): поверну з
	// відсотком, і доти ціль подушки піднята. Діє лише на відʼємній сумі:
	// «позичити, кладучи гроші в подушку» не означає нічого.
	Loan bool `json:"loan"`
	// LoanRatePct — ставка цієї позики. Порожньо = з налаштування
	// reserve_loan_rate_pct. У позику вона йде ЗНІМКОМ, тож пізніша
	// правка налаштування її не переписує.
	LoanRatePct string `json:"loan_rate_pct"`
	LoanDue     string `json:"loan_due"`
	// LoanID — це ПОПОВНЕННЯ гасить названу позику. Порожньо (0) на
	// поповненні не означає «нічого не гасить»: без привʼязки гроші
	// розливаються по відкритих позиках у порядку узяття (довід — шапка
	// state_reserve_loans.go).
	LoanID int64 `json:"loan_id"`
}

func reserveFromReq(req reserveReq) (store.ReserveOp, error) {
	d := domain.NewDate(time.Now())
	if req.Date != "" {
		var err error
		if d, err = domain.ParseDate(req.Date); err != nil {
			return store.ReserveOp{}, err
		}
	}
	cur := req.Currency
	if cur == "" {
		cur = money.UAH
	}
	minor, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		return store.ReserveOp{}, err
	}
	// Нуль відхиляємо: рух на нуль нічого не змінює, але засмічує журнал і
	// зсуває «останній рух» на дату, коли насправді нічого не сталось.
	if minor == 0 {
		return store.ReserveOp{}, errors.New("сума руху не може бути нульовою")
	}
	op := store.ReserveOp{Date: d, Amount: minor, Currency: cur,
		Place: strings.TrimSpace(req.Place), Note: req.Note}
	if minor > 0 {
		op.LoanID = req.LoanID
	}
	return op, nil
}

// reserveLoanFromReq — умови позики для щойно записаного зняття.
//
// Ставка береться з налаштування, коли поле порожнє, і саме ТУТ, а не при
// читанні: у сховище вона лягає знімком, і другий раз її ніхто не питає.
func (s *Server) reserveLoanFromReq(ctx context.Context, req reserveReq, opID int64) (store.ReserveLoan, error) {
	l := store.ReserveLoan{OpID: opID, Note: req.Note}
	bp, err := parsePercentBPOpt(req.LoanRatePct)
	if err != nil {
		return l, err
	}
	if bp > 0 {
		l.RateBP = bp
	} else if strings.TrimSpace(req.LoanRatePct) == "" {
		// Поле не заповнене — ставку бере налаштування. ЯВНИЙ НУЛЬ сюди не
		// потрапляє й дефолтом не підміняється: «позичаю без відсотка» —
		// теж рішення, і мовчки дописувати до нього 12% не можна.
		raw, err := s.st.AllSettings(ctx)
		if err != nil {
			return l, err
		}
		if set := loadSettings(raw); set.ReserveLoanRatePct != nil {
			l.RateBP = int64(math.Round(*set.ReserveLoanRatePct * 100))
		}
	}
	if l.RateBP < 0 {
		return l, errors.New("ставка позики не може бути відʼємною")
	}
	if txt := strings.TrimSpace(req.LoanDue); txt != "" {
		d, err := domain.ParseDate(txt)
		if err != nil {
			return l, err
		}
		l.DueDate = string(d)
	}
	return l, nil
}

func (s *Server) handleAddReserveOp(w http.ResponseWriter, r *http.Request) {
	var req reserveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op, err := reserveFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Знімок рейтингу — ДО запису, тим самим порядком, що й у покупок
	// (див. шапку decisions.go): після нього подушка вже підросла, стеля
	// місяця впала, і «від чого ці гроші відмовились» стало б відповіддю
	// про портфель, у якому вони вже відмовились.
	//
	// ЛИШЕ НА ПОПОВНЕННІ. Зняття з подушки — рішення протилежного знаку,
	// і альтернатива в ньому не при чому: гроші звідти беруть тоді, коли
	// сталось те, заради чого подушку й тримали. Записати таке рядком
	// «відмовився від 9.4%» означало б назвати аварію вибором.
	now := time.Now()
	var snap decisionSnapshot
	if op.Amount > 0 {
		snap = s.takeOutsideSnapshot(r.Context(), now)
	}
	// Умови позики розбираються ДО запису руху: помилка в ставці чи даті
	// має лишити журнал недоторканим, а не зняття без позики, яке потім
	// доведеться шукати руками.
	var loan store.ReserveLoan
	wantLoan := req.Loan && op.Amount < 0
	if wantLoan {
		if loan, err = s.reserveLoanFromReq(r.Context(), req, 0); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	id, err := s.st.AddReserveOp(r.Context(), op)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if wantLoan {
		loan.OpID = id
		if _, err := s.st.AddReserveLoan(r.Context(), loan); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	if op.Amount > 0 {
		s.saveDecision(r.Context(), snap, now, decisionKindReserve, op.Place,
			money.New(op.Amount, op.Currency), id, op.Note)
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdateReserveOp(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req reserveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op, err := reserveFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op.ID = id
	if err := s.st.UpdateReserveOp(r.Context(), op); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteReserveOp(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteReserveOp(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusInternalServerError)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListReserveOps(w http.ResponseWriter, r *http.Request) {
	ops, err := s.st.ListReserveOps(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Які зняття є позиками — щоб журнал не мовчав про те, чого сам не
	// показує: без цього рядок «−12 000» нічим не відрізнявся б від
	// звичайної витрати подушки.
	loans, err := s.st.ListReserveLoans(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	loanOf := map[int64]store.ReserveLoan{}
	for _, l := range loans {
		loanOf[l.OpID] = l
	}
	type opJSON struct {
		ID     int64     `json:"id"`
		Date   string    `json:"date"`
		Amount moneyJSON `json:"amount"`
		Place  string    `json:"place"`
		Note   string    `json:"note"`
		// Позика, відкрита цим зняттям (0 = звичайний рух).
		LoanID      int64   `json:"loan_id,omitempty"`
		LoanRatePct float64 `json:"loan_rate_pct,omitempty"`
		LoanDue     string  `json:"loan_due,omitempty"`
		// Позика, яку гасить це поповнення.
		RepaysLoanID int64 `json:"repays_loan_id,omitempty"`
	}
	out := make([]opJSON, 0, len(ops))
	for _, op := range ops {
		row := opJSON{ID: op.ID, Date: string(op.Date),
			Amount: toMoneyJSON(money.New(op.Amount, op.Currency)),
			Place:  op.Place, Note: op.Note, RepaysLoanID: op.LoanID}
		if l, ok := loanOf[op.ID]; ok {
			row.LoanID, row.LoanRatePct, row.LoanDue = l.ID, float64(l.RateBP)/100, l.DueDate
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

// --- позики в самого себе (0057) ---

// reserveLoanReq — умови позики. Тіла й дати тут немає: вони живуть у
// русі, на якому позика стоїть, і другого їх означення не буде.
type reserveLoanReq struct {
	// OpID — зняття, яке стає позикою. Потрібен лише на створенні: при
	// правці рух не перевішується, інакше задним числом змінились би й
	// сума, і дата.
	OpID    int64  `json:"op_id"`
	RatePct string `json:"rate_pct"`
	DueDate string `json:"due_date"`
	Note    string `json:"note"`
}

func (s *Server) handleAddReserveLoan(w http.ResponseWriter, r *http.Request) {
	var req reserveLoanReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.OpID == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("не названо рух, який стає позикою"))
		return
	}
	// Позикою може стати лише ЗНЯТТЯ. Поповнення, оголошене позикою, не
	// означає нічого: подушка на нього не впала, повертати нічого.
	ops, err := s.st.ListReserveOps(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var found bool
	for _, op := range ops {
		if op.ID == req.OpID {
			found = op.Amount < 0
			break
		}
	}
	if !found {
		writeErr(w, http.StatusBadRequest, errors.New("позикою може стати лише зняття з подушки"))
		return
	}
	loan, err := s.reserveLoanFromReq(r.Context(),
		reserveReq{LoanRatePct: req.RatePct, LoanDue: req.DueDate, Note: req.Note}, req.OpID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddReserveLoan(r.Context(), loan)
	if err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdateReserveLoan(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req reserveLoanReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	loan, err := s.reserveLoanFromReq(r.Context(),
		reserveReq{LoanRatePct: req.RatePct, LoanDue: req.DueDate, Note: req.Note}, 0)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	loan.ID = id
	if err := s.st.UpdateReserveLoan(r.Context(), loan); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteReserveLoan знімає з руху статус позики — сам рух лишається.
// «Це була не позика, а витрата за призначенням» — законне виправлення, і
// платити за нього стертим журналом подушки не треба.
func (s *Server) handleDeleteReserveLoan(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteReserveLoan(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// handleListReserveLoans віддає ВСІ позики, зокрема закриті, — на відміну
// від картки подушки, яка показує лише відкриті.
//
// Різниця навмисна: картка відповідає на «що я винен», а це — журнал, і
// закрита позика в ньому потрібна, щоб її можна було виправити чи зняти
// статус із руху, який позикою не був.
func (s *Server) handleListReserveLoans(w http.ResponseWriter, r *http.Request) {
	loans, err := s.st.ListReserveLoans(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	ops, err := s.st.ListReserveOps(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Залишок рахується у ВЛАСНІЙ валюті позики, а не в гривні: це журнал
	// рядків, а не зведення. Курс тут не потрібен зовсім — і добре, бо
	// журнал мусить читатись і тоді, коли курсу на сьогодні ще немає.
	today := domain.NewDate(time.Now())
	repays := reserveRepays(loans, ops, today)
	type loanJSON struct {
		ID      int64     `json:"id"`
		OpID    int64     `json:"op_id"`
		Date    string    `json:"date"`
		Taken   moneyJSON `json:"taken"`
		RatePct float64   `json:"rate_pct"`
		DueDate string    `json:"due_date,omitempty"`
		Note    string    `json:"note,omitempty"`
		// Owed/Interest — лише у відкритих; закрита каже про себе Closed.
		Owed     moneyJSON `json:"owed,omitempty"`
		Interest moneyJSON `json:"interest,omitempty"`
		Closed   bool      `json:"closed,omitempty"`
	}
	out := make([]loanJSON, 0, len(loans))
	for _, l := range loans {
		row := loanJSON{ID: l.ID, OpID: l.OpID, Date: string(l.TakenDate),
			Taken:   toMoneyJSON(money.New(l.TakenAmount, l.TakenCurrency)),
			RatePct: float64(l.RateBP) / 100, DueDate: l.DueDate, Note: l.Note}
		owed, interest := domain.ReserveLoanBalance(l.TakenAmount, l.RateBP,
			l.TakenDate, repays[l.ID], today)
		row.Closed = owed <= 0
		if !row.Closed {
			row.Owed = toMoneyJSON(money.New(owed, l.TakenCurrency))
			row.Interest = toMoneyJSON(money.New(interest, l.TakenCurrency))
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}
