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
	"math"
	"net/http"
	"sort"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
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
	// PickISIN — папір, який людина обрала САМА для добору залишку замість
	// вершини рейтингу. Те саме питання й та сама відповідь, що параметр
	// pick у GET /api/route: вибір — частина питання, а не відповіді.
	//
	// Набору рядків плану він не стосується взагалі: план каже, що вже
	// вирішено, а вибір — куди вести те, що ще не розписано. Тому поле й
	// стоїть поруч із трьома попередніми, а не всередині draft.
	PickISIN string `json:"pick_isin,omitempty"`
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
	// BuyDate — коли планую; порожньо = «зараз».
	//
	// Future каже, чи дата в НАСТУПНОМУ місяці або далі — саме місяць, а
	// не день: сітка симуляції календарно-місячна, і рядок цього місяця
	// їй нема куди покласти окремо від сьогоднішнього (state_plan_buys.go).
	//
	// Overdue — дата вже минула. Прострочений намір рахується як «зараз»
	// (гроші за ним досі не витрачені), тож у нього Future = false, як і в
	// рядка цього місяця, — і без окремого поля підпис у таблиці не
	// відрізнив би одне від одного. Порівнювати дати в браузері не можна:
	// його «сьогодні» й серверне — різні дати в різних поясах.
	BuyDate string `json:"buy_date,omitempty"`
	Future  bool   `json:"future,omitempty"`
	Overdue bool   `json:"overdue,omitempty"`
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

// basketDoc — план купівель у грошах.
//
// НЕСТАЧІ ТУТ БІЛЬШЕ НЕМАЄ, і абзац лишається, щоб її не завели заново.
// Доти поруч із Totals стояли Shorts: скільки бракує кожному брокеру,
// пораховане проти СЬОГОДНІШНЬОГО залишку. Питання виявилось не тим.
// План купівель міряється ПЛАНОВИМИ грошима — тим, що надійде, — а не
// тим, що лежить на рахунку зараз: якщо на рахунку бракує, він
// поповниться з планових надходжень раніше, ніж покупка станеться. Тобто
// «у mono бракує 1 000» було тривогою про стан, який не настане.
//
// Половина цього доводу вже стояла в коді — і стосувалась лише далеких
// рядків («назвати нестачею те, що станеться після п'яти зарплат»). Вона
// просто не була поширена на найближчі.
//
// Сама арифметика жива й недоторкана: shortfallMinor у cash_shortfall.go
// обслуговує форми запису (лот, вклад, поповнення, НПФ) і дату «коли
// вистачить» у ready_on.go. Там питання інше — «я записую платіж ЗАРАЗ»,
// — і сьогоднішній залишок відповідає на нього правильно.
type basketDoc struct {
	Lines  []basketLine `json:"lines"`
	Totals []moneyJSON  `json:"totals"` // разом по кожній валюті
}

type whatIfPayload struct {
	After  *state.Doc `json:"after"`
	Basket basketDoc  `json:"basket"`
	// Topup — чим добрати РЕШТУ грошей місяця, щоб частки вирівнялись.
	//
	// Тим самим типом, що розкладка надходження (allocPlan), і тією ж
	// функцією: питання одне — «ось сума, розклади її цілими квитками», —
	// і друга відповідь на нього розійшлася б із першою. Різниця лише в
	// тому, ЯКА це сума й ВІД ЯКОГО портфеля міряються частки.
	//
	// nil означає «розкладати нема чого»: плану доходу немає, або місяць
	// уже закритий, або весь залишок розписаний планом купівель. Порожня
	// розкладка нуля читалась би як поломка, тому нуля тут не буває.
	Topup *allocPlan `json:"topup,omitempty"`
	// TopupPlanUAH / TopupLeftUAH — два числа, з яких вийшла сума Topup:
	// скільки план місяця ще обіцяє і скільки з того вже розписав план
	// купівель. Без них картка показала б результат віднімання, не
	// показавши самого віднімання, — а питання «чому пропонують так мало»
	// виникає рівно на ньому.
	TopupPlanUAH float64 `json:"topup_plan_uah,omitempty"`
	TopupLeftUAH float64 `json:"topup_left_uah,omitempty"`
}

