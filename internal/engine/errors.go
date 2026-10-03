package engine

import "fmt"

// BadRequestError — помилка, яку обробник віддає як 400. Окремий тип, бо
// решта помилок фаз — це поломки сховища, і плутати їх з опискою у формі
// означало б показати людині 500 там, де вона просто не дозаповнила поле.
//
// Жила в state_plan_buys.go (першим її споживачем був кошик /api/whatif);
// після того як план купівель прибрали, лишились розкладка, маршрут і
// помічник — вибір паперу, якого немає серед порад.
type BadRequestError struct{ msg string }

func (e BadRequestError) Error() string { return e.msg }

// BadRequestf — BadRequestError із форматованим текстом.
func BadRequestf(format string, args ...any) error {
	return BadRequestError{msg: fmt.Sprintf(format, args...)}
}
