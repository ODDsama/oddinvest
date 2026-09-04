// POST /api/spend — «Ціна покупки»: що ця витрата коштує насправді.
//
// ПИТАННЯ, ЯКОГО ЗАСТОСУНОК ДОТИ НЕ СТАВИВ. Він умів сказати, що
// станеться, якщо КУПИТИ ПАПІР (POST /api/whatif), і мовчав про те, що
// станеться, якщо просто ВИТРАТИТИ. Два контури — борговий і портфельний
// — жили поруч і не розмовляли: борг знав свою дату виходу з ліміту,
// портфель знав свою дохідність, а «скільки мені коштує цей холодильник»
// не належало жодному з них.
//
// ВЛАСНОЇ АРИФМЕТИКИ ТУТ НЕМАЄ. Частки, драбину, дюрацію, концентрацію,
// подушку, точку незалежності, місячний план і ПРОХІД ВИХОДУ З ЛІМІТУ
// рахує buildStateWith — той самий код, що й для справжнього стану.
// Порожня витрата мусить дати документ, невідрізнимий від /api/summary, і
// на це є тест: саме він робить законним віднімання на фронтенді.
//
// ЧОМУ ОКРЕМИЙ МАРШРУТ, А НЕ ПОЛЕ В /api/whatif. Чотири причини, і три з
// них уже записані при handlers_policy_preview.go. Whatif типово тягне
// ЗБЕРЕЖЕНИЙ план купівель, а питання тут — про портфель, як він є
// сьогодні. Витрата не є рядком plan_buys: вона нічого не набуває — ні
// кількості, ні ціни за одиницю, ні ISIN, — тож basketLine дістав би
// чотири мертві поля (CLAUDE.md §3). Форми відповідей різні: там
// «after + basket», тут «after + cost». І питання в них різні аж до того,
// ЩО САМЕ доводиться збирати двічі: whatif будує другий стан заради
// винуватця нестачі, а тут — заради альтернативи, яку не можна міряти в
// світі, де витрата вже сталась (довід при виклику).
//
// МІГРАЦІЇ НЕМАЄ, І ЦЕ НЕ ЛІНОЩІ. Витрата живе рівно один запит — той
// самий статус, що в чернетки кошика. Відмова README від кошика в
// localStorage сюди не тягнеться: той довід був про ПЛАН із датою, який
// конкурував би з plan_buys за право бути джерелом правди, а відповідь,
// що дана один раз і нікуди не записана, ні з чим не конкурує.
//
// ЦЕ НЕ ДРУГИЙ СПОСІБ ЗАПИСАТИ ВИТРАТУ. Справді витрачені гроші
// описуються зняттям у «Грошах» або рухом боргу в «Боргах»; тут лише
// питання. Якби відповідь ще й писалась, у застосунку зʼявилось би два
// журнали одних грошей.
//
// НЕ СУПЕРЕЧИТЬ ВІДМОВІ «АВТОМАТИЧНИХ МІСЯЧНИХ ВИТРАТ». Та відмова про
// ВИВЕДЕННЯ вартості місяця життя зі зняттів — тобто про число, яке
// застосунок порахував би сам із випадкових рухів. Тут одна названа
// покупка, яку людина вводить руками, і monthly_expenses не чіпається
// взагалі: на це є тест, який робить ту відмову механічною, а не
// обіцянкою.
//
// ЩО ЦЕ ПРЕВʼЮ НЕ РУХАЄ. GET /api/payoff і GET /api/route читають борги
// власними шляхами й гіпотези не бачать, тож черга погашення й маршрут
// грошей лишаються сьогоднішніми. Сказано тут, щоб читач не припустив
// зворотного.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

const (
	spendCash        = "cash"
	spendCard        = "card"
	spendInstallment = "installment"
)

type spendReq struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
	Date     string `json:"date"`
	// Pay — cash | card | installment. Порожньо = cash.
	Pay    string `json:"pay"`
	Broker string `json:"broker"`
	CardID int64  `json:"card_id"`
	// Installment — умови розстрочки, у ТОМУ САМОМУ вигляді, що й при
	// POST /api/debts: розбирає їх спільний debtFromReq, тож превʼю не
	// може прийняти те, що запис відхилить.
	Installment *debtReq `json:"installment"`
	Note        string   `json:"note"`
}

