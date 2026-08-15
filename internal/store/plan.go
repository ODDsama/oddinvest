// Джерела доходу й планові дії (фаза 9 «План»).
//
// Сирі рядки, як ReserveOp: sources.go (api) читає їх РІВНО ПО РАЗУ на
// документ, а розгортання в помісячні вектори — справа sleeveFactory
// (internal/api/state_projection.go), бо саме там уже живе фабрика
// валютних рукавів, під яку вектори й будуються. Тут — тільки збереження
// й читання.
package store

import (
	"context"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// PlanFlow — регулярний або разовий рух грошей: дохід чи витрата.
// Amount — мінорні, завжди додатне; напрям задає Kind.
type PlanFlow struct {
	ID        int64
	Name      string
	Kind      string // income | expense
	Amount    int64
	Currency  string
	Cadence   string // once | month | quarter | year
	FromDate  domain.Date
	UntilDate domain.Date // "" = безстроково
	GrowthBP  int64       // індексація, %/рік × 100
	InvestBP  int64       // яка частка потоку йде в портфель, %×100
	Note      string
}

// PlanAction — точкове рішення на дату, у термінах валютного рукава.
//
// USDBP/EURBP = -1 означає «не задано» (0 — легальна ціль, «долара не
// лишається зовсім»).
type PlanAction struct {
	ID       int64
	Date     domain.Date
	Type     string // set_shares | lock
	USDBP    int64
	EURBP    int64
	Amount   int64 // lock: сума, мінорні
	Currency string
	RateBP   int64 // lock: ставка, %×100
	Months   int   // lock: строк; 0 = безстроково
	Name     string
	Note     string
}

func (s *Store) AddPlanFlow(ctx context.Context, f PlanFlow) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO plan_flows
		(name, kind, amount, currency, cadence, from_date, until_date, growth_bp, invest_bp, note)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		f.Name, f.Kind, f.Amount, f.Currency, f.Cadence, string(f.FromDate),
		string(f.UntilDate), f.GrowthBP, f.InvestBP, f.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdatePlanFlow переписує потік, зберігаючи id.
func (s *Store) UpdatePlanFlow(ctx context.Context, f PlanFlow) error {
	res, err := s.db.ExecContext(ctx, `UPDATE plan_flows SET
		name=?, kind=?, amount=?, currency=?, cadence=?, from_date=?, until_date=?,
		growth_bp=?, invest_bp=?, note=? WHERE id=?`,
		f.Name, f.Kind, f.Amount, f.Currency, f.Cadence, string(f.FromDate),
		string(f.UntilDate), f.GrowthBP, f.InvestBP, f.Note, f.ID)
	if err != nil {
		return err
	}
	return affectedOne(res, "плановий потік")
}

// Видалення теж через affectedOne: доти DELETE за неіснуючим id віддавав
// 204, тобто «видалено» звучало однаково і коли справді видалили, і коли
// видаляти було нічого.
func (s *Store) DeletePlanFlow(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM plan_flows WHERE id=?`, id)
	if err != nil {
		return err
	}
	return affectedOne(res, "плановий потік")
}

func (s *Store) ListPlanFlows(ctx context.Context) ([]PlanFlow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, kind, amount, currency, cadence,
		from_date, until_date, growth_bp, invest_bp, note FROM plan_flows ORDER BY from_date, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanFlow
	for rows.Next() {
		var f PlanFlow
		var from, until string
		if err := rows.Scan(&f.ID, &f.Name, &f.Kind, &f.Amount, &f.Currency, &f.Cadence,
			&from, &until, &f.GrowthBP, &f.InvestBP, &f.Note); err != nil {
			return nil, err
		}
		f.FromDate, f.UntilDate = domain.Date(from), domain.Date(until)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) AddPlanAction(ctx context.Context, a PlanAction) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO plan_actions
		(date, type, usd_bp, eur_bp, amount, currency, rate_bp, months, name, note)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		string(a.Date), a.Type, a.USDBP, a.EURBP, a.Amount, a.Currency, a.RateBP, a.Months, a.Name, a.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdatePlanAction переписує дію, зберігаючи id.
func (s *Store) UpdatePlanAction(ctx context.Context, a PlanAction) error {
	res, err := s.db.ExecContext(ctx, `UPDATE plan_actions SET
		date=?, type=?, usd_bp=?, eur_bp=?, amount=?, currency=?, rate_bp=?, months=?, name=?, note=?
		WHERE id=?`,
		string(a.Date), a.Type, a.USDBP, a.EURBP, a.Amount, a.Currency, a.RateBP, a.Months, a.Name, a.Note, a.ID)
	if err != nil {
		return err
	}
	return affectedOne(res, "планова дія")
}

func (s *Store) DeletePlanAction(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM plan_actions WHERE id=?`, id)
	if err != nil {
		return err
	}
	return affectedOne(res, "планова дія")
}

func (s *Store) ListPlanActions(ctx context.Context) ([]PlanAction, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, date, type, usd_bp, eur_bp, amount,
		currency, rate_bp, months, name, note FROM plan_actions ORDER BY date, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanAction
	for rows.Next() {
		var a PlanAction
		var dt string
		if err := rows.Scan(&a.ID, &dt, &a.Type, &a.USDBP, &a.EURBP, &a.Amount,
			&a.Currency, &a.RateBP, &a.Months, &a.Name, &a.Note); err != nil {
			return nil, err
		}
		a.Date = domain.Date(dt)
		out = append(out, a)
	}
	return out, rows.Err()
}
