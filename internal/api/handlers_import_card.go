// Імпорт виписки КАРТКИ — гілка /api/import для профілю з debt_id.
//
// Виписка банку інша за брокерську трьома речами, і кожна тут названа:
//
//   - у неї два журнали-адресати замість трьох: надходження на картку
//     (card_in) стає платежем по картці, зняття готівки чи переказ
//     (card_cash) — готівкою з ліміту під відсотком; ПОКУПКИ (card_out)
//     не пишуться нікуди — вони вже сидять у балансі звірки, і журнал
//     кожної покупки нічого не додав би (довід фази 22: витрати
//     ВИМІРЮЮТЬСЯ зі звірок). Вони лише сумуються по місяцях, щоб людина
//     бачила, скільки витратила, поруч із «заявлено» й «виміряно зі
//     звірок»;
//   - у ній є залишок після кожної операції, тобто ЗВІРКА, — але без
//     суми виписки (statement_due), якої CSV банку не несе. Звірку без
//     неї писати не можна: StatementDue = 0 мовчки сховав би задачу
//     «внести до розрахункової дати». Тому суму виписки вводить людина в
//     превʼю (mark_due), і лише з нею пишеться звірка;
//   - знак залишку в файлі — не наш: у кредитки «залишок» банк пише або
//     як власні гроші плюс ліміт, або як власні мінус використане.
//     Розбирач знак не вгадує: превʼю показує число як у файлі, а людина
//     підтверджує або виправляє його (mark_balance).
//
// Дубль руху — та сама картка, дата, вид і сума. Точніше не вийде: у
// debt_ops немає ні часу, ні опису.
package api

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/imports"
)

// importCard — картковий блок відповіді /api/import.
type importCard struct {
	DebtID int64  `json:"debt_id"`
	Name   string `json:"name"`
	// BalanceRaw/BalanceDate — залишок після останньої операції файлу,
	// зі знаком ФАЙЛУ; порожньо, коли колонки залишку немає.
	BalanceRaw  string `json:"balance_raw,omitempty"`
	BalanceDate string `json:"balance_date,omitempty"`
	// BalanceMinor — те саме число для поля превʼю; вказівник, бо нуль —
	// законний залишок.
	BalanceMinor *int64 `json:"balance_minor,omitempty"`
	// Spend — витрати (card_out), надходження й готівка по місяцях.
	Spend []importCardMonth `json:"spend"`
	// CashSincePrevMark — готівка з файлу після попередньої звірки: це
	// non_grace нової звірки, коли її пишуть.
	CashSincePrevMark float64 `json:"cash_since_prev_mark"`
	PrevMarkDate      string  `json:"prev_mark_date,omitempty"`
	// MarkWritten/MarkNote — чи записано звірку й чому ні.
	MarkWritten bool   `json:"mark_written"`
	MarkNote    string `json:"mark_note,omitempty"`
}

type importCardMonth struct {
	Month   string  `json:"month"`
	OutUAH  float64 `json:"out_uah"`
	InUAH   float64 `json:"in_uah"`
	CashUAH float64 `json:"cash_uah"`
}

// cardImport — стан одного імпорту виписки картки.
type cardImport struct {
	s    *Server
	card domain.Debt
	seen map[string]bool
	// prevMark — остання звірка до імпорту; готівка після неї йде в
	// non_grace нової.
	prevMark domain.Date
	months   map[string]*importCardMonth
	lastBal  int64
	lastDate domain.Date
	hasBal   bool
	cashNew  int64
	// Що просить людина (з запиту): суму виписки й, за бажанням, знак/
	// значення залишку.
	markDue     *int64
	markBalance *int64
}

func (s *Server) newCardImport(ctx context.Context, debtID int64, q url.Values) (*cardImport, error) {
	debts, err := s.st.ListDebts(ctx)
	if err != nil {
		return nil, err
	}
	var card *domain.Debt
	for i := range debts {
		if debts[i].ID == debtID {
			card = &debts[i]
		}
	}
	if card == nil || !card.IsCard() {
		return nil, fmt.Errorf("профіль привʼязаний до картки #%d, якої немає", debtID)
	}
	ops, err := s.st.ListDebtOps(ctx)
	if err != nil {
		return nil, err
	}
	ci := &cardImport{s: s, card: *card, seen: map[string]bool{}, months: map[string]*importCardMonth{}}
	for _, op := range ops {
		if op.DebtID == debtID {
			ci.seen[fmt.Sprintf("%s|%s|%d", op.Date, op.Kind, op.Amount)] = true
		}
	}
	marks, err := s.st.ListDebtMarks(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range marks {
		if m.DebtID == debtID && m.Date.After(ci.prevMark) {
			ci.prevMark = m.Date
		}
	}
	if ci.markDue, err = minorParam(q, "mark_due"); err != nil {
		return nil, err
	}
	if ci.markBalance, err = minorParam(q, "mark_balance"); err != nil {
		return nil, err
	}
	return ci, nil
}

// minorParam — десяткова сума з запиту в мінорних одиницях; nil, коли
// параметра немає.
func minorParam(q url.Values, key string) (*int64, error) {
	raw := strings.TrimSpace(q.Get(key))
	if raw == "" {
		return nil, nil
	}
	raw = strings.NewReplacer(" ", "", " ", "", ",", ".").Replace(raw)
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, fmt.Errorf("%s: очікували число, маємо %q", key, q.Get(key))
	}
	v := int64(math.Round(f * 100))
	return &v, nil
}

