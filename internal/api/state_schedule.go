// Розклад портфеля — коли й скільки повертається, і що з того дохід.
//
// Четверта фаза розбиття buildState. Тут дві сусідні, але різні речі:
// СКЛАСТИ розклад (календар виплат і драбина погашень, нативно) і
// ЗВЕСТИ з нього показники (надходження по місяцях, дохід на місяць,
// драбина в грн-екв.). Перше — про факти й оцінки, друге — про те, як
// вони показуються; змішані в одній функції, вони вимагали тримати в
// голові обидва питання одразу.
//
// ⚠️ Порядок append тут значущий. Обидва зрізи сортуються НЕПОВНИМ
// ключем — календар лише за датою, — а sort.Slice нестабільний. Тобто
// дві виплати того самого дня стоять у тому порядку, у якому їх додали,
// і переставити виклики місцями означає мовчки переставити рядки в
// календарі. Сьогодні це відтворювано (усі джерела дають сталий порядок,
// і TestBuildStateIsDeterministic це стереже), але тримається на
// відсутності повного ключа, а не на його наявності. Зробити ключ
// повним — окреме, свідоме виправлення: воно ПЕРЕСТАВИТЬ рядки, які
// зараз стоять інакше, і мусить прийти зі своїм diff-ом golden.
package api

import (
	"fmt"
	"sort"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"

	money "github.com/Rhymond/go-money"
)

// schedule — усе, що портфель поверне, у НАТИВНИХ валютах.
type schedule struct {
	// Cashflow — календар виплат: купони й погашення ОВДП, відсотки й тіло
	// вкладів, оцінені дивіденди фондів. Відсортований за датою.
	Cashflow []domain.CashflowItem
	// Ladder — номінал, що повертається кожного року, окремо по валютах.
	Ladder []domain.LadderEntry
}

// buildSchedule складає календар і драбину з усіх трьох інструментів.
//
// ДВІ дати, і різниця між ними суттєва. from — звідки ПОКАЗУВАТИ вже
// відомі виплати: зведенню це сьогодні, а вкладці «Календар» — 1970-й,
// бо там гортають і минуле. today — від чого рахувати ОЦІНКИ, і це
// завжди справжній сьогоднішній день: оцінених дивідендів у минулому не
// буває, там є фактичні операції фонду.
//
// Доти цієї функції існувало дві. Ендпойнт /api/calendar збирав потоки
// сам — облігації плюс вклади, — і фонди в нього не потрапляли взагалі.
// На живих даних це виглядало так: у зведенні REIT платить 10 числа
// щомісяця й дає чверть усього доходу, а у вкладці «Календар» за рік
// 26 рядків і жодного фондового. Одне питання, дві відповіді, і жодного
// способу помітити розбіжність, окрім як звірити очима.
// fundMonths — на скільки місяців уперед оцінювати дивіденди фондів.
//
// Параметр, а не константа, бо в двох питань різні горизонти. Календар і
// зведення дивляться на рік — рівно стільки заслуговує ОЦІНКА на питання
// «що впаде на рахунок найближчим часом». Профіль надходжень малює форму
// плану на весь горизонт до дедлайну, і фонд, що замовкає на тринадцятому
// місяці, лишив би там сходинку, якої в житті немає.
const scheduleFundMonths = 12

