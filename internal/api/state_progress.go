// Прогрес: віхи, серія внесків і поле колекції.
//
// ЧОМУ ЦЕ ВЗАГАЛІ ПРИЙНЯТНО
//
// Це гейміфікація, і межа, яка робить її чесною, одна: КОЖНЕ ЧИСЛО ТУТ
// ВИМІРЯНЕ, ЖОДНЕ НЕ НАРАХОВАНЕ. Прибери гру — числа лишаться ті самі,
// просто читатимуться гірше. Звідси випливає все інше в цьому файлі, і
// саме тому нижче немає чотирьох речей:
//
//   — ОЧОК ЗА АКТИВНІСТЬ. Ні за відкриття застосунку, ні за «сім днів
//     поспіль», ні за перегляд сторінки. Застосунок цього не міряє, і
//     винагороджувати поведінку означало б завести перший показник, не
//     підпертий даними. Якщо колись захочеться — це вимагатиме почати
//     ЗБИРАТИ поведінку, і ось тоді про це треба сперечатись окремо;
//   — ДОКОРУ ЗА РОЗРИВ СЕРІЇ. Розрив показаний фактом рівно один раз
//     (Streak.BrokenOn) і не оцінений жодним полем. Причина не в
//     делікатності: застосунок відкривають, коли гроші й так болять, і
//     сором — найгірший спосіб привести людину туди, де ухвалюють
//     рішення про гроші;
//   — ПОРІВНЯННЯ З ЧУЖИМИ ПОРТФЕЛЯМИ. Даних немає, а вигадана медіана
//     «таких, як ти» була б рівно тим числом, яке виглядає виведеним і
//     насправді вигадане;
//   — «ЗІБРАТИ ВСЕ» ЯК ЦІЛІ. Поле колекції — карта того, що є, а не
//     список до закриття; ліміт на один рік погашень існує саме тому.
//
// ЧОМУ ЦЕ ОКРЕМА ФАЗА, А НЕ ЧАСТИНА buildState
//
// Той самий довід, що й у черги задач: прогрес читає ГОТОВИЙ документ,
// а понад нього ще й знімки та помісячний рух грошей. Покласти це
// всередину buildState означало б платити обходом усієї історії на
// КОЖНОМУ POST /api/whatif, де прогрес не потрібен взагалі.
//
// ЧОМУ ОКРЕМИЙ МАРШРУТ, А НЕ ПОЛЕ ДОКУМЕНТА
//
// Віхи не їдуть у документ стану, тобто їх не бачить ні MQTT, ні добовий
// знімок, ні інтеграція Home Assistant. Це свідомо: документ публікується
// щодня й лягає в знімок, а прогрес коштує обходу всієї історії. Коли
// віхи справді знадобляться в HA, перенести їх у документ — окрема
// зміна контракту (contract/, TestFixtureUpToDate і репозиторій
// ha-oddinvest), а не побічний ефект.

package api

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// progressDoc — відповідь GET /api/progress.
type progressDoc struct {
	GeneratedAt string `json:"generated_at"`

	// Level — КІЛЬКІСТЬ ЗІБРАНИХ ВІХ, а не окуляри. Поле є у відповіді, а
	// не рахується в браузері, саме тому, що воно показане поруч зі
	// списком віх і мусить дорівнювати їх підрахунку.
	Level   int `json:"level"`
	LevelOf int `json:"level_of"`

	// NextKey — ключ найближчої віхи, а не її копія. Сама віха вже їде
	// в Milestones, і другий її екземпляр у відповіді розійшовся б із
	// першим при першій же правці. Порожньо означає «попереду немає
	// нічого, до чого відома відстань» — див. pickNext.
	NextKey string `json:"next_key,omitempty"`

	Streak     streakDoc     `json:"streak"`
	Discipline disciplineDoc `json:"discipline"`
	Collection collectionDoc `json:"collection"`
	Milestones []milestone   `json:"milestones"`
}

