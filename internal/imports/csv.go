package imports

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

// Читач CSV — поруч із xlsx.go і з тим самим інтерфейсом: файл на
// [][]string, а що означають колонки, вирішує профіль.
//
// ЧОМУ ВЗАГАЛІ CSV. Та сама виписка в більшості брокерів доступна в обох
// виглядах, а CSV ще й єдиний формат, який людина може виправити руками,
// коли в xlsx злиплись клітинки.

// ReadCSV читає таблицю з CSV/TSV.
//
// Роздільник ВИЗНАЧАЄТЬСЯ, а не питається: український експорт однаково
// часто йде через кому, крапку з комою й таб, і питати про це в профілі
// означало б завести поле, у якому помиляються, замість перевірки, яка
// не помиляється. Рахуємо роздільники в першому непорожньому рядку й
// беремо той, якого більше.
//
// Кількість колонок НЕ фіксована (FieldsPerRecord = -1): у виписках
// трапляються короткі рядки-роздільники, і валити на них увесь файл
// означало б відмовитись від виписки через порожній рядок.
func ReadCSV(r io.Reader) ([][]string, error) {
	br := bufio.NewReader(r)
	// BOM: Excel його ставить, і без зняття перша колонка першого рядка
	// приїжджає з невидимим символом на початку — тобто заголовок не
	// збігається ні з чим, а індекси колонок мовчки їдуть.
	if b, err := br.Peek(3); err == nil && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		if _, err := br.Discard(3); err != nil {
			return nil, err
		}
	}
	data, err := io.ReadAll(br)
	if err != nil {
		return nil, err
	}
	text := string(data)
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("порожній файл")
	}
	cr := csv.NewReader(strings.NewReader(text))
	cr.Comma = guessComma(text)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSV не розбирається: %w", err)
	}
	return rows, nil
}

func guessComma(text string) rune {
	line := text
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		line = text[:i]
	}
	best, bestN := ',', strings.Count(line, ",")
	for _, c := range []rune{';', '\t'} {
		if n := strings.Count(line, string(c)); n > bestN {
			best, bestN = c, n
		}
	}
	return best
}
