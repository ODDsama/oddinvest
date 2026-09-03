package api

import (
	"fmt"
	"strings"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/store"
)

// Покриття даних по фондах.
//
// Купони ОВДП картка знає з довідника НБУ: досить завести лот, і графік
// приходить сам. Дивіденди фонду так не приходять — вони потрапляють у базу
// лише з виписки, яку хтось імпортував. Тому картка може знати про рік
// рівно два місяці й показати їх як рік, нічим не виказавши різниці. Саме
// це й сталось: у журналі фондів найраніший запис 04.06.2026, за 2026-й
// показувалось 76,05 грн дивідендів, а картки за 2023-2025 стояли порожні —
// не «даних немає», а саме порожні, ніби доходу не було.
//
// Числа за минуле полагодить імпорт старих виписок; код мусить полагодити
// інше — картка не має права мовчати про межу власного знання.

// fundGap — фонд і місяці, у яких виплата мала бути, а запису немає.
type fundGap struct {
	Fund   string   `json:"fund"`
	Months []string `json:"months"`
}

// fundCoverage — межа даних по фондах і дірки всередині неї.
//
// Два різні твердження, і плутати їх не можна. Примітка каже, ЧОГО КАРТКА
// НЕ БАЧИЛА ВЗАГАЛІ: період раніший за перший запис у журналі. Дірки
// кажуть про місяці ВСЕРЕДИНІ покриття, де фонд мав платити (позиція є,
// день виплати відомий і вже минув), а запису немає — тобто виписку
// заведено не повністю.
//
// Порожній журнал не породжує нічого: у того, хто не має фондів, немає й
// межі, про яку варто говорити. Тривога на порожньому місці зробила б
// примітку шумом, який перестають читати.
func fundCoverage(ops []domain.FundOp, refs []store.Fund, from, to, today domain.Date) (string, []fundGap) {
	if len(ops) == 0 {
		return "", nil
	}
	earliest := ops[0].Date
	for _, op := range ops {
		if op.Date.Before(earliest) {
			earliest = op.Date
		}
	}

	note := ""
	switch {
	case to.Before(earliest):
		note = fmt.Sprintf("по фондах за цей період записів немає: журнал починається з %s", human(earliest))
	case from.Before(earliest):
		note = fmt.Sprintf("по фондах дані з %s — за раніші місяці виписку не заведено", human(earliest))
	}

	// Дірки шукаємо лише там, де журнал уже мав би бути повним, і лише за
	// минулі виплати: за майбутню претензій бути не може.
	start, end := from, to
	if start.Before(earliest) {
		start = earliest
	}
	if end.After(today) {
		end = today
	}
	if end.Before(start) {
		return note, nil
	}

	// Дивіденд шукаємо по (фонд, місяць), а не по точній даті: банк платить
	// «близько десятого», і вимагати збіг день у день означало б рапортувати
	// дірку там, де виплата просто зсунулась на вихідні.
	paid := map[string]bool{}
	for _, op := range ops {
		if op.Kind == domain.FundDividend {
			paid[op.Fund+"|"+string(op.Date)[:7]] = true
		}
	}

	var gaps []fundGap
	for _, ref := range refs {
		// Накопичувальний фонд не платить нічого — його дохід сидить у ціні
		// сертифіката. Та сама перевірка, що в календарі (state_schedule.go).
		// PayoutDay нуль означає «день невідомий», а не «перше число»:
		// вигадати його тут — це вигадати й дірки.
		if ref.Kind == store.FundAccumulating || ref.PayoutDay <= 0 {
			continue
		}
		var months []string
		for m := monthStart(start, 0); !m.After(end); m = m.AddMonths(1) {
			pay := payoutDate(m, int(ref.PayoutDay))
			if pay.Before(start) || pay.After(end) {
				continue
			}
			if paid[ref.Name+"|"+string(pay)[:7]] {
				continue
			}
			// Позиція на день виплати — з тих самих операцій, якими живе
			// решта фондової арифметики. Нема сертифікатів — нема й претензії.
			if heldOn(ops, ref.Name, pay) <= 0 {
				continue
			}
			// І фонд мали ВЖЕ НА ПОПЕРЕДНЮ виплату, тобто протримали
			// повний цикл. Фонд платить за реєстром, складеним раніше за
			// день виплати, тож той, хто зайшов усередині місяця, законно
			// не отримує нічого — і претензія до нього була б хибною
			// тривогою на кожному вході.
			//
			// Це не здогад: у виписці власника сертифікати Inzhur REIT
			// куплені 4 і 5 червня 2026-го, червневої виплати немає, а
			// перша прийшла 10 липня. Перша версія цієї перевірки саме
			// той червень і позначила діркою.
			if heldOn(ops, ref.Name, payoutDate(m.AddMonths(-1), int(ref.PayoutDay))) <= 0 {
				continue
			}
			months = append(months, string(pay)[:7])
		}
		if len(months) > 0 {
			gaps = append(gaps, fundGap{Fund: ref.Name, Months: months})
		}
	}
	return note, gaps
}

// payoutDate — день виплати в місяці m, підтягнутий до кінця короткого
// місяця: тридцяте число в лютому — це 28-ме, а не третє березня, і
// звичайне переповнення Go тут поставило б дірку не в той місяць.
func payoutDate(m domain.Date, day int) domain.Date {
	last := m.AddMonths(1).AddDays(-1).Day()
	if day > last {
		day = last
	}
	return domain.Date(fmt.Sprintf("%s-%02d", string(m)[:7], day))
}

// heldOn — скільки сертифікатів фонду було на дату, за журналом операцій.
func heldOn(ops []domain.FundOp, fund string, on domain.Date) int64 {
	upTo := make([]domain.FundOp, 0, len(ops))
	for _, op := range ops {
		if !op.Date.After(on) {
			upTo = append(upTo, op)
		}
	}
	p := domain.FundPositions(upTo, nil)[fund]
	if p == nil {
		return 0
	}
	return p.Qty
}

// human — дата словами для примітки: 04.06.2026 читається як дата, а
// 2026-06-04 посеред речення — як номер версії.
func human(d domain.Date) string {
	t := d.Time()
	return t.Format("02.01.2006")
}

// joinNotes зшиває примітки про курс і про покриття в одне поле. Крапка з
// комою, а не новий рядок: поле показується одним абзацом.
func joinNotes(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "; ")
}