// streakDoc — місяці поспіль, у яких місячний план внесків виконано.
type streakDoc struct {
	Months int `json:"months"`
	Best   int `json:"best"`
	// BrokenOn — місяць, у якому серія почалась заново. Факт, і більше
	// нічого: жодного поля, яке б його оцінювало, тут немає навмисно.
	BrokenOn string `json:"broken_on,omitempty"`

	// KnownFrom / UnknownBefore — З ЯКОГО МІСЯЦЯ ВЗАГАЛІ Є ЩО СУДИТИ.
	//
	// Місяць зараховується, коли внесено не менше, ніж ціль ТОГО місяця,
	// а ціль минулого місяця відома лише зі знімка, зробленого тоді.
	// До першого знімка судити нічим — і мовчазний нуль читався б як
	// «ти зривався», хоч насправді це «застосунок тоді ще не дивився».
	KnownFrom      string `json:"known_from,omitempty"`
	UnknownBefore  bool   `json:"unknown_before,omitempty"`
	MonthsMeasured int    `json:"months_measured"`

	// Marks — та сама серія, розкладена помісячно.
	//
	// Числа тут не нові: want і got усередині buildStreak рахувались і
	// доти, просто згорталися в одне число й викидались. Смужка — це
	// вони самі, і саме тому вона не може розійтись із Months.
	Marks []streakMark `json:"marks,omitempty"`
}

// streakMark — один місяць смужки.
//
// Known і Hit — РІЗНІ ПИТАННЯ, і саме тому їх двоє. Known:false означає
// «знімка з ціллю за той місяць немає, судити нічим», а Hit:false —
// «ціль була, внеску не вистачило». Один прапорець змусив би малювати
// власну сліпоту як зрив плану — рівно те, проти чого стоїть уся шапка
// цього файлу.
type streakMark struct {
	Month string `json:"month"`
	Known bool   `json:"known"`
	Hit   bool   `json:"hit"`

	// TargetUAH є лише у відомого місяця, ContribUAH — завжди: внесок
	// береться з подій руху грошей і від знімків не залежить зовсім.
	TargetUAH  float64 `json:"target_uah,omitempty"`
	ContribUAH float64 `json:"contrib_uah,omitempty"`
}

// disciplineDoc — частка покупок, узятих із верхнього рядка помічника.
//
// ВІКНА НЕМАЄ НАВМИСНО. /api/decisions віддає журнал цілком, без
// параметра періоду, і числа тут беруться з тієї самої функції над тими
// самими рядками. Написати «за 12 місяців» означало б або вигадати
// вікно, яке ні на що не впливає, або завести його ще й у журналі — і
// тоді доріжка й журнал міряли б різні зрізи, тобто рівно та розбіжність,
// проти якої існує TestProgressReconciles.
type disciplineDoc struct {
	TopRow int `json:"top_row"`
	Total  int `json:"total"`
	// Enough — чи журнал уже досить довгий, щоб із нього щось читати.
	// Той самий поріг, що й у самому журналі (decisionsMinRows): один
	// вдалий вибір із одного — це 100%, і показувати таке означає обіцяти
	// точність, якої немає.
	Enough bool `json:"enough"`
}

// collectionDoc — поле «роки погашень × валюти».
type collectionDoc struct {
	Currencies []string        `json:"currencies"`
	Rows       []collectionRow `json:"rows"`
	Filled     int             `json:"filled"`
	Of         int             `json:"of"`
	// Note — чому заповнене поле НЕ є ціллю саме по собі. Проза тут, а не
	// в UI, з тієї ж причини, що й у черги задач: споживачів може стати
	// двоє, а формулювання мусить лишитись одне.
	Note string `json:"note"`
}

type collectionRow struct {
	Year  int    `json:"year"`
	Cells []bool `json:"cells"`
}

