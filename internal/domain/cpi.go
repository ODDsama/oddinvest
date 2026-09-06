package domain

import "math"

// Індекс споживчих цін — друга лінійка реальності поруч зі знеціненням.
//
// ЩО ЦЕ МІРЯЄ І ЧОГО НЕ МІРЯЄ. Знецінення (deval.go) відповідає на
// питання «скільки доларів купить моя гривня», ІСЦ — «скільки товарів».
// В Україні ці два числа розходяться на роках, і жодне з них не є
// поправкою до іншого: 2022-го ціни виросли на 26.6% при девальвації 34%,
// а 2019-го гривня зміцніла на 14% при інфляції 4.1%. Тому вони НЕ
// складаються й ніде не складені — це дві альтернативні лінійки, і кожне
// реальне число мусить казати, якою з них воно міряне.
//
// ДЖЕРЕЛО — місячні зміни, опубліковані НБУ (nbu.Inflation). Рівень цін
// звідти не приходить узагалі, він виводиться ланцюжком, і саме тому
// живе тут, а не в колонці бази: збережений рівень розійшовся б із
// перерахованим мовчки.

// CPIPoint — опублікована пара за звітний місяць. Двійник store.CPIPoint
// навмисно: domain не знає про сховище, а сховище не знає про ланцюги.
type CPIPoint struct {
	Period string // 'YYYY-MM'
	MoMBP  int64  // % до попереднього місяця × 100
	YoYBP  int64  // % до того самого місяця торік × 100
}

// CPILevel — рівень цін на кінець місяця.
//
// БАЗА — ПЕРША ТОЧКА РЯДУ, її рівень 1.0 за побудовою. Індекс без
// названої бази — число ні про що, тож окремої «офіційної» бази (2010=100
// абощо) тут немає навмисно: ряд у базі починається там, де його дістав
// бекфіл, і прив'язка до чужої бази лише вдавала б, що ми знаємо ціни до
// неї.
type CPILevel struct {
	Period string
	Level  float64
}

// CPIChain перемножує місячні зміни в рівень цін.
//
// Порядок вхідних точок — за зростанням періоду (так їх і віддає
// store.CPISince). Дірка в ряду не заповнюється й не оголошується
// помилкою: місячна зміна пропущеного місяця просто не множиться, і
// ланцюг занижує рівень рівно на неї. Ловить це не тут, а звірка з
// опублікованим річним темпом (CPIYoYDrift) — дірка видно там числом, а
// не припущенням.
func CPIChain(points []CPIPoint) []CPILevel {
	if len(points) == 0 {
		return nil
	}
	out := make([]CPILevel, 0, len(points))
	level := 1.0
	for i, p := range points {
		if i > 0 {
			level *= 1 + float64(p.MoMBP)/10000
		}
		out = append(out, CPILevel{Period: p.Period, Level: level})
	}
	return out
}

// CPIAnnualPct — річний темп зростання цін між двома точками ланцюга.
//
// Рахує та сама AnnualPct, що й знецінення: одна формула на обидві
// лінійки, інакше вони розійшлися б на способі ануалізації, а не на
// природі, і порівнювати їх стало б нечесно. Місяць переводиться в дату
// першого числа — рівні належать кінцям місяців, але відстань між ними
// від цього не міняється.
//
// ok=false, коли точок замало або період вироджений.
func CPIAnnualPct(levels []CPILevel, from, to string) (float64, bool) {
	a, aok := levelAt(levels, from)
	b, bok := levelAt(levels, to)
	if !aok || !bok {
		return 0, false
	}
	da, errA := ParseDate(from + "-01")
	db, errB := ParseDate(to + "-01")
	if errA != nil || errB != nil {
		return 0, false
	}
	return AnnualPct(a, b, DaysBetween(da, db))
}

func levelAt(levels []CPILevel, period string) (float64, bool) {
	for _, l := range levels {
		if l.Period == period {
			return l.Level, true
		}
	}
	return 0, false
}

// CPIYoYDrift — на скільки відсоткових пунктів ланцюг розходиться з
// опублікованим річним темпом на цій точці.
//
// Це ЄДИНА зовнішня перевірка ланцюга, і саме заради неї yoy_bp
// зберігається попри те, що виводиться. Пропущений місяць, зсув ряду на
// місяць чи помилка масштабу — усі троє тихі за побудовою й видно їх
// тільки тут. ok=false, коли точки за рік до цієї в ряду немає.
func CPIYoYDrift(points []CPIPoint, levels []CPILevel, period string) (float64, bool) {
	var published int64
	found := false
	for _, p := range points {
		if p.Period == period {
			published, found = p.YoYBP, true
			break
		}
	}
	if !found {
		return 0, false
	}
	d, err := ParseDate(period + "-01")
	if err != nil {
		return 0, false
	}
	prev := string(d.AddMonths(-12))[:7]
	a, aok := levelAt(levels, prev)
	b, bok := levelAt(levels, period)
	if !aok || !bok || a <= 0 {
		return 0, false
	}
	chained := (b/a - 1) * 100
	return chained - float64(published)/100, true
}

// CPIGaps — місяці, яких у ряду БРАКУЄ між першою й останньою точками.
//
// Дірка в ряду тиха за побудовою: CPIChain просто не множить пропущений
// місяць, тож рівень виходить НИЖЧИМ, а число — правдоподібним. На
// бойовому це коштувало 2.8 в.п. (7.92%/рік замість 10.72%): бекфіл
// упіймав від НБУ серію 503, чесно порахував пропуски — і далі ніхто не
// спитав, чи ряд суцільний.
//
// CPIYoYDrift для цього НЕ ГОДИТЬСЯ, і виявилось це лише на живих даних:
// вона звіряє ОДНУ точку, а дірки лежали в 2016-2020 — останній рік був
// цілий, і дрейф показував +0.04 в.п. при зіпсованому ряді. Тобто звірка
// ловить помилку МАСШТАБУ й зсув ряду, а неповноту — ні; для неповноти
// потрібне саме перелічення місяців.
func CPIGaps(points []CPIPoint) []string {
	if len(points) < 2 {
		return nil
	}
	have := make(map[string]bool, len(points))
	for _, p := range points {
		have[p.Period] = true
	}
	var out []string
	last := points[len(points)-1].Period
	for m := points[0].Period; m != "" && m <= last; m = shiftPeriod(m, 1) {
		if !have[m] {
			out = append(out, m)
		}
	}
	return out
}

// shiftPeriod — 'YYYY-MM' плюс n місяців. Порожньо на неваліднім місяці:
// викликач тоді просто зупиняє прохід, а не ходить по колу.
func shiftPeriod(m string, n int) string {
	d, err := ParseDate(m + "-01")
	if err != nil {
		return ""
	}
	return string(d.AddMonths(n))[:7]
}

// CPIProject — скільки коштуватиме те саме через N місяців за річним
// темпом annualPct.
//
// Потрібне цілям: ціль задається в сьогоднішніх грошах, а купувати за неї
// будуть у рік дедлайну. Складний відсоток, помісячно — так само, як
// проєкція сама рахує внески (domain.MonthlyRate), інакше два числа на
// одному екрані розійшлися б на способі нарахування.
func CPIProject(amount, annualPct float64, months int) float64 {
	if months <= 0 || amount == 0 {
		return amount
	}
	return amount * math.Pow(1+annualPct/100, float64(months)/12)
}
