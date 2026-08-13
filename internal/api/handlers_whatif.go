// Кошик покупки: що станеться з портфелем, якщо це купити.
//
// Питання ставиться ДО оплати, і відповідь на нього — той самий документ
// стану, тільки над портфелем, у якому покупки вже записані. Тому тут
// немає жодної власної арифметики: ні часток, ні драбини, ні дюрації.
// buildStateWith домішує гіпотетичні лоти в sources, і далі все рахує
// той самий код, що й завжди (див. коментар до hypothetical).
//
// Чому не на фронтенді. Порахувати «нові валютні частки» у JS — це
// другий спосіб відповісти на питання, у якого вже є один. Обидва рази,
// коли в цьому застосунку зʼявлялась друга копія арифметики часток,
// наслідком були різні числа на одному екрані; state.Capital і
// handlers_reinvest.go тримають ці історії записаними.
//
// Чому не в базі. Збережений кошик — це другий спосіб задати покупки,
// і питання «який із них справжній» не мало б відповіді. Той самий
// аргумент, що й у наборів налаштувань (strategy.js).

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
)

type whatIfItem struct {
	Kind   string `json:"kind"`           // bond | fund
	ISIN   string `json:"isin,omitempty"` // для bond
	Fund   string `json:"fund,omitempty"` // для fund
	Qty    int64  `json:"qty"`
	Broker string `json:"broker,omitempty"`
}

type whatIfReq struct {
	Items []whatIfItem `json:"items"`
}

// basketLine — один рядок кошика, вже з ціною.
type basketLine struct {
	Kind     string    `json:"kind"`
	Label    string    `json:"label"`
	Qty      int64     `json:"qty"`
	Unit     moneyJSON `json:"unit"`
	Total    moneyJSON `json:"total"`
	Currency string    `json:"currency"`
	// Broker — у кого купуємо. Assumed каже, що брокера обрав застосунок,
	// а не людина: припущення, яке впливає на «вистачає / не вистачає»,
	// має бути видно, а не лежати мовчки в обчисленні.
	Broker  string `json:"broker"`
	Assumed bool   `json:"broker_assumed,omitempty"`
}

// basketShort — чого і скільки бракує в конкретного брокера.
type basketShort struct {
	Broker   string    `json:"broker"`
	Currency string    `json:"currency"`
	Short    moneyJSON `json:"short"`
}

type basketDoc struct {
	Lines  []basketLine  `json:"lines"`
	Totals []moneyJSON   `json:"totals"` // разом по кожній валюті
	Shorts []basketShort `json:"shorts,omitempty"`
}

type whatIfResp struct {
	After  *state.Doc `json:"after"`
	Basket basketDoc  `json:"basket"`
}

