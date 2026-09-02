// Віхи боргу — пʼять, і всі вони МОВЧАТЬ на портфелі без боргу.
//
// Мовчать, а не стоять незібраними: «зібрано 15 із 21» у людини, яка
// ніколи не була винна, читалось би як докір за те, чого не було. Тому
// без єдиного боргу в історії кожна з пʼяти має progressNoProgress і
// примітку «боргу не було» — pickNext такі не бачить, а рівень їх не
// рахує (ProgressPct −1 і Earned false — стан «не про тебе»).
//
// Дати — зі звірок і зі знімків, і жодна не вигадана:
//   - нуль на картці датований звіркою, яка ПОЧАЛА нинішній
//     невідʼємний відрізок (не першою невідʼємною в історії: картка,
//     що вийшла в плюс і знову провалилась, отримала б дату з минулого
//     життя);
//   - чистий капітал над нулем датований знімком за правилом міграції
//     0048 — нуль у старому рядку означає «тоді не рахували», а не
//     «чистий капітал був нулем», і такі рядки пропускаються цілком.
package api

import (
	"fmt"
	"sort"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// debtMilestones — пʼять віх боргу в сталому порядку.
func debtMilestones(doc *state.Doc, src *sources, snaps []store.Snapshot, today domain.Date) []milestone {
	silent := func(key, title string) milestone {
		return milestone{Key: key, Title: title, ProgressPct: progressNoProgress,
			Note: "боргу не було — віха не про тебе"}
	}
	if len(src.debts) == 0 {
		return []milestone{
			silent("net_worth_positive", "Чистий капітал над нулем"),
			silent("card_zero", "Картка в нулі"),
			silent("exit_by_met", "Вийшов із ліміту до дати"),
			silent("debt_covered", "Подушка перекриває борг"),
			silent("installments_done", "Розстрочки закриті"),
		}
	}
	var out []milestone

	// --- Чистий капітал над нулем ---
	//
	// Найбільша подія для того, хто в ямі: капітал більший за все, що
	// винен. Відстань — частка капіталу від боргу: при чистому −107 тис.
	// і капіталі 75 тис. пройдено 41 %.
	out = append(out, func() milestone {
		cap0, nw := capitalNow(doc), doc.NetWorthUAH
		m := milestone{Key: "net_worth_positive", Title: "Чистий капітал над нулем",
			Earned: nw > 0, EarnedOn: netWorthPositiveOn(snaps)}
		if m.Earned {
			m.Note = "чистий капітал " + uah(nw)
			m.ProgressPct = 100
			return m
		}
		owed := cap0 - nw
		m.ProgressPct = ratioPct(cap0, owed)
		m.Note = fmt.Sprintf("капітал %s проти боргу %s", uah(cap0), uah(owed))
		m.Left = "лишилось " + uah(-nw) + " боргу понад капітал"
		return m
	}())

	// --- Картка в нулі ---
	cards := openCards(src)
	out = append(out, func() milestone {
		m := milestone{Key: "card_zero", Title: "Картка в нулі", ProgressPct: progressNoProgress,
			Note: "відкритих карток немає"}
		if len(cards) == 0 {
			return m
		}
		best, bestPct, when := "", progressNoProgress, ""
		for _, c := range cards {
			st := domain.CardState(c.d, src.debtMarks, src.debtOps, src.debts, today)
			if st.Known && st.Debt == 0 {
				m.Earned = true
				if d := zeroRunStart(c.d, src.debtMarks, today); when == "" || (d != "" && d < when) {
					when = d
				}
				continue
			}
			// Відстань — частка ліміту, що лишається невибраною; без
			// ліміту міряти нічим.
			if c.d.LimitAmount > 0 {
				if p := ratioPct(float64(c.d.LimitAmount-st.Debt), float64(c.d.LimitAmount)); p > bestPct {
					best, bestPct = c.d.Name, p
				}
			}
		}
		if m.Earned {
			m.ProgressPct, m.EarnedOn = 100, when
			m.Note = "хоч одна картка без боргу"
			return m
		}
		m.ProgressPct = bestPct
		if best != "" {
			m.Note = fmt.Sprintf("найближча — «%s»", best)
			m.Left = "лишилось вибратись із мінуса"
		} else {
			m.Note = "ліміт не заданий жодній картці — міряти нічим"
		}
		return m
	}())

	// --- Вийшов із ліміту до дати ---
	//
	// Дати не діляться, тож відсотка тут немає: або встиг, або ні.
	// Вимога — КОЖНА картка з названою датою вийшла в нуль не пізніше
	// за неї; дата віхи — коли вийшла остання.
	out = append(out, func() milestone {
		m := milestone{Key: "exit_by_met", Title: "Вийшов із ліміту до дати",
			ProgressPct: progressNoProgress, Note: "дату виходу не названо"}
		named := 0
		met, when := true, ""
		for _, c := range cards {
			if c.d.ExitBy == "" {
				continue
			}
			named++
			st := domain.CardState(c.d, src.debtMarks, src.debtOps, src.debts, today)
			z := zeroRunStart(c.d, src.debtMarks, today)
			if !(st.Known && st.Debt == 0) || z == "" || z > string(c.d.ExitBy) {
				met = false
				continue
			}
			if z > when {
				when = z
			}
		}
		if named == 0 {
			return m
		}
		if met {
			m.Earned, m.ProgressPct, m.EarnedOn = true, 100, when
			m.Note = "усі картки з датою вийшли в нуль вчасно"
			return m
		}
		m.Note = "картки з датою виходу ще в мінусі"
		return m
	}())

	// --- Подушка перекриває борг ---
	out = append(out, func() milestone {
		m := milestone{Key: "debt_covered", Title: "Подушка перекриває борг",
			ProgressPct: progressNoProgress, Note: "боргу, який треба перекривати, немає"}
		r := doc.Reserve
		if r == nil || r.DebtCoverUAH <= 0 {
			return m
		}
		m.Earned = r.DebtCoverGapUAH <= 0
		m.ProgressPct = ratioPct(r.DebtCoverUAH-r.DebtCoverGapUAH, r.DebtCoverUAH)
		m.Note = fmt.Sprintf("майбутні платежі %s, подушка покриває %s",
			uah(r.DebtCoverUAH), uah(r.DebtCoverUAH-r.DebtCoverGapUAH))
		if !m.Earned {
			m.Left = "лишилось " + uah(r.DebtCoverGapUAH) + " до покриття"
		}
		return m
	}())

	// --- Розстрочки закриті ---
	out = append(out, func() milestone {
		m := milestone{Key: "installments_done", Title: "Розстрочки закриті",
			ProgressPct: progressNoProgress, Note: "розстрочок не було"}
		total, done, when := 0, 0, ""
		for _, d := range src.debts {
			if d.IsCard() {
				continue
			}
			total++
			end := installmentEnd(d)
			if d.Closed() || (end != "" && end.Before(today)) {
				done++
				if string(end) > when {
					when = string(end)
				}
			}
		}
		if total == 0 {
			return m
		}
		m.Earned = done == total
		m.ProgressPct = ratioPct(float64(done), float64(total))
		m.Note = fmt.Sprintf("закрито %d із %d", done, total)
		if m.Earned {
			m.EarnedOn = when
		} else {
			m.Left = fmt.Sprintf("лишилось %d", total-done)
		}
		return m
	}())

	return out
}

type openCard struct{ d domain.Debt }

func openCards(src *sources) []openCard {
	var out []openCard
	for _, d := range src.debts {
		if d.IsCard() && !d.Closed() {
			out = append(out, openCard{d})
		}
	}
	return out
}

// zeroRunStart — дата звірки, з якої почався НИНІШНІЙ невідʼємний
// відрізок балансу картки; порожньо, коли остання звірка в мінусі або
// звірок немає.
func zeroRunStart(card domain.Debt, marks []domain.DebtMark, today domain.Date) string {
	var own []domain.DebtMark
	for _, m := range marks {
		if m.DebtID == card.ID && !m.Date.After(today) {
			own = append(own, m)
		}
	}
	sort.Slice(own, func(i, j int) bool { return own[i].Date < own[j].Date })
	start := ""
	for _, m := range own {
		if m.Balance < 0 {
			start = ""
			continue
		}
		if start == "" {
			start = string(m.Date)
		}
	}
	return start
}

// installmentEnd — дата останнього платежу розстрочки: закриття, якщо
// названо, інакше кінець графіка; порожньо без графіка.
func installmentEnd(d domain.Debt) domain.Date {
	if d.ClosedDate != "" {
		return d.ClosedDate
	}
	sched := domain.InstallmentSchedule(d)
	if len(sched) == 0 {
		return ""
	}
	return sched[len(sched)-1].Date
}

// netWorthPositiveOn — перший знімок із чистим капіталом над нулем ПІСЛЯ
// знімка з мінусом. Нулі пропускаються: за міграцією 0048 нуль у старому
// рядку означає «тоді не рахували». Порожньо, коли мінусу в історії не
// було (нема з чого виходити) або вихід ще не стався.
func netWorthPositiveOn(snaps []store.Snapshot) string {
	sorted := append([]store.Snapshot(nil), snaps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Date < sorted[j].Date })
	wasNegative := false
	for _, sn := range sorted {
		switch {
		case sn.NetWorthUAH < 0:
			wasNegative = true
		case sn.NetWorthUAH > 0 && wasNegative:
			return string(sn.Date)
		}
	}
	return ""
}
