// Рух грошей, розкладений на події: виписка, податок, бенчмарк.
//
// УВАГА: ті самі підсумки рахує гаманець у state_cash.go, але зведеними
// за весь час. Обидві реалізації мусять сходитись —
// TestCashflowStatementReconciles саме про це. Міняєш тут — дивись і туди.

package api

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	money "github.com/Rhymond/go-money"
)

// flowEvent — один рух грошей на рахунку, у грн-еквіваленті. Знак —
// напрямок: плюс збільшує рахунок, мінус зменшує.
type flowEvent struct {
	Date  domain.Date
	Kind  string // income | contribution | purchase | conversion
	UAH   int64
	Label string
}

const (
	flowIncome       = "income"
	flowContribution = "contribution"
	flowPurchase     = "purchase"
	flowConversion   = "conversion"
)

// cashEvents — усе, що коли-небудь рухало гроші на рахунках, окремими
// датованими подіями.
//
// buildState рахує ті самі величини, але зведеними за весь час, тож
// відповісти «а що сталося в липні» з нього не можна. Тут та сама
// арифметика розкладена на події — і саме тому підсумок обов'язково має
// збігтися з account_uah зі зведення; тест на це і є захистом від того,
// що дві реалізації розійдуться.
func (s *Server) cashEvents(ctx context.Context) ([]flowEvent, error) {
	lots, sales, _, pays, err := s.portfolio(ctx)
	if err != nil {
		return nil, err
	}
	rates, err := s.rates(ctx)
	if err != nil {
		return nil, err
	}
	statuses, err := s.st.PaymentStatuses(ctx)
	if err != nil {
		return nil, err
	}
	today := domain.NewDate(time.Now())
	arrived := domain.Arrived(statuses, today)
	uah := func(m *money.Money) int64 {
		if u, err := fx.ToUAH(m, rates); err == nil {
			return u.Amount()
		}
		return 0
	}

	var out []flowEvent
	add := func(d domain.Date, kind string, amt int64, label string) {
		if amt != 0 {
			out = append(out, flowEvent{Date: d, Kind: kind, UAH: amt, Label: label})
		}
	}

	// Дохід: купони й погашення ОВДП.
	pastCF, err := domain.FuturePayments(pays, lots, sales, "1970-01-01")
	if err != nil {
		return nil, err
	}
	for _, cf := range pastCF {
		if arrived(cf.ISIN, cf.Date) {
			add(cf.Date, flowIncome, uah(cf.Amount), cf.ISIN)
		}
	}
	// Дохід і покупки по фондах.
	fundOps, err := s.st.ListFundOps(ctx)
	if err != nil {
		return nil, err
	}
	for _, op := range fundOps {
		switch op.Kind {
		case domain.FundBuy:
			add(op.Date, flowPurchase, -uah(money.New(op.Amount, op.Currency)), "сертифікати "+op.Fund)
		case domain.FundDividend:
			add(op.Date, flowIncome, uah(money.New(op.Amount-op.Tax, op.Currency)), "дивіденд "+op.Fund)
		case domain.FundSell:
			// Продаж повертає гроші на рахунок, але це не дохід і не
			// внесок — це вихід із позиції. Окремої категорії він не
			// заслуговує, тож іде від'ємною покупкою.
			add(op.Date, flowPurchase, uah(money.New(op.Amount-op.Tax, op.Currency)), "продаж "+op.Fund)
		}
	}
	// Внески в НПФ — покупки, і лише вони: доходу звідси не приходить до
	// пенсійного віку, тож зворотного рядка в цього інструмента немає.
	//
	// Це ДЗЕРКАЛО дебету гаманця в state_builder.go, і саме тому воно тут, а
	// не «для повноти». Агрегатний гаманець і цей подієвий звіт звіряються
	// TestCashflowStatementReconciles; половина без другої половини —
	// розбіжність рівно на суму внесків, і обидва числа лишились би
	// правдоподібними.
	npfAccounts, err := s.st.ListNPFAccounts(ctx)
	if err != nil {
		return nil, err
	}
	npfCur := map[int64]string{}
	npfName := map[int64]string{}
	for _, a := range npfAccounts {
		cur := a.Currency
		if cur == "" {
			cur = money.UAH
		}
		npfCur[a.ID], npfName[a.ID] = cur, a.Name
	}
	npfOps, err := s.st.ListNPFOps(ctx)
	if err != nil {
		return nil, err
	}
	for _, op := range npfOps {
		if op.Date.After(today) {
			continue
		}
		cur := npfCur[op.NPFID]
		if cur == "" {
			cur = money.UAH
		}
		add(op.Date, flowPurchase, -uah(money.New(op.Amount, cur)), "внесок "+npfName[op.NPFID])
	}
	// Вклади: розміщення й поповнення — покупки, відсотки — дохід.
	termDeposits, err := s.st.ListTermDeposits(ctx)
	if err != nil {
		return nil, err
	}
	for _, dep := range termDeposits {
		if !dep.OpenDate.After(today) {
			add(dep.OpenDate, flowPurchase, -uah(money.New(dep.Principal, dep.Currency)), "вклад "+dep.Bank)
		}
		for _, t := range dep.Topups {
			if !t.Date.After(today) {
				add(t.Date, flowPurchase, -uah(money.New(t.Amount, dep.Currency)), "поповнення вкладу "+dep.Bank)
			}
		}
		if dep.ClosedDate != "" {
			if !dep.ClosedDate.After(today) {
				add(dep.ClosedDate, flowPurchase, uah(money.New(dep.ClosedAmount, dep.Currency)), "розірвано "+dep.Bank)
			}
			continue
		}
		for _, cf := range domain.DepositSchedule(dep, "1970-01-01") {
			if arrived(cf.ISIN, cf.Date) {
				add(cf.Date, flowIncome, uah(cf.Amount), "відсотки "+dep.Bank)
			}
		}
	}
	// Купівлі паперів — ціна разом із комісією.
	for _, l := range lots {
		cost, cerr := domain.LotCost(l)
		if cerr != nil {
			continue
		}
		add(l.BuyDate, flowPurchase, -uah(cost), l.ISIN)
	}
	// Продаж паперів на вторинці — теж від'ємна покупка.
	lotISIN := map[int64]string{}
	for _, l := range lots {
		lotISIN[l.ID] = l.ISIN
	}
	for _, sl := range sales {
		if res, serr := domain.SaleProceeds(sl); serr == nil {
			add(sl.SaleDate, flowPurchase, uah(res), "продаж "+lotISIN[sl.LotID])
		}
	}
	// Свої гроші: поповнення й зняття рахунку.
	cash, err := s.st.ListDeposits(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range cash {
		label := "поповнення"
		if d.Amount < 0 {
			label = "зняття"
		}
		add(d.Date, flowContribution, uah(money.New(d.Amount, d.Currency)), label)
	}
	// Резерву тут НЕМАЄ — і це не забутий інструмент, а межа цього звіту.
	// Він відповідає на «що робилось із грошима НА РАХУНКАХ», і його
	// підсумок мусить дорівнювати account_uah (TestCashflowStatementReconciles).
	// Матрац на рахунку брокера не лежить, тож подія «відклав 5 000» його
	// не змінює — а додана сюди, зламала б рівність.
	//
	// Переміщення гаманець → резерв у звіті все одно видно: його перша
	// нога записана як зняття в `deposits` і вже пройшла вище звичайним
	// внеском зі знаком мінус. Через це `contributed_uah` цього звіту й
	// `month_deposited_uah` зі зведення — РІЗНІ величини: перша про
	// рахунок, друга про капітал, і на місяці з відкладанням вони мають
	// розійтись рівно на відкладену суму.
	//
	// Конвертації. У гривневому еквіваленті вони не нульові: обмін
	// зроблено за курсом СВОГО дня, а перераховуємо ми за сьогоднішнім,
	// і різниця — це рух курсу з того часу. Ховати її не можна, інакше
	// підсумок не зійдеться з рахунком.
	convs, err := s.st.ListConversions(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range convs {
		net := uah(money.New(c.ToAmount, c.ToCurrency)) - uah(money.New(c.FromAmount, c.FromCurrency))
		add(c.Date, flowConversion, net, c.FromCurrency+" → "+c.ToCurrency)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}

// handleBenchmark — GET /api/benchmark
//
// «А якби я просто тримав долари?» — головне питання українського
// інвестора, і доти відповісти на нього не було з чого: історія курсів
// з'явилась лише коли знецінення почали міряти, а не припускати.
//
// Рахунок простий і навмисно суворий до себе. Кожне ПОПОВНЕННЯ рахунку
// (свої гроші, не купони) переводимо в долари за курсом ТОГО дня; сума —
// це скільки доларів було б, якби ти просто купував їх і не робив
// більше нічого. Оцінюємо сьогоднішнім курсом і кладемо поруч із
// фактичним капіталом.
//
// Бенчмарк НЕ приносить відсотків: це поведінка «нічого не робити», з
// якою й порівнюють. Він може виявитись кращим за портфель — у цьому
// сенс вимірювання, а не привід його ховати.
func (s *Server) handleBenchmark(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rates, err := s.rates(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	nowUSD, _ := fx.RateMajor(money.USD, rates)
	out := struct {
		PortfolioUAH float64 `json:"portfolio_uah"`
		BenchmarkUAH float64 `json:"benchmark_uah"`
		DiffUAH      float64 `json:"diff_uah"`
		DiffPct      float64 `json:"diff_pct"`
		USDBought    float64 `json:"usd_bought"`
		RateNow      float64 `json:"rate_now"`
		Note         string  `json:"note,omitempty"`
	}{RateNow: round2(nowUSD)}

	doc, err := s.buildState(ctx, time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out.PortfolioUAH = round2(doc.NominalUAHEq + doc.AccountUAH + doc.FundsUAH + doc.DepositsUAH)

	if nowUSD <= 0 {
		out.Note = "немає курсу — порівнювати нема з чим"
		writeJSON(w, http.StatusOK, out)
		return
	}
	cash, err := s.st.ListDeposits(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var usdCents int64 // долари ×100
	var missing int
	for _, d := range cash {
		if d.Currency == money.USD {
			// Уже долари — купувати нічого не треба.
			usdCents += d.Amount
			continue
		}
		rate, rerr := s.st.RateOnOrBefore(ctx, d.Currency, d.Date)
		if d.Currency == money.UAH {
			rate, rerr = s.st.RateOnOrBefore(ctx, money.USD, d.Date)
			if rerr != nil || rate <= 0 {
				missing++
				continue
			}
			// Гривня ділиться на курс; знак зберігається, тож зняття
			// зменшує «куплені» долари так само, як і в житті.
			//
			// Через fx.FromUAH, а не цілочисельним діленням на місці:
			// `d.Amount * RateScale / rate` відкидає дробову частину до
			// нуля, тобто систематично применшує бенчмарк на кожному
			// поповненні. FromUAH рахує тим самим big.Rat із банківським
			// заокругленням, що й уся решта конвертацій.
			u, cerr := fx.FromUAH(money.New(d.Amount, money.UAH), money.USD, fx.Rates{money.USD: rate})
			if cerr != nil {
				missing++
				continue
			}
			usdCents += u.Amount()
			continue
		}
		// Інша валюта: спершу в гривню за курсом того дня, потім у долар.
		usdRate, uerr := s.st.RateOnOrBefore(ctx, money.USD, d.Date)
		if rerr != nil || uerr != nil || rate <= 0 || usdRate <= 0 {
			missing++
			continue
		}
		usdCents += d.Amount * rate / usdRate
	}
	out.USDBought = round2(float64(usdCents) / 100)
	out.BenchmarkUAH = round2(float64(usdCents) / 100 * nowUSD)
	out.DiffUAH = round2(out.PortfolioUAH - out.BenchmarkUAH)
	if out.BenchmarkUAH != 0 {
		out.DiffPct = round2(out.DiffUAH / math.Abs(out.BenchmarkUAH) * 100)
	}
	if missing > 0 {
		out.Note = fmt.Sprintf("%d %s без курсу на свою дату — не враховано",
			missing, plural(missing, "рух", "рухи", "рухів"))
	}
	writeJSON(w, http.StatusOK, out)
}

// handleTax — GET /api/tax?year= (або ?from=&to=)
//
// Скільки з доходу забрала держава. Асиметрія між інструментами вже
// зашита в real_pct, але відсотком її не відчуваєш: вклад під 16% і
// папір під 16% — це різні гроші, і рядок «податок з'їв N ₴» каже це
// пряміше за будь-яку ставку.
//
// Купон ОВДП звільнений від податку, дивіденд фонду оподатковується
// (нині 14% = ПДФО 9% + військовий збір 5%), відсотки вкладу теж (19.5%
// = ПДФО 18% + ВЗ 1.5%). Ставки НЕ зашиті: у фонду беремо фактично
// утримане з операції, у вкладу — ставку з самого вкладу.
//
// Період — через taxYear (taxyear.go), спільний із /api/export/csv.
// Доти цей обробник типово брав ковзні дванадцять місяців, а вивантаження
// поруч — календарний рік, і зійтись вони могли хіба випадково.
func (s *Server) handleTax(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	year, from, to, err := taxYear(r.URL.Query(), time.Now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	lots, sales, _, pays, err := s.portfolio(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	statuses, err := s.st.PaymentStatuses(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	today := domain.NewDate(time.Now())
	inWindow := func(d domain.Date) bool { return !d.Before(from) && !d.After(to) }
	arrived := domain.Arrived(statuses, today)

	// Курс НА ДАТУ ПОДІЇ, а не сьогоднішній (fx_asof.go). Доти всі валютні
	// суми переводились одним поточним курсом: на портфелі, де долар
	// купували по 27, а дивляться на нього по 44, податок за минулий рік
	// виходив у півтора раза більшим за реально сплачений.
	asOf := newAsOfRates(s.st)
	var fxErr error
	uah := func(m *money.Money, on domain.Date) int64 {
		v, err := asOf.uah(ctx, m, on)
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

	type line struct {
		Kind     string  `json:"kind"`
		Label    string  `json:"label"`
		GrossUAH float64 `json:"gross_uah"`
		TaxUAH   float64 `json:"tax_uah"`
		NetUAH   float64 `json:"net_uah"`
		RatePct  float64 `json:"rate_pct"`
	}
	var bondGross, fundGross, fundTax, depGross, depTax int64

	// ОВДП: купони. Погашення — повернення власного тіла, не дохід.
	pastCF, err := domain.FuturePayments(pays, lots, sales, "1970-01-01")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	for _, cf := range pastCF {
		if cf.Type == domain.PayRedemption || !inWindow(cf.Date) || !arrived(cf.ISIN, cf.Date) {
			continue
		}
		bondGross += uah(cf.Amount, cf.Date)
	}
	// Фонди: беремо ФАКТИЧНО утримане, а не ставку. Ставка змінювалась і
	// ще змінюватиметься, а у виписці стоїть те, що забрали насправді.
	fundOps, err := s.st.ListFundOps(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	for _, op := range fundOps {
		if op.Kind != domain.FundDividend || !inWindow(op.Date) {
			continue
		}
		fundGross += uah(money.New(op.Amount, op.Currency), op.Date)
		fundTax += uah(money.New(op.Tax, op.Currency), op.Date)
	}
	// Вклади: брутто й податок із того самого проходу, що й самі
	// відсотки — графік показує нетто, і ділити його назад означало б
	// накопичувати похибку.
	termDeposits, err := s.st.ListTermDeposits(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	for _, dep := range termDeposits {
		g, tx := domain.DepositInterestTax(dep, from, to)
		if g == 0 {
			continue
		}
		// Дата події для вкладу — кінець періоду, а не дати окремих
		// нарахувань: DepositInterestTax зводить їх в одну суму за вікно,
		// і розкладати назад заради курсу означало б рахувати відсотки
		// вдруге, іншим способом. Для календарного року це курс 31 грудня,
		// тобто рівно те, чим користуються в декларації.
		depGross += uah(money.New(g, dep.Currency), to)
		depTax += uah(money.New(tx, dep.Currency), to)
	}
	if fxErr != nil {
		writeErr(w, http.StatusInternalServerError, fxErr)
		return
	}

	minor := func(v int64) float64 { return round2(float64(v) / 100) }
	mk := func(kind, label string, gross, tax int64) line {
		l := line{Kind: kind, Label: label,
			GrossUAH: minor(gross), TaxUAH: minor(tax), NetUAH: minor(gross - tax)}
		if gross > 0 {
			l.RatePct = round2(float64(tax) / float64(gross) * 100)
		}
		return l
	}
	out := struct {
		// Year — 0, коли період заданий парою from/to, а не роком. Клієнту
		// це потрібно, щоб не підписувати довільний відрізок роком.
		Year     int     `json:"year,omitempty"`
		From     string  `json:"from"`
		To       string  `json:"to"`
		GrossUAH float64 `json:"gross_uah"`
		TaxUAH   float64 `json:"tax_uah"`
		NetUAH   float64 `json:"net_uah"`
		RatePct  float64 `json:"rate_pct"`
		ByKind   []line  `json:"by_kind,omitempty"`
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
		Credits []line `json:"credits,omitempty"`
		// Звідки взялись гривневі числа. Читач має право знати, що це не
		// сьогоднішній курс, і наскільки в найгіршому разі відстала точка,
		// з якої курс узято: помісячний бекфіл на подіях 2019 року дає
		// відставання до тридцяти днів, і мовчати про це означало б
		// видавати оцінку за факт.
		FXBasis     string `json:"fx_basis"`
		FXMaxLagDay int    `json:"fx_max_lag_days,omitempty"`
		Note        string `json:"note,omitempty"`
	}{
		Year: year, From: string(from), To: string(to),
		FXBasis:     "курс НБУ на дату події або найближчий попередній",
		FXMaxLagDay: asOf.maxLag,
		Note:        asOf.note(),
	}
	for _, l := range []line{
		// Нуль тут — не «податку немає в наших даних», а законодавче
		// звільнення: доходи з ОВДП не оподатковуються ні ПДФО, ні
		// військовим збором. Константа, а не літерал, щоб зміна закону
		// була одним правленням, а не полюванням по файлу.
		mk("bond", "Купони ОВДП", bondGross, bondTaxUAH),
		mk("fund", "Дивіденди фондів", fundGross, fundTax),
		mk("deposit", "Відсотки вкладів", depGross, depTax),
	} {
		if l.GrossUAH != 0 {
			out.ByKind = append(out.ByKind, l)
		}
	}
	gross, tax := bondGross+fundGross+depGross, fundTax+depTax
	out.GrossUAH, out.TaxUAH, out.NetUAH = minor(gross), minor(tax), minor(gross-tax)
	if gross > 0 {
		out.RatePct = round2(float64(tax) / float64(gross) * 100)
	}

	// Податкова знижка на внески в НПФ — лише для КАЛЕНДАРНОГО року: ліміт
	// у неї місячний, а стеля річна, тож на довільному відрізку from/to
	// вона не означала б нічого. Рік 0 (заданий парою дат) знижку не
	// показує — і це чесніше за число, пораховане не за той період.
	if year > 0 {
		if npfAccounts, aerr := s.st.ListNPFAccounts(ctx); aerr == nil && len(npfAccounts) > 0 {
			npfOps, oerr := s.st.ListNPFOps(ctx)
			set := s.loadSettings(ctx)
			if oerr == nil {
				var credit int64
				for _, acc := range npfAccounts {
					credit += int64(npfCreditUAH(acc, npfOps, set, year) * 100)
				}
				if credit > 0 {
					// GrossUAH — сума внесків у межах ліміту, TaxUAH — сама
					// знижка з мінусом: у цьому блоці «податок» і означає
					// рух державі, тож повернення від'ємне.
					out.Credits = append(out.Credits, line{
						Kind: "npf_credit", Label: "Податкова знижка на внески в НПФ",
						TaxUAH: -minor(credit), NetUAH: minor(credit),
					})
					out.Note = strings.TrimSpace(out.Note + " Знижка на внески в НПФ — ОЦІНКА, " +
						"а не факт: її треба отримати декларацією до 31 грудня наступного року, " +
						"вона працює лише проти зарплати й не переноситься. У загальні суми " +
						"звіту не входить.")
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCashflowStatement — GET /api/cashflow?from=&to=
//
// «По операціях не видно, як і куди я перевклав гроші» — це запит на
// звіт про рух, а не на прив'язку купона до покупки. Тут видно казан:
// скільки надійшло доходу, скільки ти доклав своїх і що з цього купив.
func (s *Server) handleCashflowStatement(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	to := domain.Date(q.Get("to"))
	if to == "" {
		to = domain.NewDate(time.Now())
	}
	from := domain.Date(q.Get("from"))
	if from == "" {
		// За замовчуванням — поточний місяць.
		from = domain.Date(string(to)[:8] + "01")
	}

	events, err := s.cashEvents(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type row struct {
		Date  string  `json:"date"`
		Label string  `json:"label"`
		UAH   float64 `json:"uah"`
		Kind  string  `json:"kind"`
	}
	out := struct {
		From        string  `json:"from"`
		To          string  `json:"to"`
		OpeningUAH  float64 `json:"opening_uah"`
		IncomeUAH   float64 `json:"income_uah"`
		ContribUAH  float64 `json:"contributed_uah"`
		PurchaseUAH float64 `json:"purchased_uah"`
		ConvUAH     float64 `json:"conversions_uah"`
		ClosingUAH  float64 `json:"closing_uah"`
		Rows        []row   `json:"rows,omitempty"`
	}{From: string(from), To: string(to)}

	var opening, income, contrib, purchase, conv int64
	for _, e := range events {
		if e.Date.Before(from) {
			opening += e.UAH
			continue
		}
		if e.Date.After(to) {
			continue
		}
		switch e.Kind {
		case flowIncome:
			income += e.UAH
		case flowContribution:
			contrib += e.UAH
		case flowPurchase:
			purchase += e.UAH
		case flowConversion:
			conv += e.UAH
		}
		out.Rows = append(out.Rows, row{
			Date: string(e.Date), Label: e.Label,
			UAH: round2(float64(e.UAH) / 100), Kind: e.Kind,
		})
	}
	minor := func(v int64) float64 { return round2(float64(v) / 100) }
	out.OpeningUAH = minor(opening)
	out.IncomeUAH = minor(income)
	out.ContribUAH = minor(contrib)
	// Покупки віддаємо ДОДАТНИМИ: у звіті вони віднімаються, і мінус на
	// мінусі читався б як помилка.
	out.PurchaseUAH = minor(-purchase)
	out.ConvUAH = minor(conv)
	out.ClosingUAH = minor(opening + income + contrib + purchase + conv)
	writeJSON(w, http.StatusOK, out)
}
