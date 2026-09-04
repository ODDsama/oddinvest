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
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

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
	// Dest — куди потік потрапляє: "" = ліквідний портфель, "npf:<id>" =
	// пенсійний рахунок. Знак від цього НЕ залежить: внесок лишається
	// витратою, бо з ліквідного боку він і є витратою; призначення каже
	// лише, чи гроші зникли, чи стали іншим капіталом.
	Dest string
	// Uses — на що ці гроші МОЖУТЬ піти: "" = будь-куди, інакше перелік
	// кошиків через кому (domain.ParsePlanUses). Дозвіл, а не поділ:
	// розмір вирізки лишається за стелями наповнення, і другий відсоток
	// тут був би другим означенням того самого числа.
	//
	// Має сенс лише в доходу — витрачені гроші нікуди не «можуть піти», і
	// API таке поєднання відхиляє (та сама межа, що в Dest, лише
	// дзеркальна).
	Uses string
	Note string
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

// PlanFlowRevision — стан потоку на момент правки (міграція 0026).
//
// Журнал існує заради одного питання: «скільки план давав У ТРАВНІ» —
// відповідь на нього мусить читатися, а не виводитись із того, яким план
// став сьогодні. Op каже, що саме сталося; Flow — знімок ПІСЛЯ правки, а
// для delete останній відомий стан.
type PlanFlowRevision struct {
	ID        int64
	FlowID    int64
	ChangedAt time.Time
	Op        string // seed | create | update | delete
	Flow      PlanFlow
}

// PlanReceipt — відмітка про фактичне надходження за місяць (міграція 0027).
//
// Факт до плану, якого доти не було: «факт» на картці «План проти факту»
// означав поповнення портфеля, а не те, чи прийшла зарплата. Amount —
// ВАЛОВА сума в мінорних, завжди >= 0; нуль легальний і означає «не
// прийшло», тож відрізняти його від відсутнього рядка обов'язково.
//
// FlowID = 0 — «інше», позаплановий дохід. Лише в цьому випадку читається
// InvestBP: відмітка, прив'язана до потоку, бере частку з самого потоку
// (для минулого — з його тодішньої ревізії), і саме тому пізніша правка
// частки не переписує вже записану історію.
type PlanReceipt struct {
	ID       int64
	FlowID   int64
	Month    string // YYYY-MM
	Name     string
	Amount   int64
	Currency string
	InvestBP int64
	// Uses читається за тим самим правилом, що й InvestBP: лише в «іншого».
	Uses string
	Note string
}

const (
	planRevCreate = "create"
	planRevUpdate = "update"
	planRevDelete = "delete"
)

// journalPlanFlow — один рядок журналу в ТІЙ САМІЙ транзакції, що й сама
// мутація. Порізно вони рано чи пізно розійшлися б, і розійшлися б тихо:
// потік змінився, а історія показувала б стару суму як чинну.
func journalPlanFlow(ctx context.Context, tx *sql.Tx, pid int64, at time.Time, op string, f PlanFlow) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO plan_flow_revisions
		(portfolio_id, flow_id, changed_at, op, name, kind, amount, currency, cadence,
		 from_date, until_date, growth_bp, invest_bp, dest, uses, note)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		pid, f.ID, at.UTC().Format(time.RFC3339), op, f.Name, f.Kind, f.Amount, f.Currency,
		f.Cadence, string(f.FromDate), string(f.UntilDate), f.GrowthBP, f.InvestBP,
		f.Dest, f.Uses, f.Note)
	return err
}

