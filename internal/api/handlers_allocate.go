// Розкладка надходження: прийшла сума — куди й скільки з неї.
//
// ЧОМУ ЦЕ ОКРЕМИЙ ЕНДПОЙНТ, А НЕ РЯДОК У ПОМІЧНИКУ. «Що купити» відповідає
// на «що вигідніше», і його перелік не знає ні про яку суму: він ранжує
// інструменти й каже, скільки штук тягне КОЖЕН брокер окремо. Питання тут
// інше й вужче — «ось 32 000 ₴, розклади саме їх», — і відповідь на нього
// не перелік, а НАБІР із цілими квитками, який у сумі дорівнює тому, що
// прийшло. Дві різні відповіді в одному списку означали б, що частина
// рядків стосується цих грошей, а частина ні, і розрізняти їх довелося б
// очима.
//
// ВЛАСНОЇ АРИФМЕТИКИ ТУТ НЕМАЄ ЖОДНОЇ, і це головна вимога до файла:
//
//	частка подушки — doc.Reserve.FillNowUAH, готове число картки резерву;
//	бюджет виду    — spreadMonth (state_rebalance.go), той самий поділ, що
//	                 малює картка «Куди йдуть гроші місяця», лише
//	                 прикладений до цієї суми замість плану місяця;
//	порядок і ціна — reinvestSuggestions, тобто рейтинг, упорядкований
//	                 налаштуванням reinvest_rank, яке вибрав користувач.
//
// Друга копія будь-чого з цього означала б, що «Що купити» радить одне, а
// розкладка того ж дня — інше; проти рівно цієї розбіжності в now-view.js
// уже стоїть окреме попередження.
//
// РЕЗЕРВ ЗАБИРАЄ СВОЄ ПЕРШИМ І ЖАДІБНО. Не пропорційно й не «скільки
// лишиться»: подушка має власну, абсолютну ціль, і доки розрив живий,
// перші гроші місяця закривають саме його. Часткове закриття виходить
// само — коли FillNowUAH більший за суму, вирізка дорівнює всій сумі,
// рядків покупок немає, і хвіст добере наступна відмітка.
//
// ...АЛЕ НЕ З БУДЬ-ЯКИХ ГРОШЕЙ, і це єдиний виняток із попереднього
// абзацу. Стеля подушки міряється від ПЛАНОВОГО доходу місяця
// (reserveMonthShare від MonthPlan.PlanUAH), а різалась вона з чого
// завгодно — тобто застосунок казав «подушку наповнює план» і забирав
// купон. Налаштування reserve_fill_from називає, з яких грошей вирізка
// має право брати, і сюди воно доходить одним числом — eligibleUAH.
//
// Числом, а не словом «джерело», навмисно: зведений рядок «купон 817 ₴ +
// погашення 10 000 ₴ того самого дня» — ОДНА подія з двома природами
// (readyFlow.Principal їх уже розділяє), і одне слово на неї означало б
// або віддати подушці купон, або відібрати в неї тіло. Обидва варіанти
// неправда, і жоден не можна пояснити рядком на екрані.
//
// ЧОГО ТУТ НЕМАЄ — обмеження залишком на рахунках. Гроші щойно прийшли, на
// брокері їх ще може не бути, і зрізати розкладку сьогоднішньою готівкою
// означало б відповісти на питання, якого не ставили.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
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

// Префікси SourceRef. Рядком, а не двома полями id: два поля вимагали б
// правила «що робити, коли задані обидва», а відповіді на нього немає.
const (
	allocRefFlow    = "flow:"
	allocRefReceipt = "receipt:"
)

// Джерело грошей у розкладці. Двох слів досить: природу ПОРТФЕЛЬНИХ грошей
// несе окреме число (скільки з них — повернення тіла), і третє слово
// дублювало б його з ризиком розійтись.
const (
	allocFromPlan      = "plan"
	allocFromPortfolio = "portfolio"
)

// reserveFillFrom — рівень політики словом, із дефолтом.
//
// Порожньо й будь-що невідоме читаються як "any", тобто як поведінка до
// появи ключа: налаштування, яке мовчки вимикає подушку, було б найгіршим
// виглядом помилки. Від друкарської описки стереже перелік у реєстрі, але
// в базі можуть лежати й дані, старші за цей ключ.
func reserveFillFrom(set *state.SettingsDoc) string {
	if set == nil {
		return "any"
	}
	return fillFromLevel(set.ReserveFillFrom)
}

// goalsFillFrom — те саме для цілей накопичення. Окремий ключ, а не
// спільний із подушкою: питання однакове за формою й різне за суттю.
// Подушку багато хто свідомо наповнює лише зарплатою, а цілі — усім, що
// прийде, бо на авто збирають і з премії, і з купона.
func goalsFillFrom(set *state.SettingsDoc) string {
	if set == nil {
		return "any"
	}
	return fillFromLevel(set.GoalsFillFrom)
}

// fillFromLevel — рівень політики словом, із дефолтом.
//
// Порожньо й будь-що невідоме читаються як "any", тобто як поведінка до
// появи ключа: налаштування, яке мовчки вимикає механізм, було б найгіршим
// виглядом помилки. Від друкарської описки стереже перелік у реєстрі, але
// в базі можуть лежати й дані, старші за ключ.
func fillFromLevel(raw string) string {
	switch v := strings.TrimSpace(raw); v {
	case "redeem", "plan":
		return v
	default:
		return "any"
	}
}

// reserveEligibleUAH — скільки з цих грошей політика дозволяє віддати подушці.
//
// ОДНЕ ОЗНАЧЕННЯ НА ДВОХ ЧИТАЧІВ: ручну розкладку (POST /api/allocate) і
// прохід маршруту. Друга копія означала б, що сторінка маршруту веде купон
// у папери, а розкладка того ж дня ріже з нього подушку — рівно та
// розбіжність, проти якої стоїть TestRouteFirstLegEqualsAllocate.
//
// sourceCapUAH — стеля САМОГО ДЖЕРЕЛА: скільки з цих грошей дозволяє
// подушці той, хто їх приніс (plan_flows.uses, 0041). Дві межі, а не
// одна, бо питання різні: політика каже «з яких грошей узагалі», джерело
// — «з ЦИХ конкретно». Мінімум із двох, бо жодна з них не має права
// розширити іншу.
//
// ЧИСЛОМ, а не словом «дозволено», і з того самого доводу, що при
// Principal: нога маршруту буває зведеною (місяць плану — це десяток
// потоків із різними дозволами), і одне слово на неї було б неправдою
// для половини суми.
func reserveEligibleUAH(set *state.SettingsDoc, src string,
	amountUAH, principalUAH, sourceCapUAH float64) float64 {

	return math.Min(eligibleUAH(reserveFillFrom(set), src, amountUAH, principalUAH),
		math.Max(0, sourceCapUAH))
}

// ТУТ БУЛИ debtEligibleUAH І debtFillFrom — дозвіл дострокового погашення.
// Пішли разом із самою вирізкою: розкладка більше не веде гроші в борг
// (довід — у місці, де вирізка стояла). Ключ debt_fill_from лишився без
// жодного читача й прибраний із реєстру налаштувань.

// goalsEligibleUAH — те саме для цілей, і навмисно ТІЄЮ САМОЮ функцією:
// правило «що таке планові гроші» одне на застосунок, і друга його копія
// дала б подушці й цілям різні відповіді про той самий купон.
func goalsEligibleUAH(set *state.SettingsDoc, src string,
	amountUAH, principalUAH, sourceCapUAH float64) float64 {

	return math.Min(eligibleUAH(goalsFillFrom(set), src, amountUAH, principalUAH),
		math.Max(0, sourceCapUAH))
}

func eligibleUAH(level, src string, amountUAH, principalUAH float64) float64 {
	switch level {
	case "plan":
		if src == allocFromPlan {
			return amountUAH
		}
		return 0
	case "redeem": //nolint:goconst // рівні політики названі в реєстрі, тут вони читаються
		if src == allocFromPlan {
			return amountUAH
		}
		// Повернення тіла — не заробіток портфеля, а власні гроші, що вийшли
		// з паперу. Обрізаємо сумою: тіло приходить із події, сума — з
		// горщика, і в маршруті друге буває меншим за перше.
		return math.Min(amountUAH, math.Max(0, principalUAH))
	default:
		return amountUAH
	}
}

// allocAllow — що дозволяє САМЕ ДЖЕРЕЛО цих грошей.
//
// ReserveUAH/GoalsUAH приходять уже готовими: політика (reserve_fill_from,
// goals_fill_from) і дозвіл джерела в них зведені мінімумом ще в
// reserveEligibleUAH/goalsEligibleUAH. Uses ж потрібен окремо й сирим — по
// ньому вирішується доля ВИДІВ інструментів, у яких грошового ліміту
// немає взагалі: «сюди можна» або «сюди ні».
//
// НУЛЬОВЕ ЗНАЧЕННЯ СТРУКТУРИ ОЗНАЧАЄ «БЕЗ ОБМЕЖЕНЬ ЗА ВИДАМИ» — так само,
// як порожній uses у сховищі. Це не випадковість, а страховка: булеани
// «дозволено» дали б протилежний дефолт, і структура, зібрана не до
// кінця, мовчки заборонила б усе.
type allocAllow struct {
	ReserveUAH float64
	GoalsUAH   float64
	// Uses — канонічний дозвіл джерела ("" = будь-куди), читається через
	// domain.PlanUseAllowed. Рядком, а не парою булеанів: словник кошиків
	// живе в домені, і друга його копія тут розійшлася б із першою рівно
	// тоді, коли додасться п'ятий кошик.
	Uses string
	// PickISIN — папір, який людина обрала сама для ЦЬОГО надходження.
	//
	// Це не дозвіл, а вибір, і стоїть він тут поруч із дозволами тому, що
	// відповідає на те саме питання «що можна робити з цими грошима» — лише
	// ствердно, а не заборонно. Бюджет виду ОВДП іде в цей папір І ТІЛЬКИ В
	// НЬОГО: коли квиток дорожчий за бюджет, рядка ОВДП не буде зовсім, а
	// гроші чекатимуть. Мовчки підставити вершину рейтингу замість вибору
	// не можна — це рівно те, від чого людина втекла, обираючи.
	//
	// Порожньо = рейтинг, тож нульове значення структури й далі означає
	// «без обмежень», як обіцяно вище.
	PickISIN string
}

