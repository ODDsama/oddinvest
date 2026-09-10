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
func buildCapitalDelta(src *sources, capitalNow float64, rates fx.Rates, today domain.Date) *state.CapitalDelta {
	if src.capitalAgo == nil {
		return nil
	}
	from := src.capitalAgo.Date
	fromUAH := float64(snapshotCapitalUAH(*src.capitalAgo)) / 100
	out := &state.CapitalDelta{
		FromDate: string(from),
		FromUAH:  state.Major(fromUAH, money.UAH),
		DeltaUAH: state.Major(capitalNow-fromUAH, money.UAH),
	}
	// Відсоток — у валюті звітності: старт за курсом СВОЄЇ дати, сьогодні
	// за сьогоднішнім. Суми лишаються гривневими (їх перекладе презентер тими
	// самими курсами), а відсоток — не сума, і презентер його не бачить;
	// поділити гривневий відсоток нема на що — він би не змінився, хоч за
	// місяць курс міг зʼїсти половину росту.
	fromRep, nowRep := fromUAH, capitalNow
	if src.report != "" && src.report != money.UAH {
		q := make(domain.Quotes, 0, len(src.fxHistory[src.report]))
		for _, p := range src.fxHistory[src.report] {
			q = append(q, domain.Quote{On: p.Date, V: fx.Major(p.RateE4)})
		}
		r0, ok0 := q.AsOf(from)
		r1, ok1 := q.AsOf(today)
		if ok0 && ok1 && r0 > 0 && r1 > 0 {
			fromRep, nowRep = fromUAH/r0, capitalNow/r1
		}
	}
	if fromRep > 0 {
		out.DeltaPct = round2((nowRep - fromRep) / fromRep * 100)
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
	// levelAll): гаманець, подушка, цілі. Означення й довід — externalMoves
	// (state_money.go), а не переписаний тут четвертий раз перелік.
	//
	// ПЕНСІЙНОГО тут більше немає, і це виправлення, а не спрощення. Внесок
	// у НПФ списується з рахунку (state_builder.go), тобто це переказ
	// рахунок → НПФ усередині капіталу; форма НПФ ще й заводить парний
	// рядок у deposits сама. Порахований окремим журналом, він давав ті самі
	// гроші двічі: на бойовому три внески (500 + 101 + 1 000 ₴) роздували
	// «зовнішні гроші» місяця на 1 601 ₴, а капітал від них не змінювався
	// взагалі.
	for _, m := range externalMoves(src) {
		add(m.Date, m.Amount, m.Currency)
	}
	out.ContribUAH = state.Minor(contrib, money.UAH)
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
