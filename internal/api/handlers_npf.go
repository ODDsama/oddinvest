// Пенсійні рахунки, внески й точки ЧВОПА.
//
// Три ресурси, бо три різні речі: довідник рахунку (властивості, які
// задаються раз), журнал внесків (факти з виписки) і історія ЧВОПА
// (опублікована фондом таблиця, яку вклеюють пачкою).
//
// Чого тут немає: продажу, дивіденда й дострокового виходу. У НПФ їх не
// існує, і приймати їх «на майбутнє» означало б дозволити записати те, чого
// модель не вміє порахувати.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// npfScaleDigits — знаків після коми в одиницях і ЧВОПА. Шість, як у
// виписці фонду; див. domain/npf.go про те, чому не чотири.
const npfScaleDigits = 6

type npfAccountReq struct {
	Name             string `json:"name"`
	Administrator    string `json:"administrator"`
	Currency         string `json:"currency"`
	Nav              string `json:"nav"`
	NavDate          string `json:"nav_date"`
	ExpectedYieldPct string `json:"expected_yield_pct"`
	YieldSimpleYears int64  `json:"yield_simple_years"`
	AccessDate       string `json:"access_date"`
	IncomeTaxPct     string `json:"income_tax_pct"`
	CreditRatePct    string `json:"credit_rate_pct"`
	ContribDay       int64  `json:"contrib_day"`
	// Строк і періодичність виплати після дати доступу. Нуль років =
	// разово; порожня періодичність — місяць (див. store).
	PayoutYears int64  `json:"payout_years"`
	PayoutFreq  string `json:"payout_freq"`
	Note        string `json:"note"`
}

// pctToBP — відсоток рядком у базисні пункти. Порожньо = нуль, тобто «не
// задано»: у податку й обіцянки нуль і означає «модель цього не малює».
func pctToBP(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	// Два знаки після коми — саме те, що дає × 100.
	return domain.ParseDecimalToScale(s, 2)
}

func npfAccountFromReq(req npfAccountReq) (domain.NPFAccount, error) {
	var out domain.NPFAccount
	cur := req.Currency
	if cur == "" {
		cur = money.UAH
	}
	var nav int64
	if req.Nav != "" {
		var err error
		if nav, err = domain.ParseDecimalToScale(req.Nav, npfScaleDigits); err != nil {
			return out, err
		}
	}
	exp, err := pctToBP(req.ExpectedYieldPct)
	if err != nil {
		return out, err
	}
	tax, err := pctToBP(req.IncomeTaxPct)
	if err != nil {
		return out, err
	}
	credit, err := pctToBP(req.CreditRatePct)
	if err != nil {
		return out, err
	}
	// Решту перевірок робить сховище: туди ж пише й відновлення бекапу, і
	// подвоєні тут вони розійшлися б (див. шапку store/npf.go).
	return domain.NPFAccount{
		Name: req.Name, Administrator: req.Administrator, Currency: cur,
		Nav: nav, NavDate: domain.Date(req.NavDate),
		ExpectedYieldBP: exp, YieldSimpleYears: req.YieldSimpleYears,
		AccessDate: domain.Date(req.AccessDate), IncomeTaxBP: tax,
		CreditRateBP: credit, ContribDay: req.ContribDay,
		PayoutYears: req.PayoutYears, PayoutFreq: req.PayoutFreq,
		Note: req.Note,
	}, nil
}

