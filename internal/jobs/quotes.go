// Ринкові ціни ОВДП — обхід джерела НА ВИМОГУ.
//
// ЧОМУ ТУТ НЕМАЄ ДЖОБИ, І ЦЕ НЕ ПРОПУСК. Усе інше в цьому пакеті ходить
// назовні за розкладом: довідник, курси, аукціони, ІСЦ. Ціни — ні, і
// довід не в лінощах. Ціна важить рівно тоді, коли ти збираєшся купувати;
// решту часу вона змінюється на копійки, яких ніхто не дивиться. Шістдесят
// сторінок чужого сайту щодня заради числа, яке читають раз на тиждень, —
// зловживання того самого класу, від якого відмовляється сам бекфіл
// курсів. Тому Fleet.RunDaily про ціни не знає, і кличе цей прохід лише
// кнопка.
//
// НАСЛІДОК, ЯКИЙ ТРЕБА ЗНАТИ: ціни в базі рівно такої свіжості, якої
// заслужила людина з кнопкою. Тому вік котировки видно на екрані завжди, а
// протухла (unit_cost.go) чесно відкочується на номінал плюс НКД.

package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/finomo"
	"github.com/ODDsama/oddinvest/internal/store"
)

// quotesPauseFactor — у скільки разів витримка між сторінками джерела
// довша за звичайну (250 мс → 500 мс).
//
// ЧИСЛО — ОБЕРЕЖНА ЗДОГАДКА, А НЕ ВИМІР, і це важливо записати. Про НБУ
// ми знаємо напевно, що він тротлить (cpiPauseFactor здобутий на
// бойовому); про межі цього джерела не знаємо нічого. Шістдесят
// послідовних сторінок з однієї адреси — саме та картинка, яку ловить
// бот-детектор, і ціна помилки тут не «повільніше», а «нас відрізали».
// Правити це число можна за спостереженням, не за смаком. Спостереження
// поки одне: 51 сторінка на темпі 1 с пройшла без жодної помилки. Відколи
// обхід покриває ВЕСЬ довідник (двісті сторінок), темп подвоєно — інакше
// кнопка мовчала б чотири хвилини. Це саме та зміна, після якої варто
// глянути в журнал: подвоєння темпу на вчетверо більшому обсязі.
//
// Множник, а не константа, з тієї ж причини, що й скрізь: тести з pause=0
// лишаються миттєвими.
const quotesPauseFactor = 2

// quotesCap — скільки паперів максимум за одне натискання.
//
// ДВІСТІ, А НЕ ШІСТДЕСЯТ, і це не «побільше про всяк випадок». Відколи
// папір без свіжої ціни від свого брокера ХОВАЄТЬСЯ з порад
// (handlers_reinvest.go), неспитаний папір і недоступний папір мусять
// розрізнятися — інакше зі списку зникло б півтори сотні паперів не тому,
// що їх не продають, а тому, що обхід до них не дійшов. Тому прохід
// покриває весь довідник, а не його верх.
//
// Стеля лишається як запобіжник від несподівано великого довідника: у
// ньому нині 186 паперів із майбутнім погашенням, і двісті — це «усе, що
// є» з невеликим запасом, а не окреме рішення про обсяг.
const quotesCap = 200

func (r *Runner) quotesPause() time.Duration { return r.pause * quotesPauseFactor }

