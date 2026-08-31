// Борги: сам борг, журнал рухів і звірки з банком.
//
// ТРИ СУТНОСТІ — ТРИ НАБОРИ РУЧОК, і звести їх не можна навіть попарно.
// Борг заводиться порожнім (умови картки відомі з договору ще до першої
// покупки) і живе далі сам; рух не має сенсу без боргу; а звірка — це
// взагалі не рух, а виміряний ЗАЛИШОК, і складати її з рухами означало б
// порахувати ту саму суму двічі.
//
// Окремо від handlers_goals.go з того самого доводу, що розводить самі
// таблиці (0045): у цілі питають «чи встигну», у боргу — «скільки він
// коштує й коли скінчиться», і ставки, пільгового циклу й двох порогів у
// цілі немає за визначенням.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

type debtReq struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Currency string `json:"currency"`
	CardID   string `json:"card_id"`
	// --- картка ---
	Limit           string `json:"limit"`
	StatementDay    string `json:"statement_day"`
	APRPct          string `json:"apr_pct"`
	APROverduePct   string `json:"apr_overdue_pct"`
	MinPaymentPct   string `json:"min_payment_pct"`
	MinPaymentFloor string `json:"min_payment_floor"`
	LateFee         string `json:"late_fee"`
	ExitBy          string `json:"exit_by"`
	// --- розстрочка ---
	Principal        string `json:"principal"`
	PaymentsTotal    string `json:"payments_total"`
	FirstPaymentDate string `json:"first_payment_date"`
	FeeMonthPct      string `json:"fee_month_pct"`
	FeeFreeMonths    string `json:"fee_free_months"`

	OpenedDate string `json:"opened_date"`
	ClosedDate string `json:"closed_date"`
	Place      string `json:"place"`
	Note       string `json:"note"`
}

// debtIntField — ціле число рядком. Порожньо = нуль, тобто «не задано»:
// той самий прийом, що в pctToBP.
func debtIntField(s, what string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", what, err)
	}
	return n, nil
}

func debtFromReq(req debtReq) (domain.Debt, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return domain.Debt{}, errors.New("борг без назви: за нею його й шукатимуть")
	}
	kind := strings.TrimSpace(req.Kind)
	if kind != domain.DebtCard && kind != domain.DebtInstallment {
		return domain.Debt{}, fmt.Errorf(
			"невідомий вид боргу %q: буває %q (картка з пільговим циклом) або %q",
			kind, domain.DebtCard, domain.DebtInstallment)
	}
	cur := orUAH(strings.TrimSpace(req.Currency))
	d := domain.Debt{
		Name: name, Kind: kind, Currency: cur,
		Place: strings.TrimSpace(req.Place), Note: req.Note,
	}

	var err error
	for _, f := range []struct {
		raw string
		dst *int64
		lbl string
	}{
		{req.APRPct, &d.APRBp, "ставка"},
		{req.APROverduePct, &d.APROverdueBp, "підвищена ставка"},
		{req.MinPaymentPct, &d.MinPaymentBp, "мінімальний платіж"},
		{req.FeeMonthPct, &d.FeeMonthBp, "комісія"},
	} {
		if *f.dst, err = pctToBP(f.raw); err != nil {
			return domain.Debt{}, fmt.Errorf("%s: %w", f.lbl, err)
		}
		if *f.dst < 0 {
			return domain.Debt{}, fmt.Errorf("%s не буває відʼємною", f.lbl)
		}
	}
	for _, f := range []struct {
		raw string
		dst *int64
		lbl string
	}{
		{req.Limit, &d.LimitAmount, "ліміт"},
		{req.MinPaymentFloor, &d.MinPaymentFloor, "мінімум платежу"},
		{req.LateFee, &d.LateFee, "штраф"},
		{req.Principal, &d.Principal, "тіло"},
	} {
		if strings.TrimSpace(f.raw) == "" {
			continue
		}
		if *f.dst, err = domain.ParseDecimalToMinor(f.raw, cur); err != nil {
			return domain.Debt{}, fmt.Errorf("%s: %w", f.lbl, err)
		}
		if *f.dst < 0 {
			return domain.Debt{}, fmt.Errorf("%s не буває відʼємним", f.lbl)
		}
	}
	for _, f := range []struct {
		raw string
		dst *int64
		lbl string
	}{
		{req.StatementDay, &d.StatementDay, "розрахункова дата"},
		{req.PaymentsTotal, &d.PaymentsTotal, "кількість платежів"},
		{req.FeeFreeMonths, &d.FeeFreeMonths, "місяців без комісії"},
		{req.CardID, &d.CardID, "картка"},
	} {
		if *f.dst, err = debtIntField(f.raw, f.lbl); err != nil {
			return domain.Debt{}, err
		}
	}
	// Порожні дати законні в усіх трьох полях: борг без дати відкриття —
	// звичайний випадок («картка є давно»), непогашений не має дати
	// погашення, а в картки немає й дати першого платежу.
	for _, f := range []struct {
		raw string
		dst *domain.Date
	}{
		{req.FirstPaymentDate, &d.FirstPaymentDate},
		{req.ExitBy, &d.ExitBy},
		{req.OpenedDate, &d.OpenedDate},
		{req.ClosedDate, &d.ClosedDate},
	} {
		if v := strings.TrimSpace(f.raw); v != "" {
			if *f.dst, err = domain.ParseDate(v); err != nil {
				return domain.Debt{}, err
			}
		}
	}

	if err := checkDebtShape(d, domain.NewDate(time.Now())); err != nil {
		return domain.Debt{}, err
	}
	return d, nil
}

