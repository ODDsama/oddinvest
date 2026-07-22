package imports

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Розбір виписки Inzhur.
//
// Колонки: Дата | Тип операції | Вид цінного паперу | Дебет | Кредит.
// Дебет — гроші, що НАДІЙШЛИ (поповнення, дивіденд, виручка від продажу),
// Кредит — що СПИСАНІ (купівля, податок, виведення).
//
// Податок приходить окремим рядком поруч зі своєю подією, тож він
// підтягується до найближчого дивіденда чи продажу того самого фонду.
// Без цього дохідність рахувалася б до податку, а вона в фондів, на
// відміну від ОВДП, оподаткована.

// Row — одна розібрана операція, готова до запису.
type Row struct {
	Date domain.Date
	Kind string // fund_buy | fund_sell | dividend | deposit | withdrawal
	Fund string
	Qty  int64
	// Amount — завжди додатнє, у мінорних одиницях; напрямок задає Kind.
	Amount int64
	Tax    int64
	Note   string
}

// Skipped — рядок, який не імпортуємо, і чому. Пропуски показуємо, а не
// ковтаємо: тихо загублений рядок виписки — це розбіжність у балансі,
// яку потім шукатимеш руками.
type Skipped struct {
	Date   string
	Op     string
	Reason string
}

type Result struct {
	Rows    []Row
	Skipped []Skipped
}

var (
	certRe = regexp.MustCompile(`^(Купівля|Продаж)\s+([\d\s\x{00a0}]+)\s+сертифікат`)
	bondRe = regexp.MustCompile(`^(Купівля|Продаж)\s+([\d\s\x{00a0}]+)\s+облігаці`)
)

// ParseInzhur розбирає рядки аркуша (перший рядок — заголовок).
func ParseInzhur(rows [][]string) (Result, error) {
	var res Result
	if len(rows) < 2 {
		return res, fmt.Errorf("порожня виписка")
	}
	type pending struct {
		idx  int // індекс у res.Rows
		fund string
		date domain.Date
	}
	var lastTaxable []pending // дивіденди й продажі, до яких може прийти податок

	// Виписка йде від новішого до старішого, а нам потрібен зворотний
	// порядок — і не для краси: податок стоїть окремим рядком ПІСЛЯ своєї
	// події за часом, тож у порядку файлу він трапляється раніше за неї.
	// Та сама хронологія потрібна й далі: собівартість позиції рахується
	// послідовно, і зворотний порядок дав би інший результат.
	type parsed struct {
		date domain.Date
		ord  float64 // серійний номер Excel: у ньому є час, а в даті — ні
		cell func(int) string
	}
	items := make([]parsed, 0, len(rows)-1)
	for _, r := range rows[1:] {
		row := r
		cell := func(i int) string {
			if i < len(row) {
				return row[i]
			}
			return ""
		}
		if strings.TrimSpace(cell(1)) == "" {
			continue
		}
		date, err := excelDate(cell(0))
		if err != nil {
			res.Skipped = append(res.Skipped, Skipped{cell(0), cell(1), "не розпізнав дату"})
			continue
		}
		ord, _ := strconv.ParseFloat(strings.TrimSpace(cell(0)), 64)
		items = append(items, parsed{date, ord, cell})
	}
	// Сортуємо за СЕРІЙНИМ номером, а не за датою: у номері є час, а в
	// даті лише день. Податок і його подія стоять в одному дні з різницею
	// в секунди, тож сортування по днях їх не переставило б, і податок
	// так і лишався б перед подією.
	sort.SliceStable(items, func(i, j int) bool { return items[i].ord < items[j].ord })

	for _, it := range items {
		cell, date := it.cell, it.date
		op, fund := cell(1), cell(2)
		debit, credit := money(cell(3)), money(cell(4))
		skip := func(reason string) {
			res.Skipped = append(res.Skipped, Skipped{string(date), op, reason})
		}

		switch {
		case certRe.MatchString(op):
			m := certRe.FindStringSubmatch(op)
			qty, _ := strconv.ParseInt(digits(m[2]), 10, 64)
			if qty <= 0 {
				skip("не розпізнав кількість сертифікатів")
				continue
			}
			if m[1] == "Купівля" {
				res.Rows = append(res.Rows, Row{Date: date, Kind: "fund_buy", Fund: fund,
					Qty: qty, Amount: credit})
			} else {
				res.Rows = append(res.Rows, Row{Date: date, Kind: "fund_sell", Fund: fund,
					Qty: qty, Amount: debit})
				lastTaxable = append(lastTaxable, pending{len(res.Rows) - 1, fund, date})
			}

		case strings.HasPrefix(op, "Нарахування дивід"):
			res.Rows = append(res.Rows, Row{Date: date, Kind: "dividend", Fund: fund, Amount: debit})
			lastTaxable = append(lastTaxable, pending{len(res.Rows) - 1, fund, date})

		case strings.HasPrefix(op, "Сплата податку"):
			// Прив'язуємо до найсвіжішої оподатковуваної події того ж
			// фонду в межах доби — саме так вони й стоять у виписці.
			attached := false
			for i := len(lastTaxable) - 1; i >= 0; i-- {
				p := lastTaxable[i]
				if p.fund != fund {
					continue
				}
				if d := domain.DaysBetween(p.date, date); d < 0 || d > 1 {
					continue
				}
				res.Rows[p.idx].Tax += credit
				attached = true
				break
			}
			if !attached {
				skip("податок без своєї події — дивіденд чи продаж поза цим файлом")
			}

		case strings.HasPrefix(op, "Поповнення"):
			res.Rows = append(res.Rows, Row{Date: date, Kind: "deposit", Amount: debit, Note: op})

		case strings.HasPrefix(op, "Виведення"):
			res.Rows = append(res.Rows, Row{Date: date, Kind: "withdrawal", Amount: credit, Note: op})

		case bondRe.MatchString(op):
			skip("облігації вносяться вручну")

		case strings.HasPrefix(op, "Конвертація"), strings.HasPrefix(op, "Доплата"):
			// У рядку конвертації немає кількості сертифікатів, тож
			// відновити позицію з нього неможливо — лише сума.
			skip("конвертація без кількості сертифікатів — внеси вручну")

		default:
			skip("невідомий тип операції")
		}
	}
	return res, nil
}

// excelDate перетворює серійний номер Excel на дату. Епоха — 1899-12-30
// (а не 1900-01-01): Excel вважає 1900 високосним, і зсув це враховує.
func excelDate(s string) (domain.Date, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f < 1 {
		// трапляється й звичайна дата
		if d, derr := domain.ParseDate(strings.TrimSpace(s)); derr == nil {
			return d, nil
		}
		return "", fmt.Errorf("не дата: %q", s)
	}
	t := time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(f))
	return domain.NewDate(t), nil
}

// money розбирає «1 234,56» / «1234.56» у мінорні одиниці.
func money(s string) int64 {
	s = strings.NewReplacer(" ", "", " ", "", ",", ".").Replace(strings.TrimSpace(s))
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(math.Round(f * 100))
}

func digits(s string) string {
	var b strings.Builder
	for _, ch := range s {
		if ch >= '0' && ch <= '9' {
			b.WriteRune(ch)
		}
	}
	return b.String()
}
