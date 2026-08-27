// Запис рішення: що радив помічник у той момент, коли ти вирішував.
//
// ДВА ВИДИ РІШЕНЬ, і другий додано пізніше. Купівля — «яким рядком стояло
// обране»; рух у подушку — «що стояло верхнім, коли гроші пішли повз
// рейтинг». Друге завелось тому, що без нього журнал був сліпий саме до
// найчастішого рішення живого портфеля: маршрут веде в подушку всі
// надходження року, а купівель за той рік може не бути жодної. Механіка в
// них різна (takeDecisionSnapshot проти takeReserveSnapshot), і в зведенні
// вони НЕ зводяться в один знаменник — аргумент при decisionsSummary.
//
// ПОРЯДОК ТУТ ВИРІШАЛЬНИЙ. Знімок рейтингу знімається ДО запису операції,
// а сам рядок журналу пишеться ПІСЛЯ. Інакше знімок був би про портфель,
// у якому покупка вже сталася: частки зрушились, ліміт міг спрацювати,
// драбина закрила дірку — і папір, що стояв першим, опинився б п'ятим.
// Тобто журнал систематично брехав би саме про те, заради чого існує.
//
// ЦІНА ЦЬОГО чесно названа: знімок — це повний buildState плюс збірка
// рейтингу, тобто найдорожчий шлях бекенда, і POST /api/lots через нього
// помітно повільнішає. Прийнятно, бо покупка — рідка дія людини, а не
// щось у циклі; той самий порядок величин уже витрачає /api/lots/check,
// який UI кличе перед кожним записом.
//
// ПОМИЛКИ ТУТ НЕ ВАЛЯТЬ ОПЕРАЦІЮ. Лот — це факт, рішення — примітка до
// нього. Відмовити в записі покупки через те, що не склався рейтинг,
// означало б поставити примітку вище за факт.
package api