// checkDebtShape відхиляє поєднання, які представимі в таблиці й не
// означають нічого.
//
// Перевірка тут, а не в CHECK міграції, бо вона про ЗВʼЯЗОК полів, а не
// про кожне окремо: SQLite вміє й такі CHECK, але помилка звідти прийшла б
// текстом рушія, а не реченням, яке можна показати людині.
func checkDebtShape(d domain.Debt, today domain.Date) error {
	// ПОГАШЕНО НАПЕРЕД НЕ БУВАЄ, і це не формальність.
	//
	// Спіймано на живих даних: власник завів картку й поставив у це поле
	// дату, до якої треба ВНЕСТИ — найприроднішим чином, бо іншого поля з
	// датою на картці немає взагалі (дата платежу виводиться з
	// розрахункового числа). Борг миттєво став закритим і зник ЗВІДУСІХ
	// чисел одразу: із черги погашення, з пільгового блоку, з обовʼязкових
	// платежів місяця й із документа стану. Екран при цьому виглядав так,
	// ніби застосунок просто не взяв дані.
	//
	// Мовчазне зникнення — найгірший спосіб відповісти на помилку вводу,
	// тож тепер це відмова з поясненням. Сьогоднішня дата законна: борг,
	// закритий сьогодні, справді закритий.
	if d.ClosedDate != "" && d.ClosedDate.After(today) {
		return errors.New("«Погашено» — це дата, коли борг ЗАКРИТО, і наперед її не ставлять. " +
			"Дату найближчого платежу застосунок рахує сам із розрахункового числа картки; " +
			"якщо борг ще живий — лиши поле порожнім")
	}
	if d.IsCard() {
		if d.CardID != 0 {
			return errors.New("картка не може лежати всередині картки")
		}
		// Розрахункова дата — одне з двох, без чого картка не рахується
		// взагалі: без неї немає ні пільгового порогу, ні дати, ні
		// «скільки вільно».
		if d.StatementDay < 1 || d.StatementDay > 31 {
			return errors.New("розрахункова дата картки — число місяця від 1 до 31 (у ПУМБ це 10, 20 або 30)")
		}
		// Друге — ставка. Без неї сторінка перетворюється на арифметику ні
		// про що: мінімальний платіж нуль, ціна обох помилок нуль, а в
		// черзі погашення борг стоїть із нульовою ставкою, тобто ОСТАННІМ —
		// застосунок радив би не поспішати гасити те, що коштує сорок сім
		// відсотків річних. Впевнено хибне число гірше за відмову.
		if d.APRBp <= 0 {
			return errors.New("картці потрібна ставка після пільгового періоду — " +
				"без неї немає ні мінімального платежу, ні ціни помилки, ні місця в черзі погашення " +
				"(у договорі ПУМБ це «річна ставка», зазвичай 47,88% або 35,88%)")
		}
		// Ціль виходу в минулому не питання, а спогад: «чи встигну» до
		// дати, яка минула, не ставлять. Порожньо лишається законним — це
		// й означає «режиму виходу немає».
		if d.ExitBy != "" && !d.ExitBy.After(today) {
			return errors.New("«вийти в нуль до» — це майбутня дата: " +
				"до вчорашньої встигати вже нема куди")
		}
		return nil
	}
	if d.ExitBy != "" {
		return errors.New("вихід із ліміту буває лише в картки: " +
			"у розстрочки є свій графік, і «вийти» з неї означає доплатити решту тіла")
	}
	if d.PaymentsTotal <= 0 {
		return errors.New("у розстрочки має бути кількість платежів")
	}
	if d.Principal <= 0 {
		return errors.New("у розстрочки має бути тіло — сума, яку розтягнули")
	}
	if d.FirstPaymentDate == "" {
		return errors.New("у розстрочки має бути дата першого платежу: від неї рахується весь графік")
	}
	if d.FeeFreeMonths > d.PaymentsTotal {
		return errors.New("місяців без комісії більше, ніж платежів узагалі")
	}
	return nil
}

