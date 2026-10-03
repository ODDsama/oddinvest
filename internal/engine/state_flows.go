// Рух грошей на межі інструмента — одне означення «скільки зайшло в
// портфель і скільки з нього вийшло».
//
// ЧОМУ МЕЖА ІНСТРУМЕНТА, А НЕ РАХУНОК. Доти зовнішніми грошима був журнал
// поповнень рахунку (deposits): усе, що заходило на рахунок брокера, і
// було внеском, а покупка лише переносила гроші з рахунку в папір. Власник
// рахунків у застосунку не веде (ревізія 2026-10-03, рахунки прибрано),
// тож рахунку немає — і межею стає сам інструмент:
//
//   - покупка (лот із комісією, сертифікат, внесок НПФ, тіло й поповнення
//     вкладу понад пул призначення) — гроші ЗАЙШЛИ в портфель;
//   - виплата (купон, погашення, дивіденд нетто, відсотки й тіло вкладу) і
//     вихід (продаж лота чи сертифіката, розірвання вкладу) — гроші ВИЙШЛИ.
//
// XIRR це не міняє: domain.PortfolioFlows і сусіди завжди рахували саме
// так — покупка відтік, виплата приплив, — і жодного разу не читали
// рахунку. Міняється лише «внесено»: воно стає НЕТТО за вікно (місяць із
// великим погашенням, яке не перевклали, справді від'ємний — це
// розщадження, а не похибка).
//
// Знак у instrFlow — з боку ВЛАСНИКА (як колись на рахунку): + гроші
// повернулись до нього, − пішли в інструмент. Зовнішній рух портфеля
// (externalMoves) — той самий журнал із протилежним знаком.
package engine

import (
	"cmp"

	"github.com/ODDsama/oddinvest/internal/domain"
	money "github.com/Rhymond/go-money"
)

// Види руху на межі інструмента.
const (
	flowIncome   = "income"   // виплата з розкладу: купон, погашення, дивіденд, відсотки
	flowPurchase = "purchase" // гроші пішли в інструмент
	flowExit     = "exit"     // вихід із позиції: продаж, розірвання вкладу
)

// instrFlow — один рух на межі інструмента в нативній валюті.
type instrFlow struct {
	Date     domain.Date
	Amount   int64 // мінорні; + до власника, − в інструмент
	Currency string
	Kind     string
	// Instr — вид інструмента: bond | fund | npf | deposit. Читає «Ціна
	// моїх рішень»: рівень «портфель» — без пенсійного.
	Instr string
	// Principal — виплата, що повертає ТІЛО (погашення ОВДП, тіло вкладу):
	// для звіту це той самий рух, що й купон, а заробітком він не є.
	Principal bool
	// New — для покупки: скільки з неї НОВИХ грошей (мінорні, ≥ 0). Зазвичай
	// −Amount; у купівлі-нозі конвертації фонду — лише доплата понад
	// виручку продаж-ноги (domain.FundBuyNew). Читає «дохід без діла».
	New   int64
	Label string
}

// flowInputs — те, з чого складається журнал. Окремою структурою, бо
// читачів двоє з різних боків: збирач стану (sources уже в пам'яті) і
// CashEvents (читає сховище сам).
type flowInputs struct {
	lots        []domain.Lot
	sales       []domain.Sale
	pays        []domain.Payment
	arrived     func(string, domain.Date) bool
	fundOps     []domain.FundOp
	npfAccounts []domain.NPFAccount
	npfOps      []domain.NPFOp
	deposits    []domain.Deposit
	pools       earmarkPools
	today       domain.Date
}

