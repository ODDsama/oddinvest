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
	"github.com/ODDsama/oddinvest/internal/store"
	money "github.com/Rhymond/go-money"
)

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	doc, err := s.buildStateTasked(r.Context(), time.Now())
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

// xirrMinMoneyDays — з якого середньозваженого віку грошей публікується
// XIRR. Доводи за сам поріг — біля місця, де він застосовується; тут він
// іменем, бо їде в документ стану (RealizedRow.MinDays) і звідти в
// пояснення на екрані. Літерал у двох місцях розійшовся б.
const xirrMinMoneyDays = 30

// round2 — округлення до 2 знаків для довідкових (не облікових) чисел.
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// BuildStateDoc — спільна збірка документа стану для API і MQTT.
//
// Із чергою задач, як і GET /api/summary: обидва шляхи ведуть до людини —
// один в браузер, другий у Home Assistant, — і показувати їй різні відповіді
// на «що робити» було б гірше, ніж не показувати жодної.
//
// Решта викликів (whatif, план, cashflow, xirr) лишається на голому
// buildState навмисно: черга їм ні до чого, а вона тягне за собою
// SearchBonds на п'ять тисяч паперів.
func (s *Server) BuildStateDoc(ctx context.Context, now time.Time) (*state.Doc, error) {
	return s.buildStateTasked(ctx, now)
}

// hypothetical — покупки, яких ЩЕ НЕМАЄ. Порожня структура означає
// звичайний стан, і саме тому buildState нижче лишається однорядковою
// обгорткою: жоден із його викликів не знає, що така можливість є.
//
// Навіщо взагалі. Кошик покупки питає «що станеться з портфелем, якщо це
// купити», і відповідь на це — той самий документ стану, тільки над
// портфелем, у якому покупки вже записані. Домішуються вони ОДРАЗУ після
// loadSources, і далі все правильне за побудовою: капітал, валютні
// частки, вид інструмента, драбина, дюрація, концентрація й готівка по
// брокерах рахуються тим самим кодом, що й завжди.
//
// Другого способу порахувати частки в застосунку немає навмисно —
// state.Capital документує, чим це закінчилось минулого разу.
//
// ДВА РІЗНІ ПОЛЯ ЗА ПРИРОДОЮ. Перші чотири — портфель, якого ще немає:
// вони домішуються в src і роблять «після» правильним за побудовою.
// Останні два — ПЛАН, якого ще немає: покупка, датована наступним
// березнем, не має права рухати сьогоднішні частки й готівку, зате
// мусить рухати точку незалежності й криву капіталу. Чому саме так —
// у шапці state_plan_buys.go.
type hypothetical struct {
	lots     []domain.Lot
	fundOps  []domain.FundOp
	deposits []domain.Deposit
	npfOps   []domain.NPFOp
	actions  []store.PlanAction
	flows    []store.PlanFlow
	// bonds/pays — довідник для паперів, яких у портфелі ЩЕ НЕМАЄ.
	//
	// Без них прийом мовчки недорахував би: loadSources тягне довідник
	// рівно для тих ISIN, що зустрічаються в реальних лотах, а гіпотеза
	// дописується ПІСЛЯ. Лот без свого Bond не має ні номіналу, ні
	// графіка виплат — тобто гроші з рахунку списувались, а капітал
	// просідав рівно на їхню суму, ніби папір коштує нуль. Спіймано
	// вживу: «капітал 500 000 → 498 928» на покупці за 1 071.
	//
	// Тести цього не бачили, бо всі вони купували папір, який у портфелі
	// вже був.
	bonds map[string]domain.Bond
	pays  []domain.Payment
	// settings — ПОЛІТИКА, якої ще немає: цілі, ліміти й порядок «Що
	// купити», які людина поки лише розглядає.
	//
	// Третя природа поруч із двома описаними вище, і питання вона ставить
	// своє: не «що станеться, якщо це купити», а «що названі цілі означають
	// для портфеля, який уже є». Відповідь на нього — той самий документ
	// стану, тільки з іншими налаштуваннями, тож і механізм той самий.
	//
	// Набір налаштувань був до цього константою фронтенду: пʼятнадцять
	// чисел, однакових для порожнього портфеля й для портфеля на три
	// мільйони. Порахувати їхню ціну в гривнях у браузері означало б завести
	// другу копію арифметики часток — ту саму, що вже двічі закінчилась
	// різними числами на одному екрані (state/capital.go).
	settings map[string]string
}

