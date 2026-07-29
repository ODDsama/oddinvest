// Збирання документа стану — головна операція сервісу.
//
// Читання сховища — у state_sources.go, зведення лотів і фондів — у
// domain/holdings.go, гаманець — у state_cash.go. Там же й попередження
// про те, що ті самі гроші рахує ще cashEvents у cashflow.go: воно
// переїхало РАЗОМ із кодом, до якого стосується. Коментар-ADR за дві
// функції від того, що він пояснює, читають випадково, а не тоді, коли
// він потрібен.

package api

import (
	"context"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
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
	today := domain.NewDate(now)
	// Усі читання сховища — одним місцем (state_sources.go). Доти вони
	// були розсипані по всій функції, і ListDeposits через це викликався
	// двічі за пʼятсот рядків один від одного.
	src, err := s.loadSources(ctx, today)
	if err != nil {
		return nil, err
	}
	lots, sales, bonds, pays := src.lots, src.sales, src.bonds, src.pays
	rates, deval := src.rates, src.deval
	fundOps, termDeposits := src.fundOps, src.termDeposits

	// Чим володіємо — зведене за ОДИН прохід (domain/holdings.go). Доти
	// lots обходився тут сімома циклами, а залишок після продажів
	// рахувався по чотири рази на лот, щоразу наново.
	payoutDays := make(map[string]int64, len(src.fundRefs))
	for name, ref := range src.fundRefs {
		payoutDays[name] = ref.PayoutDay
	}
	hold := domain.NewHoldings(lots, sales, bonds, fundOps, payoutDays, today)

	positions, err := domain.Positions(bonds, pays, lots, sales, today)
	if err != nil {
		return nil, err
	}
	// Розклад — календар виплат і драбина погашень (state_schedule.go).
	sch, err := buildSchedule(src, hold, today)
	if err != nil {
		return nil, err
	}
	cashflow, ladder := sch.Cashflow, sch.Ladder

	// Тіло діючих вкладів у грн-екв: усього й по валютах — для капіталу й
	// валютних часток. Розірвані/погашені не рахуємо: їхнє тіло вже не
	// «в портфелі», воно повернулось на рахунок.
	depositsUAH := 0.0
	depositsUAHByCur := map[string]float64{}
	depositExposureUAH := map[string]float64{} // банк → тіло, грн-екв.
	// Тіло вкладів у НАТИВНІЙ валюті — для рукавів проєкції: вони рахують
	// у своїй валюті, а не в грн-еквіваленті.
	depositBodyByCur := map[string]float64{}
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
		// Банк вкладу — такий самий контрагент, як брокер: гроші замкнені
		// саме в ньому. Ліміт концентрації рахується по обох разом, бо
		// питання «скільки я втрачу, якщо ця установа зникне» від того,
		// брокер це чи банк, не залежить.
		depositExposureUAH[dep.Bank] += v
		depositBodyByCur[dep.Currency] += float64(dep.BalanceAt(today)) / 100
	}

	// Резерв («матрац») — журнал рухів, поточний залишок це Σ сум. Читаємо
	// весь журнал, а не агрегат: потрібні ще й місця зберігання та дата
	// останнього руху, а рухів тут одиниці.
	//
	// НЕ додається ні до accounts, ні до brokers: перше зіпсувало б звірку
	// (now_uah == account_uah), друге зробило б резерв купівельною
	// спроможністю, і помічник запропонував би купити папір за аварійні
	// гроші.
	reserveOps := src.reserveOps
	reserveUAH := 0.0
	reserveByCur := map[string]float64{}
	reserveUAHByCur := map[string]float64{}
	reservePlaces := map[string]float64{}
	reserveLastMove := ""
	for _, op := range reserveOps {
		u, cerr := fx.ToUAH(money.New(op.Amount, op.Currency), rates)
		if cerr != nil {
			continue
		}
		v := float64(u.Amount()) / 100
		reserveUAH += v
		reserveUAHByCur[op.Currency] += v
		reserveByCur[op.Currency] += float64(op.Amount) / 100
		place := op.Place
		if place == "" {
			place = "без місця"
		}
		reservePlaces[place] += v
		if string(op.Date) > reserveLastMove {
			reserveLastMove = string(op.Date)
		}
	}
	// Місця й валюти, що вийшли в нуль (усе забрали), прибираємо: рядок
	// «сейф — 0 ₴» описує не стан, а історію, і в картці лише заважає.
	for k, v := range reservePlaces {
		if math.Abs(v) < 0.005 {
			delete(reservePlaces, k)
		}
	}
	for k, v := range reserveByCur {
		if math.Abs(v) < 0.005 {
			delete(reserveByCur, k)
			delete(reserveUAHByCur, k)
		}
	}

	// внески місяця: покупки поточного місяця в грн-еквіваленті
	monthInv := money.New(0, money.UAH)
	for _, l := range hold.Lots {
		// Уся куплена кількість, а не залишок: питання «скільки я вклав
		// цього місяця», і продаж наступного дня факту покупки не скасовує.
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
	for _, d := range src.deposits {
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
	// Резерв рахується в тому самому нетто, і саме тому, що переміщення
	// гаманець → матрац записується ДВОМА ногами (мінус у deposits, плюс
	// тут): порізно перша нога виглядала б як втрата капіталу, а разом
	// вони дають нуль, як і має бути. Відкладені зовні гроші, які на
	// рахунок брокера не заходили, це й далі чесний внесок.
	for _, op := range reserveOps {
		if op.Date.Year() != now.Year() || int(op.Date.Month()) != int(now.Month()) {
			continue
		}
		if op.Amount < 0 {
			if u, cerr := fx.ToUAH(money.New(-op.Amount, op.Currency), rates); cerr == nil {
				if sum, aerr := monthOut.Add(u); aerr == nil {
					monthOut = sum
				}
			}
		}
		if u, cerr := fx.ToUAH(money.New(op.Amount, op.Currency), rates); cerr == nil {
			if sum, aerr := monthDep.Add(u); aerr == nil {
				monthDep = sum
			}
		}
	}

	// Неперевкладені: надійшлі виплати без статусу reinvested. Рахуються по
	// ВСІХ інструментах із розкладом — купони й погашення ОВДП тут, відсотки
	// й тіло вкладів нижче, у їхньому циклі. Правило одне: запланована
	// виплата, що вже надійшла і не позначена «перевкладено», — це гроші,
	// які лежать без діла.
	statuses := src.statuses

	// Чому дата сама по собі не відповідь і навіщо тут кнопка «Отримано» —
	// у domain.Arrived. Предикат один на застосунок навмисно: доти його
	// було три, і два перевіряли різне.
	arrived := domain.Arrived(statuses, today)
	pastCF, err := domain.FuturePayments(pays, lots, sales, "1970-01-01")
	if err != nil {
		return nil, err
	}
	// Дохід і покупки збираємо подіями, а рахуємо простій наприкінці —
	// коли вже відомий баланс рахунків, яким число обмежується.
	var incomeEvents, purchaseEvents []domain.CashEvent
	// --- гаманець: (брокер × валюта), зведення по валютах виводиться ---
	// Формула: Σ поповнень + Σ конвертацій + Σ отриманих виплат −
	// Σ вартості лотів (усе нативно, у своїй валюті). Див. state_cash.go
	// про те, чому акумулятор тут один, а не два.
	cash := newCashLedger()
	for _, cf := range pastCF {
		if !arrived(cf.ISIN, cf.Date) {
			continue
		}
		// Тут рахунок НЕ кредитується: ту саму виплату нижче розносить по
		// брокерах цикл по pays×lots, і зведення по валютах виводиться вже
		// з нього. Доти це були два різні обчислення одного числа —
		// агрегат по (дата, ISIN, тип) проти суми по лотах, — і сходились
		// вони лише тому, що MulQty множить цілі мінорні одиниці й тому
		// точно лінійна. Домовленість, а не механізм.
		uahAmt, err := fx.ToUAH(cf.Amount, rates)
		if err != nil {
			return nil, err
		}
		incomeEvents = append(incomeEvents, domain.CashEvent{Date: cf.Date, Amount: uahAmt.Amount()})
	}

	// купон кредитує рахунок ТОГО брокера, де куплено папір.
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
				cash.add(l.Channel, amt.Currency().Code, amt.Amount())
			}
		}
	}

	for k, amt := range src.depByBC {
		cash.add(k.Broker, k.Currency, amt)
	}
	for k, net := range src.convBC {
		cash.add(k.Broker, k.Currency, net)
	}
	for _, l := range hold.Lots {
		cost := domain.MulQty(l.PricePerBond, l.Qty)
		if l.Fee != nil && !l.Fee.IsZero() {
			if cost, err = cost.Add(l.Fee); err != nil {
				return nil, err
			}
		}
		cash.add(l.Channel, cost.Currency().Code, -cost.Amount())
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
		cash.add(op.Broker, op.Currency, delta)
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
		// розміщення: −тіло на дату відкриття (якщо вона вже настала)
		if !dep.OpenDate.After(today) {
			cash.add(dep.Bank, dep.Currency, -dep.Principal)
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
				cash.add(dep.Bank, dep.Currency, -t.Amount)
				if u, cerr := fx.ToUAH(money.New(t.Amount, dep.Currency), rates); cerr == nil {
					purchaseEvents = append(purchaseEvents, domain.CashEvent{Date: t.Date, Amount: u.Amount()})
				}
			}
		}
		if dep.ClosedDate != "" {
			if !dep.ClosedDate.After(today) {
				cash.add(dep.Bank, dep.Currency, dep.ClosedAmount)
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
			cash.add(dep.Bank, cf.Amount.Currency().Code, cf.Amount.Amount())
			// Відсотки вкладу — такий самий дохід, як купон, і в чергу
			// простою стають нарівні з ним.
			if u, cerr := fx.ToUAH(cf.Amount, rates); cerr == nil {
				incomeEvents = append(incomeEvents, domain.CashEvent{Date: cf.Date, Amount: u.Amount()})
			}
		}
	}

	// Гаманець зібрано. Далі — лише похідні від нього зрізи: зведення по
	// валютах і розклад по брокерах. Обидва ВИВОДЯТЬСЯ, тож розійтись їм
	// більше нема як.
	bal := cash.byCurrency()
	brokers := cash.byBroker()

	// Скільки грошей стоїть за КОЖНИМ контрагентом, грн-екв. — для ліміту
	// концентрації. Це не investedByBroker нижче: там собівартість, бо
	// картка «Вкладено по брокерах» відповідає на «скільки я туди заніс».
	// Тут питання інше — «скільки я втрачу, якщо цей брокер чи банк завтра
	// зникне», а на нього відповідає сьогоднішня вартість: номінал
	// паперів, ринкова вартість сертифікатів, тіло вкладу й готівка.
	//
	// Резерву тут немає: у нього не брокер, а «місце» (готівка, сейф), і
	// ризик контрагента до нього не застосовний — у цьому й сенс матраца.
	brokerExposureUAH := map[string]float64{}
	addExposure := func(name string, uah float64) {
		if name == "" {
			name = "—"
		}
		brokerExposureUAH[name] += uah
	}

	// Вкладено по брокерах (грн-екв.): дзеркалить логіку Positions —
	// ціна×залишок + пропорційна комісія, лише згруповано по брокеру.
	investedByBroker := map[string]float64{}
	for _, l := range hold.Lots {
		rem := l.Remaining
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
		// Друге зведення фондів тут більше не будується: Holdings уже має
		// його, і в сталому порядку. Доти це була свіжа мапа, і саме тому
		// нікого не турбувало, що будівник дописує PayoutDay у першу.
		for _, f := range hold.Funds {
			pos := f.FundPosition
			byBroker := boughtByFundBroker[pos.Fund]
			var totalBought int64
			for _, v := range byBroker {
				totalBought += v
			}
			if totalBought == 0 || pos.CostBasis == 0 {
				continue
			}
			// Ринкова вартість тієї самої позиції, поділена так само: для
			// ризику контрагента важить не те, скільки ти заніс, а те,
			// скільки там лежить зараз. Рахує домен — ціна зберігається
			// ×10⁴, і власна арифметика тут розійшлася б із рештою.
			mvMinor := pos.MarketValue()
			for b, v := range byBroker {
				share := money.New(pos.CostBasis*v/totalBought, pos.Currency)
				if u, uerr := fx.ToUAH(share, rates); uerr == nil {
					investedByBroker[b] += float64(u.Amount()) / 100
				}
				if u, uerr := fx.ToUAH(money.New(mvMinor*v/totalBought, pos.Currency), rates); uerr == nil {
					addExposure(b, float64(u.Amount())/100)
				}
			}
		}
	}

	// Решта експозиції контрагентів: папери за номіналом, готівка на
	// рахунках і тіла вкладів (сертифікати додались вище, разом зі своїм
	// поділом по брокерах).
	for _, l := range hold.Lots {
		if !l.Held() {
			continue
		}
		nom := l.Bond.Nominal
		if u, err := fx.ToUAH(money.New(nom.Amount()*l.Remaining, nom.Currency().Code), rates); err == nil {
			addExposure(l.Channel, float64(u.Amount())/100)
		}
	}
	// Валюти — у сталому порядку, і це не косметика. Додавання float64 не
	// асоціативне, тож обхід мапи давав брокеру, який тримає дві валюти,
	// суму, що різнилась у останніх бітах від запуску до запуску. Саме так
	// число, яке лягло рівно на пів копійки, округлялось то вниз, то вгору:
	// over_uah у концентрації показував 215026.58 або .59 на тих самих
	// даних. Дві сусідні перезавантаження сторінки — дві різні копійки, і
	// в добовий знімок потрапляла та, яка випала.
	for name, byCur := range brokers {
		curs := make([]string, 0, len(byCur))
		for cur := range byCur {
			curs = append(curs, cur)
		}
		sort.Strings(curs)
		for _, cur := range curs {
			if u, err := fx.ToUAH(money.New(int64(math.Round(byCur[cur]*100)), cur), rates); err == nil {
				addExposure(name, float64(u.Amount())/100)
			}
		}
	}
	for bank, v := range depositExposureUAH {
		addExposure(bank, v)
	}

	// Розклад, зведений у показники документа (state_schedule.go).
	inc := summarizeIncome(sch, rates, today)
	ladderUAH := inc.LadderUAH
	income12m, coupons12m := inc.Income12m, inc.Coupons12m
	incomeMonthlyNow := inc.MonthlyNow

	// Сертифікати фондів — рядки картки й зважена дохідність
	// (state_funds.go).
	fnd := buildFunds(src, hold, rates, deval, today)
	fundRows, fundsUAH := fnd.Rows, fnd.TotalUAH
	fundValueByCur := fnd.ValueByCur
	fundsYield, fundsYieldReal := fnd.YieldPct, fnd.YieldRealPct
	// Зведена по портфелю — окремо від фондової: це третє число, а не
	// уточнення другого, і рахується воно нижче, коли вже відома
	// облігаційна частина. Парою навмисно: номінальна без реальної на
	// екрані читається як помилка.
	var blendedYield, blendedYieldReal float64

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
	minNoms := src.minNominal
	// Мінімум по валютах у мінорних: спершу найдешевший папір (ОВДП), потім
	// зливаємо мінімум вкладу. Вклад — теж інструмент реінвесту, тож там, де
	// його поріг нижчий (або де паперу у валюті немає), «до реінвесту
	// готовий» настає раніше. Саме це дає простою USD/EUR куди йти без
	// відповідних облігацій.
	depMinByCur := src.depositMin
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

	// Ціна ОДНОГО сертифіката — найдешевшого з тих, що вже в портфелі.
	// Каталогу цін фондів у застосунку немає (ціна приходить із виписки
	// разом з операцією), тож про фонд, якого ще не купували, сказати
	// нічого не можна: 0 означає «невідомо», і ребаланс тоді просто не
	// перевіряє здійсненність, а не вигадує поріг.
	minFundPriceUAH := 0.0
	for _, row := range fundRows {
		if row.LastPrice <= 0 {
			continue
		}
		cur := row.Currency
		if cur == "" {
			cur = money.UAH
		}
		minOfFund := row.LastPrice
		if cur != money.UAH {
			u, err := fx.ToUAH(money.New(int64(math.Round(row.LastPrice*100)), cur), rates)
			if err != nil {
				continue
			}
			minOfFund = float64(u.Amount()) / 100
		}
		if minFundPriceUAH == 0 || minOfFund < minFundPriceUAH {
			minFundPriceUAH = minOfFund
		}
	}

	// Найдешевший вхід ОКРЕМО по видах — для ребалансу за видом
	// інструмента. reinvestMin для цього не годиться: він уже змішав
	// облігації з вкладами (у цьому й був його сенс — «на що завгодно
	// вистачає раніше»), а тут питання саме «скільки коштує зайти в цей
	// вид», і змішане число відповіло б на нього неправильно для обох.
	minBondUAH, minDepositUAH := 0.0, 0.0
	minOf := func(dst *float64, minor int64, cur string) {
		u, err := fx.ToUAH(money.New(minor, cur), rates)
		if err != nil {
			return
		}
		v := float64(u.Amount()) / 100
		if *dst == 0 || v < *dst {
			*dst = v
		}
	}
	for cur, minNom := range minNoms {
		minOf(&minBondUAH, minNom, cur)
	}
	for cur, depMin := range depMinByCur {
		minOf(&minDepositUAH, depMin, cur)
	}

	// Налаштування — одним проходом по реєстру (settings_registry.go).
	// Доти двадцять ключів читались циклом, ще шість — окремими блоками
	// поруч, і два мали третє читання в інших файлах.
	settings := src.settings
	// MonthlyTargetUAH проставляється НИЖЧЕ, коли target уже порахований.
	// Тут стояла перевірка `if !target.IsZero()` — і вона ніколи не
	// спрацьовувала: target отримує значення лише в блоці місячного плану,
	// на чотириста рядків далі. Тобто поле settings.monthly_target_uah
	// жива служба не віддавала жодного разу, а у фікстурі воно є тільки
	// тому, що тест проставляє його руками.
	//
	// Список брокерів більше не зберігається рядком — він збирається з
	// довідника. У зведенні лишається як рядок навмисно: це похідне поле
	// для випадайок, а не місце зберігання, і сутності HA, які на нього
	// підписані, не мусять знати про зміну схеми.
	if len(src.brokers) > 0 {
		names := make([]string, 0, len(src.brokers))
		for _, b := range src.brokers {
			names = append(names, b.Name)
		}
		settings.Channels = strings.Join(names, ", ")
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
	var nominalUAH int64                // сумарний номінал у грн-екв.
	nominalByCur := map[string]int64{}  // номінал нативно по валютах
	nominalByISIN := map[string]int64{} // номінал по ПАПЕРАХ, грн-екв.
	ytmLotsByCur := map[string][]domain.YTMLot{}
	var ytmWeightUAH, ytmWeightedUAH, ytmWeightedRealUAH float64
	for _, l := range hold.Lots {
		if !l.Held() {
			continue
		}
		b, q := l.Bond, l.Remaining
		cur := b.Nominal.Currency().Code
		nominalByCur[cur] += b.Nominal.Amount() * q
		if n, err := fx.ToUAH(money.New(b.Nominal.Amount()*q, cur), rates); err == nil {
			nominalUAH += n.Amount()
			// Частка на ОДИН папір рахувалась тут транзитом і викидалась —
			// а це єдиний вимір диверсифікації, якого в застосунку не було
			// зовсім: «половина портфеля в одному ISIN» побачити не було де.
			nominalByISIN[l.ISIN] += n.Amount()
		}
		lot := ytmLot(l.Lot, q)
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
	if len(src.deposits) > 0 {
		first := today
		var totalUAH int64
		for _, d := range src.deposits {
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

	// Капітал — один раз і на всіх. Далі його читають ребаланс, старт
	// проєкції й сам документ; доти кожен з них складав свою суму, і на
	// сусідніх картках стояли числа, які не сходились. Номінал по валютах
	// переводимо в грн-екв. тут, бо частки міряються в спільній одиниці.
	bondsByCurUAH := map[string]float64{}
	for cur, minor := range nominalByCur {
		if u, err := fx.ToUAH(money.New(minor, cur), rates); err == nil {
			bondsByCurUAH[cur] = float64(u.Amount()) / 100
		}
	}
	capital := state.Capital{
		BondsUAH: nominalMajor, AccountUAH: float64(accountUAHMinor) / 100,
		FundsUAH: fundsUAH, DepositsUAH: depositsUAH, ReserveUAH: reserveUAH,
		BondsByCur: bondsByCurUAH, DepositsByCur: depositsUAHByCur,
		ReserveByCur: reserveUAHByCur,
	}

	if nominalMajor+fundsUAH > 0 {
		blend := func(bond, fund float64) float64 {
			return math.Round((bond*nominalMajor+fund*fundsUAH)/(nominalMajor+fundsUAH)*100) / 100
		}
		blendedYield = blend(portfolioYield, fundsYield)
		blendedYieldReal = blend(portfolioYieldReal, fundsYieldReal)
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
	avgRate := src.avgRate

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
			// Замкнений капітал — це НЕ лише номінал ОВДП. Тіло вкладу
			// поводиться точно як номінал паперу: лежить, платить за
			// відомим графіком і повертається в кінці строку, — а
			// сертифікат лежить безстроково й платить дивідендами. Обидва
			// потоки вже стоять у Coupon/Redeem нижче.
			//
			// Доти в базі рукава їх не було, і з цього виходило дві біди.
			// Перша: погашення вкладу приходило в готівку з тіла, якого
			// модель не тримала, — гроші зʼявлялись нізвідки. Друга:
			// колонка «Внесено» стартувала з усього капіталу, а «З
			// реінвестом» — лише з облігацій і рахунку, тож приріст між
			// ними був занижений рівно на фонди й вклади, а на портфелі,
			// де їх більшість, ставав відʼємним.
			//
			// Сертифікат лежить у `locked` і сам не росте: його дохід
			// приходить дивідендами (реальними або обіцяними фондом), а
			// подорожчання ціни застосунок не моделює ніде — див.
			// «Що купити». Це занижує довгі горизонти, і краще так, ніж
			// домальовувати зростання, якого ніхто не обіцяв.
			nom := float64(nominalByCur[cur])/100 + depositBodyByCur[cur] + fundValueByCur[cur]
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

	// Ставка, ЯКУ ПРОЄКЦІЯ СПРАВДІ ВЖИЛА: середня по рукавах, зважена
	// капіталом кожного в грн-екв.
	//
	// Доти сюди йшла зведена дохідність (YTM облігацій + дохідність фондів),
	// і поле projection_rate_pct обіцяло «ставку реінвесту, що використана»,
	// хоч жоден рукав її не бачив: кожен рахував за власним YTM своєї
	// валюти. Число стояло на екрані як пояснення до кривої, якої воно не
	// пояснювало.
	capRate := 0.0
	if sl := buildSleeves(0, 0); len(sl) > 0 {
		var w, wr float64
		for _, s := range sl {
			base := (s.Cash0 + s.Nominal0) * s.Rate0
			w += base
			wr += base * s.RatePct
		}
		if w > 0 {
			capRate = round2(wr / w)
		}
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
	// Ось ТУТ місячний план нарешті існує — і тільки тепер його можна
	// покласти в налаштування. Раніше присвоєння стояло на чотириста
	// рядків вище, де target ще нуль, і поле не віддавалось ніколи.
	if !target.IsZero() {
		v := float64(target.Amount()) / 100
		settings.MonthlyTargetUAH = &v
	}

	// Старт проєкції — капітал БЕЗ резерву. Решта входить уся, разом із
	// сертифікатами й вкладами: інакше крива починалась би нижче за
	// плитку «Капітал» на ту саму суму (доти так і було — вклади сюди не
	// потрапляли зовсім).
	//
	// Резерв — свідомий виняток, і саме тому він тут віднімається явно, а
	// не «просто не додається»: він не інвестується й не компаундиться,
	// тож включити його означало б показати приріст на гроші, які лежать
	// без руху. Через це проєкція стартує нижче за плитку рівно на суму
	// матраца — і це правильно, а не розбіжність, яку треба ховати.
	p0 := capital.TotalUAH() - capital.ReserveUAH
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

	nbuAt := src.nbuAt

	// --- накопичений купонний дохід (НКД) на сьогодні ---
	// Гроші, які вже зароблені, але ще не виплачені. Показуємо ОКРЕМО, а не
	// додаємо в капітал проєкцій: у симуляції майбутні купони вже враховані
	// повністю, тож додавання НКД було б подвійним рахунком.
	var accruedUAH int64
	for _, l := range hold.Lots {
		q := l.Remaining
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
	//
	// Знаменник і чисельник — ТІ САМІ, що в плитці «Частка USD»: обидва
	// числа беруться з capital (див. state/capital.go). Доти ребаланс
	// рахував від «номінал + рахунок» і лише по облігаціях, а плитка — від
	// усього капіталу з фондами, вкладами й резервом. Стояли вони на
	// одному екрані й показували різне: 57.6% проти 0% для тієї самої
	// валюти.
	//
	// Окремо перевіряємо здійсненність: найдешевший папір може бути більший
	// за всю цільову суму — тоді ціль поки недосяжна без перекосу структури.
	totalMajor := capital.TotalUAH()
	targets := map[string]*float64{money.USD: settings.USDTargetSharePct, money.EUR: settings.EURTargetSharePct}
	var rebalance []state.RebalanceRow
	for _, cur := range []string{money.USD, money.EUR} {
		tp := targets[cur]
		if tp == nil || *tp <= 0 {
			continue
		}
		rateMajor, ok := fx.RateMajor(cur, rates)
		if !ok {
			continue // курсу немає — рядок чесніше не малювати
		}
		curUAH := capital.ExposureUAH(cur)
		currentPct := capital.SharePct(cur)
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
			Dimension: "currency", Key: cur,
			Currency: cur, TargetPct: *tp, CurrentPct: round2(currentPct),
			DeficitUAH: round2(deficitUAH), DeficitNative: round2(deficitUAH / rateMajor),
			CashNative: round2(cashNative), BondCostNative: round2(unitNative),
			BondCostUAH: round2(unitUAH), CanBuy: canBuy, ConvertUAH: round2(convertUAH),
			MinPortfolioUAH: round2(unitUAH / (*tp / 100)),
			Feasible:        unitUAH > 0 && unitUAH <= targetUAH,
			UnitKind:        unitKind,
		})
	}

	// --- ребаланс за ВИДОМ інструмента ---
	//
	// Валютна ціль каже, в чому тримати гроші; ця — чим ризикувати.
	// Портфель на 100% в ОВДП і портфель на 100% у фондах можуть мати
	// однакові валютні частки й геть різну поведінку: у першого ризик
	// процентний і державний, у другого — ринковий і керуючої компанії.
	//
	// Сума цілей НЕ мусить давати 100. Нормалізувати мовчки означало б
	// підмінити введене користувачем: поставив 40/20 — і отримав 67/33,
	// не питаючи. Нерозподілене показуємо числом і лишаємо як є.
	kindTargets := []struct {
		key    string
		nowUAH float64
		target *float64
		unit   float64 // найдешевший вхід у цей вид, грн-екв.; 0 = невідомо
	}{
		{"bonds", capital.BondsUAH, settings.TargetBondsPct, minBondUAH},
		{"funds", capital.FundsUAH, settings.TargetFundsPct, minFundPriceUAH},
		{"deposits", capital.DepositsUAH, settings.TargetDepositsPct, minDepositUAH},
	}
	for _, k := range kindTargets {
		if k.target == nil || *k.target <= 0 || totalMajor <= 0 {
			continue
		}
		currentPct := k.nowUAH / totalMajor * 100
		targetUAH := totalMajor * (*k.target) / 100
		row := state.RebalanceRow{
			Dimension: "kind", Key: k.key, Currency: money.UAH,
			TargetPct: round2(*k.target), CurrentPct: round2(currentPct),
			DeficitUAH: round2(math.Max(0, targetUAH-k.nowUAH)),
			// Одиниця входу тут завжди в гривні-еквіваленті: питання «яким
			// інструментом», а не «якою валютою», і мішати сюди ще й
			// нативні суми означало б два виміри в одному рядку.
			BondCostUAH: round2(k.unit), UnitKind: k.key,
			// Без заданої одиниці входу здійсненність не перевіряється:
			// у резерв кладуть будь-яку суму, а не «мінімальний внесок».
			Feasible: k.unit == 0 || k.unit <= targetUAH,
		}
		if k.unit > 0 {
			row.MinPortfolioUAH = round2(k.unit / (*k.target / 100))
		}
		rebalance = append(rebalance, row)
	}
	// Резерв — рядок БЕЗ цілі, суто довідковий. Спокуса вивести його
	// цільову частку з місяців витрат виглядає природною, але дає число,
	// яке нічого не означає: на живих даних ціль у 150 000 ₴ при капіталі
	// 38 913 ₴ показалась як «385% капіталу». Частка рухається щоразу, коли
	// росте портфель, навіть якщо резерву ніхто не чіпав, — а ціль резерву
	// за природою абсолютна, бо міряється місяцями життя, не портфелем.
	//
	// Тож ціль лишається там, де вона осмислена (картка резерву, у місяцях
	// і гривнях), а сюди резерв входить лише щоб картина складу була
	// повною: без нього частки видів не сходились би з очевидним «а решта
	// де?».
	if capital.ReserveUAH > 0 && totalMajor > 0 {
		rebalance = append(rebalance, state.RebalanceRow{
			Dimension: "kind", Key: "reserve", Currency: money.UAH,
			CurrentPct: round2(capital.ReserveUAH / totalMajor * 100),
			UnitKind:   "reserve", Feasible: true,
		})
	}

	// --- концентрація: де зібрано надто щільно ---
	//
	// Три виміри, три різні питання. Один папір — «що буде, якщо саме цей
	// емітент не заплатить». Одна установа — «що буде, якщо цей брокер чи
	// банк зникне». Один рік драбини — «що буде, якщо саме тоді ставки
	// впадуть»: гроші повернуться всі одразу й підуть за гіршою ставкою.
	//
	// Показуємо ВСІ рядки з заданим лімітом, а не самі порушення: «45% при
	// ліміті 50%» теж варте знання, а список, що з'являється лише коли вже
	// пізно, читається як аварія, а не як приладова панель.
	var concentration []state.ConcentrationRow
	addConc := func(dim, key, label string, amount, base, limit float64) {
		if base <= 0 {
			return
		}
		share := amount / base * 100
		row := state.ConcentrationRow{
			Dimension: dim, Key: key, Label: label,
			AmountUAH: round2(amount), SharePct: round2(share), LimitPct: limit,
		}
		if share > limit {
			row.OverUAH = round2(amount - base*limit/100)
		}
		concentration = append(concentration, row)
	}
	if settings.LimitISINPct != nil && *settings.LimitISINPct > 0 {
		for isin, minor := range nominalByISIN {
			label := ""
			if b, ok := bonds[isin]; ok {
				label = b.Descr
			}
			addConc("isin", isin, label, float64(minor)/100, totalMajor, *settings.LimitISINPct)
		}
		// Фонди — сюди ж. Питання виміру не «скільки в облігаціях», а
		// «скільки залежить від ОДНОГО емітента», і сертифікат відповідає
		// на нього так само, як папір. Без цього список брехав найгучніше
		// саме там, де ризик найбільший: на живих даних один фонд важив
		// 16.7% капіталу — більше за будь-яку окрему облігацію в списку.
		for _, row := range fundRows {
			if row.MarketValue <= 0 {
				continue
			}
			addConc("isin", domain.FundISINPrefix+row.Fund, row.Fund,
				row.MarketValue, totalMajor, *settings.LimitISINPct)
		}
	}
	if settings.LimitBrokerPct != nil && *settings.LimitBrokerPct > 0 {
		for name, v := range brokerExposureUAH {
			addConc("broker", name, "", v, totalMajor, *settings.LimitBrokerPct)
		}
	}
	if settings.LimitYearPct != nil && *settings.LimitYearPct > 0 {
		// База тут — УСІ погашення, а не капітал: питання «чи рівномірно
		// рознесені повернення», і міряти його від капіталу означало б
		// вважати порушенням будь-яку драбину в портфелі, де більшість
		// грошей у фондах, — тобто там, де погашень немає взагалі.
		ladderTotal := 0.0
		for _, y := range ladderUAH {
			ladderTotal += y.UAH
		}
		for _, y := range ladderUAH {
			addConc("year", strconv.Itoa(y.Year), "", y.UAH, ladderTotal, *settings.LimitYearPct)
		}
	}
	// Найщільніше — зверху: список читають згори, і перше, що впадає в
	// око, має бути найбільшим ризиком, а не найдавнішим роком.
	sort.Slice(concentration, func(i, j int) bool {
		if concentration[i].Dimension != concentration[j].Dimension {
			return concentration[i].Dimension < concentration[j].Dimension
		}
		return concentration[i].SharePct > concentration[j].SharePct
	})

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
		// Фонд не переоцінюється зміною ставок ОВДП: у сертифіката немає ні
		// строку, ні фіксованого купона, тож дисконтувати його оцінені
		// дивіденди означало б вигадати ціновий ризик, якого немає.
		if domain.IsFundISIN(cf.ISIN) {
			continue
		}
		if !domain.IsDepositISIN(cf.ISIN) {
			ptsByCur[c] = append(ptsByCur[c], domain.CashPoint{Years: yrs, Amount: amt})
		}
		// Строк перевкладення — у гривні, щоб валюти складались, і БЕЗ
		// дисконтування: тут питають, коли гроші прийдуть, а не скільки
		// вони варті сьогодні.
		rateMajor, _ := fx.RateMajor(c, rates)
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
		rateMajor, _ := fx.RateMajor(c, rates)
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
		ReserveUAH: round2(reserveUAH),
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
		// Capital зібраний вище один раз; state його лише читає.
		Capital:     capital,
		DepositsUAH: round2(depositsUAH),
		ReserveUAH:  round2(reserveUAH), ReserveByCur: reserveByCur,
		ReservePlaces:    reservePlaces,
		ReserveLastMove:  reserveLastMove,
		IncomeMonthlyNow: incomeMonthlyNow,
		ReinvestMinByCur: reinvestMinByCur, TopN: 5,
		Settings: settings, XIRRPct: xirr, PortfolioYieldPct: portfolioYield,
		FundsYieldPct: fundsYield, BlendedYieldPct: blendedYield,
		PortfolioYield:    portfolioYieldByCur,
		FundsYieldRealPct: fundsYieldReal, BlendedYieldRealPct: blendedYieldReal,
		PortfolioYieldReal: portfolioYieldRealByCur,
		Projection:         projection, ProjectionRatePct: capRate, Forecast: forecast,
		Rebalance: rebalance, Concentration: concentration,
		RateRisk: rateRisk, Liquidity: liquidity,
		AccruedUAH: round2(float64(accruedUAH) / 100), NBURefreshedAt: nbuAt,
		ActualMonthlyUAH: actualMonthly, ActualMonths: actualMonths,
	})
}
