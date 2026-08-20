// Гроші на рахунках: поповнення/зняття і конвертації валют.
//
// Це саме рух коштів, а не інструмент: банківський вклад живе окремо, у
// handlers_deposits.go (таблиці deposits і term_deposits — різні речі).

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
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
	cur := req.Currency
	if cur == "" {
		cur = money.UAH
	}
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
		ID     int64     `json:"id"`
		Date   string    `json:"date"`
		Amount moneyJSON `json:"amount"`
		Broker string    `json:"broker"`
		Note   string    `json:"note"`
	}
	out := make([]depJSON, 0, len(deps))
	for _, d := range deps {
		out = append(out, depJSON{d.ID, string(d.Date),
			toMoneyJSON(money.New(d.Amount, d.Currency)), d.Broker, d.Note})
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
		ID     int64     `json:"id"`
		Date   string    `json:"date"`
		From   moneyJSON `json:"from"`
		To     moneyJSON `json:"to"`
		Broker string    `json:"broker"`
		Note   string    `json:"note"`
	}
	out := make([]convJSON, 0, len(convs))
	for _, c := range convs {
		out = append(out, convJSON{c.ID, string(c.Date),
			toMoneyJSON(money.New(c.FromAmount, c.FromCurrency)),
			toMoneyJSON(money.New(c.ToAmount, c.ToCurrency)), c.Broker, c.Note})
	}
	writeJSON(w, http.StatusOK, out)
}