// milestone — одна віха.
type milestone struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	// Note — готова проза, як у state.Task: споживачів у неї може стати
	// двоє, і формулювання мусить бути одне.
	Note string `json:"note"`

	// Earned і EarnedOn — РІЗНІ ПИТАННЯ, і саме тому їх двоє.
	//
	// Дата є не в кожної зібраної віхи. «Перший папір» знає її з журналу
	// лотів, «Перший мільйон» — зі знімків. А «Обіграв просто долари» і
	// «Чотири види в портфелі» — це стан, який став правдою колись до
	// того, як застосунок почав вести знімки; дати в нього немає НІДЕ.
	// Один прапорець (earned_on != null) змусив би або вигадати день,
	// або оголосити зібрану віху незібраною.
	//
	// Ціна названа: банер святкування вибирає лише з датованих, і віха
	// без дати в ньому не з'явиться ніколи.
	Earned   bool   `json:"earned"`
	EarnedOn string `json:"earned_on,omitempty"`

	// Left — СКІЛЬКИ ЛИШИЛОСЬ, готовою прозою і в одиницях самої віхи:
	// «лишилось 24 636 ₴», «лишилось 4 місяці поспіль». Відсоток каже,
	// наскільки далеко зайшов, і не каже, що зробити; гривні кажуть.
	//
	// Порожньо в зібраної віхи й у тієї, чий ProgressPct дорівнює
	// progressNoProgress: коли міряти нічим, то й відстані немає, і
	// написати сюди щось означало б вигадати її. Правило одне на всі
	// віхи, винятків немає.
	//
	// Проза тут, а не в UI, з тієї ж причини, що й Note: споживачів у неї
	// вже двоє — герой «Шляху» й рядок «Огляду».
	Left string `json:"left,omitempty"`

	// ProgressPct — скільки пройдено, коли це можна виміряти. -1 означає
	// «виміряти нічим», і UI показує прочерк, а не нуль: у незібраної
	// віхи нуль і невідомість читаються однаково, а означають різне.
	ProgressPct int `json:"progress_pct"`
}

const progressNoProgress = -1

// milestoneCount — скільки віх у наборі. Число зашите ТУТ, а не
// виводиться з len(): набір сталий, і зміна його довжини мусить бути
// свідомою — рівень людини («6 із 14») інакше мовчки поїхав би від
// додавання чи прибирання однієї віхи.
const milestoneCount = 14

// buildProgress — сам прогрес. Чиста функція над готовими даними:
// жодного запиту, тож її поведінку читають згори вниз.
func buildProgress(
	doc *state.Doc,
	src *sources,
	snaps []store.Snapshot,
	ev []flowEvent,
	dec *decisionsSummary,
	bench *benchResult,
	today domain.Date,
) progressDoc {
	out := progressDoc{
		GeneratedAt: doc.GeneratedAt,
		LevelOf:     milestoneCount,
		Streak:      buildStreak(snaps, ev, today),
		Collection:  buildCollection(doc),
	}
	if dec != nil {
		out.Discipline = disciplineDoc{TopRow: dec.Followed, Total: dec.Count, Enough: true}
	}
	out.Milestones = buildMilestones(doc, src, snaps, out.Streak, bench, today)
	for _, m := range out.Milestones {
		if m.Earned {
			out.Level++
		}
	}
	out.NextKey = pickNext(out.Milestones)
	return out
}

// ---------------------------------------------------------------------
// Серія
// ---------------------------------------------------------------------

