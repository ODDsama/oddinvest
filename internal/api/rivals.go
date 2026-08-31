// Ціна моїх рішень: два рівні грошей проти чотирьох механічних суперників.
//
// Арифметика живе в domain/rival.go; тут ухвалюються рівно два рішення,
// яких той пакет ухвалити не може, бо не знає ні журналів, ні капіталу.
//
// РІШЕННЯ ПЕРШЕ: ЩО ТАКЕ «МОЇ ГРОШІ». Їх два рівні, і вони не зводяться в
// одне число навмисно — той самий хід, що вже зроблений із «зароблено /
// обіцяно» (state_builder.go):
//
//	портфель  — що робить ІНВЕСТОР: облігації, сертифікати, вклади й
//	            гроші на рахунку; внески — журнал гаманця.
//	усі гроші — що робить ЛЮДИНА: те саме плюс подушка, цілі й пенсійний;
//	            внески — чотири журнали разом.
//
// Різниця рівнів не вигадана й не порахована окремо: база другого — це
// рівно state.Capital.TotalUAH(), база першого — рівно та сама сума без
// трьох останніх доданків. На боці внесків різниця — рівно ті самі три
// журнали. Обидві тотожності мають тест, і саме вони роблять законним
// віднімання одного рівня від другого.
//
// НАВІЩО ДРУГИЙ РІВЕНЬ. На живих даних подушка — половина капіталу, і
// доти вона не порівнювалась ні з чим узагалі: бенчмарк «а якби просто
// долари» брав внески з гаманця, а reserve_ops повз гаманець не ходять.
// Рішення тримати половину грошей готівкою — найдорожче з усіх, які
// приймає власник цього застосунку, — не мало ціни ніде, хоча журнал
// рішень уже пише йому втрачену вигоду рядком.
//
// РІШЕННЯ ДРУГЕ: ПОДВІЙНОГО РАХУНКУ НЕМАЄ, І ЦЕ ВЛАСТИВІСТЬ ДАНИХ, А НЕ
// СПОДІВАННЯ. Окремої сутності «переказ» у застосунку немає: переказ
// записується парою «зняття + поповнення» (див. коментар у
// state_month.go про «внесено нетто»). Тож НЕТТО-сума чотирьох журналів і
// є зовнішніми грошима: гроші, перекладені з гаманця під матрац, дають
// −X і +X, а нові гроші дають лише +X. На це є тест.
//
// ЧОГО ТУТ СВІДОМО НЕМАЄ — див. README; найкоротше: суперника «завжди
// верхній рядок помічника» відтворити нізвідки (журнал рішень молодший за
// портфель, а довідник паперів витирається щоранку), і суперника
// «депозит» теж (ряду історичних банківських ставок у базі немає, а
// вигадана ставка зробила б суперника переконливим і хибним).

package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// Рівні. Рядками, бо приходять параметром запиту й їдуть у JSON.
const (
	levelPortfolio = "portfolio"
	levelAll       = "all"
)

// rivalOVDPBucket — строк, який купує ринковий суперник.
//
// Названо константою й показано в відповіді (`ovdp_bucket`), бо це
// ПОЛІТИКА, а не подробиця реалізації: на дворічному строку суперник дав
// би інше число, і мовчазний вибір строку читався б як властивість ринку.
const rivalOVDPBucket = "1y"

// rivalYoungDays — доки порівнянню менше за цей вік, воно каже про момент
// входу, а не про стратегію.
//
// Числа при цьому НЕ глушаться, на відміну від XIRR (min_days) і зведення
// журналу рішень (min_rows): там глушиться СТАВКА, виведена з короткого
// ряду, а тут — просто гроші, і вони такі, які є. Глушиться лише вирок:
// прапорець вмикає в картці прозу про те, що саме це число означає.
const rivalYoungDays = 90

