package domain

import (
	"math"
	"sort"
)

// Ціна моїх рішень: ті самі гроші, ті самі дати — і те, що зробив би з
// ними той, хто не вирішував нічого.
//
// Застосунок міряє, скільки портфель заробив, і не міряє, скільки він
// заробив ПРОТИ АЛЬТЕРНАТИВИ. Одна картка це робила — «а якби просто
// долари» (cashflow.go) — і робила на одному супернику й на половині
// грошей. Тут суперників четверо, а грошей стільки, скільки їх дали.
//
// ЩО ЦЕЙ ФАЙЛ ЗНАЄ Й ЧОГО НЕ ЗНАЄ. Він не знає ні про БД, ні про HTTP, ні
// про валютні коди застосунку: на вхід приходять внески, вже зведені в
// гривню курсом СВОГО дня, і ряди чисел на дати. Рішення «що таке внесок»
// і «що таке сьогоднішня вартість» ухвалюються в api/rivals.go, бо саме
// там живуть чотири журнали, з яких вони збираються.
//
// РАХУНОК У float64, А НЕ В МІНОРНИХ ОДИНИЦЯХ, і це відхилення від
// грошової моделі репозиторію (README «Грошова модель»), тож ось довід.
// Суперник — це МОДЕЛЬ, а не облік: жодне з цих чисел ніколи не стане
// сумою, яку хтось заплатить, і жодне не звіряється з випискою. Рівно так
// само рахує проєкція (domain/sleeves.go) — і з тієї самої причини:
// зростання за ставкою дробове за природою, а int64 тут дав би не
// точність, а систематичне заниження на кожному кроці. Мінорні одиниці
// лишаються там, де гроші справжні, — у журналах, і переводяться рівно
// один раз, на вході.
//
// ВИНЯТОК ІЗ ПОПЕРЕДНЬОГО АБЗАЦУ: перерахований внесок ОКРУГЛЮЄТЬСЯ до
// сотих одиниці суперника (центів долара, копійок гривні). Це не
// прикраса — саме так рахувала попередня версія бенчмарка через
// fx.FromUAH, і без округлення число, яке вже показувалось користувачеві,
// поїхало б на пил без жодної на те причини.
//
// МЕЖА, ЯКУ ЦЕЙ ФАЙЛ НЕ ПЕРЕХОДИТЬ — та сама, що у fxwindow.go і
// state_market.go: тут немає ані ануалізації, ані вироку. «Долар обіграв
// портфель на 3.65%» за сорок сім днів — це твердження про сорок сім
// днів; помножене на 365/47, воно стало б твердженням про стратегію, якої
// ніхто не перевіряв. Порівняння показуємо, рішення лишаємо людині.

// Quote — значення на дату: курс валюти в мажорних одиницях (₴ за $1) або
// річна ставка в ЧАСТКАХ (0.1518, не 15.18).
//
// Власний тип, а не store.RatePoint: цей пакет сховища не знає, а ставка
// аукціону взагалі не курс. Спільне в них рівно одне — питання «яке
// значення діяло на цю дату», і відповідає на нього AsOf.
type Quote struct {
	On Date
	V  float64
}

// Quotes — ряд котирувань. Порядок довільний: AsOf сортує сам.
type Quotes []Quote

// AsOf — значення, що діяло на дату on: остання точка НЕ ПІЗНІША за неї.
//
// Саме «не пізніша», а не найближча: курс наступного тижня в день внеску
// був невідомий, і взяти його означало б дати супернику знання, якого в
// нього не було. Хибний ok — дірка в даних, і поводитись із нею мусить
// викликач (див. «все або нічого» в RunRivals).
func (q Quotes) AsOf(on Date) (float64, bool) {
	sort.SliceStable(q, func(i, j int) bool { return q[i].On < q[j].On })
	out, ok := 0.0, false
	for _, p := range q {
		if p.On > on {
			break
		}
		out, ok = p.V, true
	}
	return out, ok
}

// Contribution — один зовнішній рух грошей, зведений у гривню курсом
// СВОГО дня. Додатне — вніс, від'ємне — забрав.
type Contribution struct {
	On  Date
	UAH float64
}

