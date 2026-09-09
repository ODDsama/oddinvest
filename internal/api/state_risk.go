// Процентний ризик, ліквідність і накопичений купон.
//
// Десята фаза розбиття buildState.
//
// Тут три різні питання про той самий графік виплат, і кожне має власну
// вибірку — саме тому вони й розʼїжджались, коли стояли впереміш.
//
// ЦІНОВИЙ ризик (сценарії ±п.п.) — лише ОВДП: переоцінюється те, що має
// вторинний ринок. Сертифікат фонду не має ні строку, ні фіксованого
// купона, тож дисконтувати його оцінені дивіденди означало б вигадати
// ціновий ризик, якого немає.
//
// ПЕРЕВКЛАДЕННЯ (коли гроші повернуться) — ОВДП і вклади разом, бо
// гасяться обидва. Рахується БЕЗ дисконтування: питання «коли гроші
// прийдуть», а не «скільки вони варті сьогодні».
//
// ЛІКВІДНІСТЬ — що робити, коли гроші раптом знадобились. Вікна тут
// НАКОПИЧУВАЛЬНІ: «за 90 днів» уже містить «за 30», бо саме так на них і
// дивляться — скільки буде в розпорядженні на той момент, якщо нічого не
// купувати.
package api

import (
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"

	money "github.com/Rhymond/go-money"
)

// riskInput — усе, від чого залежать ризик і ліквідність.
type riskInput struct {
	Cashflow     []domain.CashflowItem
	Holdings     domain.Holdings
	Pays         []domain.Payment
	TermDeposits []domain.Deposit
	Rates        fx.Rates

	// Дохідність портфеля — нею дисконтуються потоки. По валютах, зі
	// спадом на зведену там, де своєї ще немає.
	YieldPct     float64
	YieldByCur   map[string]float64
	AccountMinor int64
	ReserveUAH   float64
	GoalsUAH     float64
	// NPFRows — пенсійні рахунки. Потрібні лише ліквідності: у ціновий ризик
	// і ризик перевкладення НПФ не входить, бо не породжує потоку взагалі.
	NPFRows []state.NPFPositionRow

	Now   time.Time
	Today domain.Date
}

// riskPhase — три картки, які з цього виходять.
type riskPhase struct {
	RateRisk  *state.RateRisk
	Liquidity *state.Liquidity
	// AccruedUAH — накопичений купонний дохід, МІНОРНІ. Гроші, які вже
	// зароблені, але ще не виплачені.
	//
	// Показується ОКРЕМО, а не додається в капітал проєкцій: у симуляції
	// майбутні купони враховані повністю, тож додавання НКД було б
	// подвійним рахунком.
	AccruedUAH int64
}

