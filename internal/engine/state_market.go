// Фаза «ринок»: що первинний ринок платить за строк.
//
// Найменша з фаз BuildState і єдина, що дивиться назовні: решта зводить
// портфель користувача, ця приносить у документ зовнішній орієнтир.
// Чистa проєкція — рядки зі сховища на рядки контракту, жодного
// звернення до бази (воно в state_sources.go, як вимагає sources-boundary).

package engine

import (
	"github.com/ODDsama/oddinvest/internal/domain"
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
//
// yieldByCur — НОМІНАЛЬНА дохідність портфеля по валютах (bnd.YieldByCur).
// Саме номінальна: чому не реальна, сказано над MarketYieldRow.VsPortfolioPP.
// Рахуємо різницю тут, а не у споживача, бо споживачів уже двоє (картка у
// вебі й сповіщення в HA), і кожен вибирав би базу самостійно.
func buildMarket(pts []store.AuctionPoint, yieldByCur map[string]float64) marketPhase {
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
		pct := Round2(float64(p.IncomeBP) / 100)
		row := state.MarketYieldRow{
			Currency: p.Currency,
			Bucket:   p.Bucket,
			Pct:      pct,
			Date:     string(p.Date),
			ISIN:     p.ISIN,
		}
		// Порожньо, коли паперів цієї валюти в портфелі немає: нуль тут
		// прочитався б як «ринок платить рівно стільки ж», хоч насправді
		// порівнювати нема з чим.
		if my, ok := yieldByCur[p.Currency]; ok && my > 0 {
			row.VsPortfolioPP = Round2(pct - my)
		}
		out = append(out, row)
	}
	return marketPhase{yield: out}
}

// marketRate — стартова ставка реінвесту валюти з кривої аукціонів і
// дата розміщення, з якого вона взята.
type marketRate struct {
	Pct  float64
	Date domain.Date
}

// auctionMinDays — коротші розміщення (3–6 місяців) у стартову ставку не
// йдуть: вона про гроші, які реінвестуються роками, і тримісячна ставка
// тут відповідала б на інше питання.
const auctionMinDays = 180

// auctionRateByCur — під скільки Мінфін розміщує ЗАРАЗ, по валютах: з неї
// прогноз стартує реінвест (рішення власника 2026-09-23).
//
// Доти стартовою ставкою була дохідність УЖЕ куплених лотів. Це факт про
// минулі покупки, а реінвестуються купони й нові гроші за тим, що ринок
// дасть сьогодні, — і портфель, купований пів року тому під 19 %, малював
// би майбутнє під 19 % і тоді, коли аукціон дає 16.
//
// Правило вибору, без нового запиту (рядки ті самі, що в buildMarket):
//   - лише свіжі розміщення: не старші за staleAfterDays — той самий поріг
//     несвіжості, що в помічника реінвесту;
//   - строк «1y» (RivalOVDPBucket — той самий орієнтир, що в бенчмарку);
//     немає його — строк, найближчий до року, але не коротший за
//     auctionMinDays; рівні — новіше розміщення.
//
// Валюти без свіжого аукціону в мапі немає: там прогноз лишається на
// запасних шляхах (дохідність портфеля, далі купон довідника) — див.
// sleeveFactory.startRate.
func auctionRateByCur(pts []store.AuctionPoint, today domain.Date) map[string]marketRate {
	type pick struct {
		p    store.AuctionPoint
		dist int64
	}
	best := map[string]pick{}
	for _, p := range pts {
		if p.IncomeBP <= 0 || p.Days < auctionMinDays {
			continue
		}
		if daysBetween(p.Date, today) > staleAfterDays {
			continue
		}
		dist := p.Days - 365
		if dist < 0 {
			dist = -dist
		}
		if p.Bucket == RivalOVDPBucket {
			dist = -1 // «1y» перемагає будь-що
		}
		cur, ok := best[p.Currency]
		if !ok || dist < cur.dist || (dist == cur.dist && p.Date.After(cur.p.Date)) {
			best[p.Currency] = pick{p, dist}
		}
	}
	out := make(map[string]marketRate, len(best))
	for c, b := range best {
		out[c] = marketRate{Pct: Round2(float64(b.p.IncomeBP) / 100), Date: b.p.Date}
	}
	return out
}