// Ключі суперників. Рядками, а не iota: вони їдуть у JSON і в CSS-класи
// кривої, тобто живуть за межами Go, і число там не прочитається.
const (
	RivalUAHCash    = "uah_cash"
	RivalUSDCash    = "usd_cash"
	RivalEURCash    = "eur_cash"
	RivalOVDPMarket = "ovdp_market"
)

// RivalInputs — ряди, з яких суперники живуть.
type RivalInputs struct {
	// USD, EUR — курс НБУ, ₴ за одиницю, у мажорних.
	USD Quotes
	EUR Quotes
	// OVDP — рівень розміщення Мінфіну на річному строку, У ЧАСТКАХ.
	// Строк прибитий до одного року й названий вголос в api/rivals.go:
	// драбина суперника — це політика, а мовчазна політика гірша за
	// названу.
	OVDP Quotes
}

// Rival — один суперник на всій сітці днів.
type Rival struct {
	Key string
	// Points — вартість на кожен день сітки, грн-екв. Порожній, коли
	// Why непорожній.
	Points []float64
	// TerminalUAH — вартість на останній день сітки.
	TerminalUAH float64
	// Why — чому суперник мовчить. Непорожній рівно тоді, коли даних
	// забракло: суперник не має права віддати число з діркою.
	Why string
}

// ovdpTermDays — строк паперу, який купує ринковий суперник, і період
// його перекладення. Рік, бо це єдиний строк, який Мінфін розміщує
// практично щотижня: на довших бакетах ряд рідкий, і «рівень на дату»
// подекуди означав би рівень піврічної давнини.
const ovdpTermDays = 365

// RunRivals проганяє кожного суперника по спільній сітці днів.
//
// Сітка приходить ззовні, а не будується тут, і це навмисно: крива факту
// малюється зі знімків, і дві сітки, побудовані нарізно, розійшлись би на
// день — рівно на той, у який демон не працював.
func RunRivals(flows []Contribution, days []Date, in RivalInputs) []Rival {
	if len(days) == 0 {
		return nil
	}
	ordered := append([]Contribution(nil), flows...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].On < ordered[j].On })

	return []Rival{
		cashRival(ordered, days),
		fxRival(RivalUSDCash, ordered, days, in.USD, "долара"),
		fxRival(RivalEURCash, ordered, days, in.EUR, "євро"),
		ovdpRival(ordered, days, in.OVDP),
	}
}

// cashRival — «нічого не робив»: гроші лежать гривнею і не ростуть.
//
// Нульова лінія, від якої читаються всі інші. Даних їй не треба взагалі,
// тож замовкнути вона не може: сума внесків відома завжди.
func cashRival(flows []Contribution, days []Date) Rival {
	out := Rival{Key: RivalUAHCash, Points: make([]float64, 0, len(days))}
	sum, fi := 0.0, 0
	for _, d := range days {
		for fi < len(flows) && flows[fi].On <= d {
			sum += flows[fi].UAH
			fi++
		}
		out.Points = append(out.Points, sum)
	}
	out.TerminalUAH = out.Points[len(out.Points)-1]
	return out
}

// fxRival — «купив валюту й не робив більше нічого».
//
// Відсотків не приносить навмисно: це поведінка «нічого не робити», з
// якою й порівнюють. Купони, дивіденди й відсотки — це рівно те, що ти
// отримав НАТОМІСТЬ, і зарахувати їх ще й супернику означало б порівняти
// портфель сам із собою.
func fxRival(key string, flows []Contribution, days []Date, q Quotes, what string) Rival {
	out := Rival{Key: key, Points: make([]float64, 0, len(days))}
	units, fi := 0.0, 0
	for _, d := range days {
		for fi < len(flows) && flows[fi].On <= d {
			rate, ok := q.AsOf(flows[fi].On)
			if !ok || rate <= 0 {
				return Rival{Key: key, Why: "немає курсу " + what + " на " + string(flows[fi].On)}
			}
			units += math.Round(flows[fi].UAH/rate*100) / 100
			fi++
		}
		rate, ok := q.AsOf(d)
		if !ok || rate <= 0 {
			return Rival{Key: key, Why: "немає курсу " + what + " на " + string(d)}
		}
		out.Points = append(out.Points, units*rate)
	}
	out.TerminalUAH = out.Points[len(out.Points)-1]
	return out
}

