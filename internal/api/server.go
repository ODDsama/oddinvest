// Package api — HTTP-сервер: REST + вбудований веб-UI.
package api

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ODDsama/oddinvest/internal/store"
	"github.com/ODDsama/oddinvest/internal/tunnel"
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

	// Стан публікатора стану — див. publishAsync.
	pubMu      sync.Mutex
	pubRunning bool
	pubPending bool

	// Замок на /api/* і лічильник невдалих входів — auth.go.
	auth      authCache
	authFails *authState
	// tun — доступ ззовні (internal/tunnel). nil у тестах і в збірках без
	// демона: сторінка тоді каже «не налаштовано», а дії відповідають 501.
	tun *tunnel.Manager
}

// SetTunnel підʼєднує менеджер тунелю. Окремим сеттером, як і Refresher, і
// з тієї самої причини: менеджеру потрібне сховище, яке відкриває main,
// а серверу — сам менеджер, тож кільце розривається присвоєнням.
func (s *Server) SetTunnel(t *tunnel.Manager) { s.tun = t }

// SetRefresher підʼєднує фонову частину. Сеттер, а не аргумент New: runner
// будується з BuildStateDoc цього ж сервера, тож кільце api ↔ jobs
// розривається присвоєнням — доти main створював сервер двічі.
func (s *Server) SetRefresher(ref Refresher) { s.ref = ref }

// NewSatellite — сервер ІНШОГО портфеля (0054): те саме сховище, звужене
// до його portfolio_id, ті самі маршрути, але без замка й без тунелю.
// Замок один на всіх і стоїть у Hub перед диспетчером; тут його немає
// навмисно, інакше секрети читались би раз на портфель, а зміна пароля
// на головному розлогінювала б лише його.
func NewSatellite(st *store.Store, log *slog.Logger) *Server {
	return &Server{st: st, log: log}
}

// New — сервер із секретами, прочитаними зі сховища.
//
// Помилка читання не фатальна й не мовчазна: кеш лишається порожнім, тобто
// замка немає, і застосунок покаже форму «задай пароль». Валити старт
// сервера через це не можна — сторінка відновлення з копії саме тоді й
// потрібна, коли зі сховищем щось не так.
func New(st *store.Store, ref Refresher, log *slog.Logger) *Server {
	s := &Server{st: st, ref: ref, log: log, authFails: newAuthState()}
	if err := s.reloadAuth(context.Background()); err != nil {
		log.Error("секрети не прочитались — сервіс лишається відкритим", "err", err)
	}
	if !s.authEnabled() {
		log.Warn("пароль не заданий: /api/* відкритий усім, хто бачить порт — задай його першим входом у застосунок")
	}
	return s
}

// Handler — маршрути ОДНОГО портфеля під замком і журналом. Так живуть
// тести й збірка без хаба; у бою ту саму композицію робить Hub.Handler,
// лише замок там один на всі портфелі.
func (s *Server) Handler() http.Handler {
	// Замок стоїть ПІД журналом і ПІД заголовком кешу: відмова 401 мусить
	// потрапити в журнал і не мусить осісти в кеші браузера — а noStoreAPI
	// ставить заголовок ДО виклику наступного, тож він має бути зовні.
	return logMiddleware(s.log, noStoreAPI(s.requireAuth(s.routes())))
}