import (
	"context"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// decisionSnapshot — рейтинг помічника в момент рішення, звужений до
// того, що про нього питають.
//
// ok=false означає «рішення не фіксуємо»: купленого не було в рейтингу
// взагалі. Це не помилка й не рідкість — так виглядає купівля паперу,
// який помічник не пропонував (не проходить за умовами, немає в
// довіднику, куплений усупереч пораді). Записати такий рядок із нульовою
// обіцянкою означало б сказати «помічник обіцяв 0%», хоч він не обіцяв
// нічого.
type decisionSnapshot struct {
	ok         bool
	realPct    float64
	rankPos    int
	topLabel   string
	topRealPct float64
	rankMode   string
}

// takeDecisionSnapshot — знайти купленe в сьогоднішньому рейтингу.
//
// Кличеться ДО запису операції (див. шапку файла). Порівнюємо за парою
// (kind, label): у помічника label для облігації — це ISIN, для фонду —
// назва, для вкладу — банк, для НПФ — назва рахунку, тобто рівно ті самі
// слова, якими операція називає свою сутність.
func (s *Server) takeDecisionSnapshot(ctx context.Context, now time.Time,
	kind, ref string) decisionSnapshot {
	if kind == "" || ref == "" {
		return decisionSnapshot{}
	}
	doc, err := s.buildState(ctx, now)
	if err != nil {
		s.log.Debug("рішення: стан не зібрався", "err", err)
		return decisionSnapshot{}
	}
	sugg, err := s.reinvestSuggestions(ctx, now, doc)
	if err != nil {
		s.log.Debug("рішення: рейтинг не зібрався", "err", err)
		return decisionSnapshot{}
	}
	snap := decisionSnapshot{rankMode: "plan"}
	if doc.Settings != nil && doc.Settings.ReinvestRank != "" {
		snap.rankMode = doc.Settings.ReinvestRank
	}
	for i, g := range sugg {
		if g.Kind != kind || g.Label != ref {
			continue
		}
		snap.ok, snap.realPct, snap.rankPos = true, g.RealPct, i+1
		// Перший рядок сам себе альтернативою не буває: «віддав перевагу
		// X замість X» — не твердження, а шум.
		if i > 0 {
			snap.topLabel, snap.topRealPct = sugg[0].Label, sugg[0].RealPct
		}
		break
	}
	return snap
}

// saveDecision — дописати рядок журналу, якщо знімок щось знайшов.
//
// Помилка лише логується: див. шапку файла про те, чому примітка не
// може завалити факт.
func (s *Server) saveDecision(ctx context.Context, snap decisionSnapshot,
	now time.Time, kind, ref string, amount *money.Money, opID int64) {
	if !snap.ok {
		return
	}
	d := store.Decision{
		MadeOn: domain.NewDate(now), Kind: kind, Ref: ref,
		RealPct: snap.realPct, RankPos: snap.rankPos,
		TopLabel: snap.topLabel, TopRealPct: snap.topRealPct,
		RankMode: snap.rankMode, OpID: opID,
	}
	if amount != nil {
		d.Amount, d.Currency = amount.Amount(), amount.Currency().Code
	}
	if _, err := s.st.AddDecision(ctx, d); err != nil {
		s.log.Debug("рішення: рядок не записався", "kind", kind, "ref", ref, "err", err)
	}
}

// decisionKindReserve — вид рядка журналу для руху в подушку.
//
// Слово поза словником інструментів («bond»/«fund»/«deposit»/«npf»)
// НАВМИСНО: подушка інструментом не є, ціль у неї абсолютна, у рейтингу
// вона не стоїть і стояти не може. Спільна таблиця тут виправдана не
// схожістю сутностей, а спільним питанням: «що радив помічник у ту саму
// хвилину, коли ти клав ці гроші».
const decisionKindReserve = "reserve"

// takeReserveSnapshot — рейтинг у момент, коли гроші пішли в подушку.
//
// # ЧОМУ ДЛЯ РЕЗЕРВУ ПОТРІБЕН СВІЙ ЗНІМОК
//
// takeDecisionSnapshot шукає КУПЛЕНЕ в рейтингу за парою (kind, label).
// Подушка в рейтингу не стоїть узагалі: вирізка на неї береться ДО
// ранжування (allocatePlan), бо ціль у неї своя й абсолютна. Тобто
// звичайний знімок повернув би ok=false, і журнал лишався б сліпим саме
// до тих рішень, які на живому портфелі трапляються найчастіше — маршрут
// у ньому веде в подушку всі надходження року.
//
// # ЩО САМЕ ЗАПИСУЄТЬСЯ
//
// Не «яким рядком стояла подушка» (таким рядком вона не стоїть), а ЩО
// СТОЯЛО ВЕРХНІМ — тобто найкраще, від чого ці гроші відмовились.
// real_pct лишається нулем, і це не «помічник обіцяв 0%», а точне
// твердження: журнал матраца не приносить нічого, і саме тому питання
// «скільки коштує ця подушка» взагалі має сенс.
//
// Ціна подушки при цьому НЕ називається втратою. Резерв купують не заради
// дохідності, а заради того, щоб не продавати папір у поганий місяць;
// назвати різницю «втраченим» означало б оцінити рішення, чого цей
// застосунок не робить ніде. Число стоїть поруч і мовчить.
func (s *Server) takeReserveSnapshot(ctx context.Context, now time.Time) decisionSnapshot {
	doc, err := s.buildState(ctx, now)
	if err != nil {
		s.log.Debug("рішення: стан не зібрався", "err", err)
		return decisionSnapshot{}
	}
	sugg, err := s.reinvestSuggestions(ctx, now, doc)
	if err != nil || len(sugg) == 0 {
		// Порожній рейтинг — не помилка: буває на порожньому портфелі й
		// тоді, коли купити нема чого. Але тоді й альтернативи немає, а
		// рядок журналу без альтернативи не каже нічого.
		s.log.Debug("рішення: рейтинг для резерву порожній", "err", err)
		return decisionSnapshot{}
	}
	snap := decisionSnapshot{ok: true, rankMode: "plan"}
	if doc.Settings != nil && doc.Settings.ReinvestRank != "" {
		snap.rankMode = doc.Settings.ReinvestRank
	}
	// rankPos лишається нулем: подушка в рейтингу не стоїть, і будь-яке
	// число тут читалось би як її місце в ньому.
	snap.topLabel, snap.topRealPct = sugg[0].Label, sugg[0].RealPct
	return snap
}
