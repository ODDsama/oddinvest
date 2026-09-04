package domain

import (
	"fmt"
	"sort"
	"strconv"
)

// Найгірший рух курсу, який СПРАВДІ був, — і межа, яка робить його
// прийнятним: тут немає жодного сценарію, тільки вимір.
//
// Десять років курсів НБУ лежать у базі з першого дня (fx_rates) і
// читались двома способами: крайні точки для знецінення (deval.go) і
// місце сьогоднішнього курсу серед історії (fxwindow.go). Питання «що з
// ЦИМ портфелем зробив би рух, який уже одного разу стався» не спиралось
// на них ніяк — а відповідь лежить за один прохід по тому самому ряду.
//
// ЧОМУ ЖОДЕН ЕПІЗОД НЕ ЗАШИТИЙ. Спокуса назвати «2014» і «2022»
// константами очевидна: обидва рухи пам'ятають без бази. Але зашита дата
// застаріває мовчки — наступний стрибок не додасться сам, і застосунок
// показував би «найгірше, що було» для історії, яка скінчилась у рік
// написання коду. Тому найгірше вікно ШУКАЄТЬСЯ в тому, що є, і
// називається справжніми датами: перепишеться саме, щойно історія
// перепишеться.
//
// МЕЖА, ЯКУ ЦЕЙ ФАЙЛ НЕ ПЕРЕХОДИТЬ — та сама, що описана довгим абзацом
// у fxwindow.go, і повторювати її тут нема потреби: ані «рекомендації»,
// ані порога, ані сигналу. Рух у минулому — твердження про минуле.
// Гривня падає стрибками, і найгірший рух за десять років був найгіршим
// рівно до наступного разу.
//
// ЛИШЕ РУХ УГОРУ. Дзеркальний бік («а якби гривня зміцнилась») ніхто не
// питає, а показаний поруч він читався б як половина поради. Шок ставить
// одне питання — чи витримає портфель те, що вже бувало.

// FXPoint — курс на дату в термінах domain.
//
// Копія форми store.RatePoint, і це не недогляд: domain про сховище не
// знає (той самий поділ, що змушує FXPlace брати голий зріз int64, а
// відбір за датою лишати викликачеві). Переклад робить шар api.
type FXPoint struct {
	Date   Date
	RateE4 int64
}

// FXMove — ВИМІРЯНИЙ рух курсу: дві справжні точки й відсоток між ними.
//
// From/To — дати, які реально є в ряду, а не межі запитаного вікна:
// число, яке стоїть поруч із фактичними курсами, саме мусить бути
// фактом (той самий довід, що при MedianE4 у fxwindow.go).
type FXMove struct {
	Months       int
	From, To     Date
	FromE4, ToE4 int64
	// Pct — (To/From − 1) × 100. Додатний = гривня подешевшала.
	Pct float64
}

// minFXWindows — скільки вікон-кандидатів мусить бути, щоб «найгірше»
// щось означало.
//
// Той самий довід, що при minFXPoints у сусідньому файлі, лише про іншу
// величину: максимум із трьох кандидатів — не максимум, а монетка, яка
// виглядає такою ж точною, як максимум зі ста. Дванадцять — це рік
// спостережень на місячній сітці.
const minFXWindows = 12

// monthKey — місяць точки, "YYYY-MM". Порожньо для зіпсованої дати.
func monthKey(d Date) string {
	if len(d) < 7 {
		return ""
	}
	return string(d)[:7]
}

// shiftMonth зсуває "YYYY-MM" на n місяців.
//
// Арифметика по ключу, а НЕ Date.AddMonths: календарне додавання до
// 31 січня дає 3 березня, і вікно «один місяць» мовчки стало б довшим.
// Тут же місяць — просто число, і зсув точний за побудовою.
func shiftMonth(key string, n int) string {
	if len(key) < 7 {
		return ""
	}
	y, errY := strconv.Atoi(key[:4])
	m, errM := strconv.Atoi(key[5:7])
	if errY != nil || errM != nil || m < 1 || m > 12 {
		return ""
	}
	t := y*12 + (m - 1) + n
	if t < 0 {
		return ""
	}
	return fmt.Sprintf("%04d-%02d", t/12, t%12+1)
}

// monthsBetween — скільки місяців між двома ключами "YYYY-MM".
func monthsBetween(a, b string) int {
	if len(a) < 7 || len(b) < 7 {
		return 0
	}
	ay, errAY := strconv.Atoi(a[:4])
	am, errAM := strconv.Atoi(a[5:7])
	by, errBY := strconv.Atoi(b[:4])
	bm, errBM := strconv.Atoi(b[5:7])
	if errAY != nil || errAM != nil || errBY != nil || errBM != nil {
		return 0
	}
	return (by*12 + bm) - (ay*12 + am)
}

