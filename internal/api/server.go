// Package api — HTTP-сервер: REST + вбудований веб-UI.
package api

import (
	"bytes"
	"context"
	"embed"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/ODDsama/oddinvest/internal/imports"
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
	mux.HandleFunc("PUT /api/lots/{id}", s.handleUpdateLot)
	mux.HandleFunc("DELETE /api/lots/{id}", s.handleDeleteLot)
	mux.HandleFunc("POST /api/sales", s.handleAddSale)
	mux.HandleFunc("GET /api/sales", s.handleListSales)
	mux.HandleFunc("GET /api/deposits", s.handleListDeposits)
	mux.HandleFunc("POST /api/deposits", s.handleAddDeposit)
	mux.HandleFunc("PUT /api/deposits/{id}", s.handleUpdateDeposit)
	mux.HandleFunc("DELETE /api/deposits/{id}", s.handleDeleteDeposit)
	mux.HandleFunc("GET /api/conversions", s.handleListConversions)
	mux.HandleFunc("POST /api/conversions", s.handleAddConversion)
	mux.HandleFunc("PUT /api/conversions/{id}", s.handleUpdateConversion)
	mux.HandleFunc("DELETE /api/conversions/{id}", s.handleDeleteConversion)
	mux.HandleFunc("GET /api/bonds/search", s.handleSearchBonds)
	mux.HandleFunc("GET /api/bonds/{isin}", s.handleGetBond)
	mux.HandleFunc("GET /api/accrued/{isin}", s.handleAccrued)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("PUT /api/settings", s.handlePutSettings)
	mux.HandleFunc("POST /api/payments/status", s.handlePaymentStatus)
	mux.HandleFunc("POST /api/refresh", s.handleRefresh)
	mux.HandleFunc("GET /api/xirr", s.handleXIRR)
	mux.HandleFunc("GET /api/brokers", s.handleListBrokers)
	mux.HandleFunc("POST /api/brokers", s.handleAddBroker)
	mux.HandleFunc("PUT /api/brokers/{id}", s.handleRenameBroker)
	mux.HandleFunc("DELETE /api/brokers/{id}", s.handleDeleteBroker)
	mux.HandleFunc("GET /api/fund-catalog", s.handleListFundCatalog)
	mux.HandleFunc("PUT /api/fund-catalog/{id}", s.handleUpdateFundCatalog)
	mux.HandleFunc("DELETE /api/fund-catalog/{id}", s.handleDeleteFundCatalog)
	mux.HandleFunc("GET /api/funds", s.handleFundOps)
	mux.HandleFunc("POST /api/funds", s.handleAddFundOp)
	mux.HandleFunc("PUT /api/funds/{id}", s.handleUpdateFundOp)
	mux.HandleFunc("DELETE /api/funds/{id}", s.handleDeleteFundOp)
	mux.HandleFunc("GET /api/term-deposits", s.handleTermDeposits)
	mux.HandleFunc("POST /api/term-deposits", s.handleAddTermDeposit)
	mux.HandleFunc("PUT /api/term-deposits/{id}", s.handleUpdateTermDeposit)
	mux.HandleFunc("DELETE /api/term-deposits/{id}", s.handleDeleteTermDeposit)
	mux.HandleFunc("GET /api/reinvest", s.handleReinvest)
	mux.HandleFunc("GET /api/snapshots", s.handleSnapshots)
	mux.HandleFunc("GET /api/export/csv", s.handleExportCSV)
	mux.HandleFunc("GET /api/backup", s.handleBackupExport)
	mux.HandleFunc("POST /api/restore", s.handleBackupImport)
	mux.HandleFunc("POST /api/import/inzhur", s.handleImportInzhur)

	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("GET /", noCache(http.FileServerFS(sub)))
	return logMiddleware(s.log, mux)
}

// noCache забороняє браузеру віддавати статику з кешу без перепитування.
//
// UI складається з ESM-модулів, які тягнуть один одного відносними
// шляхами. Версіонувати кожен шлях нема чим: збірки немає навмисно, а
// без неї немає й хеша у назві файла. Тому свіжість тримається
// заголовком: інакше після оновлення бінарника браузер лишався б на
// старих модулях, і зміни не з'являлись би навіть після Ctrl+Shift+R.
//
// Ціна — повторне вивантаження ~90 КБ на завантаження сторінки: файли
// вбудовані через go:embed з нульовим ModTime, тож Last-Modified немає і
// відповісти 304 нема на що. Для домашнього сервісу в локальній мережі
// це дешевше за годину пошуку «чому не оновилось».
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func logMiddleware(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Info("http", "method", r.Method, "path", r.URL.Path, "dur", time.Since(start))
	})
}

// --- helpers ---

