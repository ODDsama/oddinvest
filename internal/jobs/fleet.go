package jobs

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/finomo"
	"github.com/ODDsama/oddinvest/internal/store"
)

// Fleet — усі Runner-и процесу: головний і по одному на кожен інший
// портфель (0054).
//
// Добовий цикл жив у Runner, доки Runner був один. З портфелями робота
// розпалась на дві різні за природою частини. Довідник НБУ, курси й
// аукціони — СПІЛЬНІ таблиці, і тягнути їх раз на портфель означало б
// стільки ж зайвих запитів до чужого сервера, скільки портфелів, з тим
// самим результатом. Знімок, дамп і публікація — навпаки, у КОЖНОГО свої:
// свій рядок snapshots, свій файл бекапу, свій топік MQTT. Флот і є тим
// місцем, де ця межа проведена: перше робить лише головний, друге — всі.
//
// Maintain — один раз на прогін, а не на портфель: integrity_check ходить
// по всій базі, і десять портфелів дали б десять однакових перевірок.
type Fleet struct {
	main *Runner

	mu     sync.RWMutex
	others map[string]*Runner // за slug портфеля
}

func NewFleet(main *Runner) *Fleet {
	return &Fleet{main: main, others: map[string]*Runner{}}
}

// Add — портфель зʼявився (при старті або створений із застосунку).
func (f *Fleet) Add(slug string, r *Runner) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.others[slug] = r
}

// Remove — портфель стерто; його Runner більше не бере участі в прогоні.
func (f *Fleet) Remove(slug string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.others, slug)
}

// runners — головний першим: його знімок і публікація найважливіші, а
// порядок решти ні на що не впливає.
func (f *Fleet) runners() []*Runner {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]*Runner, 0, 1+len(f.others))
	out = append(out, f.main)
	for _, r := range f.others {
		out = append(out, r)
	}
	return out
}

// Satellite — Refresher для сателіта (api.Server іншого портфеля).
//
// Кнопка «Оновити НБУ» на будь-якому портфелі оновлює СПІЛЬНИЙ довідник —
// це справа головного Runner-а. Публікація ж — своя: у сателіта свій топік
// і свій документ. Головний після цього не републікується; його поправить
// найближча мутація або добовий прогін, а довідник у нього і так той самий.
type Satellite struct {
	Main, Own *Runner
}

func (s Satellite) RefreshAll(ctx context.Context) error   { return s.Main.RefreshAll(ctx) }
func (s Satellite) PublishState(ctx context.Context) error { return s.Own.PublishState(ctx) }

// RefreshQuotes — теж справа ГОЛОВНОГО, і з того самого доводу, що
// RefreshAll: ціна паперу в брокера спільна для всіх портфелів (0059), тож
// другий обхід тим самим сайтом заради тих самих чисел був би лише другим
// приводом нас відрізати. Перелік паперів при цьому свій — його зібрав той
// api.Server, у якого натиснули кнопку.
func (s Satellite) RefreshQuotes(ctx context.Context, isins []string) (finomo.RunResult, error) {
	return s.Main.RefreshQuotes(ctx, isins)
}

// dailyRun — добовий прогін: довідник один раз, далі кожному портфелю
// його знімок, дамп і публікацію, наостанок гігієна сховища.
//
// RefreshAll НЕ обриває послідовність: його помилка означає лише «НБУ
// мовчить», а знімки й бекапи рахуються з того, що вже в базі, і чужа
// недоступність не привід їх пропустити (шапка Runner.RefreshAll).
//
// КОЖНА ФАЗА — СВІЙ ТАЙМАУТ. Доти одні пʼять хвилин ділили всі кроки, і
// НБУ, що відповідає по 30 секунд на запит, зʼїдав час знімкам і бекапам
// портфелів, які від нього не залежать узагалі.
//
// Повертає помилку оновлення НБУ: за нею RunDaily вирішує про повтор.
func (f *Fleet) dailyRun(ctx context.Context) error {
	// Без спільної стелі: кожна фаза RefreshAll має свою (phaseTimeout), і
	// спільна лише знову віддала б час повільного довідника решті.
	refreshErr := f.main.RefreshAll(ctx)
	if refreshErr != nil {
		f.main.log.Error("добове оновлення НБУ", "err", refreshErr)
	}
	for _, r := range f.runners() {
		pctx, pcancel := context.WithTimeout(ctx, 2*time.Minute)
		r.persistDaily(pctx)
		if err := r.PublishState(pctx); err != nil {
			r.log.Error("добова публікація", "err", err)
		}
		pcancel()
	}
	mctx, mcancel := context.WithTimeout(ctx, 2*time.Minute)
	defer mcancel()
	// Результат перевірки цілісності — міткою в app_state: інакше про
	// пошкоджену базу знав би лише журнал (задача db-integrity, /healthz).
	// Таймаут чи зайнята база мітку не чіпають — це не відповідь про базу.
	merr := f.main.st.Maintain(mctx)
	switch {
	case merr == nil:
		f.markIntegrity(mctx, "ok")
	case errors.Is(merr, store.ErrIntegrity):
		f.main.log.Error("обслуговування сховища", "err", merr)
		f.markIntegrity(mctx, merr.Error())
	default:
		f.main.log.Error("обслуговування сховища", "err", merr)
	}
	return refreshErr
}

