// Звіти й службові дії: статуси виплат, оновлення довідника, бекап,
// знімки, експорт CSV, XIRR.

package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// plural — українське відмінювання для довідкових підписів.
func plural(n int, one, few, many string) string {
	d, h := n%10, n%100
	switch {
	case d == 1 && h != 11:
		return one
	case d >= 2 && d <= 4 && (h < 10 || h >= 20):
		return few
	default:
		return many
	}
}

func (s *Server) handlePaymentStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ISIN    string `json:"isin"`
		PayDate string `json:"pay_date"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d, err := domain.ParseDate(req.PayDate)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Порожній статус (або "none") — зняти позначку. Той самий маршрут, а
	// не окремий DELETE: календар уже шле сюди isin+дату+статус, і
	// «скасувати» — це просто ще одне значення статусу для UI, яке на
	// боці сервіса означає видалення рядка.
	if req.Status == "" || req.Status == "none" {
		if err := s.st.ClearPaymentStatus(r.Context(), req.ISIN, d); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	} else if err := s.st.SetPaymentStatus(r.Context(), req.ISIN, d, req.Status); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if s.ref == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("refresher недоступний"))
		return
	}
	if err := s.ref.RefreshAll(r.Context()); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleBackupExport віддає повний бекап користувацьких даних як файл.
func (s *Server) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	b, err := s.st.ExportAll(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	b.ExportedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	fname := "oddinvest-backup-" + time.Now().Format("2006-01-02") + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+fname+`"`)
	w.Write(data)
}

// handleBackupImport ЗАМІНЮЄ всі користувацькі дані вмістом бекапу.
func (s *Server) handleBackupImport(w http.ResponseWriter, r *http.Request) {
	var b store.Backup
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("не схоже на бекап: %w", err))
		return
	}
	if err := s.st.ImportAll(r.Context(), &b); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusOK, map[string]any{
		"restored": map[string]int{
			"lots": len(b.Lots), "sales": len(b.Sales), "deposits": len(b.Deposits),
			"conversions": len(b.Conversions), "fund_ops": len(b.FundOps),
			"term_deposits": len(b.TermDeposits), "deposit_topups": len(b.DepositTopups),
			"settings":       len(b.Settings),
			"payment_status": len(b.PaymentStatus), "snapshots": len(b.Snapshots),
		},
	})
}

func (s *Server) handleXIRR(w http.ResponseWriter, r *http.Request) {
	doc, err := s.buildState(r.Context(), time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"xirr_pct": doc.XIRRPct,
		"note":     "залишок оцінено за номіналом; по валютах окремо, без конвертації",
	})
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	snaps, err := s.st.ListSnapshots(r.Context(),
		domain.Date(q.Get("from")), domain.Date(q.Get("to")))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type snapJSON struct {
		Date           string  `json:"date"`
		InvestedUAH    float64 `json:"invested_uah"`
		NominalUAHEq   float64 `json:"nominal_uah_eq"`
		USDSharePct    float64 `json:"usd_share_pct"`
		UninvestedUAH  float64 `json:"uninvested_uah"`
		MonthTargetUAH float64 `json:"month_target_uah"`
		AccountUAH     float64 `json:"account_uah"`
		FundsUAH       float64 `json:"funds_uah"`
		DepositsUAH    float64 `json:"deposits_uah"`
		// FundsCostUAH — за скільки фонди куплені. Разом з invested_uah
		// (облігації) і deposits_uah дає повну собівартість, від якої крива
		// рахує прибуток.
		FundsCostUAH float64 `json:"funds_cost_uah"`
	}
	out := make([]snapJSON, 0, len(snaps))
	for _, sn := range snaps {
		out = append(out, snapJSON{string(sn.Date), float64(sn.InvestedUAH) / 100,
			float64(sn.NominalUAHEq) / 100, float64(sn.USDShareBP) / 100,
			float64(sn.UninvestedUAH) / 100, float64(sn.MonthTargetUAH) / 100,
			float64(sn.AccountUAH) / 100, float64(sn.FundsUAH) / 100,
			float64(sn.DepositsUAH) / 100, float64(sn.FundsCostUAH) / 100})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleExportCSV — рухи за рік для декларації: отримані виплати і
// продажі з реалізованим результатом. Роздільник ';' і UTF-8 BOM —
// щоб україномовний Excel відкривав без танців.
func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	year := r.URL.Query().Get("year")
	if year == "" {
		year = fmt.Sprintf("%d", time.Now().Year())
	}
	ctx := r.Context()
	lots, sales, _, pays, err := s.portfolio(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	past, err := domain.FuturePayments(pays, lots, sales, domain.Date(year+"-01-01"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	today := domain.NewDate(time.Now())

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=oddinvest-%s.csv", year))
	w.Write([]byte{0xEF, 0xBB, 0xBF}) // BOM
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	cw.Write([]string{"тип", "дата", "ISIN", "кількість", "сума", "валюта", "коментар"})

	typeNames := map[domain.PayType]string{
		domain.PayCoupon: "купон", domain.PayRedemption: "погашення",
		domain.PayEarly: "дострокове погашення",
	}
	for _, cf := range past {
		if !strings.HasPrefix(string(cf.Date), year) || cf.Date.After(today) {
			continue
		}
		cw.Write([]string{typeNames[cf.Type], string(cf.Date), cf.ISIN, "",
			toMoneyJSON(cf.Amount).Amount, cf.Amount.Currency().Code, ""})
	}
	lotByID := map[int64]domain.Lot{}
	for _, l := range lots {
		lotByID[l.ID] = l
	}
	for _, sl := range sales {
		if !strings.HasPrefix(string(sl.SaleDate), year) {
			continue
		}
		lot := lotByID[sl.LotID]
		res, err := domain.RealizedResult(lot, sl, pays)
		if err != nil {
			continue
		}
		proceeds, _ := domain.SaleProceeds(sl) //nolint:errcheck // RealizedResult вище вже відсіяв биті продажі
		cw.Write([]string{"продаж", string(sl.SaleDate), lot.ISIN,
			fmt.Sprintf("%d", sl.Qty), toMoneyJSON(proceeds).Amount,
			proceeds.Currency().Code,
			"результат " + toMoneyJSON(res).Amount + " " + res.Currency().Code})
	}
	cw.Flush()
}

// --- довідники брокерів і фондів ---
//
// Досі списку брокерів як сутності не існувало: він жив CSV-рядком у
// налаштуваннях, тож перейменувати брокера означало не зачепити жодного
// лота — назва в записах лишалась старою. Тепер записи тримаються за id,
// і перейменування підхоплюють усі разом.
