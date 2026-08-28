// Ретроспектива помічника: наскільки обіцянка справдилась і наскільки ти
// їй слідуєш.
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
// decisionsMinRows зведення не віддається зовсім, а поріг їде в
// відповіді, щоб UI не вписував його в себе (та сама причина, з якої
// min_days лежить у RealizedRow).
package api

import (
	"context"
	"net/http"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// decisionsMinRows — скільки рішень має набратись, перш ніж зведення
// почне щось означати.
const decisionsMinRows = 10

type decisionRow struct {
	ID       int64     `json:"id"`
	MadeOn   string    `json:"made_on"`
	Kind     string    `json:"kind"`
	Ref      string    `json:"ref"`
	Amount   moneyJSON `json:"amount"`
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
}

// decisionsSummary — зведення, яке й є відповіддю розділу.
type decisionsSummary struct {
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
}

type decisionsModeRow struct {
	Mode       string  `json:"mode"`
	Count      int     `json:"count"`
	Followed   int     `json:"followed"`
	Measured   int     `json:"measured"`
	DriftPPAvg float64 `json:"drift_pp_avg,omitempty"`
}

// handleDecisions — GET /api/decisions.
// decisionRows — журнал, розкладений у рядки відповіді.
//
// Окремо від обробника, відколи дисципліну питає ще й прогрес: доріжка
// «Дисципліна» на «Огляді» мусить дорівнювати журналу рішень ЗНАК У
// ЗНАК, і єдиний спосіб це гарантувати — рахувати обидва з одних рядків
// однією функцією. Другою реалізацією вони розійшлись би так само тихо,
// як у прототипі, де доріжка казала 75% при 2 з 4 у журналі.
func (s *Server) decisionRows(ctx context.Context) ([]decisionRow, error) {
	list, err := s.st.ListDecisions(ctx)
	if err != nil {
		return nil, err
	}
	lots, sales, bonds, pays, err := s.portfolio(ctx)
	if err != nil {
		return nil, err
	}
	lotByID := make(map[int64]domain.Lot, len(lots))
	for _, l := range lots {
		lotByID[l.ID] = l
	}
	today := domain.NewDate(time.Now())
	deval := s.devaluation(ctx)

	rows := make([]decisionRow, 0, len(list))
	for _, d := range list {
		row := decisionBase(d)
		if actual, basis, ok := decisionActual(d, lotByID, sales, bonds, pays, today, deval); ok {
			row.ActualPct, row.Basis = actual, basis
			row.DriftPP = round2(actual - d.RealPct)
		} else {
			row.Basis = basis
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *Server) handleDecisions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.decisionRows(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	out := struct {
		Rows    []decisionRow     `json:"rows"`
		Summary *decisionsSummary `json:"summary,omitempty"`
		MinRows int               `json:"min_rows"`
	}{Rows: rows, MinRows: decisionsMinRows}
	if len(rows) >= decisionsMinRows {
		sum := summarizeDecisions(rows)
		out.Summary = &sum
	}
	writeJSON(w, http.StatusOK, out)
}

// decisionBase — та половина рядка, яку видно з самого журналу, без
// портфеля: що обрано, яким рядком воно стояло і на скільки п.п.
// розійшлось із верхнім.
//
// Винесено окремо заради підсумку періоду (handlers_period.go): там
// потрібні саме ці поля й НЕ потрібен факт (він про долю паперу, а не про
// місяць). Без спільної функції вираз VsTopPP — а разом із ним і умова,
// коли він узагалі має сенс, — жив би у двох місцях.
func decisionBase(d store.Decision) decisionRow {
	row := decisionRow{
		ID: d.ID, MadeOn: string(d.MadeOn), Kind: d.Kind, Ref: d.Ref,
		Amount:   toMoneyJSON(money.New(d.Amount, orUAH(d.Currency))),
		RankMode: d.RankMode, PromisedPct: d.RealPct, RankPos: d.RankPos,
	}
	if d.Kind == decisionKindReserve {
		// У подушки немає ні місця в рейтингу, ні власної обіцянки — лише
		// те, від чого ці гроші відмовились. PromisedPct лишається нулем, і
		// це точне твердження: журнал матраца не приносить нічого.
		row.TopLabel, row.ForgonePct = d.TopLabel, round2(d.TopRealPct)
		return row
	}
	if d.RankPos > 1 && d.TopLabel != "" {
		row.TopLabel = d.TopLabel
		row.VsTopPP = round2(d.RealPct - d.TopRealPct)
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
	if err != nil || rate > 1.0 || rate < -0.95 {
		return 0, "ще зарано міряти", false
	}
	return round2(realYield(rate, cur, deval) * 100), "за фактом виплат", true
}

// summarizeDecisions — зведення по журналу.
//
// Рахується тут, а не в браузері (CLAUDE.md §5): середнє по підмножині —
// саме той різновид арифметики, який у двох місцях дає два різні числа,
// бо підмножини визначають по-різному.
func summarizeDecisions(rows []decisionRow) decisionsSummary {
	var sum decisionsSummary
	byMode := map[string]*decisionsModeRow{}
	var order []string
	var vsTop, vsTopN float64
	var drift, driftN float64
	var forgone float64

	for _, r := range rows {
		// Подушка — своя пара чисел, і в жодну з решти вона не входить:
		// аргумент при ReserveCount. Режими її теж не стосуються — рух у
		// матрац не залежить від того, чим упорядкований рейтинг.
		if r.Kind == decisionKindReserve {
			sum.ReserveCount++
			forgone += r.ForgonePct
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
		sum.VsTopPPAvg = round2(vsTop / vsTopN)
	}
	if driftN > 0 {
		sum.DriftPPAvg = round2(drift / driftN)
	}
	if sum.ReserveCount > 0 {
		sum.ReserveForgonePctAvg = round2(forgone / float64(sum.ReserveCount))
	}
	// Порядок режимів — той, у якому вони вперше трапились у журналі,
	// тобто хронологічний. Мапа дала б новий порядок на кожен запит.
	for _, name := range order {
		m := byMode[name]
		if m.Measured > 0 {
			m.DriftPPAvg = round2(m.DriftPPAvg / float64(m.Measured))
		}
		sum.ByMode = append(sum.ByMode, *m)
	}
	return sum
}
