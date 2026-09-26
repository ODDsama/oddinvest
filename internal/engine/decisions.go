// Запис рішення: що радив помічник у той момент, коли ти вирішував.
//
// ДВА МЕХАНІЗМИ, ТРИ ВИДИ РІШЕНЬ. Купівля — «яким рядком стояло обране»;
// рух у подушку й рух у ціль накопичення — «що стояло верхнім, коли гроші
// пішли повз рейтинг». Другий механізм завівся тому, що без нього журнал
// був сліпий саме до найчастішого рішення живого портфеля: маршрут веде в
// подушку й цілі всі надходження року, а купівель за той рік може не бути
// жодної. Механіка різна (TakeDecisionSnapshot проти TakeOutsideSnapshot),
// і в зведенні жоден із трьох видів не зводиться в спільний знаменник —
// аргумент при DecisionsSummary.
//
// ПОРЯДОК ТУТ ВИРІШАЛЬНИЙ. Знімок рейтингу знімається ДО запису операції,
// а сам рядок журналу пишеться ПІСЛЯ. Інакше знімок був би про портфель,
// у якому покупка вже сталася: частки зрушились, ліміт міг спрацювати,
// драбина закрила дірку — і папір, що стояв першим, опинився б п'ятим.
// Тобто журнал систематично брехав би саме про те, заради чого існує.
//
// ЦІНА ЦЬОГО чесно названа: знімок — це повний BuildState плюс збірка
// рейтингу, тобто найдорожчий шлях бекенда, і POST /api/lots через нього
// помітно повільнішає. Прийнятно, бо покупка — рідка дія людини, а не
// щось у циклі; той самий порядок величин уже витрачає /api/lots/check,
// який UI кличе перед кожним записом.
//
// ПОМИЛКИ ТУТ НЕ ВАЛЯТЬ ОПЕРАЦІЮ. Лот — це факт, рішення — примітка до
// нього. Відмовити в записі покупки через те, що не склався рейтинг,
// означало б поставити примітку вище за факт.
package engine