// ovdpPaper — один синтетичний папір ринкового суперника.
type ovdpPaper struct {
	principal float64
	rate      float64
	from      Date
	matures   Date
}

func (p ovdpPaper) valueOn(d Date) float64 {
	return p.principal * math.Pow(1+p.rate, float64(DaysBetween(p.from, d))/ovdpTermDays)
}

// ovdpRival — «не вибирав, а просто брав ринок».
//
// Кожен внесок купує річний гривневий папір за рівнем, під який Мінфін
// розміщував НА ТУ ДАТУ; на погашенні тіло з відсотками перекладається в
// рівень уже того дня. Найжорсткіший із чотирьох суперників і єдиний, у
// кого є дохідність.
//
// ПОДАТКУ НЕМАЄ, і це не спрощення: дохід за ОВДП звільнений від ПДФО й
// військового збору. Рядок податку тут був би вигадкою в бік, вигідний
// портфелю.
//
// ЗНЯТТЯ зменшує всі відкриті папери пропорційно, за нарахованою
// вартістю. Продати ОВДП на вторинному ринку можна, але ціни застосунок
// не моделює — це та сама відмова, що записана в README, — тож альтернатив
// рівно дві: порахувати за балансом або вигадати ціну. Друге гірше.
func ovdpRival(flows []Contribution, days []Date, q Quotes) Rival {
	out := Rival{Key: RivalOVDPMarket, Points: make([]float64, 0, len(days))}
	var open []ovdpPaper
	fi := 0
	for _, d := range days {
		// 1. Погашення й перекладення — ДО потоків дня: гроші, що
		// звільнились сьогодні, вже лежать у новому папері, коли надходить
		// сьогоднішній внесок.
		for i := range open {
			for open[i].matures <= d {
				rate, ok := q.AsOf(open[i].matures)
				if !ok || rate < 0 {
					return Rival{Key: RivalOVDPMarket,
						Why: "немає рівня розміщення на " + string(open[i].matures)}
				}
				open[i] = ovdpPaper{
					principal: open[i].principal * (1 + open[i].rate),
					rate:      rate,
					from:      open[i].matures,
					matures:   open[i].matures.AddDays(ovdpTermDays),
				}
			}
		}
		// 2. Потоки дня.
		for fi < len(flows) && flows[fi].On <= d {
			f := flows[fi]
			fi++
			if f.UAH >= 0 {
				rate, ok := q.AsOf(f.On)
				if !ok || rate < 0 {
					return Rival{Key: RivalOVDPMarket,
						Why: "немає рівня розміщення на " + string(f.On)}
				}
				open = append(open, ovdpPaper{principal: f.UAH, rate: rate,
					from: f.On, matures: f.On.AddDays(ovdpTermDays)})
				continue
			}
			total := 0.0
			for _, p := range open {
				total += p.valueOn(d)
			}
			if total <= 0 {
				continue
			}
			keep := 1 + f.UAH/total // f.UAH від'ємне
			if keep < 0 {
				keep = 0
			}
			for i := range open {
				open[i].principal *= keep
			}
		}
		// 3. Точка дня.
		sum := 0.0
		for _, p := range open {
			sum += p.valueOn(d)
		}
		out.Points = append(out.Points, sum)
	}
	out.TerminalUAH = out.Points[len(out.Points)-1]
	return out
}

// DaysGrid — суцільна сітка днів від from до to включно.
//
// Живе тут, а не в api, бо сітка — частина домовленості між кривою факту
// й кривими суперників: обидві мусять бути однакової довжини, інакше
// графік покаже зсув як розбіжність.
func DaysGrid(from, to Date) []Date {
	if from == "" || to == "" || from > to {
		return nil
	}
	out := []Date{}
	for d := from; d <= to; d = d.AddDays(1) {
		out = append(out, d)
	}
	return out
}
