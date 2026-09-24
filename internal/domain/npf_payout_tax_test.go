package domain

import (
	"math"
	"testing"
)

// Податок на виплату НПФ — з УСІЄЇ виплати, а не з прибутку.
//
// ПКУ 164.2.16: пенсійна виплата на строк оподатковується на 60% СУМИ
// виплати; IncomeTaxBP уже несе це як ефективну ставку (18% + 5% × 60% =
// 13,8%, коментар при NPFAccount). Модель же брала ці 13,8% лише з
// прибутку: 500 тис. внесків, 1,2 млн на дату доступу — податок 96,6 тис.
// замість 165,6 тис., і виплата з проєкцією були на ~69 тис. вищі.
// Рішення власника (2026-09-24): від усієї виплати.
func TestNPFPayoutTaxedOnWholePayout(t *testing.T) {
	a := Accum{Value0: 1_200_000, Cost0: 500_000, RatePct: 0, CloseM: 1, TaxPct: 13.8, TaxOnPayout: true}
	if got := AccumCloseValue(a); math.Abs(got-1_034_400) > 0.01 {
		t.Errorf("на руки %.2f, чекали 1 034 400 (1,2 млн − 13,8%% від усієї суми)", got)
	}
	// Сертифікат фонду — як доти: податок лише з доходу.
	a.TaxOnPayout = false
	if got := AccumCloseValue(a); math.Abs(got-1_103_400) > 0.01 {
		t.Errorf("фонд: на руки %.2f, чекали 1 103 400 (податок з 700 тис. доходу)", got)
	}
	// Ставка після податку для НПФ: за 10 років при 10% і 13,8% з виплати.
	want := (math.Pow(math.Pow(1.10, 10)*(1-0.138), 0.1) - 1) * 100
	if got := NPFNetRatePct(10, 13.8, 10); math.Abs(got-want) > 1e-9 {
		t.Errorf("ставка НПФ після податку %.4f, чекали %.4f", got, want)
	}
}

// Графік виплат НПФ від 31-го — по одній на кожен місяць.
func TestNPFPayoutScheduleMonthEnd(t *testing.T) {
	a := NPFAccount{ID: 1, Currency: "UAH", AccessDate: "2040-01-31", PayoutYears: 10, PayoutFreq: "month"}
	sch := NPFPayoutSchedule(a, 120_000_00, "2040-04-30")
	want := []Date{"2040-01-31", "2040-02-29", "2040-03-31", "2040-04-30"}
	if len(sch) != len(want) {
		t.Fatalf("виплат %d, чекали %d: %+v", len(sch), len(want), sch)
	}
	for i, w := range want {
		if sch[i].Date != w {
			t.Errorf("виплата %d — %s, чекали %s", i+1, sch[i].Date, w)
		}
	}
}