// instrumentFlows — журнал руху на межі інструмента, за датою не
// впорядкований (порядок — за видом, як у збирачі).
func instrumentFlows(in flowInputs) ([]instrFlow, error) {
	var out []instrFlow
	add := func(f instrFlow) {
		if f.Amount != 0 {
			out = append(out, f)
		}
	}

	// Купони й погашення ОВДП, що вже надійшли. Позначена наперед виплата
	// лягає сьогоднішнім днем (domain.ArrivalDate).
	pastCF, err := domain.FuturePayments(in.pays, in.lots, in.sales, "1970-01-01")
	if err != nil {
		return nil, err
	}
	for _, cf := range pastCF {
		if !in.arrived(cf.ISIN, cf.Date) {
			continue
		}
		add(instrFlow{Instr: "bond", Date: domain.ArrivalDate(cf.Date, in.today), Amount: cf.Amount.Amount(),
			Currency: cf.Amount.Currency().Code, Kind: flowIncome,
			Principal: cf.Type == domain.PayRedemption, Label: cf.ISIN})
	}

	// Лоти — ціна разом із комісією; продаж на вторинці — вихід (чиста
	// ціна + НКД).
	lotISIN := make(map[int64]string, len(in.lots))
	for _, l := range in.lots {
		lotISIN[l.ID] = l.ISIN
		cost, cerr := domain.LotCost(l)
		if cerr != nil {
			return nil, cerr
		}
		add(instrFlow{Instr: "bond", Date: l.BuyDate, Amount: -cost.Amount(), Currency: cost.Currency().Code,
			Kind: flowPurchase, New: cost.Amount(), Label: l.ISIN})
	}
	for _, sl := range in.sales {
		proceeds, serr := domain.SaleProceeds(sl)
		if serr != nil {
			return nil, serr
		}
		add(instrFlow{Instr: "bond", Date: sl.SaleDate, Amount: proceeds.Amount(), Currency: proceeds.Currency().Code,
			Kind: flowExit, Label: "продаж " + lotISIN[sl.LotID]})
	}

	// Фонди. Купівля — повна сума: ноги конвертації йдуть покупкою з
	// протилежними знаками й гасять одна одну, а різниця (доплата) — рівно
	// те, що справді зайшло. Новими грошима при цьому вважається лише
	// доплата (FundBuyNew).
	for _, op := range in.fundOps {
		what := op.Fund
		if op.PairID != 0 {
			what = "конвертація " + op.Fund
		}
		switch op.Kind {
		case domain.FundBuy:
			label := "сертифікати " + what
			if op.PairID != 0 {
				label = what
			}
			add(instrFlow{Instr: "fund", Date: op.Date, Amount: -op.Amount, Currency: op.Currency,
				Kind: flowPurchase, New: max(0, domain.FundBuyNew(op, in.fundOps)), Label: label})
		case domain.FundDividend:
			add(instrFlow{Instr: "fund", Date: op.Date, Amount: op.Amount - op.Tax, Currency: op.Currency,
				Kind: flowIncome, Label: "дивіденд " + op.Fund})
		case domain.FundSell:
			label := "продаж " + op.Fund
			if op.PairID != 0 {
				label = what
			}
			add(instrFlow{Instr: "fund", Date: op.Date, Amount: op.Amount - op.Tax, Currency: op.Currency,
				Kind: flowExit, Label: label})
		}
	}

	// Внески в НПФ — гроші йдуть в один бік: зворотного руху до пенсійного
	// віку немає. Записаний факт, тож arrived() не потрібен — лише дата.
	npfCur, npfName := map[int64]string{}, map[int64]string{}
	for _, a := range in.npfAccounts {
		npfCur[a.ID], npfName[a.ID] = cmp.Or(a.Currency, money.UAH), a.Name
	}
	for _, op := range in.npfOps {
		if op.Date.After(in.today) {
			continue
		}
		cur := cmp.Or(npfCur[op.NPFID], money.UAH)
		add(instrFlow{Instr: "npf", Date: op.Date, Amount: -op.Amount, Currency: cur,
			Kind: flowPurchase, New: op.Amount, Label: "внесок " + npfName[op.NPFID]})
	}

	// Вклади. Тіло й поповнення вкладу подушки чи цілі спершу беруть гроші
	// з пулу свого призначення (earmark_pool.go), і зайшло лише те, що
	// понад пул. Виплати таких вкладів у пулі й лишаються — їх рахує
	// подушка чи ціль, тож і руху назовні немає.
	for _, dep := range in.deposits {
		debit := func(on domain.Date, amount, fromPool int64, label string) {
			if amount -= fromPool; amount > 0 {
				add(instrFlow{Instr: "deposit", Date: on, Amount: -amount, Currency: dep.Currency,
					Kind: flowPurchase, New: amount, Label: label})
			}
		}
		if !dep.OpenDate.After(in.today) {
			debit(dep.OpenDate, dep.Principal, in.pools.fromPool(dep.ID, 0), "вклад "+dep.Bank)
		}
		for _, t := range dep.Topups {
			if !t.Date.After(in.today) {
				debit(t.Date, t.Amount, in.pools.fromPool(dep.ID, t.ID), "поповнення вкладу "+dep.Bank)
			}
		}
		if dep.Earmarked() {
			continue
		}
		credit := func(cf domain.CashflowItem) {
			if !in.arrived(cf.ISIN, cf.Date) {
				return
			}
			label := "відсотки " + dep.Bank
			if cf.Type == domain.PayRedemption {
				label = "тіло вкладу " + dep.Bank
			}
			add(instrFlow{Instr: "deposit", Date: domain.ArrivalDate(cf.Date, in.today), Amount: cf.Amount.Amount(),
				Currency: cf.Amount.Currency().Code, Kind: flowIncome,
				Principal: cf.Type == domain.PayRedemption, Label: label})
		}
		if dep.ClosedDate != "" {
			// Відсотки, що надійшли ДО розірвання, лишаються виплатою; саме
			// розірвання — вихід, а не дохід.
			for _, cf := range dep.PaidBeforeClose() {
				credit(cf)
			}
			if !dep.ClosedDate.After(in.today) {
				add(instrFlow{Instr: "deposit", Date: dep.ClosedDate, Amount: dep.ClosedAmount, Currency: dep.Currency,
					Kind: flowExit, Label: "розірвано " + dep.Bank})
			}
			continue
		}
		for _, cf := range domain.DepositSchedule(dep, "1970-01-01") {
			credit(cf)
		}
	}
	return out, nil
}
