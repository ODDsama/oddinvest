// Збирання документа стану — головна операція сервісу.
//
// Читання сховища — у state_sources.go, зведення лотів і фондів — у
// domain/holdings.go, потоки на межі інструмента — у state_flows.go (їх
// же читають externalMoves і CashEvents, тож журнал один на всіх).
// Гаманця (state_cash.go) немає з ревізії 2026-10-03: рахунків застосунок
// не веде.

package engine

import (
	"cmp"
	"context"
	"math"
	"sort"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/settings"
	"github.com/ODDsama/oddinvest/internal/state"

	money "github.com/Rhymond/go-money"
)

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
// XIRR. Іменем тут, бо їде в документ стану (RealizedRow.MinDays) і звідти
// в пояснення на екрані; саме число одне на застосунок — domain.XIRRMinMoneyDays.
const xirrMinMoneyDays = domain.XIRRMinMoneyDays

// Hypothetical — світ або політика, яких ЩЕ НЕМАЄ. Порожня структура
// означає звичайний стан, і саме тому BuildState нижче лишається
// однорядковою обгорткою: жоден із його викликів не знає, що така
// можливість є.
//
// Природ дві, і питання в кожної своє:
//
//   - settings — ПОЛІТИКА, якої ще немає: цілі, ліміти й порядок «Що
//     взяти», які людина поки лише розглядає (превʼю набору налаштувань).
//     Відповідь — той самий документ стану з іншими налаштуваннями: друга
//     копія арифметики часток у браузері вже двічі закінчувалась різними
//     числами на одному екрані (state/capital.go);
//   - rates — КУРСИ, яких сьогодні немає: «що з ЦИМ портфелем зробив би
//     рух курсу, який УЖЕ був» (валютний шок). Домішується РАНІШЕ за
//     накладку політики: витрати можуть бути задані у валюті (0038), і
//     перекласти їх треба вже за новим курсом. Знецінення (src.deval) при
//     цьому НЕ рухається: шок — разовий рух РІВНЯ, а не зміна річного
//     темпу (довід — шапка devaluation.go).
//
// Гіпотетичних ПОКУПОК тут більше немає. Вони жили заради плану купівель
// і кошика «що станеться, якщо купити» (/api/whatif, «Чим добрати»), а
// разом із ними — шість полів, планована купівля фонду й KeepPrice.
// Власник купує сам і лише записує покупку (ревізія 2026-10-03), тож
// план купівель прибрано цілком.
type Hypothetical struct {
	settings map[string]string
	rates    fx.Rates
}

// HypoRates — гіпотеза «інші курси» (валютний шок, api/handlers_fx_shock.go);
// HypoSettings — «інша політика» (превʼю налаштувань). Конструкторами, а
// не літералом: поля гіпотези закриті для обробників, і кожен із них
// бачить рівно ту одну підміну, яку просить.
func HypoRates(r fx.Rates) Hypothetical { return Hypothetical{rates: r} }

func HypoSettings(set map[string]string) Hypothetical { return Hypothetical{settings: set} }

// empty — чи це звичайна збірка. НОВЕ ПОЛЕ ГІПОТЕЗИ МУСИТЬ ЗʼЯВИТИСЬ І
// ТУТ: інакше BuildStateWith мовчки пропустить блок домішування.
func (h Hypothetical) empty() bool {
	return len(h.settings) == 0 && len(h.rates) == 0
}

// BuildState — стан портфеля яким він є.
func (e *Engine) BuildState(ctx context.Context, now time.Time) (*state.Doc, error) {
	return e.BuildStateWith(ctx, now, Hypothetical{})
}