func (c *cardImport) month(d domain.Date) *importCardMonth {
	k := string(d)[:7]
	m := c.months[k]
	if m == nil {
		m = &importCardMonth{Month: k}
		c.months[k] = m
	}
	return m
}

// take — один рядок виписки. Повертає, чи рух уже був.
func (c *cardImport) take(ctx context.Context, row imports.Row, dry bool) (bool, error) {
	if row.HasBalance && !row.Date.Before(c.lastDate) {
		c.lastBal, c.lastDate, c.hasBal = row.Balance, row.Date, true
	}
	m := c.month(row.Date)
	switch row.Kind {
	case "card_out":
		// Покупка не пишеться (шапка файла) — лише сумується. «Вже є»
		// тут означає «врахована», і превʼю підписує її саме так.
		m.OutUAH = round2(m.OutUAH + float64(row.Amount)/100)
		return true, nil
	case "card_in":
		m.InUAH = round2(m.InUAH + float64(row.Amount)/100)
	case "card_cash":
		m.CashUAH = round2(m.CashUAH + float64(row.Amount)/100)
		if row.Date.After(c.prevMark) {
			c.cashNew += row.Amount
		}
	}
	kind := domain.DebtOpPayment
	if row.Kind == "card_cash" {
		kind = domain.DebtOpCash
	}
	key := fmt.Sprintf("%s|%s|%d", row.Date, kind, row.Amount)
	if c.seen[key] {
		return true, nil
	}
	c.seen[key] = true
	if dry {
		return false, nil
	}
	_, err := c.s.st.AddDebtOp(ctx, domain.DebtOp{DebtID: c.card.ID, Date: row.Date,
		Kind: kind, Amount: row.Amount, Note: "виписка"})
	return false, err
}

// finish — блок відповіді й, коли просили і не dry, звірка.
func (c *cardImport) finish(ctx context.Context, dry bool) (*importCard, error) {
	out := &importCard{DebtID: c.card.ID, Name: c.card.Name, Spend: []importCardMonth{},
		CashSincePrevMark: round2(float64(c.cashNew) / 100), PrevMarkDate: string(c.prevMark)}
	for _, m := range c.months {
		out.Spend = append(out.Spend, *m)
	}
	sortMonths(out.Spend)
	if c.hasBal {
		bal := c.lastBal
		out.BalanceRaw = money.New(bal, c.card.Currency).Display()
		out.BalanceDate = string(c.lastDate)
		out.BalanceMinor = &bal
	}
	switch {
	case c.markDue == nil:
		out.MarkNote = "звірка не записана: суму виписки (mark_due) не введено"
	case !c.hasBal && c.markBalance == nil:
		out.MarkNote = "звірка не записана: у файлі немає залишку, а mark_balance не введено"
	case dry:
		out.MarkNote = "перегляд: звірка буде записана при імпорті"
	default:
		bal := c.lastBal
		if c.markBalance != nil {
			bal = *c.markBalance
		}
		date := c.lastDate
		if date == "" {
			date = c.prevMark
		}
		if !date.After(c.prevMark) && c.prevMark != "" {
			out.MarkNote = "звірка не записана: на " + string(date) + " вона вже є"
			return out, nil
		}
		if _, err := c.s.st.AddDebtMark(ctx, domain.DebtMark{DebtID: c.card.ID, Date: date,
			Balance: bal, StatementDue: *c.markDue, NonGrace: c.cashNew, Note: "виписка"}); err != nil {
			return nil, err
		}
		out.MarkWritten = true
	}
	return out, nil
}

func sortMonths(ms []importCardMonth) {
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0 && ms[j].Month < ms[j-1].Month; j-- {
			ms[j], ms[j-1] = ms[j-1], ms[j]
		}
	}
}