// routes — голий mux сервера: усі /api/* цього портфеля й статика. Без
// замка й журналу — їх додає той, хто складає обробник (Handler або Hub).
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/summary", s.handleSummary)
	mux.HandleFunc("GET /api/positions", s.handlePositions)
	mux.HandleFunc("GET /api/calendar", s.handleCalendar)
	mux.HandleFunc("GET /api/ladder", s.handleLadder)
	mux.HandleFunc("GET /api/lots", s.handleListLots)
	mux.HandleFunc("POST /api/lots", s.handleAddLot)
	// /check нічого не пише — лише каже, чи вистачить грошей і скільки
	// бракує. Форма питає ним ДО запису, щоб запропонувати поповнення.
	mux.HandleFunc("POST /api/lots/check", s.handleLotCheck)
	mux.HandleFunc("PUT /api/lots/{id}", s.handleUpdateLot)
	mux.HandleFunc("DELETE /api/lots/{id}", s.handleDeleteLot)
	mux.HandleFunc("POST /api/sales", s.handleAddSale)
	mux.HandleFunc("GET /api/sales", s.handleListSales)
	mux.HandleFunc("PUT /api/sales/{id}", s.handleUpdateSale)
	mux.HandleFunc("DELETE /api/sales/{id}", s.handleDeleteSale)
	mux.HandleFunc("GET /api/deposits", s.handleListDeposits)
	mux.HandleFunc("POST /api/deposits", s.handleAddDeposit)
	mux.HandleFunc("PUT /api/deposits/{id}", s.handleUpdateDeposit)
	mux.HandleFunc("DELETE /api/deposits/{id}", s.handleDeleteDeposit)
	mux.HandleFunc("GET /api/reserve", s.handleListReserveOps)
	mux.HandleFunc("POST /api/reserve", s.handleAddReserveOp)
	mux.HandleFunc("PUT /api/reserve/{id}", s.handleUpdateReserveOp)
	mux.HandleFunc("DELETE /api/reserve/{id}", s.handleDeleteReserveOp)

	// Цілі накопичення — ДВА ресурси, бо сутностей дві: сама ціль і рухи
	// під нею. Довід, чому їх не можна звести в один, — у шапці
	// handlers_goals.go.
	mux.HandleFunc("GET /api/goals", s.handleListGoals)
	mux.HandleFunc("POST /api/goals", s.handleAddGoal)
	mux.HandleFunc("PUT /api/goals/{id}", s.handleUpdateGoal)
	mux.HandleFunc("DELETE /api/goals/{id}", s.handleDeleteGoal)
	mux.HandleFunc("GET /api/goal-ops", s.handleListGoalOps)
	mux.HandleFunc("POST /api/goal-ops", s.handleAddGoalOp)
	mux.HandleFunc("PUT /api/goal-ops/{id}", s.handleUpdateGoalOp)
	mux.HandleFunc("DELETE /api/goal-ops/{id}", s.handleDeleteGoalOp)

	// handlers_debts.go. Три набори, бо сутностей три: борг, рух під ним і
	// звірка з банком (довід — у шапці файла).
	mux.HandleFunc("GET /api/debts", s.handleListDebts)
	mux.HandleFunc("POST /api/debts", s.handleAddDebt)
	mux.HandleFunc("PUT /api/debts/{id}", s.handleUpdateDebt)
	mux.HandleFunc("DELETE /api/debts/{id}", s.handleDeleteDebt)
	mux.HandleFunc("GET /api/debt-ops", s.handleListDebtOps)
	mux.HandleFunc("POST /api/debt-ops", s.handleAddDebtOp)
	mux.HandleFunc("PUT /api/debt-ops/{id}", s.handleUpdateDebtOp)
	mux.HandleFunc("DELETE /api/debt-ops/{id}", s.handleDeleteDebtOp)
	mux.HandleFunc("GET /api/debt-marks", s.handleListDebtMarks)
	mux.HandleFunc("POST /api/debt-marks", s.handleAddDebtMark)
	mux.HandleFunc("PUT /api/debt-marks/{id}", s.handleUpdateDebtMark)
	mux.HandleFunc("DELETE /api/debt-marks/{id}", s.handleDeleteDebtMark)
	// handlers_payoff.go — проєкція, а не стан (довід у шапці файла).
	mux.HandleFunc("GET /api/payoff", s.handlePayoff)
	mux.HandleFunc("GET /api/conversions", s.handleListConversions)
	mux.HandleFunc("POST /api/conversions", s.handleAddConversion)
	mux.HandleFunc("PUT /api/conversions/{id}", s.handleUpdateConversion)
	mux.HandleFunc("DELETE /api/conversions/{id}", s.handleDeleteConversion)
	mux.HandleFunc("GET /api/bonds/search", s.handleSearchBonds)
	mux.HandleFunc("GET /api/bonds/{isin}", s.handleGetBond)
	mux.HandleFunc("GET /api/accrued/{isin}", s.handleAccrued)
	mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	mux.HandleFunc("GET /api/devaluation", s.handleDevaluation)
	mux.HandleFunc("GET /api/cashflow", s.handleCashflowStatement)
	mux.HandleFunc("GET /api/period", s.handlePeriod)
	mux.HandleFunc("GET /api/year", s.handleYear)
	mux.HandleFunc("GET /api/tax", s.handleTax)
	mux.HandleFunc("GET /api/benchmark", s.handleBenchmark)
	mux.HandleFunc("GET /api/rivals", s.handleRivals)
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
	// Позначки ціни сертифіката (0034). Окремий ресурс, а не поле довідника:
	// це ІСТОРІЯ, а не остання відома величина, і колонка «остання ціна» в
	// funds дала б їй розійтись із журналом (шапка 0009).
	mux.HandleFunc("GET /api/fund-prices", s.handleFundPrices)
	mux.HandleFunc("POST /api/fund-prices", s.handleAddFundPrices)
	mux.HandleFunc("PUT /api/fund-prices/{id}", s.handleUpdateFundPrice)
	mux.HandleFunc("DELETE /api/fund-prices/{id}", s.handleDeleteFundPrice)
	// НПФ: довідник рахунків, журнал внесків і історія ЧВОПА — три ресурси,
	// бо три різні речі (властивості рахунку, факти з виписки, опублікована
	// фондом таблиця). /nav окремо від PUT рахунку: переписати число з
	// кабінету — найчастіша дія, і повне тіло для неї дало б формі
	// перезаписати поля, яких вона не показує.
	mux.HandleFunc("GET /api/npf-accounts", s.handleNPFAccounts)
	mux.HandleFunc("POST /api/npf-accounts", s.handleAddNPFAccount)
	mux.HandleFunc("PUT /api/npf-accounts/{id}", s.handleUpdateNPFAccount)
	mux.HandleFunc("PUT /api/npf-accounts/{id}/nav", s.handleSetNPFNav)
	mux.HandleFunc("DELETE /api/npf-accounts/{id}", s.handleDeleteNPFAccount)
	mux.HandleFunc("GET /api/npf", s.handleNPFOps)
	mux.HandleFunc("POST /api/npf", s.handleAddNPFOp)
	mux.HandleFunc("POST /api/npf/check", s.handleNPFOpCheck)
	mux.HandleFunc("PUT /api/npf/{id}", s.handleUpdateNPFOp)
	mux.HandleFunc("DELETE /api/npf/{id}", s.handleDeleteNPFOp)
	mux.HandleFunc("GET /api/npf-nav", s.handleNPFNav)
	mux.HandleFunc("POST /api/npf-nav", s.handleAddNPFNav)
	mux.HandleFunc("PUT /api/npf-nav/{id}", s.handleUpdateNPFNav)
	mux.HandleFunc("DELETE /api/npf-nav/{id}", s.handleDeleteNPFNav)
	mux.HandleFunc("GET /api/term-deposits", s.handleTermDeposits)
	mux.HandleFunc("POST /api/term-deposits", s.handleAddTermDeposit)
	mux.HandleFunc("POST /api/term-deposits/check", s.handleTermDepositCheck)
	mux.HandleFunc("PUT /api/term-deposits/{id}", s.handleUpdateTermDeposit)
	mux.HandleFunc("DELETE /api/term-deposits/{id}", s.handleDeleteTermDeposit)
	mux.HandleFunc("POST /api/term-deposits/{id}/topups", s.handleAddDepositTopup)
	mux.HandleFunc("POST /api/term-deposits/{id}/topups/check", s.handleDepositTopupCheck)
	mux.HandleFunc("PUT /api/term-deposits/{id}/topups/{topupId}", s.handleUpdateDepositTopup)
	mux.HandleFunc("DELETE /api/term-deposits/{id}/topups/{topupId}", s.handleDeleteDepositTopup)
	mux.HandleFunc("GET /api/plan", s.handlePlanTimeline)
	mux.HandleFunc("GET /api/plan/flows", s.handleListPlanFlows)
	mux.HandleFunc("POST /api/plan/flows", s.handleAddPlanFlow)
	mux.HandleFunc("PUT /api/plan/flows/{id}", s.handleUpdatePlanFlow)
	mux.HandleFunc("DELETE /api/plan/flows/{id}", s.handleDeletePlanFlow)
	mux.HandleFunc("GET /api/plan/buys", s.handleListPlanBuys)
	mux.HandleFunc("POST /api/plan/buys", s.handleAddPlanBuy)
	mux.HandleFunc("PUT /api/plan/buys/{id}", s.handleUpdatePlanBuy)
	mux.HandleFunc("DELETE /api/plan/buys/{id}", s.handleDeletePlanBuy)
	mux.HandleFunc("GET /api/plan/receipts", s.handleListPlanReceipts)
	mux.HandleFunc("POST /api/plan/receipts", s.handleAddPlanReceipt)
	mux.HandleFunc("PUT /api/plan/receipts/{id}", s.handleUpdatePlanReceipt)
	mux.HandleFunc("DELETE /api/plan/receipts/{id}", s.handleDeletePlanReceipt)
	mux.HandleFunc("GET /api/plan/actions", s.handleListPlanActions)
	mux.HandleFunc("POST /api/plan/actions", s.handleAddPlanAction)
	mux.HandleFunc("PUT /api/plan/actions/{id}", s.handleUpdatePlanAction)
	mux.HandleFunc("DELETE /api/plan/actions/{id}", s.handleDeletePlanAction)
	mux.HandleFunc("GET /api/reinvest", s.handleReinvest)
	// Розкладка суми — теж поруч, і з тієї самої причини: вона бере рейтинг
	// помічника як є. Свій порядок тут означав би, що «Що купити» і «куди
	// закинути те, що прийшло» радять різне.
	mux.HandleFunc("POST /api/allocate", s.handleAllocate)
	// Маршрут — та сама розкладка, прокручена вперед по календарю
	// надходжень. Поруч навмисно: перша його нога мусить збігатися з
	// розкладкою на ту саму суму, і тест на це стоїть саме заради того,
	// щоб «куди закинути те, що прийшло» і «куди піде те, що прийде»
	// лишались однією відповіддю.
	mux.HandleFunc("GET /api/route", s.handleRoute)
	// Перекладання — поруч із помічником навмисно: два боки одного
	// питання про реінвест, і альтернативу вони беруть з одного рейтингу.
	// Ретроспектива помічника — теж поруч: журнал рішень існує рівно
	// заради питання «чи працює те, що радить /api/reinvest».
	mux.HandleFunc("GET /api/decisions", s.handleDecisions)
	mux.HandleFunc("GET /api/progress", s.handleProgress)
	mux.HandleFunc("GET /api/switch", s.handleSwitch)
	mux.HandleFunc("POST /api/switch", s.handleSwitchVerdict)
	mux.HandleFunc("GET /api/auctions/curve", s.handleAuctionsCurve)
	mux.HandleFunc("POST /api/whatif", s.handleWhatIf)
	mux.HandleFunc("POST /api/policy/preview", s.handlePolicyPreview)
	// Третє превʼю поверх того самого buildStateWith, і питання в нього
	// третє: не «якщо це купити» й не «що означають цілі», а «що зробив
	// би з цим портфелем рух курсу, який уже був». GET, бо тіла від
	// людини немає — вхід виводиться з власної історії (довід у шапці).
	mux.HandleFunc("GET /api/fx-shock", s.handleFXShock)
	// Четверте превʼю, і воно стоїть на межі двох контурів: питає не «що
	// буде, якщо це купити», а «що коштує те, що я просто витрачу».
	// Довід, чому окремим маршрутом, — у шапці handlers_spend.go.
	mux.HandleFunc("POST /api/spend", s.handleSpend)
	mux.HandleFunc("GET /api/snapshots", s.handleSnapshots)
	mux.HandleFunc("GET /api/export/csv", s.handleExportCSV)
	mux.HandleFunc("GET /api/backup", s.handleBackupExport)
	mux.HandleFunc("POST /api/restore", s.handleBackupImport)
	// Імпорт виписки. /api/import — за профілем; /api/import/inzhur —
	// історичний псевдонім на один реліз (див. handlers_import.go).
	mux.HandleFunc("POST /api/import", s.handleImport)
	mux.HandleFunc("POST /api/import/inzhur", s.handleImportInzhur)
	mux.HandleFunc("GET /api/import/profiles", s.handleListImportProfiles)
	mux.HandleFunc("PUT /api/import/profiles/{name}", s.handleSaveImportProfile)
	mux.HandleFunc("DELETE /api/import/profiles/{name}", s.handleDeleteImportProfile)

	// Вхід/вихід, питання «чи треба входити» і перший пароль — єдині
	// маршрути /api/*, які requireAuth пропускає (auth.go). Решта замка —
	// зміна пароля й токен машин — під замком, як і все інше.
	mux.HandleFunc("GET /api/auth", s.handleAuthStatus)
	mux.HandleFunc("POST /api/auth/setup", s.handleAuthSetup)
	mux.HandleFunc("PUT /api/auth/password", s.handleAuthPassword)
	mux.HandleFunc("POST /api/auth/token", s.handleAuthToken)
	mux.HandleFunc("DELETE /api/auth/token", s.handleAuthTokenRevoke)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /api/remote", s.handleRemoteStatus)
	mux.HandleFunc("POST /api/remote/connect", s.handleRemoteConnect)
	mux.HandleFunc("POST /api/remote/cert", s.handleRemoteCert)
	mux.HandleFunc("POST /api/remote/disconnect", s.handleRemoteDisconnect)

	sub, _ := fs.Sub(webFS, "web") //nolint:errcheck // шлях у go:embed — константа, помилка неможлива
	mux.Handle("GET /", noCache(http.FileServerFS(sub)))
	return mux
}

