package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Що змінилось за вікно — і ЧОМУ саме.
//
// Питання щоденне, а відповіді доти не було ніде. capital_delta_30 у
// документі дає рівно два числа — «на скільки змінився» і «скільки з
// цього внесено ззовні», — а решта злита в неявний залишок. Причина, з
// якої там більше нічого немає, записана в ha-oddinvest (sensor.py): одне
// число «заробило» змішало б курсову переоцінку з купонами. Довід чинний;
// висновок із нього — не «мовчати», а НАЗВАТИ доданки нарізно.
//
// ЩО РОЗКЛАД СТВЕРДЖУЄ, А ЩО НІ. Власні гроші й дохід — виміряні: вони
// проходять журналом, і кожен рядок має дату. Курсова переоцінка —
// ОБЧИСЛЕНА за НИНІШНІМ валютним обсягом: обсягу на початок вікна
// застосунок не зберігає (у знімку є частка долара, але немає євро), тож
// гроші, які зайшли всередині вікна, порахуються повним рухом курсу.
// Решта — саме решта: ціни фондів, ЧВОПА НПФ, накопичений купон,
// округлення. Вона названа реш­ткою, а не розкладена, бо подобового
// джерела під нею немає, і вигадане було б гіршим за чесне «не знаю».
//
// REST-only, поза MQTT: це екран, а не стан портфеля.
//
// Сповіщень немає навмисно (рішення власника). Push вимагав би порогу
// «що вважати вартим сповіщення», тобто судження, якого застосунок не
// виносить ніде.
type digestCause struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	UAH      float64 `json:"uah"`
	Measured bool    `json:"measured"`
	Why      string  `json:"why,omitempty"`
}

type digestResp struct {
	Days int `json:"days"`
	// FromDate / ToDate — СПРАВЖНІ дати знімків, а не межі вікна: демон
	// міг лежати, і підписати різницю замовленими датами означало б
	// приписати їй проміжок, якого вона не міряє. Той самий прийом, що в
	// periodStructureOf.
	FromDate  string           `json:"from_date,omitempty"`
	ToDate    string           `json:"to_date,omitempty"`
	FromUAH   float64          `json:"from_uah,omitempty"`
	ToUAH     float64          `json:"to_uah,omitempty"`
	DeltaUAH  float64          `json:"delta_uah"`
	DeltaPct  float64          `json:"delta_pct,omitempty"`
	Causes    []digestCause    `json:"causes,omitempty"`
	Structure *periodStructure `json:"structure,omitempty"`
	Why       string           `json:"why,omitempty"`
}

// digestWindows — вікна, які має сенс питати. Три, а не довільне число:
// день («що сталося вчора»), тиждень («як пройшов тиждень») і тридцять
// днів — те саме вікно, що в capital_delta_30, щоб два екрани не давали
// різних чисел на те саме питання.
var digestWindows = map[int]bool{1: true, 7: true, 30: true}

func (s *Server) handleDigest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	days := 7
	if q := r.URL.Query().Get("window"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || !digestWindows[n] {
			writeErr(w, http.StatusBadRequest,
				fmt.Errorf("вікно може бути 1, 7 або 30 днів, не %q", q))
			return
		}
		days = n
	}
	now := time.Now()
	today := domain.NewDate(now)
	from := today.AddDays(-days)
	out := digestResp{Days: days}

	snaps, err := s.st.ListSnapshots(ctx, "", "")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	st, why := periodStructureOf(snaps, from, "проміжок", "проміжку")
	if st == nil {
		out.Why = why
		writeJSON(w, http.StatusOK, out)
		return
	}
	out.Structure = st
	out.FromDate, out.ToDate = st.FromDate, st.ToDate
	for _, row := range st.Rows {
		if row.Key == "capital" {
			out.FromUAH, out.ToUAH, out.DeltaUAH = row.Before, row.After, row.Delta
			if row.Before > 0 {
				out.DeltaPct = round2(row.Delta / row.Before * 100)
			}
		}
	}

	// Проміжок для потоків — ТОЙ САМИЙ, що між знімками. Інакше причини
	// покривали б інші дні, ніж різниця, яку вони пояснюють, і залишок
	// приймав би на себе чужі гроші.
	events, err := s.cashEvents(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	fromD, toD := domain.Date(st.FromDate).AddDays(1), domain.Date(st.ToDate)
	sum := summarizeCash(events, fromD, toD)

	// Тіло погашення НЕ дохід: воно лише переїжджає з номіналу на
	// рахунок, і капітал від нього не міняється. Той самий поділ, що в
	// «Рік у цифрах» (buildYear).
	var principal int64
	for _, e := range sum.Rows {
		if e.Principal {
			principal += e.UAH
		}
	}
	earned := sum.IncomeUAH - principal

	fx, fxWhy := s.digestFX(ctx, fromD)
	own, income := sum.major(sum.OwnUAH()), sum.major(earned)
	rest := round2(out.DeltaUAH - own - income - fx)

	out.Causes = []digestCause{
		{Key: "own", Label: "Свої гроші", UAH: own, Measured: true,
			Why: "внески й зняття, разом із подушкою та цілями"},
		{Key: "income", Label: "Дохід", UAH: income, Measured: true,
			Why: "купони, дивіденди й відсотки, що надійшли; тіло погашення сюди не входить — воно лише переїжджає"},
		{Key: "fx", Label: "Курс", UAH: fx, Measured: false, Why: fxWhy},
		{Key: "rest", Label: "Решта", UAH: rest, Measured: false,
			Why: "ціни фондів, ЧВОПА НПФ, накопичений купон, округлення — тут немає подобового джерела, тож це чесно решта, а не розкладка"},
	}
	writeJSON(w, http.StatusOK, out)
}

// digestFX — курсова переоцінка валютної частини капіталу.
//
// Обсяг береться НИНІШНІЙ (частки з документа стану), бо обсягу на
// початок вікна застосунок не зберігає: у знімку є частка долара й немає
// євро. Похибка при цьому в один бік і названа: гроші, що зайшли
// всередині вікна, порахуються повним рухом курсу.
func (s *Server) digestFX(ctx context.Context, from domain.Date) (float64, string) {
	doc, err := s.buildState(ctx, time.Now())
	if err != nil || doc == nil {
		return 0, "курс не пораховано: стан не зібрався"
	}
	total := 0.0
	var parts []string
	for cur, share := range map[string]float64{
		money.USD: doc.USDSharePct, money.EUR: doc.EURSharePct,
	} {
		if share <= 0 {
			continue
		}
		nowE4, err := s.st.LatestRate(ctx, cur)
		if err != nil || nowE4 <= 0 {
			continue
		}
		then, err := s.st.RatePointOnOrBefore(ctx, cur, from)
		if err != nil || then.RateE4 <= 0 {
			continue
		}
		exposure := doc.CapitalUAH * share / 100
		v := exposure * (1 - float64(then.RateE4)/float64(nowE4))
		total += v
		parts = append(parts, fmt.Sprintf("%s %s→%s", cur,
			fx4(then.RateE4), fx4(nowE4)))
	}
	if len(parts) == 0 {
		return 0, "валютної частини немає — рухати нічого"
	}
	why := "переоцінка НИНІШНЬОГО валютного обсягу: " + joinSemi(parts) +
		". Обсягу на початок вікна застосунок не зберігає, тож гроші, що зайшли всередині, пораховані повним рухом курсу"
	return round2(total), why
}

func fx4(e4 int64) string { return fmt.Sprintf("%.2f", float64(e4)/10000) }

func joinSemi(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "; "
		}
		out += p
	}
	return out
}
