package domain

import "testing"

// Позначена наперед виплата лягає в гроші сьогодні, а не датою графіка.
func TestArrivalDate(t *testing.T) {
	if got := ArrivalDate("2026-12-12", "2026-09-22"); got != "2026-09-22" {
		t.Errorf("наперед: %s, чекали сьогодні", got)
	}
	if got := ArrivalDate("2026-09-16", "2026-09-22"); got != "2026-09-16" {
		t.Errorf("минула дата мусить лишитись своєю: %s", got)
	}
}
