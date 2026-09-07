// Ринкова ціна ОВДП: почім папір у конкретного продавця.
//
// Живе окремо від `bonds` і без FK на нього з того самого доводу, що й
// аукціони: ReplaceDirectory щоранку витирає довідник дощенту. Подробиці —
// у коментарі міграції 0059.
//
// Одна таблиця на два походження. Рядки з origin='finomo' приносить обхід
// джерела, рядки з origin='manual' вписує людина — там, де джерело не
// покриває брокера (mono ОВДП продає, а цін не публікує ніде). Питання до
// них одне — «почім цей папір у цього продавця», — і два сховища
// розійшлися б у свіжості; надійність же різна, і саме тому походження
// їде окремою колонкою й доходить до екрана.

package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	money "github.com/Rhymond/go-money"

	"github.com/ODDsama/oddinvest/internal/domain"
)

// QuotesSweptAtKey — ключ у app_state: коли обхід востаннє покрив УВЕСЬ
// довідник.
//
// Знак ставить джоба (jobs/quotes.go) — і лише після проходу, який дійшов
// до кінця: не обірваного джерелом і не впертого в стелю. Читає його api,
// щоб вирішити, чи можна ховати папери без ціни: «ми питали про всі, і про
// цей ніхто не дав ціни» — твердження, а «ми не питали» — ні.
//
// ЖИВЕ ТУТ, А НЕ В jobs, з того самого доводу, що й finomo.RunResult:
// межа між api й jobs навмисно вільна від типів одне одного, а app_state
// належить сховищу — обидва й так його імпортують.
const QuotesSweptAtKey = "ovdp_quotes_swept_at"

// Походження ціни. Рядком, а не булеаном «ручна»: третій вид джерела
// колись з'явиться, і булеан довелось би міняти на рядок разом з усіма
// його читачами.
const (
	QuoteOriginFinomo = "finomo"
	QuoteOriginManual = "manual"
)

// Quote — ціна одного паперу в одного продавця.
//
// PriceMinor — БРУДНА ціна за одну штуку, тобто разом із НКД: та сума, яку
// віддаєш. Перевірено XIRR'ом на п'яти паперах (довід цілком — у міграції
// 0059 і в unit_cost.go). Додавати до неї накопичений купон НЕ МОЖНА.
type Quote struct {
	ISIN       string      `json:"isin"`
	Source     string      `json:"source"`
	Date       domain.Date `json:"date"`
	PriceMinor int64       `json:"price_minor"`
	Currency   string      `json:"currency"`
	Origin     string      `json:"origin"`
	FetchedAt  string      `json:"fetched_at"`
}

// Money — ціна як гроші. Окремим методом, бо мінорні одиниці без валюти
// читаються як гривня, а папери бувають доларові.
func (q Quote) Money() *money.Money { return money.New(q.PriceMinor, q.Currency) }

