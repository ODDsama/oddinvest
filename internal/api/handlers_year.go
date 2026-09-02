// Рік у цифрах — GET /api/year?year=YYYY.
//
// Та сама природа, що в підсумку місяця (handlers_period.go): сторінка
// дивиться назад на ЗАКРИТИЙ період як на ціле, і нічого тут не
// рахується вдруге. Гроші — summarizeCash, «було → стало» —
// periodStructureOf, рішення — periodDecisionsOf, місяці — та сама
// смужка серії (buildStreak), яку малює «Звичка». Рік лише складає їх
// поруч і додає те, чого місяць не має:
//
//   - хітмап днів (Days) — кожен день року з рухом грошей і рівень
//     інтенсивності, порахований ТУТ, а не в браузері (CLAUDE.md §5);
//   - зароблене окремо від поверненого тіла — з ознаки Principal.
//
// Чого тут свідомо НЕМАЄ: податку й віх. Податковий звіт уже є окремою
// ручкою з власним курсом на дату події (/api/tax), віхи — у
// /api/progress; сторінка складає їх поруч сама. Другий примірник тих
// самих чисел тут розійшовся б із першим.
package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

type yearMonth struct {
	Month      string  `json:"month"`
	ContribUAH float64 `json:"contributed_uah"`
	IncomeUAH  float64 `json:"income_uah"`
	// TargetUAH/Known/Hit — з тієї самої смужки серії, що у «Звичці»:
	// ціль ТОГО місяця зі знімка, і Known:false означає «судити нічим»,
	// а не «повз».
	TargetUAH float64 `json:"target_uah,omitempty"`
	Known     bool    `json:"known"`
	Hit       bool    `json:"hit"`
}

// yearDay — один день із рухом грошей. Lvl — рівень інтенсивності 1..4
// за квартилями суми |внесок|+|дохід|+|покупка| серед активних днів
// року; нуль не буває (дні без руху в списку відсутні).
type yearDay struct {
	Date        string  `json:"date"`
	ContribUAH  float64 `json:"contributed_uah,omitempty"`
	IncomeUAH   float64 `json:"income_uah,omitempty"`
	PurchaseUAH float64 `json:"purchased_uah,omitempty"`
	Lvl         int     `json:"lvl"`
}

type yearResp struct {
	Year int    `json:"year"`
	From string `json:"from"`
	To   string `json:"to"`
	// Partial — рік ще триває: числа «поки що», і сторінка каже це
	// вголос замість того, щоб читатись як провал.
	Partial bool `json:"partial"`
	// Years — які роки є взагалі: від першого руху грошей до сьогодні.
	Years []int `json:"years"`

	Money periodMoney `json:"money"`
	// EarnedUAH — дохід БЕЗ повернутого тіла; PrincipalUAH — саме тіло
	// (погашення ОВДП, тіло вкладу). Разом вони і є Money.IncomeUAH.
	EarnedUAH    float64 `json:"earned_uah"`
	PrincipalUAH float64 `json:"principal_uah,omitempty"`
	IdleUAH      float64 `json:"idle_uah"`

	Structure     *periodStructure `json:"structure,omitempty"`
	StructureNote string           `json:"structure_note,omitempty"`

	Months    []yearMonth `json:"months"`
	BestMonth *yearMonth  `json:"best_month,omitempty"`

	Decisions periodDecisions `json:"decisions"`
	Days      []yearDay       `json:"days"`
}

