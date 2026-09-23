package imports

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// Виписка — файл ззовні, і парсер не має права впасти на жодному байті:
// сторінка імпорту чекає або рядків, або пояснення, чого бракує. Seed-и
// ганяються кожним `go test`; довгий пошук — вручну:
//
//	go test ./internal/imports -run '^$' -fuzz FuzzReadCSV -fuzztime 60s
func FuzzReadCSV(f *testing.F) {
	f.Add([]byte("Дата;Операція;Сума\n01.09.2026;Поповнення;1000,00\n"))
	f.Add([]byte("\xef\xbb\xbfa,b\n\"не закрита лапка\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		rows, err := ReadCSV(bytes.NewReader(data))
		if err != nil {
			return
		}
		// Ні Inzhur, ні профіль не мають падати на будь-яких рядках.
		_, _ = ParseInzhur(rows)   //nolint:errcheck // перевіряємо лише відсутність паніки
		_, _ = Parse(rows, mono()) //nolint:errcheck // те саме
	})
}

// XLSX — zip усередині: обрізаний архів, «бомба» чи сміття мусять давати
// помилку, а не паніку.
func FuzzReadXLSX(f *testing.F) {
	f.Add([]byte("PK\x03\x04"))
	f.Add([]byte("не архів зовсім"))
	f.Fuzz(func(t *testing.T, data []byte) {
		rows, err := ReadXLSX(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		_, _ = ParseInzhur(rows) //nolint:errcheck // перевіряємо лише відсутність паніки
	})
}

// xlsxWith — мінімальна книга з одним аркушем заданого XML.
func xlsxWith(t *testing.T, sheet string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(sheet)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Посилання на клітинку поза межами аркуша Excel (колонок не більше XFD =
// 16 384) — помилка файла, а не спроба виділити рядок на сотні мільярдів
// клітинок. Знайдено fuzz-ом: FuzzReadXLSX застигав на такому вході, бо
// colIndex рахував номер колонки без межі, і make([]string, width) міг
// поставити сервіс на коліна одним файлом виписки.
func TestReadXLSXRejectsCellBeyondSheet(t *testing.T) {
	data := xlsxWith(t, `<worksheet><sheetData><row><c r="ZZZZZZZZ1"><v>1</v></c></row></sheetData></worksheet>`)
	if _, err := ReadXLSX(bytes.NewReader(data), int64(len(data))); err == nil {
		t.Fatal("клітинка ZZZZZZZZ1 мала дати помилку")
	}
	// Останній законний стовпець — працює.
	data = xlsxWith(t, `<worksheet><sheetData><row><c r="XFD1"><v>1</v></c></row></sheetData></worksheet>`)
	rows, err := ReadXLSX(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(rows) != 1 || len(rows[0]) != 16384 || rows[0][16383] != "1" {
		t.Fatalf("XFD1: %v, ширина %d", err, len(rows[0]))
	}
}

// Багато рядків із далекою клітинкою — теж відмова, а не мільйони комірок
// у памʼяті: стеля на весь аркуш (maxXLSXCells).
func TestReadXLSXRejectsHugeSheet(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("<worksheet><sheetData>")
	for i := 0; i < 200; i++ {
		sb.WriteString(`<row><c r="XFD1"><v>1</v></c></row>`)
	}
	sb.WriteString("</sheetData></worksheet>")
	data := xlsxWith(t, sb.String())
	if _, err := ReadXLSX(bytes.NewReader(data), int64(len(data))); err == nil {
		t.Fatal("200 рядків × 16 384 клітинки мали дати відмову")
	}
}