// pathID — {id} зі шляху. Три обробники розбирали його однаково, а з
// появою PUT стало б шість копій.
func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

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

	// Операції фондів тягнемо РАЗ, як і облігації вище: далі вони
	// потрібні п'ятьом різним агрегатам (внески місяця, баланс рахунку,
	// вкладено-по-брокерах, картка фондів, XIRR), і доти, доки кожен
	// тягнув їх сам, це були п'ять однакових запитів у БД, що теоретично
	// могли розійтися між собою. Помилку ковтаємо — фонди могли ще не
	// існувати в старій БД, і це не привід валити весь стан; порожній зріз
	// просто нічого не додасть у жоден агрегат.
	fundOps, _ := s.st.ListFundOps(ctx)
	// Вклади — так само раз, третім інструментом поряд із лотами й фондами.
	termDeposits, _ := s.st.ListTermDeposits(ctx)

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
		u, cerr := fx.ToUAH(money.New(dep.Principal, dep.Currency), rates)
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

	// неперевкладені: минулі виплати без статусу reinvested
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
	unin := money.New(0, money.UAH)
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
		case domain.FundSell, domain.FundDividend:
			delta = op.Amount - op.Tax
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
		}
		if dep.ClosedDate != "" {
			if !dep.ClosedDate.After(today) {
				bal[dep.Currency] += dep.ClosedAmount
				balBC[bc] += dep.ClosedAmount
			}
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
	incomeMonthlyNow := round2(couponSum / 12)

	// --- сертифікати фондів ---
	// Позиція — сальдо журналу операцій. Дивіденди беремо ПІСЛЯ податку:
	// купон ОВДП від нього звільнений, дивіденд фонду ні, тож у спільну
	// картку доходу вони можуть потрапити лише чистими.
	// Дохідність фондів і зведена по портфелю. Рахуються нижче, коли вже
	// зібрані позиції фондів — тут лише оголошені, щоб було видно, що
	// це три різні числа, а не одне з уточненнями.
	var fundsYield, blendedYield float64
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
			fundRows = append(fundRows, row)
			// Чистий дивідендний потік за рік — у спільний «пасивний дохід».
			if y > 0 {
				fundDivNet += mvUAH * y / 100
			}
		}
		incomeMonthlyNow = round2(incomeMonthlyNow + fundDivNet/12)
		// Дохідність фондів — зважена ринковою вартістю: більший фонд
		// має важити більше, ніж дрібний із гучним відсотком.
		var wSum, w float64
		for _, row := range fundRows {
			if row.MarketValue <= 0 {
				continue
			}
			wSum += row.YieldNetPct * row.MarketValue
			w += row.MarketValue
		}
		if w > 0 {
			fundsYield = math.Round(wSum/w*100) / 100
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
		// тягнув усе число в −42%.
		var wDays, wMoney float64
		for _, f := range flows {
			if f.Amount >= 0 {
				continue // вкладення, не повернення
			}
			d := float64(domain.DaysBetween(f.Date, today))
			wDays += d * float64(-f.Amount)
			wMoney += float64(-f.Amount)
		}
		if wMoney <= 0 || wDays/wMoney < 30 {
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
	var ytmWeightUAH, ytmWeightedUAH float64
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
		// Собівартість одного паперу: «брудна» ціна плюс частка комісії —
		// комісія теж з'їдає дохідність, тож ховати її не можна.
		cost := l.PricePerBond
		if fee, ferr := domain.Apportion(l.Fee, 1, l.Qty); ferr == nil && !fee.IsZero() {
			if c2, aerr := cost.Add(fee); aerr == nil {
				cost = c2
			}
		}
		lot := domain.YTMLot{CostPerBond: cost, Qty: q, BuyDate: l.BuyDate, ISIN: l.ISIN}
		ytmLotsByCur[cur] = append(ytmLotsByCur[cur], lot)
		// Для зведеної цифри вагу переводимо в гривню, щоб валюти
		// складались коректно, а самі ставки лишались нативними.
		if y, ok := domain.WeightedYTM([]domain.YTMLot{lot}, pays); ok {
			if w, err := fx.ToUAH(money.New(cost.Amount()*q, cur), rates); err == nil {
				ytmWeightUAH += float64(w.Amount())
				ytmWeightedUAH += float64(w.Amount()) * y
			}
		}
	}
	var portfolioYield float64
	if ytmWeightUAH > 0 {
		portfolioYield = math.Round(ytmWeightedUAH/ytmWeightUAH*100) / 100
	}
	portfolioYieldByCur := map[string]float64{}
	for cur, ls := range ytmLotsByCur {
		if y, ok := domain.WeightedYTM(ls, pays); ok {
			portfolioYieldByCur[cur] = math.Round(y*100) / 100
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
		blendedYield = math.Round((portfolioYield*nominalMajor+fundsYield*fundsUAH)/
			(nominalMajor+fundsUAH)*100) / 100
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
		Rates: rates, MonthInvestedUAH: monthInv, MonthDepositedUAH: monthDep,
		MonthWithdrawnUAH: monthOut,
		MonthTargetUAH: target,
		UninvestedUAH: unin, AccountUAH: account, ReinvestMinUAH: reinvestMin,
		Accounts: accounts, Brokers: brokers, InvestedByBroker: investedByBroker,
		LadderUAH: ladderUAH, Income12m: income12m, Coupons12m: coupons12m,
		FundsUAH: round2(fundsUAH), Funds: fundRows,
		DepositsUAH: round2(depositsUAH), DepositsUAHByCur: depositsUAHByCur,
		IncomeMonthlyNow: incomeMonthlyNow,
		ReinvestMinByCur: reinvestMinByCur, TopN: 5,
		Settings: settings, XIRRPct: xirr, PortfolioYieldPct: portfolioYield,
		FundsYieldPct: fundsYield, BlendedYieldPct: blendedYield,
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
	deposits, _ := s.st.ListTermDeposits(ctx)
	cf = append(cf, domain.DepositCashflows(deposits, from)...)
	sort.Slice(cf, func(i, j int) bool { return cf[i].Date < cf[j].Date })
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
	now := domain.NewDate(time.Now())
	ladder := domain.Ladder(bonds, lots, sales, now)
	deposits, _ := s.st.ListTermDeposits(ctx)
	ladder = append(ladder, domain.DepositLadder(deposits, now)...)
	sort.Slice(ladder, func(i, j int) bool {
		if ladder[i].Year != ladder[j].Year {
			return ladder[i].Year < ladder[j].Year
		}
		return ladder[i].Currency < ladder[j].Currency
	})
	writeJSON(w, http.StatusOK, ladder)
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

// lotFromReq — розбір тіла запиту в лот. Спільний для POST і PUT: інакше
// дві копії правил (валюта з довідника, комісія, дата) неминуче розійшлись
// би, і редагування почало б поводитись інакше за створення.
func (s *Server) lotFromReq(r *http.Request, req lotReq) (domain.Lot, error) {
	var out domain.Lot
	if req.Qty <= 0 {
		return out, errors.New("qty має бути > 0")
	}
	bd, err := domain.ParseDate(req.BuyDate)
	if err != nil {
		return out, err
	}
	cur := req.Currency
	if cur == "" { // валюту беремо з довідника, якщо папір відомий
		if b, _ := s.st.GetBond(r.Context(), req.ISIN); b != nil {
			cur = b.Nominal.Currency().Code
		} else {
			return out, errors.New("папір не в довіднику — вкажіть currency явно")
		}
	}
	price, err := parseMoney(req.Price, cur)
	if err != nil {
		return out, err
	}
	var fee *money.Money
	if strings.TrimSpace(req.Fee) != "" {
		if fee, err = parseMoney(req.Fee, cur); err != nil {
			return out, err
		}
	}
	return domain.Lot{ISIN: req.ISIN, Qty: req.Qty, PricePerBond: price, Fee: fee,
		BuyDate: bd, Channel: req.Channel, Note: req.Note}, nil
}

func (s *Server) handleAddLot(w http.ResponseWriter, r *http.Request) {
	var req lotReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	lot, err := s.lotFromReq(r, req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddLot(r.Context(), lot)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// handleUpdateLot — правка лота БЕЗ видалення. Видалити й створити заново
// не те саме: продажі посилаються на лот через lot_id, тож така «правка»
// осиротила б історію продажів.
func (s *Server) handleUpdateLot(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req lotReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	lot, err := s.lotFromReq(r, req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	lot.ID = id
	// Не даємо зменшити кількість нижче вже проданої — інакше залишок
	// став би від'ємним, а календар виплат порахувався б на мінус.
	sales, err := s.st.ListSales(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var sold int64
	for _, sl := range sales {
		if sl.LotID == id {
			sold += sl.Qty
		}
	}
	if lot.Qty < sold {
		writeErr(w, http.StatusBadRequest,
			fmt.Errorf("з лота вже продано %d паперів — кількість не може бути меншою", sold))
		return
	}
	if err := s.st.UpdateLot(r.Context(), lot); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
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

func depositFromReq(req depositReq) (store.Deposit, error) {
	d := domain.NewDate(time.Now())
	if req.Date != "" {
		var err error
		if d, err = domain.ParseDate(req.Date); err != nil {
			return store.Deposit{}, err
		}
	}
	cur := req.Currency
	if cur == "" {
		cur = money.UAH
	}
	minor, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		return store.Deposit{}, err
	}
	return store.Deposit{Date: d, Amount: minor, Currency: cur,
		Broker: strings.TrimSpace(req.Broker), Note: req.Note}, nil
}

func (s *Server) handleAddDeposit(w http.ResponseWriter, r *http.Request) {
	var req depositReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	dep, err := depositFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddDeposit(r.Context(), dep)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdateDeposit(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req depositReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	dep, err := depositFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	dep.ID = id
	if err := s.st.UpdateDeposit(r.Context(), dep); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
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

func conversionFromReq(req conversionReq) (store.Conversion, error) {
	var out store.Conversion
	d := domain.NewDate(time.Now())
	if req.Date != "" {
		var err error
		if d, err = domain.ParseDate(req.Date); err != nil {
			return out, err
		}
	}
	if req.FromCurrency == req.ToCurrency {
		return out, errors.New("валюти конвертації мають відрізнятись")
	}
	fromMinor, err := domain.ParseDecimalToMinor(req.FromAmount, req.FromCurrency)
	if err != nil {
		return out, err
	}
	toMinor, err := domain.ParseDecimalToMinor(req.ToAmount, req.ToCurrency)
	if err != nil {
		return out, err
	}
	if fromMinor <= 0 || toMinor <= 0 {
		return out, errors.New("суми конвертації мають бути > 0")
	}
	return store.Conversion{Date: d, FromCurrency: req.FromCurrency, FromAmount: fromMinor,
		ToCurrency: req.ToCurrency, ToAmount: toMinor,
		Broker: strings.TrimSpace(req.Broker), Note: req.Note}, nil
}

func (s *Server) handleAddConversion(w http.ResponseWriter, r *http.Request) {
	var req conversionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c, err := conversionFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddConversion(r.Context(), c)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdateConversion(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req conversionReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c, err := conversionFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	c.ID = id
	if err := s.st.UpdateConversion(r.Context(), c); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
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

// «channels» тут більше немає: список брокерів був CSV-рядком у
// налаштуваннях, а тепер це таблиця brokers із власними ендпойнтами.
var settingsKeys = []string{"usd_target_share_pct", "eur_target_share_pct",
	"assumed_rate_pct", "goal_amount_uah", "goal_date",
	"reinvest_rank", "goal_pessimistic_uah", "goal_realistic_uah", "goal_optimistic_uah",
	"uah_devaluation_pct", "terminal_rate_pct", "rate_glide_years",
	// import_since рухає сам імпорт, але лишається редагованим: інакше
	// «перезавантажити позаминулий місяць» стало б неможливим взагалі.
	"import_since"}

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
	// Порожній статус (або "none") — зняти позначку. Той самий маршрут, а
	// не окремий DELETE: календар уже шле сюди isin+дату+статус, і
	// «скасувати» — це просто ще одне значення статусу для UI, яке на
	// боці сервіса означає видалення рядка.
	if req.Status == "" || req.Status == "none" {
		if err := s.st.ClearPaymentStatus(r.Context(), req.ISIN, d); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	} else if err := s.st.SetPaymentStatus(r.Context(), req.ISIN, d, req.Status); err != nil {
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
			"conversions": len(b.Conversions), "fund_ops": len(b.FundOps),
			"term_deposits": len(b.TermDeposits), "settings": len(b.Settings),
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
	rank := "plan"
	if doc.Settings != nil && doc.Settings.ReinvestRank != "" {
		rank = doc.Settings.ReinvestRank
	}
	// Знецінення гривні: те саме припущення, що й у прогнозі, інакше
	// помічник радив би одне, а прогноз малював інше.
	devalPct := defaultDevaluationPct
	if doc.Settings != nil && doc.Settings.UAHDevaluationPct != nil && *doc.Settings.UAHDevaluationPct >= 0 {
		devalPct = *doc.Settings.UAHDevaluationPct
	}
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

	// brokerFit — скільки таких паперів тягне конкретний брокер. Баланси
	// роздільні, тож загальна сума нічого не каже: гривня на inzhur не
	// купить папір у mono.
	type brokerFit struct {
		Broker string `json:"broker"`
		Qty    int64  `json:"qty"`
	}
	type suggestion struct {
		ISIN     string    `json:"isin"`
		Currency string    `json:"currency"`
		RatePct  string    `json:"rate_pct"`
		Maturity string    `json:"maturity"`
		Nominal  moneyJSON `json:"nominal"`
		// CostPerBond — номінал плюс НКД: стільки коштує зайти сьогодні,
		// якщо папір торгується за номіналом.
		CostPerBond moneyJSON `json:"cost_per_bond"`
		// YTMPct — дохідність до погашення за цією ціною, ефективна річна.
		// RealPct — вона ж у сьогоднішніх гривнях, тобто за вирахуванням
		// знецінення для гривневих паперів. Саме RealPct робить гривневі
		// й валютні папери порівнянними.
		YTMPct        float64     `json:"ytm_pct"`
		RealPct       float64     `json:"real_pct"`
		Brokers       []brokerFit `json:"brokers,omitempty"`
		Affordable    int64       `json:"affordable"`
		CanBuy        bool        `json:"can_buy"`
		Reason        string      `json:"reason"`
		DurationNow   float64     `json:"duration_now"`
		DurationAfter float64     `json:"duration_after"`
		def       float64
		ladderNom float64
		rate      int64
	}
	out := []suggestion{}
	for _, b := range bonds {
		c := b.Nominal.Currency().Code
		nomMajor := float64(b.Nominal.Amount()) / 100
		if nomMajor <= 0 {
			continue
		}
		// Ціна входу сьогодні: номінал плюс НКД. Ціни в довіднику НБУ
		// немає, тож рахуємо «за номіналом» — це чесне наближення, і
		// принаймні НКД воно враховує, а він реально сплачується.
		cost := b.Nominal
		if acc, aerr := domain.EstimateAccrued(paysByISIN[b.ISIN], b.ISIN, today); aerr == nil && acc != nil {
			if c2, err2 := cost.Add(acc); err2 == nil {
				cost = c2
			}
		}
		costMajor := float64(cost.Amount()) / 100
		if costMajor <= 0 {
			continue
		}
		// Дохідність до погашення за цією ціною і вона ж у сьогоднішніх
		// гривнях. Для гривневих ділимо на знецінення — саме тут 4% у
		// доларі перестають програвати 16% у гривні автоматично.
		ytm, yerr := domain.YTM(cost, today, paysByISIN[b.ISIN], b.ISIN)
		if yerr != nil {
			continue // без майбутніх виплат порівнювати нема чого
		}
		real := ytm
		if c == money.UAH {
			real = (1+ytm)/(1+devalPct/100) - 1
		}

		// Скільки тягне кожен брокер окремо.
		var fits []brokerFit
		var best int64
		for name, byCur := range doc.Brokers {
			if n := int64(byCur[c] / costMajor); n > 0 {
				fits = append(fits, brokerFit{Broker: name, Qty: n})
				if n > best {
					best = n
				}
			}
		}
		sort.Slice(fits, func(i, j int) bool {
			if fits[i].Qty != fits[j].Qty {
				return fits[i].Qty > fits[j].Qty
			}
			return fits[i].Broker < fits[j].Broker
		})
		// Показуємо рекомендації ЗАВЖДИ, навіть коли грошей ще не вистачає:
		// інакше список порожніє одразу після покупки й помічник мовчить
		// саме тоді, коли ти плануєш наступний крок. Доступність — перший
		// критерій сортування, тож «можу купити» лишається зверху.
		canBuy := best > 0
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

		out = append(out, suggestion{
			ISIN: b.ISIN, Currency: c,
			RatePct:     fmt.Sprintf("%d.%02d", b.RateBP/100, b.RateBP%100),
			Maturity:    string(b.Maturity), Nominal: toMoneyJSON(b.Nominal),
			CostPerBond: toMoneyJSON(cost),
			YTMPct:      round2(ytm * 100), RealPct: round2(real * 100),
			Brokers:     fits,
			Affordable:  best, CanBuy: canBuy, Reason: strings.Join(parts, "; "),
			DurationNow: round2(curMac), DurationAfter: round2(newMac),
			def: def, ladderNom: lnom, rate: b.RateBP,
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
			// «за ставкою» тепер означає за РЕАЛЬНОЮ дохідністю: сира
			// купонна ставка непорівнянна між валютами.
			if math.Abs(a.RealPct-b.RealPct) > 0.01 {
				return a.RealPct > b.RealPct
			}
		case "short":
			if a.Maturity != b.Maturity {
				return a.Maturity < b.Maturity
			}
		case "ladder":
			if a.ladderNom != b.ladderNom {
				return a.ladderNom < b.ladderNom
			}
			if math.Abs(a.RealPct-b.RealPct) > 0.01 {
				return a.RealPct > b.RealPct
			}
		default: // plan
			if a.def != b.def {
				return a.def > b.def
			}
			if math.Abs(a.RealPct-b.RealPct) > 0.01 {
				return a.RealPct > b.RealPct
			}
			if a.ladderNom != b.ladderNom {
				return a.ladderNom < b.ladderNom
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
		FundsUAH       float64 `json:"funds_uah"`
	}
	out := make([]snapJSON, 0, len(snaps))
	for _, sn := range snaps {
		out = append(out, snapJSON{string(sn.Date), float64(sn.InvestedUAH) / 100,
			float64(sn.NominalUAHEq) / 100, float64(sn.USDShareBP) / 100,
			float64(sn.UninvestedUAH) / 100, float64(sn.MonthTargetUAH) / 100,
			float64(sn.AccountUAH) / 100, float64(sn.FundsUAH) / 100})
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

// --- довідники брокерів і фондів ---
//
// Досі списку брокерів як сутності не існувало: він жив CSV-рядком у
// налаштуваннях, тож перейменувати брокера означало не зачепити жодного
// лота — назва в записах лишалась старою. Тепер записи тримаються за id,
// і перейменування підхоплюють усі разом.

func (s *Server) handleListBrokers(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListBrokers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleAddBroker(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddBroker(r.Context(), req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleRenameBroker(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.RenameBroker(r.Context(), id, req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteBroker(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteBroker(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListFundCatalog(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListFunds(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// Фонд заводиться сам при першій операції, тож окремого POST немає —
// лишається виправити назву або валюту.
func (s *Server) handleUpdateFundCatalog(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		Name     string `json:"name"`
		Currency string `json:"currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.RenameFund(r.Context(), id, req.Name, req.Currency); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteFundCatalog(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteFund(r.Context(), id); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// --- сертифікати фондів ---

type fundOpReq struct {
	Date     string `json:"date"`
	Fund     string `json:"fund"`
	Kind     string `json:"kind"` // buy | sell | dividend
	Qty      int64  `json:"qty"`
	Amount   string `json:"amount"`
	Tax      string `json:"tax"`
	Currency string `json:"currency"`
	Broker   string `json:"broker"`
	Note     string `json:"note"`
}

func fundOpFromReq(req fundOpReq) (domain.FundOp, error) {
	var out domain.FundOp
	d := domain.NewDate(time.Now())
	if req.Date != "" {
		var err error
		if d, err = domain.ParseDate(req.Date); err != nil {
			return out, err
		}
	}
	kind := domain.FundOpKind(strings.TrimSpace(req.Kind))
	switch kind {
	case domain.FundBuy, domain.FundSell, domain.FundDividend:
	default:
		return out, fmt.Errorf("kind має бути buy, sell або dividend, маємо %q", req.Kind)
	}
	if strings.TrimSpace(req.Fund) == "" {
		return out, errors.New("вкажіть фонд")
	}
	cur := req.Currency
	if cur == "" {
		cur = money.UAH
	}
	amount, err := domain.ParseDecimalToMinor(req.Amount, cur)
	if err != nil {
		return out, err
	}
	if amount <= 0 {
		return out, errors.New("сума має бути > 0")
	}
	var tax int64
	if strings.TrimSpace(req.Tax) != "" {
		if tax, err = domain.ParseDecimalToMinor(req.Tax, cur); err != nil {
			return out, err
		}
	}
	// Кількість обов'язкова для купівлі й продажу і безглузда для
	// дивіденда: він нараховується на позицію, а не на штуки.
	if kind == domain.FundDividend {
		req.Qty = 0
	} else if req.Qty <= 0 {
		return out, errors.New("кількість сертифікатів має бути > 0")
	}
	return domain.FundOp{Date: d, Fund: strings.TrimSpace(req.Fund), Kind: kind,
		Qty: req.Qty, Amount: amount, Tax: tax, Currency: cur,
		Broker: strings.TrimSpace(req.Broker), Note: req.Note}, nil
}

func (s *Server) handleFundOps(w http.ResponseWriter, r *http.Request) {
	ops, err := s.st.ListFundOps(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type row struct {
		ID       int64     `json:"id"`
		Date     string    `json:"date"`
		Fund     string    `json:"fund"`
		Kind     string    `json:"kind"`
		Qty      int64     `json:"qty,omitempty"`
		Amount   moneyJSON `json:"amount"`
		Tax      moneyJSON `json:"tax,omitempty"`
		Broker   string    `json:"broker,omitempty"`
		Note     string    `json:"note,omitempty"`
	}
	out := make([]row, 0, len(ops))
	for _, op := range ops {
		out = append(out, row{ID: op.ID, Date: string(op.Date), Fund: op.Fund,
			Kind: string(op.Kind), Qty: op.Qty,
			Amount: toMoneyJSON(money.New(op.Amount, op.Currency)),
			Tax:    toMoneyJSON(money.New(op.Tax, op.Currency)),
			Broker: op.Broker, Note: op.Note})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddFundOp(w http.ResponseWriter, r *http.Request) {
	var req fundOpReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op, err := fundOpFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddFundOp(r.Context(), op)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdateFundOp(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req fundOpReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op, err := fundOpFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	op.ID = id
	if err := s.st.UpdateFundOp(r.Context(), op); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteFundOp(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteFundOp(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// --- банківські вклади ---

type termDepositReq struct {
	Bank         string `json:"bank"`
	Currency     string `json:"currency"`
	Principal    string `json:"principal"`
	RatePct      string `json:"rate_pct"`
	OpenDate     string `json:"open_date"`
	MaturityDate string `json:"maturity_date"`
	Payout       string `json:"payout"`
	Capitalized  bool   `json:"capitalized"`
	TaxPct       string `json:"tax_pct"`
	ClosedDate   string `json:"closed_date"`
	ClosedAmount string `json:"closed_amount"`
	Note         string `json:"note"`
}

// parsePercentBP: "16.5" -> 1650. Ставки й податок вводяться відсотками,
// а зберігаються базисними пунктами (×100), як RateBP у Bond.
func parsePercentBP(s string) (int64, error) {
	return domain.ParseDecimalToMinor(strings.TrimSpace(s), money.UAH)
}

func termDepositFromReq(req termDepositReq) (domain.Deposit, error) {
	var out domain.Deposit
	cur := strings.TrimSpace(req.Currency)
	if cur == "" {
		cur = money.UAH
	}
	principal, err := domain.ParseDecimalToMinor(req.Principal, cur)
	if err != nil {
		return out, err
	}
	if principal <= 0 {
		return out, errors.New("тіло вкладу має бути > 0")
	}
	rate, err := parsePercentBP(req.RatePct)
	if err != nil {
		return out, fmt.Errorf("ставка: %w", err)
	}
	open, err := domain.ParseDate(req.OpenDate)
	if err != nil {
		return out, fmt.Errorf("дата відкриття: %w", err)
	}
	mat, err := domain.ParseDate(req.MaturityDate)
	if err != nil {
		return out, fmt.Errorf("дата погашення: %w", err)
	}
	if !open.Before(mat) {
		return out, errors.New("дата погашення має бути пізніше за відкриття")
	}
	payout := domain.DepositPayout(strings.TrimSpace(req.Payout))
	switch payout {
	case "":
		payout = domain.PayoutEnd
	case domain.PayoutEnd, domain.PayoutMonthly, domain.PayoutQuarterly:
	default:
		return out, fmt.Errorf("payout має бути end, monthly або quarterly, маємо %q", req.Payout)
	}
	// Податок за замовчуванням 19.5% (ПДФО 18% + військовий збір 1.5%);
	// порожнє поле = це значення, а не «без податку».
	tax := int64(1950)
	if strings.TrimSpace(req.TaxPct) != "" {
		if tax, err = parsePercentBP(req.TaxPct); err != nil {
			return out, fmt.Errorf("податок: %w", err)
		}
	}
	out = domain.Deposit{
		Bank: strings.TrimSpace(req.Bank), Currency: cur, Principal: principal,
		RateBP: rate, OpenDate: open, MaturityDate: mat, Payout: payout,
		Capitalized: req.Capitalized, TaxBP: tax, Note: req.Note,
	}
	// Дострокове розірвання: обидва поля разом або жодного.
	if strings.TrimSpace(req.ClosedDate) != "" {
		cd, err := domain.ParseDate(req.ClosedDate)
		if err != nil {
			return out, fmt.Errorf("дата розірвання: %w", err)
		}
		ca, err := domain.ParseDecimalToMinor(req.ClosedAmount, cur)
		if err != nil {
			return out, fmt.Errorf("сума розірвання: %w", err)
		}
		out.ClosedDate, out.ClosedAmount = cd, ca
	}
	return out, nil
}

func (s *Server) handleTermDeposits(w http.ResponseWriter, r *http.Request) {
	deps, err := s.st.ListTermDeposits(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	type row struct {
		ID           int64     `json:"id"`
		Bank         string    `json:"bank,omitempty"`
		Principal    moneyJSON `json:"principal"`
		RatePct      float64   `json:"rate_pct"`
		OpenDate     string    `json:"open_date"`
		MaturityDate string    `json:"maturity_date"`
		Payout       string    `json:"payout"`
		Capitalized  bool      `json:"capitalized,omitempty"`
		TaxPct       float64   `json:"tax_pct"`
		ClosedDate   string    `json:"closed_date,omitempty"`
		ClosedAmount moneyJSON `json:"closed_amount,omitempty"`
		Note         string    `json:"note,omitempty"`
	}
	out := make([]row, 0, len(deps))
	for _, d := range deps {
		out = append(out, row{
			ID: d.ID, Bank: d.Bank,
			Principal:    toMoneyJSON(money.New(d.Principal, d.Currency)),
			RatePct:      float64(d.RateBP) / 100,
			OpenDate:     string(d.OpenDate), MaturityDate: string(d.MaturityDate),
			Payout: string(d.Payout), Capitalized: d.Capitalized,
			TaxPct:     float64(d.TaxBP) / 100,
			ClosedDate: string(d.ClosedDate),
			ClosedAmount: toMoneyJSON(money.New(d.ClosedAmount, d.Currency)),
			Note:       d.Note,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAddTermDeposit(w http.ResponseWriter, r *http.Request) {
	var req termDepositReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d, err := termDepositFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	id, err := s.st.AddTermDeposit(r.Context(), d)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (s *Server) handleUpdateTermDeposit(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req termDepositReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d, err := termDepositFromReq(req)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d.ID = id
	if err := s.st.UpdateTermDeposit(r.Context(), d); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteTermDeposit(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.st.DeleteTermDeposit(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.publishAsync()
	w.WriteHeader(http.StatusNoContent)
}

// --- імпорт виписки ---

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// handleImportInzhur приймає xlsx-виписку.
//
// Два режими одним ендпойнтом: ?dry=1 лише показує, що буде зроблено, без
// запису. Стан між викликами не зберігаємо — файл лежить у користувача,
// і повторно надіслати його дешевше, ніж тримати серверну сесію, яку
// потім треба протухати.
//
// Дедуплікація обов'язкова: щомісячна виписка містить і старі рядки, тож
// без неї другий імпорт подвоїв би позицію.
func (s *Server) handleImportInzhur(w http.ResponseWriter, r *http.Request) {
	dry := r.URL.Query().Get("dry") == "1"
	broker := strings.TrimSpace(r.URL.Query().Get("broker"))
	if broker == "" {
		broker = "inzhur"
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("очікували файл у полі file: %w", err))
		return
	}
	defer file.Close()
	buf, err := io.ReadAll(io.LimitReader(file, 16<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sheet, err := imports.ReadXLSX(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	res, err := imports.ParseInzhur(sheet)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	ctx := r.Context()
	deps, _ := s.st.ListDeposits(ctx)
	lots, _ := s.st.ListLots(ctx)
	// Лот вважаємо тим самим за папером, датою й кількістю. Ціну в ключ
	// не беремо: та сама купівля, внесена вручну, могла бути округлена
	// інакше, і розбіжність у копійці не робить її іншою купівлею.
	lotSeen := map[string]bool{}
	for _, l := range lots {
		lotSeen[fmt.Sprintf("%s|%s|%d", l.ISIN, l.BuyDate, l.Qty)] = true
	}
	depSeen := map[string]bool{}
	for _, d := range deps {
		depSeen[fmt.Sprintf("%s|%d|%s|%s", d.Date, d.Amount, d.Currency, d.Broker)] = true
	}

	type outRow struct {
		Date   string `json:"date"`
		Kind   string `json:"kind"`
		Fund   string `json:"fund,omitempty"`
		Qty    int64  `json:"qty,omitempty"`
		Amount string `json:"amount"`
		Tax    string `json:"tax,omitempty"`
		Exists bool   `json:"exists"`
		// Conflict — та сама сума вже лежить у гаманці ручним рухом.
		// Поки обліку фондів не було, купівлі й продажі сертифікатів
		// доводилось записувати як поповнення/зняття; тепер операція фонду
		// теж рухає гаманець, тож стара пара стала б подвійним рахунком.
		Conflict string `json:"conflict,omitempty"`
	}
	// Водяний знак: усе, що старше за нього, не розглядаємо взагалі.
	// Виписка щомісяця приносить повну історію, і покладатись лише на
	// дедуплікацію більше не можна — після ручного підчищення журналу
	// старі рядки почали проситися назад.
	since, _ := s.st.GetSetting(ctx, "import_since")
	out := struct {
		Rows     []outRow          `json:"rows"`
		Skipped  []imports.Skipped `json:"skipped"`
		Imported int               `json:"imported"`
		New      int               `json:"new"`
		// Since — від якої дати враховуємо, Before — скільки рядків
		// відсіяно як старіші. Списком їх не віддаємо: у файлі їх сотні,
		// і перегляд перетворився б на портянку.
		Since  string `json:"since,omitempty"`
		Before int    `json:"before,omitempty"`
	}{Rows: []outRow{}, Skipped: res.Skipped, Since: since}

	for _, row := range res.Rows {
		if since != "" && string(row.Date) < since {
			out.Before++
			continue
		}
		cur := money.UAH
		var exists bool
		switch row.Kind {
		case "fund_buy", "fund_sell", "dividend":
			kind := domain.FundBuy
			if row.Kind == "fund_sell" {
				kind = domain.FundSell
			} else if row.Kind == "dividend" {
				kind = domain.FundDividend
			}
			op := domain.FundOp{Date: row.Date, Fund: row.Fund, Kind: kind, Qty: row.Qty,
				Amount: row.Amount, Tax: row.Tax, Currency: cur, Broker: broker, Note: "виписка"}
			if exists, _ = s.st.FundOpExists(ctx, op); !exists && !dry {
				if _, aerr := s.st.AddFundOp(ctx, op); aerr != nil {
					writeErr(w, http.StatusInternalServerError, aerr)
					return
				}
			}
		case "bond_buy":
			key := fmt.Sprintf("%s|%s|%d", row.Fund, row.Date, row.Qty)
			exists = lotSeen[key]
			if !exists {
				lotSeen[key] = true
				if !dry {
					// Ціна за папір — сума ділена на кількість; залишок від
					// ділення кладемо в комісію, щоб сумарна вартість лота
					// збіглася з випискою до копійки. Зазвичай там і справді
					// сидить комісія брокера, вшита в ціну.
					per := row.Amount / row.Qty
					fee := row.Amount - per*row.Qty
					cur := money.UAH
					if b, berr := s.st.GetBond(ctx, row.Fund); berr == nil && b != nil {
						cur = b.Nominal.Currency().Code
					}
					var feeM *money.Money
					if fee > 0 {
						feeM = money.New(fee, cur)
					}
					if _, aerr := s.st.AddLot(ctx, domain.Lot{ISIN: row.Fund, Qty: row.Qty,
						PricePerBond: money.New(per, cur), Fee: feeM, BuyDate: row.Date,
						Channel: broker, Note: "виписка"}); aerr != nil {
						writeErr(w, http.StatusInternalServerError, aerr)
						return
					}
				}
			}

		case "deposit", "withdrawal":
			amt := row.Amount
			if row.Kind == "withdrawal" {
				amt = -amt
			}
			key := fmt.Sprintf("%s|%d|%s|%s", row.Date, amt, cur, broker)
			exists = depSeen[key]
			if !exists {
				depSeen[key] = true // не задвоїти в межах одного файлу
				if !dry {
					if _, aerr := s.st.AddDeposit(ctx, store.Deposit{Date: row.Date, Amount: amt,
						Currency: cur, Broker: broker, Note: "виписка"}); aerr != nil {
						writeErr(w, http.StatusInternalServerError, aerr)
						return
					}
				}
			}
		}
		if !exists {
			out.New++
			if !dry {
				out.Imported++
			}
		}
		// Шукаємо ручний рух, який СТОЇТЬ ЗАМІСТЬ цієї операції.
		//
		// Напрямок вирішальний. Купівля списує гроші, тож її ручним
		// відповідником було б ЗНЯТТЯ; продаж і дивіденд зараховують —
		// отже ПОПОВНЕННЯ. Порівняння за модулем, як було спершу, ловило
		// й цілком нормальну пару «поповнив 8 051,74 і того ж дня купив
		// на 8 051,74»: гроші прийшли й пішли, ніякого подвоєння немає.
		// Хибні тривоги тут дорого коштують — на них перестають зважати
		// саме тоді, коли трапляється справжня.
		var conflict string
		switch row.Kind {
		case "fund_buy", "fund_sell", "dividend", "bond_buy":
			want := row.Amount
			if row.Kind == "dividend" {
				want = row.Amount - row.Tax
			}
			outflow := row.Kind == "fund_buy" || row.Kind == "bond_buy"
			for _, d := range deps {
				if (d.Amount < 0) != outflow {
					continue // рух у той самий бік, що й операція, — не заміна їй
				}
				if abs64(d.Amount) != want && abs64(d.Amount) != row.Amount {
					continue
				}
				if n := domain.DaysBetween(d.Date, row.Date); n < -2 || n > 2 {
					continue
				}
				conflict = fmt.Sprintf("та сама сума вже є ручним рухом від %s (%s) — видали його, інакше гроші порахуються двічі",
					d.Date, d.Broker)
				break
			}
		}
		out.Rows = append(out.Rows, outRow{Date: string(row.Date), Kind: row.Kind,
			Fund: row.Fund, Qty: row.Qty,
			Amount: money.New(row.Amount, cur).Display(),
			Tax:    money.New(row.Tax, cur).Display(), Exists: exists, Conflict: conflict})
	}
	// Водяний знак рухаємо на ДЕНЬ ЗАПУСКУ, а не на найпізніший рядок
	// файлу: файл міг закінчитись тижнем раніше, ніж його завантажили, і
	// брати дату з даних означало б щоразу знову перебирати той тиждень.
	//
	// Рухаємо навіть коли нічого не імпортовано: «переглянув і нічого
	// нового не було» — теж відповідь, і наступного разу перебирати ті
	// самі рядки ні до чого. Але не при перегляді: dry нічого не змінює,
	// інакше кнопка «Переглянути» тихо з'їдала б період.
	if !dry {
		today := string(domain.NewDate(time.Now()))
		if serr := s.st.SetSetting(ctx, "import_since", today); serr != nil {
			writeErr(w, http.StatusInternalServerError, serr)
			return
		}
		out.Since = today
		if out.Imported > 0 {
			s.publishAsync()
		}
	}
	writeJSON(w, http.StatusOK, out)
}
