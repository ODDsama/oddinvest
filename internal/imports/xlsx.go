// Package imports — розбір брокерських виписок.
//
// Читання файлу і розуміння його змісту навмисно розділені: xlsx.go знає
// лише про zip і XML, inzhur.go — лише про рядки таблиці. Завдяки цьому
// правила розбору тестуються без жодного файлу, а підтримка іншого
// брокера чи формату не тягне за собою переписування читача.
package imports

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// ReadXLSX повертає перший аркуш книги як рядки клітинок.
//
// Власний читач замість бібліотеки: потрібне тут — це zip, два XML і
// таблиця спільних рядків, тобто сотня рядків коду. Залежність на кілька
// мегабайтів заради цього не окупається, а формат виписки стабільний.
func ReadXLSX(r io.ReaderAt, size int64) ([][]string, error) {
	z, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("не схоже на xlsx: %w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range z.File {
		files[f.Name] = f
	}

	// Рядки можуть жити в спільній таблиці або просто в клітинці —
	// різні експортери роблять по-різному, тож підтримуємо обидва.
	var shared []string
	if f := files["xl/sharedStrings.xml"]; f != nil {
		var doc struct {
			SI []struct {
				T []string `xml:"t"`
			} `xml:"si"`
		}
		if err := readXML(f, &doc); err != nil {
			return nil, err
		}
		for _, si := range doc.SI {
			shared = append(shared, strings.Join(si.T, ""))
		}
	}

	sheet := files["xl/worksheets/sheet1.xml"]
	if sheet == nil {
		return nil, fmt.Errorf("у книзі немає аркуша")
	}
	var doc struct {
		Rows []struct {
			Cells []struct {
				Ref  string   `xml:"r,attr"`
				Type string   `xml:"t,attr"`
				V    string   `xml:"v"`
				IS   []string `xml:"is>t"`
			} `xml:"c"`
		} `xml:"sheetData>row"`
	}
	if err := readXML(sheet, &doc); err != nil {
		return nil, err
	}

	out := make([][]string, 0, len(doc.Rows))
	for _, row := range doc.Rows {
		cells := map[int]string{}
		width := 0
		for _, c := range row.Cells {
			var txt string
			switch c.Type {
			case "s":
				if i, err := strconv.Atoi(c.V); err == nil && i >= 0 && i < len(shared) {
					txt = shared[i]
				}
			case "inlineStr":
				txt = strings.Join(c.IS, "")
			default:
				txt = c.V
			}
			i := colIndex(c.Ref)
			cells[i] = strings.TrimSpace(txt)
			if i+1 > width {
				width = i + 1
			}
		}
		line := make([]string, width)
		for i := range line {
			line[i] = cells[i]
		}
		out = append(out, line)
	}
	return out, nil
}

func readXML(f *zip.File, v any) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return xml.NewDecoder(rc).Decode(v)
}

var refRe = regexp.MustCompile(`^([A-Z]+)`)

// colIndex перетворює «C7» на 2. Порожнє чи дивне посилання — нульова
// колонка: краще зсув, ніж паніка на чужому файлі.
func colIndex(ref string) int {
	m := refRe.FindStringSubmatch(ref)
	if m == nil {
		return 0
	}
	n := 0
	for _, ch := range m[1] {
		n = n*26 + int(ch-'A') + 1
	}
	return n - 1
}