// buildStreak — місяці поспіль, у яких внесено не менше цілі того місяця.
//
// ВНЕСЕНО береться з подій руху грошей (cashflow.go), а не зі знімків:
// саме ця розкладка вже звірена зі зведенням тестом
// TestCashflowStatementReconciles, тобто вона єдина, про яку відомо, що
// вона не розходиться з рахунком.
//
// ЦІЛЬ береться зі ЗНІМКА того місяця, а не з сьогоднішніх налаштувань.
// Ціль живе в налаштуваннях і історії не має; узявши сьогоднішню, ми
// переписували б минуле щоразу, коли її змінюють, — підняв ціль удвічі й
// заднім числом «зривався» пів року.
func buildStreak(snaps []store.Snapshot, ev []flowEvent, today domain.Date) streakDoc {
	// Внесено по місяцях. Тільки contribution: купон і погашення теж
	// збільшують рахунок, але вони не є ТВОЇМ внеском, а серія саме про
	// нього.
	got := map[string]int64{}
	for _, e := range ev {
		if e.Kind == flowContribution {
			got[monthOf(e.Date)] += e.UAH
		}
	}

	// Ціль по місяцях — з ОСТАННЬОГО знімка місяця: він бачив місяць
	// найповніше. Нульова ціль означає «не задана», і такий місяць
	// судити нічим.
	want := map[string]int64{}
	sorted := append([]store.Snapshot(nil), snaps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Date < sorted[j].Date })
	for _, sn := range sorted {
		if sn.MonthTargetUAH > 0 {
			want[monthOf(sn.Date)] = sn.MonthTargetUAH
		}
	}
	if len(want) == 0 {
		return streakDoc{UnknownBefore: len(snaps) == 0}
	}

	months := make([]string, 0, len(want))
	for m := range want {
		months = append(months, m)
	}
	sort.Strings(months)

	out := streakDoc{KnownFrom: months[0], MonthsMeasured: len(months)}
	// Поточний місяць у серію НЕ входить: він ще не закінчився, і
	// зарахувати його означало б святкувати наперед, а не зарахувати —
	// обірвати серію першого ж числа.
	nowMonth := monthOf(today)

	// Смужка: СУЦІЛЬНИЙ ряд від першого відомого місяця до попереднього.
	//
	// Суцільний, а не «лише виміряні»: діра в знімках дає клітинку
	// known:false, і саме вона показує розрив ЗНАННЯ. Стиснувши ряд до
	// відомих місяців, ми поставили б поруч два місяці, між якими
	// насправді лежить третій, — і серія на смужці читалась би довшою за
	// ту, яку рахує цикл нижче.
	//
	// ВІКНА НЕМАЄ. Смужка віддається цілком, скільки її є: коротка й
	// обрізана виглядають однаково, а означають різне.
	//
	// Поточного місяця в ній немає з того самого доводу, що й у серії, —
	// він ще не закінчився.
	for m := months[0]; m != "" && m < nowMonth; m = nextMonth(m) {
		mk := streakMark{Month: m, Known: want[m] > 0, ContribUAH: float64(got[m]) / 100}
		if mk.Known {
			mk.TargetUAH = float64(want[m]) / 100
			mk.Hit = got[m] >= want[m]
		}
		out.Marks = append(out.Marks, mk)
	}

	streak, best := 0, 0
	prev := ""
	for _, m := range months {
		if m == nowMonth {
			continue
		}
		// Діра в місяцях — це теж обрив знання, а не пропущений план:
		// знімків за той місяць немає, тож судити нічим.
		if prev != "" && !isNextMonth(prev, m) {
			streak = 0
		}
		prev = m
		if got[m] >= want[m] {
			streak++
			if streak > best {
				best = streak
			}
			continue
		}
		if streak > 0 {
			out.BrokenOn = m
		}
		streak = 0
	}
	out.Months, out.Best = streak, best
	return out
}

func monthOf(d domain.Date) string { return string(d)[:7] }

// isNextMonth — чи «b» іде рівно за «a» («2026-01» → «2026-02»).
func isNextMonth(a, b string) bool {
	var ay, am, by, bm int
	if _, err := fmt.Sscanf(a, "%d-%d", &ay, &am); err != nil {
		return false
	}
	if _, err := fmt.Sscanf(b, "%d-%d", &by, &bm); err != nil {
		return false
	}
	return ay*12+am+1 == by*12+bm
}

// nextMonth — місяць після «m» («2026-12» → «2027-01»). Своя, а не
// isNextMonth: та відповідає на питання, а ця рухає лічильник.
//
// Порожньо, коли рядок не є місяцем: смужка тоді просто закінчується, а
// не крутиться вічно.
func nextMonth(m string) string {
	var y, mo int
	if _, err := fmt.Sscanf(m, "%d-%d", &y, &mo); err != nil {
		return ""
	}
	if mo >= 12 {
		return fmt.Sprintf("%04d-01", y+1)
	}
	return fmt.Sprintf("%04d-%02d", y, mo+1)
}

// ---------------------------------------------------------------------
// Поле колекції
// ---------------------------------------------------------------------

