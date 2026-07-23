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
	t, _ := time.Parse(dateLayout, string(d))
	return t
}

func (d Date) Valid() bool {
	_, err := time.Parse(dateLayout, string(d))
	return err == nil
}

func (d Date) Year() int          { return d.Time().Year() }
func (d Date) Month() time.Month  { return d.Time().Month() }
func (d Date) Before(o Date) bool { return d < o }
func (d Date) After(o Date) bool  { return d > o }

// DaysBetween — кількість днів від a до b (ACT).
func DaysBetween(a, b Date) int {
	return int(b.Time().Sub(a.Time()).Hours() / 24)
}

func (d Date) AddDays(n int) Date {
	return NewDate(d.Time().AddDate(0, 0, n))
}

// AddMonths зсуває дату на n місяців. Використовує звичайну Go-семантику
// переповнення (31 січня + 1 міс = 2 або 3 березня): для графіка виплат
// вкладу це не має значення — банк платить у ту саму календарну дату
// щомісяця, а точний день переповнення на суму не впливає.
func (d Date) AddMonths(n int) Date {
	return NewDate(d.Time().AddDate(0, n, 0))
}
