// Package api — HTTP-сервер: REST + вбудований веб-UI.
package api

import (
	"context"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/fx"
	"github.com/ODDsama/oddinvest/internal/state"
	"github.com/ODDsama/oddinvest/internal/store"
)

//go:embed web
var webFS embed.FS

// Refresher — те, що вміє фонова частина (оновити довідник/курси і
// републікувати стан). Реалізується в jobs, сюди приходить інтерфейсом.
type Refresher interface {
	RefreshAll(ctx context.Context) error
	PublishState(ctx context.Context) error
}

type Server struct {
	st  *store.Store
	ref Refresher
	log *slog.Logger
}

func New(st *store.Store, ref Refresher, log *slog.Logger) *Server {
	return &Server{st: st, ref: ref, log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/summary", s.handleSummary)
	mux.HandleFunc("GET /api/positions", s.handlePositions)
	mux.HandleFunc("GET /api/calendar", s.handleCalendar)
	mux.HandleFunc("GET /api/ladder", s.handleLadder)
	mux.HandleFunc("GET /api/lots", s.handleListLots)
	mux.HandleFunc("POST /api/lots", s.handleAddLot)
	mux.HandleFunc("DELETE /api/lots/{id}", s.handleDeleteLot)
	mux.HandleFunc("POST /api/sales", s.handleAddSale)
	mux.HandleFunc("GET /api/sales", s.handleListSales)
	mux.HandleFunc("GET /api/deposits", s.handleListDeposits)
	mux.HandleFunc("POST /api/deposits", s.handleAddDeposit)
	mux.HandleFunc("DELETE /api/deposits/{id}", s.handleDeleteDeposit)
	mux.HandleFunc("GET /api/conversions", s.handleListConversions)
	mux.HandleFunc("POST /api/conversions", s.handleAddConversion)
	mux.HandleFunc("DELETE /api/conversions/{id}", s.handleDeleteConversion)
	mux.HandleFunc("GET /api/bonds/search", s.handleSearchBonds)
	mux.HandleFunc("GET /api/bonds/{isin}", s.handleGetBond)
	mux.HandleFunc("GET /api/accrued/{isin}", s.handleAccrued)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handlePutSettings)
	mux.HandleFunc("POST /api/payments/status", s.handlePaymentStatus)
	mux.HandleFunc("POST /api/refresh", s.handleRefresh)
	mux.HandleFunc("GET /api/xirr", s.handleXIRR)
	mux.HandleFunc("GET /api/reinvest", s.handleReinvest)
	mux.HandleFunc("GET /api/snapshots", s.handleSnapshots)
	mux.HandleFunc("GET /api/export/csv", s.handleExportCSV)
	mux.HandleFunc("GET /api/backup", s.handleBackupExport)
	mux.HandleFunc("POST /api/restore", s.handleBackupImport)

	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", http.FileServerFS(sub))
	return logMiddleware(s.log, mux)
}

func logMiddleware(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Info("http", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start))
	})
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

type moneyJSON struct {
	Amount   string `json:"amount"` // десятковий рядок "995.00"
	Currency string `json:"currency"`
}

func toMoneyJSON(m *money.Money) moneyJSON {
	if m == nil {
		return moneyJSON{Amount: "0", Currency: money.UAH}
	}
	minor := m.Amount()
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	return moneyJSON{
		Amount:   fmt.Sprintf("%s%d.%02d", sign, minor/100, minor%100),
		Currency: m.Currency().Code,
	}
}

func parseMoney(amount, currency string) (*money.Money, error) {
	minor, err := domain.ParseDecimalToMinor(amount, currency)
	if err != nil {
		return nil, err
	}
	return money.New(minor, currency), nil
}

// portfolio — все, що треба домену, одним заходом.
func (s *Server) portfolio(ctx context.Context) (lots []domain.Lot, sales []domain.Sale,
	bonds map[string]domain.Bond, pays []domain.Payment, err error) {
	lots, err = s.st.ListLots(ctx)
	if err != nil {
		return
	}
	sales, err = s.st.ListSales(ctx)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	var isins []string
	for _, l := range lots {
		if !seen[l.ISIN] {
			seen[l.ISIN] = true
			isins = append(isins, l.ISIN)
		}
	}
	bonds, err = s.st.BondsFor(ctx, isins)
	if err != nil {
		return
	}
	pays, err = s.st.PaymentsFor(ctx, isins)
	return
}

func (s *Server) rates(ctx context.Context) (fx.Rates, error) {
	r := fx.Rates{}
	for _, code := range []string{"USD", "EUR"} {
		v, err := s.st.LatestRate(ctx, code)
		if err != nil {
			return nil, err
		}
		if v > 0 {
			r[code] = v
		}
	}
	return r, nil
}