// buildRisk рахує процентний ризик, ліквідність і НКД.
func buildRisk(in riskInput) riskPhase {
	var out riskPhase
	today, rates := in.Today, in.Rates

	for _, l := range in.Holdings.Lots {
		q := l.Remaining
		if q == 0 {
			continue
		}
		acc, err := domain.EstimateAccrued(in.Pays, l.ISIN, today)
		if err != nil || acc == nil || acc.IsZero() {
			continue
		}
		if u, err := fx.ToUAH(money.New(acc.Amount()*q, acc.Currency().Code), rates); err == nil {
			out.AccruedUAH += u.Amount()
		}
	}

	// --- процентний ризик ---
	ptsByCur := map[string][]domain.CashPoint{}
	var backWeighted, backUAH, backSoonUAH float64
	for _, cf := range in.Cashflow {
		yrs := float64(domain.DaysBetween(today, cf.Date)) / 365.0
		if yrs < 0 {
			continue
		}
		c := cf.Amount.Currency().Code
		amt := float64(cf.Amount.Amount()) / 100
		if domain.IsFundISIN(cf.ISIN) {
			continue
		}
		// НПФ — із тієї ж причини, що фонд, і з однією власною. Пенсійні
		// активи не переоцінюються ставками ОВДП, тож у ціновому ризику їм
		// нема місця; а в ризику ПЕРЕВКЛАДЕННЯ їм нема місця гостріше, ніж
		// фонду: виплата на пенсії настає за двадцять пʼять років, і
		// зважений строк перевкладення з нею перестав би означати будь-що.
		// До того ж перевкладати пенсію ніхто не збирається — її витрачають.
		if domain.IsNPFISIN(cf.ISIN) {
			continue
		}
		if !domain.IsDepositISIN(cf.ISIN) {
			ptsByCur[c] = append(ptsByCur[c], domain.CashPoint{Years: yrs, Amount: amt})
		}
		// Строк перевкладення — у гривні, щоб валюти складались.
		rateMajor, _ := fx.RateMajor(c, rates)
		uah := amt * rateMajor
		backUAH += uah
		backWeighted += yrs * uah
		if yrs <= 1 {
			backSoonUAH += uah
		}
	}
	byCurDur := map[string]float64{}
	var pvUAHTotal, macWeighted float64
	for c, pts := range ptsByCur {
		y := in.YieldByCur[c] / 100
		if y <= 0 {
			y = in.YieldPct / 100
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
		mod := mac / (1 + in.YieldPct/100)
		scen := make([]state.RiskScenario, 0, 4)
		for _, d := range []float64{-2, -1, 1, 2} {
			chg := domain.PriceChangePct(mod, d)
			scen = append(scen, state.RiskScenario{
				DeltaPP: d, ChangePct: round2(chg), ChangeUAH: round2(chg / 100 * pvUAHTotal),
			})
		}
		out.RateRisk = &state.RateRisk{
			DurationYears: round2(mac), ModifiedDur: round2(mod), PVUAH: round2(pvUAHTotal),
			ByCurrency: byCurDur, Scenarios: scen,
		}
	}
	// Строк перевкладення живе й без облігацій: портфель із самих вкладів
	// цінового ризику не має, але питання «коли перевкладати» — має.
	if backUAH > 0 {
		if out.RateRisk == nil {
			out.RateRisk = &state.RateRisk{}
		}
		out.RateRisk.ReinvestYears = round2(backWeighted / backUAH)
		out.RateRisk.ReturningUAH = round2(backUAH)
		out.RateRisk.ReinvestSoonUAH = round2(backSoonUAH)
	}

	// --- ліквідність ---
	//
	// ПІД РУКОЮ — це рахунки ПЛЮС готівка подушки ПЛЮС відкладене під
	// цілі. Доти головним числом картки стояли самі рахунки, і на
	// портфелі, де подушка лежить готівкою, вона казала «доступно 9,87 ₴»
	// людині з десятьма тисячами в сейфі.
	//
	// Питання картки — коли гроші стають ДОСТУПНІ, а не що з них дозволено
	// витратити. Подушку й ціль діставати нізвідки не треба: вони вже в
	// руках, і мовчати про них означало б відповідати не на те питання.
	// Куди їх не можна: у NowUAH (на рівності now_uah == account_uah
	// тримається звірка звіту про рух коштів) і в LockedUAH (те означає
	// «доведеться щось ламати»).
	//
	// Подвійного обліку з резервними ВКЛАДАМИ тут немає: сюди приходить
	// ReserveUAH == reserveLiquidUAH, тобто журнальна готівка подушки без
	// тіл рунг (state_builder.go). Самі рунги проходять картку нижче
	// звичайними строковими.
	availableNow := float64(in.AccountMinor)/100 + in.ReserveUAH + in.GoalsUAH
	d30 := domain.NewDate(in.Now.AddDate(0, 0, 30))
	d90 := domain.NewDate(in.Now.AddDate(0, 0, 90))
	var cf30, cf90 int64
	for _, cf := range in.Cashflow {
		if cf.Date.After(d90) {
			continue
		}
		u, cerr := fx.ToUAH(cf.Amount, rates)
		if cerr != nil {
			continue
		}
		if !cf.Date.After(d30) {
			cf30 += u.Amount()
		}
		cf90 += u.Amount()
	}
	// ЗАМКНЕНЕ Й ЗЛАМНЕ — ДВІ РІЗНІ ВІДПОВІДІ, і доти вони стояли під одним
	// підписом.
	//
	// Строковий вклад в Україні безвідкличний ЗА ЗАМОВЧУВАННЯМ: за ЦКУ
	// забрати гроші достроково можна лише там, де це прямо в договорі. Тому
	// все, що тут лежало, чесно звалось «замкнено» — але тільки доки
	// застосунок не вмів записати зворотне. Тепер уміє (прапорець
	// revocable), і зсипати обидва в одне число означало б казати «цього не
	// дістати» про гроші, які дістати можна, заплативши відсотками.
	//
	// У «під рукою»/In30/In90 зламне НЕ входить: це не вільні гроші, і
	// додати їх туди означало б назвати негайно доступним те, що лежить у
	// банку до дати погашення.
	var lockedUAH, breakableUAH int64
	// РЕЗЕРВНІ РУНГИ рахуються тут же й окремою сумою. Вони лишаються в
	// спільному числі, бо для питання «коли гроші звільняться» вклад є
	// вклад, — але без підпису картка мовчала про те, що частина
	// замкненого це власна подушка, яка просто ще не дозріла, а не
	// портфель.
	// Цільові вклади (0062) у ці ДВА підписані числа не входять навмисно:
	// вони кажуть «скільки із замкненого — твоя подушка», і домішати в них
	// гроші на авто означало б відповісти на інше питання тим самим полем.
	// У самі locked/breakable вони входять на загальних правах — замкнені
	// вони так само. Окремого підпису для цілей поки немає, і це названа
	// відсутність, а не недогляд.
	var lockedReserveUAH, breakableReserveUAH int64
	var unlockDate domain.Date
	for _, dep := range in.TermDeposits {
		// Вклад, що гаситься у вікні, вже порахований потоками вище —
		// інакше та сама сума стояла б і в «доступному», і в «замкненому».
		if dep.ClosedDate != "" || !dep.Active(today) || !dep.MaturityDate.After(d90) {
			continue
		}
		if u, cerr := fx.ToUAH(money.New(dep.BalanceAt(today), dep.Currency), rates); cerr == nil {
			if dep.Revocable {
				breakableUAH += u.Amount()
				if dep.IsReserve {
					breakableReserveUAH += u.Amount()
				}
			} else {
				lockedUAH += u.Amount()
				if dep.IsReserve {
					lockedReserveUAH += u.Amount()
				}
			}
		}
		// Дата — з УСІХ строкових, і зламних теж: питання «коли звільниться
		// найближче» про них стоїть так само, а розірвання це вибір, а не
		// розклад.
		if unlockDate == "" || dep.MaturityDate.Before(unlockDate) {
			unlockDate = dep.MaturityDate
		}
	}
	// НПФ — теж замкнене, і саме тут його місце. Причина, через яку в
	// locked_uah немає ОБЛІГАЦІЙ, до нього не діє.
	//
	// САМА ТА ПРИЧИНА ЗМІНИЛАСЬ (0059), і це варто сказати, бо в
	// застосунку тепер є ринкова ціна паперу. Але вона є лише після
	// натискання кнопки, лише для частини паперів і лише кілька днів
	// (quoteFreshDays). Твердження «стільки-то капіталу замкнено» —
	// постійне, і будувати його на числі, яке зникає, коли ціну не
	// оновлювали тиждень, означало б, що замкнена частка портфеля стрибає
	// від старанності людини, а не від складу активів. Вартість же
	// пенсійних активів відома точно й завжди.
	//
	// Окремою сумою поверх спільної: unlock_date бере НАЙБЛИЖЧУ дату, тобто
	// вклад, і без розщеплення картка казала б «замкнено 1.4 млн,
	// розблокується 2027-03», коли більшість недоступна до 2051-го. Дату
	// доступу НПФ у unlock_date не підмішуємо навмисно — там питання «коли
	// звільниться найближче», і 2051 рік на нього не відповідає.
	var npfLockedUAH float64
	for _, row := range in.NPFRows {
		npfLockedUAH += row.ValueUAH
	}
	out.Liquidity = &state.Liquidity{
		AvailableNowUAH:     round2(availableNow),
		NowUAH:              round2(float64(in.AccountMinor) / 100),
		In30UAH:             round2(availableNow + float64(cf30)/100),
		In90UAH:             round2(availableNow + float64(cf90)/100),
		ReserveUAH:          round2(in.ReserveUAH),
		GoalsUAH:            round2(in.GoalsUAH),
		LockedUAH:           round2(float64(lockedUAH)/100 + npfLockedUAH),
		BreakableUAH:        round2(float64(breakableUAH) / 100),
		UnlockDate:          string(unlockDate),
		LockedNPFUAH:        round2(npfLockedUAH),
		LockedReserveUAH:    round2(float64(lockedReserveUAH) / 100),
		BreakableReserveUAH: round2(float64(breakableReserveUAH) / 100),
	}
	return out
}