// spendAlternative — що ці гроші заробили б за рік, якби пішли в найкраще
// доступне зараз. Береться ПЕРШИЙ рядок «Що купити», який справді можна
// купити, за власним порядком людини — другого рейтингу тут не заводиться.
type spendAlternative struct {
	Kind       string  `json:"kind"`
	Label      string  `json:"label"`
	Currency   string  `json:"currency"`
	RealPct    float64 `json:"real_pct"`
	NominalPct float64 `json:"nominal_pct,omitempty"`
	YieldBasis string  `json:"yield_basis,omitempty"`
	YearUAH    float64 `json:"year_uah"`
}

// spendCredit — чесна ціна кредиту. Жодне число тут не народжується:
// ставку рахує domain.DebtEffectiveRate, комісії — InstallmentSchedule.
type spendCredit struct {
	// Basis — "graph" (XIRR за графіком розстрочки) або "compound"
	// (ставка картки після грейсу з місячною капіталізацією).
	Basis    string  `json:"basis"`
	APRPct   float64 `json:"apr_pct"`
	ExtraUAH float64 `json:"extra_uah,omitempty"`
	// Prepay — що буде з комісією при достроковому (domain.DebtPrepayBasis).
	Prepay string `json:"prepay,omitempty"`
	// DueDate / DaysToDue — для картки: доки борг безкоштовний.
	DueDate   string `json:"due_date,omitempty"`
	DaysToDue int    `json:"days_to_due,omitempty"`
}

type spendCost struct {
	Alternative    *spendAlternative `json:"alternative,omitempty"`
	AlternativeWhy string            `json:"alternative_why,omitempty"`
	Credit         *spendCredit      `json:"credit,omitempty"`
	CreditWhy      string            `json:"credit_why,omitempty"`
}

type spendDoc struct {
	After *state.Doc `json:"after"`
	Cost  spendCost  `json:"cost"`
}

// spendPlan — розібраний запит: сама гіпотеза плюс те, що потрібно для
// секції «ціна кредиту».
type spendPlan struct {
	what   hypothetical
	amount int64
	cur    string
	date   domain.Date
	pay    string
	// card / hyp — картка, на яку лягає покупка, і гіпотетична розстрочка.
	card *domain.Debt
	hyp  *domain.Debt
}

func (s *Server) handleSpend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	today := domain.NewDate(now)

	var req spendReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	plan, err := s.spendPlanFrom(ctx, req, today)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	after, err := s.buildStateWith(ctx, now, plan.what)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	out := spendDoc{After: after}
	if plan.amount > 0 {
		// Альтернатива міряється в СЬОГОДНІШНЬОМУ світі, тобто над станом
		// БЕЗ гіпотези, — і це не педантизм, а виправлення замикання,
		// спійманого на живому запуску. Над станом «після» помічник
		// побачив щойно взяту розстрочку під 50% і чесно поставив
		// найкращим рядком «погасити цей борг»: питання «від чого ці
		// гроші відмовляються» діставало відповідь «від того, щоб
		// скасувати самих себе». Те саме сталось би з карткою.
		//
		// Друга причина того самого: доступність. «Чи вистачає» мусить
		// питатись про гроші, які ще Є, а не про залишок після витрати.
		//
		// Ціна — другий повний прохід buildState, і вона свідома: рівно
		// так само платить /api/whatif, і з тим самим доводом — питання
		// справді два, а не одне.
		before, err := s.buildState(ctx, now)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		out.Cost.Alternative, out.Cost.AlternativeWhy = s.spendAlternative(ctx, now, before, plan)
		out.Cost.Credit, out.Cost.CreditWhy = spendCreditOf(plan, after)
	}
	writeJSON(w, http.StatusOK, out)
}

