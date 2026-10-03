// Рух грошей, розкладений на події: підсумок місяця, рік, серія, податок.
//
// Події — той самий журнал руху на межі інструмента, що й «внесено» в
// документі стану (state_flows.go), лише в гривні й із підписами, плюс
// рухи подушки й цілей. Рахунків застосунок не веде (ревізія 2026-10-03),
// тож поповнень, знять і конвертацій тут більше немає, а «внесено своїх»
// виводиться: покупки мінус виплати й виходи плюс подушка й цілі.

package engine

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/settings"
	"github.com/ODDsama/oddinvest/internal/state"
	money "github.com/Rhymond/go-money"
)

// FlowEvent — один рух грошей у грн-еквіваленті. Знак — з боку власника:
// плюс гроші повернулись до нього (виплата, вихід), мінус пішли в
// інструмент (покупка). У подушки й цілей — плюс відклав, мінус забрав.
type FlowEvent struct {
	Date  domain.Date
	Kind  string // income | purchase | outside
	UAH   int64
	Label string
	// Principal — цей «дохід» є поверненням ТІЛА: погашення ОВДП або
	// тіло вкладу. Для звіту це той самий рух, що й купон, але заробітком
	// воно не є: гроші повернулись, а не прибули.
	Principal bool
}

const (
	FlowIncome   = "income"
	FlowPurchase = "purchase"
	// FlowOutside — рух ПОЗА інструментами: у подушку чи ціль накопичення й
	// назад. Свої гроші, які людина відклала або забрала сама.
	FlowOutside = "outside"
)

