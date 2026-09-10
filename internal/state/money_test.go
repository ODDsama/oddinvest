package state

import (
	"encoding/json"
	"math"
	"testing"

	money "github.com/Rhymond/go-money"
)

// Marshal мусить давати той самий текст, що encoding/json для float64:
// на цьому тримається незмінність контракту й golden документа.
func TestMoneyMarshalMatchesFloat(t *testing.T) {
	for _, minor := range []int64{0, 1, -1, 50, -50, 100, 10000, 1234567, -1234567,
		99, 100000000000, 123456789012345} {
		m := UAH(minor)
		got, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(float64(minor) / 100)
		if string(got) != string(want) {
			t.Errorf("%d коп.: Money → %s, float64 → %s", minor, got, want)
		}
	}
}

// Нуль у названій валюті випадає під omitzero так само, як нульовий float
// під omitempty, а нуль без omitzero пишеться числом.
func TestMoneyOmitZero(t *testing.T) {
	type doc struct {
		A Money `json:"a,omitzero"`
		B Money `json:"b"`
		C Money `json:"c,omitzero"`
	}
	got, err := json.Marshal(doc{A: UAH(0), C: Minor(0, money.USD)})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"b":0}` {
		t.Errorf("omitzero: %s", got)
	}
}

func TestMoneyUnmarshal(t *testing.T) {
	var m Money
	if err := json.Unmarshal([]byte("12345.67"), &m); err != nil {
		t.Fatal(err)
	}
	if m.Minor() != 1234567 || m.Currency() != money.UAH {
		t.Errorf("з числа: %v", m)
	}
	if err := json.Unmarshal([]byte(`{"amount":"995.00","currency":"USD"}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.Minor() != 99500 || m.Currency() != money.USD {
		t.Errorf("з об'єкта: %v", m)
	}
	if err := json.Unmarshal([]byte("null"), &m); err != nil {
		t.Fatal(err)
	}
	if !m.IsZero() {
		t.Errorf("null: %v", m)
	}
	if err := json.Unmarshal([]byte(`"x"`), &m); err == nil {
		t.Error("рядок мав би бути помилкою")
	}
}

// Major не обрізає float-похибку й заокруглює так само, як round2 доти.
func TestMoneyMajorRounding(t *testing.T) {
	for _, c := range []struct {
		in   float64
		want int64
	}{
		{0.29, 29}, {-0.29, -29}, {12345.67, 1234567}, {2.5, 250},
		{0.125, 13}, {-0.125, -13}, {0.135, 14}, {1.005, 100},
	} {
		if got := Major(c.in, money.UAH).Minor(); got != c.want {
			t.Errorf("Major(%v) = %d, хочемо %d", c.in, got, c.want)
		}
	}
	if math.Abs(UAH(1234567).Major()-12345.67) > 1e-9 {
		t.Error("Major() назад")
	}
}

func TestMoneyArithmetic(t *testing.T) {
	a, b := UAH(150), UAH(250)
	if a.Add(b).Minor() != 400 || b.Sub(a).Minor() != 100 || a.Neg().Minor() != -150 {
		t.Error("додавання/віднімання/знак")
	}
	if a.Cmp(b) != -1 || b.Cmp(a) != 1 || a.Cmp(a) != 0 {
		t.Error("порівняння")
	}
	if UAH(1000).Mul(0.155).Minor() != 155 || UAH(1000).Mul(1.0/3).Minor() != 333 {
		t.Error("множення")
	}
	// Нуль без валюти складається з чим завгодно й бере валюту доданка.
	var total Money
	total = total.Add(Minor(300, money.USD))
	if total.Currency() != money.USD || total.Minor() != 300 {
		t.Errorf("акумулятор: %v", total)
	}
	if got := Of(money.New(4200, money.EUR)); got.Currency() != money.EUR || got.Minor() != 4200 {
		t.Errorf("Of: %v", got)
	}
	if !Of(nil).IsZero() {
		t.Error("Of(nil)")
	}
	if got := UAH(4200).Money(); got.Amount() != 4200 || got.Currency().Code != money.UAH {
		t.Error("назад у домен")
	}
	if UAH(1234567).String() != "12345.67 UAH" {
		t.Errorf("String: %s", UAH(1234567))
	}
}

// Різні валюти — паніка, і саме паніка: така помилка мусить бути видна на
// першому тесті, а не дійти до документа правдоподібним числом.
func TestMoneyMismatchPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("долар плюс гривня мав би панікувати")
		}
	}()
	_ = UAH(100).Add(Minor(100, money.USD))
}