// validate — перевірка тут, а не лише в обробнику, і з того самого доводу,
// що в позначок ціни фондів: у таблицю пишуть двоє (обхід і людина), і
// правило «що таке ціна» мусить бути одне.
func (q *Quote) validate() error {
	q.ISIN = strings.ToUpper(strings.TrimSpace(q.ISIN))
	q.Source = strings.TrimSpace(q.Source)
	q.Currency = strings.ToUpper(strings.TrimSpace(q.Currency))
	switch {
	case q.ISIN == "":
		return fmt.Errorf("ціна ОВДП: порожній ISIN")
	case q.Source == "":
		return fmt.Errorf("ціна ОВДП %s: порожній продавець", q.ISIN)
	case q.PriceMinor <= 0:
		return fmt.Errorf("ціна ОВДП %s/%s: ціна має бути > 0", q.ISIN, q.Source)
	case money.GetCurrency(q.Currency) == nil:
		return fmt.Errorf("ціна ОВДП %s/%s: невідома валюта %q", q.ISIN, q.Source, q.Currency)
	}
	if _, err := time.Parse("2006-01-02", string(q.Date)); err != nil {
		return fmt.Errorf("ціна ОВДП %s/%s: дата %q: %w", q.ISIN, q.Source, q.Date, err)
	}
	if q.Origin != QuoteOriginManual {
		q.Origin = QuoteOriginFinomo
	}
	if q.FetchedAt == "" {
		q.FetchedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return nil
}

// SaveQuotes записує ціни. Ідемпотентно за ключем (папір, продавець,
// дата): кнопку можна тиснути скільки завгодно разів на день.
func (s *Store) SaveQuotes(ctx context.Context, qs []Quote) error {
	if len(qs) == 0 {
		return nil
	}
	for i := range qs {
		if err := qs[i].validate(); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // після Commit це no-op, а до нього — саме те, що треба
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO ovdp_quotes
		(isin, source, quote_date, price_minor, currency, origin, fetched_at)
		VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, q := range qs {
		if _, err := stmt.ExecContext(ctx, q.ISIN, q.Source, string(q.Date),
			q.PriceMinor, q.Currency, q.Origin, q.FetchedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteManualQuote прибирає ціну, вписану руками.
//
// Тільки ручну, і це не обережність, а межа відповідальності: рядок обходу
// людина не заводила, і видаляти його з екрана нема сенсу — наступне
// натискання кнопки поверне його назад. Помилку ж у власному числі
// виправити треба, і саме тому КРУД тут повний (CLAUDE.md §2).
func (s *Store) DeleteManualQuote(ctx context.Context, isin, source string, date domain.Date) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM ovdp_quotes
		WHERE isin=? AND source=? AND quote_date=? AND origin=?`,
		strings.ToUpper(strings.TrimSpace(isin)), strings.TrimSpace(source),
		string(date), QuoteOriginManual)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("ручна ціна %s/%s на %s %w", isin, source, date, ErrNotFound)
	}
	return nil
}

// LatestQuotes — НАЙСВІЖІША ціна кожного продавця по кожному з названих
// паперів.
//
// ОДНИМ ЗАПИТОМ на всі папери, а не циклом: рейтинг тримає під дві сотні
// рядків, і запит на кожен був би тим самим N+1, проти якого вже стоять
// доводи в auctions.go. Порожній перелік означає «всі папери» — так читає
// сторінка позицій, якій потрібен цілий зріз.
//
// Порядок: папір, далі найдешевший продавець першим. На ньому тримається
// «у кого дешевше», і дві однакові ціни впорядковуються назвою, щоб
// відповідь не мінялась від запуску до запуску.
func (s *Store) LatestQuotes(ctx context.Context, isins []string) ([]Quote, error) {
	q := `SELECT isin, source, quote_date, price_minor, currency, origin, fetched_at
		FROM ovdp_quotes AS q
		WHERE quote_date = (SELECT MAX(quote_date) FROM ovdp_quotes AS m
		                    WHERE m.isin=q.isin AND m.source=q.source)`
	var args []any
	if len(isins) > 0 {
		q += " AND isin IN (" + placeholders(len(isins)) + ")"
		for _, s := range isins {
			args = append(args, strings.ToUpper(strings.TrimSpace(s)))
		}
	}
	q += " ORDER BY isin, price_minor, source"
	var out []Quote
	err := s.scan(ctx, q, func(scan func(...any) error) error {
		var v Quote
		var date string
		if err := scan(&v.ISIN, &v.Source, &date, &v.PriceMinor,
			&v.Currency, &v.Origin, &v.FetchedAt); err != nil {
			return err
		}
		v.Date = domain.Date(date)
		out = append(out, v)
		return nil
	}, args...)
	return out, err
}

// QuotesFetchedAt — коли ми востаннє ходили по ціни. Штамп для екрана:
// «оновлено сьогодні» мусить бути видно, бо ціни тут не оновлює ніхто, крім
// людини з кнопкою.
func (s *Store) QuotesFetchedAt(ctx context.Context) (string, error) {
	var at *string
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(fetched_at) FROM ovdp_quotes`).Scan(&at)
	if err != nil || at == nil {
		return "", err
	}
	return *at, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
