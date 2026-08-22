// Позначки ціни сертифіката (0034): чотири ручки над fund_prices.
//
// Аналога PUT /api/npf-accounts/{id}/nav тут свідомо НЕМАЄ. У пенсійного
// рахунку «остання відома ЧВОПА» лежить колонкою в довіднику, і оновлення з
// кабінету переписує саме її; у фонда такої колонки немає й не буде — ціна
// виводиться з журналу й позначок, а окреме поле поруч дало б їм розійтись
// (шапка 0009). Тож «позначити ціну на сьогодні» — це та сама вставка
// точки, лише з одним рядком.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// fundPriceScaleDigits — чотири знаки після коми. Стільки має ціна
// сертифіката, і рівно на цьому масштабі живе FundPosition.LastPrice.
const fundPriceScaleDigits = 4

func (s *Server) handleFundPrices(w http.ResponseWriter, r *http.Request) {
	pts, err := s.st.ListFundPrices(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// fund_id, а не назва: панель у довіднику фільтрує рядки за id, і саме
	// id переживає перейменування фонду. Назва тут була б порожнім швом —
	// єдиний споживач її вже має.
	type row struct {
		ID     int64   `json:"id"`
		FundID int64   `json:"fund_id"`
		Date   string  `json:"date"`
		Price  float64 `json:"price"`
	}
	out := make([]row, 0, len(pts))
	for _, p := range pts {
		out = append(out, row{ID: p.ID, FundID: p.FundID,
			Date: string(p.Date), Price: float64(p.Price) / 10000})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAddFundPrices — POST /api/fund-prices: вклеїти позначки.
//
// Одна ручка на обидві форми — «ціна на сьогодні» й «вклеїти історію».
// Це той самий жест із різною кількістю рядків, і другий маршрут заради
// len(points)==1 був би порожнім швом. Одна транзакція на пачку (див.
// store): половина вклеєної історії гірша за жодну.
func (s *Server) handleAddFundPrices(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FundID int64 `json:"fund_id"`
		Points []struct {
			Date  string `json:"date"`
			Price string `json:"price"`
		} `json:"points"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	pts := make([]domain.FundPrice, 0, len(req.Points))
	for i, p := range req.Points {
		price, err := domain.ParseDecimalToScale(p.Price, fundPriceScaleDigits)
		if err != nil {
			writeErr(w, http.StatusBadRequest,
				errors.New("рядок "+strconv.Itoa(i+1)+": "+err.Error()))
			return
		}
		pts = append(pts, domain.FundPrice{
			FundID: req.FundID, Date: domain.Date(p.Date), Price: price,
		})
	}
	n, err := s.st.AddFundPricePoints(r.Context(), req.FundID, pts)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int{"added": n})
}

// handleUpdateFundPrice — PUT /api/fund-prices/{id}: виправити ОДНУ точку.
//
// Поруч із пачковим POST, а не замість нього: заводять історію таблицею, а
// виправляють одне число, і це два різні жести. Те саме міркування, що для
// точок ЧВОПА.
func (s *Server) handleUpdateFundPrice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Date  string `json:"date"`
		Price string `json:"price"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	price, err := domain.ParseDecimalToScale(req.Price, fundPriceScaleDigits)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.UpdateFundPricePoint(r.Context(), domain.FundPrice{
		ID: id, Date: domain.Date(req.Date), Price: price,
	}); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteFundPrice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteFundPricePoint(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusInternalServerError)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}