func (s *Server) handleNPFAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.st.ListNPFAccounts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type row struct {
		ID               int64   `json:"id"`
		Name             string  `json:"name"`
		Administrator    string  `json:"administrator,omitempty"`
		Currency         string  `json:"currency"`
		Nav              float64 `json:"nav,omitempty"`
		NavDate          string  `json:"nav_date,omitempty"`
		ExpectedYieldPct float64 `json:"expected_yield_pct,omitempty"`
		YieldSimpleYears int64   `json:"yield_simple_years,omitempty"`
		AccessDate       string  `json:"access_date,omitempty"`
		IncomeTaxPct     float64 `json:"income_tax_pct,omitempty"`
		CreditRatePct    float64 `json:"credit_rate_pct,omitempty"`
		ContribDay       int64   `json:"contrib_day,omitempty"`
		PayoutYears      int64   `json:"payout_years,omitempty"`
		PayoutFreq       string  `json:"payout_freq,omitempty"`
		Note             string  `json:"note,omitempty"`
	}
	out := make([]row, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, row{
			ID: a.ID, Name: a.Name, Administrator: a.Administrator,
			Currency: a.Currency,
			Nav:      float64(a.Nav) / 1_000_000, NavDate: string(a.NavDate),
			ExpectedYieldPct: float64(a.ExpectedYieldBP) / 100,
			YieldSimpleYears: a.YieldSimpleYears,
			AccessDate:       string(a.AccessDate),
			IncomeTaxPct:     float64(a.IncomeTaxBP) / 100,
			CreditRatePct:    float64(a.CreditRateBP) / 100,
			ContribDay:       a.ContribDay,
			PayoutYears:      a.PayoutYears, PayoutFreq: a.PayoutFreq,
			Note: a.Note,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddNPFAccount(w http.ResponseWriter, r *http.Request) {
	var req npfAccountReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	acc, err := npfAccountFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddNPFAccount(r.Context(), acc)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdateNPFAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req npfAccountReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	acc, err := npfAccountFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	acc.ID = id
	if err := s.st.UpdateNPFAccount(r.Context(), acc); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteNPFAccount(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteNPFAccount(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusInternalServerError)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// handleSetNPFNav — PUT /api/npf-accounts/{id}/nav: переписати останню
// відому ЧВОПА.
//
// Окремий ресурс, а не PUT усього рахунку: це найчастіша дія («взяв число з
// кабінету»), і вимагати для неї повного тіла означало б дати формі
// перезаписати поля, яких вона не показує.
func (s *Server) handleSetNPFNav(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Nav  string `json:"nav"`
		Date string `json:"date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	nav, err := domain.ParseDecimalToScale(req.Nav, npfScaleDigits)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.SetNPFNav(r.Context(), id, nav, domain.Date(req.Date)); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// --- внески ---

type npfOpReq struct {
	NPFID  int64  `json:"npf_id"`
	Date   string `json:"date"`
	Units  string `json:"units"`
	Amount string `json:"amount"`
	Broker string `json:"broker"`
	Note   string `json:"note"`
}

func npfOpFromReq(req npfOpReq, cur string) (domain.NPFOp, error) {
	var out domain.NPFOp
	d := domain.NewDate(time.Now())
	if req.Date != "" {
		var err error
		if d, err = domain.ParseDate(req.Date); err != nil {
			return out, err
		}
	}
	units, err := domain.ParseDecimalToScale(req.Units, npfScaleDigits)
	if err != nil {
		return out, err
	}
	amount, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		return out, err
	}
	return domain.NPFOp{
		NPFID: req.NPFID, Date: d, Units: units, Amount: amount,
		Broker: req.Broker, Note: req.Note,
	}, nil
}

// npfCurrency — валюта рахунку, у якій розбирати суму внеску. Читається зі
// сховища, а не приймається в тілі: валюта — властивість рахунку, і дати
// формі надіслати іншу означало б дозволити гривневий внесок у доларовий
// фонд без жодної помилки.
func (s *Server) npfCurrency(r *http.Request, npfID int64) (string, error) {
	accounts, err := s.st.ListNPFAccounts(r.Context())
	if err != nil {
		return "", err
	}
	for _, a := range accounts {
		if a.ID == npfID {
			if a.Currency == "" {
				return money.UAH, nil
			}
			return a.Currency, nil
		}
	}
	return "", errors.New("пенсійного рахунку не знайдено")
}

func (s *Server) handleNPFOps(w http.ResponseWriter, r *http.Request) {
	ops, err := s.st.ListNPFOps(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	accounts, err := s.st.ListNPFAccounts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	cur := map[int64]string{}
	for _, a := range accounts {
		c := a.Currency
		if c == "" {
			c = money.UAH
		}
		cur[a.ID] = c
	}
	type row struct {
		ID     int64     `json:"id"`
		NPFID  int64     `json:"npf_id"`
		Date   string    `json:"date"`
		Units  float64   `json:"units"`
		Amount moneyJSON `json:"amount"`
		// Nav — ЧВОПА, за якою пройшов внесок. Віддається ВИВЕДЕНОЮ, а не
		// зберігається: вона завжди сума ÷ одиниці, і окреме поле лише дало б
		// їм розійтись.
		Nav    float64 `json:"nav"`
		Broker string  `json:"broker,omitempty"`
		Note   string  `json:"note,omitempty"`
	}
	out := make([]row, 0, len(ops))
	for _, op := range ops {
		c := cur[op.NPFID]
		if c == "" {
			c = money.UAH
		}
		out = append(out, row{
			ID: op.ID, NPFID: op.NPFID, Date: string(op.Date),
			Units:  float64(op.Units) / 1_000_000,
			Amount: toMoneyJSON(money.New(op.Amount, c)),
			Nav:    float64(op.NavE6()) / 1_000_000,
			Broker: op.Broker, Note: op.Note,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddNPFOp(w http.ResponseWriter, r *http.Request) {
	var req npfOpReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cur, err := s.npfCurrency(r, req.NPFID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op, err := npfOpFromReq(req, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Помічник називає пенсійний рахунок його іменем, а операція знає
	// лише id, тож ім'я доводиться взяти з довідника. Порожнє означає, що
	// рахунок зник між двома запитами — тоді рішення просто не пишеться.
	now := time.Now()
	var snap decisionSnapshot
	name := s.npfAccountName(r.Context(), req.NPFID)
	if name != "" {
		snap = s.takeDecisionSnapshot(r.Context(), now, store.BuyNPF, name)
	}
	id, err := s.st.AddNPFOp(r.Context(), op)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.saveDecision(r.Context(), snap, now, store.BuyNPF, name,
		money.New(op.Amount, cur), id, op.Note)
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// handleNPFOpCheck — POST /api/npf/check: чи вистачить грошей на внесок.
//
// Те саме тіло й той самий розбирач, що в POST /api/npf: перевіряти треба
// рівно те, що потім запишуть (див. шапку cash_shortfall.go).
func (s *Server) handleNPFOpCheck(w http.ResponseWriter, r *http.Request) {
	var req npfOpReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cur, err := s.npfCurrency(r, req.NPFID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op, err := npfOpFromReq(req, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.writeCashCheck(w, r, cashDebit{
		Broker: op.Broker, Currency: cur, Amount: op.Amount,
	})
}

func (s *Server) handleUpdateNPFOp(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req npfOpReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cur, err := s.npfCurrency(r, req.NPFID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op, err := npfOpFromReq(req, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op.ID = id
	if err := s.st.UpdateNPFOp(r.Context(), op); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteNPFOp(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteNPFOp(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusInternalServerError)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// --- точки ЧВОПА ---

func (s *Server) handleNPFNav(w http.ResponseWriter, r *http.Request) {
	pts, err := s.st.ListNPFNav(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type row struct {
		ID    int64   `json:"id"`
		NPFID int64   `json:"npf_id"`
		Date  string  `json:"date"`
		Nav   float64 `json:"nav"`
	}
	out := make([]row, 0, len(pts))
	for _, p := range pts {
		out = append(out, row{ID: p.ID, NPFID: p.NPFID,
			Date: string(p.Date), Nav: float64(p.Nav) / 1_000_000})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAddNPFNav — POST /api/npf-nav: вклеїти пачку точок ЧВОПА.
//
// Масивом, бо саме так їх і беруть: опублікована фондом історія — таблиця
// за багато років, і заводити її по рядку означало б не заводити зовсім.
// Одна транзакція на пачку (див. store): половина вклеєної історії гірша за
// жодну, бо на ній порахувалась би дохідність за відрізок, якого ніхто не
// вибирав.
func (s *Server) handleAddNPFNav(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NPFID  int64 `json:"npf_id"`
		Points []struct {
			Date string `json:"date"`
			Nav  string `json:"nav"`
		} `json:"points"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	pts := make([]domain.NPFNav, 0, len(req.Points))
	for i, p := range req.Points {
		nav, err := domain.ParseDecimalToScale(p.Nav, npfScaleDigits)
		if err != nil {
			writeErr(w, http.StatusBadRequest,
				errors.New("рядок "+strconv.Itoa(i+1)+": "+err.Error()))
			return
		}
		pts = append(pts, domain.NPFNav{
			NPFID: req.NPFID, Date: domain.Date(p.Date), Nav: nav,
		})
	}
	n, err := s.st.AddNPFNavPoints(r.Context(), req.NPFID, pts)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int{"added": n})
}

// handleUpdateNPFNav — PUT /api/npf-nav/{id}: виправити ОДНУ точку.
//
// Поруч із пачковим POST, а не замість нього: заводять історію таблицею за
// багато років, а виправляють одне число, і це два різні жести. Доти був
// лише перший, тож одруківка в одній точці лікувалась видаленням і
// повторним вклеюванням — на найдорожчих за працею даних у базі.
func (s *Server) handleUpdateNPFNav(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Date string `json:"date"`
		Nav  string `json:"nav"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	nav, err := domain.ParseDecimalToScale(req.Nav, npfScaleDigits)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.UpdateNPFNavPoint(r.Context(), domain.NPFNav{
		ID: id, Date: domain.Date(req.Date), Nav: nav,
	}); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteNPFNav(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteNPFNavPoint(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusInternalServerError)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// npfAccountName — ім'я рахунку, яким його називає помічник.
//
// Окремим читанням, а не полем операції: NPFOp тримає id, і дублювати
// ім'я в неї означало б завести другу відповідь на питання «як цей
// рахунок зветься» — ту саму, що вже є в довіднику.
func (s *Server) npfAccountName(ctx context.Context, id int64) string {
	accs, err := s.st.ListNPFAccounts(ctx)
	if err != nil {
		return ""
	}
	for _, a := range accs {
		if a.ID == id {
			return a.Name
		}
	}
	return ""
}
