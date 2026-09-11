// Package present — шар презентації: документ, зібраний у книжковій валюті
// (гривня), перекладається у валюту звітності на виході.
//
// НАВІЩО ОКРЕМИЙ ШАР. Гроші в базі, домені й добовому знімку — гривня, і
// такими лишаються: знімок читає числа з того самого документа, що йде в
// API, і конвертувати документ усередині збірки означало б мовчки писати
// долари в гривневі колонки. Тому переклад іде ПІСЛЯ збірки, на копії, у
// рівно трьох місцях: /api/summary, публікація в MQTT і обробники з
// власними відповідями. Добовий знімок презентера не бачить ніколи.
//
// ЯК ВІН ЗНАЄ, ЩО ГРОШІ. За типом: state.Money несе валюту, і поле в
// книжковій валюті перекладається, а натуральне (ціль у євро, борг у
// доларах) — ні. Структ-теги лишились лише для того, чого тип не каже:
//
//	money:"asof"          — поле-дата: курс на цю дату для сусідів і нащадків
//	money:"asof=from_date" — курс на дату НАЗВАНОГО сусіда (до/після)
//	money:"asof=days"      — для []Money при []string дат: поіндексно
//	money:"diff=after,before" — дельта як різниця ВЖЕ перекладених сусідів,
//	                          а не ділення гривневої дельти на один курс
//	money:"ruler=real_pct" — поле бере значення названого сусіда (лінійка
//	                          дохідності у валюті звітності); сусід лишається
//	                          рівним йому — лінійка одна, і читачі обох полів
//	                          бачать те саме число
//	money:"uah-only"       — обнуляється при валюті ≠ книжкової (ІСЦ, цілі
//	                          «у майбутніх грошах»)
//	money:"code"           — рядок із кодом ефективної валюти
//
// Приватні поля структур прохід не бачить (на дріт вони не йдуть), з
// одним винятком: неіменоване вкладення приватного типу — його поля json
// піднімає до господаря, і презентер іде в нього (довід у visitStruct).
//
// У книжковій валюті прохід — тотожність: він лише проставляє code. Саме
// це тримає золотий документ незмінним і робить шар безпечним для тих, хто
// його не вмикав.
//
// КУРС НА ДАТУ. Історія (знімки, місяці, дні) перекладається курсом своєї
// дати; те, що про майбутнє, — сьогоднішнім. Дата без точки в історії
// (старша за перший бекфіл) бере сьогоднішній курс; без сьогоднішнього
// курсу перекладати нема чим — Apply повертає помилку, і викликач лишає
// документ у гривні, назвавши причину в currency_note.
package present

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
)

// Rates — курс валюти звітності на дату: скільки книжкової валюти за її
// одиницю. Другий результат хибний, коли точки на цю дату чи раніше немає.
type Rates interface {
	At(on domain.Date) (float64, bool)
}

// Opts — що і в що перекладати.
type Opts struct {
	Book, Report string
	Rates        Rates
	Today        domain.Date
}

// Apply перекладає значення на місці. v — вказівник на структуру (документ
// чи відповідь обробника) або будь-що, що містить state.Money.
func Apply(v any, o Opts) error {
	if o.Book == "" {
		o.Book = "UAH"
	}
	if o.Report == "" {
		o.Report = o.Book
	}
	w := &walker{o: o, identity: o.Report == o.Book}
	if !w.identity {
		if _, ok := w.rate(o.Today); !ok {
			return fmt.Errorf("present: курсу %s на %s немає", o.Report, o.Today)
		}
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("present: потрібен вказівник, дано %T", v)
	}
	w.visit(rv, "")
	return w.err
}

var moneyType = reflect.TypeOf(state.Money{})

type walker struct {
	o        Opts
	identity bool
	err      error
	// scopes — стек структур від кореня до поточної: у кожній — поля за
	// json-іменем і дата області (з поля money:"asof", або успадкована).
	scopes []scope
}

type scope struct {
	val    reflect.Value
	byJSON map[string]int
	asof   domain.Date
}

func (w *walker) fail(err error) {
	if w.err == nil {
		w.err = err
	}
}

// rate — курс на дату; без точки на цю дату — сьогоднішній.
func (w *walker) rate(on domain.Date) (float64, bool) {
	if on != "" {
		if r, ok := w.o.Rates.At(on); ok && r > 0 {
			return r, true
		}
	}
	r, ok := w.o.Rates.At(w.o.Today)
	return r, ok && r > 0
}

