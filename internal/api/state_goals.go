// Цілі накопичення — переклад зі сховища в те, з чого state.deriveGoals
// рахує прогрес.
//
// ЧОМУ ОКРЕМИЙ ФАЙЛ, А НЕ РЯДОК У БУДІВНИКУ. Той самий довід, що в шапці
// state_reserve_ladder.go: тут чотири перекази, і кожен потребує того,
// чого в пакеті state немає, — курсу (ціль у доларах, гроші в гривні),
// «сьогодні» (вікно темпу) і розкладки по місцях зберігання. Сама ж
// арифметика «скільки лишилось і чи встигаю» курсів не потребує взагалі й
// живе там, де її видно поруч із рештою правил.
//
// ГОЛОВНЕ РІШЕННЯ ЦЬОГО ФАЙЛА: ЗІБРАНЕ МІРЯЄТЬСЯ СЬОГОДНІШНІМ КУРСОМ.
//
// Ціль у доларах, а відкладати можна й гривнею — отже, питання «скільки
// зібрано» має два різні чесні прочитання. Перше: перевести кожен рух у
// долари курсом ЙОГО ДНЯ й скласти. Друге: скласти в гривні й перевести
// сьогоднішнім курсом. Взято друге, і це не зручність.
//
// Питання, на яке відповідає ціль, — «чи вистачить мені на авто». Гривні,
// відкладені торік, купують сьогодні стільки доларів, скільки за них дають
// СЬОГОДНІ; скільки вони купували торік, для покупки авто не важить ніяк.
// Перший спосіб намалював би прогрес, якого немає, — і рівно в той момент,
// коли девальвація відкинула тебе назад, застосунок казав би «усе за
// планом».
//
// Курс на дату події (fx_asof.go) тут ні до чого: він відповідає на інше
// питання — «скільки коштувала подія, коли вона сталась», і потрібен
// податковому звіту, де база рахується саме на дату.
package api

