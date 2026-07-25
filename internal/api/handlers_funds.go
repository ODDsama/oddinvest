// Операції з сертифікатами фондів (купівля, продаж, дивіденди).

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	money "github.com/Rhymond/go-money"
)

type fundOpReq struct {
	Date     string `json:"date"`
	Fund     string `json:"fund"`
	Kind     string `json:"kind"` // buy | sell | dividend
	Qty      int64  `json:"qty"`
	Amount   string `json:"amount"`
	Tax      string `json:"tax"`
	Currency string `json:"currency"`
	Broker   string `json:"broker"`
	Note     string `json:"note"`
}

func fundOpFromReq(req fundOpReq) (domain.FundOp, error) {
	var out domain.FundOp
	d := domain.NewDate(time.Now())
	if req.Date != "" {
		var err error
		if d, err = domain.ParseDate(req.Date); err != nil {
			return out, err
		}
	}
	kind := domain.FundOpKind(strings.TrimSpace(req.Kind))
	switch kind {
	case domain.FundBuy, domain.FundSell, domain.FundDividend:
	default:
		return out, fmt.Errorf("kind має бути buy, sell або dividend, маємо %q", req.Kind)
	}
	if strings.TrimSpace(req.Fund) == "" {
		return out, errors.New("вкажіть фонд")
	}
	cur := req.Currency
	if cur == "" {
		cur = money.UAH
	}
	amount, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		return out, err
	}
	if amount <= 0 {
		return out, errors.New("сума має бути > 0")
	}
	var tax int64
	if strings.TrimSpace(req.Tax) != "" {
		if tax, err = domain.ParseDecimalToMinor(req.Tax, cur); err != nil {
			return out, err
		}
	}
	// Кількість обов'язкова для купівлі й продажу і безглузда для
	// дивіденда: він нараховується на позицію, а не на штуки.
	if kind == domain.FundDividend {
		req.Qty = 0
	} else if req.Qty <= 0 {
		return out, errors.New("кількість сертифікатів має бути > 0")
	}
	return domain.FundOp{Date: d, Fund: strings.TrimSpace(req.Fund), Kind: kind,
		Qty: req.Qty, Amount: amount, Tax: tax, Currency: cur,
		Broker: strings.TrimSpace(req.Broker), Note: req.Note}, nil
}

func (s *Server) handleFundOps(w http.ResponseWriter, r *http.Request) {
	ops, err := s.st.ListFundOps(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type row struct {
		ID     int64     `json:"id"`
		Date   string    `json:"date"`
		Fund   string    `json:"fund"`
		Kind   string    `json:"kind"`
		Qty    int64     `json:"qty,omitempty"`
		Amount moneyJSON `json:"amount"`
		Tax    moneyJSON `json:"tax,omitempty"`
		Broker string    `json:"broker,omitempty"`
		Note   string    `json:"note,omitempty"`
	}
	out := make([]row, 0, len(ops))
	for _, op := range ops {
		out = append(out, row{ID: op.ID, Date: string(op.Date), Fund: op.Fund,
			Kind: string(op.Kind), Qty: op.Qty,
			Amount: toMoneyJSON(money.New(op.Amount, op.Currency)),
			Tax:    toMoneyJSON(money.New(op.Tax, op.Currency)),
			Broker: op.Broker, Note: op.Note})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddFundOp(w http.ResponseWriter, r *http.Request) {
	var req fundOpReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op, err := fundOpFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddFundOp(r.Context(), op)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdateFundOp(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req fundOpReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op, err := fundOpFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op.ID = id
	if err := s.st.UpdateFundOp(r.Context(), op); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteFundOp(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteFundOp(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// --- банківські вклади ---
