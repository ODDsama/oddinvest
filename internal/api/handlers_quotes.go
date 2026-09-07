// Ринкові ціни ОВДП: обхід на вимогу, зріз для екрана й ручна ціна.
//
// # ЧОМУ ОБХІД СИНХРОННИЙ
//
// Прохід по шістдесяти сторінках чужого сайту з паузою в секунду — це
// хвилина, поки кнопка мовчить. Асинхронність коштувала б сховища
// прогресу, ендпойнта статусу й прапорця «вже біжить» — три нові шви
// заради дії, яку роблять кілька разів на місяць (CLAUDE.md §3).
// Прецедент поруч: POST /api/refresh теж уміє йти десятки секунд.
//
// # І ЧОМУ ПЕРЕЛІК ПАПЕРІВ ЗБИРАЄТЬСЯ ТУТ, А НЕ В jobs
//
// «Які папери мене цікавлять» — питання портфеля й рейтингу, тобто цього
// пакета. «Як сходити назовні, не нарвавшись» — питання пауз і таймаутів,
// тобто jobs. Зібрати перелік там означало б потягнути в фонову частину
// побудову документа стану.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/finomo"
	"github.com/ODDsama/oddinvest/internal/store"
)

// quotesRunTimeout — стеля на весь прохід.
//
// Шістдесят сторінок по секунді плюс відповіді — це близько двох хвилин у
// найгіршому разі; три дають запас, не перетворюючи зависання джерела на
// вічно відкрите з'єднання.
const quotesRunTimeout = 3 * time.Minute

// quoteRow — рядок зрізу для екрана: ціна плюс те, що про неї треба знати.
type quoteRow struct {
	store.Quote
	// Mine — назва ТВОГО брокера, з яким зіставлено це джерело. Порожньо
	// означає, що рахунку тут немає, і саме тому ця ціна в квиток не
	// потрапляє: показуємо її як орієнтир, а не як пораду.
	Mine string `json:"mine,omitempty"`
	// Price — та сама ціна грошима. Мінорні одиниці поруч лишаються, бо на
	// них тримається порівняння, а гроші — щоб екран не ділив на сто сам.
	Price moneyJSON `json:"price"`
}

type quotesDoc struct {
	FetchedAt string     `json:"fetched_at,omitempty"`
	Rows      []quoteRow `json:"rows"`
	// Sources — які ключі джерел взагалі бувають, і чи зіставлений кожен.
	// Потрібне формі зіставлення: перелік продавців не сутність у базі, а
	// властивість джерела, тож refSelect для нього не годиться.
	Sources []quoteSource `json:"sources"`
}

type quoteSource struct {
	Key string `json:"key"`
	// Label — назва продавця людською мовою. Без неї у випадайці стояли б
	// самі латинські ключі, і «BtcBroker» довелось би вгадувати.
	Label string `json:"label,omitempty"`
	Mine  string `json:"mine,omitempty"`
}

// handleListQuotes — увесь відомий зріз цін.
func (s *Server) handleListQuotes(w http.ResponseWriter, r *http.Request) {
	book, err := s.quotesFor(r.Context(), nil, domain.NewDate(time.Now()))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	doc := quotesDoc{FetchedAt: book.fetchedAt, Rows: []quoteRow{}}
	seen := map[string]bool{}
	for _, q := range book.all {
		doc.Rows = append(doc.Rows, quoteRow{Quote: q, Mine: book.mine[q.Source], Price: toMoneyJSON(q.Money())})
		seen[q.Source] = true
	}
	// Зіставлені джерела показуються навіть тоді, коли ціни від них ще
	// немає: інакше форма зіставлення була б порожня рівно доти, доки не
	// натиснуто кнопку, і виглядало б це як поломка зіставлення.
	for src := range book.mine {
		seen[src] = true
	}
	// І ВІДОМІ ПРОДАВЦІ — ТЕЖ, НАВІТЬ КОЛИ ЦІН ЩЕ НЕМА ЖОДНОЇ.
	//
	// Без цього виходило замкнене коло, у яке робота й потрапила на
	// бойовому: ціна не йде в квиток без зіставлення, а зіставляти нема з
	// чим, доки не набрано цін. Перелік — засів (finomo.KnownSellers), і
	// саме тому він ЗВОДИТЬСЯ з тим, що реально прийшло, а не підміняє
	// його: новий продавець у джерела з'явиться сам після першого обходу.
	for src := range finomo.KnownSellers {
		seen[src] = true
	}
	for src := range seen {
		doc.Sources = append(doc.Sources, quoteSource{
			Key: src, Label: finomo.KnownSellers[src], Mine: book.mine[src],
		})
	}
	sort.Slice(doc.Sources, func(i, j int) bool { return doc.Sources[i].Key < doc.Sources[j].Key })
	writeJSON(w, http.StatusOK, doc)
}

