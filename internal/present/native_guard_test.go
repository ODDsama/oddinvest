package present

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ODDsama/oddinvest/internal/state"
)

// keyedByName — мапи з грошима, де ключ НЕ валюта, а назва (брокер,
// місце, рахунок). Їхні числа — грн-екв., і перекладаються, як усе.
// Кожна інша мапа з грошима мусить мати money:"native": ключем у неї
// валюта, і переклад числа збрехав би ключу (25a4a82 — brokers.mono.UAH
// = 11227.05 доларів, звірка записала б поправку на сотні тисяч).
//
// Перелік явний навмисно: нова мапа має бути класифікована тим, хто її
// заводить, а не вгадана тестом за назвою поля.
var keyedByName = map[string]bool{
	"invested_by_broker": true, // брокер → вкладено, грн-екв.
	"places":             true, // місце зберігання → сума, грн-екв.
}

// Сторож: кожна мапа з грошима й кожне поле …Native у документі стану
// мають явне рішення про переклад.
func TestNativeShapesTagged(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(tp reflect.Type, path string)
	walk = func(tp reflect.Type, path string) {
		for tp.Kind() == reflect.Pointer || tp.Kind() == reflect.Slice || tp.Kind() == reflect.Array {
			tp = tp.Elem()
		}
		if tp.Kind() == reflect.Map {
			walk(tp.Elem(), path+"[]")
			return
		}
		if tp.Kind() != reflect.Struct || tp == moneyType || seen[tp] {
			return
		}
		seen[tp] = true
		for i := 0; i < tp.NumField(); i++ {
			f := tp.Field(i)
			if !f.IsExported() && !f.Anonymous {
				continue
			}
			name := jsonName(f)
			tag := f.Tag.Get("money")
			where := path + "." + name
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Map && holdsMoney(ft.Elem(), map[reflect.Type]bool{}) {
				if tag != "native" && !keyedByName[name] {
					t.Errorf("%s (%s.%s): мапа з грошима без рішення — money:\"native\", якщо ключ валюта, "+
						"або keyedByName, якщо назва", where, tp.Name(), f.Name)
				}
				if tag == "native" {
					continue // усередину переклад не йде
				}
			}
			if ft == moneyType && strings.HasSuffix(f.Name, "Native") && tag != "native" && tag != "uah-only" {
				t.Errorf("%s (%s.%s): нативна сума без money:\"native\" — у гривневому рядку її "+
					"переклали б під гривневою міткою", where, tp.Name(), f.Name)
			}
			walk(f.Type, where)
		}
	}
	walk(reflect.TypeOf(state.Doc{}), "doc")
}

// holdsMoney — чи є в типі state.Money (прямо або глибше).
func holdsMoney(tp reflect.Type, seen map[reflect.Type]bool) bool {
	for tp.Kind() == reflect.Pointer || tp.Kind() == reflect.Slice || tp.Kind() == reflect.Array || tp.Kind() == reflect.Map {
		tp = tp.Elem()
	}
	if tp == moneyType {
		return true
	}
	if tp.Kind() != reflect.Struct || seen[tp] {
		return false
	}
	seen[tp] = true
	for i := 0; i < tp.NumField(); i++ {
		if holdsMoney(tp.Field(i).Type, seen) {
			return true
		}
	}
	return false
}
