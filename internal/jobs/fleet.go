package jobs

import (
	"context"
	"sync"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
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

// dailyRun — добовий прогін: довідник один раз, далі кожному портфелю
// його знімок, дамп і публікацію, наостанок гігієна сховища.
//
// RefreshAll НЕ обриває послідовність: його помилка означає лише «НБУ
// мовчить», а знімки й бекапи рахуються з того, що вже в базі, і чужа
// недоступність не привід їх пропустити (шапка Runner.RefreshAll).
func (f *Fleet) dailyRun(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := f.main.RefreshAll(cctx); err != nil {
		f.main.log.Error("добове оновлення довідника", "err", err)
	}
	for _, r := range f.runners() {
		r.persistDaily(cctx)
		if err := r.PublishState(cctx); err != nil {
			r.log.Error("добова публікація", "err", err)
		}
	}
	if err := f.main.st.Maintain(cctx); err != nil {
		f.main.log.Error("обслуговування сховища", "err", err)
	}
}

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
func (f *Fleet) RunDaily(ctx context.Context) {
	if f.needsCatchUp(ctx) {
		f.main.log.Info("знімка за сьогодні немає — наздоганяю")
		f.dailyRun(ctx)
	}
	loc := f.main.loc
	for {
		now := time.Now().In(loc)
		next := time.Date(now.Year(), now.Month(), now.Day(), 6, 10, 0, 0, loc)
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		f.main.log.Info("наступне оновлення", "at", next.Format(time.RFC3339))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			f.dailyRun(ctx)
		}
	}
}

// needsCatchUp — чи бракує знімка за сьогодні. Питає ГОЛОВНИЙ портфель:
// прогін один на всіх, і якщо він був, знімок є в кожного.
//
// Помилка читання веде до «не наздоганяємо»: зайвий прогін дешевий, але
// не настільки, щоб робити його наосліп при зламаному сховищі — там
// однаково все впаде наступним кроком, і сказати про це має він, а не ця
// перевірка.
func (f *Fleet) needsCatchUp(ctx context.Context) bool {
	today := domain.NewDate(time.Now().In(f.main.loc))
	snaps, err := f.main.st.ListSnapshots(ctx, today, today)
	if err != nil {
		f.main.log.Warn("не перевірив знімок за сьогодні", "err", err)
		return false
	}
	return len(snaps) == 0
}