func (s *Server) handleYear(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	today := domain.NewDate(time.Now())
	year := today.Year()
	if q := r.URL.Query().Get("year"); q != "" {
		y, err := strconv.Atoi(q)
		if err != nil || y < 2000 || y > 2200 {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("рік має вигляд YYYY, а не %q", q))
			return
		}
		year = y
	}
	from := domain.Date(fmt.Sprintf("%04d-01-01", year))
	to := domain.Date(fmt.Sprintf("%04d-12-31", year))

	events, err := s.cashEvents(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	snaps, err := s.st.ListSnapshots(ctx, "", to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	list, err := s.st.ListDecisions(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, buildYear(year, from, to, today, events, snaps, list))
}

// buildYear — чиста функція над готовими даними (як buildProgress).
func buildYear(year int, from, to, today domain.Date, events []flowEvent,
	snaps []store.Snapshot, list []store.Decision) yearResp {

	sum := summarizeCash(events, from, to)
	out := yearResp{
		Year: year, From: string(from), To: string(to),
		Partial: to.After(today),
		Years:   yearsOf(events, snaps, today),
		Money: periodMoney{
			OpeningUAH:  sum.major(sum.OpeningUAH),
			IncomeUAH:   sum.major(sum.IncomeUAH),
			ContribUAH:  sum.major(sum.ContribUAH),
			PurchaseUAH: sum.major(-sum.PurchaseUAH),
			ConvUAH:     sum.major(sum.ConvUAH),
			ClosingUAH:  sum.major(sum.ClosingUAH()),
			OutsideUAH:  sum.major(sum.OutsideUAH),
			OwnUAH:      sum.major(sum.OwnUAH()),
		},
		Months: []yearMonth{},
		Days:   []yearDay{},
	}

	var earned, principal int64
	var income, buys []domain.CashEvent
	byDay := map[string]*yearDay{}
	byMonthIncome := map[string]int64{}
	for _, e := range sum.Rows {
		d := byDay[string(e.Date)]
		if d == nil {
			d = &yearDay{Date: string(e.Date)}
			byDay[string(e.Date)] = d
		}
		switch e.Kind {
		case flowIncome:
			income = append(income, domain.CashEvent{Date: e.Date, Amount: e.UAH})
			if e.Principal {
				principal += e.UAH
			} else {
				earned += e.UAH
			}
			d.IncomeUAH += float64(e.UAH) / 100
			byMonthIncome[string(e.Date)[:7]] += e.UAH
		case flowPurchase:
			buys = append(buys, domain.CashEvent{Date: e.Date, Amount: -e.UAH})
			d.PurchaseUAH += float64(e.UAH) / 100
		case flowContribution, flowOutside:
			// Свої гроші — гаманець і подушка разом, як у плитці «Цей
			// місяць»: день, коли відклав у подушку, — день із рухом.
			d.ContribUAH += float64(e.UAH) / 100
		}
	}
	out.EarnedUAH = round2(float64(earned) / 100)
	out.PrincipalUAH = round2(float64(principal) / 100)
	out.IdleUAH = sum.major(domain.IdleIncome(income, buys))
	out.Days = heatDays(byDay)

	out.Structure, out.StructureNote = periodStructureOf(snaps, from, "рік", "року")
	out.Decisions = periodDecisionsOf(list, from, to)
	if out.Decisions.Count == 0 {
		out.Decisions.Note = "цього року нічого не куплено"
	}

	// Місяці — зі смужки серії: ті самі want/got, що у «Звичці». Поточний
	// місяць смужка не містить (ціль ще не закрита), і тут його теж
	// немає — за тим самим доводом.
	prefix := fmt.Sprintf("%04d-", year)
	for _, mk := range buildStreak(snaps, events, today).Marks {
		if len(mk.Month) < 4 || mk.Month[:5] != prefix {
			continue
		}
		out.Months = append(out.Months, yearMonth{
			Month: mk.Month, ContribUAH: mk.ContribUAH, TargetUAH: mk.TargetUAH,
			IncomeUAH: round2(float64(byMonthIncome[mk.Month]) / 100),
			Known:     mk.Known, Hit: mk.Hit,
		})
	}
	for i := range out.Months {
		if out.BestMonth == nil || out.Months[i].ContribUAH > out.BestMonth.ContribUAH {
			out.BestMonth = &out.Months[i]
		}
	}
	if out.BestMonth != nil && out.BestMonth.ContribUAH <= 0 {
		out.BestMonth = nil
	}
	return out
}

// heatDays — дні з рухом, за датою, з рівнем 1..4 за квартилями величини
// руху серед активних днів. Квартилі, а не частка від максимуму: один
// великий внесок інакше зробив би решту року блідою.
func heatDays(byDay map[string]*yearDay) []yearDay {
	out := make([]yearDay, 0, len(byDay))
	mags := make([]float64, 0, len(byDay))
	for _, d := range byDay {
		mag := abs(d.ContribUAH) + abs(d.IncomeUAH) + abs(d.PurchaseUAH)
		if mag == 0 {
			continue
		}
		d.ContribUAH, d.IncomeUAH, d.PurchaseUAH =
			round2(d.ContribUAH), round2(d.IncomeUAH), round2(d.PurchaseUAH)
		out = append(out, *d)
		mags = append(mags, mag)
	}
	sort.Float64s(mags)
	q := func(p float64) float64 {
		if len(mags) == 0 {
			return 0
		}
		i := int(p * float64(len(mags)-1))
		return mags[i]
	}
	q1, q2, q3 := q(0.25), q(0.5), q(0.75)
	for i := range out {
		mag := abs(out[i].ContribUAH) + abs(out[i].IncomeUAH) + abs(out[i].PurchaseUAH)
		switch {
		case mag > q3:
			out[i].Lvl = 4
		case mag > q2:
			out[i].Lvl = 3
		case mag > q1:
			out[i].Lvl = 2
		default:
			out[i].Lvl = 1
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// yearsOf — роки від першого руху грошей (або знімка) до сьогодні.
func yearsOf(events []flowEvent, snaps []store.Snapshot, today domain.Date) []int {
	first := today.Year()
	for _, e := range events {
		if y := e.Date.Year(); y < first {
			first = y
		}
	}
	for _, sn := range snaps {
		if y := sn.Date.Year(); y < first {
			first = y
		}
	}
	out := make([]int, 0, today.Year()-first+1)
	for y := today.Year(); y >= first; y-- {
		out = append(out, y)
	}
	return out
}
