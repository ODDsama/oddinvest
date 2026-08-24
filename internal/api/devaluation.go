// Знецінення гривні: вимірювання за курсами НБУ, три сходинки довіри
// (задане руками > виміряне > припущене) і реальна дохідність із поправкою
// на нього.

package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

// nbuRefreshedKey — час останнього успішного оновлення довідника НБУ.
// Пишеться джобою (не через PUT /api/settings), тож у settingsKeys нема.
const nbuRefreshedKey = "nbu_refreshed_at"

// defaultDevaluationPct — очікуване річне знецінення гривні до твердої
// валюти, коли користувач не задав своє. 6% — приблизний темп 2016-2025
// (26 -> 42 ₴/$ за 9 років), тобто без урахування шоків 2014 і 2022.
// Свідомо не «історичне середнє з 2014»: воно дало б ~16% і зробило б
// будь-який гривневий папір безнадійним.
const defaultDevaluationPct = 6.0

// devalWindowYears — вікно вимірювання знецінення. Десять років, бо
// гривня падає стрибками: коротке вікно ловить або стрибок, або затишшя
// між ними, і число стає лотереєю (2016→2020 дають −0.3%/рік, 2022→2023
// дають +34%). Десятирічне усереднює і те, і те.
const devalWindowYears = 10

// devalMinDays — скільки історії мусить бути, щоб їй вірити. Вісім років
// із десяти: недобір у пару років вікно ще витримує, а трирічний уламок —
// уже інша величина.
const devalMinDays = 8 * 365

// devaluationSource — звідки взялося чинне число, для UI.
const (
	devalManual   = "manual"
	devalMeasured = "measured"
	devalDefault  = "default"
)

// measuredDevaluation — річний темп зі СПРАВЖНІХ курсів НБУ, що лежать у
// fx_rates. Історію туди пише добова джоба, а глибину дає разовий
// backfill при старті.
func (s *Server) measuredDevaluation(ctx context.Context) (float64, store.RatePoint, store.RatePoint, bool) {
	from := domain.NewDate(time.Now().AddDate(-devalWindowYears, 0, 0))
	oldest, err := s.st.OldestRate(ctx, money.USD, from)
	if err != nil || oldest.RateE4 <= 0 {
		return 0, oldest, store.RatePoint{}, false
	}
	newest, err := s.st.NewestRate(ctx, money.USD)
	if err != nil || newest.RateE4 <= 0 {
		return 0, oldest, newest, false
	}
	days := domain.DaysBetween(oldest.Date, newest.Date)
	// Вікно має бути справді довгим, а не «яке вийшло». Якщо backfill
	// обірвався на півдорозі й історії лишилось три роки, темп із неї
	// вийде 10.2% замість 6.4% — і це буде не виміряне знецінення, а
	// виміряний уламок вікна. Краще чесно відступити на припущення.
	if days < devalMinDays {
		return 0, oldest, newest, false
	}
	pct, ok := domain.AnnualPct(float64(oldest.RateE4), float64(newest.RateE4), days)
	return round2(pct), oldest, newest, ok
}

// devaluation — знецінення, з яким рахує ВЕСЬ застосунок. Три сходинки, і
// порядок тут — це порядок довіри: те, що людина задала свідомо, важить
// більше за виміряне, а виміряне — більше за припущене.
func (s *Server) devaluation(ctx context.Context) float64 {
	v, _ := s.devaluationWithSource(ctx)
	return v
}

func (s *Server) devaluationWithSource(ctx context.Context) (float64, string) {
	if raw, _ := s.st.GetSetting(ctx, "uah_devaluation_pct"); raw != "" { //nolint:errcheck // порожньо = не задано; помилка так само веде на виміряне значення
		if f, err := strconv.ParseFloat(raw, 64); err == nil && f >= 0 {
			return f, devalManual
		}
	}
	if m, _, _, ok := s.measuredDevaluation(ctx); ok {
		return m, devalMeasured
	}
	return defaultDevaluationPct, devalDefault
}

// realYield — річна дохідність, приведена до сьогоднішньої гривні.
// Для валютних лишається як є (долар купівельну спроможність тримає),
// для гривневих ділиться на знецінення. Одна формула на всі інструменти
// й на обидва боки застосунку: і на те, що можна купити (реінвест), і на
// те, що вже лежить у портфелі. Інакше вони б не порівнювались.
func realYield(y float64, cur string, devalPct float64) float64 {
	if cur == money.UAH {
		return (1+y)/(1+devalPct/100) - 1
	}
	return y
}

// nominalYield — зворотний бік realYield: яка НОМІНАЛЬНА ставка дає таку
// реальну в цій валюті.
//
// Потрібна там, де порівняння вже зроблено в реальних відсотках, а
// рахувати далі треба в номінальних гривнях. Такий випадок один —
// перекладання (handlers_switch.go): рейтинг альтернатив порівнює
// інструменти за реальною дохідністю, а дисконтувати графік виплат
// паперу можна лише номінальною ставкою його власної валюти. Підставити
// туди реальну означало б занизити поріг рівно на все знецінення, тобто
// радити продаж там, де його немає.
//
// Стоїть упритул до realYield навмисно: це пара, і розійтись їм не можна.
// Перевірку на взаємність тримає TestRealNominalRoundTrip.
func nominalYield(real float64, cur string, devalPct float64) float64 {
	if cur == money.UAH {
		return (1+real)*(1+devalPct/100) - 1
	}
	return real
}

// handleDevaluation — звідки взялося знецінення і що показують дані.
//
// REST-only, поза MQTT: це екран Налаштувань, а не стан портфеля, і
// роздувати retained-повідомлення довідковою таблицею немає сенсу.
//
// Вікна показуємо всі, а не лише чинне десятирічне, саме тому, що вони
// РІЗНІ: побачивши поруч «за рік 4.3%» і «за десять 6.1%», людина
// розуміє, чому число не можна брати з короткого вікна, — а не мусить
// вірити на слово.
func (s *Server) handleDevaluation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	type window struct {
		Label string  `json:"label"`
		Years int     `json:"years"`
		Pct   float64 `json:"pct"`
		From  string  `json:"from"`
		To    string  `json:"to"`
	}
	out := struct {
		EffectivePct float64  `json:"effective_pct"`
		Source       string   `json:"source"`
		Windows      []window `json:"windows,omitempty"`
		Note         string   `json:"note,omitempty"`
	}{}
	out.EffectivePct, out.Source = s.devaluationWithSource(ctx)

	newest, err := s.st.NewestRate(ctx, money.USD)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if newest.RateE4 <= 0 {
		out.Note = "історії курсу ще немає — застосунок працює на припущенні"
		writeJSON(w, http.StatusOK, out)
		return
	}
	for _, y := range []int{1, 3, 5, 10} {
		from := domain.NewDate(time.Now().AddDate(-y, 0, 0))
		oldest, err := s.st.OldestRate(ctx, money.USD, from)
		if err != nil || oldest.RateE4 <= 0 {
			continue
		}
		days := domain.DaysBetween(oldest.Date, newest.Date)
		pct, ok := domain.AnnualPct(float64(oldest.RateE4), float64(newest.RateE4), days)
		if !ok {
			continue
		}
		out.Windows = append(out.Windows, window{
			Label: fmt.Sprintf("за %d %s", y, plural(y, "рік", "роки", "років")),
			Years: y, Pct: round2(pct),
			From: string(oldest.Date), To: string(newest.Date),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