import (
	"strings"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

// goalsBuilt — усе, що будівник дістає з цілей за один прохід.
//
// Структурою, а не чотирма поверненнями: три з чотирьох потрібні різним
// споживачам (документ, капітал, валютні частки), і нумерувати їх у
// сигнатурі означало б плутати місцями два map[string]float64.
type goalsBuilt struct {
	// Input — готові рядки для state.deriveGoals.
	Input []state.GoalInput
	// UAH — усі цілі разом, грн-екв.; ByCur — валютна експозиція за валютою
	// САМИХ ГРОШЕЙ, а не за валютою цілі (аргумент — у capital.go).
	UAH   float64
	ByCur map[string]float64
	// MovedUAH — скільки покладено в цілі за ПОТОЧНИЙ місяць, нетто.
	// Дзеркалить ReserveMovedUAH і потрібне тому самому: стеля наповнення
	// віднімає вже відкладене, інакше порада висіла б незмінною.
	MovedUAH float64
}

// buildGoals зводить цілі та їхні журнали.
//
// Рухи розкладаються по цілях ОДНИМ проходом: ListGoalOps віддає весь
// журнал разом навмисно (див. її коментар), і запит на кожну ціль означав
// би N звернень до сховища на кожен /api/summary.
func buildGoals(goals []store.Goal, ops []store.GoalOp,
	rates fx.Rates, today domain.Date, now time.Time) goalsBuilt {

	out := goalsBuilt{ByCur: map[string]float64{}}
	if len(goals) == 0 {
		return out
	}

	type acc struct {
		uah        float64
		byCur      map[string]float64
		places     map[string]float64
		lastMove   string
		windowUAH  float64
		windowFrom domain.Date
		hasWindow  bool
		movedUAH   float64
	}
	per := make(map[int64]*acc, len(goals))
	for _, g := range goals {
		per[g.ID] = &acc{byCur: map[string]float64{}, places: map[string]float64{}}
	}

	for _, op := range ops {
		a := per[op.GoalID]
		if a == nil {
			// Рух під ціллю, якої немає. FK це унеможливлює, але сирий рядок
			// із бекапу старшої схеми міг би дійти сюди — і мовчки роздути
			// капітал на суму, яку нема до чого віднести.
			continue
		}
		u, err := fx.ToUAH(money.New(op.Amount, op.Currency), rates)
		if err != nil {
			// Невідомий код валюти. Пропускаємо саме рух, а не всю ціль:
			// решта журналу правдива, і показати її краще, ніж не показати
			// нічого (той самий підхід, що в reserveLadderInput).
			continue
		}
		v := float64(u.Amount()) / 100
		a.uah += v
		a.byCur[op.Currency] += float64(op.Amount) / 100
		place := strings.TrimSpace(op.Place)
		if place == "" {
			place = "—"
		}
		a.places[place] += v
		if string(op.Date) > a.lastMove {
			a.lastMove = string(op.Date)
		}
		// Темп — за тим самим вікном, що й темп поповнень портфеля.
		if n := domain.DaysBetween(op.Date, today); n >= 0 && n <= actualWindowDays {
			a.windowUAH += v
			if !a.hasWindow || op.Date.Before(a.windowFrom) {
				a.windowFrom, a.hasWindow = op.Date, true
			}
		}
		// Покладене цього місяця — нетто, як і в резерву: воно віднімається
		// зі стелі наповнення, тож зняття мусить її повертати.
		if op.Date.Year() == now.Year() && int(op.Date.Month()) == int(now.Month()) {
			a.movedUAH += v
			out.MovedUAH += v
		}
	}

	for _, g := range goals {
		a := per[g.ID]
		rate := goalRate(g.Currency, rates)
		in := state.GoalInput{
			ID: g.ID, Name: g.Name, Currency: g.Currency,
			TargetNative: float64(g.TargetAmount) / 100,
			TargetUAH:    float64(g.TargetAmount) / 100 * rate,
			CollectedUAH: a.uah,
			// У валюту цілі — СЬОГОДНІШНІМ курсом; довід у шапці файла.
			CollectedNative: a.uah / rate,
			ByCurrency:      pruneZero(a.byCur),
			Places:          pruneZero(a.places),
			LastMove:        a.lastMove,
			DueDate:         string(g.DueDate),
			DoneDate:        string(g.DoneDate),
			MovedUAH:        a.movedUAH,
		}
		if a.hasWindow && a.windowUAH > 0 {
			months := paceMonths(a.windowFrom, today)
			in.ActualUAH = a.windowUAH / months
			in.ActualNative = in.ActualUAH / rate
		}
		out.Input = append(out.Input, in)

		// У капітал і в експозицію йдуть гроші, які ПІД ціллю ще лежать.
		// Закрита ціль потрапляє сюди на тих самих правах: якщо річ куплена,
		// журнал уже має зняття й залишок нульовий, а якщо ні — гроші справді
		// є. Окремого правила для done_date тут свідомо немає.
		out.UAH += a.uah
		for cur, v := range a.byCur {
			if u, err := fx.ToUAH(money.New(int64(v*100+0.5), cur), rates); err == nil {
				out.ByCur[cur] += float64(u.Amount()) / 100
			}
		}
	}
	out.MovedUAH = round2(out.MovedUAH)
	return out
}

// goalRate — курс валюти цілі в гривні. Одиниця для гривні й для
// невідомого: нуль пішов би в знаменник (та сама причина, що в allocRate).
func goalRate(cur string, rates fx.Rates) float64 {
	if r, ok := fx.RateMajor(cur, rates); ok && r > 0 {
		return r
	}
	return 1
}

// pruneZero прибирає копійчані залишки, які лишаються після нетто.
//
// Ціль, куди поклали 5 000 ₴ і звідти ж їх узяли, не мусить показувати
// «UAH — 0,00 ₴»: рядок є, суми немає, і читається це як загублені гроші.
// Той самий прийом уже стоїть для місць резерву в state_builder.go.
func pruneZero(m map[string]float64) map[string]float64 {
	for k, v := range m {
		if v > -0.005 && v < 0.005 {
			delete(m, k)
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}
