// Рух капіталу за 30 днів — проти добового знімка.
//
// Це перше число історії, яке документ віддає сам. Доти історію читали
// лише окремі ручки (/api/period, /api/progress, /api/rivals), а шапка
// веб-застосунку й герой «Огляду» стояли без «за 30 днів» і казали про
// це вголос. Правило одне: арифметика над знімками живе тут, у браузері
// її не буде (CLAUDE.md §5).
package api

import (
	"context"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// deltaWindowDays — вікно дельти. Тридцять, а не календарний місяць: у
// шапці число читають щодня, і «з початку місяця» першого числа було б
// нулем, а тридцять першого — місяцем.
const deltaWindowDays = 30

// deltaLookbackDays — скільки ще назад шукати знімок, коли рівно на
// тридцятий день його немає (демон лежав). Два тижні: старший знімок уже
// не «за місяць».
const deltaLookbackDays = 14

// snapshotAgo — останній знімок на deltaWindowDays і більше днів тому в
// межах пошуку; nil, коли такого немає.
func (s *Server) snapshotAgo(ctx context.Context, today domain.Date) (*store.Snapshot, error) {
	to := today.AddDays(-deltaWindowDays)
	snaps, err := s.st.ListSnapshots(ctx, to.AddDays(-deltaLookbackDays), to)
	if err != nil || len(snaps) == 0 {
		return nil, err
	}
	sn := snaps[len(snaps)-1] // ListSnapshots віддає за датою
	return &sn, nil
}

// buildCapitalDelta — дельта проти знімка з sources, або nil.
//
// ContribUAH — зовнішні гроші від дати знімка (включно, з того самого
// доводу, що в rivalFlows: знімок кладеться о 06:10, люди вносять удень)
// до сьогодні, сьогоднішнім курсом. Не курсом дня кожного руху, як у
// зведеному XIRR: вікно місячне, різниця в межах похибки, а курс на дату
// коштував би запиту на рух у збірці, що йде на кожен whatif.
func buildCapitalDelta(src *sources, capitalNow float64, rates fx.Rates) *state.CapitalDelta {
	if src.capitalAgo == nil {
		return nil
	}
	from := src.capitalAgo.Date
	fromUAH := float64(snapshotCapitalUAH(*src.capitalAgo)) / 100
	out := &state.CapitalDelta{
		FromDate: string(from),
		FromUAH:  round2(fromUAH),
		DeltaUAH: round2(capitalNow - fromUAH),
	}
	if fromUAH > 0 {
		out.DeltaPct = round2((capitalNow - fromUAH) / fromUAH * 100)
	}
	var contrib int64
	add := func(on domain.Date, amount int64, cur string) {
		if on.Before(from) || amount == 0 {
			return
		}
		if u, err := fx.ToUAH(money.New(amount, cur), rates); err == nil {
			contrib += u.Amount()
		}
	}
	// Той самий склад, що в «усіх грошах» ціни рішень (rivalFlows,
	// levelAll): гаманець, подушка, цілі, пенсійний.
	for _, d := range src.deposits {
		add(d.Date, d.Amount, d.Currency)
	}
	for _, op := range src.reserveOps {
		add(op.Date, op.Amount, op.Currency)
	}
	for _, op := range src.goalOps {
		add(op.Date, op.Amount, op.Currency)
	}
	for _, op := range src.npfOps {
		add(op.Date, op.Amount, money.UAH)
	}
	out.ContribUAH = round2(float64(contrib) / 100)
	return out
}

// snapshotCapitalUAH — капітал зі знімка, мінорні одиниці.
//
// У знімка немає колонки capital_uah: він старший за state.Capital, а
// дописувати її заднім числом означало б лишити нулі в усіх минулих
// рядках, тобто зробити колонку брехливою рівно там, де на неї дивляться.
// Сума збирається з тих самих доданків, що й у документі стану, і
// ЄДИНИЙ її двійник — запасна гілка capitalUAH у web/js/format.js, яка
// існує для старішого бекенда. Міняєш склад капіталу — дивись і туди.
//
// Через snapshotPortfolioUAH, а не сімома доданками: різниця між двома
// рівнями «Ціни моїх рішень» — це рівно три останні доданки, і записана
// вона тут одним рядком, щоб не бути домовленістю. Читачі: підсумок
// місяця, ціна рішень, віхи капіталу й ця дельта — одне означення на всіх.
func snapshotCapitalUAH(sn store.Snapshot) int64 {
	return snapshotPortfolioUAH(sn) + sn.ReserveUAH + sn.GoalsUAH + sn.NPFUAH
}
