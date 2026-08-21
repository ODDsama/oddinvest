// Драбина доступу до подушки — переклад резервних вкладів у те, з чого
// state.deriveReserveLadder рахує покриття.
//
// ЧОМУ ОКРЕМИЙ ФАЙЛ, А НЕ РЯДОК У БУДІВНИКУ. Тут три перекази, і кожен
// потребує того, чого в пакеті state немає: курс (гривневий еквівалент),
// «сьогодні» (скільки місяців лишилось до погашення) і domain.NetRate
// (скільки сходинка приносить). Сама ж арифметика покриття — порівняння
// накопиченого з витраченим — курсів не потребує взагалі, і саме тому
// живе там, де її видно поруч із рештою правил про подушку.
//
// Той самий поділ, що в ReservePlaces: будівник зводить, state тлумачить.
package api

import (
	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"

	money "github.com/Rhymond/go-money"
)

// reserveLadderInput — резервні вклади у вигляді, потрібному драбині.
func reserveLadderInput(deps []domain.Deposit, today domain.Date,
	rates fx.Rates) []state.ReserveDeposit {
	if len(deps) == 0 {
		return nil
	}
	out := make([]state.ReserveDeposit, 0, len(deps))
	for _, d := range deps {
		u, err := fx.ToUAH(money.New(d.BalanceAt(today), d.Currency), rates)
		if err != nil {
			continue
		}
		amountUAH := float64(u.Amount()) / 100
		// МІСЯЦІ, а не дні, і дробові: горизонти драбини цілі, тож вклад,
		// що гаситься через 45 днів, мусить потрапити у другий місяць, а не
		// в перший. Округлення вгору зробило б це за нас і збрехало б у
		// безпечний бік лише на вигляд — «доступно через місяць» там, де
		// гроші прийдуть за півтора.
		months := float64(domain.DaysBetween(today, d.MaturityDate)) / 30.44
		if months < 0 {
			months = 0
		}
		// Скільки сходинка приносить за рік ПІСЛЯ податку. Знецінення сюди
		// НЕ входить, на відміну від порад реінвесту: питання тут не «чи
		// вигідно це проти інфляції», а «що саме ми втрачаємо, тримаючи
		// подушку готівкою». Відповідь на нього — гривні на рахунку.
		earns := 0.0
		if d.RateBP > 0 {
			earns = amountUAH * domain.NetRate(d.RateBP, d.TaxBP)
		}
		out = append(out, state.ReserveDeposit{
			Months: months, AmountUAH: amountUAH,
			Revocable: d.Revocable, EarnsUAH: earns,
		})
	}
	return out
}