// noStoreAPI забороняє кешувати ВІДПОВІДІ API взагалі.
//
// noCache нижче накритий лише статикою, а маршрути /api/ не мали
// заголовків кешу жодних — і тоді браузер має право застосувати
// евристику й віддати стару відповідь із власного кешу. Спіймалось це
// живцем: після відновлення бекапу сторінка й далі показувала попередній
// набір знімків, хоча сервер уже віддавав новий.
//
// Різниця з noCache навмисна. Статику перепитувати можна й треба
// (no-cache = «спитай, чи не змінилось»), а відповідь API не має сенсу
// зберігати взагалі: вона правдива рівно в момент запиту, і вчорашній
// капітал, показаний як сьогоднішній, гірший за його відсутність.
func noStoreAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
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
//
// Виняток один — шрифти. Вони важать більше за весь решту застосунку
// разом, і та сама відсутність ModTime означала б 88 КБ на КОЖНЕ
// відкриття сторінки. Кешувати їх назавжди можна саме тому, чому не
// можна модулі: вміст файла шрифту незмінний за побудовою, бо нова
// версія — це нове імʼя (inter-var.v1.woff2 → …v2…, див.
// web-fonts-build.sh). Модулі такої властивості не мають.
//
// Іконки для «додати на екран» — той самий виняток, що й шрифти, з тієї
// самої причини: новий знак означав би новий файл, а не новий уміст.
func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/fonts/") || strings.HasPrefix(r.URL.Path, "/icons/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

