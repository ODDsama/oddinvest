// Конвертація за курсом НА ДАТУ ПОДІЇ.
//
// Чому не в internal/fx. Той пакет — чиста арифметика: приймає курси,
// віддає суми, у базу не ходить. Читання сховища зламало б саме ту межу,
// яку стереже гейт fx-boundary у Makefile, і наступний запит до бази
// поруч виглядав би вже нормально. Тому шар «звідки взялись курси» живе
// тут, у api, а сама конвертація й далі йде через fx.ToUAH.
//
// Навіщо взагалі. /api/tax переводив УСІ події одним сьогоднішнім курсом.
// На портфелі, де долар купували по 27, а дивись на нього по 44, це не
// похибка округлення: податок за 2022 рік виходив у півтора раза більшим,
// ніж його реально сплатили. Історія курсів у базі була весь цей час.

package api

import (
	"context"
	"fmt"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/store"
)

// asOfRates — курси на потрібні дати, з памʼяттю про те, наскільки вони
// відстали від самої події.
//
// Вартість запитів: один на кожну ПАРУ (валюта, унікальна дата), а не на
// подію. Річний звіт із двох сотень купонів по кількох датах виплат
// робить одиниці запитів, а не двісті. Сказано вголос, бо наступний
// читач інакше замінить це на повне вичитування fx_rates «щоб швидше».
type asOfRates struct {
	st    *store.Store
	cache map[string]int64 // "USD|2026-07-15" → курс ×10⁴

	// maxLag — найбільше відставання курсу від дати події, у днях.
	// missing — скільки подій лишились без курсу взагалі.
	//
	// Обидва накопичуються, а не ховаються: помісячний бекфіл означає, що
	// подія 2019 року може відставати днів на тридцять, і чесна відповідь
	// — назвати це число, а не робити вигляд, що конвертація точна.
	maxLag  int
	missing int
}

func newAsOfRates(st *store.Store) *asOfRates {
	return &asOfRates{st: st, cache: map[string]int64{}}
}

// rate — курс валюти до гривні на вказану дату (×10⁴). Нуль означає, що
// точки на цю дату чи раніше немає взагалі.
//
// Гривню сюди не подають: uah() відсікає її раніше. Так і задумано —
// масштаб курсу (×10⁴) не має витікати за межі internal/fx, і саме це
// стереже гейт fx-boundary.
func (a *asOfRates) rate(ctx context.Context, code string, on domain.Date) (int64, error) {
	key := code + "|" + string(on)
	if r, ok := a.cache[key]; ok {
		return r, nil
	}
	p, err := a.st.RatePointOnOrBefore(ctx, code, on)
	if err != nil {
		return 0, err
	}
	if p.RateE4 > 0 && p.Date != "" {
		if lag := domain.DaysBetween(p.Date, on); lag > a.maxLag {
			a.maxLag = lag
		}
	}
	a.cache[key] = p.RateE4
	return p.RateE4, nil
}

// uah — сума в гривнях за курсом НА ДАТУ ПОДІЇ.
//
// Нуль і лічильник missing замість помилки: одна подія без курсу не
// привід не показати звіт цілком. Скільки їх було — видно в note.
func (a *asOfRates) uah(ctx context.Context, m *money.Money, on domain.Date) (int64, error) {
	if m == nil {
		return 0, nil
	}
	code := m.Currency().Code
	if code == money.UAH {
		return m.Amount(), nil
	}
	r, err := a.rate(ctx, code, on)
	if err != nil {
		return 0, err
	}
	if r <= 0 {
		a.missing++
		return 0, nil
	}
	u, cerr := fx.ToUAH(m, fx.Rates{code: r})
	if cerr != nil {
		a.missing++
		return 0, nil
	}
	return u.Amount(), nil
}

// note — те, що треба сказати читачеві про якість конвертації.
//
// Форма навмисно та сама, що й у handleBenchmark: там уже вирішено, як
// цей застосунок говорить про пропущені курси, і другий спосіб сказати
// те саме читався б як інша за природою проблема.
func (a *asOfRates) note() string {
	if a.missing == 0 {
		return ""
	}
	return fmt.Sprintf("%d %s без курсу на свою дату — не враховано",
		a.missing, plural(a.missing, "рух", "рухи", "рухів"))
}