func (f *Fleet) markIntegrity(ctx context.Context, v string) {
	if err := f.main.st.SetAppState(ctx, store.IntegrityKey, v); err != nil {
		f.main.log.Warn("мітка цілісності не записалась", "err", err)
	}
}

// refreshRetries — скільки разів за день повторити прогін, якщо НБУ
// лежав: щогодини до 12:10. Далі — завтра о 06:10, як і доти. Повтор —
// увесь прогін, бо він ідемпотентний (upsert знімка, перезапис дампа).
const refreshRetries = 6

// RunDaily — цикл: щодня о 06:10 Києва добовий прогін.
//
// НАЗДОГАНЯННЯ ПРИ СТАРТІ. Доти цикл просто чекав найближчої 06:10 і при
// старті не робив нічого — тож рестарт о 06:11 (а оновлення відбувається
// саме серед дня) коштував дню знімка й бекапу разом. Помітно це не
// одразу: діра в кривій за один день читається як «того дня нічого не
// змінилось».
//
// Коштує наздоганяння нічого, бо крок ідемпотентний: (portfolio_id, date)
// у snapshots — PRIMARY KEY, а SaveSnapshot робить upsert, тож повторний
// прогін того самого дня перезаписує рядок тими самими числами.
//
// ПОВТОР ПІСЛЯ ЗБОЮ. Доти НБУ, що лежав о 06:10, коштував цілої доби
// несвіжих даних: наступна спроба — лише завтра. Тепер прогін, чиє
// оновлення впало, повторюється щогодини (refreshRetries разів), і
// лічильник скидається першим же вдалим прогоном.
func (f *Fleet) RunDaily(ctx context.Context) {
	failed := false
	if f.needsCatchUp(ctx) {
		f.main.log.Info("знімка за сьогодні немає — наздоганяю")
		failed = f.dailyRun(ctx) != nil
	}
	loc := f.main.loc
	retries := 0
	for {
		now := time.Now().In(loc)
		next := nextDaily(now)
		retry := false
		if failed && retries < refreshRetries {
			if r := now.Add(time.Hour); r.Before(next) {
				next, retry = r, true
			}
		}
		f.main.log.Info("наступне оновлення", "at", next.Format(time.RFC3339), "повтор", retry)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			if retry {
				retries++
			} else {
				retries = 0
			}
			failed = f.dailyRun(ctx) != nil
		}
	}
}

// nextDaily — найближчі 06:10 у часовому поясі now.
//
// Календарною добою (AddDate), а не 24 годинами: доба переходу на
// зимовий час має 25 годин, на літній — 23, і «+24 год» до сьогоднішніх
// 06:10 давало 05:10 чи 07:10 — прогін зсувався на годину двічі на рік.
func nextDaily(now time.Time) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), 6, 10, 0, 0, now.Location())
	if !next.After(now) {
		next = time.Date(now.Year(), now.Month(), now.Day()+1, 6, 10, 0, 0, now.Location())
	}
	return next
}

// needsCatchUp — чи бракує знімка за сьогодні. Питає ГОЛОВНИЙ портфель:
// прогін один на всіх, і якщо він був, знімок є в кожного.
//
// Помилка читання веде до «не наздоганяємо»: зайвий прогін дешевий, але
// не настільки, щоб робити його наосліп при зламаному сховищі — там
// однаково все впаде наступним кроком, і сказати про це має він, а не ця
// перевірка.
//
// ПО КОЖНОМУ ПОРТФЕЛЮ, а не лише по головному: сателіт, чий знімок не
// записався, лишався б без точки на кривій до наступної доби.
func (f *Fleet) needsCatchUp(ctx context.Context) bool {
	today := domain.NewDate(time.Now().In(f.main.loc))
	for _, r := range f.runners() {
		snaps, err := r.st.ListSnapshots(ctx, today, today)
		if err != nil {
			r.log.Warn("не перевірив знімок за сьогодні", "err", err)
			return false
		}
		if len(snaps) == 0 {
			return true
		}
	}
	return false
}