type rivalRow struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// TerminalUAH — вартість суперника сьогодні, грн-екв.
	TerminalUAH float64 `json:"terminal_uah"`
	// DiffUAH — наскільки МОЇ гроші попереду суперника (може бути
	// від'ємним: у цьому й сенс вимірювання).
	DiffUAH float64 `json:"diff_uah"`
	DiffPct float64 `json:"diff_pct"`
	// PointsDiff — НАСКІЛЬКИ я попереду цього суперника на кожен день
	// сітки. Саме воно малюється, а не самі вартості.
	//
	// Причина знайдена на живих даних, у браузері. Абсолютні криві на
	// молодому портфелі — це пʼять майже однакових сходинок: форму їм
	// задають внески, а не рішення, і різниці в кілька сотень гривень на
	// шкалі до сорока тисяч не видно взагалі. Тобто графік малював те,
	// що й так відоме («я вносив гроші»), і ховав те, заради чого
	// існує. Крива різниці має нуль осмисленою лінією: вище — я
	// попереду, нижче — суперник.
	//
	// Рахується ТУТ, а не в браузері: CLAUDE.md §5. Остання точка мусить
	// дорівнювати DiffUAH — на це є тест.
	PointsDiff []float64 `json:"points_diff,omitempty"`
	Why        string    `json:"why,omitempty"`
}

type rivalsResp struct {
	Level      string `json:"level"`
	LevelLabel string `json:"level_label"`
	// ActualUAH — мої гроші сьогодні на цьому рівні.
	ActualUAH float64 `json:"actual_uah"`
	// Days / Actual — сітка й крива факту, однакової довжини з Points
	// кожного суперника, що не мовчить.
	Days   []string  `json:"days"`
	Actual []float64 `json:"actual"`
	// OpenUAH — скільки грошей уже було на руках у перший день вікна.
	// Входить у порівняння першим внеском: суперник дістає рівно те, що
	// мав я, у той самий день, за тодішньою ціною.
	OpenUAH float64 `json:"open_uah"`
	// InUAH — скільки грошей зайшло в гру НЕТТО, грн-екв. за курсами
	// їхніх днів: відкриття плюс дальші рухи. Це і є термінал «гривні під
	// матрацом», і показується він окремим полем, щоб різницю можна було
	// перевірити відніманням.
	InUAH      float64    `json:"in_uah"`
	Flows      int        `json:"flows"`
	FirstDay   string     `json:"first_day,omitempty"`
	DayCount   int        `json:"day_count"`
	Young      bool       `json:"young"`
	OVDPBucket string     `json:"ovdp_bucket"`
	Rivals     []rivalRow `json:"rivals"`
	Note       string     `json:"note,omitempty"`
	// Why — чому порівняння не склалось узагалі. Порожні рядки замість
	// нього показали б чотири нулі, а нуль на цьому екрані читається як
	// «усе втрачено», а не як «нема чого рахувати».
	Why string `json:"why,omitempty"`
}

// row — рядок суперника за ключем. Порожній, коли такого немає.
func (r rivalsResp) row(key string) rivalRow {
	for _, x := range r.Rivals {
		if x.Key == key {
			return x
		}
	}
	return rivalRow{}
}

var rivalLabels = map[string]string{
	domain.RivalUAHCash:    "Гривня під матрацом",
	domain.RivalUSDCash:    "Долари під матрацом",
	domain.RivalEURCash:    "Євро під матрацом",
	domain.RivalOVDPMarket: "Ринкова ОВДП, " + rivalOVDPBucket,
}

var rivalLevelLabels = map[string]string{
	levelPortfolio: "Портфель",
	levelAll:       "Усі гроші",
}