// spendPlanFrom розбирає запит у гіпотезу.
//
// ТРИ ВІДМОВИ, І КОЖНА НАЗИВАЄ СВІЙ ДОВІД. Усі три стережуть від
// відповіді, яка виглядала б правильною й нею не була: мовчазна
// неправильність тут гірша за 400.
func (s *Server) spendPlanFrom(ctx context.Context, req spendReq, today domain.Date) (*spendPlan, error) {
	p := &spendPlan{cur: orUAH(strings.TrimSpace(req.Currency)), date: today}
	p.pay = strings.TrimSpace(req.Pay)
	if p.pay == "" {
		p.pay = spendCash
	}
	if p.pay != spendCash && p.pay != spendCard && p.pay != spendInstallment {
		return nil, fmt.Errorf("невідомий спосіб оплати %q: буває %q, %q або %q",
			p.pay, spendCash, spendCard, spendInstallment)
	}

	if raw := strings.TrimSpace(req.Amount); raw != "" {
		v, err := domain.ParseDecimalToMinor(raw, p.cur)
		if err != nil {
			return nil, fmt.Errorf("сума: %w", err)
		}
		if v < 0 {
			return nil, errors.New("витрата не буває відʼємною: це надходження, і йому місце в «Грошах»")
		}
		p.amount = v
	}
	// Порожня витрата законна й дає документ, рівний /api/summary: саме на
	// ній стоїть головна гарантія прийому, і відхиляти її означало б
	// зробити цю гарантію неперевірною.
	if p.amount == 0 {
		return p, nil
	}

	if d := strings.TrimSpace(req.Date); d != "" {
		p.date = domain.Date(d)
	}
	if p.date.Before(today) {
		return nil, errors.New(
			"витрата, яка вже сталась, описується операцією, а не питанням: " +
				"занеси її рухом у «Грошах» або в «Боргах»")
	}
	future := p.date.After(today)

	switch p.pay {
	case spendCash:
		return p, s.spendFromCash(p, req, future)
	case spendCard:
		return p, s.spendOnCard(ctx, p, req, today, future)
	default:
		return p, spendAsInstallment(p, req, today, future)
	}
}

// spendFromCash — зняття з рахунку або разова майбутня витрата.
//
// АСИМЕТРІЯ ДАТИ ТУТ НЕ ВИПАДКОВА. Гаманець сумує рухи БЕЗ фільтра за
// датою (state_builder.go), тож майбутнє зняття, записане рухом, зменшило
// б сьогоднішній баланс — тобто відповіло б на інше питання. Тому воно
// стає разовою витратою плану: рухає прогноз, ціль і точку незалежності й
// не чіпає сьогоднішніх грошей. Та сама межа, що описана в
// state_plan_buys.go для датованої купівлі.
func (s *Server) spendFromCash(p *spendPlan, req spendReq, future bool) error {
	if future {
		p.what.flows = []store.PlanFlow{{
			// ID від'ємний: справжні йдуть AUTOINCREMENT, тож збігу бути
			// не може, а нуль читався б як «потік без запису».
			ID: -1, Name: spendName(req), Kind: "expense", Cadence: "once",
			Amount: p.amount, Currency: p.cur, FromDate: p.date, UntilDate: p.date,
		}}
		return nil
	}
	p.what.cash = []store.Deposit{{
		Date: p.date, Amount: -p.amount, Currency: p.cur,
		Broker: strings.TrimSpace(req.Broker), Note: spendName(req),
	}}
	return nil
}

// spendOnCard — покупка на вже наявну картку.
func (s *Server) spendOnCard(ctx context.Context, p *spendPlan, req spendReq,
	today domain.Date, future bool) error {
	if future {
		return errors.New(
			"картковий контур майбутніх покупок не бачить: баланс картки міряється " +
				"звіркою й рухами до сьогодні, а вихід із ліміту вже припускає " +
				"МІСЯЧНИЙ темп витрат — разова майбутня покупка порахувалась би проти нього двічі")
	}
	debts, err := s.st.ListDebts(ctx)
	if err != nil {
		return err
	}
	for i := range debts {
		if debts[i].ID == req.CardID && debts[i].IsCard() {
			p.card = &debts[i]
			break
		}
	}
	if p.card == nil {
		return errors.New("не знайшов такої картки: покупку в борг треба покласти на конкретну")
	}
	marks, err := s.st.ListDebtMarks(ctx)
	if err != nil {
		return err
	}
	// Звірка — це ВИМІРЯНИЙ залишок, і CardState відкидає рухи, датовані
	// не пізніше за неї (domain/debt.go). Покупка на таку дату була б
	// мовчки проковтнута: відповідь прийшла б із кодом 200 і без сліду
	// витрати. Краще відмова з причиною.
	for _, m := range marks {
		if m.DebtID == p.card.ID && !m.Date.After(today) && !p.date.After(m.Date) {
			return fmt.Errorf(
				"остання звірка «%s» датована %s, а звірка — це виміряний залишок: "+
					"покупку цим днем картковий контур не побачить", p.card.Name, m.Date)
		}
	}
	p.what.debtOps = []domain.DebtOp{{
		ID: -1, DebtID: p.card.ID, Date: p.date,
		Kind: domain.DebtOpDraw, Amount: p.amount, Note: spendName(req),
	}}
	return nil
}

