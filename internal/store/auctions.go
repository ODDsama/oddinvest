// Історія аукціонів Мінфіну — єдине джерело, що каже, скільки ринок
// платить за СТРОК. Довідник знає ставку паперу, але не строк як вимір.
//
// Живе окремо від `bonds` навмисно: ReplaceDirectory щоранку витирає
// довідник дощенту, і історія на ньому стиралась би щодня непомітно.
// Подробиці — у коментарі міграції 0024.

package store

import (
	"context"
	"sort"

	"github.com/ODDsama/oddinvest/internal/domain"
	"github.com/ODDsama/oddinvest/internal/nbu"
)

// AuctionPoint — «під скільки, коли й на який строк». Одна відповідь на
// два різні питання: коли востаннє розміщували ЦЕЙ папір і що ринок
// платить за ЦЕЙ строк.
type AuctionPoint struct {
	Date     domain.Date `json:"date"`
	ISIN     string      `json:"isin"`
	Currency string      `json:"currency"`
	Bucket   string      `json:"bucket"`
	IncomeBP int64       `json:"income_bp"`
	// Days — днів до погашення на день аукціону. Потрібне саме для
	// впорядкування: назви строків сортуються за абеткою неправильно
	// («1.5y» < «1y» < «2y»), а криву читають зліва направо за строком.
	Days int64 `json:"days"`
}

// SaveAuctions записує результати аукціонів. Ідемпотентно за ключем
// (день, папір, номер): повторний бекфіл нічого не дублює, тож його
// можна ганяти скільки завгодно разів, не думаючи про наслідки.
func (s *Store) SaveAuctions(ctx context.Context, as []nbu.Auction) error {
	if len(as) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // після Commit це no-op, а до нього — саме те, що треба
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO ovdp_auctions
		(auction_date, isin, auction_num, currency, bucket, days_to_repay,
		 repay_date, income_bp, min_bp, max_bp, btc_x100, sold_minor)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, a := range as {
		if _, err := stmt.ExecContext(ctx, string(a.Date), a.ISIN, a.Num, a.Currency,
			a.Bucket, a.DaysToRepay, string(a.RepayDate), a.IncomeBP,
			a.MinBP, a.MaxBP, a.BTCx100, a.SoldMinor); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// NewestAuctionDate — найсвіжіший день, який уже є в базі. Порожня дата
// означає «нічого немає». На цьому тримається дешеве опитування: якщо
// найновіший аукціон у НБУ не новіший за цей день, шукати між ними нічого.
func (s *Store) NewestAuctionDate(ctx context.Context) (domain.Date, error) {
	var d *string
	if err := s.db.QueryRowContext(ctx,
		`SELECT MAX(auction_date) FROM ovdp_auctions`).Scan(&d); err != nil {
		return "", err
	}
	if d == nil {
		return "", nil
	}
	return domain.Date(*d), nil
}

// CountAuctionDays — скільки РІЗНИХ днів аукціонів уже зібрано. Саме днів,
// а не рядків: за один день їх буває від одного до чотирьох, тож рядки
// нічого не кажуть про глибину історії, яку ми маємо.
func (s *Store) CountAuctionDays(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT auction_date) FROM ovdp_auctions`).Scan(&n)
	return n, err
}

// scanPoints — спільне вичитування для обох запитів нижче.
func (s *Store) scanPoints(ctx context.Context, q string) ([]AuctionPoint, error) {
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuctionPoint{}
	for rows.Next() {
		var p AuctionPoint
		var d string
		if err := rows.Scan(&p.ISIN, &d, &p.Currency, &p.Bucket, &p.IncomeBP, &p.Days); err != nil {
			return nil, err
		}
		p.Date = domain.Date(d)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Обидва запити нижче спираються на задокументовану особливість SQLite:
// при агрегації MAX() решта колонок беруться З ТОГО САМОГО РЯДКА, на
// якому максимум і досягнуто. Це не загальний SQL, і саме тому сказано
// вголос — на іншій СУБД тут знадобиться віконна функція.
//
// Один запит, а не по одному на папір: інакше помічник реінвесту робив
// би 187 звернень до бази на кожне відкриття «Огляду».

// LastAuctionByISIN — коли й під скільки востаннє розміщували кожен папір.
func (s *Store) LastAuctionByISIN(ctx context.Context) (map[string]AuctionPoint, error) {
	pts, err := s.scanPoints(ctx, `SELECT isin, MAX(auction_date), currency, bucket,
		income_bp, days_to_repay FROM ovdp_auctions GROUP BY isin`)
	if err != nil {
		return nil, err
	}
	out := make(map[string]AuctionPoint, len(pts))
	for _, p := range pts {
		out[p.ISIN] = p
	}
	return out, nil
}

// AuctionLatestByBucket — остання дохідність розміщення за кожною парою
// (валюта, строк). Це і є крива первинного ринку на сьогодні.
//
// Впорядкування в Go, а не в SQL: ORDER BY по колонці, якої немає в
// GROUP BY, у агрегатному запиті бере значення з невизначеного рядка, і
// покладатись на це поруч із уже задекларованою особливістю MAX() було б
// однією хиткою підставою забагато.
func (s *Store) AuctionLatestByBucket(ctx context.Context) ([]AuctionPoint, error) {
	pts, err := s.scanPoints(ctx, `SELECT isin, MAX(auction_date), currency, bucket,
		income_bp, days_to_repay FROM ovdp_auctions WHERE bucket <> ''
		GROUP BY currency, bucket`)
	if err != nil {
		return nil, err
	}
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].Currency != pts[j].Currency {
			return pts[i].Currency < pts[j].Currency
		}
		return pts[i].Days < pts[j].Days
	})
	return pts, nil
}
