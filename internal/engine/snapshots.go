// Ряд кривої «Як росте»: добові знімки, жива точка на сьогодні й
// накопичені зовнішні гроші на кожен день.
//
// Сама по собі таблиця знімків на два питання кривої не відповідає.
//
//  1. Сьогоднішньої точки в ній або немає, або вона ранкова: знімок
//     пишеться о 06:10, і операція, внесена вдень, лягає на криву аж
//     завтра. Тому останню точку дає документ стану на момент запиту —
//     рівно той прийом і з тим самим доводом, що в rivalActual
//     (rivals.go): різницю читають саме з кінця кривої.
//
//  2. «Внесено» зі знімка не вийде. Доти UI складав його із собівартостей
//     (облігації + фонди + вклади + резерв + цілі + НПФ), і рахунку
//     брокера там не було — тож гашення паперу (гроші переїжджають на
//     рахунок) смикало лінію вниз, а купівля з рахунку — вгору, хоча ззовні
//     не заходило й не виходило нічого. Питання «скільки грошей зайшло
//     ззовні» застосунок уже вміє ставити — externalMoves (state_money.go),
//     і тут воно ставиться вп'яте тим самим складом.
package engine

import (
	"context"
	"sort"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

// SnapshotRow — рядок кривої: знімок і те, чого в таблиці знімків немає.
type SnapshotRow struct {
	store.Snapshot
	// Live — рядок зібраний із документа на момент запиту, у БД його
	// немає. Архівна таблиця під кривою такий рядок пропускає: вона про
	// записане, а не про стан.
	Live bool
	// ExternalUAH — зовнішні гроші, що зайшли до кінця дня Date включно,
	// нето, копійки гривні.
	ExternalUAH int64
}

// SnapshotSeries — знімки за [from, to] (порожня межа — без межі), а з
// live ще й сьогоднішній день із документа.
//
// Жива точка ЗАМІНЮЄ сьогоднішній знімок, якщо той уже є, і в БД не
// пишеться: знімок лишається ранковим записом, бо на ньому як на записі
// стоять дельта-30, дайджест і суперники. Якщо документ не зібрався,
// ряд віддається без неї — крива без останнього дня краща за п'ятисотку.
func (e *Engine) SnapshotSeries(ctx context.Context, from, to domain.Date, now time.Time, live bool) ([]SnapshotRow, error) {
	snaps, err := e.st.ListSnapshots(ctx, from, to)
	if err != nil {
		return nil, err
	}
	rows := make([]SnapshotRow, 0, len(snaps)+1)
	for i := range snaps {
		rows = append(rows, SnapshotRow{Snapshot: snaps[i]})
	}

	today := domain.NewDate(now)
	if live && (to == "" || !to.Before(today)) && (from == "" || !today.Before(from)) {
		if doc, derr := e.BuildState(ctx, now); derr != nil {
			if e.log != nil {
				e.log.Warn("жива точка кривої не зібралась", "err", derr)
			}
		} else {
			row := SnapshotRow{Snapshot: store.SnapshotOf(today, doc), Live: true}
			if n := len(rows); n > 0 && rows[n-1].Date == today {
				rows[n-1] = row
			} else {
				rows = append(rows, row)
			}
		}
	}

	ext, err := e.externalByDay(ctx, rows)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].ExternalUAH = ext[i]
	}
	return rows, nil
}

// externalByDay — накопичені зовнішні гроші на кінець дня кожного рядка.
//
// Курс СЬОГОДНІШНІЙ, як у плитці «Внесено за місяць» і в темпі
// (state_month.go), а не курс дня руху, як у суперників. Причина — одна
// сторінка: під кривою стоїть «Внесено цього місяця», і воно мусить
// збігатись із плиткою місяця до копійки, інакше два числа про те саме
// розійдуться на першому ж валютному поповненні. Ціна рішення: крива
// «уся історія» — у сьогоднішніх гривнях, тож долар, внесений рік тому,
// важить на ній як сьогоднішній.
//
// Рух без курсу пропускається мовчки — так само, як у плитці місяця.
func (e *Engine) externalByDay(ctx context.Context, rows []SnapshotRow) ([]int64, error) {
	out := make([]int64, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	// Лише три журнали, а не loadSources цілком: складу externalMoves
	// більше нічого не треба, а повне завантаження тягне весь портфель.
	src := &sources{}
	var err error
	if src.deposits, err = e.st.ListDeposits(ctx); err != nil {
		return nil, err
	}
	if src.reserveOps, err = e.st.ListReserveOps(ctx); err != nil {
		return nil, err
	}
	if src.goalOps, err = e.st.ListGoalOps(ctx); err != nil {
		return nil, err
	}
	rates, err := e.Rates(ctx)
	if err != nil {
		return nil, err
	}
	moves := externalMoves(src)
	sort.SliceStable(moves, func(i, j int) bool { return moves[i].Date.Before(moves[j].Date) })

	var acc int64
	mi := 0
	for i := range rows {
		for mi < len(moves) && !rows[i].Date.Before(moves[mi].Date) {
			if u, cerr := fx.ToUAH(money.New(moves[mi].Amount, moves[mi].Currency), rates); cerr == nil {
				acc += u.Amount()
			}
			mi++
		}
		out[i] = acc
	}
	return out, nil
}
