package state

import "math"

// round2 — суми в документі мажорні з двома знаками (див. пакетний
// коментар). Тут своя копія, бо будівник стану живе в іншому пакеті.
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// Capital — ЄДИНЕ місце, де вирішується, що таке «капітал» і що таке
// «частка валюти».
//
// Доти це вирішувалось у чотирьох місцях незалежно, і всі чотири давали
// різні числа:
//
//	Build            — папери + рахунок + фонди + вклади
//	ребаланс         — папери + рахунок
//	старт проєкції   — папери + рахунок + фонди
//	фронтенд         — сума полів документа
//
// Наслідок було видно на одному екрані: плитка «Частка USD» показувала
// 57.6%, а картка ребалансу під нею — 0% для тієї самої валюти. Обидва
// числа були «правильні» кожне у своїй арифметиці, і саме тому дізнатись,
// яке з них брехливе, не було де.
//
// Тому тип, а не хелпер: хелпер із п'ятьма параметрами так само дозволяє
// передати різне з різних місць, а структура збирається один раз і далі
// лише читається.
type Capital struct {
	// BondsUAH — номінал ОВДП, грн-екв., усі валюти разом.
	BondsUAH float64
	// AccountUAH — гроші на рахунках брокерів, грн-екв.
	AccountUAH float64
	// FundsUAH — ринкова вартість сертифікатів, грн-екв.
	FundsUAH float64
	// DepositsUAH — тіло діючих вкладів, грн-екв.
	DepositsUAH float64
	// ReserveUAH — резерв («матрац»), грн-екв.
	ReserveUAH float64

	// *ByCur — грн-еквівалент того, що є СПРАВЖНЬОЮ експозицією в валюту.
	//
	// Готівки брокера тут немає навмисно: вона от-от стане чимось іншим,
	// тож стоїть лише в знаменнику («вільний кеш розводить частки»).
	// Резерв, навпаки, грішми й лишиться — $5000 у матраці це валютна
	// експозиція нарівні з доларовим папером.
	BondsByCur    map[string]float64
	DepositsByCur map[string]float64
	ReserveByCur  map[string]float64
}

// TotalUAH — увесь капітал, грн-екв.
func (c Capital) TotalUAH() float64 {
	return c.BondsUAH + c.AccountUAH + c.FundsUAH + c.DepositsUAH + c.ReserveUAH
}

// ExposureUAH — скільки капіталу стоїть у цій валюті, грн-екв.
func (c Capital) ExposureUAH(cur string) float64 {
	return c.BondsByCur[cur] + c.DepositsByCur[cur] + c.ReserveByCur[cur]
}

// SharePct — частка валюти в капіталі, %.
func (c Capital) SharePct(cur string) float64 {
	total := c.TotalUAH()
	if total <= 0 {
		return 0
	}
	return c.ExposureUAH(cur) * 100 / total
}