// statusWriter запамʼятовує код відповіді для журналу. Доти рядок «http»
// не мав статусу взагалі — і 401 від замка виглядав би в журналі так само,
// як 200, тобто підбір пароля не було б чим помітити.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func logMiddleware(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.code == 0 {
			sw.code = http.StatusOK
		}
		log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.code, "dur", time.Since(start))
	})
}

// --- helpers ---

// publishAsync републікує стан у MQTT після мутації — не більш як один
// раз одночасно й не частіше, ніж встигає зібратись документ.
//
// Доти кожен із шести десятків викликів просто запускав власну горутину,
// і це давало дві біди на одному місці.
//
// ПОРЯДОК. Дві швидкі правки поспіль запускали дві збірки, кожна з яких
// читає базу двома десятками запитів через ЄДИНЕ зʼєднання
// (SetMaxOpenConns(1)). Хто з них дійде до publish першим — не визначено,
// тож у retained-топік могла лягти СТАРІША картина, і HA показував би її
// аж до наступної мутації. Помилка, яку неможливо відтворити навмисно й
// нічим не видно в журналі.
//
// ЗЛИТТЯ. Поки збірка йде, наступні виклики не додають нової черги:
// вистачить одного повтору після завершення, бо документ будується з
// поточного стану бази, а не з того, який був у мить виклику. Десять
// правок підряд коштують дві публікації, а не десять.
//
// Свого воркера з власним життєвим циклом навмисно немає: горутина живе
// рівно доти, доки є що публікувати, тож ні зупиняти, ні чекати на неї
// нікому не треба — а Server створюється в тестах десятками.
func (s *Server) publishAsync() {
	if s.ref == nil {
		return
	}
	s.pubMu.Lock()
	defer s.pubMu.Unlock()
	if s.pubRunning {
		s.pubPending = true
		return
	}
	s.pubRunning = true
	go s.publishLoop()
}

func (s *Server) publishLoop() {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := s.ref.PublishState(ctx)
		cancel()
		if err != nil {
			s.log.Error("публікація стану", "err", err)
		}
		s.pubMu.Lock()
		if !s.pubPending {
			s.pubRunning = false
			s.pubMu.Unlock()
			return
		}
		s.pubPending = false
		s.pubMu.Unlock()
	}
}