// collectionCurrencies — колонки поля. Ті самі три, що й усюди в
// застосунку, і в тому самому порядку: LadderRow тримає рівно їх.
var collectionCurrencies = []string{"UAH", "USD", "EUR"}

// buildCollection — поле «роки погашень × валюти» прямо з драбини.
//
// Жодного власного підрахунку: клітинка заповнена тоді й лише тоді, коли
// в драбині за цей рік у цій валюті є номінал. Драбина — те саме число,
// що показує «Портфель → Ліміти», і другий спосіб її порахувати дав би
// другу правду про той самий портфель.
func buildCollection(doc *state.Doc) collectionDoc {
	out := collectionDoc{
		Currencies: collectionCurrencies,
		Note: "Заповнене поле — не ціль сама по собі: діра в якомусь році означає " +
			"рік, коли тіло не повертається нізвідки, а порожня колонка — валюту, " +
			"якої в портфелі просто немає. Ліміт на один рік погашень існує саме тому.",
	}
	for _, r := range doc.Ladder {
		if r.Year == 0 {
			continue
		}
		cells := []bool{r.UAH > 0, r.USD > 0, r.EUR > 0}
		for _, c := range cells {
			if c {
				out.Filled++
			}
		}
		out.Of += len(cells)
		out.Rows = append(out.Rows, collectionRow{Year: r.Year, Cells: cells})
	}
	return out
}

// ---------------------------------------------------------------------
// Віхи
// ---------------------------------------------------------------------

// capitalThresholds — пороги капіталу, які відзначаються окремою віхою.
// Три, а не сходинка кожні сто тисяч: віха мусить лишатись подією.
var capitalThresholds = []struct {
	key, title string
	uah        float64
}{
	{"first_100k", "Перша сотня тисяч", 100_000},
	{"first_1m", "Перший мільйон", 1_000_000},
	{"first_2m", "Два мільйони", 2_000_000},
}