func (s *Store) AddPlanFlow(ctx context.Context, f PlanFlow) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // відкат після Commit — no-op
	res, err := tx.ExecContext(ctx, `INSERT INTO plan_flows
		(portfolio_id, name, kind, amount, currency, cadence, from_date, until_date,
		 growth_bp, invest_bp, dest, uses, note)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		s.pid, f.Name, f.Kind, f.Amount, f.Currency, f.Cadence, string(f.FromDate),
		string(f.UntilDate), f.GrowthBP, f.InvestBP, f.Dest, f.Uses, f.Note)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	f.ID = id
	if err := journalPlanFlow(ctx, tx, s.pid, time.Now(), planRevCreate, f); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// UpdatePlanFlow переписує потік, зберігаючи id.
func (s *Store) UpdatePlanFlow(ctx context.Context, f PlanFlow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	res, err := tx.ExecContext(ctx, `UPDATE plan_flows SET
		name=?, kind=?, amount=?, currency=?, cadence=?, from_date=?, until_date=?,
		growth_bp=?, invest_bp=?, dest=?, uses=?, note=? WHERE id=? AND portfolio_id=?`,
		f.Name, f.Kind, f.Amount, f.Currency, f.Cadence, string(f.FromDate),
		string(f.UntilDate), f.GrowthBP, f.InvestBP, f.Dest, f.Uses, f.Note, f.ID, s.pid)
	if err != nil {
		return err
	}
	if err := affectedOne(res, "плановий потік"); err != nil {
		return err
	}
	if err := journalPlanFlow(ctx, tx, s.pid, time.Now(), planRevUpdate, f); err != nil {
		return err
	}
	return tx.Commit()
}

// Видалення теж через affectedOne: доти DELETE за неіснуючим id віддавав
// 204, тобто «видалено» звучало однаково і коли справді видалили, і коли
// видаляти було нічого.
func (s *Store) DeletePlanFlow(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	// Спершу читаємо, що саме зникає: без цього рядок журналу не зміг би
	// відповісти, ЩО видалили, і видалення не відрізнялось би від
	// «такого потоку ніколи не було».
	var f PlanFlow
	var from, until string
	err = tx.QueryRowContext(ctx, `SELECT id, name, kind, amount, currency, cadence,
		from_date, until_date, growth_bp, invest_bp, dest, uses, note
		FROM plan_flows WHERE id=? AND portfolio_id=?`, id, s.pid).
		Scan(&f.ID, &f.Name, &f.Kind, &f.Amount, &f.Currency, &f.Cadence,
			&from, &until, &f.GrowthBP, &f.InvestBP, &f.Dest, &f.Uses, &f.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("плановий потік %w", ErrNotFound)
	}
	if err != nil {
		return err
	}
	f.FromDate, f.UntilDate = domain.Date(from), domain.Date(until)

	res, err := tx.ExecContext(ctx, `DELETE FROM plan_flows WHERE id=? AND portfolio_id=?`, id, s.pid)
	if err != nil {
		return err
	}
	if err := affectedOne(res, "плановий потік"); err != nil {
		return err
	}
	if err := journalPlanFlow(ctx, tx, s.pid, time.Now(), planRevDelete, f); err != nil {
		return err
	}
	return tx.Commit()
}

// ListPlanFlowRevisions — журнал від since (включно), у хронологічному
// порядку. Порядок тут не косметика: реконструкція бере ОСТАННЮ ревізію
// кожного потоку до заданого моменту, і покладається саме на нього.
//
// id як вторинне сортування — щоб дві правки в ту саму секунду лишались
// у тому порядку, у якому вони сталися.
func (s *Store) ListPlanFlowRevisions(ctx context.Context, since time.Time) ([]PlanFlowRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, flow_id, changed_at, op,
		name, kind, amount, currency, cadence, from_date, until_date,
		growth_bp, invest_bp, dest, uses, note
		FROM plan_flow_revisions WHERE portfolio_id=? AND changed_at >= ? ORDER BY changed_at, id`,
		s.pid, since.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanFlowRevision
	for rows.Next() {
		var r PlanFlowRevision
		var at, from, until string
		if err := rows.Scan(&r.ID, &r.FlowID, &at, &r.Op, &r.Flow.Name, &r.Flow.Kind,
			&r.Flow.Amount, &r.Flow.Currency, &r.Flow.Cadence, &from, &until,
			&r.Flow.GrowthBP, &r.Flow.InvestBP, &r.Flow.Dest, &r.Flow.Uses,
			&r.Flow.Note); err != nil {
			return nil, err
		}
		// Час, який не розібрався, — нульовий, і реконструкція побачить
		// його як «було завжди». Це чесніше за падіння на одному кривому
		// рядку: журнал читають заради картинки, а не заради грошей.
		r.ChangedAt, _ = time.Parse(time.RFC3339, at) //nolint:errcheck
		r.Flow.ID = r.FlowID
		r.Flow.FromDate, r.Flow.UntilDate = domain.Date(from), domain.Date(until)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ListPlanFlows(ctx context.Context) ([]PlanFlow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, kind, amount, currency, cadence,
		from_date, until_date, growth_bp, invest_bp, dest, uses, note
		FROM plan_flows WHERE portfolio_id=? ORDER BY from_date, id`, s.pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanFlow
	for rows.Next() {
		var f PlanFlow
		var from, until string
		if err := rows.Scan(&f.ID, &f.Name, &f.Kind, &f.Amount, &f.Currency, &f.Cadence,
			&from, &until, &f.GrowthBP, &f.InvestBP, &f.Dest, &f.Uses, &f.Note); err != nil {
			return nil, err
		}
		f.FromDate, f.UntilDate = domain.Date(from), domain.Date(until)
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) AddPlanAction(ctx context.Context, a PlanAction) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO plan_actions
		(portfolio_id, date, type, usd_bp, eur_bp, amount, currency, rate_bp, months, name, note)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		s.pid, string(a.Date), a.Type, a.USDBP, a.EURBP, a.Amount, a.Currency, a.RateBP, a.Months, a.Name, a.Note)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdatePlanAction переписує дію, зберігаючи id.
func (s *Store) UpdatePlanAction(ctx context.Context, a PlanAction) error {
	res, err := s.db.ExecContext(ctx, `UPDATE plan_actions SET
		date=?, type=?, usd_bp=?, eur_bp=?, amount=?, currency=?, rate_bp=?, months=?, name=?, note=?
		WHERE id=? AND portfolio_id=?`,
		string(a.Date), a.Type, a.USDBP, a.EURBP, a.Amount, a.Currency, a.RateBP, a.Months, a.Name, a.Note, a.ID, s.pid)
	if err != nil {
		return err
	}
	return affectedOne(res, "планова дія")
}

func (s *Store) DeletePlanAction(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM plan_actions WHERE id=? AND portfolio_id=?`, id, s.pid)
	if err != nil {
		return err
	}
	return affectedOne(res, "планова дія")
}

// --- відмітки надходжень (0027) ---
//
// Простий CRUD, як у ReserveOp, і БЕЗ журналу ревізій — на відміну від
// самих потоків. Різниця принципова: план — це намір, який мінявся в часі,
// і питання «яким він був у травні» має відповідь лише в журналі. Факт же
// або записаний правильно, або виправлений; історії в нього немає.

// planReceiptConflict перекладає порушення часткового UNIQUE-індексу в
// сентинел. Рядком, а не типом драйвера, — щоб internal/store не тягнув
// go-sqlite3 у сигнатури заради однієї перевірки; текст SQLite для UNIQUE
// стабільний десятиліттями.
func planReceiptConflict(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("надходження за цей місяць уже відмічено: %w", ErrConflict)
	}
	return err
}

