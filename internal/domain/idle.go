package domain

import "sort"

// CashEvent — сума в грн-еквіваленті з датою. Спільний вигляд і для
// доходу, що надійшов, і для покупок, що його витратили.
type CashEvent struct {
	Date   Date
	Amount int64 // мінорні, грн-екв.
}

// RemainingInflows — які надходження ще лежать і з якого дня.
//
// Події зі знаком в одній черзі: додатна — гроші прийшли, відʼємна — пішли.
// Правило одне на весь застосунок (довід нижче, при IdleIncome): списання
// зʼїдає НАЙСТАРІШЕ надходження з датою не пізніше за своє; те, що надійшло
// після списання, воно не чіпає. Повертає уцілілі надходження в порядку
// дат, кожне зі своєю початковою датою й тим, що від нього лишилось.
//
// Це та сама черга, що рахує «дохід без діла», лише не згорнута в одне
// число: вік грошей на рахунку виводиться звідси, а не з окремої історії
// балансів, якої в застосунку немає й не треба.
func RemainingInflows(events []CashEvent) []CashEvent {
	var inc, out []CashEvent
	for _, e := range events {
		switch {
		case e.Amount > 0:
			inc = append(inc, e)
		case e.Amount < 0:
			out = append(out, CashEvent{Date: e.Date, Amount: -e.Amount})
		}
	}
	sort.SliceStable(inc, func(i, j int) bool { return inc[i].Date < inc[j].Date })
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date < out[j].Date })

	left := make([]int64, len(inc))
	for i, e := range inc {
		left[i] = e.Amount
	}
	for _, b := range out {
		need := b.Amount
		for i := range inc {
			if need <= 0 {
				break
			}
			// Черга відсортована, тож перший «пізніший за списання» означає,
			// що далі теж усі пізніші.
			if inc[i].Date.After(b.Date) {
				break
			}
			if left[i] <= 0 {
				continue
			}
			take := left[i]
			if take > need {
				take = need
			}
			left[i] -= take
			need -= take
		}
	}
	var rest []CashEvent
	for i, v := range left {
		if v > 0 {
			rest = append(rest, CashEvent{Date: inc[i].Date, Amount: v})
		}
	}
	return rest
}

// IdleIncome — скільки з ОТРИМАНОГО доходу ще не пішло в діло.
//
// Питання здається простим, доки не спробуєш відповісти на нього по
// одній виплаті. Купон на 82 ₴ не купує нічого: найдешевший папір
// коштує тисячу. Тож у покупку купон потрапляє змішаним із власними
// внесками, і питати «який саме купон оплатив цей папір» — те саме, що
// питати, яка саме крапля води в склянці з крана. Гроші незлічувані.
//
// Раніше на це питання відповідала людина: клікала «перевкладено» на
// кожній виплаті, вгадуючи заднім числом. Вгадування не буває
// систематичним, тож число було рівно настільки правдиве, наскільки
// сумлінним був клік.
//
// Тому домовляємось про правило: купівля з'їдає НАЙСТАРІШИЙ дохід, що
// надійшов до неї. Це домовленість, а не факт про конкретні гривні, —
// але вона однакова щодня й не потребує кліків. Купівля, більша за
// наявний дохід, просто вичерпує його: решту оплатили твої гроші, і це
// нормальний випадок, а не помилка.
//
// Дохід, що надійшов ПІСЛЯ покупки, вона не з'їдає: платити наперед
// грошима, яких ще немає, не можна.
//
// Це обгортка над RemainingInflows: дохід — додатні події, покупки —
// відʼємні, відповідь — сума уцілілого. Дві черги з одним правилом
// розійшлися б на першій же правці, тому черга одна.
func IdleIncome(income, purchases []CashEvent) int64 {
	events := make([]CashEvent, 0, len(income)+len(purchases))
	events = append(events, income...)
	for _, b := range purchases {
		events = append(events, CashEvent{Date: b.Date, Amount: -b.Amount})
	}
	var idle int64
	for _, e := range RemainingInflows(events) {
		idle += e.Amount
	}
	return idle
}