// checkDebtParent — картка, до якої чіпляють розстрочку, мусить існувати,
// бути КАРТКОЮ і бути в тій самій валюті.
//
// FK перевіряє лише перше. Друге й третє важать тому, що частина
// розстрочки падає у виписку картки й бере участь у пільговому порозі:
// розстрочка всередині розстрочки не має де падати, а долар усередині
// гривневої виписки зробив би поріг сумою двох різних одиниць.
func (s *Server) checkDebtParent(r *http.Request, d domain.Debt) error {
	if d.CardID == 0 {
		return nil
	}
	debts, err := s.st.ListDebts(r.Context())
	if err != nil {
		return err
	}
	for _, p := range debts {
		if p.ID != d.CardID {
			continue
		}
		if !p.IsCard() {
			return fmt.Errorf("%q — не картка, до неї не можна чіпляти розстрочку", p.Name)
		}
		if p.Currency != d.Currency {
			return fmt.Errorf("розстрочка в %s, а картка %q в %s — виписка складалась би з двох одиниць",
				d.Currency, p.Name, p.Currency)
		}
		return nil
	}
	return fmt.Errorf("картки %d немає", d.CardID)
}

func (s *Server) handleAddDebt(w http.ResponseWriter, r *http.Request) {
	var req debtReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d, err := debtFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.checkDebtParent(r, d); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddDebt(r.Context(), d)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdateDebt(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req debtReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d, err := debtFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d.ID = id
	// Борг не може бути власною карткою: FK такого не ловить (рядок існує),
	// а цикл зробив би обхід списку нескінченним.
	if d.CardID == id {
		writeErr(w, http.StatusBadRequest, errors.New("борг не може лежати сам у собі"))
		return
	}
	if err := s.checkDebtParent(r, d); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.UpdateDebt(r.Context(), d); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteDebt — видалити можна лише борг без рухів, звірок і
// привʼязаних розстрочок; відмова приходить зі сховища зі своєю причиною
// (debts.go). 400, а не 500: це не збій, а відповідь «так не можна, і ось
// чому».
func (s *Server) handleDeleteDebt(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteDebt(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// handleListDebts віддає СИРІ борги, без стану картки й без ставки.
//
// Пораховані числа — залишок, два пороги, «вільно», ефективна ставка —
// живуть у /api/summary і /api/payoff, і другого їх джерела тут бути не
// мусить (CLAUDE.md §5). Цей список потрібен формам правки: їм треба те,
// що ввів користувач, а не те, що з нього вивелось.
func (s *Server) handleListDebts(w http.ResponseWriter, r *http.Request) {
	debts, err := s.st.ListDebts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type debtJSON struct {
		ID              int64     `json:"id"`
		Name            string    `json:"name"`
		Kind            string    `json:"kind"`
		Currency        string    `json:"currency"`
		CardID          int64     `json:"card_id,omitempty"`
		Limit           moneyJSON `json:"limit,omitempty"`
		StatementDay    int64     `json:"statement_day,omitempty"`
		APRPct          float64   `json:"apr_pct,omitempty"`
		APROverduePct   float64   `json:"apr_overdue_pct,omitempty"`
		MinPaymentPct   float64   `json:"min_payment_pct,omitempty"`
		MinPaymentFloor moneyJSON `json:"min_payment_floor,omitempty"`
		LateFee         moneyJSON `json:"late_fee,omitempty"`
		ExitBy          string    `json:"exit_by,omitempty"`

		Principal        moneyJSON `json:"principal,omitempty"`
		PaymentsTotal    int64     `json:"payments_total,omitempty"`
		FirstPaymentDate string    `json:"first_payment_date,omitempty"`
		FeeMonthPct      float64   `json:"fee_month_pct,omitempty"`
		FeeFreeMonths    int64     `json:"fee_free_months,omitempty"`

		OpenedDate string `json:"opened_date,omitempty"`
		ClosedDate string `json:"closed_date,omitempty"`
		Place      string `json:"place,omitempty"`
		Note       string `json:"note,omitempty"`
	}
	out := make([]debtJSON, 0, len(debts))
	for _, d := range debts {
		out = append(out, debtJSON{
			ID: d.ID, Name: d.Name, Kind: d.Kind, Currency: d.Currency,
			CardID:          d.CardID,
			Limit:           toMoneyJSON(money.New(d.LimitAmount, d.Currency)),
			StatementDay:    d.StatementDay,
			APRPct:          float64(d.APRBp) / 100,
			APROverduePct:   float64(d.APROverdueBp) / 100,
			MinPaymentPct:   float64(d.MinPaymentBp) / 100,
			MinPaymentFloor: toMoneyJSON(money.New(d.MinPaymentFloor, d.Currency)),
			LateFee:         toMoneyJSON(money.New(d.LateFee, d.Currency)),
			ExitBy:          string(d.ExitBy),

			Principal:        toMoneyJSON(money.New(d.Principal, d.Currency)),
			PaymentsTotal:    d.PaymentsTotal,
			FirstPaymentDate: string(d.FirstPaymentDate),
			FeeMonthPct:      float64(d.FeeMonthBp) / 100,
			FeeFreeMonths:    d.FeeFreeMonths,

			OpenedDate: string(d.OpenedDate),
			ClosedDate: string(d.ClosedDate),
			Place:      d.Place, Note: d.Note,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type debtOpReq struct {
	DebtID string `json:"debt_id"`
	Date   string `json:"date"`
	Kind   string `json:"kind"`
	Amount string `json:"amount"`
	Note   string `json:"note"`
}

func debtOpFromReq(req debtOpReq, cur string) (domain.DebtOp, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(req.DebtID), 10, 64)
	if err != nil || id <= 0 {
		return domain.DebtOp{}, errors.New("рух без боргу: вкажи, до якого він іде")
	}
	kind := strings.TrimSpace(req.Kind)
	switch kind {
	case domain.DebtOpPayment, domain.DebtOpDraw, domain.DebtOpCash:
	default:
		return domain.DebtOp{}, fmt.Errorf(
			"невідомий вид руху %q: буває %q (унесено), %q (покупка) або %q (готівка чи переказ)",
			kind, domain.DebtOpPayment, domain.DebtOpDraw, domain.DebtOpCash)
	}
	d := domain.NewDate(time.Now())
	if req.Date != "" {
		if d, err = domain.ParseDate(req.Date); err != nil {
			return domain.DebtOp{}, err
		}
	}
	minor, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		return domain.DebtOp{}, err
	}
	// Тільки додатна: напрям несе вид, а не знак (правило 0025). Відʼємна
	// сума при вигляді payment означала б «унесення навпаки», тобто те
	// саме, що draw, — і два записи означали б одне.
	if minor <= 0 {
		return domain.DebtOp{}, errors.New("сума руху має бути більшою за нуль: напрям задає вид руху, а не знак")
	}
	return domain.DebtOp{DebtID: id, Date: d, Kind: kind, Amount: minor,
		Note: strings.TrimSpace(req.Note)}, nil
}

// debtCurrency — валюта боргу за id. Валюти в самому русі немає навмисно:
// рух під гривневою карткою в доларах не означає нічого, а поле для нього
// зробило б таку комбінацію представимою.
func (s *Server) debtCurrency(r *http.Request, id int64) (string, error) {
	debts, err := s.st.ListDebts(r.Context())
	if err != nil {
		return "", err
	}
	for _, d := range debts {
		if d.ID == id {
			return d.Currency, nil
		}
	}
	return "", fmt.Errorf("боргу %d немає", id)
}

// debtChild читає id боргу з тіла запиту ще до розбору сум: валюта суми
// відома лише з боргу, а боргу — лише з тіла.
func debtChildID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("не вказано, до якого боргу це належить")
	}
	return id, nil
}

func (s *Server) handleAddDebtOp(w http.ResponseWriter, r *http.Request) {
	var req debtOpReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := debtChildID(req.DebtID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cur, err := s.debtCurrency(r, id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op, err := debtOpFromReq(req, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	newID, err := s.st.AddDebtOp(r.Context(), op)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": newID})
}

func (s *Server) handleUpdateDebtOp(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req debtOpReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	debtID, err := debtChildID(req.DebtID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cur, err := s.debtCurrency(r, debtID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op, err := debtOpFromReq(req, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op.ID = id
	if err := s.st.UpdateDebtOp(r.Context(), op); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteDebtOp(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteDebtOp(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusInternalServerError)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListDebtOps(w http.ResponseWriter, r *http.Request) {
	debts, err := s.st.ListDebts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	cur := map[int64]string{}
	for _, d := range debts {
		cur[d.ID] = d.Currency
	}
	ops, err := s.st.ListDebtOps(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type opJSON struct {
		ID     int64     `json:"id"`
		DebtID int64     `json:"debt_id"`
		Date   string    `json:"date"`
		Kind   string    `json:"kind"`
		Amount moneyJSON `json:"amount"`
		Note   string    `json:"note,omitempty"`
	}
	out := make([]opJSON, 0, len(ops))
	for _, op := range ops {
		out = append(out, opJSON{op.ID, op.DebtID, string(op.Date), op.Kind,
			toMoneyJSON(money.New(op.Amount, orUAH(cur[op.DebtID]))), op.Note})
	}
	writeJSON(w, http.StatusOK, out)
}

type debtMarkReq struct {
	DebtID string `json:"debt_id"`
	Date   string `json:"date"`
	// Balance знакозмінний: плюс — власні гроші на картці, мінус —
	// використаний ліміт. Мінус тут не помилка вводу, а нормальний стан
	// половини місяця.
	Balance      string `json:"balance"`
	StatementDue string `json:"statement_due"`
	NonGrace     string `json:"non_grace"`
	Note         string `json:"note"`
}

func debtMarkFromReq(req debtMarkReq, cur string) (domain.DebtMark, error) {
	id, err := debtChildID(req.DebtID)
	if err != nil {
		return domain.DebtMark{}, err
	}
	d := domain.NewDate(time.Now())
	if req.Date != "" {
		if d, err = domain.ParseDate(req.Date); err != nil {
			return domain.DebtMark{}, err
		}
	}
	m := domain.DebtMark{DebtID: id, Date: d, Note: strings.TrimSpace(req.Note)}
	if m.Balance, err = domain.ParseDecimalToMinor(req.Balance, cur); err != nil {
		return domain.DebtMark{}, fmt.Errorf("баланс: %w", err)
	}
	for _, f := range []struct {
		raw string
		dst *int64
		lbl string
	}{
		{req.StatementDue, &m.StatementDue, "сума до сплати"},
		{req.NonGrace, &m.NonGrace, "поза пільговим"},
	} {
		if strings.TrimSpace(f.raw) == "" {
			continue
		}
		if *f.dst, err = domain.ParseDecimalToMinor(f.raw, cur); err != nil {
			return domain.DebtMark{}, fmt.Errorf("%s: %w", f.lbl, err)
		}
		// Обидва — розміри БОРГУ, а не балансу, тож знак у них один.
		// Відʼємна «сума до сплати» означала б, що банк винен тобі, і
		// перетворила б поріг на премію.
		if *f.dst < 0 {
			return domain.DebtMark{}, fmt.Errorf("%s не буває відʼємною", f.lbl)
		}
	}
	if m.NonGrace > m.StatementDue && m.StatementDue > 0 {
		return domain.DebtMark{}, errors.New(
			"поза пільговим більше, ніж уся сума до сплати — перевір, що з чого списував")
	}
	return m, nil
}

func (s *Server) handleAddDebtMark(w http.ResponseWriter, r *http.Request) {
	var req debtMarkReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := debtChildID(req.DebtID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cur, err := s.debtCurrency(r, id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	m, err := debtMarkFromReq(req, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	newID, err := s.st.AddDebtMark(r.Context(), m)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": newID})
}

func (s *Server) handleUpdateDebtMark(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req debtMarkReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	debtID, err := debtChildID(req.DebtID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cur, err := s.debtCurrency(r, debtID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	m, err := debtMarkFromReq(req, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	m.ID = id
	if err := s.st.UpdateDebtMark(r.Context(), m); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteDebtMark(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteDebtMark(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusInternalServerError)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListDebtMarks(w http.ResponseWriter, r *http.Request) {
	debts, err := s.st.ListDebts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	cur := map[int64]string{}
	for _, d := range debts {
		cur[d.ID] = d.Currency
	}
	marks, err := s.st.ListDebtMarks(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type markJSON struct {
		ID           int64     `json:"id"`
		DebtID       int64     `json:"debt_id"`
		Date         string    `json:"date"`
		Balance      moneyJSON `json:"balance"`
		StatementDue moneyJSON `json:"statement_due,omitempty"`
		NonGrace     moneyJSON `json:"non_grace,omitempty"`
		Note         string    `json:"note,omitempty"`
	}
	out := make([]markJSON, 0, len(marks))
	for _, m := range marks {
		c := orUAH(cur[m.DebtID])
		out = append(out, markJSON{m.ID, m.DebtID, string(m.Date),
			toMoneyJSON(money.New(m.Balance, c)),
			toMoneyJSON(money.New(m.StatementDue, c)),
			toMoneyJSON(money.New(m.NonGrace, c)), m.Note})
	}
	writeJSON(w, http.StatusOK, out)
}
