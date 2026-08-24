// Лоти ОВДП: купівля, правка, видалення, список.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

type lotJSON struct {
	ID        int64     `json:"id"`
	ISIN      string    `json:"isin"`
	Qty       int64     `json:"qty"`
	Remaining int64     `json:"remaining"`
	Price     moneyJSON `json:"price_per_bond"`
	Fee       moneyJSON `json:"fee"`
	BuyDate   string    `json:"buy_date"`
	Channel   string    `json:"channel"`
	Note      string    `json:"note"`
}

type lotReq struct {
	ISIN     string `json:"isin"`
	Qty      int64  `json:"qty"`
	Price    string `json:"price_per_bond"` // десятковий рядок
	Currency string `json:"currency"`
	Fee      string `json:"fee"` // сумарна комісія за лот, десятковий рядок; опційно
	BuyDate  string `json:"buy_date"`
	Channel  string `json:"channel"`
	Note     string `json:"note"`
}

// lotFromReq — розбір тіла запиту в лот. Спільний для POST і PUT: інакше
// дві копії правил (валюта з довідника, комісія, дата) неминуче розійшлись
// би, і редагування почало б поводитись інакше за створення.
func (s *Server) lotFromReq(r *http.Request, req lotReq) (domain.Lot, error) {
	var out domain.Lot
	if req.Qty <= 0 {
		return out, errors.New("qty має бути > 0")
	}
	bd, err := domain.ParseDate(req.BuyDate)
	if err != nil {
		return out, err
	}
	cur := req.Currency
	if cur == "" { // валюту беремо з довідника, якщо папір відомий
		if b, _ := s.st.GetBond(r.Context(), req.ISIN); b != nil { //nolint:errcheck // папір може бути невідомий — валюта тоді береться з запиту
			cur = b.Nominal.Currency().Code
		} else {
			return out, errors.New("папір не в довіднику — вкажіть currency явно")
		}
	}
	price, err := parseMoney(req.Price, cur)
	if err != nil {
		return out, err
	}
	var fee *money.Money
	if strings.TrimSpace(req.Fee) != "" {
		if fee, err = parseMoney(req.Fee, cur); err != nil {
			return out, err
		}
	}
	return domain.Lot{ISIN: req.ISIN, Qty: req.Qty, PricePerBond: price, Fee: fee,
		BuyDate: bd, Channel: req.Channel, Note: req.Note}, nil
}

func (s *Server) handleAddLot(w http.ResponseWriter, r *http.Request) {
	var req lotReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	lot, err := s.lotFromReq(r, req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Знімок рейтингу — ДО запису: після нього частки вже зрушені, і
	// папір, що стояв першим, опинився б п'ятим (шапка decisions.go).
	now := time.Now()
	snap := s.takeDecisionSnapshot(r.Context(), now, store.BuyBond, lot.ISIN)
	id, err := s.st.AddLot(r.Context(), lot)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if cost, cerr := domain.LotCost(lot); cerr == nil {
		s.saveDecision(r.Context(), snap, now, store.BuyBond, lot.ISIN, cost, id)
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// handleLotCheck — POST /api/lots/check: чи вистачить у брокера грошей
// на цей лот і скільки бракує.
//
// Тіло — той самий lotReq, що й у POST /api/lots, і розбирає його той
// самий lotFromReq: перевіряти треба рівно те, що потім запишуть.
// Вартість тут не введена, а виведена з кількості, ціни й комісії — саме
// тому лоту потрібен власний /check, а не спільний «скільки бракує на
// суму N».
func (s *Server) handleLotCheck(w http.ResponseWriter, r *http.Request) {
	var req lotReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	lot, err := s.lotFromReq(r, req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d, err := lotDebit(lot)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.writeCashCheck(w, r, d)
}

// handleUpdateLot — правка лота БЕЗ видалення. Видалити й створити заново
// не те саме: продажі посилаються на лот через lot_id, тож така «правка»
// осиротила б історію продажів.
func (s *Server) handleUpdateLot(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req lotReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	lot, err := s.lotFromReq(r, req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	lot.ID = id
	// Не даємо зменшити кількість нижче вже проданої — інакше залишок
	// став би від'ємним, а календар виплат порахувався б на мінус.
	sales, err := s.st.ListSales(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var sold int64
	for _, sl := range sales {
		if sl.LotID == id {
			sold += sl.Qty
		}
	}
	if lot.Qty < sold {
		writeErr(w, http.StatusBadRequest,
			fmt.Errorf("з лота вже продано %d паперів — кількість не може бути меншою", sold))
		return
	}
	if err := s.st.UpdateLot(r.Context(), lot); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteLot(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteLot(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusInternalServerError)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListLots(w http.ResponseWriter, r *http.Request) {
	lots, err := s.st.ListLots(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	sales, err := s.st.ListSales(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]lotJSON, 0, len(lots))
	for _, l := range lots {
		out = append(out, lotJSON{l.ID, l.ISIN, l.Qty, domain.RemainingQtyNow(l, sales),
			toMoneyJSON(l.PricePerBond), toMoneyJSON(l.Fee), string(l.BuyDate), l.Channel, l.Note})
	}
	writeJSON(w, http.StatusOK, out)
}