import (
	"cmp"
	"context"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

// DecisionSnapshot — рейтинг помічника в момент рішення, звужений до
// того, що про нього питають.
//
// ok=false означає «рішення не фіксуємо»: купленого не було в рейтингу
// взагалі. Це не помилка й не рідкість — так виглядає купівля паперу,
// який помічник не пропонував (не проходить за умовами, немає в
// довіднику, куплений усупереч пораді). Записати такий рядок із нульовою
// обіцянкою означало б сказати «помічник обіцяв 0%», хоч він не обіцяв
// нічого.
type DecisionSnapshot struct {
	ok         bool
	realPct    float64
	rankPos    int
	topLabel   string
	topRealPct float64
	rankMode   string
}

// TakeDecisionSnapshot — знайти купленe в сьогоднішньому рейтингу.
//
// Кличеться ДО запису операції (див. шапку файла). Порівнюємо за парою
// (kind, label): у помічника label для облігації — це ISIN, для фонду —
// назва, для вкладу — банк, для НПФ — назва рахунку, тобто рівно ті самі
// слова, якими операція називає свою сутність.
func (e *Engine) TakeDecisionSnapshot(ctx context.Context, now time.Time,
	kind, ref string) DecisionSnapshot {
	if kind == "" || ref == "" {
		return DecisionSnapshot{}
	}
	doc, err := e.BuildState(ctx, now)
	if err != nil {
		e.log.Debug("рішення: стан не зібрався", "err", err)
		return DecisionSnapshot{}
	}
	sugg, err := e.ReinvestSuggestions(ctx, now, doc)
	if err != nil {
		e.log.Debug("рішення: рейтинг не зібрався", "err", err)
		return DecisionSnapshot{}
	}
	snap := DecisionSnapshot{rankMode: "plan"}
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

// SaveDecision — дописати рядок журналу, якщо знімок щось знайшов.
//
// Помилка лише логується: див. шапку файла про те, чому примітка не
// може завалити факт.
//
// note — нотатка САМОЇ операції («чому купив»), переписана в журнал у
// момент рішення. Через рік «Ціна рішень» покаже не лише що ти зробив,
// а й що тоді думав; тягти її з операції за op_id не можна — операцію
// правлять і видаляють, а журнал мусить памʼятати той день.
func (e *Engine) SaveDecision(ctx context.Context, snap DecisionSnapshot,
	now time.Time, kind, ref string, amount *money.Money, opID int64, note string) {
	if !snap.ok {
		return
	}
	d := store.Decision{
		MadeOn: domain.NewDate(now), Kind: kind, Ref: ref,
		RealPct: snap.realPct, RankPos: snap.rankPos,
		TopLabel: snap.topLabel, TopRealPct: snap.topRealPct,
		RankMode: snap.rankMode, OpID: opID, Note: note,
	}
	if amount != nil {
		d.Amount, d.Currency = amount.Amount(), amount.Currency().Code
	}
	if _, err := e.st.AddDecision(ctx, d); err != nil {
		e.log.Debug("рішення: рядок не записався", "kind", kind, "ref", ref, "err", err)
	}
}

// Види рядків журналу для грошей, що пішли ПОВЗ РЕЙТИНГ.
//
// Слова поза словником інструментів («bond»/«fund»/«deposit»/«npf»)
// НАВМИСНО: ні подушка, ні ціль накопичення інструментом не є, у рейтингу
// вони не стоять і стояти не можуть. Спільна таблиця тут виправдана не
// схожістю сутностей, а спільним питанням: «що радив помічник у ту саму
// хвилину, коли ти клав ці гроші».
//
// ДВА СЛОВА, А НЕ ОДНЕ, хоч питання спільне. Подушку тримають, щоб НЕ
// витратити, ціль — щоб витратити у названу дату, і зведення мусить уміти
// сказати це нарізно: «на подушку пішло стільки, на речі — стільки». Одне
// слово злило б їх у «не в портфель» і сховало б різницю, заради якої цілі
// й заводились.
const (
	DecisionKindReserve = "reserve"
	DecisionKindGoal    = "goal"
)

// TakeOutsideSnapshot — рейтинг у момент, коли гроші пішли ПОВЗ нього:
// у подушку або в ціль накопичення.
//
// # ЧОМУ ЦИМ ГРОШАМ ПОТРІБЕН СВІЙ ЗНІМОК
//
// TakeDecisionSnapshot шукає КУПЛЕНЕ в рейтингу за парою (kind, label).
// Ні подушка, ні ціль у рейтингу не стоять узагалі: вирізка на них
// береться ДО ранжування (AllocatePlan), бо ціль у кожної своя й
// абсолютна. Тобто звичайний знімок повернув би ok=false, і журнал лишався
// б сліпим саме до тих рішень, які на живому портфелі трапляються
// найчастіше — маршрут веде в подушку й цілі всі надходження року.
//
// # ЩО САМЕ ЗАПИСУЄТЬСЯ
//
// Не «яким рядком стояла подушка» (таким рядком вона не стоїть), а ЩО
// СТОЯЛО ВЕРХНІМ — тобто найкраще, від чого ці гроші відмовились.
// real_pct лишається нулем, і це не «помічник обіцяв 0%», а точне
// твердження: ні матрац, ні шухляда під авто не приносять нічого, і саме
// тому питання «скільки це коштує» взагалі має сенс.
//
// Ціна при цьому НЕ називається втратою. Резерв тримають, щоб не продавати
// папір у поганий місяць, а на авто збирають, щоб його купити; назвати
// різницю «втраченим» означало б оцінити рішення, чого цей застосунок не
// робить ніде. Число стоїть поруч і мовчить.
//
// ОДНА ФУНКЦІЯ НА ДВІ СУТНОСТІ, бо в тілі немає нічого, що відрізняло б
// подушку від цілі: знімок питає рейтинг, а не того, хто його питає.
// Друга копія розійшлася б із першою на першій же правці режиму.
func (e *Engine) TakeOutsideSnapshot(ctx context.Context, now time.Time) DecisionSnapshot {
	doc, err := e.BuildState(ctx, now)
	if err != nil {
		e.log.Debug("рішення: стан не зібрався", "err", err)
		return DecisionSnapshot{}
	}
	sugg, err := e.ReinvestSuggestions(ctx, now, doc)
	if err != nil || len(sugg) == 0 {
		// Порожній рейтинг — не помилка: буває на порожньому портфелі й
		// тоді, коли купити нема чого. Але тоді й альтернативи немає, а
		// рядок журналу без альтернативи не каже нічого.
		e.log.Debug("рішення: рейтинг порожній", "err", err)
		return DecisionSnapshot{}
	}
	snap := DecisionSnapshot{ok: true, rankMode: "plan"}
	if doc.Settings != nil && doc.Settings.ReinvestRank != "" {
		snap.rankMode = doc.Settings.ReinvestRank
	}
	// rankPos лишається нулем: ці гроші в рейтингу не стоять, і будь-яке
	// число тут читалось би як їхнє місце в ньому.
	snap.topLabel, snap.topRealPct = sugg[0].Label, sugg[0].RealPct
	return snap
}

// DecisionsSummary — зведення, яке й є відповіддю розділу.
type DecisionsSummary struct {
	Count int `json:"count"`
	// Followed — скільки разів обране стояло верхнім рядком.
	Followed int `json:"followed"`
	// VsTopPPAvg — середня різниця дохідності з верхнім рядком по тих
	// рішеннях, де верхнім стояло щось інше. Середнє саме по НИХ, а не по
	// всіх: нулі решти розмили б число до непомітного, і «раз відступив
	// на 3 п.п.» виглядало б як «завжди відступаю на 0.2».
	VsTopPPAvg float64 `json:"vs_top_pp_avg,omitempty"`
	// Measured — скільки рішень удалось перевірити фактом;
	// DriftPPAvg — середнє розходження обіцянки з фактом по них.
	Measured   int     `json:"measured"`
	DriftPPAvg float64 `json:"drift_pp_avg,omitempty"`
	// ByMode — те саме в розрізі режимів рейтингу. Заради цього розрізу
	// журнал і заведено: інакше вибір режиму лишається здогадкою.
	ByMode []decisionsModeRow `json:"by_mode,omitempty"`
	// ПОДУШКА РАХУЄТЬСЯ ОКРЕМО ВІД ПОКУПОК, і це головне рішення зведення.
	//
	// Рух у матрац — теж рішення, ухвалене проти того самого рейтингу, і
	// доти журнал його не бачив узагалі: знімок шукав куплене В рейтингу, а
	// подушка в ньому не стоїть. На живому портфелі це робило журнал сліпим
	// саме до найчастішого рішення — маршрут веде в подушку всі надходження
	// року.
	//
	// Але злити їх в один відсоток не можна. «Слідую помічнику» означає
	// «взяв те, що стояло верхнім»; подушка верхнім не стоїть НІКОЛИ й
	// стояти не може, тож кожен її рух тягнув би Followed донизу й
	// перетворив би метрику дисципліни на метрику «як часто я поповнюю
	// резерв». Тому Count вище — це покупки, а подушка має свою пару чисел.
	ReserveCount int `json:"reserve_count,omitempty"`
	// ReserveForgonePctAvg — середня дохідність найкращого доступного в ті
	// хвилини. НЕ «втрачене»: резерв тримають не заради дохідності, а щоб
	// не продавати папір у поганий місяць. Число стоїть поруч і мовчить.
	//
	// Середнє просте, а не зважене сумами: ваги вимагали б курсів, яких у
	// цій чистій функції немає, а тягнути їх сюди заради одного рядка
	// означало б завести в зведення власну конвертацію.
	ReserveForgonePctAvg float64 `json:"reserve_forgone_pct_avg,omitempty"`
	// Цілі накопичення — ТРЕТЯ пара чисел, а не додаток до подушки.
	//
	// Довід проти злиття той самий, що вивів подушку з Count, лише на ярус
	// нижче: обидві — гроші повз рейтинг, але доля в них різна. Подушку
	// тримають, щоб НЕ витратити; ціль — щоб витратити у названу дату.
	// Спільне число сказало б «не в портфель пішло стільки» й не сказало б,
	// скільки з того піде назад у життя, а скільки лишиться лежати.
	GoalCount int `json:"goal_count,omitempty"`
	// GoalForgonePctAvg — те саме, що ReserveForgonePctAvg, і так само НЕ
	// «втрачене»: на авто збирають, щоб його купити, а не щоб заробити.
	GoalForgonePctAvg float64 `json:"goal_forgone_pct_avg,omitempty"`
}

// РЕТРОСПЕКТИВА ПОМІЧНИКА (GET /api/decisions): наскільки обіцянка
// справдилась і наскільки ти їй слідуєш.
//
// ДВА РІЗНІ ПИТАННЯ, і плутати їх не можна.
//
// ПЕРШЕ — «чи слухаюсь». Помічник упорядковує рядки за реальною
// дохідністю, а ти щоразу береш якийсь один. Скільки разів це був верхній
// рядок і скільки відсоткових пунктів коштували решта випадків — на це
// відповідає сам знімок, без жодних припущень. Працює для всіх чотирьох
// видів однаково.
//
// ДРУГЕ — «чи справдилось». Обіцянка на момент купівлі проти того, що
// папір дав за фактом. Тут відповідь є ЛИШЕ ДЛЯ ОБЛІГАЦІЙ, і це не
// недогляд: рішення про папір перетворюється на ОДИН лот, чиї потоки
// відокремлені від решти портфеля, тож XIRR по ньому — про це рішення й
// ні про що більше.
//
// Чому не для решти. Операція фонду не відокремлюється: позиція — сальдо
// журналу з середньозваженою собівартістю, і дохідність по ній міряє всі
// купівлі разом, а не ту одну. Вклад і внесок у НПФ факту й не потребують:
// ставка вкладу договірна, а ЧВОПА приходить із самим внеском — обіцянка
// там і є фактом за побудовою. Приписати їм «дохідність за фактом»
// означало б показати те саме число двічі й назвати це перевіркою.
//
// ЧОМУ ЗВЕДЕННЯ МОВЧИТЬ НА МАЛИХ ЧИСЛАХ. Різниця в кілька десятих
// відсоткового пункта на трьох рішеннях — це шум, а поданий як висновок
// шум гірший за мовчання: за ним міняють режим рейтингу. Тому нижче
// DecisionsMinRows зведення не віддається зовсім, а поріг їде в
// відповіді, щоб UI не вписував його в себе (та сама причина, з якої
// min_days лежить у RealizedRow).

// DecisionsMinRows — скільки рішень має набратись, перш ніж зведення
// почне щось означати.
const DecisionsMinRows = 10

type DecisionRow struct {
	ID       int64     `json:"id"`
	MadeOn   string    `json:"made_on"`
	Kind     string    `json:"kind"`
	Ref      string    `json:"ref"`
	Amount   MoneyJSON `json:"amount"`
	RankMode string    `json:"rank_mode,omitempty"`
	// PromisedPct — реальна дохідність обраного НА МОМЕНТ рішення.
	PromisedPct float64 `json:"promised_pct"`
	// RankPos — яким рядком воно стояло; 1 = верхній. TopLabel і
	// GivenUpPP заповнені лише коли верхнім стояло щось інше.
	RankPos  int    `json:"rank_pos"`
	TopLabel string `json:"top_label,omitempty"`
	// VsTopPP — наскільки дохідність обраного ВИЩА за дохідність
	// верхнього рядка, у п.п. Знак значущий в обидва боки, і назвати це
	// «втраченим» не можна: у режимі «plan» рейтинг зважує дохідність
	// разом із дефіцитом до цілі, тож верхнім цілком законно стоїть менш
	// дохідний рядок, який зрушує портфель до політики. Тоді додатне
	// число означає «взяв дохідніше, ніж радили», а від'ємне — «взяв
	// менш дохідне», і обидва варіанти — нормальні рішення, а не помилки.
	VsTopPP float64 `json:"vs_top_pp,omitempty"`
	// ActualPct — реальна дохідність ЗА ФАКТОМ на сьогодні; DriftPP —
	// наскільки вона розійшлася з обіцянкою. Порожньо, коли факт не
	// міряється окремо (див. шапку файла); Basis каже, чому саме.
	ActualPct float64 `json:"actual_pct,omitempty"`
	DriftPP   float64 `json:"drift_pp,omitempty"`
	Basis     string  `json:"basis,omitempty"`
	// ForgonePct — реальна дохідність найкращого доступного в ту хвилину.
	// ЛИШЕ в рядків подушки, і окремим полем від VsTopPP навмисно: там
	// різниця двох дохідностей, а тут дохідність, якої в обраного немає
	// зовсім. Звести їх в одне число означало б порівняти «взяв менш
	// дохідний папір» із «не купив нічого» — а це різні за природою
	// рішення, і зводити їх у середнє не можна.
	ForgonePct float64 `json:"forgone_pct,omitempty"`
	// Note — нотатка операції в момент рішення («чому купив»), переписана
	// в журнал тоді ж: операцію правлять і видаляють, журнал памʼятає.
	Note string `json:"note,omitempty"`
}

type decisionsModeRow struct {
	Mode       string  `json:"mode"`
	Count      int     `json:"count"`
	Followed   int     `json:"followed"`
	Measured   int     `json:"measured"`
	DriftPPAvg float64 `json:"drift_pp_avg,omitempty"`
}

// handleDecisions — GET /api/decisions.
// DecisionRows — журнал, розкладений у рядки відповіді.
//
// Окремо від обробника, відколи дисципліну питає ще й прогрес: доріжка
// «Дисципліна» на «Огляді» мусить дорівнювати журналу рішень ЗНАК У
// ЗНАК, і єдиний спосіб це гарантувати — рахувати обидва з одних рядків
// однією функцією. Другою реалізацією вони розійшлись би так само тихо,
// як у прототипі, де доріжка казала 75% при 2 з 4 у журналі.
func (e *Engine) DecisionRows(ctx context.Context) ([]DecisionRow, error) {
	list, err := e.st.ListDecisions(ctx)
	if err != nil {
		return nil, err
	}
	lots, sales, bonds, pays, err := e.Portfolio(ctx)
	if err != nil {
		return nil, err
	}
	lotByID := make(map[int64]domain.Lot, len(lots))
	for _, l := range lots {
		lotByID[l.ID] = l
	}
	today := domain.NewDate(time.Now())
	deval := e.Devaluation(ctx)

	rows := make([]DecisionRow, 0, len(list))
	for _, d := range list {
		row := DecisionBase(d)
		if actual, basis, ok := decisionActual(d, lotByID, sales, bonds, pays, today, deval); ok {
			row.ActualPct, row.Basis = actual, basis
			row.DriftPP = domain.Round2(actual - d.RealPct)
		} else {
			row.Basis = basis
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// DecisionBase — та половина рядка, яку видно з самого журналу, без
// портфеля: що обрано, яким рядком воно стояло і на скільки п.п.
// розійшлось із верхнім.
//
// Винесено окремо заради підсумку періоду (api/handlers_period.go): там
// потрібні саме ці поля й НЕ потрібен факт (він про долю паперу, а не про
// місяць). Без спільної функції вираз VsTopPP — а разом із ним і умова,
// коли він узагалі має сенс, — жив би у двох місцях.
func DecisionBase(d store.Decision) DecisionRow {
	row := DecisionRow{
		ID: d.ID, MadeOn: string(d.MadeOn), Kind: d.Kind, Ref: d.Ref,
		Amount:   ToMoneyJSON(money.New(d.Amount, cmp.Or(d.Currency, money.UAH))),
		RankMode: d.RankMode, PromisedPct: d.RealPct, RankPos: d.RankPos,
		Note: d.Note,
	}
	if d.Kind == DecisionKindReserve || d.Kind == DecisionKindGoal {
		// Ні в подушки, ні в цілі немає ні місця в рейтингу, ні власної
		// обіцянки — лише те, від чого ці гроші відмовились. PromisedPct
		// лишається нулем, і це точне твердження: ні матрац, ні шухляда під
		// авто не приносять нічого.
		row.TopLabel, row.ForgonePct = d.TopLabel, domain.Round2(d.TopRealPct)
		return row
	}
	if d.RankPos > 1 && d.TopLabel != "" {
		row.TopLabel = d.TopLabel
		row.VsTopPP = domain.Round2(d.RealPct - d.TopRealPct)
	}
	return row
}

// decisionActual — що рішення дало за фактом.
//
// ok=false разом із причиною: порожній рядок у колонці «за фактом» без
// пояснення читався б як «нуль» або як поламаний розрахунок, а тут це
// свідома межа вимірюваного.
func decisionActual(d store.Decision, lotByID map[int64]domain.Lot,
	sales []domain.Sale, bonds map[string]domain.Bond, pays []domain.Payment,
	today domain.Date, deval float64) (float64, string, bool) {
	if d.Kind != store.BuyBond {
		return 0, "за фактом окремо не міряється", false
	}
	lot, ok := lotByID[d.OpID]
	if !ok {
		// Лота більше немає — його видалили або правка розірвала звʼязок.
		// Рішення лишається в журналі (воно було), але міряти вже нічого.
		return 0, "лота більше немає", false
	}
	b, ok := bonds[lot.ISIN]
	if !ok {
		return 0, "паперу немає в довіднику", false
	}
	cur := b.Nominal.Currency().Code
	// PortfolioFlows на ОДНОМУ лоті — той самий код, що будує потоки для
	// всього портфеля, просто зі списку в один рядок. Власна збірка
	// потоків тут була б другою відповіддю на питання «які гроші рухались
	// по цій покупці», і розійшлася б із першою на першій же правці
	// (термінальна вартість, межі виплат, продажі частинами).
	flows, err := domain.PortfolioFlows(bonds, pays, []domain.Lot{lot}, sales, cur, today)
	if err != nil {
		return 0, "потоки не склались", false
	}
	// ТОЙ САМИЙ ПОРІГ ЗРІЛОСТІ, що і в зведеному XIRR, і з тієї ж причини.
	// Річна ставка на двотижневих грошах — не мале число з великою
	// похибкою, а число, якого немає: папір, куплений за номіналом два
	// тижні тому, дав тут 546% річних, бо термінальна вартість піднялась
	// на копійку й ануалізувалась у піврічний множник. Друга відповідь на
	// «коли ставці вірити» була б рівно та ж помилка, яку xirrMinMoneyDays
	// уже виправив в іншому місці.
	if domain.MoneyWeightedDays(flows, today) < xirrMinMoneyDays {
		return 0, "ще зарано міряти", false
	}
	rate, err := domain.XIRR(flows)
	// Клапан на безглузді корені — той самий, що в total_return: XIRR на
	// коротких і рваних потоках буває збіжним і при цьому нікчемним.
	if err != nil || !domain.XIRRPlausible(rate) {
		return 0, "ще зарано міряти", false
	}
	return domain.Round2(RealYield(rate, cur, deval) * 100), "за фактом виплат", true
}

// SummarizeDecisions — зведення по журналу.
//
// Рахується тут, а не в браузері (CLAUDE.md §5): середнє по підмножині —
// саме той різновид арифметики, який у двох місцях дає два різні числа,
// бо підмножини визначають по-різному.
func SummarizeDecisions(rows []DecisionRow) DecisionsSummary {
	var sum DecisionsSummary
	byMode := map[string]*decisionsModeRow{}
	var order []string
	var vsTop, vsTopN float64
	var drift, driftN float64
	var forgone, goalForgone float64

	for _, r := range rows {
		// Подушка й цілі — свої пари чисел, і в жодну з решти вони не
		// входять: аргумент при ReserveCount. Режими їх теж не стосуються —
		// рух повз рейтинг не залежить від того, чим той упорядкований.
		if r.Kind == DecisionKindReserve {
			sum.ReserveCount++
			forgone += r.ForgonePct
			continue
		}
		if r.Kind == DecisionKindGoal {
			sum.GoalCount++
			goalForgone += r.ForgonePct
			continue
		}
		sum.Count++
		m := byMode[r.RankMode]
		if m == nil {
			m = &decisionsModeRow{Mode: r.RankMode}
			byMode[r.RankMode] = m
			order = append(order, r.RankMode)
		}
		m.Count++
		if r.RankPos == 1 {
			sum.Followed++
			m.Followed++
		} else if r.TopLabel != "" {
			vsTop += r.VsTopPP
			vsTopN++
		}
		if r.Basis == "за фактом виплат" {
			sum.Measured++
			m.Measured++
			drift += r.DriftPP
			driftN++
			m.DriftPPAvg += r.DriftPP
		}
	}
	if vsTopN > 0 {
		sum.VsTopPPAvg = domain.Round2(vsTop / vsTopN)
	}
	if driftN > 0 {
		sum.DriftPPAvg = domain.Round2(drift / driftN)
	}
	if sum.ReserveCount > 0 {
		sum.ReserveForgonePctAvg = domain.Round2(forgone / float64(sum.ReserveCount))
	}
	if sum.GoalCount > 0 {
		sum.GoalForgonePctAvg = domain.Round2(goalForgone / float64(sum.GoalCount))
	}
	// Порядок режимів — той, у якому вони вперше трапились у журналі,
	// тобто хронологічний. Мапа дала б новий порядок на кожен запит.
	for _, name := range order {
		m := byMode[name]
		if m.Measured > 0 {
			m.DriftPPAvg = domain.Round2(m.DriftPPAvg / float64(m.Measured))
		}
		sum.ByMode = append(sum.ByMode, *m)
	}
	return sum
}
