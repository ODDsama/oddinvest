// Package engine — розрахунковий шар застосунку: усе, що з портфеля
// (сховища, звуженого до одного портфеля) і часу виводить числа, — без
// HTTP.
//
// Доти ці методи висіли на *api.Server разом з обробниками, авторизацією й
// публікацією в MQTT, хоч потребували лише сховища й журналу. Через це
// алгоритм не можна було викликати, не збудувавши сервер, а межа між «що
// це рахує» і «як це віддається» трималась лише на звичці. Тепер вона —
// пакет: тут немає ні запиту, ні відповіді, ні замка, ні публікатора, а
// api імпортує engine і ніколи навпаки.
//
// Експортоване — рівно те, що потрібне обробникам і їхнім тестам (список
// виведено сканером на go/types, а не на око). Джерела (sources) і поля
// гіпотези лишаються закритими: обробник не складає документ стану сам, а
// просить готовий — BuildState, Route, WhatIf, Progress…
package engine

import (
	"context"
	"log/slog"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/store"
)

type Engine struct {
	st  *store.Store
	log *slog.Logger
}

// New — розрахунки над сховищем одного портфеля.
func New(st *store.Store, log *slog.Logger) *Engine {
	return &Engine{st: st, log: log}
}

// portfolio — все, що треба домену, одним заходом.
func (e *Engine) Portfolio(ctx context.Context) (lots []domain.Lot, sales []domain.Sale,
	bonds map[string]domain.Bond, pays []domain.Payment, err error) {
	lots, err = e.st.ListLots(ctx)
	if err != nil {
		return
	}
	sales, err = e.st.ListSales(ctx)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	var isins []string
	for _, l := range lots {
		if !seen[l.ISIN] {
			seen[l.ISIN] = true
			isins = append(isins, l.ISIN)
		}
	}
	bonds, err = e.st.BondsFor(ctx, isins)
	if err != nil {
		return
	}
	pays, err = e.st.PaymentsFor(ctx, isins)
	return
}

func (e *Engine) Rates(ctx context.Context) (fx.Rates, error) {
	r := fx.Rates{}
	for _, code := range []string{"USD", "EUR"} {
		v, err := e.st.LatestRate(ctx, code)
		if err != nil {
			return nil, err
		}
		if v > 0 {
			r[code] = v
		}
	}
	return r, nil
}
