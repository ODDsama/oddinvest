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
		p, err := r.nbu.Inflation(ctx, m)
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
		time.Sleep(r.pause)
	}
	if got > 0 {
		r.log.Info("ІСЦ оновлено", "місяців", got, "до", last)
	}
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
		p, err := r.nbu.Inflation(ctx, m)
		if err != nil {
			missing++
			r.log.Debug("backfill ІСЦ: місяць пропущено", "month", m, "err", err)
			time.Sleep(r.pause)
			continue
		}
		if err := r.st.SaveCPI(ctx, store.CPIPoint{Period: p.Period, MoMBP: p.MoMBP, YoYBP: p.YoYBP}); err != nil {
			return err
		}
		got++
		time.Sleep(r.pause)
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
