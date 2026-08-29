// Стрічка часу плану: усе, що на ній малюється, одним документом.
//
// Лишається в REST, у MQTT-контракт не йде — той самий прецедент, що й
// AuctionPoint (фаза 7, A5): дані для картинки, а не для сутностей Home
// Assistant.
package api

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"

	money "github.com/Rhymond/go-money"
)

type timelineFlow struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Kind      string  `json:"kind"`
	Cadence   string  `json:"cadence"`
	From      string  `json:"from"`
	Until     string  `json:"until,omitempty"`
	AmountUAH float64 `json:"amount_uah"`
}

type timelineAction struct {
	ID        int64   `json:"id"`
	Date      string  `json:"date"`
	Type      string  `json:"type"`
	Name      string  `json:"name,omitempty"`
	AmountUAH float64 `json:"amount_uah,omitempty"`
}

// timelineInstrument — термін реального інструмента: коли він повертає
// гроші (To) чи, для фонду, коли зачиняється вікно купівлі (BuyUntil).
type timelineInstrument struct {
	Kind     string `json:"kind"` // bond | deposit | fund
	Label    string `json:"label"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	BuyUntil string `json:"buy_until,omitempty"`
}

type timelineMilestone struct {
	Date  string `json:"date"`
	Label string `json:"label"`
}

type timelineCurvePoint struct {
	Date        string  `json:"date"`
	Plan        float64 `json:"plan"`
	Optimistic  float64 `json:"optimistic,omitempty"`
	Pessimistic float64 `json:"pessimistic,omitempty"`
	Actual      float64 `json:"actual,omitempty"`
}

// profileSeries / profilePoint — «форма плану в часі»: скільки ₴/міс
// заходить у кожен місяць горизонту, з розкладом по потоках.
//
// Values іде в тому самому порядку, що й Series, — тобто масив чисел, а не
// мапа: рядів одиниці, а мапа коштувала б назви потоку в кожній точці.
type profileSeries struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // income | expense
}

type profilePoint struct {
	Date   string    `json:"date"`
	Values []float64 `json:"values"`
	Net    float64   `json:"net"`
	// Income — що ЦЬОГО місяця приносить сам портфель: купони ОВДП,
	// відсотки вкладів, дивіденди фондів. Окремо від Values і від Net
	// навмисно: Net мусить і далі точно дорівнювати плитці «План дає»,
	// інакше картинка знову розійдеться з числом над нею.
	Income float64 `json:"income,omitempty"`
}

// profileEvent — повернення ТІЛА: погашення ОВДП, закриття вкладу,
// закриття фонду.
//
// Не площею, а подією на осі, і це не про вигляд. По-перше, це твої ж
// гроші, що повертаються, а не новий дохід — у стосі надходжень вони
// збрехали б. По-друге, площею одне погашення дало б шпиль на пів графіка
// й розчавило б решту в смужку. По-третє, профіль усереднює вікно кроку,
// тож на довгому горизонті одноразова подія розмазалась би на квартал і
// виглядала втричі меншою, ніж вона є.
type profileEvent struct {
	Date string `json:"date"`
	// npf — та сама природа події, що закриття фонду: гроші стають доступні
	// рівно раз, і в розкладі цього моменту немає. Різниця лише в горизонті —
	// двадцять пʼять років проти трьох.
	Kind      string  `json:"kind"` // bond | deposit | fund | npf
	Label     string  `json:"label"`
	AmountUAH float64 `json:"amount_uah"`
}

type planProfile struct {
	StepMonths int             `json:"step_months"`
	Series     []profileSeries `json:"series"`
	Points     []profilePoint  `json:"points"`
	Events     []profileEvent  `json:"events,omitempty"`
}

// planHistoryPoint — один МИНУЛИЙ місяць трьома числами: скільки план мав
// завести, скільки нових грошей зайшло насправді і скільки застосунок тоді
// вважав нестачею понад план.
//
// Три різні джерела, і плутати їх не можна (той самий урок, що записаний у
// contrib.js). PlanUAH читається з ЖУРНАЛУ РЕВІЗІЙ — з того стану таблиці
// потоків, який діяв на кінець того місяця (planAsOf), тож пізніша правка
// суми на нього не впливає. ActualUAH — реальні поповнення з датами,
// нетто зі зняттями. GapUAH береться зі знімка того місяця й НЕ
// перераховується: це те, що застосунок вважав нестачею ТОДІ.
//
// GapUAH — саме «бракує», а не «треба»: month_target_uah виводиться з цілі
// ПОНАД те, що вже дає план (state_builder.go, target := prj.TargetUAH).
// Підписати його словом «треба» означало б повторити ту саму підміну, через
// яку колись «до цілі бракує ще 70 270» і «скільки треба вносити: 70 270»
// читались як два різні показники.
// PlanDerived — план за цей місяць не прочитано з журналу, а ВИВЕДЕНО з
// теперішньої таблиці потоків: журнал тоді ще не існував. Позначка їде в
// UI, бо різницю між «так було» і «так виходить, якщо припустити, що
// нічого не мінялось» читач мусить бачити, а не вгадувати.
type planHistoryPoint struct {
	Month       string  `json:"month"` // YYYY-MM
	PlanUAH     float64 `json:"plan_uah"`
	ActualUAH   float64 `json:"actual_uah"`
	GapUAH      float64 `json:"gap_uah,omitempty"`
	PlanDerived bool    `json:"plan_derived,omitempty"`
	// ReceivedUAH — скільки НАДІЙШЛО за відмітками, у тих самих грошах, що
	// й PlanUAH (частка в портфель застосована). omitempty тут безпечний
	// лише тому, що поруч стоїть Marked: сам по собі нуль не відрізнити від
	// «не відмічено», і саме прапорець вирішує, малювати стовпчик чи ні.
	ReceivedUAH float64 `json:"received_uah,omitempty"`
	Marked      bool    `json:"marked,omitempty"`
}

// flowRevisionRow — рядок «Історії правок» для UI.
//
// Журнал, якого не прочитати, — це просто прихована таблиця, тож те саме,
// на чому стоїть реконструкція, показується й людині.
// FlowID тут обов'язковий: попередню ревізію шукають саме по ньому, а НЕ
// по назві. Після «⇗» два рядки називаються однаково («Зарплата»), і
// порівняння по назві показувало б підвищення як правку старого рядка.
type flowRevisionRow struct {
	At     string  `json:"at"` // RFC3339
	Op     string  `json:"op"` // seed | create | update | delete
	FlowID int64   `json:"flow_id"`
	Name   string  `json:"name"`
	Flow   planRev `json:"flow"`
}

// planRev — поля потоку, які має сенс порівнювати між ревізіями. Сума
// рядком у мінорних одиницях не потрібна: UI показує гроші, а не int64.
type planRev struct {
	Amount    float64 `json:"amount"`
	Currency  string  `json:"currency"`
	Cadence   string  `json:"cadence"`
	FromDate  string  `json:"from_date"`
	UntilDate string  `json:"until_date,omitempty"`
	InvestPct float64 `json:"invest_pct"`
	GrowthPct float64 `json:"growth_pct,omitempty"`
	// Uses — дозвіл на момент ревізії. Без нього історія правок мовчала б
	// про зміну, яка рухає стелю подушки: «нічого не змінилось» там, де
	// змінилось те, з чого вона рахується.
	Uses []string `json:"uses,omitempty"`
}

// receiptRow — відмітка надходження для UI.
//
// GivesUAH — те, що ця сума дає В ПОРТФЕЛЬ (частка застосована), тобто
// число в тих самих грошах, що PlanUAH історії. Amount лишається валовим:
// у чеклисті людина звіряє його з тим, що прийшло на картку, а не з тим,
// скільки з нього дійде до брокера.
type receiptRow struct {
	ID        int64     `json:"id"`
	FlowID    int64     `json:"flow_id"`
	Month     string    `json:"month"`
	Name      string    `json:"name"`
	Amount    moneyJSON `json:"amount"`
	InvestPct float64   `json:"invest_pct"`
	GivesUAH  float64   `json:"gives_uah"`
	// Uses — дозвіл, ЧИННИЙ для цієї відмітки: у прив'язаної він приходить
	// із потоку, у позапланової — власний. Тією самою підстановкою, що й
	// InvestPct поруч, і з того самого доводу.
	Uses []string `json:"uses,omitempty"`
	Note string   `json:"note,omitempty"`
}

// expectedReceipt — очікувана виплата одного потоку в одному місяці: рядок
// чеклиста «Надходження місяця».
//
// Розгортається з плану, а не зберігається: очікування — це і є план,
// другої його копії в базі бути не повинно. Amount ВАЛОВА, до частки в
// портфель, — саме її людина порівнює з випискою, і саме її підставляє
// кнопка «прийшло».
//
// Receipt — відмітка, якщо вона вже стоїть. Порожнє поле означає «місяць
// ще не відмічено», і це третій стан поруч із «прийшло» та «не прийшло»:
// плутати його з нулем не можна ніде, ні тут, ні в історії.
type expectedReceipt struct {
	FlowID  int64       `json:"flow_id"`
	Name    string      `json:"name"`
	Month   string      `json:"month"`
	DueDate string      `json:"due_date"`
	Amount  moneyJSON   `json:"amount"`
	PlanUAH float64     `json:"plan_uah"`
	Uses    []string    `json:"uses,omitempty"`
	Receipt *receiptRow `json:"receipt,omitempty"`
}

type timelineDoc struct {
	From          string               `json:"from"`
	To            string               `json:"to"`
	Flows         []timelineFlow       `json:"flows"`
	Actions       []timelineAction     `json:"actions"`
	Instruments   []timelineInstrument `json:"instruments"`
	Milestones    []timelineMilestone  `json:"milestones"`
	Curve         []timelineCurvePoint `json:"curve,omitempty"`
	Profile       *planProfile         `json:"profile,omitempty"`
	History       []planHistoryPoint   `json:"history,omitempty"`
	FlowRevisions []flowRevisionRow    `json:"flow_revisions,omitempty"`
	Expected      []expectedReceipt    `json:"expected,omitempty"`
	Receipts      []receiptRow         `json:"receipts,omitempty"`
}

// profileMaxPoints — стеля точок профілю. Довший горизонт малюється з
// кроком у квартал: 120 місячних точок на графік завширшки з картку — це
// вже менше пікселя на точку, і крок дрібніший за них нічого не додає, а
// відповідь роздуває.
const profileMaxPoints = 120

// profileFundMonths — горизонт оцінки дивідендів для профілю. Стеля
// профілю в точках усе одно обріже зайве, а от коротший горизонт лишив би
// на графіку сходинку в тому місці, де просто закінчилась оцінка.
const profileFundMonths = 720

// buildPlanProfile розгортає потоки в помісячні суми на тому самому
// ядрі, що й проєкція з колонкою «дає ₴/міс» (planFlowMonthlyUAH) — тож
// третього означення надходжень не з'являється, і форма на картинці не
// може розійтися з числом над нею.
func buildPlanProfile(flows []store.PlanFlow, marks planMarks, today, to domain.Date,
	rates fx.Rates, cashflow []domain.CashflowItem) *planProfile {
	if len(flows) == 0 {
		return nil
	}
	months := monthOffsetRaw(today, to)
	if months < 12 {
		months = 12
	}
	step := 1
	for months/step > profileMaxPoints {
		step += 2 // 1 → 3 → 5 …: квартал, потім рідше
	}
	p := &planProfile{StepMonths: step}
	for _, f := range flows {
		p.Series = append(p.Series, profileSeries{ID: f.ID, Name: f.Name, Kind: f.Kind})
	}

	// Дохід портфеля розкладаємо по тих самих місяцях, що й план. Джерело
	// — buildSchedule, тобто рівно те, з чого живуть «Виплати» й зведення:
	// власного означення купона тут не з'являється.
	income := make(map[int]float64)
	for _, cf := range cashflow {
		if cf.Type == domain.PayRedemption {
			continue // повернення тіла — подія, не дохід (див. profileEvent)
		}
		mi := monthOffsetRaw(today, cf.Date)
		if mi < 1 || mi > months {
			continue
		}
		u, err := fx.ToUAH(cf.Amount, rates)
		if err != nil {
			continue
		}
		income[mi] += float64(u.Amount()) / 100
	}

	for m := 1; m <= months; m += step {
		pt := profilePoint{Date: string(today.AddMonths(m)), Values: make([]float64, len(flows))}
		var inc float64
		for k := 0; k < step; k++ {
			inc += income[m+k]
		}
		pt.Income = round2(inc / float64(step))
		for i, f := range flows {
			// Крок > 1 усереднює вікно, а не бере його перший місяць:
			// інакше квартальний потік то потрапляв би в точку, то ні, і
			// картинка стрибала б від вибору кроку, а не від плану.
			var sum float64
			for k := 0; k < step; k++ {
				sum += planFlowMonthlyUAH(f, today, rates, m+k, marks)
			}
			v := round2(sum / float64(step))
			pt.Values[i] = v
			pt.Net += v
		}
		// Net — САМЕ план, без доходу портфеля: на ньому стоїть рівність
		// із плиткою «План дає», і тест її стереже.
		pt.Net = round2(pt.Net)
		p.Points = append(p.Points, pt)
	}
	return p
}

// planHistoryMonths — скільки минулих місяців показує «План проти факту».
// Рік: коротше вікно не показало б ані сезонності, ані того, що сталося з
// планом після підвищення, а довше впирається в те, що застосунком просто
// ще не користувались стільки часу.
const planHistoryMonths = 12

// monthKeyAt — календарний місяць, зсунутий на m від today, як "YYYY-MM".
//
// Рахується арифметикою над роком і місяцем, а НЕ через Date.AddMonths: та
// має Go-семантику переповнення (31 березня мінус місяць = 3 березня), і
// ключ місяця з неї часом виходив би не тим. Тут та сама формула, що і в
// monthOffsetRaw, тільки в інший бік — тож місяць, який monthOffsetRaw
// називає m-им, і місяць, який називає ним ця функція, завжди один і той же.
func monthKeyAt(today domain.Date, m int) string {
	tot := today.Year()*12 + int(today.Month()) - 1 + m
	return fmt.Sprintf("%04d-%02d", tot/12, tot%12+1)
}

// planAsOf — план таким, яким він був ВІДОМИЙ на момент t: остання ревізія
// кожного потоку з changed_at <= t, без тих, чия остання ревізія — delete.
//
// Саме тут минуле перестає переписуватись. Доти «скільки план давав у
// травні» виводилось із теперішньої таблиці, тож виправлена на місці
// зарплата робила вигляд, ніби вона була такою завжди. Тепер травень
// читає ту ревізію, яка діяла в травні, і пізніша правка на нього не
// впливає взагалі.
//
// revs мусять бути в хронологічному порядку — так їх і віддає
// ListPlanFlowRevisions.
func planAsOf(revs []store.PlanFlowRevision, t time.Time) []store.PlanFlow {
	last := map[int64]store.PlanFlowRevision{}
	var order []int64
	for _, r := range revs {
		if r.ChangedAt.After(t) {
			break // хронологія — далі лише пізніші
		}
		if _, seen := last[r.FlowID]; !seen {
			order = append(order, r.FlowID)
		}
		last[r.FlowID] = r
	}
	out := make([]store.PlanFlow, 0, len(order))
	for _, id := range order {
		r := last[id]
		if r.Op == "delete" {
			continue // потік на той момент уже не існував
		}
		out = append(out, r.Flow)
	}
	return out
}

// flowRevisionsShown — скільки правок показує «Історія правок». Стеля, а
// не вікно: журнал сам по собі читається за рік, а список під таблицею —
// це «що мінялось останнім часом», а не архів.
const flowRevisionsShown = 50

// flowRevisionRows — журнал для UI, найновіші згори. Порівняння «що саме
// змінилось» робить браузер: там уже є і форматування грошей, і підписи
// періодичності, а другий їх набір тут неминуче розійшовся б із першим.
func flowRevisionRows(revs []store.PlanFlowRevision) []flowRevisionRow {
	out := make([]flowRevisionRow, 0, len(revs))
	for i := len(revs) - 1; i >= 0 && len(out) < flowRevisionsShown; i-- {
		r := revs[i]
		out = append(out, flowRevisionRow{
			At: r.ChangedAt.Format(time.RFC3339), Op: r.Op,
			FlowID: r.FlowID, Name: r.Flow.Name,
			Flow: planRev{
				Amount:    float64(r.Flow.Amount) / 100,
				Currency:  r.Flow.Currency,
				Cadence:   r.Flow.Cadence,
				FromDate:  string(r.Flow.FromDate),
				UntilDate: string(r.Flow.UntilDate),
				InvestPct: float64(r.Flow.InvestBP) / 100,
				GrowthPct: float64(r.Flow.GrowthBP) / 100,
				Uses:      domain.PlanUsesList(r.Flow.Uses),
			},
		})
	}
	return out
}

// monthEnd — остання мить місяця "YYYY-MM". Найперше число НАСТУПНОГО
// місяця мінус наносекунда, а не «31-ше»: різна довжина місяців тут не
// має ставати ще одним місцем, де можна помилитись.
func monthEnd(key string) time.Time {
	y, mo := 0, 0
	if _, err := fmt.Sscanf(key, "%04d-%02d", &y, &mo); err != nil {
		return time.Time{}
	}
	return time.Date(y, time.Month(mo)+1, 1, 0, 0, 0, 0, time.UTC).Add(-time.Nanosecond)
}

// buildPlanHistory — минулий рік помісячно: план, факт, нестача.
//
// Факт міряється ПОПОВНЕННЯМИ, а не покупками, і це не дрібниця: купівля
// лише переносить гроші з рахунку в папери й до цілі не додає нічого
// (те саме означення, що й у buildMonth — state_month.go). Нетто зі
// зняттями — щоб переказ між брокерами (він записується як зняття плюс
// поповнення) не роздував факт на свою суму. Резерв входить сюди ж: гроші,
// відкладені в матрац, це такий самий внесок, а переміщення гаманець →
// матрац записане двома ногами й у сумі дає нуль.
// ReceivedUAH/Marked — четверте число, і воно відповідає на питання, якого
// решта три не ставлять: скільки ПРИЙШЛО. Факт (ActualUAH) міряє те, що я
// відніс брокерові; надходження — те, що мені заплатили, і між ними може
// лежати ціле життя. Місяць без жодної відмітки не отримує ні числа, ні
// стовпчика: Marked існує саме для того, щоб записаний нуль («не прийшло»)
// не читався як «місяць не відмічено» — злиті в одне, ці два стани зробили
// б увесь ряд безглуздим.
//
// Відмітки заміщають план лише В МАЙБУТНЬОМУ (там вони — найкраще відоме
// про місяць). Тут, у минулому, план читається БЕЗ них: інакше обидва ряди
// збігалися б за побудовою, і порівнювати не було б чого.
func buildPlanHistory(flows []store.PlanFlow, deposits []store.Deposit,
	reserveOps []store.ReserveOp, snaps []store.Snapshot, revs []store.PlanFlowRevision,
	receipts []store.PlanReceipt, today domain.Date, rates fx.Rates) []planHistoryPoint {
	if len(flows) == 0 && len(deposits) == 0 && len(receipts) == 0 {
		return nil
	}
	marks := newPlanMarks(receipts)
	first := monthKeyAt(today, -planHistoryMonths)
	last := monthKeyAt(today, -1)

	actual := map[string]float64{}
	addMove := func(d domain.Date, amount int64, cur string) {
		key := string(d)
		if len(key) < 7 {
			return
		}
		key = key[:7]
		if key < first || key > last {
			return
		}
		// Знак зберігається: зняття зменшує внесок місяця так само, як
		// поповнення його збільшує.
		if u, err := fx.ToUAH(money.New(amount, cur), rates); err == nil {
			actual[key] += float64(u.Amount()) / 100
		}
	}
	for _, d := range deposits {
		addMove(d.Date, d.Amount, d.Currency)
	}
	for _, op := range reserveOps {
		addMove(op.Date, op.Amount, op.Currency)
	}

	// Нестача — з ОСТАННЬОГО знімка місяця: цифра дня, найближчого до
	// підсумку. Знімки приходять упорядкованими за датою, тож просто
	// перезаписуємо.
	gap := map[string]float64{}
	for _, s := range snaps {
		key := string(s.Date)
		if len(key) < 7 {
			continue
		}
		key = key[:7]
		if key < first || key > last {
			continue
		}
		gap[key] = float64(s.MonthTargetUAH) / 100
	}

	out := make([]planHistoryPoint, 0, planHistoryMonths)
	for k := planHistoryMonths; k >= 1; k-- {
		key := monthKeyAt(today, -k)
		// Стан плану беремо на КІНЕЦЬ місяця: питання «скільки план давав
		// у травні» про травень цілком, а не про якийсь його день.
		asOf := monthEnd(key)
		// Журнал ще не існував — виводимо з теперішньої таблиці, як доти, і
		// чесно кажемо про це позначкою.
		derived := len(revs) == 0 || revs[0].ChangedAt.After(asOf)
		src := flows
		if !derived {
			src = planAsOf(revs, asOf)
		}
		var plan, recv float64
		var marked bool
		for _, f := range src {
			// nil — план БЕЗ відміток: див. коментар над функцією.
			plan += planFlowMonthlyUAHPast(f, today, rates, -k, nil)
			if _, ok := marks.at(f.ID, today, -k); !ok {
				continue
			}
			// А тут — з ними, тим самим ядром: частка в портфель береться з
			// потоку ТОГО місяця (src — це planAsOf), тож пізніша правка
			// частки записану історію не переписує.
			recv += planFlowMonthlyUAHPast(f, today, rates, -k, marks)
			marked = true
		}
		// «Інше» до жодного потоку не прив'язане, тож повз ядро — зі своєю
		// часткою. У план воно не входить ніде й ніколи: позаплановий дохід
		// не був обіцяний, і зараховувати його в виконання плану означало б
		// хвалити себе за випадковість.
		for _, r := range receipts {
			if r.FlowID != 0 || r.Month != key {
				continue
			}
			recv += planFlowUAH(float64(r.Amount)/100*float64(r.InvestBP)/10000, r.Currency, rates)
			marked = true
		}
		out = append(out, planHistoryPoint{
			Month: key, PlanUAH: round2(plan), PlanDerived: derived,
			ActualUAH: round2(actual[key]), GapUAH: round2(gap[key]),
			ReceivedUAH: round2(recv), Marked: marked,
		})
	}
	// Порожній хвіст на початку — це не «нуль», а «застосунком тоді ще не
	// користувались»: намальований нулем він читався б як провалений план.
	// Той самий прийом, що й usableSnaps на кривій «Як росте».
	i := 0
	for i < len(out) && out[i].PlanUAH == 0 && out[i].ActualUAH == 0 && out[i].GapUAH == 0 &&
		!out[i].Marked {
		i++
	}
	if i >= len(out)-1 {
		return nil // менше двох місяців — порівнювати нема чого
	}
	return out[i:]
}

// planFlowAtMonth — планова сума потоку на місяці m, у власній валюті, для
// будь-якого знака m. Різниця між напрямками часу вже означена в
// planFlowNative/planFlowNativePast (підтягувати дату початку чи ні); тут
// лише вибір між ними, щоб чеклист не робив цього вибору в трьох місцях.
func planFlowAtMonth(f store.PlanFlow, today domain.Date, m int, marks planMarks) float64 {
	if m >= 1 {
		return planFlowNative(f, today, m, marks)
	}
	return planFlowNativePast(f, today, m, marks)
}

// receiptDueDate — платіжний день у межах місяця. День береться з дати
// початку потоку (зарплата 17-го приходить 17-го), а обрізання до довжини
// місяця потрібне для 29–31: інакше «31-го» у лютому дало б неіснуючу дату.
func receiptDueDate(month string, day int) string {
	y, mo := 0, 0
	if _, err := fmt.Sscanf(month, "%04d-%02d", &y, &mo); err != nil {
		return ""
	}
	last := time.Date(y, time.Month(mo)+1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1).Day()
	if day < 1 {
		day = 1
	}
	if day > last {
		day = last
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, mo, day)
}

// receiptRows — відмітки для UI.
//
// Частка в портфель для прив'язаної відмітки береться з ТЕПЕРІШНЬОГО
// потоку, а не з його тодішньої ревізії, і це свідома межа: цей список
// живить чеклист, тобто розмову про «зараз». Історія, де точність
// важлива, рахує свою частку сама й із planAsOf — див. buildPlanHistory.
func receiptRows(rs []store.PlanReceipt, flows []store.PlanFlow, rates fx.Rates) []receiptRow {
	share := map[int64]int64{}
	uses := map[int64]string{}
	for _, f := range flows {
		share[f.ID] = f.InvestBP
		uses[f.ID] = f.Uses
	}
	out := make([]receiptRow, 0, len(rs))
	for _, r := range rs {
		bp := r.InvestBP
		use := r.Uses
		if r.FlowID != 0 {
			if s, ok := share[r.FlowID]; ok {
				bp = s
			}
			// Дозвіл — теж із потоку, тією самою підстановкою: прив'язана
			// відмітка своєї колонки не читає (0041), і взяти її звідти
			// означало б показати порожнечу як «можна всюди».
			use = uses[r.FlowID]
		}
		out = append(out, receiptRow{
			ID: r.ID, FlowID: r.FlowID, Month: r.Month, Name: r.Name,
			Amount:    toMoneyJSON(money.New(r.Amount, r.Currency)),
			InvestPct: round2(float64(bp) / 100),
			GivesUAH:  round2(planFlowUAH(float64(r.Amount)/100*float64(bp)/10000, r.Currency, rates)),
			Uses:      domain.PlanUsesList(use),
			Note:      r.Note,
		})
	}
	return out
}

// buildExpectedReceipts — чеклист «що має прийти» на вікно навколо сьогодні.
//
// Розгортається з плану щоразу, а не зберігається другою таблицею: очікування
// — це і є план, і копія його неминуче розійшлась би з оригіналом після
// першої ж правки суми.
//
// Вікно [-planHistoryMonths, +planProvidesMonths] обране не круглим числом,
// а двома наявними межами: назад — рівно те, що показує «План проти факту»
// (глибше відмічати нема куди, стовпчика все одно не буде), уперед — рівно
// вікно, за яким усереднюється «План дає» (далі відмітка на плитку вже не
// вплине). Тобто чеклист навігується саме там, де відмітка щось означає.
//
// Минулі місяці читають план ІЗ ЖУРНАЛУ (planAsOf), як і історія: підставляти
// в травневий рядок сьогоднішню зарплату означало б пропонувати відмітити
// суму, якої тоді не було.
func buildExpectedReceipts(flows []store.PlanFlow, receipts []store.PlanReceipt,
	revs []store.PlanFlowRevision, today domain.Date, rates fx.Rates) []expectedReceipt {
	if len(flows) == 0 && len(revs) == 0 {
		return nil
	}
	byKey := map[markKey]store.PlanReceipt{}
	for _, r := range receipts {
		if r.FlowID != 0 {
			byKey[markKey{flow: r.FlowID, month: r.Month}] = r
		}
	}
	var out []expectedReceipt
	for m := -planHistoryMonths; m <= planProvidesMonths; m++ {
		key := monthKeyAt(today, m)
		src := flows
		if m < 0 {
			if asOf := planAsOf(revs, monthEnd(key)); len(revs) > 0 && !revs[0].ChangedAt.After(monthEnd(key)) {
				src = asOf
			}
		}
		for _, f := range src {
			// Витрати в чеклист не йдуть: картка про надходження, і «чи
			// прийшла комуналка» — питання не про неї.
			if f.Kind != "income" {
				continue
			}
			// Валова сума — тим самим фокусом, що й planFlowGrossUAH: копія
			// потоку зі 100% частки. І БЕЗ відміток: чеклист показує, скільки
			// план ОБІЦЯВ, інакше вже відмічений рядок пропонував би звірити
			// суму сам із собою.
			gross := f
			gross.InvestBP = 10000
			amt := planFlowAtMonth(gross, today, m, nil)
			if amt == 0 {
				continue // цього місяця потік мовчить — відмічати нема чого
			}
			e := expectedReceipt{
				FlowID: f.ID, Name: f.Name, Month: key,
				DueDate: receiptDueDate(key, f.FromDate.Day()),
				Amount:  toMoneyJSON(money.New(int64(math.Round(amt*100)), f.Currency)),
				PlanUAH: round2(planFlowUAH(amt*float64(f.InvestBP)/10000, f.Currency, rates)),
				Uses:    domain.PlanUsesList(f.Uses),
			}
			if r, ok := byKey[markKey{flow: f.ID, month: key}]; ok {
				row := receiptRows([]store.PlanReceipt{r}, flows, rates)[0]
				e.Receipt = &row
			}
			out = append(out, e)
		}
	}
	return out
}

// profileEvents — повернення тіла на горизонті профілю.
//
// Погашення ОВДП і закриття вкладів беруться з того самого розкладу, що й
// дохід (там вони позначені PayRedemption). Закриття фонду в розкладі
// НЕМАЄ — воно живе лише в симуляції, — тож рахується окремо
// domain.AccumCloseValue, і тест стереже, що та функція дає рівно те, що
// випускає симуляція.
func profileEvents(cashflow []domain.CashflowItem, rows []state.FundPositionRow,
	refs []store.Fund, npfRows []state.NPFPositionRow, npfAccounts []domain.NPFAccount,
	flows []store.PlanFlow, marks planMarks,
	today, to domain.Date, rates fx.Rates) []profileEvent {
	var out []profileEvent
	for _, cf := range cashflow {
		if cf.Type != domain.PayRedemption || cf.Date < today || cf.Date > to {
			continue
		}
		u, err := fx.ToUAH(cf.Amount, rates)
		if err != nil {
			continue
		}
		kind, label := "bond", cf.ISIN
		if domain.IsFundISIN(cf.ISIN) {
			continue // у фонда своя подія нижче, з податком на закритті
		}
		if strings.HasPrefix(cf.ISIN, "deposit:") {
			kind, label = "deposit", "вклад"
		}
		out = append(out, profileEvent{
			Date: string(cf.Date), Kind: kind, Label: label,
			AmountUAH: round2(float64(u.Amount()) / 100),
		})
	}

	// Накопичувальний фонд віддає гроші рівно раз — на закритті, і саме
	// цього моменту в розкладі немає взагалі.
	byName := map[string]store.Fund{}
	for _, f := range refs {
		byName[f.Name] = f
	}
	for _, r := range rows {
		ref := byName[r.Fund]
		if ref.Kind != store.FundAccumulating || ref.CloseDate == "" {
			continue
		}
		close := domain.Date(ref.CloseDate)
		if close < today || close > to {
			continue
		}
		closeM := monthOffsetRaw(today, close)
		if closeM < 1 {
			continue
		}
		// MarketValue у рядку документа вже гривневий, тож і подія гривнева.
		v := domain.AccumCloseValue(domain.Accum{
			Value0: r.MarketValue, Cost0: r.CostBasis,
			RatePct: r.ExpectedPct, CloseM: closeM, TaxPct: r.IncomeTaxPct,
		})
		if v <= 0 {
			continue
		}
		out = append(out, profileEvent{
			Date: ref.CloseDate, Kind: "fund", Label: r.Fund, AmountUAH: round2(v),
		})
	}

	// Пенсійний рахунок — та сама природа події, що закриття фонду: гроші
	// стають доступні рівно раз, і в розкладі цього моменту немає.
	//
	// Різниця одна, і вона на користь НПФ: сума на дату доступу залежить не
	// лише від зростання, а й від того, скільки ще внесуть до неї, — тому
	// внески з плану доводяться сюди тим самим вектором, що йде в проєкцію.
	// Без нього подія показувала б суму, до якої припинили вносити сьогодні.
	npfByName := map[string]domain.NPFAccount{}
	for _, a := range npfAccounts {
		npfByName[a.Name] = a
	}
	for _, r := range npfRows {
		acc, ok := npfByName[r.Name]
		if !ok || r.AccessDate == "" {
			continue
		}
		access := domain.Date(r.AccessDate)
		if access < today || access > to {
			continue
		}
		closeM := monthOffsetRaw(today, access)
		if closeM < 1 {
			continue
		}
		// Внески з плану — тим самим ядром, що живить проєкцію
		// (planFlowMonthlyUAH), а не власною арифметикою: періодичність,
		// індексація й відмітки надходжень мусять діяти однаково, інакше
		// подія на осі й крива капіталу розійшлися б саме там, де людина
		// щось у плані змінила.
		var contrib []float64
		dest := domain.NPFPlanDest(acc.ID)
		for _, fl := range flows {
			if fl.Dest != dest {
				continue
			}
			if contrib == nil {
				contrib = make([]float64, closeM)
			}
			for m := 1; m <= closeM; m++ {
				if v := planFlowMonthlyUAH(fl, today, rates, m, marks); v < 0 {
					contrib[m-1] += -v
				}
			}
		}
		if r.ValueUAH <= 0 && contrib == nil {
			continue
		}
		// Ставка НОМІНАЛЬНА власна, а не real_pct: та вже після податку й
		// знецінення, а тут потрібне саме зростання, з якого податок ще
		// доведеться взяти. Виміряне витісняє обіцянку — те саме правило, що
		// в самому рядку позиції.
		rate := r.NavReturnPct
		if rate == 0 {
			rate = r.ExpectedPct
		}
		v := domain.AccumCloseValue(domain.Accum{
			Value0: r.ValueUAH, Cost0: r.CostUAH,
			RatePct: rate, CloseM: closeM,
			TaxPct:         float64(acc.IncomeTaxBP) / 100,
			ContribByMonth: contrib,
		})
		if v <= 0 {
			continue
		}
		out = append(out, profileEvent{
			Date: r.AccessDate, Kind: "npf", Label: r.Name, AmountUAH: round2(v),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// handlePlanTimeline — GET /api/plan. Той самий buildState, що й
// /api/summary (тож дедлайн цілі, крива й точка незалежності — ті самі
// числа, що й на «Майбутньому»), плюс сирі терміни інструментів, яких у
// state.Doc немає: вони потрібні лише картинці, а не сутностям HA.
func (s *Server) handlePlanTimeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	today := domain.NewDate(now)

	doc, err := s.buildState(ctx, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	flows, err := s.st.ListPlanFlows(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	actions, err := s.st.ListPlanActions(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	lots, sales, bonds, pays, err := s.portfolio(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	positions, err := domain.Positions(bonds, pays, lots, sales, today)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rates, err := s.rates(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Свідомо ковтані, як і в state_sources.go: порожній довідник вкладів
	// чи фондів — звичайний стан, не привід валити стрічку.
	termDeposits, _ := s.st.ListTermDeposits(ctx) //nolint:errcheck
	funds, _ := s.st.ListFunds(ctx)               //nolint:errcheck
	npfAccounts, _ := s.st.ListNPFAccounts(ctx)   //nolint:errcheck // те саме: НПФ може не бути

	// Дохід портфеля для профілю — тим самим збирачем, що й «Виплати» зі
	// зведенням. Свого не пишемо: у календаря колись був власний, і фонди
	// повз нього пролітали — одне питання давало дві відповіді
	// (handlers_positions.go). Горизонт дивідендів тут ширший за річний,
	// бо профіль малює форму до дедлайну, а фонд, що замовкає на 13-му
	// місяці, лишив би сходинку, якої в житті немає.
	//
	// Помилка збирача профіль не валить: стрічка малюється й без доходу.
	var portfolioCF []domain.CashflowItem
	var moves, reserveMoves = []store.Deposit(nil), []store.ReserveOp(nil)
	if src, serr := s.loadSources(ctx, today); serr == nil {
		hold := domain.NewHoldings(src.lots, src.sales, src.bonds, src.fundOps, src.fundPrices, src.payoutDays(), today)
		if sch, serr := buildSchedule(src, hold, today, today, profileFundMonths); serr == nil {
			portfolioCF = sch.Cashflow
		}
		// Рух грошей для «Плану проти факту». Читається звідси, а не окремим
		// запитом: loadSources уже сходив по нього для решти документа.
		moves, reserveMoves = src.deposits, src.reserveOps
	}
	// Знімки потрібні лише заради колонки «бракувало». Помилку ковтаємо
	// свідомо: без них картка малює два ряди замість трьох, а не зникає.
	snaps, _ := s.st.ListSnapshots(ctx, today.AddMonths(-planHistoryMonths-1), today) //nolint:errcheck
	// Журнал ревізій — те, з чого читається «яким план БУВ». Помилка так
	// само не валить стрічку: без журналу історія просто виводиться з
	// теперішньої таблиці й каже про це позначкою.
	//
	// Вікно на місяць ширше за саму картку: щоб знати стан на початок
	// найдавнішого показаного місяця, потрібна ревізія, що йому передує.
	revSince := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).
		AddDate(0, -planHistoryMonths-1, 0)
	revs, _ := s.st.ListPlanFlowRevisions(ctx, revSince) //nolint:errcheck
	// Відмітки надходжень. Ковтаємо з тієї ж причини, що й усе вище:
	// невідмічений план — звичайний стан, а не поламана стрічка.
	receipts, _ := s.st.ListPlanReceipts(ctx) //nolint:errcheck
	marks := newPlanMarks(receipts)

	out := timelineDoc{From: string(today)}

	// Горизонт — дедлайн цілі, якщо він далі за мінімум у рік; інакше рік
	// наперед. Той самий мінімум, що й у решти «Майбутнього».
	to := today.AddMonths(12)
	if doc.Forecast != nil && domain.Date(doc.Forecast.Date).After(to) {
		to = domain.Date(doc.Forecast.Date)
	}
	out.To = string(to)

	for _, f := range flows {
		amt := float64(f.Amount) / 100
		if f.Currency != "UAH" {
			if u, cerr := fx.ToUAH(money.New(f.Amount, f.Currency), rates); cerr == nil {
				amt = float64(u.Amount()) / 100
			}
		}
		// invest_bp: стрічка показує, скільки з потоку РЕАЛЬНО йде в
		// портфель — те саме число, що вже рахує plan_provides_uah,
		// інакше смуга на картинці не сходилась би з вердиктом над нею.
		amt *= float64(f.InvestBP) / 10000
		if f.Kind == "expense" {
			amt = -amt
		}
		out.Flows = append(out.Flows, timelineFlow{
			ID: f.ID, Name: f.Name, Kind: f.Kind, Cadence: f.Cadence,
			From: string(f.FromDate), Until: string(f.UntilDate), AmountUAH: round2(amt),
		})
	}

	for _, a := range actions {
		ta := timelineAction{ID: a.ID, Date: string(a.Date), Type: a.Type, Name: a.Name}
		if a.Type == "lock" {
			amt := float64(a.Amount) / 100
			if a.Currency != "UAH" {
				if u, cerr := fx.ToUAH(money.New(a.Amount, a.Currency), rates); cerr == nil {
					amt = float64(u.Amount()) / 100
				}
			}
			ta.AmountUAH = round2(amt)
		}
		out.Actions = append(out.Actions, ta)
	}

	// Погашення ОВДП — лише те, що ще в портфелі (Qty > 0): погашений чи
	// проданий папір на стрічці майбутнього не термін, а історія.
	for _, p := range positions {
		if p.Qty <= 0 || !p.Maturity.Valid() {
			continue
		}
		out.Instruments = append(out.Instruments,
			timelineInstrument{Kind: "bond", Label: p.ISIN, To: string(p.Maturity)})
	}
	for _, d := range termDeposits {
		if !d.Active(today) {
			continue
		}
		out.Instruments = append(out.Instruments, timelineInstrument{
			Kind: "deposit", Label: d.Bank, From: string(d.OpenDate), To: string(d.MaturityDate),
		})
	}
	for _, f := range funds {
		if f.BuyUntil == "" && f.CloseDate == "" {
			continue
		}
		out.Instruments = append(out.Instruments,
			timelineInstrument{Kind: "fund", Label: f.Name, To: f.CloseDate, BuyUntil: f.BuyUntil})
	}

	out.Profile = buildPlanProfile(flows, marks, today, to, rates, portfolioCF)
	if out.Profile != nil {
		out.Profile.Events = profileEvents(portfolioCF, doc.Funds, funds,
			doc.NPF, npfAccounts, flows, marks, today, to, rates)
	}
	out.History = buildPlanHistory(flows, moves, reserveMoves, snaps, revs, receipts, today, rates)
	out.FlowRevisions = flowRevisionRows(revs)
	out.Expected = buildExpectedReceipts(flows, receipts, revs, today, rates)
	out.Receipts = receiptRows(receipts, flows, rates)

	out.Milestones = append(out.Milestones, timelineMilestone{Date: string(today), Label: "сьогодні"})
	if doc.Forecast != nil && doc.Forecast.Date != "" {
		out.Milestones = append(out.Milestones, timelineMilestone{Date: doc.Forecast.Date, Label: "дедлайн цілі"})
	}
	if doc.Independence != nil && doc.Independence.PlanDate != "" {
		out.Milestones = append(out.Milestones,
			timelineMilestone{Date: doc.Independence.PlanDate, Label: "точка незалежності"})
	}

	if doc.Forecast != nil && doc.Forecast.Curve != nil {
		for _, p := range doc.Forecast.Curve.Points {
			out.Curve = append(out.Curve, timelineCurvePoint{
				Date: string(today.AddMonths(p.Month)), Plan: p.Plan,
				Optimistic: p.Optimistic, Pessimistic: p.Pessimistic, Actual: p.Actual,
			})
		}
	}

	writeJSON(w, http.StatusOK, out)
}
