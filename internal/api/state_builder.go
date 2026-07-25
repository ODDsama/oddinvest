// Збирання документа стану — головна операція сервісу.
//
// УВАГА: ті самі величини рахує ще cashEvents у cashflow.go — там та сама
// арифметика, але розкладена на окремі події. Дві реалізації мусять
// сходитись, і єдиний захист від їх розходження — тест
// TestCashflowStatementReconciles. Міняєш тут — дивись і туди.

package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	doc, err := s.buildState(r.Context(), time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// ytmLot — лот у вигляді, який розуміє розрахунок дохідності: собівартість
// одного паперу — «брудна» ціна плюс частка комісії. Комісія теж з'їдає
// дохідність, тож ховати її означало б завищувати результат.
func ytmLot(l domain.Lot, qty int64) domain.YTMLot {
	cost := l.PricePerBond
	if fee, err := domain.Apportion(l.Fee, 1, l.Qty); err == nil && !fee.IsZero() {
		if c2, aerr := cost.Add(fee); aerr == nil {
			cost = c2
		}
	}
	return domain.YTMLot{CostPerBond: cost, Qty: qty, BuyDate: l.BuyDate, ISIN: l.ISIN}
}

// defaultTerminalRatePct — довгострокова гривнева ставка ОВДП, до якої
// сповзає сьогоднішня. 11% — це ціль НБУ по інфляції (5%) плюс типова
// реальна премія держпаперу. Сьогоднішні 16-17% — наслідок війни, а не
// норма, і закладати їх на десять років уперед означає малювати капітал,
// якого не буде.
const defaultTerminalRatePct = 11.0

// defaultGlideYears — за скільки років ставка проходить шлях від
// сьогоднішньої до довгострокової.
const defaultGlideYears = 5.0

// round2 — округлення до 2 знаків для довідкових (не облікових) чисел.
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// buildState — спільна збірка документа стану для API і MQTT.
func (s *Server) BuildStateDoc(ctx context.Context, now time.Time) (*state.Doc, error) {
	return s.buildState(ctx, now)
}

func (s *Server) buildState(ctx context.Context, now time.Time) (*state.Doc, error) {
	lots, sales, bonds, pays, err := s.portfolio(ctx)
	if err != nil {
		return nil, err
	}
	rates, err := s.rates(ctx)
	if err != nil {
		return nil, err
	}
	today := domain.NewDate(now)
	// Знецінення — РАЗ на весь документ. Далі його бачать дохідності
	// позицій, зведені дохідності, прогноз і сценарії; якби кожен читав
	// сам, вони могли б розійтися між собою в межах однієї відповіді.
	deval := s.devaluation(ctx)

	// Операції фондів тягнемо РАЗ, як і облігації вище: далі вони
	// потрібні п'ятьом різним агрегатам (внески місяця, баланс рахунку,
	// вкладено-по-брокерах, картка фондів, XIRR), і доти, доки кожен
	// тягнув їх сам, це були п'ять однакових запитів у БД, що теоретично
	// могли розійтися між собою. Помилку ковтаємо — фонди могли ще не
	// існувати в старій БД, і це не привід валити весь стан; порожній зріз
	// просто нічого не додасть у жоден агрегат.
	fundOps, _ := s.st.ListFundOps(ctx) //nolint:errcheck // свідомо: див. коментар вище — старій БД фондів могло не бути
	// Вклади — так само раз, третім інструментом поряд із лотами й фондами.
	termDeposits, _ := s.st.ListTermDeposits(ctx) //nolint:errcheck // свідомо, як і фонди вище: вклади з'явились пізніше за схему

	positions, err := domain.Positions(bonds, pays, lots, sales, today)
	if err != nil {
		return nil, err
	}
	cashflow, err := domain.FuturePayments(pays, lots, sales, today)
	if err != nil {
		return nil, err
	}
	ladder := domain.Ladder(bonds, lots, sales, today)

	// Вклади мають розклад, тож їхні відсотки й повернення тіла входять у
	// той самий календар і ту саму драбину, що й купони й погашення ОВДП.
	// Фонди сюди не потрапляють — у них розкладу немає, і саме ця межа
	// відрізняє «заплановані потоки» від «оцінки».
	cashflow = append(cashflow, domain.DepositCashflows(termDeposits, today)...)
	ladder = append(ladder, domain.DepositLadder(termDeposits, today)...)
	// next_payment бере перший потік, драбина йде по роках — обидва
	// покладаються на порядок, який append порушив.
	sort.Slice(cashflow, func(i, j int) bool { return cashflow[i].Date < cashflow[j].Date })
	sort.Slice(ladder, func(i, j int) bool {
		if ladder[i].Year != ladder[j].Year {
			return ladder[i].Year < ladder[j].Year
		}
		return ladder[i].Currency < ladder[j].Currency
	})

	// Тіло діючих вкладів у грн-екв: усього й по валютах — для капіталу й
	// валютних часток. Розірвані/погашені не рахуємо: їхнє тіло вже не
	// «в портфелі», воно повернулось на рахунок.
	depositsUAH := 0.0
	depositsUAHByCur := map[string]float64{}
	for _, dep := range termDeposits {
		if !dep.Active(today) {
			continue
		}
		// Накопичене тіло (початкове + поповнення), а не сума відкриття:
		// поповнюваний вклад росте, і капітал має рости з ним.
		u, cerr := fx.ToUAH(money.New(dep.BalanceAt(today), dep.Currency), rates)
		if cerr != nil {
			continue
		}
		v := float64(u.Amount()) / 100
		depositsUAH += v
		depositsUAHByCur[dep.Currency] += v
	}

	// внески місяця: покупки поточного місяця в грн-еквіваленті
	monthInv := money.New(0, money.UAH)
	for _, l := range lots {
		if l.BuyDate.Year() == now.Year() && l.BuyDate.Month() == now.Month() {
			cost := domain.MulQty(l.PricePerBond, l.Qty)
			if l.Fee != nil && !l.Fee.IsZero() {
				if cost, err = cost.Add(l.Fee); err != nil {
					return nil, err
				}
			}
			uahAmt, err := fx.ToUAH(cost, rates)
			if err != nil {
				return nil, err
			}
			monthInv, err = monthInv.Add(uahAmt)
			if err != nil {
				return nil, err
			}
		}
	}

	// Сертифікати фондів — теж купівля паперів, тож у «вкладено цього
	// місяця» вони входять нарівні з облігаціями. Досі не входили лише
	// тому, що фонди прибудовувались до моделі пізніше.
	for _, op := range fundOps {
		if op.Kind != domain.FundBuy ||
			op.Date.Year() != now.Year() || op.Date.Month() != now.Month() {
			continue
		}
		if u, cerr := fx.ToUAH(money.New(op.Amount, op.Currency), rates); cerr == nil {
			if sum, aerr := monthInv.Add(u); aerr == nil {
				monthInv = sum
			}
		}
	}

	// target — місячний план. Не читається з налаштувань: виводиться з
	// цілі й дедлайну нижче, коли вже зібрані валютні рукави.
	target := money.New(0, money.UAH)

	// Поповнення за поточний місяць. Саме поповнення, а не купівлі:
	// план тепер означає «скільки НОВИХ грошей треба вносити до цілі»,
	// а купівля лише переносить гроші з рахунку в папери. Порівнювати
	// план із купівлями означало б показувати 100% виконання за папір,
	// куплений на накопичені купони, — до цілі це не додає нічого.
	monthDep := money.New(0, money.UAH)
	monthOut := money.New(0, money.UAH) // зняття цього місяця, додатнім числом
	if deps, derr := s.st.ListDeposits(ctx); derr == nil {
		for _, d := range deps {
			if d.Date.Year() != now.Year() || int(d.Date.Month()) != int(now.Month()) {
				continue
			}
			if d.Amount < 0 {
				if u, cerr := fx.ToUAH(money.New(-d.Amount, d.Currency), rates); cerr == nil {
					if sum, aerr := monthOut.Add(u); aerr == nil {
						monthOut = sum
					}
				}
			}
			// Нетто, а не сума поповнень: зняття зменшує капітал так само,
			// як поповнення його збільшує. Без цього переказ між брокерами
			// (він записується як зняття + поповнення, бо окремої сутності
			// переказу немає) роздував би «внесено» на свою суму, не
			// додавши жодної нової копійки.
			if u, cerr := fx.ToUAH(money.New(d.Amount, d.Currency), rates); cerr == nil {
				if sum, aerr := monthDep.Add(u); aerr == nil {
					monthDep = sum
				}
			}
		}
	}

	// Неперевкладені: надійшлі виплати без статусу reinvested. Рахуються по
	// ВСІХ інструментах із розкладом — купони й погашення ОВДП тут, відсотки
	// й тіло вкладів нижче, у їхньому циклі. Правило одне: запланована
	// виплата, що вже надійшла і не позначена «перевкладено», — це гроші,
	// які лежать без діла.
	statuses, err := s.st.PaymentStatuses(ctx)
	if err != nil {
		return nil, err
	}

	// arrived — чи вважати виплату вже отриманою, тобто чи класти її на
	// рахунок. Так, якщо дата вже минула АБО користувач сам позначив її
	// в календарі.
	//
	// Позначка тут не косметична, і саме тому дата сама по собі — не
	// відповідь. Графік НБУ каже, коли виплата ПОВИННА прийти, а не коли
	// вона прийшла: гроші лягають у брокера в різний час дня, а часом і
	// наступного. Тому день-у-день ми не зараховуємо нічого самі —
	// інакше з ранку баланс показував би гроші, яких ще немає, і звірка
	// з брокером щоразу розходилась би рівно на купон.
	//
	// Кнопка «Отримано» — це і є спосіб сказати «вже прийшли»,
	// не чекаючи опівночі.
	arrived := func(isin string, d domain.Date) bool {
		if d.Before(today) {
			return true
		}
		st := statuses[isin+"|"+string(d)]
		return st == "received" || st == "reinvested"
	}
	pastCF, err := domain.FuturePayments(pays, lots, sales, "1970-01-01")
	if err != nil {
		return nil, err
	}
	// Дохід і покупки збираємо подіями, а рахуємо простій наприкінці —
	// коли вже відомий баланс рахунків, яким число обмежується.
	var incomeEvents, purchaseEvents []domain.CashEvent
	bal := map[string]int64{} // валюта -> мінорні (нативно): баланс рахунку
	for _, cf := range pastCF {
		if !arrived(cf.ISIN, cf.Date) {
			continue
		}
		// отримана виплата кредитує рахунок у своїй валюті
		bal[cf.Amount.Currency().Code] += cf.Amount.Amount()
		uahAmt, err := fx.ToUAH(cf.Amount, rates)
		if err != nil {
			return nil, err
		}
		incomeEvents = append(incomeEvents, domain.CashEvent{Date: cf.Date, Amount: uahAmt.Amount()})
	}

	// --- грошові рахунки: (брокер × валюта) ---
	// Рахунки роздільні: гривня в mono не купить папір в inzhur, тож
	// баланс ведемо по кожному брокеру окремо. `bal` лишається зведеним
	// по валютах — його використовують портфельні показники.
	// Формула однакова: Σ поповнень + Σ конвертацій + Σ отриманих виплат −
	// Σ вартості лотів (усе нативно, у своїй валюті).
	balBC := map[store.BrokerCur]int64{}

	// купон кредитує рахунок ТОГО брокера, де куплено папір.
	// Умова та сама, що й для зведеного балансу вище: розійтись їм не
	// можна, інакше «Разом» і сума по брокерах показували б різне, а
	// звірка вигадала б розбіжність рівно на цей купон.
	for _, p := range pays {
		if !arrived(p.ISIN, p.PayDate) {
			continue
		}
		for _, l := range lots {
			if l.ISIN != p.ISIN {
				continue
			}
			if q := domain.HolderQty(l, sales, p.PayDate); q > 0 {
				amt := domain.MulQty(p.PerBond, q)
				balBC[store.BrokerCur{Broker: l.Channel, Currency: amt.Currency().Code}] += amt.Amount()
			}
		}
	}

	depByBC, err := s.st.DepositsByBrokerCurrency(ctx)
	if err != nil {
		return nil, err
	}
	for k, amt := range depByBC {
		bal[k.Currency] += amt
		balBC[k] += amt
	}
	convBC, err := s.st.ConversionsNetByBroker(ctx)
	if err != nil {
		return nil, err
	}
	for k, net := range convBC {
		bal[k.Currency] += net
		balBC[k] += net
	}
	for _, l := range lots {
		cost := domain.MulQty(l.PricePerBond, l.Qty)
		if l.Fee != nil && !l.Fee.IsZero() {
			if cost, err = cost.Add(l.Fee); err != nil {
				return nil, err
			}
		}
		bal[cost.Currency().Code] -= cost.Amount()
		balBC[store.BrokerCur{Broker: l.Channel, Currency: cost.Currency().Code}] -= cost.Amount()
		if u, cerr := fx.ToUAH(cost, rates); cerr == nil {
			purchaseEvents = append(purchaseEvents, domain.CashEvent{Date: l.BuyDate, Amount: u.Amount()})
		}
	}

	// Операції фондів рухають той самий гаманець: купівля списує гроші,
	// продаж і дивіденд зараховують уже за вирахуванням податку. Без
	// цього куплені сертифікати не зменшували б баланс, і звірка з
	// брокером показувала б вічну розбіжність рівно на їхню суму.
	for _, op := range fundOps {
		delta := int64(0)
		switch op.Kind {
		case domain.FundBuy:
			delta = -op.Amount
			if u, cerr := fx.ToUAH(money.New(op.Amount, op.Currency), rates); cerr == nil {
				purchaseEvents = append(purchaseEvents, domain.CashEvent{Date: op.Date, Amount: u.Amount()})
			}
		case domain.FundSell, domain.FundDividend:
			delta = op.Amount - op.Tax
			// Дивіденд — дохід і стає в чергу простою; продаж — ні: це
			// вихід із позиції, а не заробіток на ній, і питання «чи
			// перевклав» до нього не ставиться.
			if op.Kind == domain.FundDividend {
				if u, cerr := fx.ToUAH(money.New(op.Amount-op.Tax, op.Currency), rates); cerr == nil {
					incomeEvents = append(incomeEvents, domain.CashEvent{Date: op.Date, Amount: u.Amount()})
				}
			}
		}
		bal[op.Currency] += delta
		balBC[store.BrokerCur{Broker: op.Broker, Currency: op.Currency}] += delta
	}

	// Вклади рухають гаманець так само, як лоти й фонди: розміщення
	// СПИСУЄ тіло з рахунку банку (гроші замкнені на строк), а відсотки й
	// повернення тіла ЗАРАХОВУЮТЬ — але лише коли реально надійшли, через
	// той самий arrived(), що й купони. Синтетичний ISIN "deposit:<id>"
	// дає міткам у календарі за що чіплятись.
	//
	// Закритий вклад — за фактом: списане тіло при відкритті й повернута
	// сума ClosedAmount на дату розірвання (як фактична ціна продажу лота).
	for _, dep := range termDeposits {
		bc := store.BrokerCur{Broker: dep.Bank, Currency: dep.Currency}
		// розміщення: −тіло на дату відкриття (якщо вона вже настала)
		if !dep.OpenDate.After(today) {
			bal[dep.Currency] -= dep.Principal
			balBC[bc] -= dep.Principal
			// Відкрити вклад — така сама покупка, як узяти папір: гроші
			// пішли в діло.
			if u, cerr := fx.ToUAH(money.New(dep.Principal, dep.Currency), rates); cerr == nil {
				purchaseEvents = append(purchaseEvents, domain.CashEvent{Date: dep.OpenDate, Amount: u.Amount()})
			}
		}
		// кожне поповнення теж списує гроші з рахунку банку на свою дату —
		// це записаний факт, тож arrived() не потрібен
		for _, t := range dep.Topups {
			if !t.Date.After(today) {
				bal[dep.Currency] -= t.Amount
				balBC[bc] -= t.Amount
				if u, cerr := fx.ToUAH(money.New(t.Amount, dep.Currency), rates); cerr == nil {
					purchaseEvents = append(purchaseEvents, domain.CashEvent{Date: t.Date, Amount: u.Amount()})
				}
			}
		}
		if dep.ClosedDate != "" {
			if !dep.ClosedDate.After(today) {
				bal[dep.Currency] += dep.ClosedAmount
				balBC[bc] += dep.ClosedAmount
			}
			// У «не перевкладено» розірвання НЕ входить: це дискреційний
			// вихід, як продаж лота на вторинці, а не запланована виплата.
			// До того ж позначити його «перевкладено» нема де — закритий
			// вклад у календарі рядка не має, і сума висіла б там вічно.
			continue
		}
		// діючий вклад: відсотки й тіло — коли надійшли (минула дата або
		// позначка). DepositSchedule від "1970-01-01" дає весь графік,
		// зокрема минулі виплати.
		for _, cf := range domain.DepositSchedule(dep, "1970-01-01") {
			if !arrived(cf.ISIN, cf.Date) {
				continue
			}
			bal[cf.Amount.Currency().Code] += cf.Amount.Amount()
			balBC[store.BrokerCur{Broker: dep.Bank, Currency: cf.Amount.Currency().Code}] += cf.Amount.Amount()
			// Відсотки вкладу — такий самий дохід, як купон, і в чергу
			// простою стають нарівні з ним.
			if u, cerr := fx.ToUAH(cf.Amount, rates); cerr == nil {
				incomeEvents = append(incomeEvents, domain.CashEvent{Date: cf.Date, Amount: u.Amount()})
			}
		}
	}

	// брокер -> валюта -> сума (major), для UI і для «чи вистачає на папір»
	brokers := map[string]map[string]float64{}
	for k, m := range balBC {
		name := k.Broker
		if name == "" {
			name = "—"
		}
		if brokers[name] == nil {
			brokers[name] = map[string]float64{}
		}
		brokers[name][k.Currency] = float64(m) / 100
	}

	// Вкладено по брокерах (грн-екв.): дзеркалить логіку Positions —
	// ціна×залишок + пропорційна комісія, лише згруповано по брокеру.
	investedByBroker := map[string]float64{}
	for _, l := range lots {
		rem := domain.RemainingQtyNow(l, sales)
		if rem == 0 {
			continue
		}
		cost := domain.MulQty(l.PricePerBond, rem)
		if fee, ferr := domain.Apportion(l.Fee, rem, l.Qty); ferr == nil && !fee.IsZero() {
			if c2, aerr := cost.Add(fee); aerr == nil {
				cost = c2
			}
		}
		u, uerr := fx.ToUAH(cost, rates)
		if uerr != nil {
			continue
		}
		name := l.Channel
		if name == "" {
			name = "—"
		}
		investedByBroker[name] += float64(u.Amount()) / 100
	}
	// Сертифікати теж лежать у брокера, і без них картка «Вкладено по
	// брокерах» показувала неправду про те, ДЕ твої гроші: 3 389 ₴ в
	// inzhur просто не існували для неї.
	//
	// Собівартість тут середньозважена по фонду, а брокер — з операцій;
	// якщо той самий фонд купувався у двох брокерів, частка ділиться
	// пропорційно вкладеному в кожного.
	if len(fundOps) > 0 {
		boughtByFundBroker := map[string]map[string]int64{}
		for _, op := range fundOps {
			if op.Kind != domain.FundBuy {
				continue
			}
			if boughtByFundBroker[op.Fund] == nil {
				boughtByFundBroker[op.Fund] = map[string]int64{}
			}
			b := op.Broker
			if b == "" {
				b = "—"
			}
			boughtByFundBroker[op.Fund][b] += op.Amount
		}
		for fund, pos := range domain.FundPositions(fundOps) {
			byBroker := boughtByFundBroker[fund]
			var totalBought int64
			for _, v := range byBroker {
				totalBought += v
			}
			if totalBought == 0 || pos.CostBasis == 0 {
				continue
			}
			for b, v := range byBroker {
				share := money.New(pos.CostBasis*v/totalBought, pos.Currency)
				if u, uerr := fx.ToUAH(share, rates); uerr == nil {
					investedByBroker[b] += float64(u.Amount()) / 100
				}
			}
		}
	}

	// Драбина в грн-екв.: номінал, що повертається щороку (для стовпчиків).
	ladderByYear := map[int]int64{}
	for _, e := range ladder {
		if u, err := fx.ToUAH(money.New(e.Nominal, e.Currency), rates); err == nil {
			ladderByYear[e.Year] += u.Amount()
		}
	}
	years := make([]int, 0, len(ladderByYear))
	for y := range ladderByYear {
		years = append(years, y)
	}
	sort.Ints(years)
	ladderUAH := make([]state.YearAmount, 0, len(years))
	for _, y := range years {
		ladderUAH = append(ladderUAH, state.YearAmount{Year: y, UAH: round2(float64(ladderByYear[y]) / 100)})
	}

	// Надходження по місяцях на рік наперед, грн-екв. КУПОНИ рахуємо
	// окремо від погашень: погашення — це повернення власного тіла, а не
	// дохід, тож на питання «скільки я отримую» відповідають лише купони.
	incByMonth := map[string]float64{}
	couByMonth := map[string]float64{}
	for _, cf := range cashflow {
		u, err := fx.ToUAH(cf.Amount, rates)
		if err != nil {
			continue
		}
		key := fmt.Sprintf("%04d-%02d", cf.Date.Year(), int(cf.Date.Month()))
		v := float64(u.Amount()) / 100
		incByMonth[key] += v
		if cf.Type != domain.PayRedemption {
			couByMonth[key] += v
		}
	}
	income12m := make([]state.MonthAmount, 0, 12)
	coupons12m := make([]state.MonthAmount, 0, 12)
	couponSum := 0.0
	for i := 0; i < 12; i++ {
		t := today.Time().AddDate(0, i, 0)
		key := fmt.Sprintf("%04d-%02d", t.Year(), int(t.Month()))
		income12m = append(income12m, state.MonthAmount{Month: key, Amount: round2(incByMonth[key])})
		coupons12m = append(coupons12m, state.MonthAmount{Month: key, Amount: round2(couByMonth[key])})
		couponSum += couByMonth[key]
	}
	// Число ЧИСТЕ, і це не збіг трьох різних правил, а наслідок одного:
	// сюди все потрапляє вже після податку. Купон ОВДП від нього
	// звільнений (брутто = нетто), відсотки вкладу графік віддає
	// після утримання, дивіденди фондів додаються нижче теж чистими.
	// Скільки саме забрав податок — окремо, у /api/tax.
	incomeMonthlyNow := round2(couponSum / 12)

	// --- сертифікати фондів ---
	// Позиція — сальдо журналу операцій. Дивіденди беремо ПІСЛЯ податку:
	// купон ОВДП від нього звільнений, дивіденд фонду ні, тож у спільну
	// картку доходу вони можуть потрапити лише чистими.
	// Дохідність фондів і зведена по портфелю. Рахуються нижче, коли вже
	// зібрані позиції фондів — тут лише оголошені, щоб було видно, що
	// це три різні числа, а не одне з уточненнями.
	// Кожна — парою: номінальна й реальна. Одна без одної на екрані
	// читається як помилка, бо той самий інструмент показує різні числа.
	var fundsYield, fundsYieldReal, blendedYield, blendedYieldReal float64
	var fundsUAH float64
	var fundRows []state.FundPositionRow
	if len(fundOps) > 0 {
		positions := domain.FundPositions(fundOps)
		names := make([]string, 0, len(positions))
		for name := range positions {
			names = append(names, name)
		}
		sort.Strings(names)
		var fundDivNet float64
		for _, name := range names {
			fp := positions[name]
			mv := money.New(fp.MarketValue(), fp.Currency)
			mvUAH := float64(fp.MarketValue()) / 100
			if u, cerr := fx.ToUAH(mv, rates); cerr == nil {
				mvUAH = float64(u.Amount()) / 100
			}
			fundsUAH += mvUAH
			y, _ := domain.DividendYieldNet(fundOps, fp, today)
			row := state.FundPositionRow{
				Fund: fp.Fund, Currency: fp.Currency, Qty: fp.Qty,
				CostBasis:     round2(float64(fp.CostBasis) / 100),
				LastPrice:     math.Round(float64(fp.LastPrice)) / 10000,
				LastPriceDate: string(fp.LastPriceDate),
				MarketValue:   round2(mvUAH),
				DividendsNet:  round2(float64(fp.DividendsGross-fp.DividendsTax) / 100),
				DividendsTax:  round2(float64(fp.DividendsTax) / 100),
				Realized:      round2(float64(fp.Realized) / 100),
				YieldNetPct:   y,
				Short:         fp.Short,
			}
			// Дохідність позиції — ПОВНА: дивіденди разом зі зміною ціни.
			// Самі дивіденди поряд з облігацією нечесні, бо YTM ловить і
			// купон, і дисконт, тобто весь дохід паперу. Якщо історії ще
			// замало для ануалізації, відступаємо до дивідендної частини —
			// краще менше, ніж вигадані сотні відсотків із трьох днів.
			cur := fp.Currency
			if cur == "" {
				cur = money.UAH
			}
			if tot, ok := domain.FundTotalReturn(fundOps, fp.Fund, today); ok {
				row.TotalPct = tot
				row.RealPct = round2(realYield(tot/100, cur, deval) * 100)
				row.YieldBasis = "дивіденди + зміна ціни"
			} else if y > 0 {
				row.RealPct = round2(realYield(y/100, cur, deval) * 100)
				row.YieldBasis = "дивіденди після податку"
			}
			fundRows = append(fundRows, row)
			// Чистий дивідендний потік за рік — у спільний «пасивний дохід».
			if y > 0 {
				fundDivNet += mvUAH * y / 100
			}
		}
		incomeMonthlyNow = round2(incomeMonthlyNow + fundDivNet/12)
		// Дохідність фондів — зважена ринковою вартістю: більший фонд
		// має важити більше, ніж дрібний із гучним відсотком.
		//
		// Зважуємо ПОВНУ дохідність (дивіденди зі зміною ціни), а не саму
		// дивідендну: у рядку позиції показано саме її, і плитка, що
		// підсумовує ті самі фонди іншою мірою, суперечила б таблиці під
		// собою. Де повної ще немає (замало історії) — падаємо на
		// дивідендну, як і сам рядок.
		var wSum, wReal, w float64
		for _, row := range fundRows {
			if row.MarketValue <= 0 {
				continue
			}
			nominal := row.TotalPct
			if nominal == 0 {
				nominal = row.YieldNetPct
			}
			wSum += nominal * row.MarketValue
			wReal += row.RealPct * row.MarketValue
			w += row.MarketValue
		}
		if w > 0 {
			fundsYield = math.Round(wSum/w*100) / 100
			fundsYieldReal = math.Round(wReal/w*100) / 100
		}
	}

	accounts := map[string]float64{}
	accountUAHMinor := int64(0)
	for cur, m := range bal {
		accounts[cur] = float64(m) / 100
		if uahAmt, err := fx.ToUAH(money.New(m, cur), rates); err == nil {
			accountUAHMinor += uahAmt.Amount()
		}
	}
	account := money.New(accountUAHMinor, money.UAH)

	// Дохід, що не працює. Купівлі з'їдають його за чергою (найстаріше
	// першим), а зверху число обмежене тим, що РЕАЛЬНО лежить на
	// рахунках: якщо грошей немає, то й доходу без діла немає, хай би що
	// казала історія надходжень. Без цієї стелі наївна черга сама б собі
	// суперечила — зняв гроші з рахунку, а вона й далі рахує їх простоєм.
	idle := domain.IdleIncome(incomeEvents, purchaseEvents)
	if idle > accountUAHMinor {
		idle = accountUAHMinor
	}
	if idle < 0 {
		idle = 0
	}
	unin := money.New(idle, money.UAH)

	// найдешевший папір по валютах (нативно) + мінімум у грн-екв.
	minNoms, err := s.st.MinNominalByCurrency(ctx)
	if err != nil {
		return nil, err
	}
	// Мінімум по валютах у мінорних: спершу найдешевший папір (ОВДП), потім
	// зливаємо мінімум вкладу. Вклад — теж інструмент реінвесту, тож там, де
	// його поріг нижчий (або де паперу у валюті немає), «до реінвесту
	// готовий» настає раніше. Саме це дає простою USD/EUR куди йти без
	// відповідних облігацій.
	depMinByCur := s.depositMinMinorByCur(ctx)
	minByCur := map[string]int64{}
	for cur, minNom := range minNoms {
		minByCur[cur] = minNom
	}
	for cur, depMin := range depMinByCur {
		if have, ok := minByCur[cur]; !ok || depMin < have {
			minByCur[cur] = depMin
		}
	}
	reinvestMinByCur := map[string]float64{}
	reinvestMin := money.New(0, money.UAH)
	for cur, minNom := range minByCur {
		reinvestMinByCur[cur] = float64(minNom) / 100
		uahAmt, err := fx.ToUAH(money.New(minNom, cur), rates)
		if err != nil {
			continue
		}
		if reinvestMin.IsZero() || uahAmt.Amount() < reinvestMin.Amount() {
			reinvestMin = uahAmt
		}
	}

	settings := &state.SettingsDoc{}
	if !target.IsZero() {
		v := float64(target.Amount()) / 100
		settings.MonthlyTargetUAH = &v
	}
	if raw, _ := s.st.GetSetting(ctx, "usd_target_share_pct"); raw != "" { //nolint:errcheck // порожньо = не задано; помилка веде туди ж — до дефолту
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			settings.USDTargetSharePct = &f
		}
	}
	if raw, _ := s.st.GetSetting(ctx, "eur_target_share_pct"); raw != "" { //nolint:errcheck // порожньо = не задано; помилка веде туди ж — до дефолту
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			settings.EURTargetSharePct = &f
		}
	}
	if raw, _ := s.st.GetSetting(ctx, "assumed_rate_pct"); raw != "" { //nolint:errcheck // порожньо = не задано; помилка веде туди ж — до дефолту
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			settings.AssumedRatePct = &f
		}
	}
	// Список брокерів більше не зберігається рядком — він збирається з
	// довідника. У зведенні лишається як рядок навмисно: це похідне поле
	// для випадайок, а не місце зберігання, і сутності HA, які на нього
	// підписані, не мусять знати про зміну схеми.
	if bs, err := s.st.ListBrokers(ctx); err == nil && len(bs) > 0 {
		names := make([]string, 0, len(bs))
		for _, b := range bs {
			names = append(names, b.Name)
		}
		settings.Channels = strings.Join(names, ", ")
	}
	if raw, _ := s.st.GetSetting(ctx, "reinvest_rank"); raw != "" { //nolint:errcheck // порожньо = не задано; помилка веде туди ж — до дефолту
		settings.ReinvestRank = raw
	}
	for _, g := range []struct {
		key string
		dst **float64
	}{
		{"goal_pessimistic_uah", &settings.GoalPessimisticUAH},
		{"goal_realistic_uah", &settings.GoalRealisticUAH},
		{"goal_optimistic_uah", &settings.GoalOptimisticUAH},
		{"uah_devaluation_pct", &settings.UAHDevaluationPct},
		{"terminal_rate_pct", &settings.TerminalRatePct},
		{"rate_glide_years", &settings.RateGlideYears},
		{"deposit_min_usd", &settings.DepositMinUSD},
		{"deposit_min_eur", &settings.DepositMinEUR},
		{"deposit_min_uah", &settings.DepositMinUAH},
		{"deposit_rate_usd_pct", &settings.DepositRateUSDPct},
		{"deposit_rate_eur_pct", &settings.DepositRateEURPct},
		{"deposit_rate_uah_pct", &settings.DepositRateUAHPct},
	} {
		if raw, _ := s.st.GetSetting(ctx, g.key); raw != "" { //nolint:errcheck // порожньо = не задано; помилка веде туди ж — до дефолту
			if f, err := strconv.ParseFloat(raw, 64); err == nil {
				*g.dst = &f
			}
		}
	}
	if raw, _ := s.st.GetSetting(ctx, "goal_amount_uah"); raw != "" { //nolint:errcheck // порожньо = не задано; помилка веде туди ж — до дефолту
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			settings.GoalAmountUAH = &f
		}
	}
	if raw, _ := s.st.GetSetting(ctx, "goal_date"); raw != "" { //nolint:errcheck // порожньо = не задано; помилка веде туди ж — до дефолту
		settings.GoalDate = raw
	}

	// Фонди входять у XIRR нарівні з облігаціями: показник міряє, скільки
	// реально зароблено на вкладених грошах, а гроші в сертифікатах — ті
	// самі гроші. Без цього він рахував облігаційну частину й видавав її
	// за портфельну. fundOps уже стягнуто раз на початку buildState.
	xirr := map[string]float64{}
	for _, cur := range []string{money.UAH, money.USD, money.EUR} {
		flows, err := domain.PortfolioFlows(bonds, pays, lots, sales, cur, today)
		if err != nil {
			continue
		}
		flows = append(flows, domain.FundFlows(fundOps, cur, today)...)
		flows = append(flows, domain.DepositFlows(termDeposits, cur, today)...)
		sort.Slice(flows, func(i, j int) bool { return flows[i].Date < flows[j].Date })
		if len(flows) < 2 {
			continue
		}
		// Ануалізація на коротких горизонтах дає сміттєві сотні відсотків.
		// Міряємо вік НЕ першого потоку, а самих ГРОШЕЙ: середній зважений
		// строк, який вони вже працюють.
		//
		// Різниця не теоретична. Портфель, де фонди куплені 48 днів тому, а
		// облігації — позавчора, за старим правилом проходив поріг: перший
		// потік давній, отже «історія є». А насправді дві третини грошей
		// пролежали три дні, і їхня ануалізована дохідність — шум, який
		// тягнув усе число в −42%. Той самий поріг тепер боронить і
		// дохідність окремого фонду, тож правило живе в domain.
		if domain.MoneyWeightedDays(flows, today) < 30 {
			continue
		}
		// навіть >30 днів нерівномірні потоки дають артефакти (сотні %);
		// реалізована дохідність портфеля ОВДП поза смугою -95%..+100%
		// — це шум ануалізації, а не сигнал, тож не публікуємо.
		if r, err := domain.XIRR(flows); err == nil && r <= 1.0 && r >= -0.95 {
			xirr[cur] = math.Round(r*10000) / 100 // частка -> %, 2 знаки
		}
	}

	// Очікувана дохідність за придбаними паперами — ДОХІДНІСТЬ ДО
	// ПОГАШЕННЯ (YTM) від того, що фактично сплачено, зважена вкладеними
	// грішми. Це орієнтир для проєкцій замість ручного вводу.
	//
	// Раніше тут було «річний купон ÷ номінал». Воно відповідало на питання
	// «скільки папір платить», а не «скільки я заробляю»: ціна купівлі не
	// впливала взагалі, тож папір, узятий із дисконтом, і папір, узятий з
	// премією, виглядали однаково. YTM бачить і дисконт, і комісію, і те,
	// що піврічний купон складається всередині року.
	var nominalUAH int64               // сумарний номінал у грн-екв.
	nominalByCur := map[string]int64{} // номінал нативно по валютах
	ytmLotsByCur := map[string][]domain.YTMLot{}
	var ytmWeightUAH, ytmWeightedUAH, ytmWeightedRealUAH float64
	for _, l := range lots {
		b, ok := bonds[l.ISIN]
		if !ok || b.Maturity.Before(today) {
			continue
		}
		q := domain.RemainingQtyNow(l, sales)
		if q == 0 {
			continue
		}
		cur := b.Nominal.Currency().Code
		nominalByCur[cur] += b.Nominal.Amount() * q
		if n, err := fx.ToUAH(money.New(b.Nominal.Amount()*q, cur), rates); err == nil {
			nominalUAH += n.Amount()
		}
		lot := ytmLot(l, q)
		cost := lot.CostPerBond
		ytmLotsByCur[cur] = append(ytmLotsByCur[cur], lot)
		// Для зведеної цифри вагу переводимо в гривню, щоб валюти
		// складались коректно, а самі ставки лишались нативними.
		if y, ok := domain.WeightedYTM([]domain.YTMLot{lot}, pays); ok {
			if w, err := fx.ToUAH(money.New(cost.Amount()*q, cur), rates); err == nil {
				ytmWeightUAH += float64(w.Amount())
				ytmWeightedUAH += float64(w.Amount()) * y
				// Реальну зважуємо тут само, лотом за лотом, а не ділимо
				// готову суміш на знецінення: знецінення торкається лише
				// гривневих рукавів, і поділ суміші цілком занизив би
				// доларову частину.
				ytmWeightedRealUAH += float64(w.Amount()) * realYield(y/100, cur, deval) * 100
			}
		}
	}
	var portfolioYield, portfolioYieldReal float64
	if ytmWeightUAH > 0 {
		portfolioYield = math.Round(ytmWeightedUAH/ytmWeightUAH*100) / 100
		portfolioYieldReal = math.Round(ytmWeightedRealUAH/ytmWeightUAH*100) / 100
	}
	portfolioYieldByCur := map[string]float64{}
	// Реальний двійник кожної зведеної дохідності. Доти плитки говорили
	// номінальними числами, а таблиця під ними — реальними, і той самий
	// папір показувався двома різними числами на одному екрані без жодної
	// позначки, що бази різні.
	portfolioYieldRealByCur := map[string]float64{}
	for cur, ls := range ytmLotsByCur {
		if y, ok := domain.WeightedYTM(ls, pays); ok {
			portfolioYieldByCur[cur] = math.Round(y*100) / 100
			portfolioYieldRealByCur[cur] = round2(realYield(y/100, cur, deval) * 100)
		}
	}

	// --- фактичний темп поповнень ---
	// План може розходитись із реальністю, тож рахуємо ще й середній темп
	// НОВИХ грошей. Саме поповнень, а не покупок: покупка лише переносить
	// гроші з рахунку в папери й нового капіталу не додає (а купони вже
	// враховані окремо).
	//
	// Знаменник — це +1 місяць до проміжку «перше поповнення … сьогодні», і
	// це не косметика. Поповнення фінансують ПЕРІОДИ, а не проміжок між
	// собою: три щомісячні внески покривають три місяці, тоді як від
	// першого до сьогодні минуло лише два. Ділення на проміжок завищувало
	// темп у півтора раза (15 000 за 60 днів давали 7 610 ₴/міс замість
	// 5 000). Та сама поправка знімає й вибух на старті: одне поповнення
	// сьогодні дає знаменник 1, а не 0.1, тож окремий поріг більше не
	// потрібен — темп показуємо одразу, а поруч пишемо, на якій довжині
	// історії він порахований, щоб було видно, наскільки йому вірити.
	var actualMonthly float64
	var actualMonths int
	// Вікно — останні півроку, а не вся історія.
	//
	// Усереднення за весь час міряє не темп, а біографію: якщо портфель
	// колись виходив у нуль і починався заново, внески «до» і виведення
	// «під час» гасять одне одного, і сьогоднішні 7 500 ₴/міс виглядають
	// як 430. На реальних даних саме так і сталось — 29 місяців історії з
	// повним виходом посередині дали 0% від потрібного при живих внесках.
	//
	// Півроку — компроміс: досить довго, щоб пропущений місяць не обвалив
	// оцінку, і досить коротко, щоб показник відповідав на «як я вкладаю
	// ЗАРАЗ», а саме це питання йому й ставлять.
	const actualWindowDays = 183
	if deps, derr := s.st.ListDeposits(ctx); derr == nil && len(deps) > 0 {
		first := today
		var totalUAH int64
		for _, d := range deps {
			if n := domain.DaysBetween(d.Date, today); n < 0 || n > actualWindowDays {
				continue
			}
			if d.Date.Before(first) {
				first = d.Date
			}
			// Нетто: зняття теж рух капіталу. Інакше переказ між брокерами
			// (зняття + поповнення) завищував би темп на свою суму, а
			// прогноз «За фактом» через це малював би дисципліну, якої немає.
			if u, cerr := fx.ToUAH(money.New(d.Amount, d.Currency), rates); cerr == nil {
				totalUAH += u.Amount()
			}
		}
		if totalUAH > 0 {
			months := float64(domain.DaysBetween(first, today))/30.44 + 1
			if months < 1 {
				months = 1
			}
			actualMonths = int(months + 0.5)
			actualMonthly = round2(float64(totalUAH) / 100 / months)
		}
	}

	// --- проєкція капіталу: помісячна симуляція РЕАЛЬНИХ потоків ---
	// (купони/погашення наявних паперів) + внески; реінвест під дохідність
	// портфеля. Готівка не працює, поки не реінвестована. Це замість сухої
	// формули складного відсотка — біля-термінова частина будується з
	// фактичного календаря виплат.
	//
	// Кожна валюта рахується ОКРЕМИМ рукавом у нативній валюті: своя
	// дохідність, свій календар, свій поріг докупівлі. Інакше гривневий
	// папір під 16% завжди бив би доларовий під 4% — модель просто не
	// бачила б, що гривня знецінюється.
	// Зведена дохідність: облігації важать номіналом у грн-екв., фонди —
	// ринковою вартістю. Саме вона й потрібна проєкціям — до неї капітал
	// у сертифікатах ріс за ставкою облігацій, яких у ньому немає.
	nominalMajor := float64(nominalUAH) / 100
	if nominalMajor+fundsUAH > 0 {
		blend := func(bond, fund float64) float64 {
			return math.Round((bond*nominalMajor+fund*fundsUAH)/(nominalMajor+fundsUAH)*100) / 100
		}
		blendedYield = blend(portfolioYield, fundsYield)
		blendedYieldReal = blend(portfolioYieldReal, fundsYieldReal)
	}

	// Ставка реінвесту — ЗВЕДЕНА: капітал у сертифікатах не росте за
	// ставкою облігацій, яких у ньому немає.
	capRate := blendedYield
	if capRate <= 0 {
		capRate = portfolioYield
	}
	if capRate > 40 {
		capRate = 40 // стеля, щоб компаунд не вибухав
	}
	// contribM — місячний внесок плану. Виводиться з ЦІЛІ й ДЕДЛАЙНУ
	// нижче, коли вже зібрані валютні рукави: окреме ручне число дублювало
	// інформацію, яка й так є в цілі, і мовчки з нею розходилось.
	contribM := 0.0

	// Реальні майбутні потоки, розкладені по валютах і місяцях.
	couponByCurMonth := map[string]map[int]float64{}
	redeemByCurMonth := map[string]map[int]float64{}
	for _, cf := range cashflow {
		cur := cf.Amount.Currency().Code
		mi := (cf.Date.Year()-today.Year())*12 + int(cf.Date.Month()) - int(today.Month())
		if mi < 1 {
			mi = 1
		}
		dst := couponByCurMonth
		if cf.Type == domain.PayRedemption {
			dst = redeemByCurMonth
		}
		if dst[cur] == nil {
			dst[cur] = map[int]float64{}
		}
		dst[cur][mi] += float64(cf.Amount.Amount()) / 100
	}

	// Куди підуть майбутні поповнення: за цільовими валютними частками.
	// Це вже задано в налаштуваннях, тож нової здогадки не вводимо.
	share := map[string]float64{}
	if settings.USDTargetSharePct != nil {
		share[money.USD] = *settings.USDTargetSharePct / 100
	}
	if settings.EURTargetSharePct != nil {
		share[money.EUR] = *settings.EURTargetSharePct / 100
	}
	if rest := 1 - share[money.USD] - share[money.EUR]; rest > 0 {
		share[money.UAH] = rest
	} else {
		share[money.UAH] = 0
	}

	// Запасна дохідність для валюти, якої ще немає в портфелі.
	avgRate, err := s.st.AvgRateByCurrency(ctx, today)
	if err != nil {
		return nil, err
	}

	// Річне знецінення гривні. Одне число в налаштуваннях, від якого
	// сценарії розходяться — як і ставка.
	// Те саме число, що й у дохідностях вище: прогноз і помічник
	// зобов'язані виходити з одного припущення, інакше вони суперечать
	// одне одному на одному екрані.
	devalBase := deval
	// Куди прийде гривнева ставка і як довго вона туди йтиме.
	terminalUAH := defaultTerminalRatePct
	if settings.TerminalRatePct != nil && *settings.TerminalRatePct >= 0 {
		terminalUAH = *settings.TerminalRatePct
	}
	glideYears := defaultGlideYears
	if settings.RateGlideYears != nil && *settings.RateGlideYears >= 0 {
		glideYears = *settings.RateGlideYears
	}

	// buildSleeves збирає рукави під заданий сумарний внесок і зсув ставки.
	buildSleeves := func(contribTotal, ratePP float64) []domain.Sleeve {
		var out []domain.Sleeve
		for _, cur := range []string{money.UAH, money.USD, money.EUR} {
			cash := float64(bal[cur]) / 100
			nom := float64(nominalByCur[cur]) / 100
			contrib := contribTotal * share[cur]
			if cash == 0 && nom == 0 && contrib == 0 {
				continue // валюти немає і не планується
			}
			rate, ok := portfolioYieldByCur[cur]
			if !ok {
				rate = avgRate[cur] // паперів цієї валюти ще немає
			}
			if rate > 40 {
				rate = 40 // стеля, щоб компаунд не вибухав
			}
			// Сьогоднішня ставка — факт: за нею можна купити просто зараз.
			// Припущенням є те, куди вона прийде, тож розкид сценаріїв
			// вішаємо на довгострокову ставку, а не на сьогоднішню.
			terminal := rate
			if cur == money.UAH {
				terminal = terminalUAH
			}
			if terminal += ratePP; terminal < 0 {
				terminal = 0
			}
			if terminal > 40 {
				terminal = 40
			}
			rate0 := 1.0
			if cur != money.UAH {
				u, err := fx.ToUAH(money.New(100, cur), rates)
				if err != nil {
					continue // курсу немає — рукав порахувати чесно не вийде
				}
				rate0 = float64(u.Amount()) / 100
			}
			out = append(out, domain.Sleeve{
				Currency: cur, Cash0: cash, Nominal0: nom, RatePct: rate,
				RateTerminalPct: terminal, GlideYears: glideYears,
				Threshold: reinvestMinByCur[cur], Coupon: couponByCurMonth[cur],
				Redeem: redeemByCurMonth[cur], ContribUAH: contrib, Rate0: rate0,
			})
		}
		return out
	}

	rate0USD := 0.0
	if u, err := fx.ToUAH(money.New(100, money.USD), rates); err == nil {
		rate0USD = float64(u.Amount()) / 100
	}

	// --- місячний план: скільки треба вносити, щоб дійти до цілі ---
	//
	// Раніше це було ручне число в налаштуваннях. Воно дублювало ціль і
	// дедлайн, які вже задані, і нічого не заважало їм суперечити: можна
	// було планувати 5 000/міс під ціль, для якої треба 20 000.
	//
	// Тепер план — це відповідь на «скільки треба». Наслідок, про який
	// варто пам'ятати: реалістичний сценарій тепер за побудовою впирається
	// рівно в ціль, тож питання «чи досяжна ціль» переїхало в рядок «За
	// фактом» — порівняння потрібного темпу з тим, що є насправді.
	deadlineMonths := 0
	if domain.Date(settings.GoalDate).Valid() {
		gd := domain.Date(settings.GoalDate)
		deadlineMonths = (gd.Year()-today.Year())*12 + int(gd.Month()) - int(today.Month())
	}
	// Ціль читаємо з нового одиночного поля, зі спадом на старі три — щоб
	// профілі, які ще не пройшли міграцію 0008, не лишились без цілі.
	goalAmount := 0.0
	for _, c := range []*float64{settings.GoalAmountUAH, settings.GoalOptimisticUAH,
		settings.GoalRealisticUAH, settings.GoalPessimisticUAH} {
		if c != nil && *c > 0 {
			goalAmount = *c
			break
		}
	}
	if goalAmount > 0 && deadlineMonths > 0 {
		// Рукави тут потрібні лише щоб задати ПРОПОРЦІЇ між валютами;
		// саму суму підбирає бісекція, тож стартове число довільне.
		contribM = round2(domain.RequiredMonthlySleeves(
			buildSleeves(1, 0), devalBase, goalAmount, deadlineMonths))
		target = money.New(int64(math.Round(contribM*100)), money.UAH)
	}

	// Старт проєкції — увесь капітал, разом із сертифікатами: інакше
	// крива починалась би нижче за плитку «Капітал» на ту саму суму.
	p0 := float64(accountUAHMinor+nominalUAH)/100 + fundsUAH
	projection := make([]state.ProjectionRow, 0, 4)
	for _, y := range []int{1, 3, 5, 10} {
		m := y * 12
		res := domain.ProjectSleeves(buildSleeves(contribM, 0), devalBase, m)
		row := state.ProjectionRow{
			Years: y,
			// Обидві колонки — у сьогоднішніх гривнях, інакше таблиця
			// віднімала б номінальні гроші від реальних і на коротких
			// горизонтах показувала б від'ємний приріст.
			Contributed:   round2(domain.RealContributed(p0, contribM, devalBase, m)),
			WithReinvest:  round2(res.TodayUAH),
			IncomeMonthly: round2(res.IncomeMonthlyTodayUAH),
		}
		if actualMonthly > 0 {
			act := domain.ProjectSleeves(buildSleeves(actualMonthly, 0), devalBase, m)
			row.WithReinvestActual = round2(act.TodayUAH)
			row.IncomeMonthlyActual = round2(act.IncomeMonthlyTodayUAH)
		}
		projection = append(projection, row)
	}

	// --- віяло прогнозів на дедлайн ---
	//
	// Ціль — ОДНА сума-орієнтир. Дата в усіх рядків одна (дедлайн), тож
	// суми між собою порівнянні.
	//
	// Три сценарії описують РИНОК і відрізняються лише ринковими
	// допущеннями — ставкою й знеціненням; внесок в усіх трьох плановий.
	// Окремий рядок «За фактом» описує ТЕБЕ: плановий внесок замінено на
	// фактичний темп поповнень за ринкових допущень реалістичного
	// сценарію. Так різниця між ним і «Реалістично» — це рівно твоя
	// поведінка, без домішки ринку.
	//
	// Раніше фактичний темп підмішувався в межі внеску песимістичного й
	// оптимістичного сценаріїв, і два різні джерела невизначеності —
	// ринок і дисципліна — злипались в одне число.
	const goalHorizonMonths = 720 // 60 років — далі вважаємо недосяжним
	var forecast *state.Forecast
	if deadlineMonths > 0 {
		const rateSpreadPP = 3.0  // ± п.п. до ставки реінвесту
		const devalSpreadPP = 4.0 // ± п.п. до знецінення гривні
		type scenario struct {
			key, label             string
			contrib, ratePP, deval float64
		}
		defs := []scenario{
			{"optimistic", "Оптимістично", contribM, rateSpreadPP, math.Max(0, devalBase-devalSpreadPP)},
			{"realistic", "Реалістично", contribM, 0, devalBase},
			{"pessimistic", "Песимістично", contribM, -rateSpreadPP, devalBase + devalSpreadPP},
		}
		// Фактичний темп з'являється, коли назбирається ≥60 днів історії
		// поповнень — на коротшій вибірці середнє від стартового внеску
		// дає безглузді сотні тисяч на місяць.
		if actualMonthly > 0 {
			defs = append(defs, scenario{"actual", "За фактом", actualMonthly, 0, devalBase})
		}
		f := &state.Forecast{
			Date:        string(domain.NewDate(today.Time().AddDate(0, deadlineMonths, 0))),
			Months:      deadlineMonths,
			GoalAmount:  goalAmount,
			ContribPlan: round2(contribM),
			Rate0USD:    round2(rate0USD),
			GlideYears:  glideYears,
		}
		for _, d := range defs {
			sl := buildSleeves(d.contrib, d.ratePP)
			res := domain.ProjectSleeves(sl, d.deval, deadlineMonths)
			row := state.ForecastRow{Key: d.key, Label: d.label,
				Amount: round2(res.TodayUAH), AmountNominal: round2(res.NominalUAH),
				ContribMonthly: round2(d.contrib), DevaluationPct: round2(d.deval)}
			// Скільки треба вносити САМЕ ЗА ЦИХ допущень. За гіршого ринку
			// той самий фінансовий результат коштує більшого внеску — це і
			// показує, наскільки ціль посильна, а не лише чи вона досяжна.
			if goalAmount > 0 && d.key != "actual" {
				row.RequiredMonthly = round2(domain.RequiredMonthlySleeves(
					buildSleeves(1, d.ratePP), d.deval, goalAmount, deadlineMonths))
			}
			// Ставку показуємо ту, під яку реально росте основна валюта
			// портфеля, а не середню по лікарні.
			for _, s := range sl {
				if s.Currency == money.UAH {
					row.RatePct = round2(s.RatePct)
					row.RateTerminalPct = round2(s.RateTerminalPct)
				}
				row.ByCurrency = append(row.ByCurrency, state.SleeveRow{
					Currency: s.Currency, RatePct: round2(s.RatePct),
					RateTerminalPct: round2(s.RateTerminalPct),
					ContribMonthly:  round2(s.ContribUAH),
					Amount:          round2(res.ByCurrency[s.Currency]),
				})
			}
			if goalAmount > 0 {
				row.GoalPct = math.Round(res.TodayUAH/goalAmount*1000) / 10
				hit := domain.MonthsToReachSleeves(sl, d.deval, goalAmount, goalHorizonMonths)
				row.GoalMonths = hit
				if hit > 0 {
					row.GoalDate = string(domain.NewDate(today.Time().AddDate(0, hit, 0)))
				}
			}
			f.Rows = append(f.Rows, row)
		}
		forecast = f
	}

	nbuAt, _ := s.st.GetSetting(ctx, nbuRefreshedKey) //nolint:errcheck // порожньо = довідник ще не оновлювався; це не привід валити стан

	// --- накопичений купонний дохід (НКД) на сьогодні ---
	// Гроші, які вже зароблені, але ще не виплачені. Показуємо ОКРЕМО, а не
	// додаємо в капітал проєкцій: у симуляції майбутні купони вже враховані
	// повністю, тож додавання НКД було б подвійним рахунком.
	var accruedUAH int64
	for _, l := range lots {
		q := domain.RemainingQtyNow(l, sales)
		if q == 0 {
			continue
		}
		acc, err := domain.EstimateAccrued(pays, l.ISIN, today)
		if err != nil || acc == nil || acc.IsZero() {
			continue
		}
		if u, err := fx.ToUAH(money.New(acc.Amount()*q, acc.Currency().Code), rates); err == nil {
			accruedUAH += u.Amount()
		}
	}

	// --- валютне ребалансування: як вийти на цільові частки ---
	// Рахуємо від СУКУПНОГО капіталу (номінал + рахунок): щоб частка валюти
	// стала цільовою, треба довести номінал цієї валюти до target×капітал.
	// Окремо перевіряємо здійсненність: найдешевший папір може бути більший
	// за всю цільову суму — тоді ціль поки недосяжна без перекосу структури.
	totalMajor := float64(nominalUAH+accountUAHMinor) / 100
	targets := map[string]*float64{money.USD: settings.USDTargetSharePct, money.EUR: settings.EURTargetSharePct}
	var rebalance []state.RebalanceRow
	for _, cur := range []string{money.USD, money.EUR} {
		tp := targets[cur]
		if tp == nil || *tp <= 0 {
			continue
		}
		rateMajor := float64(rates[cur]) / fx.RateScale // грн за одиницю валюти
		if rateMajor <= 0 {
			continue
		}
		curUAH := float64(nominalByCur[cur]) / 100 * rateMajor
		currentPct := 0.0
		if totalMajor > 0 {
			currentPct = curUAH / totalMajor * 100
		}
		targetUAH := totalMajor * (*tp) / 100
		deficitUAH := math.Max(0, targetUAH-curUAH)
		cashNative := float64(bal[cur]) / 100
		// Одиниця входу з ПРІОРИТЕТОМ облігації: якщо найдешевший папір
		// вписується в цільову частку — радимо його (безподатковий купон,
		// справжній інструмент). Вклад ($100/€100) — запасний, менший вхід
		// лише коли до облігації ще не доросли: доти картка казала «ще
		// зарано» на $1000-й папір, хоча частку добирає й вклад на $100.
		bondNative := float64(minNoms[cur]) / 100
		bondUAH := bondNative * rateMajor
		depNative := 0.0
		if dm, ok := depMinByCur[cur]; ok {
			depNative = float64(dm) / 100
		}
		var unitNative, unitUAH float64
		var unitKind string
		switch {
		case bondNative > 0 && bondUAH <= targetUAH:
			unitNative, unitUAH, unitKind = bondNative, bondUAH, "bond"
		case depNative > 0:
			unitNative, unitUAH, unitKind = depNative, depNative*rateMajor, "deposit"
		default:
			unitNative, unitUAH, unitKind = bondNative, bondUAH, "bond"
		}
		var canBuy int64
		convertUAH := 0.0
		if unitNative > 0 {
			canBuy = int64(cashNative / unitNative)
			if cashNative < unitNative {
				convertUAH = (unitNative - cashNative) * rateMajor
			}
		}
		rebalance = append(rebalance, state.RebalanceRow{
			Currency: cur, TargetPct: *tp, CurrentPct: round2(currentPct),
			DeficitUAH: round2(deficitUAH), DeficitNative: round2(deficitUAH / rateMajor),
			CashNative: round2(cashNative), BondCostNative: round2(unitNative),
			BondCostUAH: round2(unitUAH), CanBuy: canBuy, ConvertUAH: round2(convertUAH),
			MinPortfolioUAH: round2(unitUAH / (*tp / 100)),
			Feasible:        unitUAH > 0 && unitUAH <= targetUAH,
			UnitKind:        unitKind,
		})
	}

	// --- процентний ризик: два різні ризики з одного графіка виплат ---
	//
	// Ціновий (сценарії ±п.п.) — лише ОВДП: переоцінюється те, що має
	// вторинний ринок. Перевкладення (коли гроші повернуться) — ОВДП і
	// вклади разом, бо гасяться обидва.
	ptsByCur := map[string][]domain.CashPoint{}
	var backWeighted, backUAH, backSoonUAH float64
	for _, cf := range cashflow {
		yrs := float64(domain.DaysBetween(today, cf.Date)) / 365.0
		if yrs < 0 {
			continue
		}
		c := cf.Amount.Currency().Code
		amt := float64(cf.Amount.Amount()) / 100
		if !domain.IsDepositISIN(cf.ISIN) {
			ptsByCur[c] = append(ptsByCur[c], domain.CashPoint{Years: yrs, Amount: amt})
		}
		// Строк перевкладення — у гривні, щоб валюти складались, і БЕЗ
		// дисконтування: тут питають, коли гроші прийдуть, а не скільки
		// вони варті сьогодні.
		rateMajor := 1.0
		if c != money.UAH {
			rateMajor = float64(rates[c]) / fx.RateScale
		}
		uah := amt * rateMajor
		backUAH += uah
		backWeighted += yrs * uah
		if yrs <= 1 {
			backSoonUAH += uah
		}
	}
	var rateRisk *state.RateRisk
	byCurDur := map[string]float64{}
	var pvUAHTotal, macWeighted float64
	for c, pts := range ptsByCur {
		y := portfolioYieldByCur[c] / 100
		if y <= 0 {
			y = portfolioYield / 100
		}
		mac, mod, pv := domain.Duration(pts, y)
		if pv <= 0 {
			continue
		}
		rateMajor := 1.0
		if c != money.UAH {
			rateMajor = float64(rates[c]) / fx.RateScale
		}
		pvUAH := pv * rateMajor
		pvUAHTotal += pvUAH
		macWeighted += mac * pvUAH
		byCurDur[c] = round2(mod)
	}
	if pvUAHTotal > 0 {
		mac := macWeighted / pvUAHTotal
		mod := mac / (1 + portfolioYield/100)
		scen := make([]state.RiskScenario, 0, 4)
		for _, d := range []float64{-2, -1, 1, 2} {
			chg := domain.PriceChangePct(mod, d)
			scen = append(scen, state.RiskScenario{
				DeltaPP: d, ChangePct: round2(chg), ChangeUAH: round2(chg / 100 * pvUAHTotal),
			})
		}
		rateRisk = &state.RateRisk{
			DurationYears: round2(mac), ModifiedDur: round2(mod), PVUAH: round2(pvUAHTotal),
			ByCurrency: byCurDur, Scenarios: scen,
		}
	}
	// Строк перевкладення живе й без облігацій: портфель із самих вкладів
	// цінового ризику не має, але питання «коли перевкладати» — має.
	if backUAH > 0 {
		if rateRisk == nil {
			rateRisk = &state.RateRisk{}
		}
		rateRisk.ReinvestYears = round2(backWeighted / backUAH)
		rateRisk.ReturningUAH = round2(backUAH)
		rateRisk.ReinvestSoonUAH = round2(backSoonUAH)
	}

	// --- ліквідність: коли гроші стають доступні ---
	// Питання не про дохідність, а про те, що робити, коли гроші раптом
	// знадобились. Вікна НАКОПИЧУВАЛЬНІ: «за 90 днів» уже містить «за
	// 30», бо саме так на нього й дивляться — скільки буде в розпорядженні
	// на той момент, якщо нічого не купувати.
	d30 := domain.NewDate(now.AddDate(0, 0, 30))
	d90 := domain.NewDate(now.AddDate(0, 0, 90))
	in30, in90 := accountUAHMinor, accountUAHMinor
	for _, cf := range cashflow {
		if cf.Date.After(d90) {
			continue
		}
		u, cerr := fx.ToUAH(cf.Amount, rates)
		if cerr != nil {
			continue
		}
		if !cf.Date.After(d30) {
			in30 += u.Amount()
		}
		in90 += u.Amount()
	}
	var lockedUAH int64
	var unlockDate domain.Date
	for _, dep := range termDeposits {
		// Вклад, що гаситься у вікні, вже порахований потоками вище —
		// інакше та сама сума стояла б і в «доступному», і в «замкненому».
		if dep.ClosedDate != "" || !dep.Active(today) || !dep.MaturityDate.After(d90) {
			continue
		}
		if u, cerr := fx.ToUAH(money.New(dep.BalanceAt(today), dep.Currency), rates); cerr == nil {
			lockedUAH += u.Amount()
		}
		if unlockDate == "" || dep.MaturityDate.Before(unlockDate) {
			unlockDate = dep.MaturityDate
		}
	}
	liquidity := &state.Liquidity{
		NowUAH:     round2(float64(accountUAHMinor) / 100),
		In30UAH:    round2(float64(in30) / 100),
		In90UAH:    round2(float64(in90) / 100),
		LockedUAH:  round2(float64(lockedUAH) / 100),
		UnlockDate: string(unlockDate),
	}

	return state.Build(state.Input{
		Now: now, Positions: positions, Cashflow: cashflow, Ladder: ladder,
		Rates: rates, MonthInvestedUAH: monthInv, MonthDepositedUAH: monthDep,
		MonthWithdrawnUAH: monthOut,
		MonthTargetUAH:    target,
		UninvestedUAH:     unin, AccountUAH: account, ReinvestMinUAH: reinvestMin,
		Accounts: accounts, Brokers: brokers, InvestedByBroker: investedByBroker,
		LadderUAH: ladderUAH, Income12m: income12m, Coupons12m: coupons12m,
		FundsUAH: round2(fundsUAH), Funds: fundRows,
		DepositsUAH: round2(depositsUAH), DepositsUAHByCur: depositsUAHByCur,
		IncomeMonthlyNow: incomeMonthlyNow,
		ReinvestMinByCur: reinvestMinByCur, TopN: 5,
		Settings: settings, XIRRPct: xirr, PortfolioYieldPct: portfolioYield,
		FundsYieldPct: fundsYield, BlendedYieldPct: blendedYield,
		PortfolioYield:    portfolioYieldByCur,
		FundsYieldRealPct: fundsYieldReal, BlendedYieldRealPct: blendedYieldReal,
		PortfolioYieldReal: portfolioYieldRealByCur,
		Projection:         projection, ProjectionRatePct: capRate, Forecast: forecast,
		Rebalance: rebalance, RateRisk: rateRisk, Liquidity: liquidity,
		AccruedUAH: round2(float64(accruedUAH) / 100), NBURefreshedAt: nbuAt,
		ActualMonthlyUAH: actualMonthly, ActualMonths: actualMonths,
	})
}
