// Відмітки надходжень (0027): факт до плану доходу.
//
// CRUD за зразком handlers_plan.go — уся перевірка форми в одній функції,
// спільній для POST і PUT. Арифметики тут немає жодної: заміщення планової
// суми робить ядро (state_plan.go), а чеклист розгортає
// handlers_plan_timeline.go. Тут — зберігання й перевірка.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

// monthRe — "YYYY-MM". Регулярка, а не time.Parse: місяць тут не дата, і
// прогнавши його через time.Parse із дописаним днем, ми б мовчки прийняли
// "2026-08-31" як серпень.
var monthRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

// planReceiptReq. Amount десятковим РЯДКОМ, як усюди в цьому API, і
// обов'язковий: «не прийшло» — це явний "0", а не пропущене поле. Порожній
// рядок відхиляється саме тому, що інакше забута форма записувала б нуль,
// не відрізнимий від свідомого «нічого не було».
type planReceiptReq struct {
	FlowID    int64  `json:"flow_id"` // 0 = «інше», позаплановий дохід
	Month     string `json:"month"`   // YYYY-MM
	Name      string `json:"name,omitempty"`
	Amount    string `json:"amount"`
	Currency  string `json:"currency,omitempty"`
	InvestPct string `json:"invest_pct,omitempty"` // лише для «іншого»; порожньо = 100
	Note      string `json:"note,omitempty"`
}

// planReceiptFromReq — уся валідація відмітки, спільна для POST і PUT.
//
// flows потрібні заради двох перевірок, які без них були б неможливі:
// відмітка мусить вказувати на ІСНУЮЧЕ джерело доходу і мати ЙОГО валюту.
// Друга — не формальність: нативний вектор проєкції складає суми у валюті
// потоку, і гривнева відмітка на доларовій зарплаті мовчки додала б
// тридцятикратну суму (state_plan.go пояснює, чому валютні потоки йдуть у
// проєкцію повз гривню).
func planReceiptFromReq(req planReceiptReq, flows []store.PlanFlow, today domain.Date) (store.PlanReceipt, error) {
	var out store.PlanReceipt
	month := strings.TrimSpace(req.Month)
	if !monthRe.MatchString(month) {
		return out, fmt.Errorf("місяць має бути у форматі YYYY-MM, маємо %q", req.Month)
	}
	cur := strings.TrimSpace(req.Currency)
	name := strings.TrimSpace(req.Name)
	invest := int64(10000)

	if req.FlowID != 0 {
		var flow *store.PlanFlow
		for i := range flows {
			if flows[i].ID == req.FlowID {
				flow = &flows[i]
				break
			}
		}
		if flow == nil {
			return out, fmt.Errorf("джерело доходу %d %w", req.FlowID, store.ErrNotFound)
		}
		if flow.Kind != "income" {
			return out, errors.New("відмічати можна лише надходження, а не витрати")
		}
		if cur == "" {
			cur = flow.Currency
		}
		if cur != flow.Currency {
			return out, fmt.Errorf("валюта відмітки (%s) має збігатися з валютою джерела (%s)",
				cur, flow.Currency)
		}
		// Назва — знімок на момент відмітки: журнал мусить читатися й після
		// того, як потік перейменували чи видалили. Та сама причина, що й у
		// plan_flow_revisions.
		name = flow.Name
		// Частка береться з потоку при кожному читанні, тож те, що лежить у
		// колонці, для прив'язаної відмітки не читає ніхто. Пишемо 100%, щоб
		// у базі не осідало число, яке виглядає значущим і не є ним.
		invest = 10000
	} else {
		if name == "" {
			return out, errors.New("позаплановому надходженню потрібна назва")
		}
		// «Інше» в майбутньому не буває: позаплановий дохід — це те, що вже
		// сталося. Відоме майбутнє надходження описується планом (потік
		// «разове»), і саме тому воно там і живе — у проєкції, а не поруч.
		if month > string(today)[:7] {
			return out, errors.New("позапланове надходження можна відмітити лише за минулий або поточний місяць")
		}
		if cur == "" {
			cur = money.UAH
		}
		if strings.TrimSpace(req.InvestPct) != "" {
			var err error
			if invest, err = parsePercentBP(req.InvestPct); err != nil {
				return out, fmt.Errorf("частка в портфель: %w", err)
			}
		}
		if invest < 0 || invest > 10000 {
			return out, errors.New("частка в портфель має бути від 0 до 100%")
		}
	}

	if strings.TrimSpace(req.Amount) == "" {
		return out, errors.New("сума обов'язкова: «не прийшло» позначається нулем")
	}
	amt, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		return out, fmt.Errorf("сума: %w", err)
	}
	// Нуль дозволений і є суттю фічі; від'ємне — ні: повернення грошей
	// назад це не надходження, а зняття, і воно вже має свій журнал.
	if amt < 0 {
		return out, errors.New("сума надходження не може бути від'ємною")
	}
	return store.PlanReceipt{
		FlowID: req.FlowID, Month: month, Name: name, Amount: amt,
		Currency: cur, InvestBP: invest, Note: strings.TrimSpace(req.Note),
	}, nil
}

// receiptFromBody — розбір тіла плюс валідація. Спільне для POST і PUT, щоб
// правила не існували у двох копіях (та сама причина, що й у lotFromReq).
func (s *Server) receiptFromBody(r *http.Request) (store.PlanReceipt, error) {
	var req planReceiptReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return store.PlanReceipt{}, err
	}
	flows, err := s.st.ListPlanFlows(r.Context())
	if err != nil {
		return store.PlanReceipt{}, err
	}
	return planReceiptFromReq(req, flows, domain.NewDate(time.Now()))
}

func (s *Server) handleListPlanReceipts(w http.ResponseWriter, r *http.Request) {
	receipts, err := s.st.ListPlanReceipts(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	flows, err := s.st.ListPlanFlows(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Курс ковтаємо свідомо, як і в списку потоків: без нього валютні
	// відмітки дадуть 0 у гривневій колонці, але сама сторінка працює.
	rates, _ := s.rates(r.Context()) //nolint:errcheck // свідомо: див. вище
	writeJSON(w, http.StatusOK, receiptRows(receipts, flows, rates))
}

func (s *Server) handleAddPlanReceipt(w http.ResponseWriter, r *http.Request) {
	rec, err := s.receiptFromBody(r)
	if err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	id, err := s.st.AddPlanReceipt(r.Context(), rec)
	if err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdatePlanReceipt(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	rec, err := s.receiptFromBody(r)
	if err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	rec.ID = id
	if err := s.st.UpdatePlanReceipt(r.Context(), rec); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeletePlanReceipt(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeletePlanReceipt(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}