// handleRivals — GET /api/rivals?level=portfolio|all
func (s *Server) handleRivals(w http.ResponseWriter, r *http.Request) {
	level := r.URL.Query().Get("level")
	if level == "" {
		level = levelPortfolio
	}
	if _, ok := rivalLevelLabels[level]; !ok {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("невідомий рівень %q — буває portfolio або all", level))
		return
	}
	ctx := r.Context()
	doc, err := s.buildState(ctx, time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out, err := s.rivals(ctx, doc, level)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// rivals — увесь рахунок над УЖЕ ЗІБРАНИМ документом.
//
// Документ приходить аргументом із тієї самої причини, що й у benchmark:
// buildState — найдорожчий шлях у бекенді, і обробник його вже має.
//
// ВІКНО ПОЧИНАЄТЬСЯ ТАМ, ДЕ ПОЧИНАЄТЬСЯ ЗНАННЯ — на першому добовому
// знімку, — і це рішення коштувало живої перевірки, тож ось воно
// повністю.
//
// Спокуса очевидна: почати від першого руху в журналі, бо там починаються
// гроші. На живих даних перший рух — квітень 2024-го, а перший знімок —
// липень 2026-го. Тобто МОЄЇ кривої на перших двох роках немає взагалі:
// застосунок тоді не знав ні капіталу, ні складу портфеля. Намальована
// від нуля, вона показала б два роки порожнечі й вертикальний стрибок —
// картинку, яка виглядає як обвал і не є ним.
//
// Друга половина того самого: суперник мусить мати ціну на кожну дату
// свого потоку («все або нічого»). Рівні розміщення Мінфіну лежать за рік
// із гаком, євро — від дня встановлення. Від квітня 2024-го обидва
// мовчать НАЗАВЖДИ, і порівнювати лишається з одним доларом — тобто рівно
// те, що вже було до цієї роботи.
//
// Вікно від першого знімка лікує обидві біди однією умовою, і чесність
// його в тому, що воно нічого не приховує: гроші, які були на руках у той
// день, входять у порівняння ПЕРШИМ ВНЕСКОМ (OpenUAH). Суперник дістає
// рівно те, що мав я, у той самий день, за тодішньою ціною. Чого це вікно
// не вміє — сказати «а якби я робив так від 2024-го»; на це відповіді
// немає й не могло бути, бо історії капіталу за той час не існує.
func (s *Server) rivals(ctx context.Context, doc *state.Doc, level string) (rivalsResp, error) {
	out := rivalsResp{Level: level, LevelLabel: rivalLevelLabels[level],
		OVDPBucket: rivalOVDPBucket, Rivals: []rivalRow{}}

	today := domain.NewDate(time.Now())
	snaps, err := s.st.ListSnapshots(ctx, "", today)
	if err != nil {
		return out, err
	}
	if len(snaps) == 0 {
		out.Why = "порівнювати ще нема з чим: добових знімків немає, тож історії власного капіталу застосунок не має"
		return out, nil
	}
	from := snaps[0].Date
	if from > today {
		out.Why = "перший знімок датований майбутнім"
		return out, nil
	}

	ar := newAsOfRates(s.st)
	flows, err := s.rivalFlows(ctx, level, ar, from)
	if err != nil {
		return out, err
	}
	out.Flows = len(flows)
	out.Note = ar.note()

	// Відкриття вікна — перший внесок. Окремої сутності йому не треба:
	// «те, що вже лежало» і «те, що донесли» — це те саме питання «скільки
	// грошей зайшло в гру до цього дня», і рушій відповідає на нього
	// однаково.
	out.OpenUAH = round2(float64(snapshotLevelUAH(snaps[0], level)) / 100)
	if out.OpenUAH == 0 && len(flows) == 0 {
		// Ані копійки у вікні. Суперники порахувались би — усі в нуль, —
		// і чотири нулі в таблиці читаються як «усе втрачено», а не як
		// «рахувати нічого». Перший день життя бази виглядає саме так:
		// демон кладе знімок одразу, а грошей ще немає.
		out.Why = "порівнювати ще нема з чим: у вікні не було жодних грошей"
		return out, nil
	}
	flows = append([]domain.Contribution{{On: from, UAH: out.OpenUAH}}, flows...)

	days := domain.DaysGrid(from, today)
	out.FirstDay, out.DayCount = string(from), len(days)
	out.Young = len(days) < rivalYoungDays

	in, err := s.rivalInputs(ctx, from)
	if err != nil {
		return out, err
	}
	actual := rivalActual(snaps, doc, level, days)
	out.Actual = actual
	out.ActualUAH = round2(actual[len(actual)-1])
	out.Days = make([]string, len(days))
	for i, d := range days {
		out.Days[i] = string(d)
	}

	for _, rv := range domain.RunRivals(flows, days, in) {
		row := rivalRow{Key: rv.Key, Label: rivalLabels[rv.Key], Why: rv.Why}
		if rv.Why == "" {
			row.TerminalUAH = round2(rv.TerminalUAH)
			row.DiffUAH = round2(out.ActualUAH - row.TerminalUAH)
			if row.TerminalUAH != 0 {
				row.DiffPct = round2(row.DiffUAH / math.Abs(row.TerminalUAH) * 100)
			}
			row.PointsDiff = diffSeries(actual, rv.Points)
		}
		if rv.Key == domain.RivalUAHCash {
			out.InUAH = row.TerminalUAH
		}
		out.Rivals = append(out.Rivals, row)
	}
	return out, nil
}

// diffSeries — наскільки я попереду суперника на кожен день.
//
// Довжини рівні за побудовою (одна сітка на всіх), але коротший ряд тут
// обрізав би криву мовчки, а не впав, — тож беремо мінімум явно.
func diffSeries(mine, rival []float64) []float64 {
	n := len(mine)
	if len(rival) < n {
		n = len(rival)
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		out[i] = round2(mine[i] - rival[i])
	}
	return out
}

// rivalFlows — зовнішні гроші рівня, зведені в гривню курсом СВОГО дня.
//
// Порядок журналів тут не має значення (RunRivals сортує сам), а от
// СКЛАД — має, і він же є означенням рівня.
func (s *Server) rivalFlows(ctx context.Context, level string, ar *asOfRates, from domain.Date) ([]domain.Contribution, error) {
	out := []domain.Contribution{}
	add := func(on domain.Date, minor int64, cur string) error {
		if minor == 0 || on < from {
			// Строго ДО дня відкриття — уже враховано в OpenUAH.
			//
			// А от рух У САМ день відкриття рахується потоком, і це не
			// недбалість: добовий знімок кладеться о 06:10 Києва
			// (jobs.RunDaily), тобто описує ранок того дня, а не його
			// кінець. Люди вносять операції вдень. Відсікання «<= from»
			// мовчки з'їдало б усе, що зроблено після сніданку першого
			// дня, — тобто саме той клас втрати грошей, від якого тут
			// усюди стоять сторожі.
			return nil
		}
		u, err := ar.uah(ctx, money.New(minor, cur), on)
		if err != nil {
			return err
		}
		if u == 0 {
			return nil // курсу на цю дату немає; ar.note() уже це порахував
		}
		out = append(out, domain.Contribution{On: on, UAH: float64(u) / 100})
		return nil
	}

	// Гаманець: поповнення й зняття. Купівлі сюди НЕ пишуться (імпорт
	// навіть застерігає про подвоєння, коли ручний рух дублює операцію),
	// тож це справді зовнішні гроші, а не обіг усередині портфеля.
	cash, err := s.st.ListDeposits(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range cash {
		if err := add(d.Date, d.Amount, d.Currency); err != nil {
			return nil, err
		}
	}
	if level != levelAll {
		return out, nil
	}

	res, err := s.st.ListReserveOps(ctx)
	if err != nil {
		return nil, err
	}
	for _, o := range res {
		if err := add(o.Date, o.Amount, o.Currency); err != nil {
			return nil, err
		}
	}
	goals, err := s.st.ListGoalOps(ctx)
	if err != nil {
		return nil, err
	}
	for _, o := range goals {
		if err := add(o.Date, o.Amount, o.Currency); err != nil {
			return nil, err
		}
	}
	// Пенсійний. Валюти в NPFOp немає, бо її немає й у самого фонда:
	// «Династія» веде рахунок у гривні, і другий код валюти був би
	// полем, якому нізвідки взятись.
	npf, err := s.st.ListNPFOps(ctx)
	if err != nil {
		return nil, err
	}
	for _, o := range npf {
		if err := add(o.Date, o.Amount, money.UAH); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// rivalInputs — ряди курсів і рівнів розміщення, кожен ОДНИМ запитом.
//
// Не по запиту на день: рік історії — це ~365 звернень на одне
// відкриття «Порівняння», і саме такий N+1 нещодавно прибирали з
// довідника брокерів. Ряди малі за природою (курс — по точці на день,
// аукціони — раз-два на тиждень), тож читаються цілком і розвʼязуються
// в памʼяті.
func (s *Server) rivalInputs(ctx context.Context, from domain.Date) (domain.RivalInputs, error) {
	var out domain.RivalInputs
	for _, c := range []struct {
		code string
		to   *domain.Quotes
	}{{money.USD, &out.USD}, {money.EUR, &out.EUR}} {
		pts, err := s.st.RatesSince(ctx, c.code, from)
		if err != nil {
			return out, err
		}
		// Точка ПЕРЕД початком сітки потрібна окремо: історія курсів
		// помісячна, і без неї суперник замовк би на всіх днях до
		// першого числа наступного місяця.
		if p, err := s.st.RatePointOnOrBefore(ctx, c.code, from); err == nil && p.RateE4 > 0 {
			pts = append(pts, p)
		}
		q := make(domain.Quotes, 0, len(pts))
		for _, p := range pts {
			q = append(q, domain.Quote{On: p.Date, V: fx.Major(p.RateE4)})
		}
		*c.to = q
	}

	lv, err := s.st.AuctionLevels(ctx, money.UAH, rivalOVDPBucket)
	if err != nil {
		return out, err
	}
	for _, p := range lv {
		if p.IncomeBP <= 0 {
			continue
		}
		// IncomeBP — базисні пункти (1518 = 15.18%), а суперник хоче
		// частку.
		out.OVDP = append(out.OVDP, domain.Quote{On: p.Date, V: float64(p.IncomeBP) / 10_000})
	}
	return out, nil
}

// rivalActual — крива МОЇХ грошей на сітці днів.
//
// Знімки приходять готовими, а не читаються тут: вікно порівняння теж
// виводиться з них, і другий прочит міг би дати інший перший день.
//
// Дві тонкощі, і обидві вже коштували б неправди на графіку:
//
//  1. Знімка на якийсь день може не бути (демон не працював) — тоді
//     береться останній відомий, а не нуль: капітал у той день не
//     зникав, зникла лише відповідь про нього.
//
//  2. ОСТАННЮ точку дає документ, а не знімок: сьогоднішній знімок
//     кладеться раз на добу й на момент запиту може ще не існувати, а
//     різниця «мої проти суперника» читається саме з кінця кривої.
//
//  3. ЗСУВУ НА ДОБУ ТУТ НЕМАЄ, і це перевірено, а не забуто.
//
//     Спокуса була: демон пише знімок о 06:10 Києва (jobs.RunDaily), тож
//     рядок за дату D мав би описувати кінець D−1, а гроші, внесені вдень,
//     суперник рахує одразу — звідси провали кривої до −3 200 ₴ на
//     портфелі в 38 000. Зсув «за день D беремо знімок D+1» ПЕРЕВІРЕНО НА
//     ЖИВИХ ДАНИХ і зробив гірше: перша точка стрибнула на +6 415 ₴.
//     Причина в тому, що 06:10 — не єдиний час запису: needsCatchUp жене
//     прогін і при старті демона, тобто серед дня, і знімок за D тоді
//     містить операції самого D. Час доби в знімка ненадійний за
//     будовою, і жоден сталий зсув його не виправить.
//
//     Тому крива лишається як є, а картка каже вголос: середина
//     намальована з добових знімків і може відставати від внеску на день.
//     Кінець кривої від цього не страждає — останню точку дає документ.
func rivalActual(snaps []store.Snapshot, doc *state.Doc, level string, days []domain.Date) []float64 {
	out := make([]float64, 0, len(days))
	last, si := 0.0, 0
	for _, d := range days {
		for si < len(snaps) && snaps[si].Date <= d {
			last = float64(snapshotLevelUAH(snaps[si], level)) / 100
			si++
		}
		out = append(out, last)
	}
	out[len(out)-1] = docLevelUAH(doc, level)
	return out
}

// snapshotLevelUAH / docLevelUAH — рівень грошей із двох різних джерел, і
// формула в них ОДНА на обидва.
//
// Знімок і документ — це те саме питання на різні дати, тож розійтись їм
// не можна: розбіжність виглядала б стрибком кривої в останній точці, а
// не помилкою.
func snapshotPortfolioUAH(sn store.Snapshot) int64 {
	return sn.NominalUAHEq + sn.AccountUAH + sn.FundsUAH + sn.DepositsUAH
}

func snapshotLevelUAH(sn store.Snapshot, level string) int64 {
	if level == levelAll {
		return snapshotCapitalUAH(sn)
	}
	return snapshotPortfolioUAH(sn)
}

func docLevelUAH(doc *state.Doc, level string) float64 {
	if level == levelAll {
		return doc.CapitalUAH
	}
	return round2(doc.NominalUAHEq + doc.AccountUAH + doc.FundsUAH + doc.DepositsUAH)
}
