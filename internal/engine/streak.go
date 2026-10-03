// Серія внесків: місяці поспіль, у яких внесено не менше цілі місяця.
//
// Жила у state_progress.go разом із віхами «Шляху». Вкладку прибрано
// (ревізія 2026-10-03), а серію читає «Рік у цифрах» (/api/year) —
// смужку місяців «виконав / ні», тож вона переїхала сюди й лишилась
// єдиним споживачем того, що колись було гейміфікацією.

package engine

import (
	"maps"
	"slices"
	"sort"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

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
	// Числа тут не нові: want і got усередині BuildStreak рахувались і
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
	Month string `json:"month" money:"asof"`
	Known bool   `json:"known"`
	Hit   bool   `json:"hit"`

	// TargetUAH є лише у відомого місяця, ContribUAH — завжди: внесок
	// береться з подій руху грошей і від знімків не залежить зовсім.
	TargetUAH  state.Money `json:"target_uah,omitzero"`
	ContribUAH state.Money `json:"contrib_uah,omitzero"`
}

// BuildStreak — місяці поспіль, у яких внесено не менше цілі того місяця.
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
func BuildStreak(snaps []store.Snapshot, ev []FlowEvent, today domain.Date) streakDoc {
	// Внесено по місяцях. Свої гроші — гаманець (contribution) І подушка
	// з цілями (outside): те саме означення, що в плитки «Цей місяць».
	// Купон і погашення теж збільшують рахунок, але вони не є ТВОЇМ
	// внеском, а серія саме про нього. Доти подушка не рахувалась, і
	// місяць, у якому $800 пішли в матрац повз гаманець, стояв «повз».
	got := map[string]int64{}
	for _, e := range ev {
		if e.Kind == FlowContribution || e.Kind == FlowOutside {
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

	months := slices.Sorted(maps.Keys(want))

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
	for m := months[0]; m != "" && m < nowMonth; m = domain.ShiftMonth(m, 1) {
		mk := streakMark{Month: m, Known: want[m] > 0, ContribUAH: state.Minor(got[m], money.UAH)}
		if mk.Known {
			mk.TargetUAH = state.Minor(want[m], money.UAH)
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
		if prev != "" && domain.ShiftMonth(prev, 1) != m {
			streak = 0
		}
		prev = m
		if got[m] >= want[m] {
			streak++
			best = max(best, streak)
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
