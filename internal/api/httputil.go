// Дрібні помічники HTTP-шару: розбір шляху, відповіді, гроші в JSON
// і два читання, які потрібні майже кожному хендлеру.

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	money "github.com/Rhymond/go-money"
)

// pathID — {id} зі шляху. Три обробники розбирали його однаково, а з
// появою PUT стало б шість копій.
func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

type moneyJSON struct {
	Amount   string `json:"amount"` // десятковий рядок "995.00"
	Currency string `json:"currency"`
}

func toMoneyJSON(m *money.Money) moneyJSON {
	if m == nil {
		return moneyJSON{Amount: "0", Currency: money.UAH}
	}
	minor := m.Amount()
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	return moneyJSON{
		Amount:   fmt.Sprintf("%s%d.%02d", sign, minor/100, minor%100),
		Currency: m.Currency().Code,
	}
}

func parseMoney(amount, currency string) (*money.Money, error) {
	minor, err := domain.ParseDecimalToMinor(amount, currency)
	if err != nil {
		return nil, err
	}
	return money.New(minor, currency), nil
}

// portfolio — все, що треба домену, одним заходом.
func (s *Server) portfolio(ctx context.Context) (lots []domain.Lot, sales []domain.Sale,
	bonds map[string]domain.Bond, pays []domain.Payment, err error) {
	lots, err = s.st.ListLots(ctx)
	if err != nil {
		return
	}
	sales, err = s.st.ListSales(ctx)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	var isins []string
	for _, l := range lots {
		if !seen[l.ISIN] {
			seen[l.ISIN] = true
			isins = append(isins, l.ISIN)
		}
	}
	bonds, err = s.st.BondsFor(ctx, isins)
	if err != nil {
		return
	}
	pays, err = s.st.PaymentsFor(ctx, isins)
	return
}

func (s *Server) rates(ctx context.Context) (fx.Rates, error) {
	r := fx.Rates{}
	for _, code := range []string{"USD", "EUR"} {
		v, err := s.st.LatestRate(ctx, code)
		if err != nil {
			return nil, err
		}
		if v > 0 {
			r[code] = v
		}
	}
	return r, nil
}

// --- handlers ---
