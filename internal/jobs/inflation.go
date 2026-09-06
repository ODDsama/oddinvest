package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
	"github.com/ODDsama/oddinvest/internal/store"
)

// cpiWatermark — до якого місяця ІСЦ уже переглянуто. Робочий стан джоби,
// а не політика портфеля, тому в реєстрі налаштувань його немає — так
// само, як nbu_refreshed_at і знак аукціонів поруч.
const cpiWatermark = "cpi_polled_through"

// cpiCatchupCap — скільки місяців максимум догоняти за один прогін. Довід
// той самий, що при auctionCatchupCap: RefreshAll живе під
// п'ятихвилинним контекстом, а сервіс міг стояти вимкненим півроку.
const cpiCatchupCap = 24

// cpiGapCap — скільки дірок латати за один добовий прогін. Латання — це
// запити до чужого сервісу під тим самим пʼятихвилинним контекстом, тож
// ряд у 30 дірок закриється за тиждень, а не однією довгою серією.
const cpiGapCap = 12

// cpiPauseFactor — у скільки разів пауза між запитами ІСЦ довша за
// звичайну.
//
// НБУ ЦЕЙ ЕНДПОЙНТ ТРОТЛИТЬ, і виявилось це лише на бойовому: з 133
// запитів по 250 мс пройшло 88, решта дістала HTTP 503 суцільними
// блоками. Кожна відповідь тут ~190 КБ (780 рядків: 25 регіонів × 12
// розділів COICOP), тобто ендпойнт важкий, і темп курсів для нього
// завеликий. Перевірено з бойового: на 2 с проходить.
//
// Множник, а не власна константа, щоб тести (pause = 0) лишались миттєвими.
const cpiPauseFactor = 8

func (r *Runner) cpiPause() time.Duration { return r.pause * cpiPauseFactor }

// RefreshCPI — місяці ІСЦ, яких ще немає.
//
// Усталений режим — нуль або один запит на добу: ІСЦ виходить раз на
// місяць, тож більшість прогонів упирається в «наступного місяця ще
// немає» з першого ж запиту.
//
// НЕОПУБЛІКОВАНИЙ МІСЯЦЬ — НЕ ПОМИЛКА І НЕ ПРИВІД РУХАТИ ЗНАК. Держстат
// публікує близько 8-10 числа, тож перший тиждень кожного місяця ця
// джоба законно нічого не приносить; знак лишається на місці, і завтра
// вона спробує той самий місяць знову.
func (r *Runner) RefreshCPI(ctx context.Context) error {
	last := prevMonth(monthOf(time.Now().In(r.loc)))
	from, err := r.cpiCursor(ctx, last)
	if err != nil {
		return err
	}
	got := 0
	for m := from; m <= last && got < cpiCatchupCap; m = nextMonth(m) {
		p, err := r.fetchCPI(ctx, m)
		if errors.Is(err, nbu.ErrCPINotPublished) {
			break
		}
		if err != nil {
			return err
		}
		if err := r.st.SaveCPI(ctx, store.CPIPoint{Period: p.Period, MoMBP: p.MoMBP, YoYBP: p.YoYBP}); err != nil {
			return err
		}
		if err := r.st.SetAppState(ctx, cpiWatermark, m); err != nil {
			return err
		}
		got++
		time.Sleep(r.cpiPause())
	}
	if got > 0 {
		r.log.Info("ІСЦ оновлено", "місяців", got, "до", last)
	}
	// Латання дірок — ПІСЛЯ догону й у тому самому прогоні. Без нього ряд
	// із провалами лишався б таким назавжди: водяний знак стоїть на
	// останньому місяці, а до старих ніхто вже не повернеться. Ціна дірки
	// не абстрактна — на бойовому 32 пропущені місяці занизили інфляцію
	// на 2.8 в.п., і мовчки.
	return r.fillCPIGaps(ctx)
}

// fetchCPI — запит із ОДНІЄЮ повторною спробою.
//
// 503 від НБУ тут не виняткова подія, а робочий режим тротлінгу: перший
// запит проходить, наступні три — ні. Одна повторна спроба з подвійною
// паузою перетворює це з дірки в ряду на затримку в секунду.
// ErrCPINotPublished не повторюється: він означає відповідь, а не збій.
func (r *Runner) fetchCPI(ctx context.Context, month string) (nbu.CPIPoint, error) {
	p, err := r.nbu.Inflation(ctx, month)
	if err == nil || errors.Is(err, nbu.ErrCPINotPublished) {
		return p, err
	}
	r.log.Debug("ІСЦ: повтор після невдачі", "month", month, "err", err)
	select {
	case <-ctx.Done():
		return nbu.CPIPoint{}, ctx.Err()
	case <-time.After(2 * r.cpiPause()):
	}
	return r.nbu.Inflation(ctx, month)
}

