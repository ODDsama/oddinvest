package domain

import money "github.com/Rhymond/go-money"

// PayType — тип виплати за папером (кодування НБУ).
type PayType int

const (
	PayCoupon     PayType = 1 // відсотки (купон)
	PayRedemption PayType = 2 // погашення номіналу
	PayEarly      PayType = 3 // дострокове погашення
)

// Bond — папір з довідника НБУ (кеш).
type Bond struct {
	ISIN     string
	Nominal  *money.Money // номінал одного паперу у валюті випуску
	RateBP   int64        // ставка купона в базисних пунктах (16.55% -> 1655)
	Maturity Date
	Descr    string
}

// Payment — одна виплата за одним папером (з графіка НБУ).
type Payment struct {
	ISIN    string
	PayDate Date
	Type    PayType
	PerBond *money.Money // сума на один папір
}

// Lot — покупка: N паперів одного ISIN за ціною за папір.
// PricePerBond — фактично сплачене за один папір («брудна» ціна,
// включно з НКД, якщо він був).
type Lot struct {
	ID           int64
	ISIN         string
	Qty          int64
	PricePerBond *money.Money
	BuyDate      Date
	Channel      string
	Note         string
}

// Sale — продаж частини або всього лота на вторинному ринку.
// CleanPerBond — «чиста» ціна за папір; Accrued — отриманий НКД
// сумарно за угоду (може бути нульовим, якщо ціна вже «брудна»).
type Sale struct {
	ID           int64
	LotID        int64
	SaleDate     Date
	Qty          int64
	CleanPerBond *money.Money
	Accrued      *money.Money
	Note         string
}

// CashflowItem — агрегована майбутня виплата по портфелю.
type CashflowItem struct {
	Date   Date
	ISIN   string
	Type   PayType
	Amount *money.Money // perBond × кількість у власності на дату
}

// Position — агрегований стан по одному ISIN.
type Position struct {
	ISIN        string
	Currency    string
	Qty         int64        // залишок у власності зараз
	Invested    *money.Money // вартість входу залишку (Σ ціна купівлі × залишок лота)
	Nominal     *money.Money // номінал × залишок
	Maturity    Date
	DaysToMat   int
	NextPayDate Date
	NextPayAmt  *money.Money
}