// handleWhatIf — стан портфеля ПІСЛЯ планованих покупок.
//
// «До» фронтенд уже тримає як ctx.summary, тож другий документ у
// відповідь не кладемо: різниця двох чисел, які обидва народжені цим
// кодом, — законне віднімання, а не власний перерахунок.
//
// Ніщо тут нічого не блокує: перевищений ліміт концентрації показується
// й лишає рішення людині. Правило живе, а от друга його ілюстрація —
// нестача грошей — пішла разом із самою нестачею (див. basketDoc).
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
	basket := exp.basket

	after, err := s.buildStateWith(ctx, now, exp.what)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := whatIfPayload{After: after, Basket: basket}
	if err := s.addTopup(ctx, now, after, basket, req.PickISIN, &out); err != nil {
		// Невідомий папір — помилка ЗАПИТУ, а не збій: людина назвала ISIN,
		// якого немає серед порад. П'ятисотка тут читалась би як поломка
		// застосунку, і сторінка не змогла б показати причину дослівно —
		// а причина в тому й полягає, щоб її прочитали.
		var bad badRequestError
		if errors.As(err, &bad) {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Обидва документи гіпотези — «до» і «після» — у валюті звітності
	// (теги state.Doc діють і на вкладених).
	if err := s.present(r.Context(), &out); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// addTopup — чим добрати решту грошей місяця, щоб частки вирівнялись.
//
// ЧОМУ ЦЕ ТУТ, А НЕ ОКРЕМИМ ЕНДПОЙНТОМ. Обидва входи є разом рівно в цьому
// місці й більше ніде: повний документ ПІСЛЯ плану (after) і сам план у
// грошах (basket). Окремий ендпойнт мусив би зібрати їх удруге — тобто
// вдруге розгорнути plan_buys і вдруге перебудувати стан, — і два
// перерахунки одного дня давали б два різні числа щоразу, коли між ними
// щось запишуть.
//
// ВЛАСНОЇ АРИФМЕТИКИ ТУТ ОДНЕ ВІДНІМАННЯ, і воно нижче. Усе решта —
// allocatePlan, та сама чиста функція, що обслуговує розкладку надходження
// й ногу маршруту.
func (s *Server) addTopup(ctx context.Context, now time.Time,
	after *state.Doc, basket basketDoc, pickISIN string, out *whatIfPayload) error {

	if after.MonthPlan == nil || after.MonthPlan.LeftUAH.Major() <= 0 {
		return nil
	}
	rates, err := s.rates(ctx)
	if err != nil {
		return err
	}
	// ВІДНІМАННЯ ТУТ БІЛЬШЕ НЕМАЄ, І ЦЕ ГОЛОВНЕ, ЩО ТРЕБА ЗНАТИ ПРО ЦЮ
	// ФУНКЦІЮ.
	//
	// Стояло `avail = LeftUAH − Σ(рядки кошика)`: LeftUAH міряв гроші,
	// ВНЕСЕНІ в портфель, а план купівель у ньому не був урахований —
	// намір, а не рух грошей, — тож без явного віднімання картка радила б
	// докупити рівно те, що вже заплановане.
	//
	// Відколи гіпотеза приносить гроші, якими план оплачений
	// (hypothetical.topUps), синтетичне поповнення потрапляє в
	// MonthDepositedUAH, і LeftUAH зменшується САМ. Лишити віднімання
	// означало б відняти план ДВІЧІ — і картка мовчала б там, де гроші ще
	// є.
	//
	// Майбутні рядки й тут не рахуються: вони грошей не приносять, тож і
	// LeftUAH не чіпають (state_plan_buys.go, гілка при topUps).
	//
	// Через це `basket` лишається в підписі, хоч більше не читається: воно
	// й далі описує ті самі гроші, і наступний автор, шукаючи «де ж тут
	// віднімання», мусить знайти цей абзац, а не порожній параметр.
	avail := after.MonthPlan.LeftUAH
	out.TopupPlanUAH = round2(planCostUAH(basket, rates) + avail.Major())
	out.TopupLeftUAH = round2(math.Max(0, avail.Major()))
	// Поріг той самий, що в розкладки: сума, з якої не вийде жодного руху,
	// не варта картки. Нуль і від'ємне значення сюди ж — план купівель
	// може бути й більшим за те, що місяць обіцяє.
	if avail.Major() < allocMinCutUAH {
		return nil
	}
	// ПОРАДИ ВІД `after`, А НЕ ВІД `before`. Рейтинг ранжує сумою розривів
	// (suggPlanScore), і розриви мусять бути ті, що лишились ПІСЛЯ плану:
	// інакше вершиною стане саме той вид, який план уже закрив.
	sug, err := s.reinvestSuggestions(ctx, now, after)
	if err != nil {
		return err
	}
	// Вибір перевіряється ТІЄЮ САМОЮ pickSuggestion, що й у розкладці, і
	// над порадами від after: невідомий ISIN мусить дати одну й ту саму
	// відмову з обох екранів, інакше два різні тексти на один папір
	// читались би як дві різні причини.
	pick, err := pickSuggestion(sug, pickISIN)
	if err != nil {
		return err
	}
	// БЕЗ ОБМЕЖЕНЬ ЗА ДЖЕРЕЛОМ, і це не недогляд. Розкладають не одне
	// надходження, а зведений залишок місяця — десяток потоків із різними
	// дозволами (plan_flows.uses), — і одне слово «чиї це гроші» на нього
	// було б неправдою для половини суми. Той самий довід, що при
	// reserveEligibleUAH, лише з протилежним висновком: там сума одна й
	// дозвіл у неї один, тут сум багато.
	plan := allocatePlan(after, sug, rates,
		toMoneyJSON(money.New(int64(math.Round(avail.Major()*100)), money.UAH)), avail.Major(),
		allocAllow{ReserveUAH: avail.Major(), GoalsUAH: avail.Major(), PickISIN: pick},
		money.UAH, s.npfIDByName(ctx))
	out.Topup = &plan
	return nil
}

// planCostUAH — скільки коштують рядки «зараз», грн-екв.
//
// Потрібне ЛИШЕ шапці картки: вона показує три числа — скільки місяць
// обіцяв, скільки з того вже розписано планом, скільки лишилось, — і без
// середнього результат віднімання стояв би без самого віднімання.
//
// Саме віднімання при цьому робить уже не картка: гіпотеза приносить гроші
// плану, тож LeftUAH зменшується сам (довід — при avail вище). Тут лише
// відновлюється те, що місяць обіцяв ДО плану: залишок плюс його вартість.
func planCostUAH(basket basketDoc, rates fx.Rates) float64 {
	out := 0.0
	for _, l := range basket.Lines {
		if l.Future {
			continue
		}
		out += moneyAmount(l.Total) * allocRate(l.Currency, rates)
	}
	return out
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
		if v := byCur[cur]; v.Major() > bestAmt {
			best, bestAmt = name, v.Major()
		}
	}
	if best == "" {
		return "—", true
	}
	return best, true
}

// Порядок у відповіді детермінований навмисно: інакше два однакові
// запити давали б різний JSON (мапи в Go обходяться випадково), і будь-яке
// порівняння відповідей — очима чи тестом — перетворилось би на гадання.
func sortMoneyJSON(m []moneyJSON) {
	sort.Slice(m, func(i, j int) bool { return m[i].Currency < m[j].Currency })
}