func buildMilestones(
	doc *state.Doc, src *sources, snaps []store.Snapshot,
	streak streakDoc, bench *benchResult, today domain.Date,
) []milestone {
	out := make([]milestone, 0, milestoneCount)
	add := func(m milestone) { out = append(out, m) }

	// --- 1. Перший папір ---
	first := ""
	for _, l := range src.lots {
		if first == "" || string(l.BuyDate) < first {
			first = string(l.BuyDate)
		}
	}
	add(milestone{
		Key: "first_bond", Title: "Перший папір",
		Note:        noteOr(first != "", "куплено "+first, "ще не куплено жодного"),
		Left:        noteOr(first != "", "", "лишилось купити перший"),
		Earned:      first != "",
		EarnedOn:    first,
		ProgressPct: pctOf(first != ""),
	})

	// --- 2-4. Пороги капіталу ---
	//
	// Дата береться з першого знімка, який перетнув поріг. Знімків до
	// певного дня немає взагалі, і тоді поріг може бути пройдений без
	// дати — саме той випадок, заради якого Earned і EarnedOn розділені.
	cap0 := capitalNow(doc)
	for _, t := range capitalThresholds {
		when := firstSnapshotAtLeast(snaps, t.uah)
		earned := cap0 >= t.uah
		add(milestone{
			Key: t.key, Title: t.title,
			Note: noteOr(earned,
				noteOr(when != "", "пройдено "+when, "пройдено до першого знімка"),
				fmt.Sprintf("%s із %s", uah(cap0), uah(t.uah))),
			Left:   noteOr(earned, "", "лишилось "+uah(t.uah-cap0)),
			Earned: earned, EarnedOn: when,
			ProgressPct: ratioPct(cap0, t.uah),
		})
	}

	// --- 5. Обіграв «просто долари» ---
	//
	// Дати немає ніде: бенчмарк — порівняння НА СЬОГОДНІ, і день, коли
	// портфель уперше обійшов долари, застосунок не записував ніколи.
	add(func() milestone {
		m := milestone{Key: "beat_dollars", Title: "Обіграв «просто долари»",
			ProgressPct: progressNoProgress}
		if bench == nil || bench.BenchmarkUAH == 0 {
			m.Note = "порівнювати ще нема з чим"
			return m
		}
		m.Earned = bench.DiffUAH > 0
		m.Note = fmt.Sprintf("%s проти %s, якби просто тримав долари",
			uah(bench.PortfolioUAH), uah(bench.BenchmarkUAH))
		return m
	}())

	// --- 6. Чотири види в портфелі ---
	kinds := kindsHeld(doc)
	add(milestone{
		Key: "four_kinds", Title: "Чотири види в портфелі",
		Note: fmt.Sprintf("%d із 4: %s", len(kinds), strings.Join(kinds, ", ")),
		Left: noteOr(len(kinds) >= 4, "", fmt.Sprintf("лишилось %d %s", 4-len(kinds),
			plural(4-len(kinds), "вид", "види", "видів"))),
		Earned:      len(kinds) >= 4,
		ProgressPct: ratioPct(float64(len(kinds)), 4),
	})

	// --- 7-8. Серія ---
	add(streakMilestone("half_year_streak", "Пів року поспіль за планом", 6, streak))
	add(streakMilestone("year_no_gaps", "Рік без пропусків", 12, streak))

	// --- 9. Резерв зібрано ---
	add(func() milestone {
		m := milestone{Key: "reserve_full", Title: "Резерв зібрано",
			ProgressPct: progressNoProgress, Note: "ціль резерву не задана"}
		r := doc.Reserve
		if r == nil || r.TargetMonths <= 0 {
			return m
		}
		m.Earned = r.Months >= r.TargetMonths
		m.ProgressPct = ratioPct(r.Months, r.TargetMonths)
		m.Note = fmt.Sprintf("%s з %s місяців витрат",
			num1(r.Months), num1(r.TargetMonths))
		if !m.Earned {
			m.Left = "лишилось " + num1(r.TargetMonths-r.Months) + " місяця витрат"
		}
		return m
	}())

	// --- 10. Драбина резерву повна ---
	add(func() milestone {
		m := milestone{Key: "ladder_full", Title: "Драбина резерву повна",
			ProgressPct: progressNoProgress, Note: "драбина резерву не налаштована"}
		r := doc.Reserve
		if r == nil || r.LadderRungsTarget <= 0 {
			return m
		}
		m.Earned = r.LadderRungs >= r.LadderRungsTarget
		m.ProgressPct = ratioPct(float64(r.LadderRungs), float64(r.LadderRungsTarget))
		m.Note = fmt.Sprintf("%d %s з %d", r.LadderRungs,
			plural(r.LadderRungs, "сходинка", "сходинки", "сходинок"), r.LadderRungsTarget)
		if n := r.LadderRungsTarget - r.LadderRungs; n > 0 {
			m.Left = fmt.Sprintf("лишилось %d %s", n,
				plural(n, "сходинка", "сходинки", "сходинок"))
		}
		return m
	}())

	// --- 11. Частки зійшлись ---
	//
	// ЗНАМЕННИК — УСІ виміри ребалансу, а не «скільки їх видно на екрані».
	// У прототипі редизайну тут стояло «3 з 6», вигадане цілком: вимірів
	// сім, і жоден не на цілі.
	add(func() milestone {
		m := milestone{Key: "shares_aligned", Title: "Частки зійшлись",
			ProgressPct: progressNoProgress, Note: "цілей часток не задано"}
		total := len(doc.Rebalance)
		if total == 0 {
			return m
		}
		at := 0
		for _, r := range doc.Rebalance {
			if atTarget(r) {
				at++
			}
		}
		m.Earned = at == total
		m.ProgressPct = ratioPct(float64(at), float64(total))
		m.Note = fmt.Sprintf("%d %s із %d на цілі", at,
			plural(at, "вимір", "виміри", "вимірів"), total)
		if n := total - at; n > 0 {
			m.Left = fmt.Sprintf("лишилось звести %d %s", n,
				plural(n, "вимір", "виміри", "вимірів"))
		}
		return m
	}())

	// --- 12. Валюта на цілі ---
	add(func() milestone {
		m := milestone{Key: "currency_aligned", Title: "Валюта на цілі",
			ProgressPct: progressNoProgress, Note: "валютної цілі не задано"}
		var total, at int
		for _, r := range doc.Rebalance {
			if r.Dimension != "currency" {
				continue
			}
			total++
			if atTarget(r) {
				at++
			}
		}
		if total == 0 {
			return m
		}
		m.Earned = at == total
		m.ProgressPct = ratioPct(float64(at), float64(total))
		m.Note = fmt.Sprintf("%d з %d валютних вимірів на цілі", at, total)
		if n := total - at; n > 0 {
			m.Left = fmt.Sprintf("лишилось звести %d %s", n,
				plural(n, "валютний вимір", "валютні виміри", "валютних вимірів"))
		}
		return m
	}())

	// --- 13. Жодного перевищеного ліміту ---
	//
	// Саме concentration, а не rebalance: ліміт — це СТЕЛЯ частки (ISIN,
	// брокер, рік погашення), і перевищення в документі зветься over_uah.
	// У rebalance ліміту немає взагалі — там ціль і відхилення від неї, а
	// «нижче цілі» й «понад ліміт» це різні новини.
	add(func() milestone {
		over := 0
		for _, c := range doc.Concentration {
			if c.OverUAH > 0 {
				over++
			}
		}
		m := milestone{Key: "no_limit_breach", Title: "Жодного перевищеного ліміту",
			Earned: over == 0, ProgressPct: pctOf(over == 0)}
		if over == 0 {
			m.Note = "усі частки в межах"
		} else {
			m.Note = fmt.Sprintf("зараз перевищено %d", over)
			m.Left = fmt.Sprintf("перевищено %d — звести до межі", over)
		}
		return m
	}())

	// --- 14. Дохід покриває чверть життя ---
	add(func() milestone {
		m := milestone{Key: "income_quarter", Title: "Дохід покриває чверть життя",
			ProgressPct: progressNoProgress,
			Note:        "місячні витрати не задані — міряти нема від чого"}
		var expenses float64
		if doc.Settings != nil && doc.Settings.MonthlyExpensesUAH != nil {
			expenses = *doc.Settings.MonthlyExpensesUAH
		}
		if expenses <= 0 {
			return m
		}
		need := expenses / 4
		m.Earned = doc.IncomeMonthlyNow >= need
		m.ProgressPct = ratioPct(doc.IncomeMonthlyNow, need)
		m.Note = fmt.Sprintf("%s на місяць із %s — це чверть витрат",
			uah(doc.IncomeMonthlyNow), uah(need))
		if !m.Earned {
			m.Left = "лишилось " + uah(need-doc.IncomeMonthlyNow) + " доходу на місяць"
		}
		return m
	}())

	return out
}

