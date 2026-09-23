// POST /api/allocate — розкладка надходження. Уся арифметика й довід,
// чому вона саме така, — в allocate.go; тут лише розбір запиту й зведення
// готових чисел (стан, поради, дозвіл джерела) до AllocatePlan.

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	money "github.com/Rhymond/go-money"
)

type allocateReq struct {
	Amount   string `json:"amount"`             // десятковий, як усюди в цьому API
	Currency string `json:"currency,omitempty"` // порожньо = UAH
	// Source — чиї це гроші: plan (планове надходження) чи portfolio
	// (виплата з портфеля). Порожньо = plan, і це не байдужість: усі три
	// місця, звідки розкладку відкривають, знають джерело напевно, а
	// найчастіша ручна сума — зарплата.
	//
	// Principal — скільки з Amount є поверненням ВЛАСНОГО тіла, у ТІЙ САМІЙ
	// валюті й тим самим десятковим записом. Порожньо = нуль. Гривневого
	// числа тут бути не може: сума буває в доларах, і змішати їх означало б
	// віддати подушці рівно курс.
	Source    string `json:"source,omitempty"`
	Principal string `json:"principal,omitempty"`
	// SourceRef — ЯКЕ САМЕ надходження розкладаємо: "flow:<id>" або
	// "receipt:<id>". Синтетичний ключ із префіксом, як domain.NPFPlanDest,
	// і з тієї ж причини: джерел із часом буде більше одного виду, а
	// префікс лишає під них місце, не плодячи по полю на вид.
	//
	// Потрібен рівно заради дозволу (plan_flows.uses, 0041): «чиї це
	// гроші» відповідає на питання політики, а «які саме» — на питання
	// джерела, і зводити їх в одне слово не можна. Порожньо = обмежень
	// немає, тобто поведінка до появи поля: ручну суму, набрану руками,
	// ніхто нічим не позначав.
	SourceRef string `json:"source_ref,omitempty"`
	// PickISIN — папір, який людина обрала САМА замість вершини рейтингу.
	// Той самий вибір, що їде в GET /api/route параметром pick: нога
	// маршруту й розкладка того самого дня мусять відповідати однаково, і
	// вибір — частина питання, а не відповіді. Порожньо = рейтинг.
	PickISIN string `json:"pick_isin,omitempty"`
}

func (s *Server) handleAllocate(w http.ResponseWriter, r *http.Request) {
	var req allocateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cur := OrUAH(strings.TrimSpace(req.Currency))
	minor, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("сума: %w", err))
		return
	}
	if minor <= 0 {
		writeErr(w, http.StatusBadRequest, BadRequestf("сума розкладки має бути > 0"))
		return
	}
	src := AllocFromPlan
	if strings.TrimSpace(req.Source) == AllocFromPortfolio {
		src = AllocFromPortfolio
	}
	var principalMinor int64
	if s := strings.TrimSpace(req.Principal); s != "" {
		if principalMinor, err = domain.ParseDecimalToMinor(s, cur); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("тіло: %w", err))
			return
		}
		if principalMinor < 0 {
			writeErr(w, http.StatusBadRequest, BadRequestf("тіло не буває відʼємним"))
			return
		}
	}
	now := time.Now()
	doc, err := s.BuildState(r.Context(), now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rates, err := s.Rates(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	uahM, err := fx.ToUAH(money.New(minor, cur), rates)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sug, err := s.ReinvestSuggestions(r.Context(), now, doc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	amountUAH := float64(uahM.Amount()) / 100
	principalUAH := 0.0
	if principalMinor > 0 {
		// Тим самим переведенням, що й сума: другий курс на тому самому
		// рядку дав би подушці й паперам різні гривні.
		if pm, cerr := fx.ToUAH(money.New(principalMinor, cur), rates); cerr == nil {
			principalUAH = float64(pm.Amount()) / 100
		}
	}
	uses, err := s.UsesForRef(r.Context(), req.SourceRef)
	if err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	pick, err := PickSuggestion(sug, req.PickISIN)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Стеля джерела — усе або нічого, і саме тому вона тут не число з
	// журналу, а сума розкладки: розкладають ОДНЕ надходження, і дозвіл у
	// нього один. Дробові стелі бувають лише в маршруту, і вже не тому, що
	// нога зводить цілий місяць (вона більше не зводить — див. planAhead), а
	// тому, що горщик там накопичує кілька надходжень із різними дозволами.
	out := AllocatePlan(doc, sug, rates,
		ToMoneyJSON(money.New(minor, cur)), amountUAH,
		AllocAllow{
			ReserveUAH: ReserveEligibleUAH(doc.Settings, src, amountUAH, principalUAH,
				SourceCapUAH(uses, domain.UsePlanReserve, amountUAH)),
			GoalsUAH: GoalsEligibleUAH(doc.Settings, src, amountUAH, principalUAH,
				SourceCapUAH(uses, domain.UsePlanGoals, amountUAH)),
			Uses:     uses,
			PickISIN: pick,
		}, cur, s.NPFIDByName(r.Context()))
	if err := s.Present(r.Context(), &out); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
