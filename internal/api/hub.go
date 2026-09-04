package api

// Хаб: один процес — кілька портфелів (0054).
//
// Сервер (Server) знає рівно один портфель: його сховище звужене до одного
// portfolio_id, і 247 звернень s.st у сорока файлах лишаються як були.
// Другий портфель — це ДРУГИЙ Server на тому самому сховищі, звуженому
// інакше (store.For), а хаб — те, що стоїть перед ними й вирішує, кому
// віддати запит. Так арифметика 34 фаз не дізналась про портфелі взагалі.
//
// ХТО ПОТОЧНИЙ — каже заголовок X-Portfolio зі slug-ом. Порожній означає
// головний: усі клієнти, що портфелів не знають (Home Assistant, старі
// вкладки, curl), працюють як доти. Невідомий slug — 404, і лише ПІСЛЯ
// замка: перебирати назви портфелів анонімно не можна.
//
// ЗАМОК ОДИН НА ВСІХ і стоїть тут, перед диспетчером, кешем ГОЛОВНОГО
// сервера. Сателіти замка не мають (NewSatellite): інакше кожен читав би
// секрети окремо, а зміна пароля на головному розлогінювала б лише його.
// З тієї ж причини ручки входу, пароля, токена й тунелю йдуть лише
// головному — заголовок для них не читається взагалі.
//
// ПОРТФЕЛЬ НЕ В АДРЕСІ. Роутер UI — хеш із трьох сегментів (routes.js), і
// четвертий сегмент означав би правку граматики заради виміру, який до
// маршруту не належить: та сама сторінка «Портфель» існує в кожному.
// Заголовок ставить транспорт, а дип-лінк несе ?p=<slug> перед хешем.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/ODDsama/oddinvest/internal/store"
)

// portfolioHeader — єдине місце, де він читається (Makefile:
// portfolio-boundary). Розбір slug-а в обробниках означав би, що кожен із
// них може помилитись по-своєму.
const portfolioHeader = "X-Portfolio"

// Spawn — що потрібно зробити з портфелем ПОЗА api: завести йому Runner
// (знімок, дамп, публікація) і, коли є MQTT, публікатор. Повертає Refresher
// для сервера цього портфеля й stop — зняти все при видаленні. Інʼєктується
// з main, щоб api не імпортував jobs і mqtt (кільце api ↔ jobs). nil у
// тестах: сателіт тоді без фонової частини, і «Оновити НБУ» на ньому
// відповідає 503, як і на головному без Refresher.
type Spawn func(p store.Portfolio, srv *Server) (Refresher, func())

type Hub struct {
	root       *store.Store
	main       *Server
	mainRoutes http.Handler
	log        *slog.Logger
	spawn      Spawn
	own        *http.ServeMux // /api/portfolios*

	mu   sync.RWMutex
	sats map[string]*satellite // за slug
}

type satellite struct {
	p      store.Portfolio
	srv    *Server
	routes http.Handler
	stop   func()
}

func NewHub(root *store.Store, main *Server, log *slog.Logger, spawn Spawn) *Hub {
	h := &Hub{root: root, main: main, log: log, spawn: spawn, sats: map[string]*satellite{}}
	// Маршрути будуються ОДИН раз: Handler сервера збирає mux зі ста
	// двадцятьма маршрутами й вбудованою статикою на кожен виклик.
	h.mainRoutes = main.routes()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/portfolios", h.handleList)
	mux.HandleFunc("POST /api/portfolios", h.handleAdd)
	mux.HandleFunc("PUT /api/portfolios/{slug}", h.handleRename)
	mux.HandleFunc("DELETE /api/portfolios/{slug}", h.handleDelete)
	h.own = mux
	return h
}

// Start — підняти сателіти для портфелів, що вже є в базі.
func (h *Hub) Start(ctx context.Context) error {
	list, err := h.root.ListPortfolios(ctx)
	if err != nil {
		return err
	}
	for _, p := range list {
		if p.ID == store.MainPortfolio {
			continue
		}
		h.attach(p)
	}
	return nil
}

