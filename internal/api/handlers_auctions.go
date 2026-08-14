// Крива первинного ринку: під скільки Мінфін розміщує на кожен строк.
//
// Єдиний зовнішній орієнтир у застосунку. Доти дохідність портфеля не
// порівнювалась НІ З ЧИМ, і на питання «13.4% — це нормально?» відповіді
// не було взагалі.
//
// Слова «бенчмарк» тут немає навмисно: /api/benchmark уже зайнятий і
// означає зовсім інше — «а якби я просто тримав долари». Два різні
// бенчмарки за одну картку один від одного породжували б плутанину
// щоразу, коли про них заходить мова.

package api

import (
	"net/http"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// curveRow — один строк однієї валюти: скільки платять зараз і скільки
// платили рік тому.
//
// Рік тому — не окремий графік, а ДРУГИЙ СТОВПЧИК поруч: сама по собі
// крива каже, що ринок платить, і мовчить про те, куди він рухається.
type curveRow struct {
	Currency string  `json:"currency"`
	Bucket   string  `json:"bucket"`
	Days     int64   `json:"days"`
	Pct      float64 `json:"pct"`
	Date     string  `json:"date"`
	ISIN     string  `json:"isin"`
	// PrevPct / PrevDate — те саме розміщення, але станом на рік тому.
	// Порожні, коли історії ще немає: показати нуль означало б намалювати
	// падіння з 15% у нікуди.
	PrevPct  float64 `json:"prev_pct,omitempty"`
	PrevDate string  `json:"prev_date,omitempty"`
	// Обставини того аукціону — не «скільки платять», а «як його брали».
	//
	// MinPct/MaxPct — смуга ПРИЙНЯТИХ заявок: вузька означає, що дилери
	// зійшлись, широка — що згоди не було й середнє число менш надійне.
	// Demand — bid-to-cover: скільки просили проти скільки взяли; нижче
	// одиниці означає, що охочих було менше за пропозицію. Sold — обсяг
	// розміщення у валюті ЦЬОГО рядка (без переводу в гривню: це факт про
	// аукціон, а не про портфель, і курс тут нічого не додає).
	//
	// Усе це лишається в REST і НЕ їде в документ стану. Причина та сама,
	// що вже записана для історії кривої в state.go над MarketYield:
	// retained-повідомлення в MQTT — це стан портфеля, а не ринковий
	// довідник, і контракт з інтеграцією свідомо є підмножиною.
	//
	// І окремо: жодне з цих чисел не є ціною й не наближає до неї. Чому
	// ціни з аукціонного рівня не виводимо взагалі — у README, розділ
	// «Чого тут свідомо немає».
	MinPct float64 `json:"min_pct,omitempty"`
	MaxPct float64 `json:"max_pct,omitempty"`
	Demand float64 `json:"demand,omitempty"`
	Sold   float64 `json:"sold,omitempty"`
}

// handleAuctionsCurve — крива під ту саму картку, що її й показує (як
// /api/benchmark і /api/devaluation): бекенд віддає рівно те, що
// малюється, а не сирий журнал аукціонів, який довелось би зводити в JS.
func (s *Server) handleAuctionsCurve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	cur, err := s.st.AuctionLatestByBucket(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	prev, err := s.st.AuctionByBucketAsOf(ctx, domain.NewDate(now.AddDate(-1, 0, 0)))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	was := map[string]store.AuctionPoint{}
	for _, p := range prev {
		was[p.Currency+"|"+p.Bucket] = p
	}
	out := make([]curveRow, 0, len(cur))
	for _, p := range cur {
		row := curveRow{
			Currency: p.Currency, Bucket: p.Bucket, Days: p.Days,
			Pct:  round2(float64(p.IncomeBP) / 100),
			Date: string(p.Date), ISIN: p.ISIN,
			MinPct: round2(float64(p.MinBP) / 100),
			MaxPct: round2(float64(p.MaxBP) / 100),
			Demand: round2(float64(p.BTCx100) / 100),
			Sold:   round2(float64(p.SoldMinor) / 100),
		}
		// Порівнюємо лише з ІНШИМ розміщенням: коли за рік нового не
		// було, «тоді» і «зараз» — це один і той самий аукціон, і два
		// однакові стовпчики поруч читались би як «нічого не змінилось»,
		// хоч насправді нічого й не вимірювалось.
		if q, ok := was[p.Currency+"|"+p.Bucket]; ok && q.Date != p.Date {
			row.PrevPct = round2(float64(q.IncomeBP) / 100)
			row.PrevDate = string(q.Date)
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, out)
}
