package api

import "testing"

// Нуль у мапі дохідностей по видах означав би «заробляє нічого», а чесна
// відповідь для відсутнього виду — прочерк. Тому ключа просто немає, і
// саме це правило тут і перевіряється: воно єдине, що kindYieldReal
// вирішує самостійно (усі чотири числа приходять уже зваженими).
func TestKindYieldRealOmitsZero(t *testing.T) {
	got := kindYieldReal(0.68, 0, 5.5, 0)
	if len(got) != 2 {
		t.Fatalf("ключів = %d, треба 2: %v", len(got), got)
	}
	if got["bonds"] != 0.68 || got["deposits"] != 5.5 {
		t.Errorf("значення поїхали: %v", got)
	}
	for _, k := range []string{"funds", "npf"} {
		if _, ok := got[k]; ok {
			t.Errorf("%q не має бути ключем: нуль — це «немає», а не «нуль»", k)
		}
	}
}

// Порожній портфель дає nil, а не порожню мапу: з `omitempty` поле тоді
// зникає з документа цілком, і споживач бачить відсутність, а не {}.
func TestKindYieldRealEmptyIsNil(t *testing.T) {
	if got := kindYieldReal(0, 0, 0, 0); got != nil {
		t.Errorf("порожній портфель дав %v, треба nil", got)
	}
}

// Від'ємна реальна дохідність — не «немає»: гривневий папір під знеціненням
// цілком може віддавати мінус, і сховати його означало б збрехати рівно там,
// де число найважливіше.
func TestKindYieldRealKeepsNegative(t *testing.T) {
	got := kindYieldReal(-0.17, 0, 0, 0)
	if v, ok := got["bonds"]; !ok || v != -0.17 {
		t.Errorf("мінус загубився: %v", got)
	}
}