// handleRefreshQuotes — сходити по ціни для паперів, які зараз важать.
func (s *Server) handleRefreshQuotes(w http.ResponseWriter, r *http.Request) {
	if s.ref == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("refresher недоступний"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), quotesRunTimeout)
	defer cancel()
	isins, err := s.quoteISINs(ctx, time.Now())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if len(isins) == 0 {
		writeErr(w, http.StatusBadRequest,
			badRequestf("немає паперів, для яких питати ціну: ані в портфелі, ані в порадах"))
		return
	}
	res, err := s.ref.RefreshQuotes(ctx, isins)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	// publishAsync НЕ кличемо: ціни не течуть у документ стану (contract/
	// oddinvest-state.schema.json їх не знає), тож публікувати нема чого.
	writeJSON(w, http.StatusOK, res)
}

// quoteISINs — папери, для яких ціна має значення: спершу свої, далі верх
// рейтингу.
//
// ПОРЯДОК ЗНАЧУЩИЙ, бо прохід має стелю (quotesCap у jobs). Портфель іде
// першим, бо за ці папери вже заплачено й переоцінка стосується їх
// напевно; далі — ті, які застосунок радить купити. Хвіст рейтингу за
// стелю не вміщається й підтягнеться наступного разу, коли зміниться склад
// порад.
func (s *Server) quoteISINs(ctx context.Context, now time.Time) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	add := func(isin string) {
		isin = strings.ToUpper(strings.TrimSpace(isin))
		if isin == "" || seen[isin] {
			return
		}
		seen[isin] = true
		out = append(out, isin)
	}
	// Свої папери — тим самим завантаженням, що й сторінка позицій: другий
	// спосіб дізнатись «що я тримаю» розійшовся б із першим рівно на
	// погашених паперах.
	lots, sales, bonds, pays, err := s.portfolio(ctx)
	if err != nil {
		return nil, err
	}
	today := domain.NewDate(now)
	pos, err := domain.Positions(bonds, pays, lots, sales, today)
	if err != nil {
		return nil, err
	}
	for _, p := range pos {
		if p.Qty > 0 {
			add(p.ISIN)
		}
	}
	doc, err := s.buildState(ctx, now)
	if err != nil {
		return nil, err
	}
	sug, err := s.reinvestSuggestions(ctx, now, doc)
	if err != nil {
		return nil, err
	}
	for _, g := range sug {
		if g.Kind == "bond" {
			add(g.ISIN)
		}
	}
	return out, nil
}

// --- ручна ціна ---

type manualQuoteReq struct {
	ISIN   string `json:"isin"`
	Source string `json:"source"`
	Date   string `json:"date"`
	Price  string `json:"price"`
	// Currency порожня = валюта паперу з довідника. Питати її в людини
	// означало б дати їй помилитись там, де відповідь уже відома.
	Currency string `json:"currency,omitempty"`
}

// handleSetManualQuote — вписати ціну руками.
//
// Потрібне там, де джерело не покриває брокера: mono ОВДП продає, а цін не
// публікує ніде. Без цього брокер, у якому лежать гроші, просто випадав би
// з порівняння «у кого дешевше», і застосунок радив би крок за ціною, якої
// в ньому немає.
func (s *Server) handleSetManualQuote(w http.ResponseWriter, r *http.Request) {
	var req manualQuoteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	isin := strings.ToUpper(strings.TrimSpace(req.ISIN))
	b, err := s.st.GetBond(r.Context(), isin)
	if err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	if b == nil {
		writeErr(w, http.StatusBadRequest, badRequestf("паперу %q немає в довіднику", req.ISIN))
		return
	}
	cur := strings.ToUpper(strings.TrimSpace(req.Currency))
	if cur == "" {
		cur = b.Nominal.Currency().Code
	}
	if cur != b.Nominal.Currency().Code {
		writeErr(w, http.StatusBadRequest, badRequestf(
			"%s випущений у %s — ціна в %s стосується іншого паперу",
			isin, b.Nominal.Currency().Code, cur))
		return
	}
	minor, err := domain.ParseDecimalToMinor(req.Price, cur)
	if err != nil {
		writeErr(w, http.StatusBadRequest, badRequestf("ціна: %v", err))
		return
	}
	date := domain.Date(strings.TrimSpace(req.Date))
	if date == "" {
		date = domain.NewDate(time.Now())
	}
	// БРУДНА, як і ціни обходу, і сказати це людині мусить форма: число з
	// додатка брокера — це те, що з тебе спишуть за штуку, разом із НКД.
	// Дві різні за складом ціни в одній таблиці зробили б дохідність
	// несумісною між рядками.
	q := store.Quote{
		ISIN: isin, Source: strings.TrimSpace(req.Source), Date: date,
		PriceMinor: minor, Currency: cur, Origin: store.QuoteOriginManual,
	}
	if err := s.st.SaveQuotes(r.Context(), []store.Quote{q}); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteManualQuote(w http.ResponseWriter, r *http.Request) {
	isin := r.PathValue("isin")
	source := r.URL.Query().Get("source")
	date := domain.Date(r.URL.Query().Get("date"))
	if source == "" || date == "" {
		writeErr(w, http.StatusBadRequest,
			badRequestf("вкажіть продавця й дату: ?source=…&date=YYYY-MM-DD"))
		return
	}
	if err := s.st.DeleteManualQuote(r.Context(), isin, source, date); err != nil {
		writeStoreErr(w, err, http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