// --- handlers ---

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	doc, err := s.buildState(r.Context(), time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// nbuRefreshedKey — час останнього успішного оновлення довідника НБУ.
// Пишеться джобою (не через PUT /api/settings), тож у settingsKeys нема.
const nbuRefreshedKey = "nbu_refreshed_at"

// defaultDevaluationPct — очікуване річне знецінення гривні до твердої
// валюти, коли користувач не задав своє. 6% — приблизний темп 2016-2025
// (26 -> 42 ₴/$ за 9 років), тобто без урахування шоків 2014 і 2022.
// Свідомо не «історичне середнє з 2014»: воно дало б ~16% і зробило б
// будь-який гривневий папір безнадійним.
const defaultDevaluationPct = 6.0

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
	positions, err := domain.Positions(bonds, pays, lots, sales, today)
	if err != nil {
		return nil, err
	}
	cashflow, err := domain.FuturePayments(pays, lots, sales, today)
	if err != nil {
		return nil, err
	}
	ladder := domain.Ladder(bonds, lots, sales, today)

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

	targetStr, _ := s.st.GetSetting(ctx, "monthly_target_uah")
	target := money.New(0, money.UAH)
	if targetStr != "" {
		if t, err := parseMoney(targetStr, money.UAH); err == nil {
			target = t
		}
	}

	// неперевкладені: минулі виплати без статусу reinvested
	statuses, err := s.st.PaymentStatuses(ctx)
	if err != nil {
		return nil, err
	}
	pastCF, err := domain.FuturePayments(pays, lots, sales, "1970-01-01")
	if err != nil {
		return nil, err
	}
	unin := money.New(0, money.UAH)
	bal := map[string]int64{} // валюта -> мінорні (нативно): баланс рахунку
	for _, cf := range pastCF {
		if cf.Date.After(today) || cf.Date == today {
			continue
		}
		// отримана виплата кредитує рахунок у своїй валюті
		bal[cf.Amount.Currency().Code] += cf.Amount.Amount()
		uahAmt, err := fx.ToUAH(cf.Amount, rates)
		if err != nil {
			return nil, err
		}
		if statuses[cf.ISIN+"|"+string(cf.Date)] == "reinvested" {
			continue
		}
		if unin, err = unin.Add(uahAmt); err != nil {
			return nil, err
		}
	}

	// --- грошові рахунки: (брокер × валюта) ---
	// Рахунки роздільні: гривня в mono не купить папір в inzhur, тож
	// баланс ведемо по кожному брокеру окремо. `bal` лишається зведеним
	// по валютах — його використовують портфельні показники.
	// Формула однакова: Σ поповнень + Σ конвертацій + Σ отриманих виплат −
	// Σ вартості лотів (усе нативно, у своїй валюті).
	balBC := map[store.BrokerCur]int64{}

	// купон кредитує рахунок ТОГО брокера, де куплено папір
	for _, p := range pays {
		if p.PayDate.After(today) || p.PayDate == today {
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

	// Дохід (купони+погашення) по місяцях на рік наперед, грн-екв.
	incByMonth := map[string]float64{}
	for _, cf := range cashflow {
		if u, err := fx.ToUAH(cf.Amount, rates); err == nil {
			incByMonth[fmt.Sprintf("%04d-%02d", cf.Date.Year(), int(cf.Date.Month()))] += float64(u.Amount()) / 100
		}
	}
	income12m := make([]state.MonthAmount, 0, 12)
	for i := 0; i < 12; i++ {
		t := today.Time().AddDate(0, i, 0)
		key := fmt.Sprintf("%04d-%02d", t.Year(), int(t.Month()))
		income12m = append(income12m, state.MonthAmount{Month: key, Amount: round2(incByMonth[key])})
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

	// найдешевший папір по валютах (нативно) + мінімум у грн-екв.
	minNoms, err := s.st.MinNominalByCurrency(ctx)
	if err != nil {
		return nil, err
	}
	reinvestMinByCur := map[string]float64{}
	reinvestMin := money.New(0, money.UAH)
	for cur, minNom := range minNoms {
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
	if raw, _ := s.st.GetSetting(ctx, "usd_target_share_pct"); raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			settings.USDTargetSharePct = &f
		}
	}
	if raw, _ := s.st.GetSetting(ctx, "eur_target_share_pct"); raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			settings.EURTargetSharePct = &f
		}
	}
	if raw, _ := s.st.GetSetting(ctx, "assumed_rate_pct"); raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			settings.AssumedRatePct = &f
		}
	}
	if raw, _ := s.st.GetSetting(ctx, "target_duration_years"); raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			settings.TargetDurationYears = &f
		}
	}
	if raw, _ := s.st.GetSetting(ctx, "channels"); raw != "" {
		settings.Channels = raw
	}
	if raw, _ := s.st.GetSetting(ctx, "reinvest_rank"); raw != "" {
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
	} {
		if raw, _ := s.st.GetSetting(ctx, g.key); raw != "" {
			if f, err := strconv.ParseFloat(raw, 64); err == nil {
				*g.dst = &f
			}
		}
	}
	if raw, _ := s.st.GetSetting(ctx, "goal_amount_uah"); raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			settings.GoalAmountUAH = &f
		}
	}
	if raw, _ := s.st.GetSetting(ctx, "goal_date"); raw != "" {
		settings.GoalDate = raw
	}

	xirr := map[string]float64{}
	for _, cur := range []string{money.UAH, money.USD, money.EUR} {
		flows, err := domain.PortfolioFlows(bonds, pays, lots, sales, cur, today)
		if err != nil || len(flows) < 2 {
			continue
		}
		// ануалізація на коротких горизонтах дає сміттєві сотні відсотків;
		// не показуємо XIRR, поки історії менше 30 днів
		if domain.DaysBetween(flows[0].Date, today) < 30 {
			continue
		}
		// навіть >30 днів нерівномірні потоки дають артефакти (сотні %);
		// реалізована дохідність портфеля ОВДП поза смугою -95%..+100%
		// — це шум ануалізації, а не сигнал, тож не публікуємо.
		if r, err := domain.XIRR(flows); err == nil && r <= 1.0 && r >= -0.95 {
			xirr[cur] = math.Round(r*10000) / 100 // частка -> %, 2 знаки
		}
	}

	// Очікувана дохідність за придбаними паперами: річний купон ÷ номінал,
	// зважено по валютах у грн-екв. Це орієнтир для проєкцій замість
	// ручного вводу. На відміну від auk_proc (дохідність розміщення)
	// відображає, що папери реально приносять.
	//
	// Річний купон беремо зі СТАВКИ паперу, а не з графіка виплат за
	// наступні 365 днів. Вікно в рік обрізало папери, що гасяться раніше:
	// у чисельник ішов один залишковий купон замість двох річних, а в
	// знаменник — повний номінал, і дохідність виходила заниженою тим
	// сильніше, чим ближче погашення.
	var couponUAH, nominalUAH int64
	couponByCur := map[string]int64{}  // річний купон нативно по валютах
	nominalByCur := map[string]int64{} // номінал нативно по валютах
	for _, l := range lots {
		b, ok := bonds[l.ISIN]
		if !ok || b.Maturity.Before(today) {
			continue
		}
		q := domain.RemainingQtyNow(l, sales)
		if q == 0 {
			continue
		}
		// ставка в базисних пунктах: 16.55% -> 1655
		annualCoupon := b.Nominal.Amount() * b.RateBP / 10000
		cur := b.Nominal.Currency().Code
		couponByCur[cur] += annualCoupon * q
		nominalByCur[cur] += b.Nominal.Amount() * q
		if c, err := fx.ToUAH(money.New(annualCoupon*q, cur), rates); err == nil {
			couponUAH += c.Amount()
		}
		if n, err := fx.ToUAH(money.New(b.Nominal.Amount()*q, cur), rates); err == nil {
			nominalUAH += n.Amount()
		}
	}
	var portfolioYield float64
	if nominalUAH > 0 {
		portfolioYield = math.Round(float64(couponUAH)/float64(nominalUAH)*10000) / 100
	}
	portfolioYieldByCur := map[string]float64{}
	for cur, nom := range nominalByCur {
		if nom > 0 {
			portfolioYieldByCur[cur] = math.Round(float64(couponByCur[cur])/float64(nom)*10000) / 100
		}
	}

	// --- фактичний темп поповнень ---
	// План може розходитись із реальністю, тож рахуємо ще й середній темп
	// НОВИХ грошей. Саме поповнень, а не покупок: покупка лише переносить
	// гроші з рахунку в папери й нового капіталу не додає (а купони вже
	// враховані окремо). Потрібно ≥60 днів історії — інакше середнє від
	// стартового внеску дає безглузді сотні тисяч на місяць.
	var actualMonthly float64
	var actualMonths int
	if deps, derr := s.st.ListDeposits(ctx); derr == nil && len(deps) > 0 {
		first := deps[0].Date
		var totalUAH int64
		for _, d := range deps {
			if d.Date.Before(first) {
				first = d.Date
			}
			if d.Amount <= 0 {
				continue // зняття — не внесок
			}
			if u, cerr := fx.ToUAH(money.New(d.Amount, d.Currency), rates); cerr == nil {
				totalUAH += u.Amount()
			}
		}
		if days := domain.DaysBetween(first, today); days >= 60 {
			months := float64(days) / 30.44
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
	capRate := portfolioYield
	if capRate > 40 {
		capRate = 40 // стеля, щоб компаунд не вибухав
	}
	contribM := 0.0
	if !target.IsZero() {
		contribM = float64(target.Amount()) / 100
	}

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
	avgRate, _ := s.st.AvgRateByCurrency(ctx, today)

	// Річне знецінення гривні. Одне число в налаштуваннях, від якого
	// сценарії розходяться — як і ставка.
	devalBase := defaultDevaluationPct
	if settings.UAHDevaluationPct != nil && *settings.UAHDevaluationPct >= 0 {
		devalBase = *settings.UAHDevaluationPct
	}
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
				Threshold:       reinvestMinByCur[cur], Coupon: couponByCurMonth[cur],
				Redeem:          redeemByCurMonth[cur], ContribUAH: contrib, Rate0: rate0,
			})
		}
		return out
	}

	rate0USD := 0.0
	if u, err := fx.ToUAH(money.New(100, money.USD), rates); err == nil {
		rate0USD = float64(u.Amount()) / 100
	}

	p0 := float64(accountUAHMinor+nominalUAH) / 100
	projection := make([]state.ProjectionRow, 0, 4)
	for _, y := range []int{1, 3, 5, 10} {
		m := y * 12
		row := state.ProjectionRow{
			Years:       y,
			// Обидві колонки — у сьогоднішніх гривнях, інакше таблиця
			// віднімала б номінальні гроші від реальних і на коротких
			// горизонтах показувала б від'ємний приріст.
			Contributed: round2(domain.RealContributed(p0, contribM, devalBase, m)),
			WithReinvest: round2(domain.ProjectSleeves(
				buildSleeves(contribM, 0), devalBase, m).TodayUAH),
		}
		if actualMonthly > 0 {
			row.WithReinvestActual = round2(domain.ProjectSleeves(
				buildSleeves(actualMonthly, 0), devalBase, m).TodayUAH)
		}
		projection = append(projection, row)
	}

	// --- віяло прогнозів на дедлайн ---
	//
	// Ціль — ОДНА сума-орієнтир. Три сценарії відрізняються не сумами, а
	// допущеннями: темпом поповнень і ставкою реінвесту. Дата в усіх одна
	// — дедлайн, тож суми між собою порівнянні.
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

	const goalHorizonMonths = 720 // 60 років — далі вважаємо недосяжним
	var forecast *state.Forecast
	if deadlineMonths > 0 {
		// Внесок: якщо фактичний темп уже відомий, беремо його і план як
		// нижню й верхню межу — це РЕАЛЬНИЙ розкид, а не вигаданий
		// відсоток. Поки історії замало, внесок в усіх сценаріях
		// однаковий і розходяться вони лише ставкою та девальвацією.
		contribLo, contribHi := contribM, contribM
		if actualMonthly > 0 {
			contribLo = math.Min(contribM, actualMonthly)
			contribHi = math.Max(contribM, actualMonthly)
		}
		const rateSpreadPP = 3.0  // ± п.п. до ставки реінвесту
		const devalSpreadPP = 4.0 // ± п.п. до знецінення гривні
		defs := []struct {
			key, label            string
			contrib, ratePP, deval float64
		}{
			{"optimistic", "Оптимістично", contribHi, rateSpreadPP, math.Max(0, devalBase-devalSpreadPP)},
			{"realistic", "Реалістично", contribM, 0, devalBase},
			{"pessimistic", "Песимістично", contribLo, -rateSpreadPP, devalBase + devalSpreadPP},
		}
		f := &state.Forecast{
			Date:        string(domain.NewDate(today.Time().AddDate(0, deadlineMonths, 0))),
			Months:      deadlineMonths,
			GoalAmount:  goalAmount,
			ContribPlan: round2(contribM),
			Rate0USD:    round2(rate0USD),
			GlideYears:  glideYears,
		}
		realisticAmount := 0.0
		for _, d := range defs {
			sl := buildSleeves(d.contrib, d.ratePP)
			res := domain.ProjectSleeves(sl, d.deval, deadlineMonths)
			row := state.ForecastRow{Key: d.key, Label: d.label,
				Amount: round2(res.TodayUAH), AmountNominal: round2(res.NominalUAH),
				ContribMonthly: round2(d.contrib), DevaluationPct: round2(d.deval)}
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
			if d.key == "realistic" {
				realisticAmount = res.TodayUAH
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
		// Потрібний внесок рахуємо за РЕАЛІСТИЧНИМИ допущеннями: більше
		// відкладати — це рішення, а вища дохідність чи слабша девальвація
		// — ні.
		if goalAmount > 0 && realisticAmount < goalAmount {
			f.RequiredMonthly = round2(domain.RequiredMonthlySleeves(
				buildSleeves(contribM, 0), devalBase, goalAmount, deadlineMonths))
		}
		forecast = f
	}


	nbuAt, _ := s.st.GetSetting(ctx, nbuRefreshedKey)

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
		bondCostNative := float64(minNoms[cur]) / 100
		bondCostUAH := bondCostNative * rateMajor
		var canBuy int64
		convertUAH := 0.0
		if bondCostNative > 0 {
			canBuy = int64(cashNative / bondCostNative)
			if cashNative < bondCostNative {
				convertUAH = (bondCostNative - cashNative) * rateMajor
			}
		}
		rebalance = append(rebalance, state.RebalanceRow{
			Currency: cur, TargetPct: *tp, CurrentPct: round2(currentPct),
			DeficitUAH: round2(deficitUAH), DeficitNative: round2(deficitUAH / rateMajor),
			CashNative: round2(cashNative), BondCostNative: round2(bondCostNative),
			BondCostUAH: round2(bondCostUAH), CanBuy: canBuy, ConvertUAH: round2(convertUAH),
			MinPortfolioUAH: round2(bondCostUAH / (*tp / 100)),
			Feasible:        bondCostUAH > 0 && bondCostUAH <= targetUAH,
		})
	}

	// --- процентний ризик: дюрація за реальним графіком виплат ---
	ptsByCur := map[string][]domain.CashPoint{}
	for _, cf := range cashflow {
		yrs := float64(domain.DaysBetween(today, cf.Date)) / 365.0
		if yrs < 0 {
			continue
		}
		c := cf.Amount.Currency().Code
		ptsByCur[c] = append(ptsByCur[c], domain.CashPoint{Years: yrs, Amount: float64(cf.Amount.Amount()) / 100})
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

	return state.Build(state.Input{
		Now: now, Positions: positions, Cashflow: cashflow, Ladder: ladder,
		Rates: rates, MonthInvestedUAH: monthInv, MonthTargetUAH: target,
		UninvestedUAH: unin, AccountUAH: account, ReinvestMinUAH: reinvestMin,
		Accounts: accounts, Brokers: brokers, InvestedByBroker: investedByBroker,
		LadderUAH: ladderUAH, Income12m: income12m,
		ReinvestMinByCur: reinvestMinByCur, TopN: 5,
		Settings: settings, XIRRPct: xirr, PortfolioYieldPct: portfolioYield,
		PortfolioYield: portfolioYieldByCur,
		Projection: projection, ProjectionRatePct: capRate, Forecast: forecast,
		Rebalance: rebalance, RateRisk: rateRisk,
		AccruedUAH: round2(float64(accruedUAH) / 100), NBURefreshedAt: nbuAt,
		ActualMonthlyUAH: actualMonthly, ActualMonths: actualMonths,
	})
}

func (s *Server) handlePositions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lots, sales, bonds, pays, err := s.portfolio(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	pos, err := domain.Positions(bonds, pays, lots, sales, domain.NewDate(time.Now()))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type posJSON struct {
		ISIN      string    `json:"isin"`
		Currency  string    `json:"currency"`
		Qty       int64     `json:"qty"`
		Invested  moneyJSON `json:"invested"`
		Nominal   moneyJSON `json:"nominal"`
		Maturity  string    `json:"maturity"`
		DaysToMat int       `json:"days_to_maturity"`
		NextDate  string    `json:"next_pay_date,omitempty"`
		NextAmt   moneyJSON `json:"next_pay_amount"`
	}
	out := make([]posJSON, 0, len(pos))
	for _, p := range pos {
		out = append(out, posJSON{p.ISIN, p.Currency, p.Qty, toMoneyJSON(p.Invested),
			toMoneyJSON(p.Nominal), string(p.Maturity), p.DaysToMat,
			string(p.NextPayDate), toMoneyJSON(p.NextPayAmt)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCalendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lots, sales, _, pays, err := s.portfolio(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	from := domain.NewDate(time.Now())
	if q := r.URL.Query().Get("from"); q != "" {
		if d, err := domain.ParseDate(q); err == nil {
			from = d
		}
	}
	cf, err := domain.FuturePayments(pays, lots, sales, from)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	statuses, _ := s.st.PaymentStatuses(ctx)
	type cfJSON struct {
		Date   string    `json:"date"`
		ISIN   string    `json:"isin"`
		Type   int       `json:"type"`
		Amount moneyJSON `json:"amount"`
		Status string    `json:"status,omitempty"`
	}
	out := make([]cfJSON, 0, len(cf))
	for _, item := range cf {
		out = append(out, cfJSON{string(item.Date), item.ISIN, int(item.Type),
			toMoneyJSON(item.Amount), statuses[item.ISIN+"|"+string(item.Date)]})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleLadder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lots, sales, bonds, _, err := s.portfolio(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, domain.Ladder(bonds, lots, sales, domain.NewDate(time.Now())))
}

type lotReq struct {
	ISIN     string `json:"isin"`
	Qty      int64  `json:"qty"`
	Price    string `json:"price_per_bond"` // десятковий рядок
	Currency string `json:"currency"`
	Fee      string `json:"fee"` // сумарна комісія за лот, десятковий рядок; опційно
	BuyDate  string `json:"buy_date"`
	Channel  string `json:"channel"`
	Note     string `json:"note"`
}

func (s *Server) handleAddLot(w http.ResponseWriter, r *http.Request) {
	var req lotReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Qty <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("qty має бути > 0"))
		return
	}
	bd, err := domain.ParseDate(req.BuyDate)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	cur := req.Currency
	if cur == "" { // валюту беремо з довідника, якщо папір відомий
		if b, _ := s.st.GetBond(r.Context(), req.ISIN); b != nil {
			cur = b.Nominal.Currency().Code
		} else {
			writeErr(w, http.StatusBadRequest, errors.New("папір не в довіднику — вкажіть currency явно"))
			return
		}
	}
	price, err := parseMoney(req.Price, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var fee *money.Money
	if strings.TrimSpace(req.Fee) != "" {
		fee, err = parseMoney(req.Fee, cur)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	id, err := s.st.AddLot(r.Context(), domain.Lot{
		ISIN: req.ISIN, Qty: req.Qty, PricePerBond: price, Fee: fee,
		BuyDate: bd, Channel: req.Channel, Note: req.Note,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleDeleteLot(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteLot(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListLots(w http.ResponseWriter, r *http.Request) {
	lots, err := s.st.ListLots(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	sales, err := s.st.ListSales(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type lotJSON struct {
		ID        int64     `json:"id"`
		ISIN      string    `json:"isin"`
		Qty       int64     `json:"qty"`
		Remaining int64     `json:"remaining"`
		Price     moneyJSON `json:"price_per_bond"`
		Fee       moneyJSON `json:"fee"`
		BuyDate   string    `json:"buy_date"`
		Channel   string    `json:"channel"`
		Note      string    `json:"note"`
	}
	out := make([]lotJSON, 0, len(lots))
	for _, l := range lots {
		out = append(out, lotJSON{l.ID, l.ISIN, l.Qty, domain.RemainingQtyNow(l, sales),
			toMoneyJSON(l.PricePerBond), toMoneyJSON(l.Fee), string(l.BuyDate), l.Channel, l.Note})
	}
	writeJSON(w, http.StatusOK, out)
}

type saleReq struct {
	LotID    int64  `json:"lot_id"`
	SaleDate string `json:"sale_date"`
	Qty      int64  `json:"qty"`
	Clean    string `json:"clean_per_bond"`
	Accrued  string `json:"accrued"` // сумарний НКД, опційно
	Currency string `json:"currency"`
	Note     string `json:"note"`
}

func (s *Server) handleAddSale(w http.ResponseWriter, r *http.Request) {
	var req saleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sd, err := domain.ParseDate(req.SaleDate)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	clean, err := parseMoney(req.Clean, req.Currency)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	accrued := money.New(0, req.Currency)
	if req.Accrued != "" {
		if accrued, err = parseMoney(req.Accrued, req.Currency); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	id, err := s.st.AddSale(r.Context(), domain.Sale{
		LotID: req.LotID, SaleDate: sd, Qty: req.Qty,
		CleanPerBond: clean, Accrued: accrued, Note: req.Note,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleListSales(w http.ResponseWriter, r *http.Request) {
	sales, err := s.st.ListSales(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	lots, err := s.st.ListLots(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	lotByID := map[int64]domain.Lot{}
	isins := []string{}
	for _, l := range lots {
		lotByID[l.ID] = l
		isins = append(isins, l.ISIN)
	}
	pays, err := s.st.PaymentsFor(r.Context(), isins)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type saleJSON struct {
		ID       int64     `json:"id"`
		LotID    int64     `json:"lot_id"`
		ISIN     string    `json:"isin"`
		SaleDate string    `json:"sale_date"`
		Qty      int64     `json:"qty"`
		Clean    moneyJSON `json:"clean_per_bond"`
		Accrued  moneyJSON `json:"accrued"`
		Result   moneyJSON `json:"realized_result"`
	}
	out := make([]saleJSON, 0, len(sales))
	for _, sl := range sales {
		lot := lotByID[sl.LotID]
		res, err := domain.RealizedResult(lot, sl, pays)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		out = append(out, saleJSON{sl.ID, sl.LotID, lot.ISIN, string(sl.SaleDate),
			sl.Qty, toMoneyJSON(sl.CleanPerBond), toMoneyJSON(sl.Accrued), toMoneyJSON(res)})
	}
	writeJSON(w, http.StatusOK, out)
}

type depositReq struct {
	Date     string `json:"date"`
	Amount   string `json:"amount"` // десятковий; + поповнення, − зняття
	Currency string `json:"currency"`
	Broker   string `json:"broker"`
	Note     string `json:"note"`
}

func (s *Server) handleAddDeposit(w http.ResponseWriter, r *http.Request) {
	var req depositReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d := domain.NewDate(time.Now())
	if req.Date != "" {
		var err error
		if d, err = domain.ParseDate(req.Date); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	cur := req.Currency
	if cur == "" {
		cur = money.UAH
	}
	minor, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddDeposit(r.Context(), store.Deposit{
		Date: d, Amount: minor, Currency: cur, Broker: strings.TrimSpace(req.Broker), Note: req.Note})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleDeleteDeposit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteDeposit(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListDeposits(w http.ResponseWriter, r *http.Request) {
	deps, err := s.st.ListDeposits(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type depJSON struct {
		ID     int64     `json:"id"`
		Date   string    `json:"date"`
		Amount moneyJSON `json:"amount"`
		Broker string    `json:"broker"`
		Note   string    `json:"note"`
	}
	out := make([]depJSON, 0, len(deps))
	for _, d := range deps {
		out = append(out, depJSON{d.ID, string(d.Date),
			toMoneyJSON(money.New(d.Amount, d.Currency)), d.Broker, d.Note})
	}
	writeJSON(w, http.StatusOK, out)
}

type conversionReq struct {
	Date         string `json:"date"`
	FromCurrency string `json:"from_currency"`
	FromAmount   string `json:"from_amount"`
	ToCurrency   string `json:"to_currency"`
	ToAmount     string `json:"to_amount"`
	Broker       string `json:"broker"`
	Note         string `json:"note"`
}

func (s *Server) handleAddConversion(w http.ResponseWriter, r *http.Request) {
	var req conversionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d := domain.NewDate(time.Now())
	if req.Date != "" {
		var err error
		if d, err = domain.ParseDate(req.Date); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	if req.FromCurrency == req.ToCurrency {
		writeErr(w, http.StatusBadRequest, errors.New("валюти конвертації мають відрізнятись"))
		return
	}
	fromMinor, err := domain.ParseDecimalToMinor(req.FromAmount, req.FromCurrency)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	toMinor, err := domain.ParseDecimalToMinor(req.ToAmount, req.ToCurrency)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if fromMinor <= 0 || toMinor <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("суми конвертації мають бути > 0"))
		return
	}
	id, err := s.st.AddConversion(r.Context(), store.Conversion{
		Date: d, FromCurrency: req.FromCurrency, FromAmount: fromMinor,
		ToCurrency: req.ToCurrency, ToAmount: toMinor,
		Broker: strings.TrimSpace(req.Broker), Note: req.Note,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleDeleteConversion(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteConversion(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListConversions(w http.ResponseWriter, r *http.Request) {
	convs, err := s.st.ListConversions(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type convJSON struct {
		ID     int64     `json:"id"`
		Date   string    `json:"date"`
		From   moneyJSON `json:"from"`
		To     moneyJSON `json:"to"`
		Broker string    `json:"broker"`
		Note   string    `json:"note"`
	}
	out := make([]convJSON, 0, len(convs))
	for _, c := range convs {
		out = append(out, convJSON{c.ID, string(c.Date),
			toMoneyJSON(money.New(c.FromAmount, c.FromCurrency)),
			toMoneyJSON(money.New(c.ToAmount, c.ToCurrency)), c.Broker, c.Note})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSearchBonds(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bonds, err := s.st.SearchBonds(r.Context(), q.Get("q"), q.Get("currency"),
		domain.Date(q.Get("maturity_from")), domain.Date(q.Get("maturity_to")), 50)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, bondsJSON(bonds))
}

func (s *Server) handleGetBond(w http.ResponseWriter, r *http.Request) {
	b, err := s.st.GetBond(r.Context(), r.PathValue("isin"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if b == nil {
		writeErr(w, http.StatusNotFound, errors.New("папір не знайдено в довіднику"))
		return
	}
	writeJSON(w, http.StatusOK, bondsJSON([]domain.Bond{*b})[0])
}

func (s *Server) handleAccrued(w http.ResponseWriter, r *http.Request) {
	isin := r.PathValue("isin")
	on := domain.NewDate(time.Now())
	if q := r.URL.Query().Get("on"); q != "" {
		var err error
		if on, err = domain.ParseDate(q); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	pays, err := s.st.PaymentsFor(r.Context(), []string{isin})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	acc, err := domain.EstimateAccrued(pays, isin, on)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"isin": isin, "on": string(on), "per_bond": toMoneyJSON(acc),
		"note": "оцінка ACT/ACT; фактичний НКД може відрізнятись",
	})
}

func bondsJSON(bonds []domain.Bond) []map[string]any {
	out := make([]map[string]any, 0, len(bonds))
	for _, b := range bonds {
		out = append(out, map[string]any{
			"isin": b.ISIN, "nominal": toMoneyJSON(b.Nominal),
			"rate_pct": fmt.Sprintf("%d.%02d", b.RateBP/100, b.RateBP%100),
			"maturity": string(b.Maturity), "descr": b.Descr,
		})
	}
	return out
}

var settingsKeys = []string{"monthly_target_uah", "usd_target_share_pct", "eur_target_share_pct",
	"assumed_rate_pct", "goal_amount_uah", "goal_date", "target_duration_years", "channels",
	"reinvest_rank", "goal_pessimistic_uah", "goal_realistic_uah", "goal_optimistic_uah",
	"uah_devaluation_pct", "terminal_rate_pct", "rate_glide_years"}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{}
	for _, k := range settingsKeys {
		v, err := s.st.GetSetting(r.Context(), k)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		out[k] = v
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	allowed := map[string]bool{}
	for _, k := range settingsKeys {
		allowed[k] = true
	}
	for k, v := range req {
		if !allowed[k] {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("невідомий ключ %q", k))
			return
		}
		if err := s.st.SetSetting(r.Context(), k, v); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePaymentStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ISIN    string `json:"isin"`
		PayDate string `json:"pay_date"`
		Status  string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d, err := domain.ParseDate(req.PayDate)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.SetPaymentStatus(r.Context(), req.ISIN, d, req.Status); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if s.ref == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("refresher недоступний"))
		return
	}
	if err := s.ref.RefreshAll(r.Context()); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleBackupExport віддає повний бекап користувацьких даних як файл.
func (s *Server) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	b, err := s.st.ExportAll(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	b.ExportedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	fname := "oddinvest-backup-" + time.Now().Format("2006-01-02") + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+fname+`"`)
	w.Write(data)
}

// handleBackupImport ЗАМІНЮЄ всі користувацькі дані вмістом бекапу.
func (s *Server) handleBackupImport(w http.ResponseWriter, r *http.Request) {
	var b store.Backup
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("не схоже на бекап: %w", err))
		return
	}
	if err := s.st.ImportAll(r.Context(), &b); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusOK, map[string]any{
		"restored": map[string]int{
			"lots": len(b.Lots), "sales": len(b.Sales), "deposits": len(b.Deposits),
			"conversions": len(b.Conversions), "settings": len(b.Settings),
			"payment_status": len(b.PaymentStatus), "snapshots": len(b.Snapshots),
		},
	})
}

func (s *Server) publishAsync() {
	if s.ref == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.ref.PublishState(ctx); err != nil {
			s.log.Error("публікація стану", "err", err)
		}
	}()
}

func (s *Server) handleXIRR(w http.ResponseWriter, r *http.Request) {
	doc, err := s.buildState(r.Context(), time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"xirr_pct": doc.XIRRPct,
		"note":     "залишок оцінено за номіналом; по валютах окремо, без конвертації",
	})
}

// handleReinvest — помічник реінвестиції: доступні папери з довідника,
// відранжовані під план (валюта, яку треба добрати до цільової частки →
// рік з «діркою» в драбині → купонна ставка). Це інструмент, не порада.
func (s *Server) handleReinvest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	today := domain.NewDate(now)
	doc, err := s.buildState(ctx, now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	bonds, err := s.st.SearchBonds(ctx, "", "", today, "", 5000) // усі майбутні папери
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	target := map[string]float64{"USD": 0, "EUR": 0}
	if doc.Settings != nil {
		if doc.Settings.USDTargetSharePct != nil {
			target["USD"] = *doc.Settings.USDTargetSharePct
		}
		if doc.Settings.EURTargetSharePct != nil {
			target["EUR"] = *doc.Settings.EURTargetSharePct
		}
	}
	target["UAH"] = 100 - target["USD"] - target["EUR"]
	cur := map[string]float64{"USD": doc.USDSharePct, "EUR": doc.EURSharePct}
	cur["UAH"] = 100 - doc.USDSharePct - doc.EURSharePct

	ladderYear := map[int]map[string]float64{}
	for _, row := range doc.Ladder {
		ladderYear[row.Year] = map[string]float64{"UAH": row.UAH, "USD": row.USD, "EUR": row.EUR}
	}

	// Поточна дюрація/PV портфеля та цільова дюрація: для кожного паперу
	// рахуємо, куди зрушить дюрацію його купівля. Комбінована дюрація —
	// це середньозважена за приведеною вартістю, тож додавання паперу
	// рахується точно, без перебудови всього портфеля.
	var curMac, curPV float64
	if doc.RateRisk != nil {
		curMac, curPV = doc.RateRisk.DurationYears, doc.RateRisk.PVUAH
	}
	targetDur := 0.0
	rank := "plan"
	if doc.Settings != nil {
		if doc.Settings.TargetDurationYears != nil {
			targetDur = *doc.Settings.TargetDurationYears
		}
		if doc.Settings.ReinvestRank != "" {
			rank = doc.Settings.ReinvestRank
		}
	}
	curDist := math.Abs(curMac - targetDur)
	rates, err := s.rates(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	isins := make([]string, 0, len(bonds))
	for _, b := range bonds {
		isins = append(isins, b.ISIN)
	}
	allPays, err := s.st.PaymentsFor(ctx, isins)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	paysByISIN := map[string][]domain.Payment{}
	for _, p := range allPays {
		paysByISIN[p.ISIN] = append(paysByISIN[p.ISIN], p)
	}

	type suggestion struct {
		ISIN          string    `json:"isin"`
		Currency      string    `json:"currency"`
		RatePct       string    `json:"rate_pct"`
		Maturity      string    `json:"maturity"`
		Nominal       moneyJSON `json:"nominal"`
		Affordable    int64     `json:"affordable"`
		CanBuy        bool      `json:"can_buy"`
		Reason        string    `json:"reason"`
		DurationNow   float64   `json:"duration_now"`
		DurationAfter float64   `json:"duration_after"`
		def           float64
		ladderNom     float64
		rate          int64
		durDist       float64
	}
	out := []suggestion{}
	for _, b := range bonds {
		c := b.Nominal.Currency().Code
		bal := doc.Accounts[c]
		nomMajor := float64(b.Nominal.Amount()) / 100
		if nomMajor <= 0 {
			continue
		}
		// Показуємо рекомендації ЗАВЖДИ, навіть коли грошей ще не вистачає:
		// інакше список порожніє одразу після покупки й помічник мовчить
		// саме тоді, коли ти плануєш наступний крок. Доступність — перший
		// критерій сортування, тож «можу купити» лишається зверху.
		canBuy := bal >= nomMajor
		year := b.Maturity.Year()
		lnom := 0.0
		if m, ok := ladderYear[year]; ok {
			lnom = m[c]
		}
		def := target[c] - cur[c]
		var parts []string
		if def > 0.5 {
			parts = append(parts, fmt.Sprintf("добирає %s (%.0f%% → ціль %.0f%%)", c, cur[c], target[c]))
		}
		if lnom == 0 {
			parts = append(parts, fmt.Sprintf("новий рік %d у драбині", year))
		} else {
			parts = append(parts, fmt.Sprintf("рік %d", year))
		}
		// куди зрушить дюрацію купівля одного такого паперу
		y := doc.PortfolioYield[c] / 100
		if y <= 0 {
			y = doc.PortfolioYieldPct / 100
		}
		var pts []domain.CashPoint
		for _, p := range paysByISIN[b.ISIN] {
			if d := domain.DaysBetween(today, p.PayDate); d > 0 {
				pts = append(pts, domain.CashPoint{
					Years: float64(d) / 365, Amount: float64(p.PerBond.Amount()) / 100,
				})
			}
		}
		bMac, _, bPV := domain.Duration(pts, y)
		fxr := 1.0
		if c != money.UAH {
			fxr = float64(rates[c]) / fx.RateScale
		}
		bPVUAH := bPV * fxr
		newMac := curMac
		if curPV+bPVUAH > 0 {
			newMac = (curMac*curPV + bMac*bPVUAH) / (curPV + bPVUAH)
		}
		durDist := math.Abs(newMac - targetDur)
		if targetDur > 0 && durDist < curDist-0.001 {
			parts = append(parts, "наближає дюрацію до цілі")
		}

		out = append(out, suggestion{
			ISIN: b.ISIN, Currency: c,
			RatePct:    fmt.Sprintf("%d.%02d", b.RateBP/100, b.RateBP%100),
			Maturity:   string(b.Maturity), Nominal: toMoneyJSON(b.Nominal),
			Affordable: int64(bal / nomMajor), CanBuy: canBuy, Reason: strings.Join(parts, "; "),
			DurationNow: round2(curMac), DurationAfter: round2(newMac),
			def: def, ladderNom: lnom, rate: b.RateBP, durDist: durDist,
		})
	}
	// Критерій ранжування обирає користувач: «під план» балансує валюту,
	// драбину й дюрацію; решта — прості й передбачувані.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.CanBuy != b.CanBuy {
			return a.CanBuy // те, що вже по кишені, — зверху
		}
		switch rank {
		case "rate":
			if a.rate != b.rate {
				return a.rate > b.rate
			}
		case "short":
			if a.Maturity != b.Maturity {
				return a.Maturity < b.Maturity
			}
		case "ladder":
			if a.ladderNom != b.ladderNom {
				return a.ladderNom < b.ladderNom
			}
			if a.rate != b.rate {
				return a.rate > b.rate
			}
		default: // plan
			if a.def != b.def {
				return a.def > b.def
			}
			if targetDur > 0 && math.Abs(a.durDist-b.durDist) > 0.01 {
				return a.durDist < b.durDist
			}
			if a.ladderNom != b.ladderNom {
				return a.ladderNom < b.ladderNom
			}
			if a.rate != b.rate {
				return a.rate > b.rate
			}
		}
		return a.Maturity < b.Maturity
	})
	// Ліміту немає свідомо: у таблиці є фільтри, сортування й пагінація,
	// тож звужує користувач, а не бекенд мовчки.
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	snaps, err := s.st.ListSnapshots(r.Context(),
		domain.Date(q.Get("from")), domain.Date(q.Get("to")))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type snapJSON struct {
		Date           string  `json:"date"`
		InvestedUAH    float64 `json:"invested_uah"`
		NominalUAHEq   float64 `json:"nominal_uah_eq"`
		USDSharePct    float64 `json:"usd_share_pct"`
		UninvestedUAH  float64 `json:"uninvested_uah"`
		MonthTargetUAH float64 `json:"month_target_uah"`
		AccountUAH     float64 `json:"account_uah"`
	}
	out := make([]snapJSON, 0, len(snaps))
	for _, sn := range snaps {
		out = append(out, snapJSON{string(sn.Date), float64(sn.InvestedUAH) / 100,
			float64(sn.NominalUAHEq) / 100, float64(sn.USDShareBP) / 100,
			float64(sn.UninvestedUAH) / 100, float64(sn.MonthTargetUAH) / 100,
			float64(sn.AccountUAH) / 100})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleExportCSV — рухи за рік для декларації: отримані виплати і
// продажі з реалізованим результатом. Роздільник ';' і UTF-8 BOM —
// щоб україномовний Excel відкривав без танців.
func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	year := r.URL.Query().Get("year")
	if year == "" {
		year = fmt.Sprintf("%d", time.Now().Year())
	}
	ctx := r.Context()
	lots, sales, _, pays, err := s.portfolio(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	past, err := domain.FuturePayments(pays, lots, sales, domain.Date(year+"-01-01"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	today := domain.NewDate(time.Now())

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=oddinvest-%s.csv", year))
	w.Write([]byte{0xEF, 0xBB, 0xBF}) // BOM
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	cw.Write([]string{"тип", "дата", "ISIN", "кількість", "сума", "валюта", "коментар"})

	typeNames := map[domain.PayType]string{
		domain.PayCoupon: "купон", domain.PayRedemption: "погашення",
		domain.PayEarly: "дострокове погашення",
	}
	for _, cf := range past {
		if !strings.HasPrefix(string(cf.Date), year) || cf.Date.After(today) {
			continue
		}
		cw.Write([]string{typeNames[cf.Type], string(cf.Date), cf.ISIN, "",
			toMoneyJSON(cf.Amount).Amount, cf.Amount.Currency().Code, ""})
	}
	lotByID := map[int64]domain.Lot{}
	for _, l := range lots {
		lotByID[l.ID] = l
	}
	for _, sl := range sales {
		if !strings.HasPrefix(string(sl.SaleDate), year) {
			continue
		}
		lot := lotByID[sl.LotID]
		res, err := domain.RealizedResult(lot, sl, pays)
		if err != nil {
			continue
		}
		proceeds, _ := domain.SaleProceeds(sl)
		cw.Write([]string{"продаж", string(sl.SaleDate), lot.ISIN,
			fmt.Sprintf("%d", sl.Qty), toMoneyJSON(proceeds).Amount,
			proceeds.Currency().Code,
			"результат " + toMoneyJSON(res).Amount + " " + res.Currency().Code})
	}
	cw.Flush()
}
