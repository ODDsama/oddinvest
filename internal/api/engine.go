// engine — розрахунковий шар застосунку: усе, що з портфеля (сховища,
// звуженого до одного портфеля) і часу виводить числа, — без HTTP.
//
// Доти ці методи висіли на *Server разом з обробниками, авторизацією й
// публікацією в MQTT, хоч потребували лише сховища й журналу. Через це
// алгоритм не можна було викликати, не збудувавши сервер, а межа між
// «що це рахує» і «як це віддається» трималась лише на звичці. Тепер
// вона — тип: методи на *engine не бачать ні запиту, ні відповіді, ні
// замка, ні публікатора.
//
// Це перший крок винесення розрахунків з internal/api (план D11): поки
// engine живе в тому самому пакеті, щоб перенос приймачів не змішався з
// переносом файлів. Далі файли з методами engine переїжджають у власний
// пакет internal/engine, а api лишається HTTP-шаром поверх нього.
package api

import (
	"context"
	"log/slog"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/store"
)

type engine struct {
	st  *store.Store
	log *slog.Logger
}

// portfolio — все, що треба домену, одним заходом.
func (e *engine) portfolio(ctx context.Context) (lots []domain.Lot, sales []domain.Sale,
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

func (e *engine) rates(ctx context.Context) (fx.Rates, error) {
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
