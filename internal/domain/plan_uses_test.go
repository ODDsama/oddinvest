package domain

import (
	"strings"
	"testing"
)

// Канон — не косметика: діф ревізій порівнює РЯДКИ, і той самий набір,
// записаний у різному порядку, показував би правку там, де її не було.
func TestParsePlanUsesCanonical(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"порядок вирівнюється", []string{"npf", "reserve"}, "reserve,npf"},
		{"дублікати склеюються", []string{"invest", "invest"}, "invest"},
		{"порожні токени ігноруються", []string{"", " reserve "}, "reserve"},
		// Обидва краї означають «без обмежень», і зводяться вони до одного
		// значення навмисно: явний повний перелік застарів би на першому ж
		// новому кошику, мовчки заборонивши його.
		{"нічого = без обмежень", nil, ""},
		{"усі чотири = без обмежень",
			[]string{"reserve", "goals", "invest", "npf"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParsePlanUses(c.in)
			if err != nil {
				t.Fatalf("несподівана помилка: %v", err)
			}
			if got != c.want {
				t.Errorf("канон %q, чекали %q", got, c.want)
			}
		})
	}
}

// Описка мусить бути ПОМИЛКОЮ, а не мовчазним нулем: набір, який тихо
// втратив кошик, заборонив би те, чого ніхто не забороняв.
func TestParsePlanUsesRejectsUnknown(t *testing.T) {
	_, err := ParsePlanUses([]string{"reserve", "papers"})
	if err == nil {
		t.Fatal("невідомий кошик прийнято мовчки")
	}
	if !strings.Contains(err.Error(), "papers") {
		t.Errorf("у помилці немає самого значення: %v", err)
	}
}

// Порожній дозвіл дозволяє ВСЕ — це поведінка до появи поля, і на ній
// стоїть уся зворотна сумісність: жоден рядок, заведений раніше, не мусить
// раптом виявитись забороненим.
func TestPlanUsesEmptyAllowsEverything(t *testing.T) {
	for _, b := range []string{UsePlanReserve, UsePlanGoals, UsePlanInvest, UsePlanNPF} {
		if !PlanUseAllowed("", b) {
			t.Errorf("порожній дозвіл заборонив %q", b)
		}
	}
	if got := PlanUsesList(""); len(got) != 4 {
		t.Errorf("перелік із порожнього — %v, чекали всі чотири кошики", got)
	}
	if PlanUsesNarrowed("") {
		t.Error("порожній дозвіл вважається звуженим")
	}
}

func TestPlanUsesNarrowedAllowsOnlyNamed(t *testing.T) {
	uses, err := ParsePlanUses([]string{"invest"})
	if err != nil {
		t.Fatal(err)
	}
	if !PlanUseAllowed(uses, UsePlanInvest) {
		t.Error("названий кошик заборонено")
	}
	for _, b := range []string{UsePlanReserve, UsePlanGoals, UsePlanNPF} {
		if PlanUseAllowed(uses, b) {
			t.Errorf("неназваний кошик %q дозволено", b)
		}
	}
	if !PlanUsesNarrowed(uses) {
		t.Error("звужений дозвіл не впізнано")
	}
}

// НОВИЙ КОШИК: порожній дозвіл пускає, звужений — ні. Саме ця пара й
// пояснює, чому «без обмежень» зберігається порожнім рядком: потік,
// заведений до появи цілей накопичення, не мусить виявитись таким, що на
// цілі не йде, а той, хто виписав перелік руками, отримує рівно те, що
// написав.
func TestPlanUsesAndAFutureBucket(t *testing.T) {
	const future = "crypto"
	if !PlanUseAllowed("", future) {
		t.Error("без обмежень новий кошик мусить бути дозволений")
	}
	narrowed, err := ParsePlanUses([]string{"reserve"})
	if err != nil {
		t.Fatal(err)
	}
	if PlanUseAllowed(narrowed, future) {
		t.Error("звужений перелік мовчки дозволив кошик, якого в ньому немає")
	}
}