// RefreshQuotes — ціни названих паперів, одним проходом.
//
// ПЕРЕЛІК ПРИХОДИТЬ АРГУМЕНТОМ, а не збирається тут, і межа проведена
// саме так навмисно: «які папери мене цікавлять» — питання рейтингу й
// портфеля, тобто api; «як сходити назовні, не нарвавшись» — питання
// тротлінгу, тобто jobs. Зібрати перелік тут означало б потягнути в jobs
// побудову документа стану.
func (r *Runner) RefreshQuotes(ctx context.Context, isins []string) (finomo.RunResult, error) {
	var res finomo.RunResult
	if r.finomo == nil {
		return res, errors.New("джерело цін не налаштоване")
	}
	// Довідник потрібен для звірки: погашення й масштаб номіналу. Без нього
	// ціни брати НЕ МОЖНА — саме він робить сторожів можливими.
	bonds, err := r.st.BondsFor(ctx, isins)
	if err != nil {
		return res, err
	}
	seen := map[string]bool{}
	for _, raw := range isins {
		isin := strings.ToUpper(strings.TrimSpace(raw))
		if isin == "" || seen[isin] {
			continue
		}
		seen[isin] = true
		if res.Asked >= quotesCap {
			res.Skipped++
			continue
		}
		res.Asked++
		b, known := bonds[isin]
		if !known {
			// Папера немає в довіднику НБУ — звірити нема з чим, і брати
			// ціну наосліп означало б лишитись без єдиного сторожа формату.
			res.Failed++
			r.log.Warn("ціни ОВДП: паперу немає в довіднику", "isin", isin)
			continue
		}
		snap, err := r.finomo.Quotes(ctx, isin, b.Maturity)
		if err != nil {
			switch {
			case errors.Is(err, finomo.ErrCutOff):
				// Зупиняємо ВЕСЬ прохід, довід — при finomo.ErrCutOff.
				return res, err
			case errors.Is(err, finomo.ErrNoBond):
				res.Missing++
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				return res, err
			default:
				res.Failed++
				r.log.Warn("ціни ОВДП", "isin", isin, "err", err)
			}
			r.sleepQuotes(ctx)
			continue
		}
		n, err := r.storeSnapshot(ctx, b, snap)
		switch {
		case err != nil:
			res.Failed++
			r.log.Warn("ціни ОВДП: запис", "isin", isin, "err", err)
		case n == 0:
			res.NoPrice++
		default:
			res.Stored++
		}
		r.sleepQuotes(ctx)
	}
	// ПОВНИЙ ОБХІД — окремий факт, а не «прохід завершився».
	//
	// Skipped означає, що частина довідника лишилась неспитаною, і ставити
	// знак у такому разі не можна: на ньому тримається право ХОВАТИ
	// папери без ціни, і поставлений передчасно він сховав би те, про що
	// ми просто не встигли спитати. Помилки окремих паперів знак не
	// скасовують — «спитали й не вийшло» це теж «спитали».
	if res.Skipped == 0 && ctx.Err() == nil {
		if err := r.st.SetAppState(ctx, store.QuotesSweptAtKey,
			time.Now().UTC().Format(time.RFC3339)); err != nil {
			r.log.Warn("ціни ОВДП: знак повного обходу", "err", err)
		}
	}
	return res, nil
}

// storeSnapshot — сторожі й запис. Повертає, скільки цін лягло.
func (r *Runner) storeSnapshot(ctx context.Context, b domain.Bond, snap finomo.Snapshot) (int, error) {
	// СТОРОЖ МАСШТАБУ. Єдиний спосіб перетворити 1013.65 на 101365 —
	// помилка масштабу, і вона мовчки зробила б один папір схожим на
	// сотню в рейтингу. Довідник знає номінал напевно, тож звіряємо з ним.
	if snap.NominalMinor != b.Nominal.Amount() {
		return 0, fmt.Errorf("номінал %d проти %d у довіднику — схоже, шкала джерела змінилась",
			snap.NominalMinor, b.Nominal.Amount())
	}
	if len(snap.Quotes) == 0 {
		return 0, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	qs := make([]store.Quote, 0, len(snap.Quotes))
	for _, q := range snap.Quotes {
		if q.Currency != b.Nominal.Currency().Code {
			// Валюта паперу авторитетна в довіднику. Розбіжність означає, що
			// одне з двох джерел говорить не про той інструмент.
			return 0, fmt.Errorf("валюта %s проти %s у довіднику",
				q.Currency, b.Nominal.Currency().Code)
		}
		qs = append(qs, store.Quote{
			ISIN: snap.ISIN, Source: q.Source, Date: q.Date,
			PriceMinor: q.PriceMinor, Currency: q.Currency,
			Origin: store.QuoteOriginFinomo, FetchedAt: now,
		})
	}
	if err := r.st.SaveQuotes(ctx, qs); err != nil {
		return 0, err
	}
	return len(qs), nil
}

// sleepQuotes — витримка, що переривається скасуванням. Стоїть і в гілці
// помилки теж: чужому серверу байдуже, чи сподобалась нам його відповідь.
func (r *Runner) sleepQuotes(ctx context.Context) {
	if r.pause <= 0 {
		return
	}
	t := time.NewTimer(r.quotesPause())
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
