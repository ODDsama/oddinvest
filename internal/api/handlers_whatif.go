// План купівель: що станеться з портфелем і з цілями, якщо це купити.
//
// Питання ставиться ДО оплати, і відповідь на нього — той самий документ
// стану, тільки над портфелем, у якому покупки вже записані. Тому тут
// немає жодної власної арифметики: ні часток, ні драбини, ні дюрації, ні
// точки незалежності. buildStateWith домішує гіпотезу в sources, і далі
// все рахує той самий код, що й завжди (див. коментар до hypothetical).
//
// Чому не на фронтенді. Порахувати «нові валютні частки» у JS — це
// другий спосіб відповісти на питання, у якого вже є один. Обидва рази,
// коли в цьому застосунку зʼявлялась друга копія арифметики часток,
// наслідком були різні числа на одному екрані; state.Capital і
// handlers_reinvest.go тримають ці історії записаними.
//
// ЧОМУ В БАЗІ — І ЧОМУ РАНІШЕ БУЛО НАВПАКИ.
//
// Тут стояв аргумент: «збережений кошик — це другий спосіб задати
// покупки, і питання «який із них справжній» не мало б відповіді». Він
// був правильний рівно доти, доки кошик означав чернетку на дві
// хвилини: набрав, подивився, пішов купувати. Тоді localStorage справді
// був чесніший за таблицю.
//
// Скасовано тим, що питання змінилось. Планована купівля має ДАТУ, і
// рядок із майбутньою датою — це вже не чернетка, а частина плану поруч
// із потоками доходу й діями: він рухає точку незалежності, криву
// капіталу й ціль так само, як замок чи зарплата. Тримати половину плану
// в базі, а половину в браузері одного пристрою — ось де зʼявляються два
// джерела правди, а не тут.
//
// «Який із них справжній» відповіді тепер не потребує: спосіб задати
// покупки один — таблиця plan_buys. Тіло запиту приймає ЧЕРНЕТКУ саме
// для того, щоб її не довелось зберігати заради превʼю: чернетка нікуди
// не пишеться й живе рівно один запит.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// whatIfReq — три випадки одним тілом.
//
//	{}                              — наслідки збереженого плану;
//	{"draft":[рядок]}               — превʼю чернетки під час введення;
//	{"exclude":[7],"draft":[рядок']} — превʼю ПРАВКИ рядка 7.
//
// Один шлях, а не три ручки: «наслідки» — це завжди наслідки одного й
// того самого набору рядків, і різниця лише в тому, як цей набір
// складено. Три ендпойнти означали б три місця, де набір збирається
// по-різному.
type whatIfReq struct {
	// Saved — чи брати збережений план. Покажчик, бо за замовчуванням
	// ТАК: порожнє тіло має відповідати на головне питання екрана, а не
	// малювати порожній кошик. false потрібен рівно тестам і превʼю
	// «лише цей рядок».
	Saved   *bool        `json:"saved,omitempty"`
	Exclude []int64      `json:"exclude,omitempty"`
	Draft   []planBuyReq `json:"draft,omitempty"`
}

// basketLine — один рядок плану, вже з ціною.
type basketLine struct {
	// ID — рядок у plan_buys; 0 означає чернетку, якої в базі ще немає.
	// Саме за ним UI чіпляє «змінити», «виконано» й «прибрати».
	ID       int64     `json:"id,omitempty"`
	Kind     string    `json:"kind"`
	Label    string    `json:"label"`
	Qty      int64     `json:"qty"`
	Unit     moneyJSON `json:"unit"`
	Total    moneyJSON `json:"total"`
	Currency string    `json:"currency"`
	// BuyDate — коли планую; порожньо = «зараз». Future каже, чи ця дата
	// СТРОГО попереду: минула дата — прострочений намір, і рахується він
	// як «зараз» (state_plan_buys.go).
	BuyDate string `json:"buy_date,omitempty"`
	Future  bool   `json:"future,omitempty"`
	// IsReserve — планований вклад є подушкою. У відповіді він потрібен не
	// заради значка: саме за ним картка наслідків вирішує, чи взагалі
	// малювати рядки подушки й драбини (рядок, який структурно не може
	// зрушити, гірший за його відсутність).
	IsReserve bool `json:"is_reserve,omitempty"`
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

// basketDoc — план купівель у грошах.
//
// АСИМЕТРІЯ, про яку треба знати: Totals рахує ВСІ рядки («скільки я
// збираюсь витратити»), а Shorts — лише сьогоднішні. Сьогоднішні залишки
// відповідають лише на сьогоднішнє питання, і сказати «у mono бракує
// 40 000» про покупку в березні означало б назвати нестачею те, що
// станеться після п'яти зарплат.
type basketDoc struct {
	Lines  []basketLine  `json:"lines"`
	Totals []moneyJSON   `json:"totals"` // разом по кожній валюті
	Shorts []basketShort `json:"shorts,omitempty"`
}

type whatIfPayload struct {
	After  *state.Doc `json:"after"`
	Basket basketDoc  `json:"basket"`
}

// handleWhatIf — стан портфеля ПІСЛЯ планованих покупок.
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
	rows, err := s.planBuyRows(ctx, req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Стан ДО — потрібен, щоб знати, у кого скільки грошей, за якою ціною
	// йде сертифікат і кого обрати брокером, коли його не назвали.
	before, err := s.buildState(ctx, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	exp, err := s.expandPlanBuys(ctx, before, today, rows)
	if err != nil {
		var bad badRequestError
		if errors.As(err, &bad) {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Чого бракує — рахуємо ДО збірки: у стані «після» гроші вже списані,
	// і від'ємний залишок там означав би те саме, але без імені винуватця.
	basket := exp.basket
	basket.Shorts = shortfalls(before, exp.spend)

	after, err := s.buildStateWith(ctx, now, exp.what)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, whatIfPayload{After: after, Basket: basket})
}

// planBuyRows — набір рядків, наслідки якого рахуємо: збережені (за
// відрахуванням виключених) плюс чернетки з тіла запиту.
//
// Чернетка проходить ту саму planBuyFromReq, що й запис у базу. Друга
// перевірка форми для превʼю означала б, що рядок може виглядати
// правильним доти, доки його не збережеш.
func (s *Server) planBuyRows(ctx context.Context, req whatIfReq) ([]store.PlanBuy, error) {
	var rows []store.PlanBuy
	if req.Saved == nil || *req.Saved {
		saved, err := s.st.ListPlanBuys(ctx)
		if err != nil {
			return nil, err
		}
		skip := map[int64]bool{}
		for _, id := range req.Exclude {
			skip[id] = true
		}
		for _, b := range saved {
			if !skip[b.ID] {
				rows = append(rows, b)
			}
		}
	}
	for _, d := range req.Draft {
		b, err := planBuyFromReq(d)
		if err != nil {
			return nil, err
		}
		rows = append(rows, b)
	}
	return rows, nil
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
//
// Саме віднімання живе в cash_shortfall.go і спільне з формами покупки:
// план і форма відповідають на одне питання, тож рахувати його двічі
// означало б завести розходження між екраном «що буде» і екраном «пишу».
func shortfalls(doc *state.Doc, spend map[string]int64) []basketShort {
	var out []basketShort
	for key, want := range spend {
		broker, cur := splitKey(key)
		if short := shortfallMinor(doc, broker, cur, want); short > 0 {
			out = append(out, basketShort{Broker: broker, Currency: cur,
				Short: toMoneyJSON(money.New(short, cur))})
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