// pickNext — найближча ВИМІРНА незібрана віха.
//
// «Вимірна» — це і є фільтр «реальна». У віхи з progressNoProgress
// відстані немає взагалі («порівнювати ще нема з чим», «ціль резерву не
// задана»), і назвати таку найближчою означало б пообіцяти, що до неї
// недалеко, не знаючи цього.
//
// Рівність відсотків розв'язується ПОРЯДКОМ ОГОЛОШЕННЯ. Порядок у
// buildMilestones тематичний і сталий, тож вибір відтворюваний: дві віхи
// на однаковому відсотку не міняються місцями від перезавантаження.
//
// ЦІНА НАЗВАНА. Двійкова віха («Жодного перевищеного ліміту») має лише 0
// або 100, тож спливе сюди тільки тоді, коли решта незібраних стоїть на
// нулі. Це не хиба ранжування: перевищений ліміт уже стоїть у черзі
// рішень окремим рядком, і показати його ще й тут як «майже зібрано»
// було б гірше, ніж не показати зовсім.
func pickNext(ms []milestone) string {
	key, best := "", progressNoProgress
	for _, m := range ms {
		if m.Earned || m.ProgressPct < 0 {
			continue
		}
		if m.ProgressPct > best {
			key, best = m.Key, m.ProgressPct
		}
	}
	return key
}

