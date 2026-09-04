// Валютний шок — рух курсу, який УЖЕ був, накладений на сьогоднішній
// портфель. Чиста частина: пошук епізоду й підміна курсів. HTTP і
// збирання документа — у handlers_fx_shock.go (той самий поділ, що між
// handleProgress і state_progress.go).
//
// ТРИ МЕЖІ ДАНИХ, ЯКІ ФІЧА НАЗИВАЄ ВГОЛОС. Кожна — не сором, а умова, за
// якої число лишається чесним; сховати їх означало б показати точність,
// якої немає.
//
//  1. ОДИНИЦЯ — МІСЯЦЬ. Історію залив backfill по одній точці на місяць
//     за десять років (jobs.BackfillRates), а добові точки є лише за час
//     роботи сервісу. Тому «найгірший місячний рух» міряний по місячній
//     сітці й МОЖЕ ЗАНИЖУВАТИ: усередині лютого 2022 курс пройшов далі,
//     ніж покаже будь-яка пара перших чисел місяця. Скільки місяців
//     реально знайшлось — стоїть у відповіді полем Measured.Months.
//
//  2. ШОК СУТО ВАЛЮТНИЙ. Виміряної історії СТАВОК за 2014 чи 2022 у базі
//     немає й нізвідки взяти: ovdp_auctions тримає близько пʼятдесяти
//     двох тижнів (jobs.BackfillAuctionsIfThin). Сценарії «ставка ±п.п.»
//     живуть окремо й давно (state_sensitivity.go, state_risk.go) — тут
//     вони не дублюються, на них посилаються.
//
//  3. ЖОДЕН ЕПІЗОД НЕ ЗАШИТИЙ. Довід — у шапці domain/fxshock.go.
//
// ЧОМУ ЯКІРНА ВАЛЮТА ОДНА. Найгірше вікно долара й найгірше вікно євро —
// різні відрізки часу, і накласти їх ОДНОЧАСНО означало б зібрати
// спільну подію, якої не було, та ще й видати її за виміряну. Тому
// найгірше вікно шукається по долару (тією ж валютою міряє знецінення в
// devaluation.go), а рух решти береться ЗА ТИМИ САМИМИ ДАТАМИ. Немає
// точки на цих датах — валюта не шокується зовсім, і причина стоїть
// поруч із нею, а не ховається в загальному «немає даних».

package api

import (
	"fmt"
	"math"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
)

// fxShockWindows — вікна, які можна питати, у місяцях.
//
// Рік, квартал і місяць: три різні питання про ту саму історію. Довшого
// немає навмисно — на десятирічному вікні «найгірші десять років» були б
// одним-єдиним кандидатом, тобто не найгіршим, а просто наявним.
var fxShockWindows = []int{1, 3, 12}

// fxShockAnchor — валюта, по якій шукається найгірше вікно.
const fxShockAnchor = money.USD

type fxShockMove struct {
	Currency string      `json:"currency"`
	From     domain.Date `json:"from,omitempty"`
	To       domain.Date `json:"to,omitempty"`
	FromRate float64     `json:"from_rate,omitempty"`
	ToRate   float64     `json:"to_rate,omitempty"`
	MovePct  float64     `json:"move_pct,omitempty"`
	RateNow  float64     `json:"rate_now,omitempty"`
	RateThen float64     `json:"rate_then,omitempty"`
	// Why непорожнє — валюта НЕ шокована, і тут сказано чому.
	Why string `json:"why,omitempty"`
}

type fxShockMeasured struct {
	Anchor string      `json:"anchor"`
	From   domain.Date `json:"from,omitempty"`
	To     domain.Date `json:"to,omitempty"`
	Months int         `json:"months"`
}

type fxShockEpisode struct {
	WindowMonths int           `json:"window_months"`
	From         domain.Date   `json:"from"`
	To           domain.Date   `json:"to"`
	Moves        []fxShockMove `json:"moves"`
}

