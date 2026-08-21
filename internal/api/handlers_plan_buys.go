// План купівель: КРУД над тим, що я збираюсь узяти (міграція 0033).
//
// Тут ЛИШЕ зберігання й перевірка форми. Ціна кроку, брокер за
// замовчуванням, підсумки по валютах і нестача рахуються в
// handlers_whatif.go — там, де вони й були, — а перетворення рядка на
// гіпотетичний портфель чи на запис прогнозу живе в state_plan_buys.go.
//
// Чому список повертає СИРІ рядки, без цін і підсумків. Ті самі числа вже
// приїжджають у basket.lines відповіді POST /api/whatif, і рахує їх один
// unit_cost.go. Порахувати їх ще й тут означало б завести другу ціну того
// самого паперу на тому самому екрані — застосунок уже проходив це з
// частками капіталу (state.Capital) і з арифметикою помічника. Тому
// GET віддає рівно те, чим заповнюється форма правки, а таблицю малює
// відповідь whatif.
//
// /api/plan/buys/check немає з тієї ж причини: «чи вистачить грошей»
// відповідає basket.shorts тим самим shortfallMinor, що й форми покупки.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

type planBuyReq struct {
	Kind      string `json:"kind"`
	Ref       string `json:"ref"`
	Qty       int64  `json:"qty,omitempty"`
	Amount    string `json:"amount,omitempty"`     // десятковий
	UnitPrice string `json:"unit_price,omitempty"` // десятковий, лише фонд
	Currency  string `json:"currency,omitempty"`
	Broker    string `json:"broker,omitempty"`
	BuyDate   string `json:"buy_date,omitempty"` // "" = зараз
	RatePct   string `json:"rate_pct,omitempty"` // вклад
	Months    int    `json:"months,omitempty"`   // вклад
	IsReserve bool   `json:"is_reserve,omitempty"`
	Note      string `json:"note,omitempty"`
}

// planBuyFromReq — перевірка форми. Кожен вид відповідає на своє питання
// «скільки», і саме тут це закріплено: папір і сертифікат — штуками,
// вклад і внесок — сумою. Поле, яке для цього виду не читається, не
// приймається мовчки: описка в ньому інакше жила б у базі, нічого не
// роблячи, доки хтось не змінив би вид рядка.
func planBuyFromReq(req planBuyReq) (store.PlanBuy, error) {
	var out store.PlanBuy
	ref := strings.TrimSpace(req.Ref)
	cur := strings.TrimSpace(req.Currency)
	out = store.PlanBuy{
		Kind: req.Kind, Ref: ref, Currency: cur,
		Broker: strings.TrimSpace(req.Broker), Note: req.Note,
	}
	// Дата: порожньо = «купую зараз». МИНУЛА дата не помилка — це
	// прострочений намір («мав узяти в березні, ще не взяв»), і рахується
	// він як «зараз», бо саме так і є: гроші досі не витрачені. Помилкою
	// вона була б лише тоді, коли б минуле йшло в прогноз — а туди
	// потрапляє тільки те, що строго попереду (state_plan_buys.go).
	if strings.TrimSpace(req.BuyDate) != "" {
		d, err := domain.ParseDate(req.BuyDate)
		if err != nil {
			return out, fmt.Errorf("дата покупки: %w", err)
		}
		out.BuyDate = d
	}

	switch req.Kind {
	case store.BuyBond, store.BuyFund:
		if ref == "" {
			return out, errors.New("вкажи, що саме купуєш")
		}
		if req.Qty <= 0 {
			return out, errors.New("кількість має бути > 0")
		}
		out.Qty = req.Qty
		if strings.TrimSpace(req.UnitPrice) != "" {
			// Ціна вручну — лише фонду, і лише тому, що каталогу цін фондів
			// у застосунку немає: про фонд, якого ще немає в портфелі,
			// сказати нічого не можна. Ціна ОВДП — це номінал плюс НКД із
			// довідника, і другий спосіб її задати став би другим джерелом
			// правди про те саме число.
			if req.Kind != store.BuyFund {
				return out, errors.New("ціну вручну можна задати лише сертифікату фонду")
			}
			p, err := domain.ParseDecimalToMinor(req.UnitPrice, orUAH(cur))
			if err != nil {
				return out, fmt.Errorf("ціна за штуку: %w", err)
			}
			if p <= 0 {
				return out, errors.New("ціна за штуку має бути > 0")
			}
			out.UnitPrice = p
		}
	case store.BuyDeposit:
		if ref == "" {
			return out, errors.New("вкажи банк: вклад лежить у конкретній установі, і саме з її рахунку йдуть гроші")
		}
		out.Currency = orUAH(cur)
		amt, err := domain.ParseDecimalToMinor(req.Amount, out.Currency)
		if err != nil {
			return out, fmt.Errorf("сума: %w", err)
		}
		if amt <= 0 {
			return out, errors.New("сума вкладу має бути > 0")
		}
		// Строк обовʼязковий, на відміну від дії lock, де 0 законно означає
		// «безстроково». Вклад без строку виходить із датою погашення,
		// рівною даті відкриття: формально діючий, у капіталі, і не платить
		// нічого — тобто число, яке виглядає правдоподібно й неправильно.
		if req.Months <= 0 {
			return out, errors.New("строк вкладу має бути > 0 місяців")
		}
		rate, err := parsePercentBPOpt(req.RatePct)
		if err != nil {
			return out, fmt.Errorf("ставка: %w", err)
		}
		if rate < 0 {
			return out, errors.New("ставка не може бути відʼємною")
		}
		out.Amount, out.RateBP, out.Months, out.IsReserve = amt, rate, req.Months, req.IsReserve
	case store.BuyNPF:
		if _, err := strconv.ParseInt(ref, 10, 64); err != nil {
			return out, errors.New("вкажи пенсійний рахунок")
		}
		// Валюту НПФ задає сам рахунок, і поле тут не читається: два місця,
		// де вона написана, рано чи пізно розійшлись би.
		out.Currency = ""
		amt, err := domain.ParseDecimalToMinor(req.Amount, money.UAH)
		if err != nil {
			return out, fmt.Errorf("сума: %w", err)
		}
		if amt <= 0 {
			return out, errors.New("сума внеску має бути > 0")
		}
		out.Amount = amt
	default:
		return out, fmt.Errorf("вид має бути bond, fund, deposit або npf, маємо %q", req.Kind)
	}
	return out, nil
}