// Приналежність flow_id тут НЕ перевіряється, на відміну від goal_ops і
// debt_ops: ключа на plan_flows немає навмисно (0027), відмітка мусить
// пережити свій потік, і «потоку не знайдено» — законний стан, а не чужий
// портфель. Витоку це не дає: рядок все одно лягає в свій portfolio_id.
func (s *Store) AddPlanReceipt(ctx context.Context, r PlanReceipt) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO plan_receipts
		(portfolio_id, flow_id, month, name, amount, currency, invest_bp, uses, note)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		s.pid, r.FlowID, r.Month, r.Name, r.Amount, r.Currency, r.InvestBP, r.Uses, r.Note)
	if err != nil {
		return 0, planReceiptConflict(err)
	}
	return res.LastInsertId()
}

// UpdatePlanReceipt переписує відмітку, зберігаючи id. FlowID і Month теж
// переписуються: перенести відмітку на інший місяць — це правка, а не
// «видалити й завести», і другий шлях загубив би id, на який дивиться UI.
func (s *Store) UpdatePlanReceipt(ctx context.Context, r PlanReceipt) error {
	res, err := s.db.ExecContext(ctx, `UPDATE plan_receipts SET
		flow_id=?, month=?, name=?, amount=?, currency=?, invest_bp=?, uses=?, note=?
		WHERE id=? AND portfolio_id=?`,
		r.FlowID, r.Month, r.Name, r.Amount, r.Currency, r.InvestBP, r.Uses, r.Note, r.ID, s.pid)
	if err != nil {
		return planReceiptConflict(err)
	}
	return affectedOne(res, "відмітка надходження")
}

func (s *Store) DeletePlanReceipt(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM plan_receipts WHERE id=? AND portfolio_id=?`, id, s.pid)
	if err != nil {
		return err
	}
	return affectedOne(res, "відмітка надходження")
}

// ListPlanReceipts — усі відмітки, за місяцем і id. Порядок за id вторинний
// із тієї ж причини, що й у журналі ревізій: «іншого» за місяць буває
// кілька, і сталий порядок робить документ відтворюваним.
func (s *Store) ListPlanReceipts(ctx context.Context) ([]PlanReceipt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, flow_id, month, name, amount,
		currency, invest_bp, uses, note FROM plan_receipts WHERE portfolio_id=? ORDER BY month, id`, s.pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanReceipt
	for rows.Next() {
		var r PlanReceipt
		if err := rows.Scan(&r.ID, &r.FlowID, &r.Month, &r.Name, &r.Amount,
			&r.Currency, &r.InvestBP, &r.Uses, &r.Note); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ListPlanActions(ctx context.Context) ([]PlanAction, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, date, type, usd_bp, eur_bp, amount,
		currency, rate_bp, months, name, note FROM plan_actions WHERE portfolio_id=? ORDER BY date, id`, s.pid)
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
