// Звіти й службові дії: статуси виплат, оновлення довідника, бекап,
// знімки, експорт CSV, XIRR.

package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	money "github.com/Rhymond/go-money"

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
			"funds": len(b.Funds), "brokers": len(b.Brokers),
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
	// Підпис переписаний: «без конвертації» перестало бути правдою, щойн
	// поруч зʼявилось total — воно якраз конвертоване, за курсом на дату
	// кожного руху. Стара фраза була б рівно тим тихим невідповідністю
	// між назвою й змістом, від якої лікують решта цих коментарів.
	writeJSON(w, http.StatusOK, map[string]any{
		"xirr_pct": doc.XIRRPct,
		"total":    doc.TotalReturn,
		"note": "залишок оцінено за номіналом; xirr_pct — по валютах у них самих, " +
			"total — усе разом у гривні за курсом на дату кожного руху",
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
	// Рядок будується з реєстру колонок (store/snapshot.go), а не
	// неіменованим літералом на десять float64 поспіль: у такому літералі
	// кількість перевіряє компілятор, а порядок — ніхто, тож сусідні поля
	// можна поміняти місцями й отримати правильний ключ із чужим числом.
	//
	// Ділення на 100 однакове для всіх: гроші йдуть з мінорних у мажорні,
	// а частка — з базисних пунктів у відсотки. Збіг зручний, але саме
	// збіг, тож якщо колись з'явиться колонка в інших одиницях — їй
	// знадобиться свій дільник, і це місце доведеться розділити.
	//
	// apiName — єдине розходження між назвою колонки й ключем відповіді:
	// у БД зберігаються базисні пункти, а віддаються відсотки.
	apiName := map[string]string{"usd_share_bp": "usd_share_pct"}
	cols := store.SnapshotColumns()
	out := make([]map[string]any, 0, len(snaps))
	for i := range snaps {
		row := make(map[string]any, len(cols)+1)
		row["date"] = string(snaps[i].Date)
		for _, c := range cols {
			name := c
			if alt, ok := apiName[c]; ok {
				name = alt
			}
			row[name] = float64(store.SnapshotValue(&snaps[i], c)) / 100
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleExportCSV — усі оподатковувані рухи за рік, одним файлом, який
// можна нести до декларації. Роздільник ';' і UTF-8 BOM — щоб
// україномовний Excel відкривав без танців.
//
// Період — через taxYear (taxyear.go), той самий, що й у /api/tax.
// Курс — через asOfRates (fx_asof.go), теж той самий: на дату події, а не
// сьогоднішній.
//
// ІНВАРІАНТ, який робить розходження неповторюваним: сума колонки
// «податок_грн» тут мусить дорівнювати tax_uah з /api/tax за той самий
// період. Обидва маршрути читають ті самі події, тим самим вікном і тим
// самим курсом — і саме тому на це є тест (TestCSVTaxMatchesTaxEndpoint),
// а не лише сподівання. Це та сама роль, яку в бекенді грає
// TestCashflowStatementReconciles для зведення й руху коштів.
//
// Доти файл містив лише виплати й продажі, без жодного податку: дивіденди
// фондів і відсотки вкладів — тобто ЄДИНЕ, з чого податок реально
// утримують, — у нього не потрапляли зовсім.
func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	yearNum, from, to, err := taxYear(r.URL.Query(), time.Now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	name := fmt.Sprintf("%d", yearNum)
	if yearNum == 0 {
		name = string(from) + "_" + string(to)
	}
	ctx := r.Context()
	lots, sales, _, pays, err := s.portfolio(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	past, err := domain.FuturePayments(pays, lots, sales, from)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	fundOps, err := s.st.ListFundOps(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	termDeposits, err := s.st.ListTermDeposits(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	statuses, err := s.st.PaymentStatuses(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	today := domain.NewDate(time.Now())
	inWindow := func(d domain.Date) bool { return !d.Before(from) && !d.After(to) }
	arrived := domain.Arrived(statuses, today)
	asOf := newAsOfRates(s.st)

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=oddinvest-%s.csv", name))
	w.Write([]byte{0xEF, 0xBB, 0xBF}) // BOM
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	cw.Write([]string{"тип", "дата", "ISIN", "назва", "кількість", "сума",
		"валюта", "курс", "сума_грн", "податок_грн", "коментар"})

	// dec — числа для Excel із КРАПКОЮ: кома вже роздільник колонок, і
	// українська локаль тут зіграла б проти нас.
	dec := func(minor int64) string { return fmt.Sprintf("%.2f", float64(minor)/100) }
	// rate — курс, за яким рядок переведено. Порожньо для гривні: писати
	// «1.0000» означало б удавати конвертацію, якої не було.
	rate := func(m *money.Money, on domain.Date) string {
		if m == nil || m.Currency().Code == money.UAH {
			return ""
		}
		e4, rerr := asOf.rate(ctx, m.Currency().Code, on)
		if rerr != nil || e4 <= 0 {
			return ""
		}
		return fmt.Sprintf("%.4f", float64(e4)/10000)
	}
	row := func(kind string, d domain.Date, isin, label, qty string,
		amt *money.Money, taxUAH int64, note string) {
		uah, uerr := asOf.uah(ctx, amt, d)
		if uerr != nil {
			return
		}
		cur := ""
		amount := ""
		if amt != nil {
			cur = amt.Currency().Code
			amount = toMoneyJSON(amt).Amount
		}
		cw.Write([]string{kind, string(d), isin, label, qty, amount, cur,
			rate(amt, d), dec(uah), dec(taxUAH), note})
	}

	typeNames := map[domain.PayType]string{
		domain.PayCoupon: "купон", domain.PayRedemption: "погашення",
		domain.PayEarly: "дострокове погашення",
	}
	// ОВДП: податку немає за законом, тож нуль у колонці — не пропуск.
	for _, cf := range past {
		if !inWindow(cf.Date) || cf.Date.After(today) {
			continue
		}
		note := ""
		if cf.Type != domain.PayRedemption && !arrived(cf.ISIN, cf.Date) {
			// Те, що ще не позначене отриманим, у /api/tax не рахується.
			// Лишаємо рядок у файлі — але з поміткою, щоб різниця з
			// карткою не виглядала розходженням.
			note = "не позначено отриманим"
		}
		row(typeNames[cf.Type], cf.Date, cf.ISIN, "", "", cf.Amount, 0, note)
	}

	lotByID := map[int64]domain.Lot{}
	for _, l := range lots {
		lotByID[l.ID] = l
	}
	for _, sl := range sales {
		if !inWindow(sl.SaleDate) {
			continue
		}
		lot := lotByID[sl.LotID]
		res, rerr := domain.RealizedResult(lot, sl, pays)
		if rerr != nil {
			continue
		}
		proceeds, _ := domain.SaleProceeds(sl) //nolint:errcheck // RealizedResult вище вже відсіяв биті продажі
		row("продаж", sl.SaleDate, lot.ISIN, "", fmt.Sprintf("%d", sl.Qty),
			proceeds, 0,
			"результат "+toMoneyJSON(res).Amount+" "+res.Currency().Code)
	}

	// Дивіденди фондів: податок ФАКТИЧНО утриманий, а не ставка. Ставка
	// змінювалась і ще змінюватиметься, у виписці стоїть те, що забрали.
	for _, op := range fundOps {
		if op.Kind != domain.FundDividend || !inWindow(op.Date) {
			continue
		}
		taxUAH, terr := asOf.uah(ctx, money.New(op.Tax, op.Currency), op.Date)
		if terr != nil {
			continue
		}
		row("дивіденд", op.Date, "", op.Fund, "",
			money.New(op.Amount, op.Currency), taxUAH, "")
	}

	// Відсотки вкладів: брутто й податок з одного проходу — так само, як
	// у /api/tax, інакше два способи порахувати одне число розійшлись би.
	// Дата — кінець періоду: DepositInterestTax зводить нарахування вікна
	// в одну суму, і розкладати її назад заради курсу означало б рахувати
	// відсотки вдруге.
	for _, dep := range termDeposits {
		g, tx := domain.DepositInterestTax(dep, from, to)
		if g == 0 {
			continue
		}
		taxUAH, terr := asOf.uah(ctx, money.New(tx, dep.Currency), to)
		if terr != nil {
			continue
		}
		row("відсотки вкладу", to, "", dep.Bank, "",
			money.New(g, dep.Currency), taxUAH, "за період "+string(from)+" → "+string(to))
	}

	if note := asOf.note(); note != "" {
		cw.Write([]string{"# " + note, "", "", "", "", "", "", "", "", "", ""})
	}
	cw.Flush()
}

// --- довідники брокерів і фондів ---
//
// Досі списку брокерів як сутності не існувало: він жив CSV-рядком у
// налаштуваннях, тож перейменувати брокера означало не зачепити жодного
// лота — назва в записах лишалась старою. Тепер записи тримаються за id,
// і перейменування підхоплюють усі разом.
