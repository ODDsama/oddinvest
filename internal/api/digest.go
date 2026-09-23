// Переоцінка валютної частини капіталу для дайджесту «що змінилось».

package api

import (
	"context"
	"fmt"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	money "github.com/Rhymond/go-money"
)

// DigestFX — курсова переоцінка валютної частини капіталу.
//
// Обсяг береться НИНІШНІЙ (частки з документа стану), бо обсягу на
// початок вікна застосунок не зберігає: у знімку є частка долара й немає
// євро. Похибка при цьому в один бік і названа: гроші, що зайшли
// всередині вікна, порахуються повним рухом курсу.
func (e *Engine) DigestFX(ctx context.Context, from domain.Date) (float64, string) {
	doc, err := e.BuildState(ctx, time.Now())
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
		nowE4, err := e.st.LatestRate(ctx, cur)
		if err != nil || nowE4 <= 0 {
			continue
		}
		then, err := e.st.RatePointOnOrBefore(ctx, cur, from)
		if err != nil || then.RateE4 <= 0 {
			continue
		}
		exposure := doc.CapitalUAH.Major() * share / 100
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
	return Round2(total), why
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