// BuildStateWith — той самий стан, але з іншими курсами чи політикою.
//
// Публічний вхід лишився байт у байт тим самим свідомо: документ
// публікується в MQTT і щодня лягає в знімок, і якби гіпотезу приймав
// САМ BuildState, рано чи пізно хтось опублікував би вигадку як стан.
func (e *Engine) BuildStateWith(ctx context.Context, now time.Time, what Hypothetical) (*state.Doc, error) {
	today := domain.NewDate(now)
	// Усі читання сховища — одним місцем (state_sources.go).
	src, err := e.loadSources(ctx, today)
	if err != nil {
		return nil, err
	}
	// Гіпотеза домішується рівно тут — до першого читання src. Нижче за
	// текстом жодна фаза не має знати, що курси чи політика вигадані.
	if !what.empty() {
		// Курси — накладкою поверх прочитаних, і ДО політики нижче:
		// витрати можуть бути названі у валюті, і переклад їх у гривню
		// мусить статись уже за новим курсом.
		//
		// Копією мапи, а не правкою на місці: s.Rates() віддає свіжу мапу
		// на кожен запит, але вона їде далі в кожну фазу документа, і
		// правити спільну структуру, коли поруч є дешева копія, — це
		// зайвий спосіб помилитись.
		if len(what.rates) > 0 {
			r := make(fx.Rates, len(src.rates)+len(what.rates))
			for k, v := range src.rates {
				r[k] = v
			}
			for k, v := range what.rates {
				r[k] = v
			}
			src.rates = r
			// Другий переклад витрат — обовʼязковий, і рівно з того самого
			// доводу, що в накладці політики нижче: без нього достатність
			// подушки мовчки міряється за старим курсом.
			settings.ResolveExpensesUAH(src.settings, src.rates)
		}
		// Політика — накладкою поверх прочитаної, і теж ТУТ: нижче за
		// текстом жодна фаза не має знати, що цілі гіпотетичні. Правити
		// документ на місці безпечно — settings.Load збирає його заново на
		// кожен запит, спільного з іншими викликами в ньому немає.
		if len(what.settings) > 0 {
			settings.Override(src.settings, what.settings)
			// Другий переклад витрат, і він обовʼязковий: накладка могла
			// назвати іншу суму або іншу валюту, а loadSources переклав ще
			// стару. Без цього рядка превʼю політики показувало б ціль
			// резерву від витрат, яких у наборі вже немає.
			settings.ResolveExpensesUAH(src.settings, src.rates)
		}
	}
	lots, sales, bonds, pays := src.lots, src.sales, src.bonds, src.pays
	rates, deval := src.rates, src.deval
	fundOps, termDeposits := src.fundOps, src.termDeposits

	// Чим володіємо — зведене за ОДИН прохід (domain/holdings.go). Доти
	// lots обходився тут сімома циклами, а залишок після продажів
	// рахувався по чотири рази на лот, щоразу наново.
	// Чому дата сама по собі не відповідь і навіщо тут кнопка «Отримано» —
	// у domain.Arrived. Предикат один на застосунок навмисно: доти його
	// було три, і два перевіряли різне. Він же вирішує, чи папір або вклад
	// УЖЕ погашений: гроші не можуть бути одночасно й на рахунку, і в
	// позиції.
	arrived := domain.Arrived(src.statuses, today)
	// Гроші погашених вкладів подушки й цілей — у пулі свого призначення, а
	// не на рахунку банку (earmark_pool.go).
	pools := buildEarmarkPools(termDeposits, arrived, today)
	hold := domain.NewHoldings(lots, sales, bonds, fundOps, src.fundPrices, src.payoutDays(), today, arrived)

	positions, err := domain.Positions(bonds, pays, lots, sales, today, arrived)
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
	depositsUAHByCur := map[string]state.Money{}
	depositExposureUAH := map[string]float64{} // банк → тіло, грн-екв.
	// Тіло вкладів у НАТИВНІЙ валюті — для рукавів проєкції: вони рахують
	// у своїй валюті, а не в грн-еквіваленті.
	depositBodyByCur := map[string]float64{}
	depositsAccruedUAH := 0.0
	// Зведена реальна ставка вкладів, зважена тілом. Рахується тут, у
	// єдиному циклі по вкладах, а не окремим проходом: формула та сама, що
	// в api/handlers_deposits.go і в реінвест-помічнику (domain.NetRate далі
	// RealYield), і третій прохід над тими самими вкладами був би третім
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
	// Вклади, позначені ЦІЛЛЮ (0062) — той самий прохід і той самий довід.
	// Записи, а не суми, і за id цілі: потрібний темп кожної цілі мусить
	// знати ставку саме СВОЇХ грошей, а не середню по всіх.
	//
	// Сум тут навмисно немає. Гроші цілей зводить buildGoals — одне місце
	// на журнал і на вклади, — інакше капітал порахував би цільовий вклад
	// двічі, і помітно це стало б аж на інваріанті зведення.
	goalDepositsByGoal := map[int64][]domain.Deposit{}
	for _, dep := range termDeposits {
		// Погашене тіло, позначене «Отримано» в сам день погашення, уже
		// лежить на рахунку (цикл гаманця нижче) — у складі вкладів його
		// бути не може, інакше до півночі воно рахувалось би двічі.
		if !dep.Active(today) || arrived(dep.SyntheticISIN(), dep.MaturityDate) {
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
		// Цільовий вклад (0062) — дзеркало резервного, і виходить із
		// портфельного контуру рівно так само: гроші, обіцяні авто, не є
		// купівельною спроможністю, і частка ОВДП не має ними міритись.
		//
		// РІЗНИЦЯ З РЕЗЕРВОМ ОДНА, і вона тут важлива: у резерву ставка
		// нікого не цікавила (подушку тримають не заради неї), а в цілі
		// саме ставка й є те, заради чого вклад заводять — інакше ціль
		// гарантовано програє інфляції. Тому ставка збирається окремо,
		// по кожній цілі, і її забирає deriveGoals: потрібний темп мусить
		// знати, під скільки працюють уже відкладені гроші.
		if dep.GoalID != 0 {
			goalDepositsByGoal[dep.GoalID] = append(goalDepositsByGoal[dep.GoalID], dep)
			continue
		}
		depositsUAH += v
		depositsUAHByCur[dep.Currency] = depositsUAHByCur[dep.Currency].Add(state.Major(v, dep.Currency))
		// Нараховані й ще не виплачені відсотки — у капіталі, як накопичений
		// купон облігацій (рішення власника). Вклад із виплатою в кінці
		// інакше до погашення «не заробляв» нічого, а в день погашення
		// стрибав. Подушки й цілі це не стосується — вони вище, лише тілом.
		if acc := dep.AccruedNet(today); acc > 0 {
			if u, aerr := fx.ToUAH(money.New(acc, dep.Currency), rates); aerr == nil {
				av := float64(u.Amount()) / 100
				depositsUAH += av
				depositsAccruedUAH += av
				depositsUAHByCur[dep.Currency] = depositsUAHByCur[dep.Currency].Add(state.Major(av, dep.Currency))
			}
		}
		// Банк вкладу — такий самий контрагент, як брокер: гроші замкнені
		// саме в ньому. Ліміт концентрації рахується по обох разом, бо
		// питання «скільки я втрачу, якщо ця установа зникне» від того,
		// брокер це чи банк, не залежить. (Резервні вклади враховані вище,
		// до розгалуження, — саме тому.)
		depositBodyByCur[dep.Currency] += float64(dep.BalanceAt(today)) / 100
		// Вклад без ставки у зважування не входить узагалі: нуль там був би
		// не «нульова дохідність», а «невідома», і тягнув би середню вниз.
		if dep.RateBP > 0 {
			// ЕФЕКТИВНА: цей доданок стоїть в одному зваженому середньому
			// з YTM облігацій, а YTM — IRR. Довід цілком — при
			// domain.Deposit.EffectiveNetRate.
			net := dep.EffectiveNetRate()
			depRealWeighted += RealYield(net, dep.Currency, deval) * 100 * v
			depNomWeighted += net * 100 * v
			depRealWeight += v
		}
	}
	depositsYieldReal, depositsYieldNominal := 0.0, 0.0
	if depRealWeight > 0 {
		depositsYieldReal = domain.Round2(depRealWeighted / depRealWeight)
		depositsYieldNominal = domain.Round2(depNomWeighted / depRealWeight)
	}

	// Резерв («матрац») — журнал рухів, поточний залишок це Σ сум. Читаємо
	// весь журнал, а не агрегат: потрібні ще й місця зберігання та дата
	// останнього руху, а рухів тут одиниці.
	//
	// Резерв — не купівельна спроможність: помічник не має пропонувати
	// купити папір за аварійні гроші.
	reserveOps := src.reserveOps
	reserveUAH := 0.0
	reserveByCur := map[string]state.Money{}
	reserveUAHByCur := map[string]state.Money{}
	reservePlaces := map[string]state.Money{}
	reserveLastMove := ""
	for _, op := range reserveOps {
		u, cerr := fx.ToUAH(money.New(op.Amount, op.Currency), rates)
		if cerr != nil {
			continue
		}
		v := float64(u.Amount()) / 100
		reserveUAH += v
		reserveUAHByCur[op.Currency] = reserveUAHByCur[op.Currency].Add(state.Major(v, op.Currency))
		reserveByCur[op.Currency] = reserveByCur[op.Currency].Add(state.Minor(op.Amount, op.Currency))
		place := cmp.Or(op.Place, "без місця")
		reservePlaces[place] = reservePlaces[place].Add(state.Major(v, money.UAH))
		if string(op.Date) > reserveLastMove {
			reserveLastMove = string(op.Date)
		}
	}
	// ГОТІВКА подушки — саме журнал, і саме ДО того, як до неї додадуться
	// резервні вклади. Це те, що можна взяти сьогодні, не ламаючи нічого, і
	// далі саме проти цього числа міряється ГОЛОВА подушки.
	// Пул подушки — теж готівка подушки: гроші погашеного резервного
	// вкладу, які ще не перевкладено. Доступні сьогодні, як і журнал.
	for k, v := range pools.left {
		if k.goal != 0 || v <= 0 {
			continue
		}
		u, cerr := fx.ToUAH(money.New(v, k.cur), rates)
		if cerr != nil {
			continue
		}
		uv := float64(u.Amount()) / 100
		reserveUAH += uv
		reserveUAHByCur[k.cur] = reserveUAHByCur[k.cur].Add(state.Major(uv, k.cur))
		reserveByCur[k.cur] = reserveByCur[k.cur].Add(state.Minor(v, k.cur))
		reservePlaces[k.bank] = reservePlaces[k.bank].Add(state.Major(uv, money.UAH))
	}
	reserveLiquidUAH := reserveUAH
	// Резервні вклади — друге джерело тієї самої подушки. Вони входять у її
	// суму й у валютні частки, але НЕ в готівку вище: рунга, що гаситься
	// через півроку, це подушка, якої сьогодні немає в руках.
	//
	// Місцем зберігання стає банк вкладу: питання «де це лежить» до нього
	// ставиться так само, як до сейфа, і відповідь на нього є.
	reserveUAH += reserveDepositsUAH
	for c, v := range reserveDepositsUAHByCur {
		reserveUAHByCur[c] = reserveUAHByCur[c].Add(state.Major(v, c))
	}
	for c, v := range reserveDepositsByCur {
		reserveByCur[c] = reserveByCur[c].Add(state.Major(v, c))
	}
	for _, dep := range reserveRungs {
		place := cmp.Or(dep.Bank, "без місця")
		u, cerr := fx.ToUAH(money.New(dep.BalanceAt(today), dep.Currency), rates)
		if cerr != nil {
			continue
		}
		reservePlaces[place] = reservePlaces[place].Add(state.Of(u))
	}
	// Місця й валюти, що вийшли в нуль (усе забрали), прибираємо: рядок
	// «сейф — 0 ₴» описує не стан, а історію, і в картці лише заважає.
	for k, v := range reservePlaces {
		if math.Abs(v.Major()) < 0.005 {
			delete(reservePlaces, k)
		}
	}
	for k, v := range reserveByCur {
		if math.Abs(v.Major()) < 0.005 {
			delete(reserveByCur, k)
			delete(reserveUAHByCur, k)
		}
	}

	// Рухи поточного місяця й фактичний темп (state_month.go).
	// Цілі накопичення — до buildMonth: та рахує «внесено нетто», і рухи
	// цілей входять у нього нарівні з рухами резерву (довід — у міграції
	// 0039 про дві ноги переказу).
	goals := buildGoals(src.goals, src.goalOps, goalDepositsByGoal, pools, rates, today, now)

	// Рух на межі інструмента — один журнал на документ (state_flows.go).
	// З нього «внесено» місяця й темп (buildMonth) і дохід без діла нижче.
	flows, err := instrumentFlows(flowInputs{
		lots: lots, sales: sales, pays: pays, arrived: arrived, fundOps: fundOps,
		npfAccounts: src.npfAccounts, npfOps: src.npfOps, deposits: termDeposits,
		pools: pools, today: today,
	})
	if err != nil {
		return nil, err
	}
	mth, err := buildMonth(src, hold, flows, rates, now, today, reserveUAH)
	if err != nil {
		return nil, err
	}
	monthInv := mth.InvestedUAH
	monthDep := mth.DepositedUAH
	actualMonthly, actualMonths := mth.ActualMonthlyUAH, mth.ActualMonths

	// Скільки грошей стоїть за КОЖНИМ контрагентом, грн-екв. — для ліміту
	// концентрації. Це не investedByBroker нижче: там собівартість, бо
	// картка «Вкладено по брокерах» відповідає на «скільки я туди заніс».
	// Тут питання інше — «скільки я втрачу, якщо цей брокер чи банк завтра
	// зникне», а на нього відповідає сьогоднішня вартість: номінал
	// паперів, ринкова вартість сертифікатів і тіло вкладу.
	//
	// Резерву тут немає: у нього не брокер, а «місце» (готівка, сейф), і
	// ризик контрагента до нього не застосовний — у цьому й сенс матраца.
	brokerExposureUAH := map[string]float64{}
	addExposure := func(name string, uah float64) {
		name = cmp.Or(name, "—")
		brokerExposureUAH[name] += uah
	}

	// Вкладено по брокерах (грн-екв.): дзеркалить логіку Positions —
	// ціна×залишок + пропорційна комісія, лише згруповано по брокеру.
	investedByBroker := map[string]state.Money{}
	for _, l := range hold.Lots {
		rem := l.Remaining
		if rem == 0 {
			continue
		}
		cost := l.PricePerBond.Multiply(rem)
		if fee, ferr := domain.Apportion(l.Fee, rem, l.Qty); ferr == nil && !fee.IsZero() {
			if c2, aerr := cost.Add(fee); aerr == nil {
				cost = c2
			}
		}
		u, uerr := fx.ToUAH(cost, rates)
		if uerr != nil {
			continue
		}
		name := cmp.Or(l.Channel, "—")
		investedByBroker[name] = investedByBroker[name].Add(state.Of(u))
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
			b := cmp.Or(op.Broker, "—")
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
					investedByBroker[b] = investedByBroker[b].Add(state.Of(u))
				}
				if u, uerr := fx.ToUAH(money.New(mvMinor*v/totalBought, pos.Currency), rates); uerr == nil {
					addExposure(b, float64(u.Amount())/100)
				}
			}
		}
	}

	// Решта експозиції контрагентів: папери за номіналом і тіла вкладів
	// (сертифікати додались вище, разом зі своїм поділом по брокерах).
	for _, l := range hold.Lots {
		if !l.Held() {
			continue
		}
		nom := l.Bond.Nominal
		if u, err := fx.ToUAH(money.New(nom.Amount()*l.Remaining, nom.Currency().Code), rates); err == nil {
			addExposure(l.Channel, float64(u.Amount())/100)
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
		addExposure(r.Administrator, r.ValueUAH.Major())
	}
	// Зведена по портфелю — окремо від фондової: це третє число, а не
	// уточнення другого, і рахується воно нижче, коли вже відома
	// облігаційна частина. Парою навмисно: номінальна без реальної на
	// екрані читається як помилка.
	var blendedYield, blendedYieldReal, blendedYieldBase float64
	var blendedYieldBasis string
	var blendedYieldSplit *state.YieldSplit

	// Дохід, що не працює: виплати, за якими ще не було покупки. Купівлі
	// з'їдають його за чергою (найстаріше першим, domain.IdleIncome).
	//
	// Стелі «скільки лежить на рахунках» більше немає — рахунків застосунок
	// не веде (ревізія 2026-10-03). Число тепер означає «дохід чекає»: купон
	// прийшов, а нової покупки після нього ще не було.
	var incomeEvents, purchaseEvents []domain.CashEvent
	for _, f := range flows {
		switch {
		case f.Kind == flowIncome:
			if u, cerr := fx.ToUAH(money.New(f.Amount, f.Currency), rates); cerr == nil {
				incomeEvents = append(incomeEvents, domain.CashEvent{Date: f.Date, Amount: u.Amount()})
			}
		case f.Kind == flowPurchase && f.New > 0:
			if u, cerr := fx.ToUAH(money.New(f.New, f.Currency), rates); cerr == nil {
				purchaseEvents = append(purchaseEvents, domain.CashEvent{Date: f.Date, Amount: u.Amount()})
			}
		}
	}
	unin := money.New(max(domain.IdleIncome(incomeEvents, purchaseEvents), 0), money.UAH)

	// найдешевший папір по валютах (нативно) + мінімум у грн-екв.
	minNoms := src.minNominal
	// Мінімальний вклад по валютах — для здійсненності ребалансу.
	depMinByCur := src.depositMin
	// Поріг реінвесту в прогнозі (Sleeve.Threshold): найдешевший квиток у
	// валюті — папір або мінімальний вклад, що дешевше. Симуляція тримає
	// купони готівкою, доки не назбирається на квиток; від рахунків це не
	// залежить, тож поріг пережив їхнє прибирання. Назовні (у документ) він
	// більше не їде — reinvest_min жив заради кнопки «вистачає на N паперів».
	reinvestMinByCur := map[string]state.Money{}
	for cur, minNom := range minNoms {
		reinvestMinByCur[cur] = state.Minor(minNom, cur)
	}
	for cur, depMin := range depMinByCur {
		if have, ok := reinvestMinByCur[cur]; !ok || depMin < have.Minor() {
			reinvestMinByCur[cur] = state.Minor(depMin, cur)
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
		cur := cmp.Or(row.Currency, money.UAH)
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
	// інструмента: питання «скільки коштує зайти в цей вид».
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

	// Налаштування — одним проходом по реєстру (internal/settings).
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
	// Рядка «channels» (брокери через кому) у зведенні більше немає: його
	// не читали ні UI, ні HA — випадайки беруть брокерів із довідника.

	// Фонди входять у XIRR нарівні з облігаціями: показник міряє, скільки
	// реально зароблено на вкладених грошах, а гроші в сертифікатах — ті
	// самі гроші. Без цього він рахував облігаційну частину й видавав її
	// за портфельну. fundOps уже стягнуто раз на початку BuildState.
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
			Gain:      state.Minor(gain, cur),
			MoneyDays: math.Round(days*10) / 10,
			MinDays:   xirrMinMoneyDays,
		}
		if invested > 0 {
			row.GainPct = domain.Round2(float64(gain) / float64(invested) * 100)
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
		if r, err := domain.XIRR(flows); err == nil && domain.XIRRPlausible(r) {
			xirr[cur] = math.Round(r*10000) / 100 // частка -> %, 2 знаки
		}
	}
	totalReturn := e.totalReturn(ctx, flowsByCur, flowsBroken, today, src.report)

	// Облігації: номінал і дохідність до погашення (state_bonds.go).
	bnd := buildBonds(hold, pays, rates, deval, today)
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

	// Капітал — один раз і на всіх, і саме тут: це ТОЧКА ЗБІРКИ п'яти
	// інструментів, а не частина котрогось із них. Далі його читають
	// ребаланс, старт проєкції й сам документ; доти кожен з них складав
	// свою суму, і на сусідніх картках стояли числа, які не сходились.
	// Облігації в капіталі — «номінал + накопичений купон» (рішення власника
	// 2026-09-22): так само, як їх оцінює XIRR, і без стрибка капіталу в
	// день купона — гроші, зароблені за пів року, не зʼявляються з нізвідки
	// одного ранку. Номінал окремо лишається в nominal_uah_eq.
	acc := accruedOf(hold, pays, today, rates)
	bondsByCur := make(map[string]state.Money, len(bnd.NominalByCurUAH))
	for cur, m := range bnd.NominalByCurUAH {
		bondsByCur[cur] = state.Minor(m.Minor()+acc.ByCur[cur], money.UAH)
	}
	for cur, minor := range acc.ByCur {
		if _, ok := bondsByCur[cur]; !ok {
			bondsByCur[cur] = state.Minor(minor, money.UAH)
		}
	}
	capital := state.Capital{
		BondsUAH:        state.Minor(bnd.NominalUAH+acc.TotalMinor, money.UAH),
		BondsAccruedUAH: state.Minor(acc.TotalMinor, money.UAH),
		// Накопичені відсотки вкладів — у DepositsUAH; окремо для проєкції.
		DepositsAccruedUAH: state.Major(depositsAccruedUAH, money.UAH),
		FundsUAH:           state.Major(fundsUAH, money.UAH), DepositsUAH: state.Major(depositsUAH, money.UAH), ReserveUAH: state.Major(reserveUAH, money.UAH),
		GoalsUAH:   state.Major(goals.UAH, money.UAH),
		NPFUAH:     state.Major(npf.TotalUAH, money.UAH),
		BondsByCur: bondsByCur, DepositsByCur: depositsUAHByCur,
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
	// Рубіж покриття боргу — підлога тієї самої цілі. Рахується тут разом зі
	// стелею, бо обидва числа читає і картка подушки, і розкладка, і другий
	// їхній екземпляр розійшовся б із першим (той самий довід, що при debtCaps).
	// Рубіж на картці — усі борги; підлога цілі — лише ті, що не гасяться
	// вигідно достроково (довід при debtCoverUAH).
	debtCover := debtCoverUAH(src.debts, src.debtMarks, src.debtOps, rates, today, false)
	debtFloor := debtCoverUAH(src.debts, src.debtMarks, src.debtOps, rates, today, true)
	// Позики в самого себе: ціль піднята на нарахований відсоток, тож
	// розрив мусить рахуватись тим самим числом, що й картка.
	resLoans := reserveLoans(src.reserveLoans, src.reserveOps, today, rates)
	_, reserveGapUAH := state.ReserveTarget(settings, reserveUAH, debtCaps, debtCover,
		reserveOwedInterestUAH(resLoans))

	// Проєкція, місячний план і віяло прогнозів (state_projection.go).
	// Вхід виписаний полем за полем навмисно: проєкція залежить від усіх
	// інструментів одразу, і серед сотні локальних змінних цього не було
	// видно — саме тому сюди роками не потрапляли то вклади, то фонди.
	_, cardDue0 := debtDueParts(src, rates, today, 0)
	prj := buildProjection(projectionInput{
		Capital: capital, Cashflow: withoutEarmarked(cashflow, termDeposits), Settings: settings,
		NominalByCur:     nominalByCur,
		DepositBodyByCur: depositBodyByCur,
		AccumByCur:       fnd.Accum, DistByCur: fnd.Dist,
		// НПФ окремим входом, а не влитий у AccumByCur: рукав мусить
		// отримати їх обидва, але злиття двох мап — робота фабрики, і
		// зробивши її тут, я лишив би проєкцію без способу відрізнити
		// замкнене від продаваного.
		NPFAccumByCur:   npf.Accum,
		MarketRateByCur: auctionRateByCur(src.auctions, today),
		YieldByCur:      portfolioYieldByCur, AvgRateByCur: src.avgRate,
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
		InstallmentDueByMonth: installmentDueByMonth(src, rates, today),
		CardDueUAH:            cardDue0,
		CardLeftUAH:           cardLeftUAH(src, rates, today),
		PlannedByMonth:        plannedByMonth(src, rates, today),
	})
	// target — місячний план. Не читається з налаштувань: виводиться з
	// цілі й дедлайну (див. state_projection.go).
	target := prj.TargetUAH
	projection, forecast, capRate, capRateReal := prj.Rows, prj.Forecast, prj.CapRatePct, prj.CapRateRealPct
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
		MinNominalByCur: minNoms, DepositMinByCur: depMinByCur,
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
		ReserveUAH: reserveLiquidUAH,
		GoalsUAH:   goals.UAH,
		NPFRows:    npf.Rows,
		Now:        now, Today: today,
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
		MonthInvestedUAH:    state.Of(monthInv),
		MonthDepositedUAH:   state.Of(monthDep),
		MonthOutsideUAH:     state.Of(mth.OutsideUAH),
		MonthContributedUAH: state.Of(mth.ContributedUAH),
		MonthTargetUAH:      state.Of(target),
		MonthPlan:           mth.Plan,
		// Чистий капітал — капітал мінус УСЕ, що винен, включно з пільговим
		// боргом картки: питання «скільки в мене насправді» не про ставки
		// (довід — при полі та в міграції 0048).
		NetWorthUAH: state.Major(capital.TotalUAH()-debtOwedUAH(src, rates, today), money.UAH),
		// Дельта за 30 днів — проти знімка з sources; nil, доки знімка
		// місячної давнини немає (state_delta.go).
		CapitalDelta30: buildCapitalDelta(src, capital.TotalUAH(),
			capital.BondsAccruedUAH.Major()+capital.DepositsAccruedUAH.Major(), rates, today),
		// Борг — після плану місяця навмисно: стеля дострокового міряється
		// від дозволеної частини ПЛАНУ, а обовʼязкові платежі той план уже
		// зменшили (state_month.go).
		Debt: buildDebtPlan(src, src.debts, src.debtMarks, src.debtOps,
			settings, mth.Plan, rates, now, today),
		UninvestedUAH: state.Of(unin),

		InvestedByBroker: investedByBroker,
		LadderUAH:        ladderUAH, Income12m: income12m, Coupons12m: coupons12m,
		FundsUAH: state.Major(fundsUAH, money.UAH), Funds: fundRows,
		DepositsUAH: state.Major(depositsUAH, money.UAH), ReserveUAH: state.Major(reserveUAH, money.UAH),
		GoalsUAH: state.Major(goals.UAH, money.UAH),
		NPFUAH:   state.Major(npf.TotalUAH, money.UAH), NPFCostUAH: state.Major(npf.CostUAH, money.UAH),
		NPF: npf.Rows, NPFContribDue: npf.ContribDue,
		IncomeMonthlyNow: state.Major(incomeMonthlyNow, money.UAH),

		Settings: settings, XIRRPct: xirr, Realized: realized,
		PortfolioYieldPct: portfolioYield, PortfolioYield: portfolioYieldByCur,
		PortfolioYieldRealPct: portfolioYieldReal, PortfolioYieldReal: portfolioYieldRealByCur,
		FundsYieldPct: fundsYield, FundsYieldRealPct: fundsYieldReal,
		FundsYieldBasis: fnd.Basis, FundsYieldSplit: fnd.Split,
		BlendedYieldPct: blendedYield, BlendedYieldRealPct: blendedYieldReal,
		BlendedYieldBasis: blendedYieldBasis, BlendedYieldBaseUAH: state.Major(blendedYieldBase, money.UAH),
		BlendedYieldSplit: blendedYieldSplit,
		TotalReturn:       totalReturn,
		KindYieldPct: kindYieldReal(portfolioYield, fundsYield,
			depositsYieldNominal, npf.YieldPct),
		KindYieldRealPct: kindYieldReal(portfolioYieldReal, fundsYieldReal,
			depositsYieldReal, npf.YieldRealPct),

		Projection: projection, ProjectionRatePct: capRate, ProjectionRateRealPct: capRateReal, Forecast: forecast,
		PlanProvidesUAH: state.Major(prj.PlanProvidesUAH, money.UAH),
		Sensitivity:     prj.Sensitivity, Independence: prj.Independence,
		Drawdown:  prj.Drawdown,
		Rebalance: rebalance, Concentration: concentration,
		RateRisk: rateRisk, Liquidity: liquidity,
		MarketYield: mkt.yield,
		FXWindow:    fxw.rows,
		AccruedUAH:  state.Minor(accruedUAH, money.UAH), NBURefreshedAt: nbuAt,
		DepositsAccruedUAH: capital.DepositsAccruedUAH,
		ActualMonthlyUAH:   state.Major(actualMonthly, money.UAH), ActualMonths: actualMonths,
		SavingsRatePct: savingsRatePct(actualMonthly, mth.GrossAvgUAH),
		SavingsBaseUAH: savingsBase(actualMonthly, mth.GrossAvgUAH),
	}
	// Похідні — те, що виводиться з уже покладеного (state/derive.go).
	// Capital зібраний вище один раз; state його лише читає.
	if err := state.Derive(doc, state.DeriveInput{
		DebtCapsReserve: debtCaps, DebtCoverUAH: state.Major(debtCover, money.UAH),
		DebtFloorUAH: state.Major(debtFloor, money.UAH),
		Now:          now, Positions: positions, Rates: rates, Capital: capital,
		Cashflow: cashflow, Ladder: ladder,
		MonthDeposited: monthDep, MonthTarget: target,
		ReserveByCur: reserveByCur, ReservePlaces: reservePlaces,
		ReserveLastMove: reserveLastMove, TopN: 5,
		ReserveFillMonthUAH: state.Major(mth.ReserveMonthUAH, money.UAH), ReserveFillNowUAH: state.Major(mth.ReserveFillUAH, money.UAH),
		ReserveMovedUAH: state.Major(mth.ReserveMovedUAH, money.UAH),
		// Інфляція — щоб ціль, задана в сьогоднішніх грошах, знала, у що
		// вона обійдеться в рік дедлайну. Нуль = ряду ще немає, і тоді
		// майбутні числа просто не малюються.
		InflationPct: src.cpi,
		// Драбина доступу: готівка подушки окремо від резервних вкладів, і
		// самі вклади, зведені до чотирьох чисел. Перевід у гривню, у місяці
		// й у річний дохід робиться ТУТ — там, де є курси, «сьогодні» й
		// domain.NetRate; у state лишається сама арифметика покриття.
		ReserveLiquidUAH: state.Major(reserveLiquidUAH, money.UAH),
		ReserveDeposits:  reserveLadderInput(reserveRungs, today, rates),
		// Позики в самого себе — теж ГОТОВИМИ: залишок і відсоток рахує
		// domain, курс і «сьогодні» знає будівник, а в state лишається
		// розклад цілі на базову й надбавку.
		ReserveLoans: resLoans,
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
	// їх ріже розкладка надходження (allocate.go) і маршрут:
	// подушка → цілі → види. Доти цілі з бази не віднімались, і карта
	// «Скільки чого за стратегією» обіцяла на вирізку цілей більше, ніж
	// показувала модалка розкладки, яку сама ж і відкриває.
	if mth.Plan != nil {
		avail := mth.Plan.PlanUAH.Major() - mth.ReserveMonthUAH - goalsMonthUAH(doc.Goals)
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
		sum += g.FillMonthUAH.Major()
	}
	return sum
}

// withoutEarmarked — потоки без виплат вкладів подушки й цілей.
//
// Для ПРОЄКЦІЇ, і лише для неї. Тіло таких вкладів уже виведене з її
// старту (гроші подушки — не купівельна спроможність), а відсотки й
// повернення тіла доливались у рукави як звичайний дохід і реінвестувались:
// гроші нізвідки. Календар, дохід місяця й драбина ці виплати бачать —
// вони справді прийдуть, — тож фільтр тут, а не в розкладі.
func withoutEarmarked(cf []domain.CashflowItem, deposits []domain.Deposit) []domain.CashflowItem {
	earmarked := map[string]bool{}
	for _, d := range deposits {
		if d.Earmarked() {
			earmarked[d.SyntheticISIN()] = true
		}
	}
	if len(earmarked) == 0 {
		return cf
	}
	out := make([]domain.CashflowItem, 0, len(cf))
	for _, c := range cf {
		if !earmarked[c.ISIN] {
			out = append(out, c)
		}
	}
	return out
}
