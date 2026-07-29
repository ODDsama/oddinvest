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

	// Рухи поточного місяця й фактичний темп (state_month.go).
	mth, err := buildMonth(src, hold, rates, now, today)
	if err != nil {
		return nil, err
	}
	monthInv := mth.InvestedUAH
	monthDep, monthOut := mth.DepositedUAH, mth.WithdrawnUAH
	actualMonthly, actualMonths := mth.ActualMonthlyUAH, mth.ActualMonths

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

	// Облігації: номінал і дохідність до погашення (state_bonds.go).
	bnd := buildBonds(hold, pays, rates, deval)
	nominalByCur, nominalByISIN := bnd.NominalByCur, bnd.NominalByISIN
	portfolioYield, portfolioYieldReal := bnd.YieldPct, bnd.YieldRealPct
	portfolioYieldByCur := bnd.YieldByCur
	portfolioYieldRealByCur := bnd.YieldRealByCur

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
	nominalMajor := float64(bnd.NominalUAH) / 100

	// Капітал — один раз і на всіх, і саме тут: це ТОЧКА ЗБІРКИ п'яти
	// інструментів, а не частина котрогось із них. Далі його читають
	// ребаланс, старт проєкції й сам документ; доти кожен з них складав
	// свою суму, і на сусідніх картках стояли числа, які не сходились.
	capital := state.Capital{
		BondsUAH: nominalMajor, AccountUAH: float64(accountUAHMinor) / 100,
		FundsUAH: fundsUAH, DepositsUAH: depositsUAH, ReserveUAH: reserveUAH,
		BondsByCur: bnd.NominalByCurUAH, DepositsByCur: depositsUAHByCur,
		ReserveByCur: reserveUAHByCur,
	}

	blendedYield = blendYield(portfolioYield, fundsYield, nominalMajor, fundsUAH)
	blendedYieldReal = blendYield(portfolioYieldReal, fundsYieldReal, nominalMajor, fundsUAH)

	// Проєкція, місячний план і віяло прогнозів (state_projection.go).
	// Вхід виписаний полем за полем навмисно: проєкція залежить від усіх
	// інструментів одразу, і серед сотні локальних змінних цього не було
	// видно — саме тому сюди роками не потрапляли то вклади, то фонди.
	prj := buildProjection(projectionInput{
		Capital: capital, Cashflow: cashflow, Settings: settings,
		CashByCur: bal, NominalByCur: nominalByCur,
		DepositBodyByCur: depositBodyByCur, FundValueByCur: fundValueByCur,
		YieldByCur: portfolioYieldByCur, AvgRateByCur: src.avgRate,
		ReinvestMinByCur: reinvestMinByCur,
		Rates:            rates, Deval: deval, ActualMonthly: actualMonthly, Today: today,
	})
	// target — місячний план. Не читається з налаштувань: виводиться з
	// цілі й дедлайну (див. state_projection.go).
	target := prj.TargetUAH
	projection, forecast, capRate := prj.Rows, prj.Forecast, prj.CapRatePct
	// Ось ТУТ місячний план нарешті існує — і тільки тепер його можна
	// покласти в налаштування. Раніше присвоєння стояло на чотириста
	// рядків вище, де target ще нуль, і поле не віддавалось ніколи.
	if !target.IsZero() {
		v := float64(target.Amount()) / 100
		settings.MonthlyTargetUAH = &v
	}

	nbuAt := src.nbuAt

	// Ребаланс і концентрація (state_rebalance.go).
	rbl := buildRebalance(rebalanceInput{
		Capital: capital, Settings: settings, Rates: rates,
		CashByCur: bal, MinNominalByCur: minNoms, DepositMinByCur: depMinByCur,
		MinBondUAH: minBondUAH, MinFundUAH: minFundPriceUAH,
		MinDepositUAH: minDepositUAH,
		NominalByISIN: nominalByISIN, Bonds: bonds, FundRows: fundRows,
		BrokerExposureUAH: brokerExposureUAH, LadderUAH: ladderUAH,
	})
	rebalance, concentration := rbl.Rebalance, rbl.Concentration

	// Процентний ризик, ліквідність і НКД (state_risk.go).
	rsk := buildRisk(riskInput{
		Cashflow: cashflow, Holdings: hold, Pays: pays, TermDeposits: termDeposits,
		Rates: rates, YieldPct: portfolioYield, YieldByCur: portfolioYieldByCur,
		AccountMinor: accountUAHMinor, ReserveUAH: reserveUAH,
		Now: now, Today: today,
	})
	rateRisk, liquidity, accruedUAH := rsk.RateRisk, rsk.Liquidity, rsk.AccruedUAH

	// Документ заповнюється НАПРЯМУ, а не через проміжний літерал на
	// пʼятдесят полів: тридцять із них були дзеркалом Doc, тобто пакет
	// state здебільшого переписував із однієї структури в іншу.
	doc := &state.Doc{
		MonthInvestedUAH:  state.Major(monthInv),
		MonthDepositedUAH: state.Major(monthDep),
		MonthWithdrawnUAH: state.Major(monthOut),
		MonthTargetUAH:    state.Major(target),
		UninvestedUAH:     state.Major(unin),
		AccountUAH:        state.Major(account),
		ReinvestMinUAH:    state.Major(reinvestMin),

		Accounts: accounts, Brokers: brokers, InvestedByBroker: investedByBroker,
		LadderUAH: ladderUAH, Income12m: income12m, Coupons12m: coupons12m,
		FundsUAH: round2(fundsUAH), Funds: fundRows,
		DepositsUAH: round2(depositsUAH), ReserveUAH: round2(reserveUAH),
		IncomeMonthlyNow: incomeMonthlyNow, ReinvestMin: reinvestMinByCur,

		Settings: settings, XIRRPct: xirr,
		PortfolioYieldPct: portfolioYield, PortfolioYield: portfolioYieldByCur,
		PortfolioYieldReal: portfolioYieldRealByCur,
		FundsYieldPct:      fundsYield, FundsYieldRealPct: fundsYieldReal,
		FundsYieldBasis: fnd.Basis,
		BlendedYieldPct: blendedYield, BlendedYieldRealPct: blendedYieldReal,

		Projection: projection, ProjectionRatePct: capRate, Forecast: forecast,
		Rebalance: rebalance, Concentration: concentration,
		RateRisk: rateRisk, Liquidity: liquidity,
		AccruedUAH: round2(float64(accruedUAH) / 100), NBURefreshedAt: nbuAt,
		ActualMonthlyUAH: actualMonthly, ActualMonths: actualMonths,
	}
	// Похідні — те, що виводиться з уже покладеного (state/derive.go).
	// Capital зібраний вище один раз; state його лише читає.
	if err := state.Derive(doc, state.DeriveInput{
		Now: now, Positions: positions, Rates: rates, Capital: capital,
		Cashflow: cashflow, Ladder: ladder,
		MonthDeposited: monthDep, MonthTarget: target,
		ReserveByCur: reserveByCur, ReservePlaces: reservePlaces,
		ReserveLastMove: reserveLastMove, TopN: 5,
	}); err != nil {
		return nil, err
	}
	return doc, nil
}