// CashEvents — усе, що рухало гроші через межу портфеля, окремими
// датованими подіями (у сьогоднішніх гривнях).
func (e *Engine) CashEvents(ctx context.Context) ([]FlowEvent, error) {
	today := domain.NewDate(time.Now())
	src, err := e.loadSources(ctx, today)
	if err != nil {
		return nil, err
	}
	flows, err := instrumentFlows(flowInputsOf(src, today))
	if err != nil {
		return nil, err
	}
	uah := func(minor int64, cur string) int64 {
		if u, err := fx.ToUAH(money.New(minor, cur), src.rates); err == nil {
			return u.Amount()
		}
		return 0
	}
	var out []FlowEvent
	for _, f := range flows {
		// Продаж і розірвання для звіту — від'ємна покупка: вихід із
		// позиції, а не заробіток на ній.
		kind := FlowPurchase
		if f.Kind == flowIncome {
			kind = FlowIncome
		}
		if v := uah(f.Amount, f.Currency); v != 0 {
			out = append(out, FlowEvent{Date: f.Date, Kind: kind, UAH: v, Label: f.Label, Principal: f.Principal})
		}
	}
	for _, op := range src.reserveOps {
		label := "у резерв"
		if op.Amount < 0 {
			label = "з резерву"
		}
		if v := uah(op.Amount, op.Currency); v != 0 {
			out = append(out, FlowEvent{Date: op.Date, Kind: FlowOutside, UAH: v, Label: label})
		}
	}
	goalName := map[int64]string{}
	for _, g := range src.goals {
		goalName[g.ID] = g.Name
	}
	for _, op := range src.goalOps {
		label := "у ціль " + goalName[op.GoalID]
		if op.Amount < 0 {
			label = "з цілі " + goalName[op.GoalID]
		}
		if v := uah(op.Amount, op.Currency); v != 0 {
			out = append(out, FlowEvent{Date: op.Date, Kind: FlowOutside, UAH: v, Label: label})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}

// taxLine — рядок звіту про податок: один вид доходу.
type taxLine struct {
	Kind     string      `json:"kind"`
	Label    string      `json:"label"`
	GrossUAH state.Money `json:"gross_uah"`
	TaxUAH   state.Money `json:"tax_uah"`
	NetUAH   state.Money `json:"net_uah"`
	RatePct  float64     `json:"rate_pct"`
}

// TaxReport — відповідь /api/tax (api/handlers_reports.go).
type taxReport struct {
	// Year — 0, коли період заданий парою from/to, а не роком. Клієнту
	// це потрібно, щоб не підписувати довільний відрізок роком.
	Year int    `json:"year,omitempty"`
	From string `json:"from"`
	To   string `json:"to"`
	// Currency — завжди гривня, і сказано це явно (schema 3): податок
	// платиться в гривні за курсом на дату події, і звіт для декларації
	// у валюту звітності НЕ перекладається — єдиний такий маршрут.
	// Читач, який бере символ із summary.currency, тут мусить узяти цей.
	Currency string    `json:"currency"`
	GrossUAH float64   `json:"gross_uah"`
	TaxUAH   float64   `json:"tax_uah"`
	NetUAH   float64   `json:"net_uah"`
	RatePct  float64   `json:"rate_pct"`
	ByKind   []taxLine `json:"by_kind,omitempty"`
	// Credits — те, що держава ПОВЕРТАЄ, а не забирає: податкова знижка
	// на внески в НПФ.
	//
	// Окремий блок, а не рядок у ByKind, і це не оформлення. Арифметика
	// того переліку — gross, tax, net = gross − tax, rate = tax/gross;
	// відʼємний рядок ламає всі чотири числа, а rate_pct у нього стає
	// безглуздим. Тому знижка стоїть поруч і в загальні суми вгорі НЕ
	// входить: там питання «скільки податку з мене взяли», і змішувати з
	// ним повернення означало б відповідати на нього заниженим числом.
	//
	// І головне: це ОЦІНКА, а не факт. Її ще треба отримати декларацією
	// до 31 грудня наступного року, вона працює лише проти зарплати й не
	// переноситься. Тому поруч стоїть Note.
	Credits []taxLine `json:"credits,omitempty"`
	// Звідки взялись гривневі числа. Читач має право знати, що це не
	// сьогоднішній курс, і наскільки в найгіршому разі відстала точка,
	// з якої курс узято: помісячний бекфіл на подіях 2019 року дає
	// відставання до тридцяти днів, і мовчати про це означало б
	// видавати оцінку за факт.
	FXBasis     string `json:"fx_basis"`
	FXMaxLagDay int    `json:"fx_max_lag_days,omitempty"`
	Note        string `json:"note,omitempty"`
	// FundGaps — місяці, у яких фонд мав заплатити (позиція була, день
	// виплати минув), а запису в журналі немає. Це НЕ дохід, який ми
	// оцінили: це зізнання, що виписку заведено не повністю. Купони
	// приходять із довідника НБУ самі, дивіденди — лише з виписки, тож
	// мовчати про пропуск означало б видавати два місяці за рік.
	FundGaps []fundGap `json:"fund_gaps,omitempty"`
}

// TaxReport — скільки з доходу забрала держава за [from, to].
//
// Скільки з доходу забрала держава. Асиметрія між інструментами вже
// зашита в real_pct, але відсотком її не відчуваєш: вклад під 16% і
// папір під 16% — це різні гроші, і рядок «податок з'їв N ₴» каже це
// пряміше за будь-яку ставку.
//
// Купон ОВДП звільнений від податку, дивіденд фонду оподатковується
// (нині 14% = ПДФО 9% + військовий збір 5%), відсотки вкладу теж (23%
// = ПДФО 18% + ВЗ 5%). Ставки НЕ зашиті: у фонду беремо фактично
// утримане з операції, у вкладу — ставку з самого вкладу.
func (e *Engine) TaxReport(ctx context.Context, year int, from, to domain.Date, now time.Time) (taxReport, error) {

	lots, sales, _, pays, err := e.Portfolio(ctx)
	if err != nil {
		return taxReport{}, err
	}
	statuses, err := e.st.PaymentStatuses(ctx)
	if err != nil {
		return taxReport{}, err
	}
	today := domain.NewDate(now)
	inWindow := func(d domain.Date) bool { return !d.Before(from) && !d.After(to) }
	arrived := domain.Arrived(statuses, today)

	// Курс НА ДАТУ ПОДІЇ, а не сьогоднішній (fx_asof.go). Доти всі валютні
	// суми переводились одним поточним курсом: на портфелі, де долар
	// купували по 27, а дивляться на нього по 44, податок за минулий рік
	// виходив у півтора раза більшим за реально сплачений.
	asOf := NewAsOfRates(e.st)
	var fxErr error
	uah := func(m *money.Money, on domain.Date) int64 {
		v, err := asOf.UAH(ctx, m, on)
		if err != nil && fxErr == nil {
			fxErr = err
		}
		return v
	}

	// bondTaxUAH — податок із купонів ОВДП. Нуль за законом: доходи з
	// державних облігацій звільнені і від ПДФО, і від військового збору.
	// Іменована константа замість голого нуля в рядку нижче — щоб було
	// видно, що це рішення законодавця, а не незаповнене поле.
	const bondTaxUAH int64 = 0

	var bondGross, bondAccrued, fundGross, fundTax, saleGross, saleTax, depGross, depTax int64

	// ОВДП: купони. Погашення — повернення власного тіла, не дохід.
	pastCF, err := domain.FuturePayments(pays, lots, sales, "1970-01-01")
	if err != nil {
		return taxReport{}, err
	}
	for _, cf := range pastCF {
		if cf.Type == domain.PayRedemption || !inWindow(cf.Date) || !arrived(cf.ISIN, cf.Date) {
			continue
		}
		bondGross += uah(cf.Amount, cf.Date)
	}
	// НКД, сплачений при купівлі, — не дохід, а повернення власних грошей.
	// Купуючи папір усередині купонного періоду, платиш продавцю накопичений
	// купон у брудній ціні; купон, що приходить за кілька днів, повертає його
	// назад. Живий приклад, на якому це побачили: 9 паперів UA4000239081,
	// куплених 15 і 18 серпня 2026, дали купон 739,80 грн 26 серпня — а НКД у
	// їхній ціні був 705,95. Заробили 33,85, картка показувала всі 739,80, і
	// разом із нею брехала ставка внизу: 1,1% замість 9,3%.
	//
	// Знак ставиться ТУТ, один раз: bondAccrued тримає те, що покаже рядок.
	//
	// Фільтри inWindow і arrived — буква в букву ті самі, що в циклі вище.
	// Саме це тримає пару разом: відрахування не може зʼявитись без купона,
	// який воно гасить (інакше рядок ОВДП пішов би в мінус). Немає лише
	// перевірки на PayRedemption — елементи купонодатовані за побудовою.
	accrued, err := domain.AccruedPaid(pays, lots, sales)
	if err != nil {
		return taxReport{}, err
	}
	for _, it := range accrued {
		if !inWindow(it.Date) || !arrived(it.ISIN, it.Date) {
			continue
		}
		bondAccrued -= uah(it.Amount, it.Date)
	}
	// Фонди: беремо ФАКТИЧНО утримане, а не ставку. Ставка змінювалась і
	// ще змінюватиметься, а у виписці стоїть те, що забрали насправді.
	fundOps, err := e.st.ListFundOps(ctx)
	if err != nil {
		return taxReport{}, err
	}
	// Довідник — щоб знати день виплати й вид фонду: без них не відрізнити
	// пропущений місяць від фонду, який просто не платить (tax_coverage.go).
	fundRefs, err := e.st.ListFunds(ctx)
	if err != nil {
		return taxReport{}, err
	}
	// Дивіденди. Продаж сертифікатів рахується окремо нижче: у нього інша
	// база — не виручка, а прибуток.
	for _, op := range fundOps {
		if op.Kind != domain.FundDividend || !inWindow(op.Date) {
			continue
		}
		fundGross += uah(money.New(op.Amount, op.Currency), op.Date)
		fundTax += uah(money.New(op.Tax, op.Currency), op.Date)
	}
	// Продаж сертифікатів. База — ПРИБУТОК, а не виручка: 17 986,80 грн у
	// графі «Нараховано» сказали б, що ви заробили сімнадцять тисяч, тоді
	// як заробили сто шістдесят дві.
	//
	// Собівартість проданого рахує domain.FundSales за FIFO — тією ж
	// конвенцією, що й брокер, і саме тому утримане сходиться з базою:
	// 37,35 грн = 23% від 162,40 у 2025-му, 1,78 = 23% від 7,73 у 2026-му.
	// Довід, чому тут FIFO, а в позиції середньозважена, написаний при
	// самій FundSales.
	//
	// Конвертації сюди не потрапляють: вихід із інструмента не відбувся, і
	// податку з них не тримали.
	for _, sale := range domain.FundSales(fundOps) {
		if !inWindow(sale.Date) {
			continue
		}
		// Збиток доходом не є й дивідендного не зменшує, тож у базу йде
		// лише додатний прибуток. Податок — завжди фактичний: якщо його
		// колись утримають зі збиткової угоди, краще побачити дивну
		// ставку, ніж загублену гривню.
		if sale.Gain > 0 {
			saleGross += uah(money.New(sale.Gain, sale.Currency), sale.Date)
		}
		saleTax += uah(money.New(sale.Tax, sale.Currency), sale.Date)
	}
	// Вклади: брутто й податок із того самого проходу, що й самі
	// відсотки — графік показує нетто, і ділити його назад означало б
	// накопичувати похибку.
	termDeposits, err := e.st.ListTermDeposits(ctx)
	if err != nil {
		return taxReport{}, err
	}
	for _, dep := range termDeposits {
		// Кожна виплата — окремою подією: зі своєю датою (курс того дня,
		// як у купонів і дивідендів) і лише коли вже НАДІЙШЛА. Доти відсотки
		// вікна зводились в одну суму за курсом кінця вікна, розірвання не
		// враховувалось, а поточний рік уже в вересні показував відсотки
		// жовтня–грудня. Ставка податку — на дату виплати (DepositTaxBPOn).
		for _, ev := range domain.DepositInterestEvents(dep, from, to) {
			if !arrived(dep.SyntheticISIN(), ev.Date) {
				continue
			}
			depGross += uah(money.New(ev.Gross, dep.Currency), ev.Date)
			depTax += uah(money.New(ev.Tax, dep.Currency), ev.Date)
		}
	}
	if fxErr != nil {
		return taxReport{}, fxErr
	}

	minor := func(v int64) float64 { return domain.Round2(float64(v) / 100) }
	mk := func(kind, label string, gross, tax int64) taxLine {
		l := taxLine{Kind: kind, Label: label,
			GrossUAH: state.Major(minor(gross), money.UAH), TaxUAH: state.Major(minor(tax), money.UAH), NetUAH: state.Major(minor(gross-tax), money.UAH)}
		// Ставку рахуємо лише на додатному брутто. Нуль тут не тільки рятує
		// від ділення на нуль: рядок відрахування (НКД) відʼємний, і ставка на
		// поверненні власних грошей — не мале число, а помилка категорії.
		if gross > 0 {
			l.RatePct = domain.Round2(float64(tax) / float64(gross) * 100)
		}
		return l
	}
	out := taxReport{
		Year: year, From: string(from), To: string(to),
		FXBasis:     "курс НБУ на дату події або найближчий попередній",
		FXMaxLagDay: asOf.maxLag,
	}
	coverNote, gaps := fundCoverage(fundOps, fundRefs, from, to, today)
	out.Note = joinNotes(asOf.Note(), coverNote)
	out.FundGaps = gaps
	for _, l := range []taxLine{
		// Нуль тут — не «податку немає в наших даних», а законодавче
		// звільнення: доходи з ОВДП не оподатковуються ні ПДФО, ні
		// військовим збором. Константа, а не літерал, щоб зміна закону
		// була одним правленням, а не полюванням по файлу.
		mk("bond", "Купони ОВДП", bondGross, bondTaxUAH),
		// Нуль податку тут — НЕ те саме звільнення, що рядком вище: повернення
		// власних грошей не має бази оподаткування взагалі. Тому літерал, а не
		// bondTaxUAH: змішати два різні нулі під одним іменем означало б, що
		// зміна закону про ОВДП мовчки поїде і сюди.
		//
		// Ключ bond_accrued — поверхня API, а не оформлення: фронт по ньому
		// робить відступ, показуючи, що рядок належить купонам НАД ним.
		mk("bond_accrued", "− НКД, сплачений при купівлі", bondAccrued, 0),
		mk("fund", "Дивіденди фондів", fundGross, fundTax),
		mk("fund_sale", "Прибуток із продажу сертифікатів", saleGross, saleTax),
		mk("deposit", "Відсотки вкладів", depGross, depTax),
	} {
		if l.GrossUAH.Major() != 0 {
			out.ByKind = append(out.ByKind, l)
		}
	}
	gross, tax := bondGross+bondAccrued+fundGross+saleGross+depGross, fundTax+saleTax+depTax
	out.GrossUAH, out.TaxUAH, out.NetUAH = minor(gross), minor(tax), minor(gross-tax)
	if gross > 0 {
		out.RatePct = domain.Round2(float64(tax) / float64(gross) * 100)
	}

	// Податкова знижка на внески в НПФ — лише для КАЛЕНДАРНОГО року: ліміт
	// у неї місячний, а стеля річна, тож на довільному відрізку from/to
	// вона не означала б нічого. Рік 0 (заданий парою дат) знижку не
	// показує — і це чесніше за число, пораховане не за той період.
	if year > 0 {
		if npfAccounts, aerr := e.st.ListNPFAccounts(ctx); aerr == nil && len(npfAccounts) > 0 {
			npfOps, oerr := e.st.ListNPFOps(ctx)
			// Помилку читання налаштувань ковтаємо тут так само, як уже
			// ковтається oerr поруч: знижка — додаткова плитка звіту, і
			// без неї звіт лишається правильним. Мапа nil читається як
			// «нічого не задано», тобто веде до дефолтів.
			raw, _ := e.st.AllSettings(ctx) //nolint:errcheck // без налаштувань знижки просто не буде — рядок звіту, а не сам звіт
			set := settings.Load(raw)
			if oerr == nil {
				// Сума часток — знижка платника: ліміт і стеля ПДФО одні на
				// всі рахунки (domain.NPFCreditByAccount).
				var credit int64
				for _, v := range npfCreditsUAH(npfAccounts, npfOps, set, year) {
					credit += int64(math.Round(v * 100))
				}
				if credit > 0 {
					// GrossUAH — сума внесків у межах ліміту, TaxUAH — сама
					// знижка з мінусом: у цьому блоці «податок» і означає
					// рух державі, тож повернення від'ємне.
					out.Credits = append(out.Credits, taxLine{
						Kind: "npf_credit", Label: "Податкова знижка на внески в НПФ",
						TaxUAH: state.Major(-minor(credit), money.UAH), NetUAH: state.Major(minor(credit), money.UAH),
					})
					out.Note = strings.TrimSpace(out.Note + " Знижка на внески в НПФ — ОЦІНКА, " +
						"а не факт: її треба отримати декларацією до 31 грудня наступного року, " +
						"вона працює лише проти зарплати й не переноситься. У загальні суми " +
						"звіту не входить.")
				}
			}
		}
	}
	out.Currency = money.UAH
	return out, nil
}

// cashSummary — рух грошей за проміжок, у мінорних гривнях.
//
// Спільний для «Підсумку місяця» й «Року»: дві сторінки питають про той
// самий проміжок, і два обчислення тих самих сум розійшлись би мовчки.
// Знаки сирі, як у самих подіях: покупка від'ємна. Перевертає її той, хто
// показує.
type cashSummary struct {
	IncomeUAH   int64
	PurchaseUAH int64
	// OutsideUAH — у подушку й цілі, нетто.
	OutsideUAH int64
	Rows       []FlowEvent
}

// ContribUAH — внесено в інструменти нетто: покупки мінус виплати й
// виходи. Не поле, а вираз: збережене поле можна забути оновити.
func (c cashSummary) ContribUAH() int64 { return -(c.IncomeUAH + c.PurchaseUAH) }

// OwnUAH — свої гроші за проміжок: інструменти плюс подушка й цілі. Те
// саме означення, що в плитки «Цей місяць» (state_month.go).
func (c cashSummary) OwnUAH() int64 { return c.ContribUAH() + c.OutsideUAH }

// Major — мінорні в гривні, для JSON. Метод, а не вільна функція, щоб
// обидва споживачі округляли однаково.
func (cashSummary) Major(v int64) float64 { return domain.Round2(float64(v) / 100) }

func SummarizeCash(events []FlowEvent, from, to domain.Date) cashSummary {
	var out cashSummary
	for _, e := range events {
		if e.Date.Before(from) || e.Date.After(to) {
			continue
		}
		switch e.Kind {
		case FlowIncome:
			out.IncomeUAH += e.UAH
		case FlowPurchase:
			out.PurchaseUAH += e.UAH
		case FlowOutside:
			out.OutsideUAH += e.UAH
		}
		out.Rows = append(out.Rows, e)
	}
	return out
}