// MonthlyFX зводить ряд до ОДНІЄЇ точки на місяць — найранішої в місяці.
//
// НАВІЩО НОРМАЛІЗАЦІЯ. Історію заливає backfill по одній точці на місяць
// (перше число, jobs.BackfillRates), а добова джоба додає по точці на
// день — тобто густота ряду різна в різні епохи. Без зведення «рух за
// місяць» міряв би в старій частині відстань між першими числами, а в
// новій — між двома довільними днями, і останні роки виглядали б
// стрибкішими рівно тому, що їх частіше питали. Це була б властивість
// вимірювання, видана за властивість гривні.
//
// Найраніша точка місяця, а не середня: середнє не було курсом жодного
// дня, а тут воно стоятиме поруч зі справжніми датами.
//
// Ідемпотентна: другий виклик на власному результаті нічого не змінює,
// і саме тому функції нижче кличуть її самі, не покладаючись на
// дисципліну викликача.
func MonthlyFX(points []FXPoint) []FXPoint {
	first := make(map[string]FXPoint, len(points))
	for _, p := range points {
		k := monthKey(p.Date)
		if k == "" || p.RateE4 <= 0 {
			continue
		}
		if cur, have := first[k]; !have || p.Date.Before(cur.Date) {
			first[k] = p
		}
	}
	out := make([]FXPoint, 0, len(first))
	for _, p := range first {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// index — місячний ряд, доступний за ключем місяця.
func index(monthly []FXPoint) map[string]FXPoint {
	idx := make(map[string]FXPoint, len(monthly))
	for _, p := range monthly {
		if k := monthKey(p.Date); k != "" {
			idx[k] = p
		}
	}
	return idx
}

// WorstFXMove — найбільший рух УГОРУ рівно за months місяців сітки.
//
// Пара мусить стояти рівно за months місяців: якщо місяця в ряду немає
// (НБУ не котирував, backfill пропустив), пара просто не складається.
// Взяти замість неї сусідній місяць означало б назвати тримісячним рухом
// те, що сталось за чотири, — помилка тиха й правдоподібна.
//
// ok=false, коли кандидатів менше за minFXWindows або жодного руху вгору
// в ряду немає. Тоді викликач мусить не показати нічого й назвати
// причину, а не намалювати нулі.
func WorstFXMove(monthly []FXPoint, months int) (FXMove, bool) {
	if months <= 0 {
		return FXMove{}, false
	}
	m := MonthlyFX(monthly)
	idx := index(m)

	var best FXMove
	windows, found := 0, false
	for _, p := range m {
		q, ok := idx[shiftMonth(monthKey(p.Date), months)]
		if !ok {
			continue
		}
		windows++
		pct := (float64(q.RateE4)/float64(p.RateE4) - 1) * 100
		if !found || pct > best.Pct {
			best = FXMove{Months: months, From: p.Date, To: q.Date,
				FromE4: p.RateE4, ToE4: q.RateE4, Pct: pct}
			found = true
		}
	}
	if windows < minFXWindows || !found || best.Pct <= 0 {
		return FXMove{}, false
	}
	return best, true
}

// FXMoveOver — рух цієї валюти за НАЗВАНИМ вікном.
//
// Потрібна для співруху: коли найгірше вікно знайдене по якірній валюті,
// решта міряється за ТИМИ САМИМИ датами, а не власними найгіршими.
// Скласти найгірший місяць долара з найгіршим місяцем євро означало б
// зібрати спільну подію, якої не було, — і видати її за виміряну.
//
// Порога minFXWindows тут немає навмисно: він стереже ВИБІР максимуму
// («максимум із трьох — монетка»), а тут нічого не вибирається — вікно
// назване ззовні, і відповідь на нього або є в ряду, або її немає.
func FXMoveOver(monthly []FXPoint, from, to Date) (FXMove, bool) {
	idx := index(MonthlyFX(monthly))
	p, okFrom := idx[monthKey(from)]
	q, okTo := idx[monthKey(to)]
	if !okFrom || !okTo {
		return FXMove{}, false
	}
	return FXMove{
		Months: monthsBetween(monthKey(p.Date), monthKey(q.Date)),
		From:   p.Date, To: q.Date,
		FromE4: p.RateE4, ToE4: q.RateE4,
		Pct: (float64(q.RateE4)/float64(p.RateE4) - 1) * 100,
	}, true
}
