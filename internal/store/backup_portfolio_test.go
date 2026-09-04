package store

import (
	"context"
	"encoding/json"
	"testing"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// Відновлення в ДРУГИЙ портфель, коли перший уже тримає рядки з тими
// самими id (0054).
//
// Усі таблиці ділять одну послідовність AUTOINCREMENT, а бекап тримає id —
// тож дамп головного, відновлений у портфель дружини, упирався б у
// UNIQUE(id) на першому ж рядку. Тут перевіряється рівно та ситуація: id
// зайняті УСІ, кожен рядок дістає новий, діти знаходять батьків за мапою, а
// головний портфель не змінюється байт у байт. Спільний довідник фондів при
// цьому зводиться за назвою, а не дублюється.
func TestImportIntoSecondPortfolioRemapsIDs(t *testing.T) {
	ctx := context.Background()
	st := openTest(t)

	lot, err := st.AddLot(ctx, domain.Lot{ISIN: "UA4000239016", Qty: 3,
		PricePerBond: money.New(107715, money.UAH), BuyDate: "2026-07-16", Channel: "mono"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddSale(ctx, domain.Sale{LotID: lot, SaleDate: "2026-08-01", Qty: 1,
		CleanPerBond: money.New(101000, money.UAH), Accrued: money.New(500, money.UAH)}); err != nil {
		t.Fatal(err)
	}
	dep, err := st.AddTermDeposit(ctx, domain.Deposit{Bank: "ПУМБ", Currency: "UAH",
		Principal: 10000000, RateBP: 1600, OpenDate: "2026-01-15", MaturityDate: "2027-01-15",
		Payout: domain.PayoutEnd, TaxBP: 2300})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDepositTopup(ctx, domain.DepositTopup{DepositID: dep, Date: "2026-02-15", Amount: 5000000}); err != nil {
		t.Fatal(err)
	}
	goal, err := st.AddGoal(ctx, Goal{Name: "авто", TargetAmount: 100000000, Currency: "UAH"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddGoalOp(ctx, GoalOp{GoalID: goal, Date: "2026-08-01", Amount: 1000000, Currency: "UAH"}); err != nil {
		t.Fatal(err)
	}
	card, err := st.AddDebt(ctx, domain.Debt{Name: "ПУМБ", Kind: domain.DebtCard, Currency: "UAH",
		LimitAmount: 20000000, StatementDay: 30, APRBp: 4788, MinPaymentBp: 300, OpenedDate: "2024-05-01"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDebt(ctx, domain.Debt{Name: "Холодильник", Kind: domain.DebtInstallment,
		Currency: "UAH", CardID: card, Principal: 3000000, PaymentsTotal: 9,
		FirstPaymentDate: "2026-09-30", FeeMonthBp: 199}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDebtOp(ctx, domain.DebtOp{DebtID: card, Date: "2026-08-05",
		Kind: domain.DebtOpPayment, Amount: 4000000}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDebtMark(ctx, domain.DebtMark{DebtID: card, Date: "2026-08-30", Balance: -300000}); err != nil {
		t.Fatal(err)
	}
	npf, err := st.AddNPFAccount(ctx, domain.NPFAccount{Name: "Династія", Currency: money.UAH})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddNPFOp(ctx, domain.NPFOp{NPFID: npf, Date: "2026-07-05", Units: 288005492, Amount: 100000, Broker: "ПУМБ"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddNPFNavPoints(ctx, npf, []domain.NPFNav{{Date: "2026-06-30", Nav: 3472156}}); err != nil {
		t.Fatal(err)
	}
	flow, err := st.AddPlanFlow(ctx, PlanFlow{Name: "Зарплата", Kind: "income", Amount: 3200000,
		Currency: "UAH", Cadence: "month", FromDate: "2026-01-17", InvestBP: 4000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddPlanReceipt(ctx, PlanReceipt{FlowID: flow, Month: "2026-05", Name: "Зарплата",
		Currency: "UAH", InvestBP: 10000}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFundOp(ctx, domain.FundOp{Date: "2026-04-02", Fund: "Inzhur Ocean",
		Kind: domain.FundBuy, Qty: 2, Amount: 805174, Currency: "UAH", Broker: "inzhur"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddDecision(ctx, Decision{MadeOn: "2026-07-16", Kind: BuyBond, Ref: "UA4000239016",
		Currency: "UAH", Amount: 323145, RealPct: 8.1, RankPos: 1, RankMode: "plan", OpID: lot}); err != nil {
		t.Fatal(err)
	}

	dump, err := st.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(dump)

	wid, err := st.AddPortfolio(ctx, "wife", "Дружина")
	if err != nil {
		t.Fatal(err)
	}
	w := st.For(wid)
	if err := w.ImportAll(ctx, dump); err != nil {
		t.Fatalf("відновлення в другий портфель: %v", err)
	}

	// --- головний не змінився ---
	again, err := st.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(again)
	if string(before) != string(after) {
		t.Errorf("дамп головного змінився після відновлення в сусідній портфель")
	}

	// --- у другого все є, і діти знайшли своїх батьків за НОВИМИ id ---
	lots, _ := w.ListLots(ctx)
	sales, _ := w.ListSales(ctx)
	if len(lots) != 1 || len(sales) != 1 || sales[0].LotID != lots[0].ID {
		t.Fatalf("лот/продаж: %+v / %+v", lots, sales)
	}
	if lots[0].ID == lot {
		t.Errorf("лот дістав той самий id %d, що й у головного — колізія мала змусити перемапу", lot)
	}
	deps, _ := w.ListTermDeposits(ctx)
	if len(deps) != 1 || len(deps[0].Topups) != 1 {
		t.Errorf("вклад із поповненням: %+v", deps)
	}
	goals, _ := w.ListGoals(ctx)
	gops, _ := w.ListGoalOps(ctx)
	if len(goals) != 1 || len(gops) != 1 || gops[0].GoalID != goals[0].ID {
		t.Errorf("ціль/рух: %+v / %+v", goals, gops)
	}
	debts, _ := w.ListDebts(ctx)
	var wCard, wInst *domain.Debt
	for i := range debts {
		if debts[i].Kind == domain.DebtCard {
			wCard = &debts[i]
		} else {
			wInst = &debts[i]
		}
	}
	if wCard == nil || wInst == nil || wInst.CardID != wCard.ID {
		t.Errorf("картка/розстрочка: %+v", debts)
	}
	dops, _ := w.ListDebtOps(ctx)
	marks, _ := w.ListDebtMarks(ctx)
	if wCard != nil && (len(dops) != 1 || dops[0].DebtID != wCard.ID || len(marks) != 1 || marks[0].DebtID != wCard.ID) {
		t.Errorf("рух/звірка боргу: %+v / %+v", dops, marks)
	}
	accs, _ := w.ListNPFAccounts(ctx)
	nops, _ := w.ListNPFOps(ctx)
	navs, _ := w.ListNPFNav(ctx)
	if len(accs) != 1 || len(nops) != 1 || nops[0].NPFID != accs[0].ID || len(navs) != 1 || navs[0].NPFID != accs[0].ID {
		t.Errorf("НПФ: %+v / %+v / %+v", accs, nops, navs)
	}
	flows, _ := w.ListPlanFlows(ctx)
	rcpts, _ := w.ListPlanReceipts(ctx)
	if len(flows) != 1 || len(rcpts) != 1 || rcpts[0].FlowID != flows[0].ID {
		t.Errorf("потік/відмітка: %+v / %+v", flows, rcpts)
	}
	decs, _ := w.ListDecisions(ctx)
	if len(decs) != 1 || decs[0].OpID != lots[0].ID {
		t.Errorf("рішення вказує не на свій лот: %+v (лот %d)", decs, lots[0].ID)
	}

	// --- фонд спільний: один рядок каталогу на обидва портфелі ---
	funds, _ := st.ListFunds(ctx)
	if len(funds) != 1 {
		t.Errorf("каталог фондів подвоївся: %+v", funds)
	}
	wops, _ := w.ListFundOps(ctx)
	if len(wops) != 1 || wops[0].Fund != "Inzhur Ocean" {
		t.Errorf("операція фонду в другого: %+v", wops)
	}

	// --- повторне відновлення в той самий портфель — без колізій ---
	if err := w.ImportAll(ctx, dump); err != nil {
		t.Fatalf("повторне відновлення: %v", err)
	}
	if l, _ := w.ListLots(ctx); len(l) != 1 {
		t.Errorf("після повтору лотів %d", len(l))
	}
	// --- і в головний після всього — теж ---
	if err := st.ImportAll(ctx, dump); err != nil {
		t.Fatalf("відновлення в головний: %v", err)
	}
	final, _ := st.ExportAll(ctx)
	fb, _ := json.Marshal(final)
	if string(fb) != string(before) {
		t.Errorf("головний після власного restore відрізняється від дампа")
	}
}