// spendAsInstallment — покупка, яка стає новою розстрочкою.
func spendAsInstallment(p *spendPlan, req spendReq, today domain.Date, future bool) error {
	if future {
		return errors.New(
			"розстрочку майбутньою датою порахувати нема з чого: її графік починається " +
				"від першого платежу, а картковий контур бачить лише те, що вже сталось")
	}
	if req.Installment == nil {
		return errors.New("умови розстрочки не названі: без ставки й кількості платежів ціни в неї немає")
	}
	r := *req.Installment
	if strings.TrimSpace(r.Kind) == "" {
		r.Kind = domain.DebtInstallment
	}
	if strings.TrimSpace(r.Name) == "" {
		r.Name = spendName(req)
	}
	if strings.TrimSpace(r.Principal) == "" {
		r.Principal = req.Amount
	}
	if strings.TrimSpace(r.Currency) == "" {
		r.Currency = p.cur
	}
	if strings.TrimSpace(r.FirstPaymentDate) == "" {
		r.FirstPaymentDate = string(today.AddMonths(1))
	}
	// Той самий розбір, що й у запису: превʼю, яке приймає відхилене,
	// показувало б числа, яких не може бути.
	d, err := debtFromReq(r)
	if err != nil {
		return err
	}
	if d.Kind != domain.DebtInstallment {
		return errors.New("тут заводиться саме розстрочка: картка вже існує, і покупку на неї кладуть через pay=card")
	}
	d.ID = -1
	d.OpenedDate = today
	p.hyp = &d
	p.what.debts = []domain.Debt{d}
	return nil
}

func spendName(req spendReq) string {
	if n := strings.TrimSpace(req.Note); n != "" {
		return n
	}
	return "покупка"
}

// spendAlternative — найкращий доступний рядок «Що купити» й скільки ці
// гроші заробили б у ньому за рік.
//
// Бере ТОЙ САМИЙ рейтинг, що й помічник, і в тому самому порядку, який
// задала людина. Другого впорядкування тут не заводиться: два різні
// «найкраще доступне» на сусідніх екранах — це вже не порівняння.
//
// Множення одне, і воно на сервері: у браузері воно стало б другою копією
// арифметики, історію якої розказано в шапці handlers_whatif.go.
func (s *Server) spendAlternative(ctx context.Context, now time.Time,
	doc *state.Doc, p *spendPlan) (*spendAlternative, string) {
	sugg, err := s.reinvestSuggestions(ctx, now, doc)
	if err != nil {
		return nil, "помічник не відповів — порівнювати нема з чим"
	}
	for _, g := range sugg {
		if !g.CanBuy {
			continue
		}
		uah := float64(p.amount) / 100
		if p.cur != money.UAH {
			if rate := (doc.Rates)[p.cur]; rate > 0 {
				uah = float64(p.amount) / 100 * rate
			}
		}
		return &spendAlternative{
			Kind: g.Kind, Label: g.Label, Currency: g.Currency,
			RealPct: g.RealPct, NominalPct: g.NominalPct, YieldBasis: g.YieldBasis,
			YearUAH: round2(uah * g.RealPct / 100),
		}, ""
	}
	return nil, "у «Що купити» немає жодного доступного рядка — нема з чим порівняти"
}

// spendCreditOf — чесна ціна кредиту, узята з domain.
//
// ДЛЯ КАРТКИ ОЧІКУВАНОЇ ВАРТОСТІ НЕ РАХУЄМО. Вона залежить від того, чи
// закриють баланс до розрахункової дати, а цього застосунок знати не
// може. Тому віддається УМОВНА пара: доки є грейс — нуль, після нього —
// ставка з місячною капіталізацією. Приписати сюди ймовірність повернення
// означало б вигадати рівно те число, від якого застерігає весь застосунок.
func spendCreditOf(p *spendPlan, doc *state.Doc) (*spendCredit, string) {
	switch {
	case p.hyp != nil:
		apr, basis := domain.DebtEffectiveRate(*p.hyp, 0)
		var fee int64
		for _, pay := range domain.InstallmentSchedule(*p.hyp) {
			fee += pay.Fee
		}
		return &spendCredit{
			Basis: basis, APRPct: round2(apr), ExtraUAH: round2(float64(fee) / 100),
			Prepay: domain.DebtPrepayBasis(*p.hyp),
		}, ""
	case p.card != nil:
		apr, basis := domain.DebtEffectiveRate(*p.card, 0)
		out := &spendCredit{Basis: basis, APRPct: round2(apr)}
		if doc != nil && doc.Debt != nil {
			for _, c := range doc.Debt.Cards {
				if c.Name == p.card.Name {
					out.DueDate, out.DaysToDue = c.DueDate, c.DaysToDue
					break
				}
			}
		}
		return out, ""
	default:
		return nil, ""
	}
}