func buildSchedule(src *sources, hold domain.Holdings, from, today domain.Date, fundMonths int) (schedule, error) {
	cashflow, err := domain.FuturePayments(src.pays, src.lots, src.sales, from)
	if err != nil {
		return schedule{}, err
	}
	ladder := domain.Ladder(src.bonds, src.lots, src.sales, today)

	// Вклади мають розклад, тож їхні відсотки й повернення тіла входять у
	// той самий календар і ту саму драбину, що й купони й погашення ОВДП.
	cashflow = append(cashflow, domain.DepositCashflows(src.termDeposits, from)...)
	ladder = append(ladder, domain.DepositLadder(src.termDeposits, today)...)

	// Фонди теж — але ОЦІНКОЮ, і лише коли відомий день виплати.
	//
	// Доти межа була суворою: у календарі стояли самі зобовʼязання. Ціна
	// суворості виявилась вищою за користь — календар показував дві
	// третини потоку й мовчав про решту, а «скільки я отримую щомісяця»
	// рахувалось із фондами в одному місці й без них у сусідньому.
	//
	// Оцінка лишається впізнаваною (ключ fund:<назва>), і там, де їй не
	// місце, вона відсіюється: у ціновому ризику сертифікат не
	// переоцінюється ставками ОВДП, а в ризику перевкладення дивіденд не
	// є поверненням тіла.
	for i := range hold.Funds {
		fp := &hold.Funds[i].FundPosition
		// Накопичувальний фонд не платить нічого — увесь його дохід сидить
		// у ціні сертифіката й виходить назовні лише на закритті.
		//
		// Доти цієї перевірки не було, і оцінка трималась виключно на
		// домовленості «в accum-рядка payout_day лишають нулем». Варто
		// комусь його проставити — і календар вигадав би щомісячні
		// дивіденди фонду, який їх не платить.
		if src.fundRefs[fp.Fund].Kind == store.FundAccumulating {
			continue
		}
		// Ставка — ВЛАСНА фондова, і береться вона тією самою функцією,
		// що й кошик виплат у симуляції (state_funds.go): обіцянка
		// фонду, якщо задана, інакше виміряна дивідендна. Два різні
		// числа на той самий потік означали б, що картка «найближчі
		// виплати» і крива капіталу говорять про різні фонди.
		//
		// Доти тут стояло сире expected_yield_bp — тобто на фонді з
		// простою обіцянкою бралось число, яке модель компаундить
		// інакше, ніж мав на увазі фонд.
		measured, _ := domain.DividendYieldNet(src.fundOps, fp, today)
		y := fundOwnRatePct(src.fundRefs[fp.Fund], measured)
		cashflow = append(cashflow, domain.FundDividendFlows(fp, y, fundMonths, today)...)
	}

	// next_payment бере перший потік, драбина йде по роках — обидва
	// покладаються на порядок, який append порушив.
	sort.Slice(cashflow, func(i, j int) bool { return cashflow[i].Date < cashflow[j].Date })
	sort.Slice(ladder, func(i, j int) bool {
		if ladder[i].Year != ladder[j].Year {
			return ladder[i].Year < ladder[j].Year
		}
		return ladder[i].Currency < ladder[j].Currency
	})
	return schedule{Cashflow: cashflow, Ladder: ladder}, nil
}

// incomeSummary — розклад, зведений у показники для документа.
type incomeSummary struct {
	// LadderUAH — драбина в грн-еквіваленті, для стовпчиків.
	LadderUAH []state.YearAmount
	// Income12m — усі надходження по місяцях на рік наперед; Coupons12m —
	// те саме БЕЗ погашень.
	Income12m  []state.MonthAmount
	Coupons12m []state.MonthAmount
	// MonthlyNow — скільки виходить на місяць у середньому за рік.
	MonthlyNow float64
}

// summarizeIncome зводить розклад у грн-еквівалент.
//
// Купони рахуються ОКРЕМО від погашень навмисно: погашення — це
// повернення власного тіла, а не дохід, і на питання «скільки я отримую»
// відповідають лише купони. Плутанина тут дала б число, яке стрибає
// вчетверо в місяць погашення й повертається назад наступного.
func summarizeIncome(sch schedule, rates fx.Rates, today domain.Date) incomeSummary {
	ladderByYear := map[int]int64{}
	for _, e := range sch.Ladder {
		if u, err := fx.ToUAH(money.New(e.Nominal, e.Currency), rates); err == nil {
			ladderByYear[e.Year] += u.Amount()
		}
	}
	years := make([]int, 0, len(ladderByYear))
	for y := range ladderByYear {
		years = append(years, y)
	}
	sort.Ints(years)
	out := incomeSummary{LadderUAH: make([]state.YearAmount, 0, len(years))}
	for _, y := range years {
		out.LadderUAH = append(out.LadderUAH,
			state.YearAmount{Year: y, UAH: round2(float64(ladderByYear[y]) / 100)})
	}

	incByMonth := map[string]float64{}
	couByMonth := map[string]float64{}
	for _, cf := range sch.Cashflow {
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
	out.Income12m = make([]state.MonthAmount, 0, 12)
	out.Coupons12m = make([]state.MonthAmount, 0, 12)
	couponSum := 0.0
	for i := 0; i < 12; i++ {
		t := today.Time().AddDate(0, i, 0)
		key := fmt.Sprintf("%04d-%02d", t.Year(), int(t.Month()))
		out.Income12m = append(out.Income12m,
			state.MonthAmount{Month: key, Amount: round2(incByMonth[key])})
		out.Coupons12m = append(out.Coupons12m,
			state.MonthAmount{Month: key, Amount: round2(couByMonth[key])})
		couponSum += couByMonth[key]
	}
	// Число ЧИСТЕ, і це не збіг трьох різних правил, а наслідок одного:
	// сюди все потрапляє вже після податку. Купон ОВДП від нього
	// звільнений (брутто = нетто), відсотки вкладу графік віддає після
	// утримання, дивіденди фондів додаються теж чистими. Скільки саме
	// забрав податок — окремо, у /api/tax.
	out.MonthlyNow = round2(couponSum / 12)
	return out
}
