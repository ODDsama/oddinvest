package domain

import (
	"fmt"
	"time"
)

// Date — календарна дата у форматі ISO-8601 (YYYY-MM-DD).
// Порівнюється лексикографічно, зберігається в БД як TEXT —
// портабельно між SQLite і Postgres.
type Date string

const dateLayout = "2006-01-02"

func NewDate(t time.Time) Date { return Date(t.Format(dateLayout)) }

func ParseDate(s string) (Date, error) {
	if _, err := time.Parse(dateLayout, s); err != nil {
		return "", fmt.Errorf("невалідна дата %q: %w", s, err)
	}
	return Date(s), nil
}

func (d Date) Time() time.Time {
	t, _ := time.Parse(dateLayout, string(d)) //nolint:errcheck // Date перевіряється при створенні; нульовий час тут і є відповідь
	return t
}

func (d Date) Valid() bool {
	_, err := time.Parse(dateLayout, string(d))
	return err == nil
}

func (d Date) Year() int          { return d.Time().Year() }
func (d Date) Month() time.Month  { return d.Time().Month() }
func (d Date) Day() int           { return d.Time().Day() }
func (d Date) Before(o Date) bool { return d < o }
func (d Date) After(o Date) bool  { return d > o }

// DaysBetween — кількість днів від a до b (ACT).
func DaysBetween(a, b Date) int {
	return int(b.Time().Sub(a.Time()).Hours() / 24)
}

// MonthsBetween — номер місяця, у який потрапляє b, якщо рахувати від a.
// День місяця не враховується: 31 січня і 1 січня дають той самий номер.
//
// Саме так проєкція розкладає майбутні потоки по кроках симуляції, і
// саме тому не через DaysBetween/30 — сітка тут календарна, а не
// тридцятиденна. Спільна функція, бо номер місяця рахується в двох
// місцях: для купонів і для дати закриття фонду, а розбіжність між ними
// поклала б гроші фонду в сусідній крок.
func MonthsBetween(a, b Date) int {
	return (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month())
}

func (d Date) AddDays(n int) Date {
	return NewDate(d.Time().AddDate(0, 0, n))
}

// AddMonths зсуває дату на n місяців зі звичайною Go-семантикою
// переповнення (31 січня + 1 міс = 2 або 3 березня). Годиться для
// горизонтів і вікон («рік наперед»), де день не важить. Для графіків
// «того самого числа щомісяця» — AddMonthsClamp: тут вклад від 31-го
// дрейфував на третє число й губив лютневу виплату.
func (d Date) AddMonths(n int) Date {
	return NewDate(d.Time().AddDate(0, n, 0))
}

// AddMonthsClamp — те саме, але день притискається до кінця місяця: 31
// січня + 1 міс = 28 (29) лютого, + 2 міс = 31 березня. Для графіків, що
// платять «того самого числа щомісяця»: банк, відкривши вклад 31-го,
// платить в останній день коротшого місяця, а не третього числа
// наступного. Рахувати завжди від ПОЧАТКОВОЇ дати (d.AddMonthsClamp(k)),
// не ланцюжком — інакше після лютого 31-ше стало б 28-м назавжди.
func (d Date) AddMonthsClamp(n int) Date {
	t := d.Time()
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, n, 0)
	last := first.AddDate(0, 1, -1).Day()
	day := t.Day()
	if day > last {
		day = last
	}
	return NewDate(time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC))
}