// allocLine — один крок розкладки.
//
// Addable каже, чи можна цей рядок покласти в план купівель одним рухом.
// Хибне воно рівно у вкладу, і це не недогляд: порада про вклад — це
// ПОПОВНЕННЯ наявного (або «новий вклад» без банку), а plan_buys описує
// НОВИЙ вклад і вимагає строку, якого в пораді немає. Рядок, що записав би
// пів-вкладу, гірший за рядок, який чесно веде у форму поповнення.
type allocLine struct {
	Kind     string `json:"kind"` // bond | fund | deposit | npf
	Ref      string `json:"ref"`  // ISIN, назва фонду, банк, id рахунку НПФ
	Label    string `json:"label"`
	Currency string `json:"currency"`
	// Qty / Unit — для паперу й сертифіката; Amount — для вкладу й внеску.
	// Кожен вид відповідає на своє питання «скільки», і та сама межа вже
	// закріплена у planBuyFromReq.
	//
	// Вказівниками, а не значеннями: omitempty на структурі не діє, і рядок
	// паперу віз би порожній `amount:{"amount":"","currency":""}`, який у
	// браузері читається як «сума є, вона нульова» — тобто «0 ₴» замість
	// прочерку.
	Qty      int64       `json:"qty,omitempty"`
	Unit     *moneyJSON  `json:"unit,omitempty"`
	Amount   *moneyJSON  `json:"amount,omitempty"`
	TotalUAH state.Money `json:"total_uah"`
	RealPct  float64     `json:"real_pct"`
	Why      string      `json:"why"`
	Addable  bool        `json:"addable"`
	// Convert — валюта кроку не збігається з валютою надходження.
	// ConvertNative — скільки самого надходження на це піде.
	//
	// Рядок при цьому НЕ ховається: сховати єдину доступну пораду тому, що
	// вона в іншій валюті, означало б відповісти «нема куди» там, де
	// відповідь — «поміняй спершу гроші». Мовчки конвертувати теж не можна,
	// тому позначка й число.
	Convert       bool        `json:"convert,omitempty"`
	ConvertNative state.Money `json:"convert_native,omitzero"`
	// Picked — папір у цьому рядку обрала людина, а не рейтинг
	// (allocAllow.PickISIN). Сторінка підписує такий рядок «твій вибір» і
	// пропонує його скинути; без позначки вибір і порада виглядали б однаково.
	Picked bool `json:"picked,omitempty"`
	// Звідки взялась ціна кроку й у кого вона така.
	//
	// CostBasis тут БЕЗ omitempty з тієї ж причини, що й у пораді:
	// підстава є завжди, і порожнє поле читалось би як «невідомо». Рядок,
	// порахований за номіналом, мусить це сказати — інакше база ціни
	// мовчки мінялася б між натисканнями, і на екрані це виглядало б як
	// рух ринку, а не як поява чи протухання котировки.
	//
	// CostAlt / CostAltWhere несуть наступну ціну серед ТВОЇХ брокерів —
	// заради того одного речення, яке робить «найдешевше» перевірюваним.
	// Жодної нової арифметики тут немає: усі чотири поля копіюються з
	// поради, яка вже їх порахувала (allocOne).
	CostBasis      string     `json:"cost_basis"`
	CostAsOf       string     `json:"cost_as_of,omitempty"`
	CostWhere      string     `json:"cost_where,omitempty"`
	CostWhereLabel string     `json:"cost_where_label,omitempty"`
	CostAlt        *moneyJSON `json:"cost_alt,omitempty"`
	CostAltWhere   string     `json:"cost_alt_where,omitempty"`
}

// allocReserve — вирізка подушки. Окремим полем, а не рядком у Lines: у
// резерву немає ні ціни кроку, ні дохідності, ні рядка в кошику, і
// поставити його в один список із покупками означало б або вигадати йому
// ці числа, або завести для нього особливий випадок у кожному читачі.
type allocReserve struct {
	AmountUAH state.Money `json:"amount_uah"`
	Why       string      `json:"why"`
}

// allocPlan.ReserveSkipWhy — чому подушка НЕ взяла те, що мала б узяти за
// стелею. Причина тут рівно одна — політика reserve_fill_from, — і сказати
// її обовʼязково: зниклий рядок вирізки без пояснення читається як
// поломка, а не як «ці гроші за твоїм рішенням ідуть у папери». Те саме
// правило, за яким мовчазного залишку не буває (RestWhy).

// allocGoalCut — вирізка однієї цілі накопичення.
//
// Масив, а не одне число, і не рядок у Lines. Цілей буває кілька, у кожної
// своя назва й своя причина («до березня бракує 24 000 ₴/міс»), а
// показувати їх однією сумою означало б сказати «на цілі — 8 000» і не
// сказати, на які саме. У Lines же їм не місце з того самого доводу, що й
// подушці: немає ні ціни кроку, ні дохідності, ні рядка в плані купівель.
type allocGoalCut struct {
	ID        int64       `json:"id"`
	Name      string      `json:"name"`
	AmountUAH state.Money `json:"amount_uah"`
	Why       string      `json:"why"`
}

type allocPlan struct {
	Amount    moneyJSON     `json:"amount"`
	AmountUAH state.Money   `json:"amount_uah"`
	Reserve   *allocReserve `json:"reserve,omitempty"`
	// Goals — вирізки цілей, у порядку наповнення. GoalsSkipWhy — чому цілі
	// не взяли свого: те саме правило, що в ReserveSkipWhy, і той самий
	// довід — зникла вирізка без пояснення читається як поломка.
	Goals        []allocGoalCut `json:"goals,omitempty"`
	GoalsUAH     state.Money    `json:"goals_uah,omitzero"`
	GoalsSkipWhy string         `json:"goals_skip_why,omitempty"`
	// ReserveSkipWhy — аргумент вище, при allocReserve.
	ReserveSkipWhy string      `json:"reserve_skip_why,omitempty"`
	AvailUAH       state.Money `json:"avail_uah"`
	Lines          []allocLine `json:"lines"`
	// RestUAH — те, що не склалося в цілі квитки. RestWhy називає причину
	// словами: сума без пояснення читається як загублена.
	RestUAH state.Money `json:"rest_uah,omitzero"`
	RestWhy string      `json:"rest_why,omitempty"`
	// Note — чому рядків немає взагалі. Порожня відповідь без причини
	// читається як поломка, а причин рівно три: усе забрала подушка, цілей
	// за видом не задано, або на жоден цілий крок не вистачило.
	Note string `json:"note,omitempty"`
}

