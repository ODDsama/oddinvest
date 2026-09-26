// Гроші на рахунках: поповнення/зняття і конвертації валют.
//
// Це саме рух коштів, а не інструмент: банківський вклад живе окремо, у
// handlers_deposits.go (таблиці deposits і term_deposits — різні речі).

package api

import (
	"cmp"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/engine"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

type depositReq struct {
	Date     string `json:"date"`
	Amount   string `json:"amount"` // десятковий; + поповнення, − зняття
	Currency string `json:"currency"`
	Broker   string `json:"broker"`
	Note     string `json:"note"`
}

func depositFromReq(req depositReq) (store.Deposit, error) {
	d := domain.NewDate(time.Now())
	if req.Date != "" {
		var err error
		if d, err = domain.ParseDate(req.Date); err != nil {
			return store.Deposit{}, err
		}
	}
	cur := cmp.Or(req.Currency, money.UAH)
	minor, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		return store.Deposit{}, err
	}
	return store.Deposit{Date: d, Amount: minor, Currency: cur,
		Broker: strings.TrimSpace(req.Broker), Note: req.Note}, nil
}

func (s *Server) handleAddDeposit(w http.ResponseWriter, r *http.Request) {
	var req depositReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	dep, err := depositFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddDeposit(r.Context(), dep)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdateDeposit(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req depositReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	dep, err := depositFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	dep.ID = id
	if err := s.st.UpdateDeposit(r.Context(), dep); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteDeposit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteDeposit(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusInternalServerError)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListDeposits(w http.ResponseWriter, r *http.Request) {
	deps, err := s.st.ListDeposits(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type depJSON struct {
		ID     int64            `json:"id"`
		Date   string           `json:"date"`
		Amount engine.MoneyJSON `json:"amount"`
		Broker string           `json:"broker"`
		Note   string           `json:"note"`
	}
	out := make([]depJSON, 0, len(deps))
	for _, d := range deps {
		out = append(out, depJSON{d.ID, string(d.Date),
			engine.ToMoneyJSON(money.New(d.Amount, d.Currency)), d.Broker, d.Note})
	}
	writeJSON(w, http.StatusOK, out)
}

type conversionReq struct {
	Date         string `json:"date"`
	FromCurrency string `json:"from_currency"`
	FromAmount   string `json:"from_amount"`
	ToCurrency   string `json:"to_currency"`
	ToAmount     string `json:"to_amount"`
	Broker       string `json:"broker"`
	Note         string `json:"note"`
}

func conversionFromReq(req conversionReq) (store.Conversion, error) {
	var out store.Conversion
	d := domain.NewDate(time.Now())
	if req.Date != "" {
		var err error
		if d, err = domain.ParseDate(req.Date); err != nil {
			return out, err
		}
	}
	if req.FromCurrency == req.ToCurrency {
		return out, errors.New("валюти конвертації мають відрізнятись")
	}
	fromMinor, err := domain.ParseDecimalToMinor(req.FromAmount, req.FromCurrency)
	if err != nil {
		return out, err
	}
	toMinor, err := domain.ParseDecimalToMinor(req.ToAmount, req.ToCurrency)
	if err != nil {
		return out, err
	}
	if fromMinor <= 0 || toMinor <= 0 {
		return out, errors.New("суми конвертації мають бути > 0")
	}
	return store.Conversion{Date: d, FromCurrency: req.FromCurrency, FromAmount: fromMinor,
		ToCurrency: req.ToCurrency, ToAmount: toMinor,
		Broker: strings.TrimSpace(req.Broker), Note: req.Note}, nil
}

func (s *Server) handleAddConversion(w http.ResponseWriter, r *http.Request) {
	var req conversionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c, err := conversionFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddConversion(r.Context(), c)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdateConversion(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req conversionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c, err := conversionFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c.ID = id
	if err := s.st.UpdateConversion(r.Context(), c); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteConversion(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteConversion(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusInternalServerError)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListConversions(w http.ResponseWriter, r *http.Request) {
	convs, err := s.st.ListConversions(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type convJSON struct {
		ID     int64            `json:"id"`
		Date   string           `json:"date"`
		From   engine.MoneyJSON `json:"from"`
		To     engine.MoneyJSON `json:"to"`
		Broker string           `json:"broker"`
		Note   string           `json:"note"`
	}
	out := make([]convJSON, 0, len(convs))
	for _, c := range convs {
		out = append(out, convJSON{c.ID, string(c.Date),
			engine.ToMoneyJSON(money.New(c.FromAmount, c.FromCurrency)),
			engine.ToMoneyJSON(money.New(c.ToAmount, c.ToCurrency)), c.Broker, c.Note})
	}
	writeJSON(w, http.StatusOK, out)
}

// cashCheckResp — відповідь усіх трьох /check.
//
// Broker повертає сервер, хоч у двох випадках його ж і прислали: для
// поповнення вкладу банк відомий лише серверу (він у самому вкладі), і
// фронтенд не мусить збирати тіло автопоповнення з двох джерел. Значення
// СИРЕ, зокрема порожнє — показувати порожнє як «—» можна, а класти «—» у
// тіло поповнення не можна: store.AddDeposit заводить брокера за назвою
// (store/refs.go:25), і в довіднику з'явився б брокер на ім'я «—».
type cashCheckResp struct {
	Broker string           `json:"broker"`
	Cost   engine.MoneyJSON `json:"cost"`
	Have   engine.MoneyJSON `json:"have"` // може бути від'ємним
	Short  engine.MoneyJSON `json:"short"`
	Enough bool             `json:"enough"`
}

// writeCashCheck — спільне тіло трьох хендлерів /check. Нічого не пише.
//
// Баланс береться на СЬОГОДНІ — те саме число, яке людина бачить на
// екрані. Для операції, датованої майбутнім, це може виявитись не тим
// балансом, який буде на її дату; свідомо не ускладнюємо, бо це та сама
// позиція, що вже записана в handlers_whatif.go:85-88 — застосунок
// показує наслідки, рішення за людиною.
func (s *Server) writeCashCheck(w http.ResponseWriter, r *http.Request, d engine.CashDebit) {
	doc, err := s.BuildState(r.Context(), time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	short := engine.ShortfallMinor(doc, d.Broker, d.Currency, d.Amount)
	writeJSON(w, http.StatusOK, cashCheckResp{
		Broker: d.Broker,
		Cost:   engine.ToMoneyJSON(money.New(d.Amount, d.Currency)),
		Have:   engine.ToMoneyJSON(money.New(engine.BrokerBalanceMinor(doc, d.Broker, d.Currency), d.Currency)),
		Short:  engine.ToMoneyJSON(money.New(short, d.Currency)),
		Enough: short == 0,
	})
}

// reconcileReq — звірка рахунку: що показує банк чи брокер. Лише факт,
// жодної різниці: її рахує сервер (правило §5 у CLAUDE.md).
type reconcileReq struct {
	Broker   string `json:"broker"`
	Currency string `json:"currency"`
	Actual   string `json:"actual"` // десятковий, як у поповненні
}

type reconcileResp struct {
	ID   int64            `json:"id,omitempty"` // запис поправки; 0 — сходиться
	Diff engine.MoneyJSON `json:"diff"`
}

// handleReconcile — поправка «фактично мінус за записами» звичайним
// поповненням із поміткою, а не окремою сутністю: так розбіжність
// лишається видимою в історії рухів.
//
// ЧОМУ НА СЕРВЕРІ. Доти різницю віднімав браузер від числа зі summary. А
// summary віддається у валюті звітності, і в доларовому вигляді гривневий
// баланс брокера приходив перекладеним (до 25a4a82 — під гривневим
// ключем): фактичні 495 375 ₴ проти «11 227» записали б поправку на
// півмільйона. Тут баланс береться з книжкового документа — того самого,
// що й у /check (engine.BrokerBalanceMinor), — тож поправка не залежить
// від того, як сторінка його показала.
func (s *Server) handleReconcile(w http.ResponseWriter, r *http.Request) {
	var req reconcileReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	broker := strings.TrimSpace(req.Broker)
	cur := cmp.Or(req.Currency, money.UAH)
	actual, err := domain.ParseDecimalToMinor(req.Actual, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	doc, err := s.BuildState(r.Context(), time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	diff := actual - engine.BrokerBalanceMinor(doc, broker, cur)
	out := reconcileResp{Diff: engine.ToMoneyJSON(money.New(diff, cur))}
	if diff == 0 {
		writeJSON(w, http.StatusOK, out)
		return
	}
	note := "звірка: незаписане надходження"
	if diff < 0 {
		note = "звірка: незаписана витрата"
	}
	out.ID, err = s.st.AddDeposit(r.Context(), store.Deposit{
		Date: domain.NewDate(time.Now()), Amount: diff, Currency: cur, Broker: broker, Note: note,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, out)
}