// fillCPIGaps — місяці, яких у ряду бракує між крайніми точками.
//
// Окремо від бекфілу, бо це інша ситуація: бекфіл наповнює порожнє, а це
// латає вже наявне. Стеля на прогін є, і саме тому дірки закриваються за
// кілька діб, а не однією довгою серією запитів під пʼятихвилинним
// контекстом.
func (r *Runner) fillCPIGaps(ctx context.Context) error {
	pts, err := r.st.CPISince(ctx, "")
	if err != nil {
		return err
	}
	dom := make([]domain.CPIPoint, 0, len(pts))
	for _, p := range pts {
		dom = append(dom, domain.CPIPoint{Period: p.Period, MoMBP: p.MoMBP, YoYBP: p.YoYBP})
	}
	gaps := domain.CPIGaps(dom)
	if len(gaps) == 0 {
		return nil
	}
	var got int
	for i, m := range gaps {
		if i >= cpiGapCap {
			break
		}
		p, err := r.fetchCPI(ctx, m)
		if err != nil {
			r.log.Debug("ІСЦ: дірка не залаталась", "month", m, "err", err)
			time.Sleep(r.cpiPause())
			continue
		}
		if err := r.st.SaveCPI(ctx, store.CPIPoint{Period: p.Period, MoMBP: p.MoMBP, YoYBP: p.YoYBP}); err != nil {
			return err
		}
		got++
		time.Sleep(r.cpiPause())
	}
	r.log.Info("ІСЦ: дірки", "було", len(gaps), "залатано", got)
	return nil
}

// cpiCursor — з якого місяця питати.
//
// Знак головний, ряд запасний: знак каже, що ми ВЖЕ ДИВИЛИСЬ, а ряд —
// лише що ми щось маємо. Різниця важить після відновлення з бекапу, де
// ІСЦ немає взагалі (він похідний), — тоді знак лишається, і джоба не
// починає все спочатку. Немає ні того, ні того — беремо останній
// завершений місяць: історію добере бекфіл, а не добова джоба.
func (r *Runner) cpiCursor(ctx context.Context, last string) (string, error) {
	mark, err := r.st.GetAppState(ctx, cpiWatermark)
	if err != nil {
		return "", err
	}
	if mark != "" {
		return nextMonth(mark), nil
	}
	newest, err := r.st.NewestCPI(ctx)
	if err != nil {
		return "", err
	}
	if newest.Period != "" {
		return nextMonth(newest.Period), nil
	}
	return last, nil
}

// BackfillCPI — історія ІСЦ за years років назад.
//
// ПРОПУЩЕНИЙ МІСЯЦЬ ТУТ ЛИШЕ РАХУЄТЬСЯ. Ряд НБУ починається приблизно з
// 2008 року, тож при years=10+ перші запити законно повертають порожньо,
// і зупинятись на них означало б не набрати нічого. Це протилежність
// правилу RefreshCPI, і протилежність навмисна: там порожній місяць — це
// «ще не вийшов», тут — «його й не було».
func (r *Runner) BackfillCPI(ctx context.Context, years int) error {
	last := prevMonth(monthOf(time.Now().In(r.loc)))
	m := last
	for i := 0; i < years*12; i++ {
		m = prevMonth(m)
	}
	var got, missing int
	for ; m <= last; m = nextMonth(m) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		p, err := r.fetchCPI(ctx, m)
		if err != nil {
			missing++
			r.log.Debug("backfill ІСЦ: місяць пропущено", "month", m, "err", err)
			time.Sleep(r.cpiPause())
			continue
		}
		if err := r.st.SaveCPI(ctx, store.CPIPoint{Period: p.Period, MoMBP: p.MoMBP, YoYBP: p.YoYBP}); err != nil {
			return err
		}
		got++
		time.Sleep(r.cpiPause())
	}
	r.log.Info("історію ІСЦ підтягнуто", "точок", got, "пропущено", missing)
	return nil
}

// BackfillCPIIfThin — те саме, що BackfillIfThin для курсів: тягнемо лише
// тоді, коли історії справді мало. Викликається при старті у власній
// горутині, бо мережа може лежати.
func (r *Runner) BackfillCPIIfThin(ctx context.Context, years, minMonths int) {
	have, err := r.st.CPIMonthCount(ctx)
	if err != nil {
		r.log.Warn("backfill ІСЦ: не вдалось порахувати історію", "err", err)
		return
	}
	if have >= minMonths {
		return
	}
	r.log.Info("історії ІСЦ замало — тягнемо з НБУ", "місяців", have, "треба", minMonths)
	if err := r.BackfillCPI(ctx, years); err != nil {
		r.log.Warn("backfill ІСЦ не завершився", "err", err)
	}
}

func monthOf(t time.Time) string { return t.Format("2006-01") }

func nextMonth(m string) string { return shiftMonth(m, 1) }
func prevMonth(m string) string { return shiftMonth(m, -1) }

func shiftMonth(m string, by int) string {
	d, err := domain.ParseDate(m + "-01")
	if err != nil {
		return m
	}
	return string(d.AddMonths(by))[:7]
}
