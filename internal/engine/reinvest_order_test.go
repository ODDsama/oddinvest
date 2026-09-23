package engine

import (
	"testing"

	money "github.com/Rhymond/go-money"
)

// TestOrderKeyDefaultsToReal — типова лінійка порядку одна, і це реальна.
// Порожній параметр, невідоме слово, будь-яке сміття з адресного рядка —
// усе це мусить давати той самий порядок, що й раніше.
func TestOrderKeyDefaultsToReal(t *testing.T) {
	s := suggestion{RealPct: 4.5, NominalPct: 16}
	for _, order := range []string{"", "real", "нісенітниця", "REAL"} {
		if got := orderKey(s, order); got != 4.5 {
			t.Fatalf("order=%q дав %v замість реальної 4.5", order, got)
		}
	}
	if got := orderKey(s, OrderNominal); got != 16 {
		t.Fatalf("номінальна лінійка дала %v", got)
	}
}

// TestLessSuggestionOrderFlipsOnlyTheYieldStep — перемикач міняє САМЕ
// крок вигоди й нічого більше. Пониження (замок, ліміт, транзит,
// застарілий папір) лишаються чинними в обох лінійках: вони твердження
// не про вигоду, а про порівнянність і впевненість.
func TestLessSuggestionOrderFlipsOnlyTheYieldStep(t *testing.T) {
	// Гривневий вклад: більше гривень, менше купівельної спроможності.
	uah := suggestion{Kind: "deposit", Currency: money.UAH, NominalPct: 16, RealPct: 3, CanBuy: true}
	// Валютний папір: навпаки.
	usd := suggestion{Kind: "bond", Currency: money.USD, NominalPct: 4.5, RealPct: 4.5, CanBuy: true}

	if !LessSuggestion(usd, uah, "rate", orderReal) {
		t.Fatal("за реальною валютний папір мусить бути вище")
	}
	if !LessSuggestion(uah, usd, "rate", OrderNominal) {
		t.Fatal("за номінальною гривневий вклад мусить бути вище")
	}

	// А тепер той самий гривневий вклад, але понад транзитом: він
	// опускається В ОБОХ лінійках, хоч номінально й найвигідніший.
	over := uah
	over.overTransit = true
	if !LessSuggestion(usd, over, "rate", OrderNominal) {
		t.Fatal("пониження за транзитом мусить діяти й у номінальній лінійці")
	}
}
