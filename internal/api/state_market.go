// Фаза «ринок»: що первинний ринок платить за строк.
//
// Найменша з фаз buildState і єдина, що дивиться назовні: решта зводить
// портфель користувача, ця приносить у документ зовнішній орієнтир.
// Чистa проєкція — рядки зі сховища на рядки контракту, жодного
// звернення до бази (воно в state_sources.go, як вимагає sources-boundary).

package api

import (
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

type marketPhase struct {
	yield []state.MarketYieldRow
}

// buildMarket — останні рівні розміщення в тому вигляді, в якому їх
// споживає контракт.
//
// Порядок зберігаємо той, у якому їх віддало сховище: там він уже за
// валютою й строком, а перекладати рядки — не привід їх перетасувати.
func buildMarket(pts []store.AuctionPoint) marketPhase {
	if len(pts) == 0 {
		return marketPhase{}
	}
	out := make([]state.MarketYieldRow, 0, len(pts))
	for _, p := range pts {
		// Нуль сюди не потрапляє ще з парсера НБУ (рівень «невідомо» не
		// зберігається зовсім), але порожній рядок у документі означав би
		// «ринок платить 0%», і перевірка тут дешевша за таку заяву.
		if p.IncomeBP <= 0 {
			continue
		}
		out = append(out, state.MarketYieldRow{
			Currency: p.Currency,
			Bucket:   p.Bucket,
			Pct:      round2(float64(p.IncomeBP) / 100),
			Date:     string(p.Date),
			ISIN:     p.ISIN,
		})
	}
	return marketPhase{yield: out}
}