// empty — чи це звичайна збірка. Дешевша перевірка, ніж порівняння
// структур, і читається на місці виклику.
func (h hypothetical) empty() bool {
	return len(h.lots) == 0 && len(h.fundOps) == 0 && len(h.deposits) == 0 &&
		len(h.npfOps) == 0 && len(h.actions) == 0 && len(h.flows) == 0 &&
		len(h.settings) == 0
}

// buildState — стан портфеля яким він є.
func (s *Server) buildState(ctx context.Context, now time.Time) (*state.Doc, error) {
	return s.buildStateWith(ctx, now, hypothetical{})
}

// buildStateWith — той самий стан, але портфель можна доповнити
// покупками, яких ще не зробили.
//
// Публічний вхід лишився байт у байт тим самим свідомо: документ
// публікується в MQTT і щодня лягає в знімок, і якби гіпотезу приймав
// САМ buildState, рано чи пізно хтось опублікував би вигадку як стан.
func (s *Server) buildStateWith(ctx context.Context, now time.Time, what hypothetical) (*state.Doc, error) {
	today := domain.NewDate(now)
	// Усі читання сховища — одним місцем (state_sources.go). Доти вони
	// були розсипані по всій функції, і ListDeposits через це викликався
	// двічі за пʼятсот рядків один від одного.
	src, err := s.loadSources(ctx, today)
	if err != nil {
		return nil, err
	}
	// Гіпотетичні покупки дописуються рівно тут — до першого читання
	// src і до Holdings. Нижче за текстом жодна фаза не має знати, що
	// частина лотів іще не куплена: у цьому вся суть прийому.
	if !what.empty() {
		src.lots = append(append([]domain.Lot{}, src.lots...), what.lots...)
		src.fundOps = append(append([]domain.FundOp{}, src.fundOps...), what.fundOps...)
		src.termDeposits = append(append([]domain.Deposit{}, src.termDeposits...), what.deposits...)
		src.npfOps = append(append([]domain.NPFOp{}, src.npfOps...), what.npfOps...)
		// План — теж копією зрізу, і теж ДО того, як його прочитає
		// buildProjection. Єдиний споживач у цій функції один (той самий
		// виклик на чотириста рядків нижче), тож розійтись тут нема чому.
		src.planActions = append(append([]store.PlanAction{}, src.planActions...), what.actions...)
		src.planFlows = append(append([]store.PlanFlow{}, src.planFlows...), what.flows...)
		// Довідник — лише для ISIN, яких у ньому ще немає: інакше графік
		// виплат наявного паперу подвоївся б, а разом із ним купони,
		// драбина й дюрація.
		if len(what.bonds) > 0 {
			merged := make(map[string]domain.Bond, len(src.bonds)+len(what.bonds))
			for k, v := range src.bonds {
				merged[k] = v
			}
			pays := append([]domain.Payment{}, src.pays...)
			for isin, b := range what.bonds {
				if _, have := src.bonds[isin]; have {
					continue
				}
				merged[isin] = b
				for _, p := range what.pays {
					if p.ISIN == isin {
						pays = append(pays, p)
					}
				}
			}
			src.bonds, src.pays = merged, pays
		}
		// Політика — накладкою поверх прочитаної, і теж ТУТ: нижче за
		// текстом жодна фаза не має знати, що цілі гіпотетичні. Правити
		// документ на місці безпечно — loadSettings збирає його заново на
		// кожен запит, спільного з іншими викликами в ньому немає.
		if len(what.settings) > 0 {
			overrideSettings(src.settings, what.settings)
			// Другий переклад витрат, і він обовʼязковий: накладка могла
			// назвати іншу суму або іншу валюту, а loadSources переклав ще
			// стару. Без цього рядка превʼю політики показувало б ціль
			// резерву від витрат, яких у наборі вже немає.
			resolveExpensesUAH(src.settings, src.rates)
		}
	}
	lots, sales, bonds, pays := src.lots, src.sales, src.bonds, src.pays
	rates, deval := src.rates, src.deval
	fundOps, termDeposits := src.fundOps, src.termDeposits

	// Чим володіємо — зведене за ОДИН прохід (domain/holdings.go). Доти
	// lots обходився тут сімома циклами, а залишок після продажів
	// рахувався по чотири рази на лот, щоразу наново.
	hold := domain.NewHoldings(lots, sales, bonds, fundOps, src.fundPrices, src.payoutDays(), today)

	positions, err := domain.Positions(bonds, pays, lots, sales, today)
	if err != nil {
		return nil, err
	}
	// Розклад — календар виплат і драбина погашень (state_schedule.go).
	sch, err := buildSchedule(src, hold, today, today, scheduleFundMonths)
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
	// Зведена реальна ставка вкладів, зважена тілом. Рахується тут, у
	// єдиному циклі по вкладах, а не окремим проходом: формула та сама, що
	// в handlers_deposits.go і в реінвест-помічнику (domain.NetRate далі
	// realYield), і третій прохід над тими самими вкладами був би третім
	// місцем, де її треба тримати незміненою.
	// Номінальний двійник іде поруч і з того самого net: правило «реальна
	// головна, номінальна дрібним поруч» діє для кожного доданка зведеної,
	// інакше суміш нема з чого зібрати. Для вкладу номінальна — це ставка
	// ПІСЛЯ податку: договірна ставка до податку стоїть окремо в рядку
	// позиції й підписана словом «ставка» саме щоб їх не сплутати.
	var depRealWeighted, depNomWeighted, depRealWeight float64
	// Вклади, позначені ПОДУШКОЮ. Збираються тут, у тому самому єдиному
	// циклі, бо другий прохід над вкладами означав би друге означення того,
	// що таке «діючий вклад».
	//
	// reserveRungs — самі записи, а не суми: драбині доступу потрібні дати
	// погашення й прапорець відкличності, тобто те, чого в гривні немає.
	reserveDepositsUAH := 0.0
	reserveDepositsByCur := map[string]float64{}
	reserveDepositsUAHByCur := map[string]float64{}
	var reserveRungs []domain.Deposit
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
		// РЕЗЕРВНИЙ ВКЛАД — не вклад у сенсі складу портфеля.
		//
		// Тіло йде в подушку, а не у вклади, і наслідки правильні всі три:
		// він виходить зі знаменника видів (у резерву своя, абсолютна ціль
		// у місяцях витрат, і конкурувати з часткою ОВДП їй нема чого), не
		// рахується транзитом до наступного валютного паперу (резервний
		// долар у тій черзі не стоїть) і не стає кандидатом реінвесту.
		//
		// Валютна ЕКСПОЗИЦІЯ від цього не зникає: подушка й далі в
		// reserveUAHByCur, а $5 000 у банку — така сама валютна експозиція,
		// як доларовий папір (див. state/capital.go).
		//
		// Концентрація по банку рахується НЕЗАЛЕЖНО від прапорця: питання
		// «скільки я втрачу, якщо ця установа зникне» до подушки стоїть
		// навіть гостріше, ніж до портфеля.
		depositExposureUAH[dep.Bank] += v
		if dep.IsReserve {
			reserveDepositsUAH += v
			reserveDepositsByCur[dep.Currency] += float64(dep.BalanceAt(today)) / 100
			reserveDepositsUAHByCur[dep.Currency] += v
			reserveRungs = append(reserveRungs, dep)
			continue
		}
		depositsUAH += v
		depositsUAHByCur[dep.Currency] += v
		// Банк вкладу — такий самий контрагент, як брокер: гроші замкнені
		// саме в ньому. Ліміт концентрації рахується по обох разом, бо
		// питання «скільки я втрачу, якщо ця установа зникне» від того,
		// брокер це чи банк, не залежить. (Резервні вклади враховані вище,
		// до розгалуження, — саме тому.)
		depositBodyByCur[dep.Currency] += float64(dep.BalanceAt(today)) / 100
		// Вклад без ставки у зважування не входить узагалі: нуль там був би
		// не «нульова дохідність», а «невідома», і тягнув би середню вниз.
		if dep.RateBP > 0 {
			net := domain.NetRate(dep.RateBP, dep.TaxBP)
			depRealWeighted += realYield(net, dep.Currency, deval) * 100 * v
			depNomWeighted += net * 100 * v
			depRealWeight += v
		}
	}
	depositsYieldReal, depositsYieldNominal := 0.0, 0.0
	if depRealWeight > 0 {
		depositsYieldReal = round2(depRealWeighted / depRealWeight)
		depositsYieldNominal = round2(depNomWeighted / depRealWeight)
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
	// ГОТІВКА подушки — саме журнал, і саме ДО того, як до неї додадуться
	// резервні вклади. Це те, що можна взяти сьогодні, не ламаючи нічого, і
	// далі саме проти цього числа міряється ГОЛОВА подушки.
	reserveLiquidUAH := reserveUAH
	// Резервні вклади — друге джерело тієї самої подушки. Вони входять у її
	// суму й у валютні частки, але НЕ в готівку вище: рунга, що гаситься
	// через півроку, це подушка, якої сьогодні немає в руках.
	//
	// Місцем зберігання стає банк вкладу: питання «де це лежить» до нього
	// ставиться так само, як до сейфа, і відповідь на нього є.
	reserveUAH += reserveDepositsUAH
	for c, v := range reserveDepositsUAHByCur {
		reserveUAHByCur[c] += v
	}
	for c, v := range reserveDepositsByCur {
		reserveByCur[c] += v
	}
	for _, dep := range reserveRungs {
		place := dep.Bank
		if place == "" {
			place = "без місця"
		}
		u, cerr := fx.ToUAH(money.New(dep.BalanceAt(today), dep.Currency), rates)
		if cerr != nil {
			continue
		}
		reservePlaces[place] += float64(u.Amount()) / 100
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
	// Цілі накопичення — до buildMonth: та рахує «внесено нетто», і рухи
	// цілей входять у нього нарівні з рухами резерву (довід — у міграції
	// 0039 про дві ноги переказу).
	goals := buildGoals(src.goals, src.goalOps, rates, today, now)

	mth, err := buildMonth(src, hold, rates, now, today, reserveUAH)
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
		cost, cerr := domain.LotCost(l.Lot)
		if cerr != nil {
			return nil, cerr
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

	// Внесок у НПФ рухає гаманець в ОДИН бік: гроші йдуть із рахунку й не
	// повертаються ніколи — точніше, не раніше пенсійного віку, і тоді це
	// буде інша сутність з іншим графіком.
	//
	// arrived() тут не потрібен, як і поповненням вкладу: внесок — це
	// записаний факт із виписки, а не обіцяна виплата, яку ще треба
	// дочекатись.
	//
	// Той самий дебет ОБОВʼЯЗКОВО дзеркалиться в cashflow.go: агрегатний
	// гаманець тут і подієвий звіт там звіряються тестом
	// TestCashflowStatementReconciles, і забути одну з двох половин означає
	// розійтись на суму внесків — обидва числа при цьому лишаться
	// правдоподібними.
	npfCurByID := map[int64]string{}
	for _, a := range src.npfAccounts {
		if a.Currency == "" {
			npfCurByID[a.ID] = money.UAH
			continue
		}
		npfCurByID[a.ID] = a.Currency
	}
	for _, op := range src.npfOps {
		if op.Date.After(today) {
			continue
		}
		cur := npfCurByID[op.NPFID]
		if cur == "" {
			cur = money.UAH
		}
		cash.add(op.Broker, cur, -op.Amount)
		// Внести в пенсійний — така сама покупка, як узяти папір: гроші пішли
		// в діло, і чергу «доходу без діла» це з'їдає нарівні з рештою.
		if u, cerr := fx.ToUAH(money.New(op.Amount, cur), rates); cerr == nil {
			purchaseEvents = append(purchaseEvents, domain.CashEvent{Date: op.Date, Amount: u.Amount()})
		}
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
	fundsYield, fundsYieldReal := fnd.YieldPct, fnd.YieldRealPct

	// Пенсійні рахунки (state_npf.go). Свідомо НЕ вливаються у фондові
	// числа: fundsYield зважує рядки ринковою вартістю в одне число, і
	// замкнена двадцятип'ятирічна обіцянка поруч із виміреними дивідендами
	// REIT дала б «різні основи» в кращому разі, а в гіршому — портфельну
	// «дохідність фондів», за якою нічого не зробиш.
	npf := buildNPF(src, rates, deval, today)
	// Адміністратор — контрагент нарівні з банком, і питання до нього те
	// саме: «скільки я втрачу, якщо він завтра зникне». Це саме
	// brokerExposureUAH (звіт про концентрацію), а НЕ довідник brokers: у
	// таблиці рахунків його немає навмисно, бо з нього нічого не списати, і
	// у випадайках лотів він був би фальшивим рахунком (див. 0028).
	for _, r := range npf.Rows {
		addExposure(r.Administrator, r.ValueUAH)
	}
	// Зведена по портфелю — окремо від фондової: це третє число, а не
	// уточнення другого, і рахується воно нижче, коли вже відома
	// облігаційна частина. Парою навмисно: номінальна без реальної на
	// екрані читається як помилка.
	var blendedYield, blendedYieldReal, blendedYieldBase float64
	var blendedYieldBasis string
	var blendedYieldSplit *state.YieldSplit

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
	//
	// Позначки ціни (0034) сюди приходять самі: LastPrice бере найсвіжіше з
	// двох джерел, тож позначена ціна працює тут нарівні з ціною виписки.
	// А от каталогу цін фондів, ЯКИХ У ПОРТФЕЛІ НЕМАЄ, як не було, так і
	// немає — позначку заводять на свій фонд, а не на чужий. Тож про фонд,
	// якого ще не купували, сказати нічого не можна: 0 означає «невідомо»,
	// і ребаланс тоді просто не перевіряє здійсненність, а не вигадує поріг.
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
	realized := map[string]state.RealizedRow{}
	// Позиції НПФ зводяться РАЗ на всі три валюти: усередині циклу це була б
	// повна редукція журналу тричі, і всі три дали б те саме.
	npfPositionsForXIRR := domain.NPFPositions(src.npfAccounts, src.npfOps)
	// Ті самі потоки, тільки збережені, — щоб зведене число будувалось із
	// РІВНО того самого матеріалу, що й валютні плитки. Зібрати їх удруге
	// окремим проходом означало б завести друге означення того, що таке
	// «потоки портфеля», і воно розійшлося б із першим на першій правці.
	flowsByCur := map[string][]domain.Flow{}
	flowsBroken := false
	for _, cur := range xirrCurrencies {
		flows, err := domain.PortfolioFlows(bonds, pays, lots, sales, cur, today)
		if err != nil {
			// Для валютної плитки мовчазний пропуск нешкідливий: плитки
			// просто немає, і це видно. Для зведеного числа — ні, воно
			// вийшло б тихо неповним, тож зведене мовчить цілком.
			flowsBroken = true
			continue
		}
		flows = append(flows, domain.FundFlows(fundOps, src.fundPrices, cur, today)...)
		flows = append(flows, domain.DepositFlows(termDeposits, cur, today)...)
		// НПФ теж: внески — ті самі гроші, і XIRR міряє, скільки на них
		// зароблено. Тут це «скільки заробили МОЇ гроші» з урахуванням дат
		// внесків, тобто величина, відмінна від зростання ЧВОПА в рядку
		// позиції; при нерівномірних внесках вони розходяться, і саме тому
		// обидві показуються, а не зводяться в одну.
		//
		// Замок на це не впливає: XIRR питає, скільки принесли вкладені
		// гроші, а не чи можна їх забрати.
		for _, acc := range src.npfAccounts {
			p := npfPositionsForXIRR[acc.ID]
			if p == nil || p.Currency != cur {
				continue
			}
			flows = append(flows, domain.NPFFlows(*p, src.npfOps, today)...)
		}
		sort.Slice(flows, func(i, j int) bool { return flows[i].Date < flows[j].Date })
		// Зведене число забирає потоки ДО порогів. Одна єврова купівля не
		// привід малювати єврову плитку, але це справжні гроші, і випасти
		// зі «скільки я заробив» вони не мають.
		flowsByCur[cur] = flows
		if len(flows) < 2 {
			continue
		}
		// Результат «за фактом» рахується ДО порога і публікується завжди:
		// він не ануалізований, тож на молодих грошах не бреше — а мовчанню
		// XIRR під ним інакше нічим пояснитись.
		days := domain.MoneyWeightedDays(flows, today)
		gain, invested := domain.RealizedGain(flows)
		row := state.RealizedRow{
			Gain:      round2(float64(gain) / 100),
			MoneyDays: math.Round(days*10) / 10,
			MinDays:   xirrMinMoneyDays,
		}
		if invested > 0 {
			row.GainPct = round2(float64(gain) / float64(invested) * 100)
		}
		realized[cur] = row

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
		if days < xirrMinMoneyDays {
			continue
		}
		// навіть >30 днів нерівномірні потоки дають артефакти (сотні %);
		// реалізована дохідність портфеля ОВДП поза смугою -95%..+100%
		// — це шум ануалізації, а не сигнал, тож не публікуємо.
		if r, err := domain.XIRR(flows); err == nil && r <= 1.0 && r >= -0.95 {
			xirr[cur] = math.Round(r*10000) / 100 // частка -> %, 2 знаки
		}
	}
	totalReturn := s.totalReturn(ctx, flowsByCur, flowsBroken, today)

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
		GoalsUAH:   goals.UAH,
		NPFUAH:     npf.TotalUAH,
		BondsByCur: bnd.NominalByCurUAH, DepositsByCur: depositsUAHByCur,
		ReserveByCur: reserveUAHByCur, GoalsByCur: goals.ByCur,
		NPFByCur: npf.ExposureUAH,
	}

	// Зведена дохідність — по ЧОТИРЬОХ видах, а не по двох.
	//
	// Рахувати тут нічого не треба, і це головне: усі чотири ставки вже
	// пораховані й уже зважені кожна там, де живуть її дані — ОВДП у
	// buildBonds, фонди в buildFunds, вклади в циклі вище, НПФ у buildNPF.
	// Доти вони сходились рівно в одному місці, kindYieldReal, тобто
	// чотирма окремими числами без спільного знаменника; зведення бракувало,
	// а не арифметики.
	//
	// Основи названі тут, а не в кожній фазі, бо це твердження про ЦЮ
	// суміш: у ОВДП і вкладу ставка зафіксована наперед, у фонда й НПФ вона
	// може бути виміряною. blendYield зводить їх сам і каже «різні основи»,
	// коли доданки міряні по-різному.
	// ОВДП і вклад ідуть ОДНИМ доданком кожен і завжди в обіцяну половину:
	// YTM зафіксований до погашення, ставка вкладу — договірна. Фонди й НПФ
	// ідуть ДВОМА, бо самі бувають змішані — REIT платить дивіденди
	// (факт), а МілТех куплено девʼять днів тому (обіцянка), — і звести їх
	// в один доданок означало б втратити рівно той поділ, заради якого все
	// й робиться.
	parts := []yieldPart{
		{Pct: portfolioYield, Real: portfolioYieldReal,
			Weight: bnd.YieldWeightUAH, Basis: "до погашення"},
		{Pct: depositsYieldNominal, Real: depositsYieldReal,
			Weight: depRealWeight, Basis: "ставка вкладу після податку"},
	}
	parts = append(parts, fnd.Mix.halves(fnd.Basis)...)
	parts = append(parts, npf.Mix.halves(npf.Basis)...)
	blendedYield, blendedYieldReal, blendedYieldBase, blendedYieldBasis,
		blendedYieldSplit = blendYield(parts)

	// Розрив подушки — тим самим ReserveTarget, яким його рахує deriveReserve.
	// Стеля подушки на час боргу: те саме рішення, що в reserveMonthShare,
	// і саме тому воно одне на застосунок (state_debts.go).
	debtCaps := debtCapsReserve(src.debts, src.debtMarks, src.debtOps, src.deval, today)
	_, reserveGapUAH := state.ReserveTarget(settings, reserveUAH, debtCaps)

	// Проєкція, місячний план і віяло прогнозів (state_projection.go).
	// Вхід виписаний полем за полем навмисно: проєкція залежить від усіх
	// інструментів одразу, і серед сотні локальних змінних цього не було
	// видно — саме тому сюди роками не потрапляли то вклади, то фонди.
	prj := buildProjection(projectionInput{
		Capital: capital, Cashflow: cashflow, Settings: settings,
		CashByCur: bal, NominalByCur: nominalByCur,
		DepositBodyByCur: depositBodyByCur,
		AccumByCur:       fnd.Accum, DistByCur: fnd.Dist,
		// НПФ окремим входом, а не влитий у AccumByCur: рукав мусить
		// отримати їх обидва, але злиття двох мап — робота фабрики, і
		// зробивши її тут, я лишив би проєкцію без способу відрізнити
		// замкнене від продаваного.
		NPFAccumByCur: npf.Accum,
		YieldByCur:    portfolioYieldByCur, AvgRateByCur: src.avgRate,
		ReinvestMinByCur: reinvestMinByCur,
		Rates:            rates, Deval: deval, ActualMonthly: actualMonthly,
		IncomeMonthlyNow: incomeMonthlyNow, Today: today,
		PlanFlows: src.planFlows, PlanActions: src.planActions,
		PlanReceipts: src.planReceipts,
		// Розриви подушки й цілей — щоб прогноз віднімав від місячних
		// внесків те, що піде поза портфель, і переставав це робити, коли
		// збирати вже нічого. Обидва рахуються ТИМИ САМИМИ функціями, що
		// й у деривації: другого означення розриву в застосунку немає.
		ReserveGapUAH: reserveGapUAH,
		GoalsGapUAH:   state.GoalsGapUAH(goals.Input),
		// Борг у прогнозі. Без цього крива обіцяла б гроші, які застосунок
		// сам же віддає банку на сусідньому екрані — дослівно вада фази 20,
		// лише про борг замість цілей.
		DebtLeftUAH:      debtLeftUAH(src, rates, today),
		DebtDueUAH:       debtDueForMonth(src, rates, today, 0),
		DebtFillSharePct: debtFillSharePct(settings),
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
		NPFRows:           npf.Rows,
		BrokerExposureUAH: brokerExposureUAH, LadderUAH: ladderUAH,
	})
	rebalance, concentration := rbl.Rebalance, rbl.Concentration

	// Процентний ризик, ліквідність і НКД (state_risk.go).
	rsk := buildRisk(riskInput{
		Cashflow: cashflow, Holdings: hold, Pays: pays, TermDeposits: termDeposits,
		Rates: rates, YieldPct: portfolioYield, YieldByCur: portfolioYieldByCur,
		// ЛІКВІДНА частина подушки, а не вся: резервні вклади проходять цю
		// фазу як звичайні строкові — у «замкнено» або «зламне» за
		// прапорцем розривності. Передати сюди повну суму означало б
		// порахувати їх двічі й назвати негайно доступним те, що лежить у
		// банку до дати погашення.
		AccountMinor: accountUAHMinor, ReserveUAH: reserveLiquidUAH,
		GoalsUAH: goals.UAH,
		NPFRows:  npf.Rows,
		Now:      now, Today: today,
	})
	rateRisk, liquidity, accruedUAH := rsk.RateRisk, rsk.Liquidity, rsk.AccruedUAH

	// Що первинний ринок платить за строк (state_market.go). Єдина фаза,
	// яка дивиться назовні, а не зводить портфель.
	mkt := buildMarket(src.auctions, portfolioYieldByCur)

	// Де стоїть сьогоднішній курс серед історії (state_fxwindow.go).
	// Після ребалансу, а не поруч із ринком: валютний дефіцит рахує саме
	// ребаланс, і другого його обчислення тут бути не має.
	fxw := buildFXWindow(src.fxHistory, rates, currencyDeficitUAH(rebalance), today)

	// Документ заповнюється НАПРЯМУ, а не через проміжний літерал на
	// пʼятдесят полів: тридцять із них були дзеркалом Doc, тобто пакет
	// state здебільшого переписував із однієї структури в іншу.
	doc := &state.Doc{
		MonthInvestedUAH:  state.Major(monthInv),
		MonthDepositedUAH: state.Major(monthDep),
		MonthWithdrawnUAH: state.Major(monthOut),
		MonthTargetUAH:    state.Major(target),
		MonthPlan:         mth.Plan,
		// Чистий капітал — капітал мінус УСЕ, що винен, включно з пільговим
		// боргом картки: питання «скільки в мене насправді» не про ставки
		// (довід — при полі та в міграції 0048).
		NetWorthUAH: round2(capital.TotalUAH() - debtOwedUAH(src, rates, today)),
		// Борг — після плану місяця навмисно: стеля дострокового міряється
		// від дозволеної частини ПЛАНУ, а обовʼязкові платежі той план уже
		// зменшили (state_month.go).
		Debt: buildDebtPlan(src, src.debts, src.debtMarks, src.debtOps,
			settings, mth.Plan, rates, now, today),
		UninvestedUAH:  state.Major(unin),
		AccountUAH:     state.Major(account),
		ReinvestMinUAH: state.Major(reinvestMin),

		Accounts: accounts, Brokers: brokers, InvestedByBroker: investedByBroker,
		LadderUAH: ladderUAH, Income12m: income12m, Coupons12m: coupons12m,
		FundsUAH: round2(fundsUAH), Funds: fundRows,
		DepositsUAH: round2(depositsUAH), ReserveUAH: round2(reserveUAH),
		GoalsUAH: round2(goals.UAH),
		NPFUAH:   round2(npf.TotalUAH), NPFCostUAH: round2(npf.CostUAH),
		NPF: npf.Rows, NPFContribDue: npf.ContribDue,
		IncomeMonthlyNow: incomeMonthlyNow, ReinvestMin: reinvestMinByCur,

		Settings: settings, XIRRPct: xirr, Realized: realized,
		PortfolioYieldPct: portfolioYield, PortfolioYield: portfolioYieldByCur,
		PortfolioYieldReal: portfolioYieldRealByCur,
		FundsYieldPct:      fundsYield, FundsYieldRealPct: fundsYieldReal,
		FundsYieldBasis: fnd.Basis, FundsYieldSplit: fnd.Split,
		BlendedYieldPct: blendedYield, BlendedYieldRealPct: blendedYieldReal,
		BlendedYieldBasis: blendedYieldBasis, BlendedYieldBaseUAH: blendedYieldBase,
		BlendedYieldSplit: blendedYieldSplit,
		TotalReturn:       totalReturn,
		KindYieldPct: kindYieldReal(portfolioYield, fundsYield,
			depositsYieldNominal, npf.YieldPct),
		KindYieldRealPct: kindYieldReal(portfolioYieldReal, fundsYieldReal,
			depositsYieldReal, npf.YieldRealPct),

		Projection: projection, ProjectionRatePct: capRate, Forecast: forecast,
		PlanProvidesUAH: prj.PlanProvidesUAH,
		Sensitivity:     prj.Sensitivity, Independence: prj.Independence,
		Drawdown:  prj.Drawdown,
		Rebalance: rebalance, Concentration: concentration,
		RateRisk: rateRisk, Liquidity: liquidity,
		MarketYield: mkt.yield,
		FXWindow:    fxw.rows,
		AccruedUAH:  round2(float64(accruedUAH) / 100), NBURefreshedAt: nbuAt,
		ActualMonthlyUAH: actualMonthly, ActualMonths: actualMonths,
	}
	// Похідні — те, що виводиться з уже покладеного (state/derive.go).
	// Capital зібраний вище один раз; state його лише читає.
	if err := state.Derive(doc, state.DeriveInput{
		DebtCapsReserve: debtCaps,
		Now:             now, Positions: positions, Rates: rates, Capital: capital,
		Cashflow: cashflow, Ladder: ladder,
		MonthDeposited: monthDep, MonthTarget: target,
		ReserveByCur: reserveByCur, ReservePlaces: reservePlaces,
		ReserveLastMove: reserveLastMove, TopN: 5,
		ReserveFillMonthUAH: mth.ReserveMonthUAH, ReserveFillNowUAH: mth.ReserveFillUAH,
		ReserveMovedUAH: mth.ReserveMovedUAH,
		// Драбина доступу: готівка подушки окремо від резервних вкладів, і
		// самі вклади, зведені до чотирьох чисел. Перевід у гривню, у місяці
		// й у річний дохід робиться ТУТ — там, де є курси, «сьогодні» й
		// domain.NetRate; у state лишається сама арифметика покриття.
		ReserveLiquidUAH: reserveLiquidUAH,
		ReserveDeposits:  reserveLadderInput(reserveRungs, today, rates),
		// Цілі — так само ГОТОВИМИ: суми в обох одиницях і поміряний темп.
		// Курс, «сьогодні» й вікно темпу знає будівник (state_goals.go), а в
		// state лишається «скільки лишилось і чи встигаю».
		Goals: goals.Input,
	}); err != nil {
		return nil, err
	}

	// Гроші місяця по видах — ОСТАННІМ кроком, і не з примхи.
	//
	// «На вирівнювання» ділиться МІЖ видами, тобто не рахується, доки не
	// відомі потреби всіх, — тому це окремий прохід, а не цикл усередині
	// ребалансу. А стоїть він саме ПІСЛЯ Derive, бо стелю цілей
	// накопичення виставляє GoalsFill усередині Derive: до нього
	// FillMonthUAH порожній, а порахувати стелю в самому ребалансі
	// означало б завести ДРУГЕ її означення — рівно та пастка, проти якої
	// написана шапка GoalsFill.
	//
	// doc.Rebalance — той самий зріз, що й rebalance: правка на місці
	// доходить у документ, другої копії тут немає.
	//
	// БАЗА — ГРОШІ ПІСЛЯ ПОДУШКИ Й ПІСЛЯ ЦІЛЕЙ, тим самим порядком, яким
	// їх ріже розкладка надходження (handlers_allocate.go) і маршрут:
	// подушка → цілі → види. Доти цілі з бази не віднімались, і карта
	// «Скільки чого за стратегією» обіцяла на вирізку цілей більше, ніж
	// показувала модалка розкладки, яку сама ж і відкриває.
	if mth.Plan != nil {
		avail := mth.Plan.PlanUAH - mth.ReserveMonthUAH - goalsMonthUAH(doc.Goals)
		spreadMonth(doc.Rebalance, avail, rbl.KindMajorUAH)
	}
	return doc, nil
}

// goalsMonthUAH — скільки з грошей місяця належить цілям накопичення разом.
//
// Стеля, а не потреба: FillMonthUAH означає «скільки цей місяць РЕАЛЬНО дає
// цілі, разом із уже покладеним», і в подушки ReserveMonthUAH означає рівно
// те саме. Складати тут потрібний темп (RequiredUAH) означало б відняти від
// грошей місяця більше, ніж застосунок насправді відріже.
//
// Закриті цілі не рахуються: GoalsFill їм стелі й не дає.
func goalsMonthUAH(goals []state.Goal) float64 {
	var sum float64
	for _, g := range goals {
		if g.DoneDate != "" {
			continue
		}
		sum += g.FillMonthUAH
	}
	return sum
}
