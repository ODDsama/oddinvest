// Плановані купівлі — що я збираюсь узяти й коли (міграція 0033).
//
// Сирі рядки, як PlanAction: sources.go (api) читає їх РІВНО ПО РАЗУ на
// документ, а перетворення в гіпотетичний портфель (рядок «на зараз») або
// в синтетичний запис плану (рядок з майбутньою датою) робить
// internal/api/state_plan_buys.go. Тут — тільки збереження й читання.
//
// Журналу ревізій немає навмисно — аргумент записаний у самій міграції.
package store

import (
	"context"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Види планованої купівлі. Рядки збігаються з kind у /api/reinvest, і це
// не збіг: «додати в план» приходить саме звідти, і два різні словники
// довелось би перекладати один в одний на кожному кроці.
const (
	BuyBond    = "bond"
	BuyFund    = "fund"
	BuyDeposit = "deposit"
	BuyNPF     = "npf"
)

// PlanBuy — один рядок плану купівель.
//
// Дві міри розміру (Qty і Amount) — не недогляд: папір і сертифікат
// купують штуками, вклад і внесок у НПФ — сумою. Яка з них читається,
// вирішує Kind; чому не одна колонка — у шапці міграції 0033.
type PlanBuy struct {
	ID   int64
	Kind string // bond | fund | deposit | npf
	// Ref — посилання В ТЕРМІНАХ СВОГО ВИДУ: ISIN, назва фонду, id
	// рахунку НПФ рядком, назва банку для вкладу.
	Ref    string
	Qty    int64
	Amount int64 // мінорні
	// UnitPrice — ціна одного сертифіката, мінорні; 0 = взяти з позиції.
	// Лише для фонда: каталогу цін фондів у застосунку немає.
	UnitPrice int64
	Currency  string // "" = вивести із сутності
	Broker    string // "" = обрати за залишком
	// BuyDate — "" означає «купую зараз». Саме тут проходить межа між
	// двома різними відповідями (див. state_plan_buys.go).
	BuyDate   domain.Date
	RateBP    int64 // вклад: ставка, %×100
	Months    int   // вклад: строк
	IsReserve bool  // планований вклад є подушкою
	Note      string
}

const planBuyCols = `id, kind, ref, qty, amount, unit_price, currency, broker,
	buy_date, rate_bp, months, is_reserve, note`

// ListPlanBuys — усі рядки плану, за датою й id. Порожня дата («зараз»)
// сортується першою сама собою: порожній рядок менший за будь-яку ISO-дату.
func (s *Store) ListPlanBuys(ctx context.Context) ([]PlanBuy, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+planBuyCols+` FROM plan_buys ORDER BY buy_date, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanBuy
	for rows.Next() {
		var b PlanBuy
		var date string
		if err := rows.Scan(&b.ID, &b.Kind, &b.Ref, &b.Qty, &b.Amount, &b.UnitPrice,
			&b.Currency, &b.Broker, &date, &b.RateBP, &b.Months, &b.IsReserve,
			&b.Note); err != nil {
			return nil, err
		}
		b.BuyDate = domain.Date(date)
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) AddPlanBuy(ctx context.Context, b PlanBuy) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO plan_buys
		(kind, ref, qty, amount, unit_price, currency, broker, buy_date,
		 rate_bp, months, is_reserve, note)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.Kind, b.Ref, b.Qty, b.Amount, b.UnitPrice, b.Currency, b.Broker,
		string(b.BuyDate), b.RateBP, b.Months, b.IsReserve, b.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdatePlanBuy переписує рядок, зберігаючи id. Вид теж переписується:
// передумати й замість паперу взяти вклад — це правка того самого наміру,
// а «видалити й завести» загубило б id, на який дивиться відкрита форма.
func (s *Store) UpdatePlanBuy(ctx context.Context, b PlanBuy) error {
	res, err := s.db.ExecContext(ctx, `UPDATE plan_buys SET
		kind=?, ref=?, qty=?, amount=?, unit_price=?, currency=?, broker=?,
		buy_date=?, rate_bp=?, months=?, is_reserve=?, note=? WHERE id=?`,
		b.Kind, b.Ref, b.Qty, b.Amount, b.UnitPrice, b.Currency, b.Broker,
		string(b.BuyDate), b.RateBP, b.Months, b.IsReserve, b.Note, b.ID)
	if err != nil {
		return err
	}
	return affectedOne(res, "планована купівля")
}

func (s *Store) DeletePlanBuy(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM plan_buys WHERE id=?`, id)
	if err != nil {
		return err
	}
	return affectedOne(res, "планована купівля")
}