// attach — сервер, маршрути й фонова частина для одного портфеля.
func (h *Hub) attach(p store.Portfolio) *satellite {
	srv := NewSatellite(h.root.For(p.ID), h.log)
	sat := &satellite{p: p, srv: srv}
	if h.spawn != nil {
		ref, stop := h.spawn(p, srv)
		srv.SetRefresher(ref)
		sat.stop = stop
	}
	sat.routes = srv.routes()
	h.mu.Lock()
	h.sats[p.Slug] = sat
	h.mu.Unlock()
	return sat
}

// Handler — журнал, заборона кешу, ОДИН замок, диспетчер.
func (h *Hub) Handler() http.Handler {
	return logMiddleware(h.log, noStoreAPI(h.main.requireAuth(http.HandlerFunc(h.dispatch))))
}

// mainOnly — маршрути, що не мають портфеля: вхід, пароль, токен машин,
// тунель. Заголовок для них не читається: /api/auth з чужим slug-ом
// мусить відповісти так само, як без нього, інакше екран входу залежав би
// від того, що лишилось у localStorage.
func mainOnly(path string) bool {
	for _, p := range []string{"/api/auth", "/api/login", "/api/logout", "/api/remote"} {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

func (h *Hub) dispatch(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/api/portfolios" || strings.HasPrefix(path, "/api/portfolios/") {
		h.own.ServeHTTP(w, r)
		return
	}
	if !strings.HasPrefix(path, "/api/") || mainOnly(path) {
		h.mainRoutes.ServeHTTP(w, r)
		return
	}
	slug := strings.TrimSpace(r.Header.Get(portfolioHeader))
	if slug == "" || slug == store.MainSlug {
		h.mainRoutes.ServeHTTP(w, r)
		return
	}
	h.mu.RLock()
	sat := h.sats[slug]
	h.mu.RUnlock()
	if sat == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("портфеля %q немає", slug))
		return
	}
	sat.routes.ServeHTTP(w, r)
}

// --- /api/portfolios ---

func (h *Hub) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := h.root.ListPortfolios(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Hub) handleAdd(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if _, err := h.root.AddPortfolio(r.Context(), in.Slug, in.Name); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	p, err := h.root.PortfolioBySlug(r.Context(), strings.TrimSpace(in.Slug))
	if err != nil || p == nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("портфель створено, але не прочитано: %v", err))
		return
	}
	h.attach(*p)
	h.log.Info("портфель створено", "slug", p.Slug, "name", p.Name)
	writeJSON(w, http.StatusCreated, p)
}

func (h *Hub) handleRename(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p, err := h.root.PortfolioBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if p == nil {
		writeErr(w, http.StatusNotFound, errors.New("портфеля немає"))
		return
	}
	if err := h.root.RenamePortfolio(r.Context(), p.ID, in.Name); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	if sat := h.sats[p.Slug]; sat != nil {
		sat.p.Name = strings.TrimSpace(in.Name)
	}
	h.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// handleDelete — стерти портфель разом з усім його вмістом. Спершу зняти
// фонову частину, потім стерти: інакше добовий прогін міг би записати
// знімок у портфель, якого вже немає, і впасти на FK.
func (h *Hub) handleDelete(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == store.MainSlug {
		writeErr(w, http.StatusConflict, errors.New("головний портфель не стирається"))
		return
	}
	p, err := h.root.PortfolioBySlug(r.Context(), slug)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if p == nil {
		writeErr(w, http.StatusNotFound, errors.New("портфеля немає"))
		return
	}
	h.mu.Lock()
	sat := h.sats[slug]
	delete(h.sats, slug)
	h.mu.Unlock()
	if sat != nil && sat.stop != nil {
		sat.stop()
	}
	if err := h.root.DeletePortfolio(r.Context(), p.ID); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	h.log.Info("портфель стерто", "slug", slug)
	w.WriteHeader(http.StatusNoContent)
}