func (w *walker) convert(m state.Money, on domain.Date) state.Money {
	if w.identity || m.IsZero() || m.Currency() != w.o.Book {
		return m
	}
	r, ok := w.rate(on)
	if !ok {
		w.fail(fmt.Errorf("present: курсу %s на %s немає", w.o.Report, on))
		return m
	}
	return m.In(w.o.Report, r)
}

func (w *walker) asof() domain.Date {
	if n := len(w.scopes); n > 0 {
		return w.scopes[n-1].asof
	}
	return ""
}

// visit — перекласти значення на місці; повертає значення для тих
// контейнерів, де на місці записати не можна (елементи мап).
func (w *walker) visit(v reflect.Value, on domain.Date) reflect.Value {
	if on == "" {
		on = w.asof()
	}
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			w.visit(v.Elem(), on)
		}
	case reflect.Interface:
		if !v.IsNil() {
			nv := w.visit(reflect.ValueOf(v.Interface()), on)
			if nv.IsValid() && v.CanSet() {
				v.Set(nv)
			}
			return nv
		}
	case reflect.Struct:
		if v.Type() == moneyType {
			nv := reflect.ValueOf(w.convert(v.Interface().(state.Money), on))
			if v.CanSet() {
				v.Set(nv)
			}
			return nv
		}
		if v.CanAddr() {
			w.visitStruct(v, on)
		} else {
			// Копія, бо структура прийшла значенням (елемент мапи).
			cp := reflect.New(v.Type()).Elem()
			cp.Set(v)
			w.visitStruct(cp, on)
			return cp
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			e := v.Index(i)
			nv := w.visit(e, on)
			if nv.IsValid() && e.CanSet() && !e.CanAddr() {
				e.Set(nv)
			}
		}
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		// Мапа з ключем "date" — рядок без структури (/api/snapshots будує
		// рядки з реєстру колонок): її дата — та сама область, що й
		// money:"asof" у структури.
		if v.Type().Key().Kind() == reflect.String {
			if d := v.MapIndex(reflect.ValueOf("date")); d.IsValid() {
				if d.Kind() == reflect.Interface {
					d = d.Elem()
				}
				if dd, ok := dateOf(d); ok {
					on = dd
				}
			}
		}
		for _, k := range v.MapKeys() {
			e := v.MapIndex(k)
			nv := w.visit(e, on)
			if nv.IsValid() {
				v.SetMapIndex(k, nv)
			}
		}
	}
	return reflect.Value{}
}

func jsonName(f reflect.StructField) string {
	name := strings.Split(f.Tag.Get("json"), ",")[0]
	if name == "" || name == "-" {
		return f.Name
	}
	return name
}

func dateOf(v reflect.Value) (domain.Date, bool) {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.String {
		return "", false
	}
	return dateString(v.String())
}

// dateString — дата з рядка. Місяць «2026-07» читається як його КІНЕЦЬ:
// рядок про місяць (історія плану, серія внесків, рік по місяцях)
// перекладається курсом останнього дня, а для поточного місяця кінець ще
// попереду — і курс береться останній відомий, тобто сьогоднішній. «-31»
// тут лише межа порівняння рядків (точки шукаються як «не пізніше»), а не
// календарна дата.
func dateString(s string) (domain.Date, bool) {
	switch len(s) {
	case 0:
		return "", false
	case 7:
		return domain.Date(s + "-31"), true
	}
	return domain.Date(s), true
}

func (w *walker) visitStruct(v reflect.Value, on domain.Date) {
	t := v.Type()
	sc := scope{val: v, byJSON: make(map[string]int, t.NumField()), asof: on}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || f.Anonymous {
			continue
		}
		sc.byJSON[jsonName(f)] = i
		if f.Tag.Get("money") == "asof" {
			if d, ok := dateOf(v.Field(i)); ok {
				sc.asof = d
			}
		}
	}
	w.scopes = append(w.scopes, sc)
	defer func() { w.scopes = w.scopes[:len(w.scopes)-1] }()

	var diffs []int
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		fv := v.Field(i)
		// Неіменоване вкладення — та сама структура з погляду json: його
		// поля лягають поруч із полями господаря, і презентер іде в нього
		// НЕЗАЛЕЖНО від експортованості. Тип вкладення в обробників
		// зазвичай пакетний (routeLeg вкладає allocPlan), і пропустити його
		// як «приватне поле» означало б віддати половину ноги в гривні під
		// знаком долара — саме так і сталось на живих даних. Reflect у
		// експортовані поля такого вкладення пише (на цьому стоїть і decode
		// json), тож окремої області вистачає: lookup іде вгору предками, і
		// asof=/diff= усередині бачать дату господаря.
		if f.Anonymous && !f.IsExported() {
			w.visit(fv, sc.asof)
			continue
		}
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("money")
		key, arg, _ := strings.Cut(tag, "=")
		switch key {
		case "":
			w.visit(fv, sc.asof)
		case "asof":
			if arg == "" {
				continue // сама дата
			}
			w.visitAsOf(sc, fv, arg)
		case "diff":
			diffs = append(diffs, i)
		case "ruler":
			w.ruler(sc, fv, arg)
		case "uah-only":
			if !w.identity {
				fv.Set(reflect.Zero(fv.Type()))
			}
		case "code":
			fv.SetString(w.o.Report)
		default:
			w.fail(fmt.Errorf("present: невідомий тег money:%q на %s.%s", tag, t.Name(), f.Name))
		}
	}
	// Дельти — ПІСЛЯ решти: різниця береться між уже перекладеними
	// сусідами, кожен зі своїм курсом.
	for _, i := range diffs {
		w.diff(sc, v.Field(i), t.Field(i).Tag.Get("money"))
	}
}