type fxShockDoc struct {
	// Granularity — завжди "month", і поле це не церемонія: воно є
	// частиною відповіді рівно тому, що межа №1 вище не видна з чисел.
	Granularity string          `json:"granularity"`
	Measured    fxShockMeasured `json:"measured"`
	// Windows — вікна, які на цій історії справді вимірюються. Порожній
	// перелік означає, що вибирати нема з чого.
	Windows []int           `json:"windows"`
	Episode *fxShockEpisode `json:"episode"`
	After   *state.Doc      `json:"after"`
	// Why непорожнє ⇒ Episode і After порожні.
	Why string `json:"why,omitempty"`
}

// rateUAH — курс ×10⁴ у звичайних гривнях.
func rateUAH(e4 int64) float64 { return math.Round(float64(e4)) / 10000 }

// buildFXShock шукає епізод і рахує курси, якими стане сьогоднішній день.
//
// Друге значення — накладка курсів для гіпотези (домішує її вже
// handlers_fx_shock.go); порожня, коли епізоду немає. Документа стану ця
// функція не збирає: вона чиста, а збирання ходить у сховище.
func buildFXShock(hist map[string][]domain.FXPoint, rates fx.Rates, window int) (*fxShockDoc, fx.Rates) {
	anchorHist := domain.MonthlyFX(hist[fxShockAnchor])
	out := &fxShockDoc{
		Granularity: "month",
		Measured:    fxShockMeasured{Anchor: fxShockAnchor, Months: len(anchorHist)},
		Windows:     []int{},
	}
	if len(anchorHist) > 0 {
		out.Measured.From = anchorHist[0].Date
		out.Measured.To = anchorHist[len(anchorHist)-1].Date
	}
	for _, w := range fxShockWindows {
		if _, ok := domain.WorstFXMove(anchorHist, w); ok {
			out.Windows = append(out.Windows, w)
		}
	}

	worst, ok := domain.WorstFXMove(anchorHist, window)
	if !ok {
		out.Why = fmt.Sprintf(
			"на місячній сітці %s набралось %d місяців — вікон на %d міс. замало, "+
				"щоб «найгірше» щось означало", fxShockAnchor, len(anchorHist), window)
		return out, nil
	}
	if rates[fxShockAnchor] <= 0 {
		out.Why = fmt.Sprintf("немає сьогоднішнього курсу %s — нема що зрушувати", fxShockAnchor)
		return out, nil
	}

	ep := &fxShockEpisode{WindowMonths: window, From: worst.From, To: worst.To}
	shocked := fx.Rates{}

	// Якір першим, решта — за ТИМИ САМИМИ датами.
	for _, cur := range fxHistoryCurrencies {
		mv, found := worst, true
		if cur != fxShockAnchor {
			mv, found = domain.FXMoveOver(hist[cur], worst.From, worst.To)
		}
		now := rates[cur]
		switch {
		case !found:
			ep.Moves = append(ep.Moves, fxShockMove{Currency: cur,
				Why: fmt.Sprintf("курсу %s на %s або %s у базі немає — цю валюту не рушимо",
					cur, worst.From, worst.To)})
			continue
		case now <= 0:
			ep.Moves = append(ep.Moves, fxShockMove{Currency: cur,
				Why: fmt.Sprintf("немає сьогоднішнього курсу %s", cur)})
			continue
		}
		// Програється ВІДНОСНИЙ рух на сьогоднішньому рівні: рівні того
		// року нам ні до чого, а от «стільки ж відсотків» — це рівно те,
		// що було виміряно.
		after := int64(math.Round(float64(now) * (1 + mv.Pct/100)))
		shocked[cur] = after
		ep.Moves = append(ep.Moves, fxShockMove{
			Currency: cur, From: mv.From, To: mv.To,
			FromRate: rateUAH(mv.FromE4), ToRate: rateUAH(mv.ToE4),
			MovePct: round2(mv.Pct), RateNow: rateUAH(now), RateThen: rateUAH(after),
		})
	}

	if len(shocked) == 0 {
		out.Why = "жодну валюту не вдалось зрушити за цим епізодом"
		return out, nil
	}
	out.Episode = ep
	return out, shocked
}