func orUAH(cur string) string {
	if cur == "" {
		return money.UAH
	}
	return cur
}

// planBuyRow — рядок таким, яким його заповнюють у формі. Готових чисел
// тут немає навмисно (див. шапку файла).
type planBuyRow struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Ref       string `json:"ref"`
	Qty       int64  `json:"qty,omitempty"`
	Amount    string `json:"amount,omitempty"`
	UnitPrice string `json:"unit_price,omitempty"`
	Currency  string `json:"currency,omitempty"`
	Broker    string `json:"broker,omitempty"`
	BuyDate   string `json:"buy_date,omitempty"`
	RatePct   string `json:"rate_pct,omitempty"`
	Months    int    `json:"months,omitempty"`
	IsReserve bool   `json:"is_reserve,omitempty"`
	Note      string `json:"note,omitempty"`
	// MaturityDate — коли вклад погасився б, якби його відкрити СЬОГОДНІ.
	// Поле є заради однієї кнопки — «Виконано», яка відкриває справжню
	// форму вкладу, — і живе воно тут, а не в браузері, бо «дата відкриття
	// плюс строк» уже порахована в state_plan_buys.go. Друга копія цього
	// додавання в JS розійшлася б із першою на першому ж кроці місяця.
	MaturityDate string `json:"maturity_date,omitempty"`
}

func toPlanBuyRow(b store.PlanBuy, today domain.Date) planBuyRow {
	out := planBuyRow{
		ID: b.ID, Kind: b.Kind, Ref: b.Ref, Qty: b.Qty, Currency: b.Currency,
		Broker: b.Broker, BuyDate: string(b.BuyDate), Months: b.Months,
		IsReserve: b.IsReserve, Note: b.Note,
	}
	// Гроші рядком, а не числом: форма їх туди й покладе назад, а
	// десятковий рядок переживає коло без плаваючої коми.
	if b.Amount > 0 {
		out.Amount = minorToDecimal(b.Amount, orUAH(b.Currency))
	}
	if b.UnitPrice > 0 {
		out.UnitPrice = minorToDecimal(b.UnitPrice, orUAH(b.Currency))
	}
	if b.RateBP > 0 {
		out.RatePct = minorToDecimal(b.RateBP, money.UAH)
	}
	if b.Kind == store.BuyDeposit && b.Months > 0 {
		out.MaturityDate = string(today.AddMonths(b.Months))
	}
	return out
}

// minorToDecimal — мінорні в десятковий рядок тим самим шляхом, яким вони
// туди потрапили (domain.ParseDecimalToMinor у зворотний бік).
func minorToDecimal(minor int64, cur string) string {
	return toMoneyJSON(money.New(minor, cur)).Amount
}

func (s *Server) handleListPlanBuys(w http.ResponseWriter, r *http.Request) {
	buys, err := s.st.ListPlanBuys(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	today := domain.NewDate(time.Now())
	out := make([]planBuyRow, 0, len(buys))
	for _, b := range buys {
		out = append(out, toPlanBuyRow(b, today))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddPlanBuy(w http.ResponseWriter, r *http.Request) {
	var req planBuyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	b, err := planBuyFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddPlanBuy(r.Context(), b)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// publishAsync тут не тому, що план змінює документ стану — він його не
	// змінює, — а тому, що так роблять усі сусідні записи, і виняток довелось
	// би пояснювати на кожному наступному читанні. Ціна — одна перезбірка.
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdatePlanBuy(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req planBuyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	b, err := planBuyFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	b.ID = id
	if err := s.st.UpdatePlanBuy(r.Context(), b); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeletePlanBuy(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeletePlanBuy(r.Context(), id); err != nil {
		writeStoreErr(w, err, http.StatusInternalServerError)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}