// handleWhatIf — стан портфеля ПІСЛЯ гіпотетичних покупок.
//
// «До» фронтенд уже тримає як ctx.summary, тож другий документ у
// відповідь не кладемо: різниця двох чисел, які обидва народжені цим
// кодом, — законне віднімання, а не власний перерахунок.
//
// Нестача грошей нічого не блокує. Це те саме правило, що й у лімітів
// концентрації: застосунок показує наслідки, а рішення за людиною —
// гроші могли ще не прийти, і побачити картину наперед так само
// корисно.
func (s *Server) handleWhatIf(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	today := domain.NewDate(now)

	var req whatIfReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Стан ДО — потрібен, щоб знати, у кого скільки грошей, і щоб
	// обрати брокера, коли його не назвали.
	before, err := s.buildState(ctx, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	var what hypothetical
	basket := basketDoc{Lines: []basketLine{}}
	totals := map[string]int64{} // валюта → мінорні
	spend := map[string]int64{}  // "брокер|валюта" → мінорні
	for _, it := range req.Items {
		if it.Qty <= 0 {
			writeErr(w, http.StatusBadRequest,
				fmt.Errorf("кількість має бути > 0, а не %d", it.Qty))
			return
		}
		var unit *money.Money
		var label string
		switch it.Kind {
		case "fund":
			f := findFundRow(before, it.Fund)
			if f == nil {
				writeErr(w, http.StatusBadRequest, fmt.Errorf("фонд %q не знайдено", it.Fund))
				return
			}
			unit, label = fundUnitCost(f.LastPrice, f.Currency), f.Fund
		default: // bond
			b, berr := s.st.GetBond(ctx, it.ISIN)
			if berr != nil || b == nil {
				writeErr(w, http.StatusBadRequest,
					fmt.Errorf("паперу %q немає в довіднику", it.ISIN))
				return
			}
			pays, perr := s.st.PaymentsFor(ctx, []string{it.ISIN})
			if perr != nil {
				writeErr(w, http.StatusInternalServerError, perr)
				return
			}
			unit, label = bondUnitCost(*b, pays, today), it.ISIN
		}
		if unit.Amount() <= 0 {
			writeErr(w, http.StatusBadRequest,
				fmt.Errorf("%s: ціни немає, купувати нема за чим", label))
			return
		}
		cur := unit.Currency().Code
		broker, assumed := pickBroker(before, cur, it.Broker)
		total := unit.Amount() * it.Qty
		totals[cur] += total
		spend[broker+"|"+cur] += total
		basket.Lines = append(basket.Lines, basketLine{
			Kind: orBond(it.Kind), Label: label, Qty: it.Qty,
			Unit: toMoneyJSON(unit), Total: toMoneyJSON(money.New(total, cur)),
			Currency: cur, Broker: broker, Assumed: assumed,
		})
		if it.Kind == "fund" {
			what.fundOps = append(what.fundOps, domain.FundOp{
				Date: today, Fund: label, Kind: domain.FundBuy, Qty: it.Qty,
				Amount: total, Currency: cur, Broker: broker,
			})
		} else {
			what.lots = append(what.lots, domain.Lot{
				ISIN: label, Qty: it.Qty, PricePerBond: unit,
				BuyDate: today, Channel: broker,
			})
		}
	}

	for cur, v := range totals {
		basket.Totals = append(basket.Totals, toMoneyJSON(money.New(v, cur)))
	}
	sortMoneyJSON(basket.Totals)
	// Чого бракує — рахуємо ДО збірки: у стані «після» гроші вже списані,
	// і від'ємний залишок там означав би те саме, але без імені винуватця.
	basket.Shorts = shortfalls(before, spend)

	after, err := s.buildStateWith(ctx, now, what)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, whatIfResp{After: after, Basket: basket})
}

func orBond(k string) string {
	if k == "fund" {
		return "fund"
	}
	return "bond"
}

// findFundRow — фонд у вже зібраному стані. Беремо звідти, а не з
// довідника, бо потрібна остання ЦІНА, а її знає саме зведення.
func findFundRow(doc *state.Doc, name string) *state.FundPositionRow {
	for i := range doc.Funds {
		if doc.Funds[i].Fund == name {
			return &doc.Funds[i]
		}
	}
	return nil
}

// pickBroker — у кого купуємо. Названого беремо як є; без назви —
// того, у кого найбільше грошей у цій валюті, і кажемо про це вголос.
//
// Рахунки роздільні: гривня на inzhur не купить папір у mono, тож
// «вистачає / не вистачає» без імені брокера відповіді не має.
func pickBroker(doc *state.Doc, cur, want string) (string, bool) {
	if want != "" {
		return want, false
	}
	best, bestAmt := "", -1.0
	for name, byCur := range doc.Brokers {
		if v := byCur[cur]; v > bestAmt {
			best, bestAmt = name, v
		}
	}
	if best == "" {
		return "—", true
	}
	return best, true
}

// shortfalls — скільки не вистачає в кожного брокера окремо.
func shortfalls(doc *state.Doc, spend map[string]int64) []basketShort {
	var out []basketShort
	for key, want := range spend {
		broker, cur := splitKey(key)
		have := int64(0)
		if byCur, ok := doc.Brokers[broker]; ok {
			have = int64(byCur[cur]*100 + 0.5)
		}
		if want > have {
			out = append(out, basketShort{Broker: broker, Currency: cur,
				Short: toMoneyJSON(money.New(want-have, cur))})
		}
	}
	sortShorts(out)
	return out
}

func splitKey(k string) (string, string) {
	for i := len(k) - 1; i >= 0; i-- {
		if k[i] == '|' {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

// Порядок у відповіді детермінований навмисно: інакше два однакові
// запити давали б різний JSON (мапи в Go обходяться випадково), і будь-яке
// порівняння відповідей — очима чи тестом — перетворилось би на гадання.
func sortMoneyJSON(m []moneyJSON) {
	sort.Slice(m, func(i, j int) bool { return m[i].Currency < m[j].Currency })
}

func sortShorts(s []basketShort) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Broker != s[j].Broker {
			return s[i].Broker < s[j].Broker
		}
		return s[i].Currency < s[j].Currency
	})
}