// visitAsOf — поле з курсом на дату названого сусіда (або предка: дні
// ряду суперників лежать у батьківській відповіді); для зрізу дат —
// поіндексно.
func (w *walker) visitAsOf(sc scope, fv reflect.Value, ref string) {
	src, ok := w.lookup(ref)
	if !ok {
		w.fail(fmt.Errorf("present: money:\"asof=%s\" не знаходить ні сусіда, ні предка", ref))
		return
	}
	if src.Kind() == reflect.Slice && src.Type().Elem().Kind() == reflect.String {
		if fv.Kind() != reflect.Slice {
			w.fail(fmt.Errorf("present: money:\"asof=%s\" поіндексно, а поле не зріз", ref))
			return
		}
		for i := 0; i < fv.Len(); i++ {
			on := sc.asof
			if i < src.Len() {
				on = domain.Date(src.Index(i).String())
			}
			w.visit(fv.Index(i), on)
		}
		return
	}
	on, ok := dateOf(src)
	if !ok {
		on = sc.asof
	}
	w.visit(fv, on)
}

// lookup — сусід за json-іменем, а коли його немає серед сусідів — у
// предків (capital_delta_30.delta_uah рахується від capital_uah кореня).
func (w *walker) lookup(name string) (reflect.Value, bool) {
	for i := len(w.scopes) - 1; i >= 0; i-- {
		if idx, ok := w.scopes[i].byJSON[name]; ok {
			return w.scopes[i].val.Field(idx), true
		}
	}
	return reflect.Value{}, false
}

func (w *walker) diff(sc scope, fv reflect.Value, tag string) {
	_, arg, _ := strings.Cut(tag, "=")
	a, b, ok := strings.Cut(arg, ",")
	if !ok {
		w.fail(fmt.Errorf("present: money:%q — потрібно два імені через кому", tag))
		return
	}
	if w.identity {
		return // будівник уже поклав різницю
	}
	av, aok := w.lookup(a)
	bv, bok := w.lookup(b)
	if !aok || !bok || av.Type() != moneyType || bv.Type() != moneyType || fv.Type() != moneyType {
		w.fail(fmt.Errorf("present: money:%q — обидва мусять бути state.Money", tag))
		return
	}
	fv.Set(reflect.ValueOf(av.Interface().(state.Money).Sub(bv.Interface().(state.Money))))
}

// ruler — поле бере значення названого сусіда; сусід ЛИШАЄТЬСЯ, рівний
// йому. Лише при валюті ≠ книжкової: у гривні обидві лінійки — як були.
//
// Не обнуляється навмисно. Читачі «реальної» (майстер-список, картки
// виду, віхи) навмисно дивляться на *_real*-поля — у доларі вони мусять
// бачити ту саму єдину лінійку, а не прочерк. Дві рівні лінійки і є
// чесним твердженням «у цій валюті лінійка одна»; повторювати число
// підписом «реальних» — справа того, хто показує, і він цього не робить.
func (w *walker) ruler(sc scope, fv reflect.Value, ref string) {
	idx, ok := sc.byJSON[ref]
	if !ok {
		w.fail(fmt.Errorf("present: money:\"ruler=%s\" не знаходить сусіда", ref))
		return
	}
	if w.identity {
		return
	}
	src := sc.val.Field(idx)
	if !src.Type().AssignableTo(fv.Type()) {
		w.fail(fmt.Errorf("present: money:\"ruler=%s\" — типи різні", ref))
		return
	}
	fv.Set(src)
}