func (s *Server) handleAllocate(w http.ResponseWriter, r *http.Request) {
	var req allocateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cur := orUAH(strings.TrimSpace(req.Currency))
	minor, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("сума: %w", err))
		return
	}
	if minor <= 0 {
		writeErr(w, http.StatusBadRequest, badRequestf("сума розкладки має бути > 0"))
		return
	}
	src := allocFromPlan
	if strings.TrimSpace(req.Source) == allocFromPortfolio {
		src = allocFromPortfolio
	}
	var principalMinor int64
	if s := strings.TrimSpace(req.Principal); s != "" {
		if principalMinor, err = domain.ParseDecimalToMinor(s, cur); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("тіло: %w", err))
			return
		}
		if principalMinor < 0 {
			writeErr(w, http.StatusBadRequest, badRequestf("тіло не буває відʼємним"))
			return
		}
	}
	now := time.Now()
	doc, err := s.buildState(r.Context(), now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rates, err := s.rates(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	uahM, err := fx.ToUAH(money.New(minor, cur), rates)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sug, err := s.reinvestSuggestions(r.Context(), now, doc)
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
	uses, err := s.usesForRef(r.Context(), req.SourceRef)
	if err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	pick, err := pickSuggestion(sug, req.PickISIN)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Стеля джерела — усе або нічого, і саме тому вона тут не число з
	// журналу, а сума розкладки: розкладають ОДНЕ надходження, і дозвіл у
	// нього один. Дробові стелі бувають лише в маршруту, і вже не тому, що
	// нога зводить цілий місяць (вона більше не зводить — див. planAhead), а
	// тому, що горщик там накопичує кілька надходжень із різними дозволами.
	out := allocatePlan(doc, sug, rates,
		toMoneyJSON(money.New(minor, cur)), amountUAH,
		allocAllow{
			ReserveUAH: reserveEligibleUAH(doc.Settings, src, amountUAH, principalUAH,
				sourceCapUAH(uses, domain.UsePlanReserve, amountUAH)),
			GoalsUAH: goalsEligibleUAH(doc.Settings, src, amountUAH, principalUAH,
				sourceCapUAH(uses, domain.UsePlanGoals, amountUAH)),
			Uses:     uses,
			PickISIN: pick,
		}, cur, s.npfIDByName(r.Context()))
	if err := s.present(r.Context(), &out); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// pickSuggestion — чи є обраний папір серед порад, і сам ISIN очищеним.
//
// Перевіряти обовʼязково, і саме тут, а не в allocatePlan: та — чиста
// функція без помилки в сигнатурі, і невідомий ISIN у ній мовчки дав би
// ногу без жодного рядка ОВДП із причиною «інструментів немає» — неправдою
// про довідник. Порад же бракує рівно двом паперам: погашеному й тому, в
// якого немає графіка виплат (domain.YTM без майбутніх виплат відмовляє), і
// обидва людина обрати може лише помилково.
//
// Один читач на два ендпойнти (розкладка й маршрут) — привід виносити, а не
// копіювати: різні тексти відмови на одному й тому самому ISIN читались би
// як різні причини.
func pickSuggestion(sug []suggestion, isin string) (string, error) {
	isin = strings.ToUpper(strings.TrimSpace(isin))
	if isin == "" {
		return "", nil
	}
	for i := range sug {
		if sug[i].Kind == "bond" && sug[i].ISIN == isin {
			return isin, nil
		}
	}
	return "", badRequestf("паперу %s немає серед порад — він або погашений, або без графіка виплат",
		isin)
}

// sourceCapUAH — стеля джерела для одного кошика: уся сума або нуль.
//
// Проміжних значень тут не буває за побудовою: дозвіл — це «можна» або
// «ні», а СКІЛЬКИ саме взяти, вирішує стеля наповнення. Число ж замість
// булеана тому, що далі його чекає мінімум із політикою, і два різні типи
// на одному рядку довелось би зводити руками (аргумент при
// reserveEligibleUAH).
func sourceCapUAH(uses, bucket string, amountUAH float64) float64 {
	if domain.PlanUseAllowed(uses, bucket) {
		return amountUAH
	}
	return 0
}

// usesForRef — дозвіл того надходження, яке розкладають.
//
// Читає СХОВИЩЕ, а не тіло запиту, і це не педантизм: дозвіл вирішує, чи
// піде вирізка в подушку, тож взяти його зі сторінки означало б дати
// застарілій вкладці право обійти щойно поставлену заборону.
//
// Невідоме посилання — помилка, а не «обмежень немає». Мовчазний дефолт
// тут був би найгіршим виглядом збою: розкладка виглядала б звичайною й
// різала б подушку з грошей, яким це заборонено.
func (s *Server) usesForRef(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	switch {
	case strings.HasPrefix(ref, allocRefFlow):
		id, err := strconv.ParseInt(strings.TrimPrefix(ref, allocRefFlow), 10, 64)
		if err != nil || id <= 0 {
			return "", badRequestf("джерело розкладки: %q не схоже на %s<id>", ref, allocRefFlow)
		}
		flows, err := s.st.ListPlanFlows(ctx)
		if err != nil {
			return "", err
		}
		for _, f := range flows {
			if f.ID == id {
				return f.Uses, nil
			}
		}
		return "", fmt.Errorf("джерело доходу %d %w", id, store.ErrNotFound)
	case strings.HasPrefix(ref, allocRefReceipt):
		id, err := strconv.ParseInt(strings.TrimPrefix(ref, allocRefReceipt), 10, 64)
		if err != nil || id <= 0 {
			return "", badRequestf("джерело розкладки: %q не схоже на %s<id>", ref, allocRefReceipt)
		}
		receipts, err := s.st.ListPlanReceipts(ctx)
		if err != nil {
			return "", err
		}
		for _, rc := range receipts {
			if rc.ID != id {
				continue
			}
			// Прив'язана відмітка своєї колонки не читає — дозвіл їй задає
			// потік. Та сама підстановка, що в receiptRows, і без неї
			// «інше» й «зарплата» відповідали б на дозвіл по-різному.
			if rc.FlowID == 0 {
				return rc.Uses, nil
			}
			return s.usesForRef(ctx, allocRefFlow+strconv.FormatInt(rc.FlowID, 10))
		}
		return "", fmt.Errorf("відмітка надходження %d %w", id, store.ErrNotFound)
	}
	return "", badRequestf("джерело розкладки: %q — буває %s<id> або %s<id>",
		ref, allocRefFlow, allocRefReceipt)
}

// npfIDByName — id рахунків НПФ за назвою.
//
// Id береться зі СХОВИЩА, а не з поради: doc.NPF несе назву (саме її й
// видно на екрані), а план тримається за id, щоб виправлення описки в
// назві не відчепило внески — розходження між двома ключами пояснене при
// domain.NPFPlanDest.
//
// Окремою функцією, бо читачів двоє: розкладка суми, що вже прийшла, і
// маршрут грошей, які ще прийдуть (route.go). Друга копія цього циклу
// означала б, що дві відповіді на «у котрий пенсійний вносити» можуть
// розійтись — а розійтись вони можуть рівно в тому випадку, заради якого
// нуль нижче й стоїть.
func (s *Server) npfIDByName(ctx context.Context) map[string]int64 {
	out := map[string]int64{}
	accs, err := s.st.ListNPFAccounts(ctx)
	if err != nil {
		return out
	}
	for _, a := range accs {
		if _, dup := out[a.Name]; dup {
			// Дві однакові назви — і сказати, у котрий саме рахунок вносити,
			// нема з чого. Нуль вимикає рядок, замість того щоб вгадати
			// навмання.
			out[a.Name] = 0
			continue
		}
		out[a.Name] = a.ID
	}
	return out
}

// allocMinCutUAH — найменше призначення, яке має право стати рядком.
//
// Це поріг не про гроші, а про РУХ. Пенсійний внесок порога входу справді
// не має — саме це тут і було записано поруч, — але поріг має людина:
// переказ на 3 ₴ коштує рівно стільки ж уваги й стільки ж кроків, скільки
// переказ на 3 000 ₴. На живих даних таких рядків вийшло шістнадцять із
// сорока семи ніг маршруту (0,26–9,88 ₴), і в кожній із тих шістнадцяти
// рядок був ЄДИНИМ призначенням ноги. Порада, якої ніхто не виконає, гірша
// за її відсутність: вона витісняє з екрана ту, яку виконають.
//
// ОДНЕ ЧИСЛО НА ТРИ ВИРІЗКИ — подушку, ціль і внесок у пенсійний. Три
// пороги розійшлися б на першій же правці, а питання в них одне. Квиток
// ОВДП свого порога вже має (allocOne), і зводити їх в одне не можна: то
// ціна цілої штуки, а це межа доцільності руху.
//
// ГРОШІ НЕ ГИНУТЬ НІДЕ. Пропущена вирізка лишається в сумі, що
// розкладається далі; пропущений внесок — у RestUAH, який маршрут везе до
// наступного надходження. Це та сама механіка, що вже возить недобраний
// квиток, тому нового правила тут не зʼявляється — лише ще один вид, який
// нарешті вміє сказати «на цей рух ще не набралось».
const allocMinCutUAH = 10

// allocKind — вид інструмента поради в термінах ребалансу. Мапа, а не
// switch у трьох місцях: ті самі чотири пари вже стоять у kindRows
// (buy-plan.js) і в kindTargets (state_rebalance.go), і п'ята копія
// розійшлася б із ними тихо.
var allocKind = map[string]string{
	"bond": "bonds", "fund": "funds", "deposit": "deposits", "npf": "npf",
}

// allocatePlan — уся розкладка. Чиста функція над готовим документом: саме
// тому її можна перевірити тестом, не піднімаючи сервера.
func allocatePlan(doc *state.Doc, sug []suggestion, rates fx.Rates,
	amount moneyJSON, amountUAH float64, allow allocAllow,
	cur string, npfID map[string]int64) allocPlan {
	mt := moneyTextOf(doc)

	out := allocPlan{Amount: amount, AmountUAH: state.Major(amountUAH, money.UAH), Lines: []allocLine{}}

	// --- подушка першою ---
	avail := amountUAH
	if doc.Reserve != nil && doc.Reserve.FillNowUAH.Major() > 0 {
		want := math.Min(amountUAH, doc.Reserve.FillNowUAH.Major())
		cut := math.Min(want, math.Max(0, allow.ReserveUAH))
		if blocked := want - cut; blocked > 0.005 {
			out.ReserveSkipWhy = reserveSkipWhy(mt, doc.Settings, blocked, cut,
				!domain.PlanUseAllowed(allow.Uses, domain.UsePlanReserve))
		}
		// ПОРІГ, І ВИНЯТОК ІЗ НЬОГО — ЗАКРИТТЯ РОЗРИВУ. Якщо цієї вирізки
		// досить, щоб розрив зник, поріг мовчить: інакше остання пʼятірка
		// гривень до цілі не закрилась би НІКОЛИ — стеля щомісяця пропонувала
		// б її знову, а поріг щомісяця відмовляв би, і подушка назавжди
		// лишилась би «майже зібраною».
		//
		// GapUAH > 0.005 у першій половині умови обовʼязкове: прохід маршруту
		// жене розрив до нуля, лишаючи FillNowUAH додатним (route.go, apply),
		// і без цієї перевірки копійчана вирізка пролізла б у вже закритий
		// розрив саме дверима винятку.
		if closes := doc.Reserve.GapUAH.Major() > 0.005 &&
			cut >= doc.Reserve.GapUAH.Major()-0.005; cut > 0.005 && cut < allocMinCutUAH && !closes {
			why := allocBelowFloorWhy(mt, "подушка", "не бере", "добере", cut)
			if out.ReserveSkipWhy != "" {
				// Політика вже сказала, ЧОМУ вирізка схудла до цього числа;
				// поріг каже, чому й цього числа не стало рядком. Обидві
				// правди, і замовчати першу означало б повести в поріг того,
				// кому насправді треба в «Політику».
				why = out.ReserveSkipWhy + ". " + why
			}
			out.ReserveSkipWhy, cut = why, 0
		}
		if cut > 0.005 {
			why := fmt.Sprintf("місячна частка подушки — %s з %s",
				mt.uah(cut), mt.uah(doc.Reserve.FillMonthUAH.Major()))
			switch {
			case cut < doc.Reserve.FillNowUAH.Major():
				why += "; більше з цієї суми не вийде — решту добере наступне надходження"
			case doc.Reserve.GapUAH.Major() > 0:
				why += fmt.Sprintf("; до цілі ще %s", mt.uah(doc.Reserve.GapUAH.Major()))
			}
			out.Reserve = &allocReserve{AmountUAH: state.Major(cut, money.UAH), Why: why}
			avail = amountUAH - cut
		}
	}
	// --- БОРГУ ТУТ БІЛЬШЕ НЕМАЄ, і це не спрощення, а виправлення контуру ---
	//
	// Стояла вирізка дострокового: між подушкою й цілями, за стелею
	// debt_fill_share_pct, і вона зменшувала avail — тобто гроші, які
	// далі діляться за цільовими частками видів.
	//
	// ЧОМУ ЦЕ БУЛО НЕПРАВИЛЬНО. Гроші течуть двома контурами: Gross =
	// Income (доходить до портфеля за invest_bp) + OnCard (усе інше —
	// лишається там, куди прийшло, і витрачається з картки). Обовʼязковий
	// платіж уже давно гаситься з ДРУГОГО, і до плану доходить лише
	// переповнення (MonthPlan.DebtFromPlanUAH). Дострокове ж бралось із
	// ПЕРШОГО: стеля рахувалась від PlanDebtUAH, тобто від портфельних
	// грошей. Виходило, що людина ставить частки ОВДП і фондів, а борг
	// мовчки з'їдає базу, від якої ці частки міряються.
	//
	// Власник сформулював це так: «борг має відніматись від планового
	// доходу, але не впливати на ті відсотки які я вже зазначив шо буду
	// класти в портфель».
	//
	// ЧОМУ ПРОСТО ПРИБРАНО, А НЕ ПЕРЕНЕСЕНО НА КАРТКОВИЙ КОНТУР. Перенос
	// вимагав би, щоб стелю витрат (buildDebtExit) навчили віднімати
	// дострокове ПОМІСЯЧНО на весь горизонт — а це той самий каскад, від
	// якого відмовилась шапка міграції 0056 («вимагав би в buildDebtExit
	// другого означення "скільки план не поглинув"»). Дострокове погашення
	// лишається свідомою дією за стелею, яку видно на сторінці боргу
	// (плитка «Достроково ще»), і рахується вона тепер від карткових
	// грошей. Вибір робить людина, а не розкладка.
	//
	// Наслідок, який треба знати: avail тепер більший рівно на цю вирізку,
	// тобто розкладка й кожна нога маршруту купують більше паперів.

	// --- цілі накопичення, ДРУГИМИ ---
	//
	// Після подушки й лише з того, що після неї лишилось. Порядок не
	// стилістичний: аварія не має дати й може статись завтра, а річ, на яку
	// збирають, дату має — на те вона й ціль. Пустити цілі поперед подушки
	// означало б платити за передбачуване з грошей, відкладених на
	// непередбачуване.
	//
	// Ліміт свій: goals_fill_from ріже незалежно від reserve_fill_from, і
	// обрізається він ще й тим, що подушка вже забрала (гроші не можна
	// віддати двічі).
	// elig ЖИВЕ ПОЗА БЛОКОМ, бо читачів у нього тепер двоє: цей цикл і
	// другий прохід залишку. Порахувати його там удруге означало б завести
	// друге означення «скільки цілям іще можна», і розійшлись би вони рівно
	// тоді, коли перша вирізка була частковою.
	//
	// Значення на виході з циклу вже правильне: гілка порога робить continue
	// ДО elig -= cut, тож пропущена вирізка й далі лічиться дозволеною.
	elig := math.Min(avail, math.Max(0, allow.GoalsUAH))
	if avail > 0 {
		// Два лічильники, а не один: «сюди не можна» і «сюди замало» ведуть у
		// різні місця — перше в «Політику», друге нікуди, бо воно про саме
		// число. Звести їх в одну фразу означало б послати людину міняти
		// настройку, яка ні при чому.
		blocked, floored := 0.0, 0.0
		for i := range doc.Goals {
			g := &doc.Goals[i]
			if g.FillNowUAH.Major() <= 0 {
				continue
			}
			want := math.Min(avail, g.FillNowUAH.Major())
			cut := math.Min(want, elig)
			if b := want - cut; b > 0.005 {
				blocked += b
			}
			if cut <= 0.005 {
				continue
			}
			// Той самий поріг і той самий виняток, що в подушки: ціль, до якої
			// лишилось чотири гривні, мусить мати право закритись, а ціль, якій
			// цього місяця перепало чотири гривні з тридцяти тисяч, — ні.
			//
			// continue стоїть ПЕРЕД avail -= cut і elig -= cut, тож пропущена
			// вирізка справді лишається доступною далі — і паперам цієї ноги, і
			// цілям наступної.
			if closes := g.GapUAH.Major() > 0.005 && cut >= g.GapUAH.Major()-0.005; cut < allocMinCutUAH && !closes {
				floored += cut
				continue
			}
			why := fmt.Sprintf("місячна частка цілі — %s з %s", mt.uah(cut), mt.uah(g.FillMonthUAH.Major()))
			switch {
			case cut < g.FillNowUAH.Major():
				why += "; більше з цієї суми не вийде — решту добере наступне надходження"
			case g.GapUAH.Major() > 0:
				why += fmt.Sprintf("; до цілі ще %s", mt.uah(g.GapUAH.Major()))
			}
			if g.ShortMonthUAH.Major() > 0 {
				why += fmt.Sprintf(". Щоб устигнути до %s, треба ще %s на місяць — стеля стільки не дає",
					g.DueDate, mt.uah(g.ShortMonthUAH.Major()))
			}
			out.Goals = append(out.Goals, allocGoalCut{
				ID: g.ID, Name: g.Name, AmountUAH: state.Major(cut, money.UAH), Why: why,
			})
			out.GoalsUAH = state.Major(out.GoalsUAH.Major()+cut, money.UAH)
			avail -= cut
			elig -= cut
		}
		if blocked > 0.005 {
			out.GoalsSkipWhy = goalsSkipWhy(mt, doc.Settings, blocked, out.GoalsUAH.Major(),
				!domain.PlanUseAllowed(allow.Uses, domain.UsePlanGoals))
		}
		if floored > 0.005 {
			why := allocBelowFloorWhy(mt, "цілі", "не беруть", "доберуть", floored)
			if out.GoalsSkipWhy != "" {
				why = out.GoalsSkipWhy + ". " + why
			}
			out.GoalsSkipWhy = why
		}
	}

	out.AvailUAH = state.Major(avail, money.UAH)
	if avail <= 0 {
		out.Note = allocAllTakenNote(out)
		return out
	}

	// --- бюджети видів ---
	//
	// spreadMonth рахує на КОПІЇ рядків документа, і копія обнуляється перед
	// викликом. Без цього вид, для якого поділ нічого не дасть, лишився б із
	// числом ВІД ПЛАНУ МІСЯЦЯ — тобто розкладка 500 ₴ могла б порадити
	// купити на тридцять тисяч (spreadMonth виходить одразу, коли ділити
	// нічого, і чужих чисел за собою не прибирає).
	rows := make([]state.RebalanceRow, len(doc.Rebalance))
	copy(rows, doc.Rebalance)
	for i := range rows {
		rows[i].MonthShareUAH, rows[i].MonthBalanceUAH = state.Money{}, state.Money{}
	}
	// ЗАБОРОНЕНИЙ ВИД ГАСИТЬСЯ ЦІЛЛЮ, А НЕ ВИКИДАЄТЬСЯ ПІСЛЯ ПОДІЛУ.
	//
	// Різниця не стилістична. Викинути рядок після spreadMonth означало б
	// лишити його частку безіменним RestUAH — тобто застосунок сказав би
	// «на цілий крок не набралось» там, де правда інша: «цим грошам туди
	// не можна». З обнуленою ціллю частка перетікає в дозволені види, і
	// сума розкладки далі дорівнює тому, що прийшло.
	//
	// Гроші при цьому НЕ перекидаються між видами всередині поділу — це
	// зламало б вирівнювання, заради якого поділ і робиться. Вид просто
	// перестає існувати для цієї суми, як перестає для того, кому ціль за
	// ним не задана взагалі.
	for i := range rows {
		if rows[i].Dimension != "kind" || !allocKindForbidden(allow.Uses, rows[i].Key) {
			continue
		}
		rows[i].TargetPct = 0
	}
	// База поділу — капітал БЕЗ подушки й БЕЗ цілей накопичення. Обидві
	// суми в капіталі є (гроші існують), але жодну з них не збираються
	// вкладати: перша чекає на аварію, друга — на річ у названу дату.
	// Лишити їх у базі означало б ділити гроші місяця на знаменник, у
	// якому частина ніколи не стане папером.
	needs := spreadMonth(rows, avail, doc.CapitalUAH.Major()-doc.ReserveUAH.Major()-doc.GoalsUAH.Major())

	type kindBudget struct {
		key string
		uah float64
		// need — недобір цього виду до частки, у гривнях, і він БІЛЬШИЙ за
		// бюджет щоразу, коли потреб більше, ніж грошей. Другий прохід
		// упорядковує приймачі саме за ним, і саме тому число приїжджає зі
		// spreadMonth, а не рахується тут удруге.
		need float64
	}
	var budgets []kindBudget
	for i, r := range rows {
		if r.Dimension != "kind" || r.TargetPct <= 0 || r.MonthBalanceUAH.Major() <= 0 {
			continue
		}
		n := 0.0
		if i < len(needs) {
			n = needs[i]
		}
		budgets = append(budgets, kindBudget{key: r.Key, uah: r.MonthBalanceUAH.Major(), need: n})
	}
	// ПОРОЖНІЙ ПЕРЕЛІК БЮДЖЕТІВ БІЛЬШЕ НЕ Є РАННІМ ВИХОДОМ.
	//
	// Доти тут стояв return, і це робило «одне означення на всі екрани»
	// неправдою рівно в трьох станах: цілей за видом не задано, усі види
	// перебрані, або джерелу дозволені самі лише подушка й цілі. А це саме
	// ті стани, у яких залишок — ЄДИНІ гроші, які подушка ще могла б узяти.
	//
	// Тому обидві причини перерахуються ПІСЛЯ проходу (allocNoteAfterTopUp):
	// фраза «розкладати нема за яким правилом», надрукована над вирізкою
	// подушки, суперечила б сама собі на одному екрані.
	// Найбільший розрив першим, ключ другим критерієм: два однакові бюджети
	// інакше стали б у порядку, різному від запуску до запуску.
	//
	// ПОРІВНЯННЯ ТОЧНЕ, ПО ОКРУГЛЕНОМУ ЧИСЛУ, а не «різниця більша за
	// епсилон». Епсилон тут виглядав охайніше й був НЕПРАВИЛЬНИЙ: відношення
	// «майже рівні» не транзитивне (a≈b, b≈c, але a>c), тобто less не є
	// строгим слабким порядком, а sort.Slice на такому less не зобовʼязаний
	// давати осмислений результат узагалі. Копійчані бюджети — не рідкість,
	// і зловити це на очі неможливо: порядок просто інший, ніж мав би бути.
	//
	// round2 тому, що гривня однаково округлюється перед показом: два
	// бюджети, які на екрані одне й те саме число, мусять і впорядкуватись
	// за ключем, а не за невидимою третьою цифрою.
	sort.Slice(budgets, func(i, j int) bool {
		a, b := round2(budgets[i].uah), round2(budgets[j].uah)
		if a != b {
			return a > b
		}
		return budgets[i].key < budgets[j].key
	})

	// --- бюджет → цілі квитки ---
	//
	// roomByKind — скільки цьому виду ще БРАКУЄ до частки після того, як він
	// витратив свій бюджет. Потрібне другому проходу: залишок має право
	// доїхати лише туди, де недобір іще живий, і рівно до нього.
	roomByKind := map[string]float64{}
	rest, cheapest, cheapestWhat := 0.0, 0.0, ""
	if len(budgets) == 0 {
		// Ділити нема на що — уся сума стає залишком і йде в другий прохід.
		// Доти це робив ранній вихід; тепер сюди мусить дійти саме число,
		// інакше прохід дістав би нуль і мовчав би там, де подушка ще могла
		// б узяти всі ці гроші.
		rest = avail
	}
	for _, b := range budgets {
		left := b.uah
		for i := range sug {
			sg := sug[i]
			if allocKind[sg.Kind] != b.key {
				continue
			}
			// Обраний папір ЗАМІСТЬ рейтингу, а не поперед нього: доки вибір
			// названий, решта ОВДП для цієї суми не існує. Довід — при
			// allocAllow.PickISIN.
			if b.key == "bonds" && allow.PickISIN != "" && sg.ISIN != allow.PickISIN {
				continue
			}
			line, spent, ok := allocOne(sg, left, rates, cur, npfID)
			if allow.PickISIN != "" && sg.ISIN == allow.PickISIN {
				line.Picked = true
				// Причина рейтингу лишається за вибором: саме вона несе
				// застереження про ліміт концентрації чи вторинний ринок, і
				// обраний папір їх заслуговує не менше за порадженого.
				line.Why = "твій вибір"
				if sg.Reason != "" {
					line.Why += "; " + sg.Reason
				}
			}
			if !ok {
				// Крок дорожчий за те, що лишилось. Запам'ятовуємо найдешевший
				// НЕДОСЯЖНИЙ крок: саме він і пояснює залишок.
				if c := allocStepUAH(sg, rates, npfID); c > 0 && (cheapest == 0 || c < cheapest) {
					cheapest, cheapestWhat = c, sg.Label
				}
				continue
			}
			allocAddLine(&out.Lines, line)
			left -= spent
			// НПФ бере бюджет виду цілком: ділити внесок на штуки нема на що,
			// тож другого рядка того самого виду в цій нозі не буває.
			if sg.Kind == "npf" {
				break
			}
			// Наступний крок — іще один такий самий папір, і він теж пояснює
			// залишок. Доки причину називали лише дальші рядки рейтингу, нога
			// з єдиним (чи обраним) папером і хвостом у 815 ₴ казала
			// «довідник порожній або без цін» — неправду про довідник, з якого
			// щойно купили. Мінімум із дальшими лишається за наявним порівнянням.
			if c := allocStepUAH(sg, rates, npfID); c > 0 && left > 0.005 &&
				(cheapest == 0 || c < cheapest) {
				cheapest, cheapestWhat = c, sg.Label
			}
		}
		rest += left
		// Недобір ПІСЛЯ бюджету: скільки цьому виду ще бракує до частки, коли
		// свої гроші він уже витратив. Від'ємне не буває — need і так
		// невід'ємний, а витратити більше за бюджет неможливо.
		roomByKind[b.key] = math.Max(0, b.need-(b.uah-left))
	}

	// --- ДРУГИЙ ПРОХІД: залишок тим, хто ще недобирає ---
	rest = allocTopUp(&out, topUpIn{
		rest: rest, mt: mt, rows: rows, rooms: roomByKind, goals: doc.Goals,
		reserve: doc.Reserve, allow: allow, goalsElig: elig,
		sug: sug, rates: rates, cur: cur, npfID: npfID,
		cheapest: &cheapest, cheapestWhat: &cheapestWhat,
	})

	out.RestUAH = state.Major(rest, money.UAH)
	// Причина залишку — три різні речі, і зводити їх до однієї фрази не можна:
	// «бракує 730 ₴» і «інструментів немає взагалі» вимагають різних дій.
	if rest > 0.005 {
		switch {
		case cheapest > rest:
			out.RestWhy = fmt.Sprintf("на наступний крок (%s, %s) бракує %s",
				cheapestWhat, mt.uah(cheapest), mt.uah(cheapest-rest))
		case cheapest > 0:
			// Найдешевший крок дешевший за залишок, і другий прохід його вже
			// пробував. Доти тут стояло «залишок зібраний із різних видів, і в
			// жодному з них своєї частки на цілий крок не набралось» — після
			// проходу це неправда: він саме що зібрав їх докупи й спробував.
			//
			// Отже причина інша й вона одна: вид, чий крок найдешевший, більше
			// НЕ ПРИЙМАЄ цих грошей — або він уже на своїй частці, або цим
			// грошам туди не можна, або до частки лишилось менше, ніж коштує
			// крок. Три стани, але дія в них одна: чекати, і саме це й сказано.
			out.RestWhy = fmt.Sprintf("найдешевший крок (%s, %s) дешевший за залишок, "+
				"але його виду ці гроші вже не потрібні: до частки йому лишилось менше, "+
				"ніж коштує крок, або він її вже добрав, або цим грошам туди не можна",
				cheapestWhat, mt.uah(cheapest))
		default:
			out.RestWhy = "інструментів із відомою ціною в цих видах немає — " +
				"довідник порожній або без цін"
		}
	}
	// Причини, які раніше друкував ранній вихід із порожнім переліком
	// бюджетів. Тепер вони рахуються ТУТ — після проходу, — бо прохід міг
	// віддати ці гроші подушці, і фраза «розкладати нема за яким правилом»
	// над живою вирізкою суперечила б сама собі.
	if len(budgets) == 0 && rest > 0.005 {
		// ДВІ РІЗНІ ПРИЧИНИ, і зводити їх в одну фразу не можна: «цілей не
		// задано» кличе в налаштування, а «сюди не можна» — у сам потік.
		// Перша ще й читалась би як поломка там, де все зроблено за
		// вказівкою людини.
		out.Note = "цілей за видом інструмента не задано, або всі вони вже перебрані — " +
			"розкладати нема за яким правилом"
		if allocSavingsOnly(allow.Uses) {
			out.Note = "цим грошам дозволено лише подушку й цілі, а вони своє вже взяли — " +
				"решта чекає на наступний місяць"
		}
	}
	// І лише коли сказати більше нема чого. RestWhy вище вже назвав, ЯКОГО
	// саме кроку бракує і скільки до нього, — а ця фраза каже те саме
	// загальними словами. Доки обидві стояли поруч, вигравала загальна: у
	// маршруті вона малюється першою (route.js, destHTML), і нога, у якій
	// бракувало вісім гривень до внеску, пояснювала себе фразою «жодного
	// виду не вистачило».
	if len(out.Lines) == 0 && out.Note == "" && out.RestWhy == "" {
		out.Note = "на цілий крок жодного виду не вистачило — гроші чекають на наступне надходження"
	}
	return out
}

// allocAddLine — рядок у набір, ЗЛИТТЯМ за (вид, посилання).
//
// Другим рядком той самий папір потрапити може: власний бюджет ОВДП на
// шостий папір не тягнув, а зведений залишок другого проходу — тягне. Два
// рядки на один ISIN дали б два записи в plan_buys, дві ціни й два різні
// «чому» на одне рішення, тож рядок росте кількістю.
//
// Порожній Ref не зливається ні з чим: у «Нового вкладу» посилання немає
// зовсім, і зводити два таких рядки в один означало б стверджувати, що це
// той самий вклад.
func allocAddLine(lines *[]allocLine, add allocLine) {
	if add.Ref != "" {
		for i := range *lines {
			l := &(*lines)[i]
			if l.Kind != add.Kind || l.Ref != add.Ref {
				continue
			}
			l.Qty += add.Qty
			l.TotalUAH = state.Major(l.TotalUAH.Major()+add.TotalUAH.Major(), money.UAH)
			l.ConvertNative = l.ConvertNative.Add(add.ConvertNative)
			return
		}
	}
	*lines = append(*lines, add)
}

// topUpIn — усе, від чого залежить другий прохід. Структурою, а не
// дванадцятьма аргументами: половина з них — числа однакового типу, і
// переставлені місцями вони компілювались би мовчки.
type topUpIn struct {
	rest    float64
	mt      moneyText // гроші в прозі «чому» — у валюті звітності
	rows    []state.RebalanceRow
	rooms   map[string]float64 // недобір виду ПІСЛЯ його бюджету, ₴
	goals   []state.Goal
	reserve *state.Reserve
	allow   allocAllow
	// goalsElig — скільки цілям іще дозволено ПІСЛЯ першого проходу. Число
	// приїжджає звідти, а не рахується тут: друге означення розійшлося б із
	// першим рівно тоді, коли вирізка була частковою.
	goalsElig float64

	sug   []suggestion
	rates fx.Rates
	cur   string
	npfID map[string]int64

	// cheapest / cheapestWhat — покажчиками, бо прохід їх ДОПОВНЮЄ: крок,
	// який він пробував і не подужав, теж пояснює залишок, і без цього
	// RestWhy назвав би крок, який прохід уже купив.
	cheapest     *float64
	cheapestWhat *string
}

// allocSpot — один приймач залишку. Види, цілі й подушка різні за природою,
// але питання до них одне: «скільки ти ще маєш право взяти».
type allocSpot struct {
	kind string // "" — вирізка, а не покупка
	goal int64  // 0 — не ціль
	rank float64
	room float64
	// allow — ДОЗВІЛ МІСЯЦЯ для цього приймача: скільки з доходу місяця
	// взагалі можна вести сюди. Нуль означає «не можна нічого», а не «без
	// обмежень», і це навмисно: приймач без дозволу мовчить, а не бере все.
	//
	// Тільки у вирізок. У видів дозвіл інший за природою — не сума, а
	// «можна/ні» (plan_flows.uses), і його вже перевірено при складанні
	// кандидатів.
	allow float64
}

// allocTopUp — ДРУГИЙ ПРОХІД: залишок пропонується тим, хто ще недобирає до
// власної цілі. Повертає те, що не взяв ніхто.
//
// НАВІЩО ВІН ВЗАГАЛІ. Бюджети видів — герметичні силоси: недобраний квиток
// лишався в залишку, і залишок чекав наступного місяця. На живих даних це
// давало 261 ₴, які лежали без діла при тому, що НПФ стояв на відсотковий
// пункт нижче цілі й приймає будь-яку суму, а сертифікат фонду коштує 11 ₴.
// Гроші не гинули — але й не працювали, і побачити, чому саме, було
// неможливо.
//
// ЧОМУ ЦЕ НЕ ПОРУШУЄ ПРАВИЛА «ГРОШІ НЕ ПЕРЕКИДАЮТЬСЯ МІЖ ВИДАМИ». Те
// правило (шапка при spreadMonth) про ПОДІЛ: коли грошей на всіх не
// вистачає, ділити треба пропорційно потребам, бо черговості видів
// застосунок не знає. Тут інше питання — про КРИХТИ, яких не взяв ніхто, — і
// відповідь на нього не «кому важливіше», а «куди воно фізично влізе».
// Поділ від цього не ламається: кожен забір обрізається власним недобором
// приймача, тож прохід уміє лише скоротити розрив і НЕ вміє штовхнути
// когось понад ціль.
//
// ЧЕРГА ВИВЕДЕНА ЗІ СТРАТЕГІЇ, А НЕ ВИГАДАНА, і в ній три яруси.
//
//  1. Цілі, які НЕ ВСТИГАЮТЬ до своєї дати (ShortMonthUAH > 0). Дата —
//     єдине тверде обмеження в усій картині: частка почекає місяць, дата не
//     посунеться. Цілі, що встигають, у прохід не входять узагалі — їх веде
//     власна стеля, і вони дійдуть вчасно.
//  2. Види — за недобором у гривнях, тим самим числом, з якого spreadMonth
//     вирахував бюджети.
//  3. Подушка — ОСТАННЯ, і саме тому прохід працює. Це єдиний приймач без
//     кроку: він бере будь-яку суму. Постав його першим — і він поглине
//     кожен залишок, а решта черги стане недосяжною, тобто прохід існував
//     би й був невидимий. Останнім він стає гарантованим дном: гроші, які
//     не змогли стати інструментом, стають подушкою замість того, щоб
//     лишатись готівкою.
//
// БОРГУ ТУТ НЕМАЄ, і це рішення власника, а не пропуск. Симетрія з
// подушкою здається очевидною — стеля дострокового теж про темп, — але
// дострокове погашення це свідома дія за стелею, яку людина сама
// поставила, а не місце, куди зсипають те, що нікуди не влізло. До того ж
// його «недобір» — увесь борг, і на шкалі в гривнях він придушив би все
// назавжди.
//
// ОДИН ПРОХІД СПИСКОМ, без циклу до нерухомої точки: приймач, чий обрізаний
// недобір дає нуль квитків, крутив би такий цикл вічно.
func allocTopUp(out *allocPlan, in topUpIn) float64 {
	rest := in.rest
	if rest < allocMinCutUAH {
		// Поріг той самий, що в першому проході, і перевіряється один раз на
		// вході: сума, з якої не вийде жодного руху, не варта ні рядка, ні
		// обходу черги.
		return rest
	}

	// РОЗРИВ МІНУС ТЕ, ЩО ВЖЕ ВЗЯВ ПЕРШИЙ ПРОХІД. doc не мутується — він
	// чистий на вхід і на вихід, — тож GapUAH тут іще ДОвирізковий, і взяти
	// його як є означало б віддати ту саму пʼятірку гривень двічі: спершу
	// стелею, потім проходом, і розрив пішов би у мінус.
	var spots []allocSpot
	// --- ярус 1: цілі, що не встигають ---
	for i := range in.goals {
		g := &in.goals[i]
		if g.ShortMonthUAH.Major() <= 0 {
			continue
		}
		room := g.GapUAH.Major() - goalTaken(out, g.ID)
		if room <= 0.005 {
			continue
		}
		spots = append(spots, allocSpot{
			goal: g.ID, rank: g.ShortMonthUAH.Major(), room: room, allow: g.FillFromUAH.Major(),
		})
	}
	sortSpots(spots)

	// --- ярус 2: види ---
	//
	// Кандидати будуються з rows — ТІЄЇ САМОЇ копії, де заборонений дозволом
	// вид уже має нульову ціль, — а не з doc.Rebalance, де ціль у нього
	// жива. Це єдине, що стоїть між зарплатою, позначеною «не в пенсійний»,
	// і внеском у пенсійний. Повторна перевірка дозволу поруч — навмисне
	// дублювання: воно коштує рядок, а ловить перестановку джерела.
	var kinds []allocSpot
	for _, r := range in.rows {
		if r.Dimension != "kind" || r.TargetPct <= 0 {
			continue
		}
		if allocKindForbidden(in.allow.Uses, r.Key) {
			continue
		}
		if room := in.rooms[r.Key]; room > 0.005 {
			kinds = append(kinds, allocSpot{kind: r.Key, rank: room, room: room})
		}
	}
	sortSpots(kinds)
	spots = append(spots, kinds...)

	// --- ярус 3: подушка, ТЕРМІНАЛЬНА ---
	//
	// ОСТАННЯ, І САМЕ ТОМУ ЯРУС ПРАЦЮЄ. Це єдиний приймач без кроку: він
	// бере будь-яку суму. Постав його першим — і він поглине кожен залишок,
	// а решта черги стане недосяжною, тобто прохід існував би й був
	// невидимий. Останнім він стає гарантованим дном: гроші, які не змогли
	// стати інструментом, стають подушкою замість того, щоб лишатись
	// готівкою.
	//
	// ДВІ СТЕЛІ, І ДРУГОЇ ДОТИ НЕ БУЛО — через неї цей ярус і не існував.
	// FillNowUAH змішує ТЕМП (reserve_fill_share_pct) і ДОЗВІЛ місяця
	// (PlanReserveUAH). Темп обійти можна: він про швидкість планових
	// внесків, а не про ціль. Дозвіл — ні: місяць, увесь дохід якого
	// позначено «не в подушку», не дає їй нічого
	// (TestRouteMonthCeilingBindsCouponToo). Тепер дозвіл приїжджає окремим
	// числом — Reserve.FillFromUAH, свіжим на кожному місяці проходу
	// (routeCarry.fillFrom), — і обидві стелі стоять поруч.
	//
	// РОЗРИВ, А НЕ СТЕЛЯ, у ролі кімнати: ребалансування питає «скільки ще
	// бракує до цілі», і саме на це питання подушка тут і відповідає.
	if in.reserve != nil {
		// allow — ВАЛОВА стеля, як і в цілей: відняти вже взяте — робота
		// того, хто ріже. Два різні правила відрахування на два приймачі
		// розійшлися б на першій же правці.
		if room := in.reserve.GapUAH.Major() - reserveTaken(out); room > 0.005 {
			spots = append(spots, allocSpot{
				rank: room, room: room,
				allow: math.Min(in.allow.ReserveUAH, in.reserve.FillFromUAH.Major()),
			})
		}
	}

	for _, s := range spots {
		if rest < allocMinCutUAH {
			break
		}
		switch {
		case s.kind != "":
			rest = topUpKind(out, in, s, rest)
		case s.goal > 0:
			rest = topUpGoal(out, in, s, rest)
		default:
			rest = topUpReserve(in.mt, out, s, rest)
		}
	}
	return rest
}

// sortSpots — сталий порядок: більший ранг першим, ключ другим критерієм.
// Порівняння точне по округленому числу (довід — при сортуванні бюджетів).
func sortSpots(s []allocSpot) {
	sort.Slice(s, func(i, j int) bool {
		a, b := round2(s[i].rank), round2(s[j].rank)
		if a != b {
			return a > b
		}
		if s[i].kind != s[j].kind {
			return s[i].kind < s[j].kind
		}
		return s[i].goal < s[j].goal
	})
}

// topUpKind — залишок у вид інструмента, цілими кроками.
func topUpKind(out *allocPlan, in topUpIn, s allocSpot, rest float64) float64 {
	// ОБРІЗАННЯ НЕДОБОРОМ ОБОВʼЯЗКОВЕ, і найгостріше воно для НПФ: allocOne
	// віддає йому ВСЕ, що дали, тож без цього рядка один пенсійний з'їв би
	// весь залишок і переїхав власну ціль — рівно те, чого прохід не має
	// права робити.
	left := math.Min(rest, s.room)
	for i := range in.sug {
		sg := in.sug[i]
		if allocKind[sg.Kind] != s.kind {
			continue
		}
		// Вибір людини діє й тут. Інакше тому, хто обрав папір за 5 000 ₴,
		// мовчки купили б рейтинговий за 1 000 ₴ із залишку — рівно те, від
		// чого вона втекла, обираючи.
		if s.kind == "bonds" && in.allow.PickISIN != "" && sg.ISIN != in.allow.PickISIN {
			continue
		}
		line, spent, ok := allocOne(sg, left, in.rates, in.cur, in.npfID)
		if !ok {
			if c := allocStepUAH(sg, in.rates, in.npfID); c > 0 &&
				(*in.cheapest == 0 || c < *in.cheapest) {
				*in.cheapest, *in.cheapestWhat = c, sg.Label
			}
			continue
		}
		if in.allow.PickISIN != "" && sg.ISIN == in.allow.PickISIN {
			line.Picked = true
			line.Why = "твій вибір"
			if sg.Reason != "" {
				line.Why += "; " + sg.Reason
			}
		}
		allocAddLine(&out.Lines, line)
		rest -= spent
		left -= spent
		if sg.Kind == "npf" {
			break
		}
	}
	return rest
}

// topUpGoal — залишок у ціль, яка не встигає до своєї дати.
//
// Вирізка РОСТЕ, а не додається другою: друга allocGoalCut із тим самим id
// подвоїла б суму в кожного читача — і в підсумку модалки, і в проході
// маршруту, який зводить їх у c.goals.
func topUpGoal(out *allocPlan, in topUpIn, s allocSpot, rest float64) float64 {
	// ДВІ СТЕЛІ, І ОБИДВІ ОБОВʼЯЗКОВІ.
	//
	// goalsElig — дозвіл ПОЛІТИКИ й ДЖЕРЕЛА (goals_fill_from + uses цього
	// надходження). FillFromUAH — дозвіл САМОГО МІСЯЦЯ: скільки з його
	// доходу взагалі можна вести в цілі (PlanGoalsUAH).
	//
	// Другої тут доти не було, і це був витік. Прохід має право обійти ТЕМП
	// (goals_fill_share_pct) — гроші, яким інакше нема куди подітись, краще
	// віддати цілі, що не встигає до дати. Але темп і дозвіл місяця
	// перемножені в FillMonthUAH одним числом, тож обходячи стелю, прохід
	// обходив і дозвіл: ціль діставала більше, ніж місяць їй дає. Саме через
	// цей витік подушка й не входила в чергу — а цілі ввійшли недоглядом.
	//
	// Обидві рахуються від ФАКТУ, а не від наміру: перший прохід міг
	// обнулити свою вирізку порогом, і тоді дозволу витрачено нуль.
	left := math.Min(in.goalsElig, s.allow) - out.GoalsUAH.Major()
	take := math.Min(math.Min(rest, s.room), math.Max(0, left))
	// Той самий поріг і той самий виняток «закриває розрив», що в першому
	// проході: остання пʼятірка гривень до цілі мусить мати право закритись.
	if closes := take >= s.room-0.005; take < allocMinCutUAH && !closes {
		return rest
	}
	if take <= 0.005 {
		return rest
	}
	growGoalCut(in.mt, out, in.goals, s.goal, take)
	return rest - take
}

// topUpReserve — залишок у подушку, останнім кроком черги.
//
// Дзеркало topUpGoal, і навмисно окремою функцією, а не гілкою в ній: у
// подушки поле одне, у цілей масив, а спільна функція з двома if усередині
// читалась би як одна дія над двома різними речами.
func topUpReserve(mt moneyText, out *allocPlan, s allocSpot, rest float64) float64 {
	take := math.Min(math.Min(rest, s.room), math.Max(0, s.allow-reserveTaken(out)))
	// Той самий поріг і той самий виняток «закриває розрив», що всюди:
	// остання пʼятірка гривень до цілі мусить мати право закритись.
	if closes := take >= s.room-0.005; take < allocMinCutUAH && !closes {
		return rest
	}
	if take <= 0.005 {
		return rest
	}
	if out.Reserve == nil {
		out.Reserve = &allocReserve{}
	}
	out.Reserve.AmountUAH = state.Major(out.Reserve.AmountUAH.Major()+take, money.UAH)
	out.Reserve.Why = allocTopUpWhy("подушка")
	// ПРИЧИНА-ПОРІГ ЗНІМАЄТЬСЯ. Перший прохід міг сказати «подушка тут свого
	// не бере: 3 ₴ — менше за 10 ₴», а цей дав їй 400 ₴; лишити обидва рядки
	// поруч означало б надрукувати заперечення власного числа. Причину
	// ПОЛІТИКИ не чіпаємо: вона й далі правда про ту частину, якої подушці
	// не дали.
	if strings.Contains(out.ReserveSkipWhy, mt.uah(allocMinCutUAH)) {
		out.ReserveSkipWhy = ""
	}
	return rest - take
}

func reserveTaken(p *allocPlan) float64 {
	if p.Reserve == nil {
		return 0
	}
	return p.Reserve.AmountUAH.Major()
}

func goalTaken(p *allocPlan, id int64) float64 {
	for i := range p.Goals {
		if p.Goals[i].ID == id {
			return p.Goals[i].AmountUAH.Major()
		}
	}
	return 0
}

func growGoalCut(mt moneyText, out *allocPlan, goals []state.Goal, id int64, take float64) {
	for i := range out.Goals {
		if out.Goals[i].ID != id {
			continue
		}
		out.Goals[i].AmountUAH = state.Major(out.Goals[i].AmountUAH.Major()+take, money.UAH)
		out.Goals[i].Why = allocTopUpWhy("ціль")
		out.GoalsUAH = state.Major(out.GoalsUAH.Major()+take, money.UAH)
		return
	}
	name := ""
	for i := range goals {
		if goals[i].ID == id {
			name = goals[i].Name
			break
		}
	}
	out.Goals = append(out.Goals, allocGoalCut{
		ID: id, Name: name, AmountUAH: state.Major(take, money.UAH), Why: allocTopUpWhy("ціль"),
	})
	out.GoalsUAH = state.Major(out.GoalsUAH.Major()+take, money.UAH)
	if strings.Contains(out.GoalsSkipWhy, mt.uah(allocMinCutUAH)) {
		out.GoalsSkipWhy = ""
	}
}

// allocTopUpWhy — чому сюди пішло БІЛЬШЕ за місячну частку.
//
// Сказати обовʼязково: місячна стеля — це рішення людини про ТЕМП планових
// внесків, і число понад неї без пояснення читається як його порушення.
// Правда інша: це гроші, яких не взяв жоден крок жодного інструмента, і
// вибір у них був лише один — лежати готівкою або закрити цей розрив.
func allocTopUpWhy(who string) string {
	return who + " добирає залишком: ці гроші не склались у цілий крок " +
		"жодного інструмента, а розрив тут іще живий — місячна частка міряє темп " +
		"планових внесків, а не саму ціль"
}

// allocKindForbidden — чи закритий цей вид ребалансу дозволом джерела.
//
// Мапа кошиків на види одна на всю функцію: «інвестиції» — це папери,
// фонди й вклади разом, бо саме так їх бачить людина, яка ставить галочку.
// Пенсійний стоїть окремо не з примхи: вийти з нього до пенсії не можна,
// і рішення «цю премію в НПФ не клади» відрізняється від «цю премію не
// вкладай» настільки ж, наскільки відрізняються самі інструменти.
func allocKindForbidden(uses, kindKey string) bool {
	bucket := domain.UsePlanInvest
	if kindKey == "npf" {
		bucket = domain.UsePlanNPF
	}
	return !domain.PlanUseAllowed(uses, bucket)
}

// allocSavingsOnly — джерело не пускає гроші в жоден інструмент.
func allocSavingsOnly(uses string) bool {
	return !domain.PlanUseAllowed(uses, domain.UsePlanInvest) &&
		!domain.PlanUseAllowed(uses, domain.UsePlanNPF)
}

// allocAllTakenNote — чому рядків покупок немає взагалі: усе забрали
// подушка й цілі. Називає ОБОХ винуватців поіменно, а не «щось забрало»:
// «усе пішло в подушку» при живих цілях було б неправдою рівно наполовину.
func allocAllTakenNote(p allocPlan) string {
	switch {
	case p.Reserve != nil && p.GoalsUAH.Major() > 0:
		return "усе розібрали подушка й цілі накопичення: доки їхні розриви живі, " +
			"вони забирають своє першими"
	case p.GoalsUAH.Major() > 0:
		return "усе пішло в цілі накопичення: доки розрив не закритий, " +
			"вони забирають своє перед паперами"
	default:
		return "усе пішло в подушку: доки розрив не закритий, вона забирає своє першою"
	}
}

// goalsSkipWhy — чому цілі не взяли своєї частки (або взяли менше).
//
// Дзеркалить reserveSkipWhy і з того самого доводу: рядок, що не веде до
// налаштування, змушує шукати причину в чужих числах.
func goalsSkipWhy(mt moneyText, set *state.SettingsDoc, blocked, cut float64, bySource bool) string {
	if bySource {
		return allocBySourceWhy(mt, "цілі", "взяли", "не беруть", blocked, cut)
	}
	rule := "їх наповнює лише плановий дохід"
	if goalsFillFrom(set) == "redeem" {
		rule = "їх наповнюють плановий дохід і повернення тіла, а це дохід портфеля"
	}
	if cut > 0.005 {
		return fmt.Sprintf("цілі взяли лише %s: решта — %s — за твоєю політикою в них не йде, %s",
			mt.uah(cut), mt.uah(blocked), rule)
	}
	return fmt.Sprintf("цілі тут свого не беруть (%s за стелею): за твоєю політикою %s",
		mt.uah(blocked), rule)
}

// reserveSkipWhy — чому подушка не взяла своєї частки (або взяла менше).
//
// Називає САМЕ ТУ політику, яку поставив користувач, а не загальне «не
// можна»: рядок, що не веде до налаштування, змушує шукати причину в
// чужих числах. Аргумент, чому це поле взагалі є, — при allocReserve.
func reserveSkipWhy(mt moneyText, set *state.SettingsDoc, blocked, cut float64, bySource bool) string {
	if bySource {
		return allocBySourceWhy(mt, "подушка", "взяла", "не бере", blocked, cut)
	}
	rule := "її наповнює лише плановий дохід"
	if reserveFillFrom(set) == "redeem" {
		rule = "її наповнюють плановий дохід і повернення тіла, а це дохід портфеля"
	}
	if cut > 0.005 {
		return fmt.Sprintf("подушка взяла лише %s: решта — %s — за твоєю політикою в неї не йде, %s",
			mt.uah(cut), mt.uah(blocked), rule)
	}
	return fmt.Sprintf("подушка тут своє не бере (%s за стелею): за твоєю політикою %s",
		mt.uah(blocked), rule)
}

// allocBySourceWhy — вирізки немає через ДОЗВІЛ САМОГО НАДХОДЖЕННЯ, а не
// через політику.
//
// Окремий текст обов'язковий. Рядок «її наповнює лише плановий дохід» під
// відміткою планового доходу читається як поломка застосунку: політика ж
// саме цей випадок і дозволяє. Причина тут інша й вона за два кліки —
// галочка в самому потоці, — тож вести треба туди, а не в «Політику».
// Дієслова параметрами, а не однією формою на двох: подушка одна, цілей
// багато, і «цілі накопичення тут свого НЕ БЕРЕ» — рядок, який людина
// прочитає як недбалість, а далі так само поставиться й до числа поруч.
func allocBySourceWhy(mt moneyText, who, took, takes string, blocked, cut float64) string {
	if cut > 0.005 {
		return fmt.Sprintf("%s %s лише %s: решта — %s — сюди не йде, "+
			"бо саме це надходження позначене інакше", who, took, mt.uah(cut), mt.uah(blocked))
	}
	return fmt.Sprintf("%s тут нічого %s (%s за стелею): це надходження "+
		"позначене як таке, що сюди не йде", who, takes, mt.uah(blocked))
}

// allocBelowFloorWhy — вирізки немає через ПОРІГ, а не через політику й не
// через дозвіл надходження.
//
// Третій текст на те саме поле, і третім він мусить бути. «За твоєю
// політикою в неї не йде» під живою політикою «звідки завгодно» читається
// як поломка застосунку, а «це надходження позначене інакше» жене шукати
// чужу галочку. Тут причини немає в жодній настройці — вона в самому
// числі, — і сказати треба саме це, разом із тим, куди гроші пішли
// натомість: сума, що «просто зникла», читається як загублена.
//
// Дієслова параметрами, а не однією формою на двох, — рівно з того доводу,
// що при allocBySourceWhy: подушка одна, цілей багато.
func allocBelowFloorWhy(mt moneyText, who, takes, willTake string, amountUAH float64) string {
	return fmt.Sprintf("%s тут свого %s: %s — менше за %s, з яких має сенс "+
		"окремий рух. Гроші не зникли, вони йдуть далі разом із сумою, "+
		"а своє %s наступного разу",
		who, takes, mt.uah(amountUAH), mt.uah(allocMinCutUAH), willTake)
}

// allocStepUAH — ціна одного кроку поради в гривні-еквіваленті. Нуль
// означає «кроку немає» або «ціни немає».
//
// У пенсійного ЦІНИ кроку немає й бути не може — він приймає будь-яку
// суму. Але КРОК у нього тепер є, і це allocMinCutUAH: найменший внесок,
// по який варто йти в застосунок фонду. Число повертається саме тому, що на
// нього вже чекає готова машинерія залишку (cheapest/cheapestWhat нижче):
// без нього нога, у якій НПФ був єдиним призначенням, пояснювала б свій
// залишок фразою «інструментів із відомою ціною в цих видах немає» —
// неправдою про наявний рахунок.
//
// І лише коли рахунок є. Без id вносити нема куди (npfIDByName вимикає
// рядок замість того, щоб угадати), а назвати поріг «наступним кроком»
// означало б обіцяти дію, якої застосунок виконати не може.
func allocStepUAH(sg suggestion, rates fx.Rates, npfID map[string]int64) float64 {
	if sg.Kind == "npf" {
		if npfID[sg.Label] > 0 {
			return allocMinCutUAH
		}
		return 0
	}
	step := moneyAmount(sg.CostPerBond)
	if step <= 0 {
		return 0
	}
	rate, ok := fx.RateMajor(sg.Currency, rates)
	if !ok {
		return 0
	}
	return step * rate
}

// allocOne — скільки цієї поради вміщується в залишок бюджету. ok=false
// означає «жодного цілого кроку»: половини паперу не буває, і саме тут
// живе «квиток ОВДП ≈1000 ₴» — одним діленням над однією ціною, тією
// самою, яку показує «Що купити» (bondUnitCost).
func allocOne(sg suggestion, left float64, rates fx.Rates,
	cur string, npfID map[string]int64) (allocLine, float64, bool) {

	line := allocLine{
		Kind: sg.Kind, Label: sg.Label, Currency: sg.Currency,
		RealPct: sg.RealPct, Why: sg.Reason,
		CostBasis: sg.CostBasis, CostAsOf: sg.CostAsOf,
		CostWhere: sg.CostWhere, CostWhereLabel: sg.CostWhereLabel,
		CostAlt: sg.CostAlt, CostAltWhere: sg.CostAltWhere,
	}

	if sg.Kind == "npf" {
		id := npfID[sg.Label]
		// ПОРІГ ТУТ Є, І ВІН НЕ ВІД ФОНДУ. «Порога входу він не має» — правда
		// про сам пенсійний і неправда про людину: доки рядок брав усе, що
		// лишилось, застосунок радив віднести в НПФ двадцять шість копійок, і
		// в шістнадцяти ногах маршруту з сорока семи це була ЄДИНА його
		// порада. Гроші нижче порога лишаються в залишку й доїжджають до
		// наступного надходження тим самим горщиком, що й недобраний квиток
		// ОВДП, а причину назве RestWhy — allocStepUAH віддає йому цей поріг
		// як ціну наступного кроку.
		//
		// Винятку «закриває розрив», який є в подушки й цілей, тут немає й
		// бути не може: у виду немає ні розриву, ні боргу — лише частка, і
		// невнесені цього разу три гривні наступного просто побільшать бюджет
		// виду.
		if id <= 0 || left < allocMinCutUAH {
			return line, 0, false
		}
		// Валюта тут гривнева свідомо: бюджети видів міряні в грн-екв., а
		// внесок приймає будь-яку суму — конвертувати нема чого, і позначка
		// «конвертація» на ньому була б неправдою.
		line.Ref = strconv.FormatInt(id, 10)
		line.Currency = money.UAH
		amt := toMoneyJSON(money.New(int64(math.Round(left*100)), money.UAH))
		line.Amount = &amt
		line.TotalUAH = state.Major(left, money.UAH)
		line.Addable = true
		return line, left, true
	}

	step := allocStepUAH(sg, rates, npfID)
	if step <= 0 {
		return line, 0, false
	}
	n := int64(left / step)
	if n < 1 {
		return line, 0, false
	}
	spent := float64(n) * step
	line.Qty = n
	unit := sg.CostPerBond
	line.Unit = &unit
	line.TotalUAH = state.Major(spent, money.UAH)
	switch sg.Kind {
	case "bond":
		line.Ref, line.Addable = sg.ISIN, sg.ISIN != ""
	case "fund":
		line.Ref, line.Addable = sg.Label, sg.Label != ""
	default: // deposit
		// Ref — банк, і лише для наявного вкладу: у рядка «Новий вклад» банку
		// немає взагалі. Addable хибне в обох випадках (див. allocLine).
		line.Ref = sg.Label
		amt := toMoneyJSON(money.New(
			int64(math.Round(spent/allocRate(sg.Currency, rates)*100)), sg.Currency))
		line.Amount = &amt
		line.Qty, line.Unit = 0, nil
	}
	if sg.Currency != cur {
		line.Convert = true
		if rate, ok := fx.RateMajor(cur, rates); ok && rate > 0 {
			line.ConvertNative = state.Major(spent/rate, cur)
		}
	}
	return line, spent, true
}

// allocRate — курс валюти в гривні; одиниця для гривні й для невідомого.
// Одиниця замість нуля тут навмисно: нуль пішов би в знаменник.
func allocRate(cur string, rates fx.Rates) float64 {
	if rate, ok := fx.RateMajor(cur, rates); ok && rate > 0 {
		return rate
	}
	return 1
}