// streakMilestone — віха «N місяців поспіль». Береться з НАЙКРАЩОЇ серії,
// а не з поточної: одного разу досягнуте не відбирається назад. Це не
// поблажка — віха фіксує, що таке вже було, а поточний стан показує сама
// доріжка поруч.
func streakMilestone(key, title string, need int, st streakDoc) milestone {
	m := milestone{Key: key, Title: title,
		Earned:      st.Best >= need,
		ProgressPct: ratioPct(float64(st.Best), float64(need)),
		Note: fmt.Sprintf("найдовша серія — %d %s з %d потрібних",
			st.Best, plural(st.Best, "місяць", "місяці", "місяців"), need),
	}
	if n := need - st.Best; n > 0 {
		m.Left = fmt.Sprintf("лишилось %d %s поспіль", n,
			plural(n, "місяць", "місяці", "місяців"))
	}
	if st.MonthsMeasured == 0 {
		m.ProgressPct = progressNoProgress
		m.Note = "місяців, які можна судити, ще немає"
		// Міряти нічим — отже, й відстані немає: те саме правило, що
		// записане при milestone.Left.
		m.Left = ""
	}
	return m
}

// atTarget — вимір стоїть на цілі. Півпункту допуску, бо частка —
// величина неперервна, і вимагати точного збігу означало б віху, яку не
// можна зібрати ніколи.
func atTarget(r state.RebalanceRow) bool {
	return r.TargetPct > 0 && math.Abs(r.CurrentPct-r.TargetPct) <= 0.5
}

// capitalNow — той самий капітал, що й у документі. Готове число, коли
// воно є; сума частин — лише запасний шлях для старішого документа, і
// саме в такому порядку, як у format.js на фронтенді.
func capitalNow(doc *state.Doc) float64 {
	if doc.CapitalUAH > 0 {
		return doc.CapitalUAH
	}
	return doc.NominalUAHEq + doc.AccountUAH + doc.FundsUAH +
		doc.DepositsUAH + doc.ReserveUAH + doc.GoalsUAH + doc.NPFUAH
}

// firstSnapshotAtLeast — перший день, коли капітал у знімках дійшов
// порога. Порожньо означає «у знімках такого дня немає»: або поріг ще не
// пройдено, або його пройшли до того, як знімки почали вестись.
func firstSnapshotAtLeast(snaps []store.Snapshot, uah float64) string {
	sorted := append([]store.Snapshot(nil), snaps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Date < sorted[j].Date })
	for _, sn := range sorted {
		// Знімок тримає МІНОРНІ одиниці (копійки) — те саме ділення на
		// сто, що робить handleSnapshots для всіх колонок одразу. Склад
		// суми — одне означення на всіх читачів знімка (state_delta.go).
		if float64(snapshotCapitalUAH(sn))/100 >= uah {
			return string(sn.Date)
		}
	}
	return ""
}

// kindsHeld — які види справді є в портфелі. Резерв не рахується: він
// нічого не заробляє, і «чотири види» про інструменти.
func kindsHeld(doc *state.Doc) []string {
	var out []string
	if doc.NominalUAHEq > 0 {
		out = append(out, "ОВДП")
	}
	if doc.FundsUAH > 0 {
		out = append(out, "фонди")
	}
	if doc.DepositsUAH > 0 {
		out = append(out, "вклади")
	}
	if doc.NPFUAH > 0 {
		out = append(out, "НПФ")
	}
	return out
}

func noteOr(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

func pctOf(done bool) int {
	if done {
		return 100
	}
	return 0
}

// ratioPct — скільки пройдено, зрізане стелею в сто відсотків: віха, яку
// перевиконали, лишається зібраною, а не «на 340%».
func ratioPct(have, need float64) int {
	if need <= 0 {
		return progressNoProgress
	}
	p := int(math.Round(have / need * 100))
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// num1 — число з одним знаком, українською комою. Своя, бо uah() поруч
// додає гривню, а тут ідеться про місяці.
func num1(v float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", v), ".", ",", 1)
}
